package assetcandidate

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/auth"
)

// A candidate confirmed from a finding import owns an unpublished working
// version that findings point at. Discarding that working version used to strand
// the candidate permanently: Accept refuses to run twice, and the findings kept a
// pending_asset_draft_id that could never be published.
func TestDiscardingCandidateWorkingVersionReleasesCandidateAndFindings(t *testing.T) {
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
		t.Fatalf("reset asset candidate fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('CANDIDATE_DISCARD_GROUP', '候选废弃测试集团', 'group') RETURNING id::text
	`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'CANDIDATE_DISCARD_SUB','候选废弃测试子公司','subsidiary') RETURNING id::text
	`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ('candidate_discard_operator','not-used','候选废弃测试运营') RETURNING id::text
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key, permission_name)
		VALUES ('asset.manage','管理网络资产') ON CONFLICT (permission_key) DO NOTHING
	`); err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	var roleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
		VALUES ('candidate_discard_role','候选废弃测试角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text
	`).Scan(&roleID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key) VALUES ($1::uuid,'asset.manage')
		ON CONFLICT DO NOTHING
	`, roleID); err != nil {
		t.Fatalf("grant permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'organization')
	`, userID, roleID, organizationID); err != nil {
		t.Fatalf("assign role: %v", err)
	}

	// Candidate creation and confirmation are headquarters-owned actions, while
	// discarding the resulting working version is an organization-side action.
	// Both run as the same seeded user so the test stays self-contained.
	principal := auth.Principal{UserID: userID, Username: "candidate_discard_operator", DisplayName: "候选废弃测试运营", Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{"asset.manage"}}
	groupPrincipal := principal
	principal = auth.Principal{UserID: userID, Username: "candidate_discard_operator", DisplayName: "候选废弃测试运营", Roles: []string{"organization_admin"}, Permissions: []string{"asset.manage"}}

	assetService := asset.NewService(pool)
	service := NewService(pool, assetService)

	created, err := service.Create(ctx, groupPrincipal, CreateInput{
		AssetType: "domain", AssetValue: "discard.example.com", Name: "待废弃资产",
		OrganizationID: organizationID, Source: "finding_import",
		SourceReference: "findings-20261010",
	}, "req-discard", "127.0.0.1")
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}
	if _, err := service.Accept(ctx, groupPrincipal, created.ID, AcceptInput{}, "req-discard", "127.0.0.1"); err != nil {
		t.Fatalf("accept candidate: %v", err)
	}

	var draftID, draftStatus string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(working_version_draft_id::text,''),status FROM asset_candidates WHERE id=$1::uuid`, created.ID).Scan(&draftID, &draftStatus); err != nil {
		t.Fatalf("load candidate draft link: %v", err)
	}
	if draftID == "" || draftStatus != "pending" {
		t.Fatalf("candidate after accept = draft %q status %q, want a working version", draftID, draftStatus)
	}

	// Attach a finding the same way the release path does, so the discard has to
	// unwind a real dependency rather than an empty case.
	var findingID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_findings (
			finding_code, title, severity, status, assignee_organization_id,
			pending_asset_draft_id, description, source, created_by, updated_by
		) VALUES ('F-DISCARD-CANDIDATE','待废弃工作版本上的漏洞','high','pending',$1::uuid,$2::uuid,
		          '用于验证废弃工作版本后漏洞回到未关联。','import',$3::uuid,$3::uuid)
		RETURNING id::text
	`, organizationID, draftID, userID).Scan(&findingID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}

	// The working version is locked: the discard must carry the version it read.
	var draftVersion int
	if err := pool.QueryRow(ctx, `SELECT version FROM inventory_asset_drafts WHERE id=$1::uuid`, draftID).Scan(&draftVersion); err != nil {
		t.Fatalf("load working version lock: %v", err)
	}
	if err := assetService.Delete(ctx, principal, draftID, &draftVersion, "req-discard", "127.0.0.1"); err != nil {
		t.Fatalf("discard working version: %v", err)
	}

	if err := pool.QueryRow(ctx, `SELECT COALESCE(working_version_draft_id::text,''),status FROM asset_candidates WHERE id=$1::uuid`, created.ID).Scan(&draftID, &draftStatus); err != nil {
		t.Fatalf("reload candidate: %v", err)
	}
	if draftID != "" {
		t.Fatalf("candidate still references discarded draft %q, want it cleared so it can be confirmed again", draftID)
	}
	if draftStatus != "pending" {
		t.Fatalf("candidate status after discard = %q, want pending", draftStatus)
	}

	var pendingDraftID string
	if err := pool.QueryRow(ctx, `SELECT COALESCE(pending_asset_draft_id::text,'') FROM security_findings WHERE id=$1::uuid`, findingID).Scan(&pendingDraftID); err != nil {
		t.Fatalf("reload finding: %v", err)
	}
	if pendingDraftID != "" {
		t.Fatalf("finding still points at discarded draft %q, want unlinked", pendingDraftID)
	}

	// The whole point of unwinding: the candidate must be confirmable again.
	if _, err := service.Accept(ctx, groupPrincipal, created.ID, AcceptInput{}, "req-discard-2", "127.0.0.1"); err != nil {
		t.Fatalf("re-accept candidate after discard: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(working_version_draft_id::text,'') FROM asset_candidates WHERE id=$1::uuid`, created.ID).Scan(&draftID); err != nil {
		t.Fatalf("reload candidate after re-accept: %v", err)
	}
	if draftID == "" {
		t.Fatalf("candidate after re-accept = draft %q, want a fresh working version", draftID)
	}
}
