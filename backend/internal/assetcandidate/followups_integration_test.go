package assetcandidate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/auth"
)

func TestCandidateFollowUpPermissionsAuditAndStatus(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset candidate follow-up fixtures: %v", err)
	}

	var groupID, organizationID, otherOrganizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('FOLLOWUP_GROUP', '候选跟进测试集团', 'group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'FOLLOWUP_SUB', '候选跟进测试子公司', 'subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'FOLLOWUP_OTHER', '候选跟进其他子公司', 'subsidiary') RETURNING id::text`, groupID).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("insert other organization: %v", err)
	}

	userID := func(username, displayName string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name) VALUES ($1,'not-used',$2) RETURNING id::text`, username, displayName).Scan(&id); err != nil {
			t.Fatalf("insert user %s: %v", username, err)
		}
		return id
	}
	securityID := userID("followup_security", "集团安全管理员")
	viewOnlyID := userID("followup_viewer", "仅查看安全管理员")
	organizationUserID := userID("followup_org", "子公司管理员")
	vendorID := userID("followup_vendor", "外部厂商")

	if _, err := pool.Exec(ctx, `
		INSERT INTO roles (role_key,role_name,role_scope,description,is_system,status)
		VALUES ('followup_org_role','候选跟进组织角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled'
	`); err != nil {
		t.Fatalf("insert organization role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key,permission_name) VALUES ('asset.manage','管理网络资产')
		ON CONFLICT (permission_key) DO NOTHING
	`); err != nil {
		t.Fatalf("insert asset permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id,permission_key)
		SELECT id,'asset.manage' FROM roles WHERE role_key='followup_org_role'
		ON CONFLICT DO NOTHING
	`); err != nil {
		t.Fatalf("grant asset permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
		SELECT $1::uuid,id,$2::uuid,'organization' FROM roles WHERE role_key='followup_org_role'
	`, organizationUserID, organizationID); err != nil {
		t.Fatalf("assign organization role: %v", err)
	}

	service := NewService(pool, asset.NewService(pool))
	groupPrincipal := auth.Principal{UserID: securityID, DisplayName: "集团管理员", Roles: []string{auth.RoleGroupAdmin}}
	candidate, err := service.Create(ctx, groupPrincipal, CreateInput{
		AssetType: "ip", AssetValue: "192.0.2.10", OrganizationID: organizationID,
		Source: "finding_import", SourceReference: "followup-test",
	}, "candidate-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("create candidate: %v", err)
	}

	securityPrincipal := auth.Principal{
		UserID: securityID, DisplayName: "集团安全管理员", Roles: []string{auth.RoleSecurityOperator},
		Permissions: []string{auth.PermissionAssetCandidateView, auth.PermissionAssetCandidateFollowUp},
	}
	followup, err := service.AddFollowUp(ctx, securityPrincipal, candidate.ID, FollowUpInput{
		Action: "handoff_request", Comment: "请责任组织确认资产归属",
	}, "followup-request", "192.0.2.1")
	if err != nil {
		t.Fatalf("add candidate follow-up: %v", err)
	}
	if followup.Action != "handoff_request" || followup.RequestID != "followup-request" {
		t.Fatalf("follow-up = %+v, want action/request metadata", followup)
	}

	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM asset_candidates WHERE id=$1::uuid`, candidate.ID).Scan(&status); err != nil {
		t.Fatalf("read candidate status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("candidate status after follow-up = %q, want pending", status)
	}

	items, err := service.ListFollowUps(ctx, securityPrincipal, candidate.ID)
	if err != nil {
		t.Fatalf("list candidate follow-ups: %v", err)
	}
	if len(items) != 1 || items[0].ID != followup.ID {
		t.Fatalf("follow-up list = %+v, want the created item", items)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='asset_candidate.follow_up' AND resource_id::text=$1 AND request_id='followup-request'`, candidate.ID).Scan(&auditCount); err != nil {
		t.Fatalf("read follow-up audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("follow-up audit count = %d, want 1", auditCount)
	}

	viewOnly := auth.Principal{
		UserID: viewOnlyID, DisplayName: "仅查看安全管理员", Roles: []string{auth.RoleSecurityOperator},
		Permissions: []string{auth.PermissionAssetCandidateView},
	}
	if _, err := service.ListFollowUps(ctx, viewOnly, candidate.ID); err != nil {
		t.Fatalf("view-only principal should list follow-ups: %v", err)
	}
	if _, err := service.AddFollowUp(ctx, viewOnly, candidate.ID, FollowUpInput{Comment: "不应写入"}, "view-only", "127.0.0.2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("view-only add follow-up error = %v, want ErrForbidden", err)
	}

	organizationPrincipal := auth.Principal{UserID: organizationUserID, DisplayName: "子公司管理员", Permissions: []string{"asset.manage"}}
	if _, err := service.ListFollowUps(ctx, organizationPrincipal, candidate.ID); err != nil {
		t.Fatalf("own organization should read follow-ups: %v", err)
	}
	if _, err := service.AddFollowUp(ctx, organizationPrincipal, candidate.ID, FollowUpInput{Comment: "组织不能写集团跟进记录"}, "org-write", "127.0.0.3"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("organization add follow-up error = %v, want ErrForbidden", err)
	}
	otherCandidate, err := service.Create(ctx, groupPrincipal, CreateInput{
		AssetType: "ip", AssetValue: "192.0.2.11", OrganizationID: otherOrganizationID,
		Source: "finding_import", SourceReference: "followup-test-other",
	}, "candidate-create-other", "127.0.0.1")
	if err != nil {
		t.Fatalf("create other candidate: %v", err)
	}
	if _, err := service.ListFollowUps(ctx, organizationPrincipal, otherCandidate.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-organization follow-up read error = %v, want ErrForbidden", err)
	}

	vendor := auth.Principal{UserID: vendorID, DisplayName: "外部厂商"}
	if _, err := service.AddFollowUp(ctx, vendor, candidate.ID, FollowUpInput{Comment: "不应写入"}, "vendor-write", "127.0.0.4"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("vendor add follow-up error = %v, want ErrForbidden", err)
	}
	if _, err := service.AddFollowUp(ctx, securityPrincipal, candidate.ID, FollowUpInput{Action: "unsupported", Comment: "非法动作"}, "invalid-action", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid follow-up action error = %v, want ErrInvalid", err)
	}
}
