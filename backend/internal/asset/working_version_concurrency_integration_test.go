package asset

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// Asset working versions used to rely on an optional updated_at comparison that
// the frontend could simply omit, so two填报员 could overwrite each other and a
// stale delete could discard freshly entered data. This pins the mandatory
// optimistic-lock contract against a migrated database.
func TestDirectWorkingAssetVersionUpdateAndDeleteRequireMatchingVersion(t *testing.T) {
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
		t.Fatalf("reset asset fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('ASSET_CAS_GROUP','资产并发测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'ASSET_CAS_SUB','资产并发测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('asset_cas_operator','not-used','资产并发填报员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key,permission_name)
		VALUES ('asset.manage','管理网络资产') ON CONFLICT (permission_key) DO NOTHING`); err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	var roleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key,role_name,role_scope,description,is_system,status)
		VALUES ('asset_cas_role','资产并发填报角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text`).Scan(&roleID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id,permission_key) VALUES ($1::uuid,'asset.manage')
		ON CONFLICT DO NOTHING`, roleID); err != nil {
		t.Fatalf("grant permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'organization')`, userID, roleID, organizationID); err != nil {
		t.Fatalf("assign role: %v", err)
	}

	principal := auth.Principal{
		UserID: userID, Username: "asset_cas_operator", DisplayName: "资产并发填报员",
		Roles: []string{"organization_editor"}, Permissions: []string{"asset.manage"},
	}
	service := NewService(pool)

	input := validWorkingAssetInput(organizationID)
	created, err := service.Create(ctx, principal, input, "manual", "req-asset-cas-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("create working asset: %v", err)
	}
	if !created.IsWorkingCopy || created.Version <= 0 {
		t.Fatalf("created working asset = %+v, want a working copy with a version", created)
	}

	if _, err = service.Update(ctx, principal, created.ID, input, "req-asset-cas-no-version", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("update without expected_version = %v, want ErrVersionRequired", err)
	}

	update := input
	update.Name = "并发测试资产（第一次修改）"
	updated, err := service.Update(ctx, principal, created.ID, withAssetVersion(update, created.Version), "req-asset-cas-update-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("first working update: %v", err)
	}
	if updated.Version != created.Version+1 {
		t.Fatalf("version after update = %d, want %d", updated.Version, created.Version+1)
	}

	update.Name = "并发测试资产（第二次修改）"
	if _, err = service.Update(ctx, principal, created.ID, withAssetVersion(update, created.Version), "req-asset-cas-update-2", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale working update = %v, want ErrEditConflict", err)
	}
	var storedName string
	if err := pool.QueryRow(ctx, `SELECT asset_name FROM inventory_asset_drafts WHERE id=$1::uuid`, created.ID).Scan(&storedName); err != nil {
		t.Fatalf("reload draft name: %v", err)
	}
	if storedName != "并发测试资产（第一次修改）" {
		t.Fatalf("stale update leaked through: stored name = %q", storedName)
	}

	if err := service.Delete(ctx, principal, created.ID, nil, "", "req-asset-cas-delete-no-version", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("delete without expected_version = %v, want ErrVersionRequired", err)
	}
	stale := created.Version
	if err := service.Delete(ctx, principal, created.ID, &stale, "", "req-asset-cas-delete-stale", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale working delete = %v, want ErrEditConflict", err)
	}
	var stillEditing string
	if err := pool.QueryRow(ctx, `SELECT draft_status FROM inventory_asset_drafts WHERE id=$1::uuid`, created.ID).Scan(&stillEditing); err != nil {
		t.Fatalf("reload draft status: %v", err)
	}
	if stillEditing != "editing" {
		t.Fatalf("draft status after stale delete = %q, want editing", stillEditing)
	}

	current := updated.Version
	if err := service.Delete(ctx, principal, created.ID, &current, "", "req-asset-cas-delete", "127.0.0.1"); err != nil {
		t.Fatalf("current working delete: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT draft_status FROM inventory_asset_drafts WHERE id=$1::uuid`, created.ID).Scan(&status); err != nil {
		t.Fatalf("reload draft status after delete: %v", err)
	}
	if status != "discarded" {
		t.Fatalf("draft status after delete = %q, want discarded", status)
	}

	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id::text=$1 AND action='asset.working_update'`, created.ID).Scan(&audits); err != nil {
		t.Fatalf("count working update audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("working update audit count = %d, want exactly 1 (a rejected stale write must not be audited as applied)", audits)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id::text=$1 AND action='asset.working_discard'`, created.ID).Scan(&audits); err != nil {
		t.Fatalf("count discard audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("discard audit count = %d, want 1", audits)
	}
}

func validWorkingAssetInput(organizationID string) CreateInput {
	return CreateInput{
		Type: "ip", Value: "192.0.2.77", Name: "并发测试资产", OrganizationID: organizationID,
		Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal",
	}
}

func withAssetVersion(input CreateInput, version int) CreateInput {
	input.ExpectedVersion = &version
	return input
}
