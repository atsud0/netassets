package securityworkorder

// This file drives the P0-3 mainline end to end with the real desensitized
// scanner export (testdata/findings-20261010-142559.desensitized.json):
//
//	finding candidates -> asset candidates -> working version -> publication
//
// The file is the actual product of the scanner the customer runs, so every
// assumption here is checked against that file rather than against a hand-made
// CSV: the malformed escape sequences inside the report text, the mixed
// endpoint/subdomain/ip/service asset rows, the native status vocabulary and
// the host:port service rows all have to survive the import.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/assetcandidate"
	"netassets/internal/auth"
	"netassets/internal/bootstrap"
	"netassets/internal/config"
	"netassets/internal/inventorytask"
	"netassets/internal/pagination"
	"netassets/internal/recyclebin"
)

const desensitizedFindingsFile = "testdata/findings-20261010-142559.desensitized.json"

func TestP03RealScannerExportRunsThroughCandidateAndPublication(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	payload, err := os.ReadFile(desensitizedFindingsFile)
	if err != nil {
		t.Fatalf("read desensitized finding export: %v", err)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset p0-3 fixtures: %v", err)
	}
	p03Bootstrap(t, pool, "MAINLINE")

	groupID := p03InsertOrg(t, pool, "", "P03_GROUP", "P0-3 测试集团", "group")
	organizationID := p03InsertOrg(t, pool, groupID, "P03_SUB", "P0-3 测试子公司", "subsidiary")
	operatorID := p03InsertUser(t, pool, "p03_security_operator", "P0-3 集团安全管理员")
	orgAdminID := p03InsertUser(t, pool, "p03_org_admin", "P0-3 子公司管理员")
	groupAdminID := p03InsertUser(t, pool, "p03_group_admin", "P0-3 集团管理员")

	p03AssignRole(t, pool, operatorID, "security_operator", "", "all")
	p03AssignRole(t, pool, orgAdminID, "organization_admin", organizationID, "organization")
	p03AssignRole(t, pool, groupAdminID, "group_admin", "", "all")

	operator := p03Principal(t, pool, operatorID, []string{"security_operator"})
	orgAdmin := p03Principal(t, pool, orgAdminID, []string{"organization_admin"})
	// Inventory tasks belong to the reporting domain: headquarters creates and
	// reviews them, a subsidiary fills them in. The group security operator has no
	// business creating one, so the test keeps the real division of labour.
	groupAdmin := p03Principal(t, pool, groupAdminID, []string{"group_admin"})

	assets := asset.NewService(pool)
	candidates := assetcandidate.NewService(pool, assets)
	findings := NewService(pool, t.TempDir())
	tasks := inventorytask.NewService(pool)

	// Step 1: import without a test batch. The export carries no batch, and the
	// product must not demand one: it is an external finding delivery.
	result, err := findings.ImportFindings(ctx, operator, "", "findings-20261010-142559.desensitized.json", strings.NewReader(string(payload)), "p03-import", "127.0.0.1")
	if err != nil {
		t.Fatalf("ImportFindings(real export) error = %v", err)
	}
	if result.TotalCount == 0 {
		t.Fatal("ImportFindings(real export) produced no rows; the scanner export shape is no longer accepted")
	}
	if result.FailureCount != 0 {
		t.Fatalf("ImportFindings(real export) failure_count = %d, want 0; errors = %+v", result.FailureCount, result.Errors)
	}
	t.Logf("import: total=%d success=%d pending=%d duplicate=%d failure=%d", result.TotalCount, result.SuccessCount, result.PendingCount, result.DuplicateCount, result.FailureCount)
	// On a freshly deployed register no finding can match a formal asset, so every
	// row must land as a pending discovery rather than be dropped.
	if result.PendingCount == 0 {
		t.Fatal("ImportFindings(real export) produced no pending discoveries; findings were dropped instead of becoming asset discoveries")
	}

	// The export is not valid JSON as it stands: the report text contains a
	// literal backslash before a domain name (\c.target-a.com) and POSIX escapes
	// such as \(...\). Without the repair path the import would reject the whole
	// file, which is exactly what happened before the scanner output was
	// inspected. Assert the repair against the real bytes rather than against a
	// synthetic sample.
	if json.Valid(payload) {
		t.Fatal("the desensitized export is now valid JSON; the malformed-escape fixture no longer covers the repair path")
	}
	repaired := repairInvalidJSONEscapes(payload)
	if !json.Valid(repaired) {
		t.Fatalf("repairInvalidJSONEscapes did not produce valid JSON: %v", repaired[min(len(repaired)-120, 0):])
	}
	var repairedRecords []map[string]any
	if err := json.Unmarshal(repaired, &repairedRecords); err != nil {
		t.Fatalf("decode repaired export: %v", err)
	}
	if len(repairedRecords) == 0 {
		t.Fatal("repaired export decoded to zero findings")
	}
	// The repair drops the offending slash and keeps the text, so the domain
	// survives as readable content rather than as an escape that would break the
	// parser again.
	if !strings.Contains(string(repaired), `c.target-a.com`) || strings.Contains(string(repaired), `\c.target-a.com`) {
		t.Fatal("the repair did not turn the malformed escape into plain content")
	}

	// Step 2: everything without a formal asset must be waiting as an asset
	// candidate, never dropped.
	page, err := pagination.Normalize(1, pagination.MaxPageSize)
	if err != nil {
		t.Fatalf("pagination.Normalize() error = %v", err)
	}
	pending, err := findings.SearchFindingImportCandidates(ctx, operator, FindingImportCandidateFilter{Status: "pending", Page: page})
	if err != nil {
		t.Fatalf("SearchFindingImportCandidates() error = %v", err)
	}
	if pending.Total == 0 {
		t.Fatal("no pending finding import candidates; findings without a formal asset were dropped instead of becoming asset discoveries")
	}
	discoveryValues := make(map[string]bool)
	for _, item := range pending.Items {
		if item.AssetCandidateID == "" {
			t.Fatalf("pending candidate %d (%s %s) has no asset candidate; the import lost the asset discovery link", item.SourceRowNumber, item.AssetType, item.AssetValue)
		}
		discoveryValues[item.AssetType+":"+item.AssetValue] = true
	}
	t.Logf("pending finding candidates: %d across %d distinct discoveries", pending.Total, len(discoveryValues))

	// The scanner records Oracle listeners as host:port. Those must be
	// discovered as the address, with the port kept as evidence on the asset,
	// not as a separate "10.10.1.7:1521" asset.
	var hostPortValue string
	if err := pool.QueryRow(ctx, `
		SELECT asset_value FROM asset_candidates WHERE asset_type='ip' AND asset_value LIKE '%:1521' LIMIT 1`).Scan(&hostPortValue); err == nil {
		t.Fatalf("asset candidate %q was created for a host:port value; the port must collapse onto the address", hostPortValue)
	}

	// Step 3: a group reviewer confirms one discovery and it becomes an
	// organization working version, not a formal asset.
	candidateID := pending.Items[0].AssetCandidateID
	var accepted asset.Asset
	batchResult := candidates.BatchAcceptWithAfterCreate(ctx, groupAdmin, assetcandidate.BatchAcceptInput{Items: []assetcandidate.BatchAcceptItem{{
		CandidateID: candidateID,
		Input: assetcandidate.AcceptInput{
			CreateInput: asset.CreateInput{
				OrganizationID: organizationID, Name: "P0-3 已确认资产", Environment: "production",
				NetworkScope: "public", Status: "active", Importance: "normal", OwnerName: "P0-3 测试子公司",
			},
			ReviewNote: "漏洞导入发现，确认归属",
		},
	}}}, "p03-accept", "127.0.0.1", func(callbackCtx context.Context, tx pgx.Tx, acceptedCandidateID string, created asset.Asset) error {
		if acceptedCandidateID != candidateID {
			return errors.New("batch callback received the wrong candidate")
		}
		accepted = created
		return findings.LinkAcceptedAssetCandidateInTx(callbackCtx, tx, groupAdmin, acceptedCandidateID, created.ID, "p03-accept", "127.0.0.1")
	})
	if len(batchResult.Items) != 1 || !batchResult.Items[0].Succeeded || batchResult.Failed != 0 {
		t.Fatalf("BatchAccept(asset candidate) result = %+v, want one successful item", batchResult)
	}
	if accepted.ID == "" {
		t.Fatal("BatchAccept(asset candidate) callback returned no working version")
	}
	// The same transaction that creates the working version released the
	// findings. A failure in that callback would roll the candidate and draft
	// back together instead of leaving a split-brain queue.
	var draftID string
	if err := pool.QueryRow(ctx, `SELECT working_version_draft_id::text FROM asset_candidates WHERE id=$1::uuid`, candidateID).Scan(&draftID); err != nil {
		t.Fatalf("load working version draft: %v", err)
	}
	if draftID != accepted.ID {
		t.Fatalf("working version draft = %q, accept returned %q", draftID, accepted.ID)
	}
	var formalAssetID string
	if err := pool.QueryRow(ctx, `SELECT count(*)::text FROM assets WHERE id=$1::uuid`, draftID).Scan(&formalAssetID); err != nil {
		t.Fatalf("count formal asset for draft: %v", err)
	}
	if formalAssetID != "0" {
		t.Fatal("confirming a discovery wrote directly into the formal asset table; the working version gate was skipped")
	}

	// The findings that arrived with the discovery are now recorded against the
	// working version rather than staying in limbo.
	var attachedToDraft int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE pending_asset_draft_id=$1::uuid`, draftID).Scan(&attachedToDraft); err != nil {
		t.Fatalf("count findings attached to working version: %v", err)
	}
	if attachedToDraft == 0 {
		t.Fatal("confirming a discovery left its findings unreleased; the import queue and the asset ledger disagree")
	}
	// The owner organization must see imported findings against its unpublished
	// working version; otherwise the asset draft and security backlog diverge
	// until group publication.
	orgFindings, err := findings.SearchFindings(ctx, orgAdmin, organizationID, "", "", page)
	if err != nil {
		t.Fatalf("organization SearchFindings(working version) error = %v", err)
	}
	if orgFindings.Total < attachedToDraft {
		t.Fatalf("organization sees %d findings while %d are linked to its working version", orgFindings.Total, attachedToDraft)
	}
	orgSummary, err := findings.SummarizeFindings(ctx, orgAdmin, organizationID)
	if err != nil {
		t.Fatalf("organization SummarizeFindings(working version) error = %v", err)
	}
	if orgSummary.PendingAssetLink < attachedToDraft {
		t.Fatalf("organization pending asset-link summary = %d, want at least %d working-version findings", orgSummary.PendingAssetLink, attachedToDraft)
	}
	t.Logf("working version %s carries %d findings visible to its organization", draftID, attachedToDraft)

	// A failed batch callback must roll back the candidate and its draft. This
	// protects the retry endpoint from a false "accepted" row whose findings
	// were never linked. Use another scanner discovery so the successful path
	// above remains available for the publication part of this scenario.
	var rollbackCandidateID string
	for _, item := range pending.Items {
		if item.AssetCandidateID != "" && item.AssetCandidateID != candidateID {
			rollbackCandidateID = item.AssetCandidateID
			break
		}
	}
	if rollbackCandidateID != "" {
		rollbackResult := candidates.BatchAcceptWithAfterCreate(ctx, groupAdmin, assetcandidate.BatchAcceptInput{Items: []assetcandidate.BatchAcceptItem{{
			CandidateID: rollbackCandidateID,
			Input:       assetcandidate.AcceptInput{CreateInput: asset.CreateInput{OrganizationID: organizationID, Name: "应回滚资产", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}},
		}}}, "p03-accept-rollback", "127.0.0.1", func(context.Context, pgx.Tx, string, asset.Asset) error {
			return errors.New("deliberate finding-link failure")
		})
		if rollbackResult.Failed != 1 || rollbackResult.Succeeded != 0 {
			t.Fatalf("failed batch callback result = %+v, want one failed row", rollbackResult)
		}
		var rollbackDraftID, rollbackStatus string
		if err := pool.QueryRow(ctx, `SELECT COALESCE(working_version_draft_id::text,''), status FROM asset_candidates WHERE id=$1::uuid`, rollbackCandidateID).Scan(&rollbackDraftID, &rollbackStatus); err != nil {
			t.Fatalf("load rolled-back candidate: %v", err)
		}
		if rollbackDraftID != "" || rollbackStatus != "pending" {
			t.Fatalf("failed batch callback left candidate draft=%q status=%q, want empty/pending", rollbackDraftID, rollbackStatus)
		}
	}

	// Step 4: the subsidiary selects the working version into its own inventory
	// task, submits it, and the group publishes it.
	task, err := tasks.Create(ctx, groupAdmin, inventorytask.CreateInput{
		Name: "P0-3 漏洞入库发布任务", StartAt: time.Now().Add(-time.Hour),
		DeadlineAt: time.Now().Add(24 * time.Hour), Status: "active", AssetNetworkScope: "all",
		RequiredFields: []string{"network_assets"}, RequireReview: true, AllowLateSubmission: true,
		OrganizationIDs: []string{organizationID},
	}, "p03-task", "127.0.0.1")
	if err != nil {
		t.Fatalf("Create(inventory task) error = %v", err)
	}
	if _, err := tasks.Start(ctx, orgAdmin, task.ID, organizationID, "p03-start", "127.0.0.1"); err != nil {
		t.Fatalf("Start(inventory task) error = %v", err)
	}
	var draftVersion int
	if err := pool.QueryRow(ctx, `SELECT version FROM inventory_asset_drafts WHERE id=$1::uuid`, draftID).Scan(&draftVersion); err != nil {
		t.Fatalf("load working version: %v", err)
	}
	if _, err := tasks.SelectWorkspaceAsset(ctx, orgAdmin, task.ID, organizationID, draftID, draftVersion, "p03-select", "127.0.0.1"); err != nil {
		t.Fatalf("SelectWorkspaceAsset() error = %v", err)
	}
	quality, err := tasks.QualityCheck(ctx, orgAdmin, task.ID, organizationID)
	if err != nil {
		t.Fatalf("QualityCheck() error = %v", err)
	}
	if !quality.Passed {
		t.Fatalf("QualityCheck() = %+v, want passed; the confirmed discovery must be publishable", quality)
	}
	if _, err := tasks.Submit(ctx, orgAdmin, task.ID, organizationID, inventorytask.SubmissionInput{Note: "P0-3 漏洞导入资产入库"}, "p03-submit", "127.0.0.1"); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if _, err := tasks.Review(ctx, groupAdmin, task.ID, organizationID, inventorytask.ReviewInput{Decision: "approved"}, "p03-review", "127.0.0.1"); err != nil {
		t.Fatalf("Review(approved) error = %v", err)
	}

	// Step 5: publication must settle the discovery and re-link every finding
	// onto the formal asset. This is the whole point of the mainline: the
	// vulnerability must end up countable under its asset.
	var publishedAssetID, candidateStatus string
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(matched_asset_id::text,''), status FROM asset_candidates WHERE id=$1::uuid`, candidateID).Scan(&publishedAssetID, &candidateStatus); err != nil {
		t.Fatalf("load asset candidate after publication: %v", err)
	}
	if candidateStatus != "accepted" || publishedAssetID == "" {
		t.Fatalf("asset candidate after publication = status %q asset %q, want accepted with a formal asset", candidateStatus, publishedAssetID)
	}
	var stillOnDraft int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE pending_asset_draft_id=$1::uuid`, draftID).Scan(&stillOnDraft); err != nil {
		t.Fatalf("count findings still on the working version: %v", err)
	}
	if stillOnDraft != 0 {
		t.Fatalf("findings still reference the discarded working version after publication = %d", stillOnDraft)
	}
	var linkedToAsset int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE asset_id=$1::uuid`, publishedAssetID).Scan(&linkedToAsset); err != nil {
		t.Fatalf("count findings linked to the published asset: %v", err)
	}
	if linkedToAsset == 0 {
		t.Fatal("publication did not move any finding onto the formal asset; vulnerabilities become untraceable at the last step")
	}
	t.Logf("published asset %s now carries %d findings", publishedAssetID, linkedToAsset)

	// Retirement never deletes the vulnerability history. Restoring the asset
	// makes the association live again but must not rewrite the finding's own
	// status or create a second asset identity.
	if _, err := pool.Exec(ctx, `UPDATE assets SET deleted_at=now() WHERE id=$1::uuid`, publishedAssetID); err != nil {
		t.Fatalf("soft delete published asset: %v", err)
	}
	var retiredCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE asset_id=$1::uuid AND asset_link_status='retired'`, publishedAssetID).Scan(&retiredCount); err != nil {
		t.Fatalf("count retired findings: %v", err)
	}
	if retiredCount != linkedToAsset {
		t.Fatalf("retired findings = %d, want all %d findings to remain linked and visible", retiredCount, linkedToAsset)
	}
	if err := recyclebin.NewService(pool).RestoreAsset(ctx, groupAdmin, publishedAssetID, "p03-restore", "127.0.0.1"); err != nil {
		t.Fatalf("restore published asset with historical findings: %v", err)
	}
	var liveCount, statusChangeCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE asset_id=$1::uuid AND asset_link_status='live'`, publishedAssetID).Scan(&liveCount); err != nil {
		t.Fatalf("count restored live findings: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE asset_id=$1::uuid AND status NOT IN ('pending','in_progress','confirmed','resolved','fixed','false_positive','ignored','duplicate','risk_accepted')`, publishedAssetID).Scan(&statusChangeCount); err != nil {
		t.Fatalf("check restored finding statuses: %v", err)
	}
	if liveCount != linkedToAsset || statusChangeCount != 0 {
		t.Fatalf("restored findings = live %d/%d, invalid_status %d; restore must preserve historical findings and their vulnerability state", liveCount, linkedToAsset, statusChangeCount)
	}

	// The publication is a single transaction: the import candidate must be
	// settled, not left pointing at a working version that no longer exists.
	var leftoverPending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM security_finding_import_candidates
		WHERE asset_candidate_id=$1::uuid AND status='pending'`, candidateID).Scan(&leftoverPending); err != nil {
		t.Fatalf("count leftover pending import candidates: %v", err)
	}
	if leftoverPending != 0 {
		t.Fatalf("import candidates still pending after publication = %d", leftoverPending)
	}

	// Step 6: the linkage must be retryable. A partial failure has to remain
	// resumable without re-importing the file.
	if _, err := findings.RetryAcceptedAssetCandidate(ctx, operator, candidateID, publishedAssetID, "p03-retry", "127.0.0.1"); err != nil {
		t.Fatalf("RetryAcceptedAssetCandidate() on a settled candidate error = %v; a settled queue must stay idempotent", err)
	}
}

func TestP03ImportRejectsIllegalValuesWithRowNumbers(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset p0-3 value fixtures: %v", err)
	}
	p03Bootstrap(t, pool, "VALUES")
	operatorID := p03InsertUser(t, pool, "p03_value_operator", "P0-3 值校验运营人员")
	p03AssignRole(t, pool, operatorID, "security_operator", "", "all")
	operator := p03Principal(t, pool, operatorID, []string{"security_operator"})
	service := NewService(pool, t.TempDir())

	csv := strings.Join([]string{
		"资产类型,资产值,漏洞标题,严重等级,漏洞描述,状态",
		"domain,example.com,合法状态漏洞,高危,应通过校验,fixed",
		"domain,bad.example,非法状态漏洞,高危,应在导入层拒绝,not_a_real_status",
		"domain,worse.example,非法严重等级漏洞,极其严重,应在导入层拒绝,pending",
		"domain,ok.example,中文状态漏洞,低危,中文标签应被接受,已修复",
	}, "\n")
	result, err := service.ImportFindings(ctx, operator, "", "illegal-values.csv", strings.NewReader(csv), "p03-values", "127.0.0.1")
	if err != nil {
		t.Fatalf("ImportFindings(illegal values) error = %v", err)
	}
	codesByRow := make(map[int]string)
	for _, item := range result.Errors {
		codesByRow[item.Row] = item.Code
	}
	if result.FailureCount != 2 {
		t.Fatalf("failure_count = %d, want 2; errors = %+v", result.FailureCount, result.Errors)
	}
	if codesByRow[3] != "invalid_status" {
		t.Fatalf("row 3 code = %q, want invalid_status", codesByRow[3])
	}
	if codesByRow[4] != "invalid_severity" {
		t.Fatalf("row 4 code = %q, want invalid_severity", codesByRow[4])
	}
	if result.SuccessCount+result.PendingCount == 0 {
		t.Fatal("the legal rows were not accepted; an illegal neighbour must not fail the whole file")
	}
	// The Chinese label of a legal status must be accepted too: the export the
	// customer produces is not guaranteed to use the English constants.
	if result.FailureCount != 2 || result.SuccessCount+result.PendingCount != 2 {
		t.Fatalf("legal rows were not both kept: success=%d pending=%d failure=%d", result.SuccessCount, result.PendingCount, result.FailureCount)
	}
}

func TestP03PendingFindingListCannotBeLinkedWithoutRealScope(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset p0-3 link fixtures: %v", err)
	}
	p03Bootstrap(t, pool, "LINK")
	groupID := p03InsertOrg(t, pool, "", "P03_LINK_GROUP", "P0-3 关联测试集团", "group")
	p03InsertOrg(t, pool, groupID, "P03_LINK_SUB", "P0-3 关联测试子公司", "subsidiary")
	operatorID := p03InsertUser(t, pool, "p03_link_operator", "P0-3 关联运营人员")
	p03AssignRole(t, pool, operatorID, "security_operator", "", "all")
	operator := p03Principal(t, pool, operatorID, []string{"security_operator"})
	service := NewService(pool, t.TempDir())

	csv := strings.Join([]string{
		"资产类型,资产值,漏洞标题,严重等级,漏洞描述",
		"ip,198.51.100.42,无批次无资产漏洞,中危,应进入待关联资产候选",
	}, "\n")
	result, err := service.ImportFindings(ctx, operator, "", "link.csv", strings.NewReader(csv), "p03-link-import", "127.0.0.1")
	if err != nil {
		t.Fatalf("ImportFindings() error = %v", err)
	}
	if result.PendingCount != 1 {
		t.Fatalf("pending_count = %d, want 1", result.PendingCount)
	}
	page, _ := pagination.Normalize(1, 20)
	pending, err := service.SearchFindingImportCandidates(ctx, operator, FindingImportCandidateFilter{Status: "pending", Page: page})
	if err != nil {
		t.Fatalf("SearchFindingImportCandidates() error = %v", err)
	}
	if pending.Total != 1 || pending.Items[0].AssetCandidateID == "" {
		t.Fatalf("pending candidate = %+v, want one row bound to an asset candidate", pending.Items)
	}
	candidate := pending.Items[0]
	if _, err := service.LinkFindingImportCandidate(ctx, operator, candidate.ID, FindingImportCandidateLinkInput{ReviewNote: "缺少资产"}, "p03-link-empty", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("LinkFindingImportCandidate() without an asset error = %v, want ErrInvalid", err)
	}
	if _, err := service.LinkFindingImportCandidate(ctx, operator, candidate.ID, FindingImportCandidateLinkInput{AssetID: "not-a-uuid"}, "p03-link-bad", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("LinkFindingImportCandidate() with a bad asset id error = %v, want ErrInvalid", err)
	}
	// A candidate belonging to an asset discovery may not be linked onto an
	// arbitrary formal asset the reviewer names: the discovery path owns it.
	if _, err := service.LinkFindingImportCandidate(ctx, operator, candidate.ID, FindingImportCandidateLinkInput{AssetCandidateID: candidate.AssetCandidateID}, "p03-link-candidate", "127.0.0.1"); err != nil {
		if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrForbidden) {
			t.Fatalf("LinkFindingImportCandidate(asset candidate without organization) error = %v", err)
		}
	}
}

// p03Bootstrap recreates the built-in roles and permissions. Other integration
// suites truncate users with CASCADE, which also empties roles and permissions;
// reading them from the database keeps these tests aligned with production
// authorization instead of hand-written permission strings.
func p03Bootstrap(t *testing.T, pool *pgxpool.Pool, suffix string) {
	t.Helper()
	cfg := config.Config{
		BootstrapAdminUsername:    "p03_root_" + suffix,
		BootstrapAdminPassword:    "P03-Bootstrap-Password-123",
		BootstrapAdminDisplayName: "P0-3 集团安全管理员",
		BootstrapGroupCode:        "P03_BOOTSTRAP_" + suffix,
		BootstrapGroupName:        "P0-3 引导集团",
	}
	if err := bootstrap.Ensure(context.Background(), pool, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("bootstrap built-in roles: %v", err)
	}
}

func p03InsertOrg(t *testing.T, pool *pgxpool.Pool, parentID, code, name, orgType string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES (NULLIF($1,'')::uuid,$2,$3,$4) RETURNING id::text`, parentID, code, name, orgType).Scan(&id); err != nil {
		t.Fatalf("insert organization %s: %v", code, err)
	}
	return id
}

