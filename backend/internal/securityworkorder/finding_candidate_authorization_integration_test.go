package securityworkorder

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/assetcandidate"
	"netassets/internal/pagination"
)

// Exercise the organization release path without supplying asset_candidate_id:
// the stored import candidate, not a client-provided ID, owns the authorization.
func TestFindingCandidateOrganizationReleaseAuthorization(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatal(err)
	}
	p03Bootstrap(t, pool, "RELEASE_AUTH")
	groupID := p03InsertOrg(t, pool, "", "RELEASE_GROUP", "关联集团", "group")
	orgID := p03InsertOrg(t, pool, groupID, "RELEASE_A", "子公司A", "subsidiary")
	otherOrgID := p03InsertOrg(t, pool, groupID, "RELEASE_B", "子公司B", "subsidiary")
	adminID := p03InsertUser(t, pool, "release_group_admin", "集团管理员")
	orgAdminID := p03InsertUser(t, pool, "release_org_admin", "组织管理员A")
	otherAdminID := p03InsertUser(t, pool, "release_other_admin", "组织管理员B")
	p03AssignRole(t, pool, adminID, "group_admin", "", "all")
	p03AssignRole(t, pool, orgAdminID, "organization_admin", orgID, "organization")
	p03AssignRole(t, pool, otherAdminID, "organization_admin", otherOrgID, "organization")
	admin := p03Principal(t, pool, adminID, []string{"group_admin"})
	orgAdmin := p03Principal(t, pool, orgAdminID, []string{"organization_admin"})
	otherAdmin := p03Principal(t, pool, otherAdminID, []string{"organization_admin"})
	service := NewService(pool, t.TempDir())
	result, err := service.ImportFindings(ctx, admin, "", "release.csv", strings.NewReader("资产类型,资产值,漏洞标题,严重等级,漏洞描述,状态\nip,198.51.100.42,组织关联漏洞,高危,确认后关联工作版本,confirmed"), "release-import", "127.0.0.1")
	if err != nil || result.PendingCount != 1 {
		t.Fatalf("import = %+v, %v", result, err)
	}
	page, _ := pagination.Normalize(1, 20)
	pending, err := service.SearchFindingImportCandidates(ctx, admin, FindingImportCandidateFilter{Status: "pending", Page: page})
	if err != nil || len(pending.Items) != 1 {
		t.Fatalf("pending = %+v, %v", pending, err)
	}
	candidate := pending.Items[0]
	candidates := assetcandidate.NewService(pool, asset.NewService(pool))
	draft, err := candidates.AcceptWithAfterCreate(ctx, admin, candidate.AssetCandidateID, assetcandidate.AcceptInput{CreateInput: asset.CreateInput{
		OrganizationID: orgID, Name: "子公司A工作资产", Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal", OwnerName: "子公司A",
	}}, "release-accept", "127.0.0.1", func(ctx context.Context, tx pgx.Tx, created asset.Asset) error {
		return service.LinkAcceptedAssetCandidateInTx(ctx, tx, admin, candidate.AssetCandidateID, created.ID, "release-accept", "127.0.0.1")
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	// A later scan arrives before the asset working version is published. It
	// reuses the discovery and must be releasable by its organization, without
	// fabricating a partially committed confirmation in the fixture.
	result, err = service.ImportFindings(ctx, admin, "", "release-later.csv", strings.NewReader("资产类型,资产值,漏洞标题,严重等级,漏洞描述,状态\nip,198.51.100.42,后续扫描漏洞,高危,同一未发布资产的新漏洞,confirmed"), "release-import-later", "127.0.0.1")
	if err != nil || result.PendingCount != 1 {
		t.Fatalf("later import = %+v, %v", result, err)
	}
	pending, err = service.SearchFindingImportCandidates(ctx, admin, FindingImportCandidateFilter{Status: "pending", Page: page})
	if err != nil || len(pending.Items) != 1 || pending.Items[0].AssetCandidateID != candidate.AssetCandidateID {
		t.Fatalf("later pending = %+v, %v", pending, err)
	}
	candidate = pending.Items[0]
	var otherAssetID string
	if err := pool.QueryRow(ctx, `INSERT INTO assets (asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by)
 VALUES ('ip','203.0.113.88','203.0.113.88','子公司B正式资产','production','public','active','normal','manual',$1::uuid,$1::uuid) RETURNING id::text`, adminID).Scan(&otherAssetID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, otherAssetID, otherOrgID); err != nil {
		t.Fatal(err)
	}

	t.Run("unique violation recovery keeps parent transaction usable", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		input := FindingInput{Title: "并发重复关联漏洞", Severity: "high", Status: "confirmed",
			AssigneeOrganizationID: otherOrgID, AssetID: otherAssetID, Source: "import",
			Description: "在预检查之后已存在相同漏洞", DiscoveredAt: time.Now()}
		originalID, _, err := service.createFindingInTx(ctx, tx, admin, input, "release-duplicate-original", "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		// Enter exactly the path taken when another importer committed after
		// the duplicate pre-check. This exercises a real PostgreSQL 23505,
		// rather than mocking an error outside a database transaction.
		id, duplicate, err := service.createImportedAssetFindingInTx(ctx, tx, admin, input, "release-duplicate-race", "127.0.0.1")
		if err != nil || !duplicate || id != originalID {
			t.Fatalf("duplicate recovery = %q/%v/%v, want %q/true/nil", id, duplicate, err, originalID)
		}
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE asset_id=$1::uuid AND title=$2`, otherAssetID, input.Title).Scan(&count); err != nil || count != 1 {
			t.Fatalf("parent transaction count = %d, %v", count, err)
		}
	})

	t.Run("source permission does not authorize foreign target", func(t *testing.T) {
		_, err := service.LinkFindingImportCandidate(ctx, orgAdmin, candidate.ID, FindingImportCandidateLinkInput{AssetID: otherAssetID}, "release-foreign", "127.0.0.1")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("foreign target error = %v, want forbidden", err)
		}
		stored, err := service.GetFindingImportCandidate(ctx, admin, candidate.ID)
		if err != nil || stored.Status != "pending" || stored.ImportedFindingID != "" {
			t.Fatalf("denied action mutated row: %+v, %v", stored, err)
		}
	})
	t.Run("foreign organization cannot release draft findings", func(t *testing.T) {
		_, err := service.LinkFindingImportCandidate(ctx, otherAdmin, candidate.ID, FindingImportCandidateLinkInput{PendingAssetDraftID: draft.ID}, "release-denied", "127.0.0.1")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("foreign organization error = %v, want forbidden", err)
		}
	})
	t.Run("transaction callback enforces organization scope", func(t *testing.T) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		err = service.LinkAcceptedAssetCandidateInTx(ctx, tx, otherAdmin, candidate.AssetCandidateID, draft.ID, "release-tx-denied", "127.0.0.1")
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("foreign callback error = %v, want forbidden", err)
		}
	})
	t.Run("manual release locks discovery before import row", func(t *testing.T) {
		lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		tx, err := pool.Begin(lockCtx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		var holderPID int
		if err := tx.QueryRow(lockCtx, `SELECT pg_backend_pid() FROM asset_candidates WHERE id=$1::uuid FOR UPDATE`, candidate.AssetCandidateID).Scan(&holderPID); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := service.LinkFindingImportCandidate(lockCtx, otherAdmin, candidate.ID, FindingImportCandidateLinkInput{PendingAssetDraftID: draft.ID}, "release-lock-order", "127.0.0.1")
			done <- err
		}()
		// Wait for the real PostgreSQL row-lock wait, not a timing guess.
		for {
			var blocked bool
			if err := pool.QueryRow(lockCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE $1::int=ANY(pg_blocking_pids(pid)))`, holderPID).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case err := <-done:
				t.Fatalf("release returned before waiting for discovery lock: %v", err)
			case <-lockCtx.Done():
				t.Fatal("release never waited for discovery lock")
			case <-time.After(10 * time.Millisecond):
			}
		}
		// Confirmation must remain able to lock the import row while owning
		// the discovery. NOWAIT makes the old inverted lock order fail at once.
		if _, err := tx.Exec(lockCtx, `SELECT id FROM security_finding_import_candidates WHERE id=$1::uuid FOR UPDATE NOWAIT`, candidate.ID); err != nil {
			t.Fatalf("manual release inverted the discovery/import lock order: %v", err)
		}
		if err := tx.Rollback(lockCtx); err != nil {
			t.Fatal(err)
		}
		if err := <-done; !errors.Is(err, ErrForbidden) {
			t.Fatalf("release after unlocking = %v, want forbidden", err)
		}
	})

	t.Run("own organization can release and read result", func(t *testing.T) {
		linked, err := service.LinkFindingImportCandidate(ctx, orgAdmin, candidate.ID, FindingImportCandidateLinkInput{PendingAssetDraftID: draft.ID}, "release-own", "127.0.0.1")
		if err != nil {
			t.Fatalf("release own draft without redundant candidate ID: %v", err)
		}
		if linked.Status != "imported" || linked.ImportedFindingID == "" {
			t.Fatalf("linked = %+v", linked)
		}
		var findingStatus, findingDraftID string
		if err := pool.QueryRow(ctx, `SELECT status,pending_asset_draft_id::text FROM security_findings WHERE id=$1::uuid`, linked.ImportedFindingID).Scan(&findingStatus, &findingDraftID); err != nil {
			t.Fatal(err)
		}
		if findingStatus != "confirmed" || findingDraftID != draft.ID {
			t.Fatalf("finding status/draft = %s/%s", findingStatus, findingDraftID)
		}
		var eventType, eventPermission, eventDraftID string
		if err := pool.QueryRow(ctx, `SELECT event_type,required_permission,details->>'pending_asset_draft_id'
			FROM notification_events WHERE resource_id=$1 AND batch_key='release-own'`, linked.ImportedFindingID).Scan(&eventType, &eventPermission, &eventDraftID); err != nil {
			t.Fatalf("load draft finding notification: %v", err)
		}
		if eventType != "security_finding.created_on_asset" || eventPermission != "asset.manage" || eventDraftID != draft.ID {
			t.Fatalf("draft finding notification type/permission/draft = %s/%s/%s", eventType, eventPermission, eventDraftID)
		}
		if _, err := service.GetFindingImportCandidate(ctx, orgAdmin, candidate.ID); err != nil {
			t.Fatalf("read own result: %v", err)
		}
		if _, err := service.GetFindingImportCandidate(ctx, otherAdmin, candidate.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("foreign read error = %v", err)
		}
		visible, err := service.SearchFindings(ctx, orgAdmin, "", "", "", page)
		if err != nil || visible.Total != 2 || len(visible.Items) != 2 {
			t.Fatalf("own working findings = %+v, %v", visible, err)
		}
		outside, _ := pagination.Normalize(999, 1)
		emptyPage, err := service.SearchFindings(ctx, orgAdmin, "", "", "", outside)
		if err != nil || len(emptyPage.Items) != 0 || emptyPage.Total != visible.Total {
			t.Fatalf("empty-page total fallback = %+v, %v", emptyPage, err)
		}
		foreign, err := service.SearchFindings(ctx, otherAdmin, "", "", "", page)
		if err != nil || foreign.Total != 0 || len(foreign.Items) != 0 {
			t.Fatalf("foreign organization saw draft findings: %+v, %v", foreign, err)
		}
		foreignSummary, err := service.SummarizeFindings(ctx, otherAdmin, "")
		if err != nil || foreignSummary.Total != 0 {
			t.Fatalf("foreign summary = %+v, %v", foreignSummary, err)
		}
	})
}
