package securityworkorder

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestFindingImportCandidatesLifecycle(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset finding import candidate fixtures: %v", err)
	}

	var groupID, organizationID, otherOrganizationID, operatorID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('FINDING_IMPORT_GROUP', '漏洞导入测试集团', 'group') RETURNING id::text
	`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'FINDING_IMPORT_SUB', '漏洞导入测试子公司', 'subsidiary') RETURNING id::text
	`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'FINDING_IMPORT_OTHER', '其他测试子公司', 'subsidiary') RETURNING id::text
	`, groupID).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("insert other organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ('finding_import_operator', 'not-used', '漏洞导入测试运营人员') RETURNING id::text
	`).Scan(&operatorID); err != nil {
		t.Fatalf("insert operator: %v", err)
	}

	insertAsset := func(assetType, assetValue, normalizedValue, assetName, ownerOrganizationID string) string {
		t.Helper()
		var assetID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO assets (
				asset_type, asset_value, normalized_value, asset_name, environment,
				network_scope, status, importance, source, created_by, updated_by
			) VALUES ($1,$2,$3,$4,'production','public','active','normal','manual',$5::uuid,$5::uuid)
			RETURNING id::text
		`, assetType, assetValue, normalizedValue, assetName, operatorID).Scan(&assetID); err != nil {
			t.Fatalf("insert asset %s: %v", assetValue, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary)
			VALUES ($1::uuid,$2::uuid,'owner',true)
		`, assetID, ownerOrganizationID); err != nil {
			t.Fatalf("assign asset %s: %v", assetValue, err)
		}
		return assetID
	}
	domainAssetID := insertAsset("domain", "example.com", "example.com", "示例域名", organizationID)
	ipAssetID := insertAsset("ip", "192.168.1.10", "192.168.1.10", "示例主机", organizationID)
	otherAssetID := insertAsset("ip", "203.0.113.50", "203.0.113.50", "批次外主机", otherOrganizationID)

	insertBatch := func(code, name string) string {
		t.Helper()
		var batchID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO security_test_batches (
				batch_code, batch_name, test_type, status, provider, authorization_note,
				authorized_by, authorized_at, start_at, end_at, created_by, updated_by
			) VALUES ($1,$2,'penetration','authorized','测试服务商','已取得测试授权',
			          $3::uuid,now(),now(),now()+interval '7 days',$3::uuid,$3::uuid)
			RETURNING id::text
		`, code, name, operatorID).Scan(&batchID); err != nil {
			t.Fatalf("insert batch %s: %v", code, err)
		}
		return batchID
	}
	batchID := insertBatch("STB-FINDING-IMPORT", "漏洞导入待关联测试")
	otherBatchID := insertBatch("STB-FINDING-OTHER", "其他授权批次")

	insertScope := func(batchID, assetID, assetType, assetValue, assetName, ownerOrganizationID, organizationCode, organizationName string) string {
		t.Helper()
		var scopeID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO security_test_scope_assets (
				batch_id, asset_id, asset_type_snapshot, asset_value_snapshot, asset_name_snapshot,
				organization_id, organization_code_snapshot, organization_name_snapshot,
				network_scope_snapshot, environment_snapshot, importance_snapshot, included_by
			) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6::uuid,$7,$8,'public','production','normal',$9::uuid)
			RETURNING id::text
		`, batchID, assetID, assetType, assetValue, assetName, ownerOrganizationID, organizationCode, organizationName, operatorID).Scan(&scopeID); err != nil {
			t.Fatalf("insert scope %s: %v", assetValue, err)
		}
		return scopeID
	}
	_ = insertScope(batchID, domainAssetID, "domain", "example.com", "示例域名", organizationID, "FINDING_IMPORT_SUB", "漏洞导入测试子公司")
	ipScopeID := insertScope(batchID, ipAssetID, "ip", "192.168.1.10", "示例主机", organizationID, "FINDING_IMPORT_SUB", "漏洞导入测试子公司")
	otherScopeID := insertScope(otherBatchID, otherAssetID, "ip", "203.0.113.50", "批次外主机", otherOrganizationID, "FINDING_IMPORT_OTHER", "其他测试子公司")

	principal := auth.Principal{
		UserID: operatorID, Username: "finding_import_operator", DisplayName: "漏洞导入测试运营人员",
		Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding},
	}
	service := NewService(pool, t.TempDir())
	csv := strings.Join([]string{
		"资产类型,资产值,漏洞标题,严重等级,漏洞描述,发现时间",
		"domain,EXAMPLE.COM.,域名精确匹配漏洞,高危,应通过标准化值直接入库,2026-07-29 10:00:00",
		// The port is evidence about the host, not part of its identity, so this
		// resolves onto the existing 192.168.1.10 asset instead of becoming a
		// second asset or a manual association.
		"ip,192.168.1.10:443,端口标识同主机漏洞,中危,应归一到该主机资产,2026-07-29 10:05:00",
		// An address the batch scope never covered still needs a human decision.
		"ip,198.51.100.77,范围外待关联漏洞,中危,应进入人工关联区,2026-07-29 10:06:00",
	}, "\n")
	result, err := service.ImportFindings(ctx, principal, batchID, "scanner.csv", strings.NewReader(csv), "finding-import-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("ImportFindings() error = %v", err)
	}
	if result.Status != "partial" || result.TotalCount != 3 || result.SuccessCount != 2 || result.PendingCount != 1 || result.FailureCount != 0 {
		t.Fatalf("ImportFindings() result = %+v, want partial/success=2/pending=1", result)
	}

	page, _ := pagination.Normalize(1, 20)
	candidates, err := service.SearchFindingImportCandidates(ctx, principal, FindingImportCandidateFilter{TestBatchID: batchID, Status: "pending", Page: page})
	if err != nil {
		t.Fatalf("SearchFindingImportCandidates() error = %v", err)
	}
	if candidates.Total != 1 || len(candidates.Items) != 1 {
		t.Fatalf("pending candidates = total %d items %d, want 1", candidates.Total, len(candidates.Items))
	}
	candidate := candidates.Items[0]
	if candidate.SuggestedScopeAssetID != "" || candidate.MatchMethod == "endpoint_host" {
		t.Fatalf("candidate suggestion = scope %q method %q, want no scope suggestion for an out-of-scope address", candidate.SuggestedScopeAssetID, candidate.MatchMethod)
	}

	// The endpoint row resolved onto the scoped host without a manual decision.
	var importedPortFinding int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM security_findings f
		WHERE f.test_batch_id=$1::uuid AND f.asset_id=$2::uuid AND f.title='端口标识同主机漏洞'
	`, batchID, ipAssetID).Scan(&importedPortFinding); err != nil {
		t.Fatalf("count endpoint finding: %v", err)
	}
	if importedPortFinding != 1 {
		t.Fatalf("endpoint finding on scoped host = %d, want 1", importedPortFinding)
	}

	options, err := service.ListFindingImportScopeOptions(ctx, principal, candidate.ID)
	if err != nil {
		t.Fatalf("ListFindingImportScopeOptions() error = %v", err)
	}
	if len(options) != 2 {
		t.Fatalf("scope options count = %d, want only 2 assets from current batch", len(options))
	}
	for _, option := range options {
		if option.ID == otherScopeID || option.AssetID == otherAssetID {
			t.Fatalf("scope options leaked asset from another batch: %+v", option)
		}
	}

	forbidden := auth.Principal{UserID: operatorID, Roles: []string{auth.RoleSecurityOperator}}
	if _, err := service.SearchFindingImportCandidates(ctx, forbidden, FindingImportCandidateFilter{Page: page}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("SearchFindingImportCandidates(forbidden) error = %v, want ErrForbidden", err)
	}
	if _, err := service.LinkFindingImportCandidate(ctx, principal, candidate.ID, FindingImportCandidateLinkInput{ScopeAssetID: otherScopeID}, "finding-import-invalid-scope", "127.0.0.1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LinkFindingImportCandidate(other batch scope) error = %v, want ErrNotFound", err)
	}

	linked, err := service.LinkFindingImportCandidate(ctx, principal, candidate.ID, FindingImportCandidateLinkInput{
		ScopeAssetID: ipScopeID,
		ReviewNote:   "确认该地址属于本批次测试范围",
	}, "finding-import-link", "127.0.0.1")
	if err != nil {
		t.Fatalf("LinkFindingImportCandidate() error = %v", err)
	}
	if linked.Status != "imported" || linked.ImportedFindingID == "" || linked.MatchedScopeAssetID != ipScopeID {
		t.Fatalf("linked candidate = %+v, want imported finding on selected scope", linked)
	}
	var pendingCount, findingCount, notificationCount int
	if err := pool.QueryRow(ctx, `SELECT pending_count FROM security_finding_imports WHERE id=$1::uuid`, result.ID).Scan(&pendingCount); err != nil {
		t.Fatalf("load pending count: %v", err)
	}
	if pendingCount != 0 {
		t.Fatalf("pending_count after link = %d, want 0", pendingCount)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE test_batch_id=$1::uuid`, batchID).Scan(&findingCount); err != nil {
		t.Fatalf("count imported findings: %v", err)
	}
	if findingCount != 3 {
		t.Fatalf("finding count after automatic matches and manual link = %d, want 3", findingCount)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM notification_events
		WHERE event_type='security_finding.created_on_asset' AND target_organization_id=$1::uuid
	`, organizationID).Scan(&notificationCount); err != nil {
		t.Fatalf("count finding notifications: %v", err)
	}
	// Two findings landed on assets: the endpoint row resolved onto the scoped
	// host and the out-of-scope address was linked by hand. The domain row is
	// keyed on a business system instead and notifies through a different event.
	if notificationCount != 2 {
		t.Fatalf("notification event count = %d, want 2", notificationCount)
	}

	// The same issue re-reported for another port on the same host. It resolves to
	// the same asset and title, so no second finding may be created.
	duplicateCSV := strings.Join([]string{
		"资产类型,资产值,漏洞标题,严重等级,漏洞描述",
		"ip,192.168.1.10:8443,端口标识待关联漏洞,中危,重复漏洞应由人工关联阶段识别",
	}, "\n")
	duplicateImport, err := service.ImportFindings(ctx, principal, batchID, "duplicate.csv", strings.NewReader(duplicateCSV), "finding-import-duplicate", "127.0.0.1")
	if err != nil {
		t.Fatalf("ImportFindings(duplicate candidate) error = %v", err)
	}
	var duplicateFindingCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM security_findings
		WHERE test_batch_id=$1::uuid AND asset_id=$2::uuid AND lower(btrim(title))=lower('端口标识待关联漏洞')
	`, batchID, ipAssetID).Scan(&duplicateFindingCount); err != nil {
		t.Fatalf("count duplicate findings: %v", err)
	}
	if duplicateFindingCount != 1 {
		t.Fatalf("findings for one host+title = %d, want 1 even across two ports", duplicateFindingCount)
	}
	// The row matched an existing issue for the same host and title at import
	// time, so it never became a manual-association row and nothing is left for
	// a reviewer to decide.
	var duplicateStillPending int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM security_finding_import_candidates c
		WHERE c.finding_import_id=$1::uuid AND c.status='pending'
	`, duplicateImport.ID).Scan(&duplicateStillPending); err != nil {
		t.Fatalf("count duplicate import statuses: %v", err)
	}
	if duplicateStillPending != 0 {
		t.Fatalf("duplicate import left %d rows for manual association, want 0", duplicateStillPending)
	}
	if duplicateImport.SuccessCount != 1 || duplicateImport.PendingCount != 0 {
		t.Fatalf("duplicate import = success %d pending %d, want 1/0", duplicateImport.SuccessCount, duplicateImport.PendingCount)
	}
	if err := pool.QueryRow(ctx, `SELECT pending_count FROM security_finding_imports WHERE id=$1::uuid`, duplicateImport.ID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatalf("duplicate import pending_count = %d, %v; want 0", pendingCount, err)
	}

	ignoreCSV := strings.Join([]string{
		"资产类型,资产值,漏洞标题,严重等级,漏洞描述",
		"ip,198.51.100.90,无对应资产漏洞,低危,无法确认资产归属时可人工忽略",
	}, "\n")
	ignoreImport, err := service.ImportFindings(ctx, principal, batchID, "ignore.csv", strings.NewReader(ignoreCSV), "finding-import-ignore", "127.0.0.1")
	if err != nil {
		t.Fatalf("ImportFindings(ignore candidate) error = %v", err)
	}
	ignoreCandidates, err := service.SearchFindingImportCandidates(ctx, principal, FindingImportCandidateFilter{TestBatchID: batchID, Status: "pending", Keyword: "198.51.100.90", Page: page})
	if err != nil || len(ignoreCandidates.Items) != 1 {
		t.Fatalf("ignore pending candidate search = %+v, %v", ignoreCandidates, err)
	}
	ignored, err := service.IgnoreFindingImportCandidate(ctx, principal, ignoreCandidates.Items[0].ID, FindingImportCandidateReviewInput{ReviewNote: "扫描器误报且不属于本批次授权资产"}, "finding-import-ignore-review", "127.0.0.1")
	if err != nil {
		t.Fatalf("IgnoreFindingImportCandidate() error = %v", err)
	}
	if ignored.Status != "ignored" || ignored.ReviewNote == "" || ignored.ReviewedAt == nil {
		t.Fatalf("ignored candidate = %+v, want ignored with review metadata", ignored)
	}
	if err := pool.QueryRow(ctx, `SELECT pending_count FROM security_finding_imports WHERE id=$1::uuid`, ignoreImport.ID).Scan(&pendingCount); err != nil || pendingCount != 0 {
		t.Fatalf("ignored import pending_count = %d, %v; want 0", pendingCount, err)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE resource_type='security_finding_import_candidate'
		  AND action IN ('security_finding_import_candidate.imported','security_finding_import_candidate.duplicate','security_finding_import_candidate.ignored')
	`).Scan(&auditCount); err != nil {
		t.Fatalf("count candidate audits: %v", err)
	}
	// Only the two rows that needed a human decision produced review actions:
	// the linked out-of-scope address and the ignored scan artefact.
	if auditCount != 2 {
		t.Fatalf("candidate audit count = %d, want 2", auditCount)
	}

	if result.ID == duplicateImport.ID || duplicateImport.ID == ignoreImport.ID {
		t.Fatalf("finding import ids should be unique: %s %s %s", result.ID, duplicateImport.ID, ignoreImport.ID)
	}
	if time.Since(linked.UpdatedAt) > time.Minute {
		t.Fatalf("linked candidate updated_at appears stale: %s", linked.UpdatedAt)
	}
}