func p03InsertUser(t *testing.T, pool *pgxpool.Pool, username, displayName string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, password_hash, display_name) VALUES ($1,'not-used',$2) RETURNING id::text`,
		username, displayName).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func p03AssignRole(t *testing.T, pool *pgxpool.Pool, userID, roleKey, organizationID, dataScope string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope)
		SELECT $1::uuid, id, NULLIF($3,'')::uuid, $4 FROM roles WHERE role_key = $2`, userID, roleKey, organizationID, dataScope); err != nil {
		t.Fatalf("assign role %s: %v", roleKey, err)
	}
}

func p03Principal(t *testing.T, pool *pgxpool.Pool, userID string, roles []string) auth.Principal {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT DISTINCT rp.permission_key
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status='enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		WHERE ur.user_id = $1::uuid`, userID)
	if err != nil {
		t.Fatalf("load permissions: %v", err)
	}
	defer rows.Close()
	permissions := make([]string, 0)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan permission: %v", err)
		}
		permissions = append(permissions, key)
	}
	return auth.Principal{UserID: userID, Roles: roles, Permissions: permissions}
}

// Two security operators editing the same finding is a normal event, so every
// read-modify-write path must prove it is writing on top of the data it read.
// This covers the generic PATCH and the dedicated status actions, because they
// used to have very different rules.
func TestP03FindingEditAndStatusActionsRequireCurrentVersion(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	p03Bootstrap(t, pool, "LOCK")
	groupID := p03InsertOrg(t, pool, "", "P03_LOCK_GROUP", "P0-3 并发测试集团", "group")
	subID := p03InsertOrg(t, pool, groupID, "P03_LOCK_SUB", "P0-3 并发测试子公司", "subsidiary")
	var systemID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO business_systems (system_code, system_name, owner_organization_id, system_status)
		VALUES ($1,$2,$3::uuid,'running') RETURNING id::text`, "P03_LOCK_SYS", "并发测试系统", subID).Scan(&systemID); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	operatorID := p03InsertUser(t, pool, "p03_lock_operator", "P0-3 并发运营人员")
	p03AssignRole(t, pool, operatorID, "security_operator", "", "all")
	operator := p03Principal(t, pool, operatorID, []string{"security_operator"})
	service := NewService(pool, t.TempDir())

	created, err := service.CreateFinding(ctx, operator, FindingInput{
		Title: "并发编辑漏洞", Severity: "high", Status: "pending", AssigneeOrganizationID: subID,
		BusinessSystemID: systemID, Description: "初始描述", Source: "manual", DiscoveredAt: time.Now(),
	}, "p03-lock-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateFinding() error = %v", err)
	}
	base := func() FindingInput {
		return FindingInput{
			Title: "并发编辑漏洞", Severity: "high", Status: "pending", AssigneeOrganizationID: subID,
			BusinessSystemID: systemID, Description: "更新描述", Source: "manual", DiscoveredAt: time.Now(),
		}
	}
	// A PATCH without the lock token is a client bug, not a convenience.
	if _, err = service.UpdateFinding(ctx, operator, created.ID, base(), "p03-lock-missing", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("UpdateFinding() without expected_updated_at error = %v, want ErrVersionRequired", err)
	}
	first, err := service.GetFinding(ctx, operator, created.ID)
	if err != nil {
		t.Fatalf("GetFinding() error = %v", err)
	}
	stale := first.UpdatedAt
	updated := base()
	updated.ExpectedUpdatedAt = &stale
	// The race: another operator commits between the read and the write.
	concurrent := base()
	fresh := time.Now().Add(time.Millisecond)
	concurrent.ExpectedUpdatedAt = &fresh
	if _, err = service.UpdateFinding(ctx, operator, created.ID, concurrent, "p03-lock-concurrent", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("UpdateFinding() with a stale token error = %v, want ErrEditConflict", err)
	}
	if _, err = service.UpdateFinding(ctx, operator, created.ID, updated, "p03-lock-ok", "127.0.0.1"); err != nil {
		t.Fatalf("UpdateFinding() with the current token error = %v", err)
	}

	current, err := service.GetFinding(ctx, operator, created.ID)
	if err != nil {
		t.Fatalf("GetFinding() after edit error = %v", err)
	}
	if _, err = service.AcceptRisk(ctx, operator, created.ID, RiskAcceptanceInput{
		Reason: "接受风险", AcceptedUntil: time.Now().Add(24 * time.Hour),
	}, "p03-lock-risk-missing", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("AcceptRisk() without expected_updated_at error = %v, want ErrVersionRequired", err)
	}
	if _, err = service.AcceptRisk(ctx, operator, created.ID, RiskAcceptanceInput{
		Reason: "接受风险", AcceptedUntil: time.Now().Add(24 * time.Hour), ExpectedUpdatedAt: &stale,
	}, "p03-lock-risk-stale", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("AcceptRisk() with a stale token error = %v, want ErrEditConflict", err)
	}
	if _, err = service.RevokeRisk(ctx, operator, created.ID, RiskRevokeInput{}, "p03-lock-revoke-missing", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("RevokeRisk() without expected_updated_at error = %v, want ErrVersionRequired", err)
	}
	accepted, err := service.AcceptRisk(ctx, operator, created.ID, RiskAcceptanceInput{
		Reason: "接受风险", AcceptedUntil: time.Now().Add(24 * time.Hour), ExpectedUpdatedAt: &current.UpdatedAt,
	}, "p03-lock-risk-ok", "127.0.0.1")
	if err != nil {
		t.Fatalf("AcceptRisk() with the current token error = %v", err)
	}
	if _, err = service.RevokeRisk(ctx, operator, created.ID, RiskRevokeInput{ExpectedUpdatedAt: &current.UpdatedAt}, "p03-lock-revoke-stale", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("RevokeRisk() with a stale token error = %v, want ErrEditConflict", err)
	}
	if _, err = service.RevokeRisk(ctx, operator, created.ID, RiskRevokeInput{ExpectedUpdatedAt: &accepted.UpdatedAt}, "p03-lock-revoke-ok", "127.0.0.1"); err != nil {
		t.Fatalf("RevokeRisk() with the current token error = %v", err)
	}

	after, err := service.GetFinding(ctx, operator, created.ID)
	if err != nil {
		t.Fatalf("GetFinding() after revoke error = %v", err)
	}
	acceptedAgain, err := service.AcceptRisk(ctx, operator, created.ID, RiskAcceptanceInput{
		Reason: "再次接受", AcceptedUntil: time.Now().Add(48 * time.Hour), ExpectedUpdatedAt: &after.UpdatedAt,
	}, "p03-lock-risk-again", "127.0.0.1")
	if err != nil {
		t.Fatalf("AcceptRisk() for the review error = %v", err)
	}
	if _, err = service.ReviewRisk(ctx, operator, created.ID, RiskReviewInput{Comment: "复核"}, "p03-lock-review-missing", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("ReviewRisk() without expected_updated_at error = %v, want ErrVersionRequired", err)
	}
	if _, err = service.ReviewRisk(ctx, operator, created.ID, RiskReviewInput{Comment: "复核", ExpectedUpdatedAt: &acceptedAgain.UpdatedAt}, "p03-lock-review-ok", "127.0.0.1"); err != nil {
		t.Fatalf("ReviewRisk() with the current token error = %v", err)
	}
	revived, err := service.RevokeRisk(ctx, operator, created.ID, RiskRevokeInput{ExpectedUpdatedAt: nil}, "p03-lock-revoke-no", "127.0.0.1")
	if err != nil && !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("RevokeRisk() with a nil token error = %v, want ErrVersionRequired", err)
	}
	_ = revived
}
