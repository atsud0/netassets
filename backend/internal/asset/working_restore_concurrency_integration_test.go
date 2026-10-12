package asset

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// A restore rewrites the whole working version from an immutable submission
// snapshot. It used to compare an optional updated_at and never bumped the draft
// version, so a restore could silently overwrite an editor's concurrent work and
// left the row looking untouched in the version ledger. This pins the CAS and
// version increment against a migrated database.
func TestWorkingRestoreLocksVersionAndIncrementsIt(t *testing.T) {
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
		t.Fatalf("reset restore fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('ASSET_RESTORE_GROUP','资产恢复测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'ASSET_RESTORE_SUB','资产恢复测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('asset_restore_operator','not-used','资产恢复测试填报员') RETURNING id::text`).Scan(&userID); err != nil {
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
		VALUES ('asset_restore_role','资产恢复测试角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text`).Scan(&roleID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role_id,permission_key) VALUES ($1::uuid,'asset.manage') ON CONFLICT DO NOTHING`, roleID); err != nil {
		t.Fatalf("grant permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'organization')`, userID, roleID, organizationID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	principal := auth.Principal{
		UserID: userID, Username: "asset_restore_operator", DisplayName: "资产恢复测试填报员",
		Roles: []string{"organization_editor"}, Permissions: []string{"asset.manage"},
	}

	var publishedID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO assets (asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by)
		VALUES ('ip','192.0.2.88','192.0.2.88','待恢复资产','production','public','active','normal','manual',$1::uuid,$1::uuid)
		RETURNING id::text`, userID).Scan(&publishedID); err != nil {
		t.Fatalf("insert published asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary)
		VALUES ($1::uuid,$2::uuid,'owner',true)`, publishedID, organizationID); err != nil {
		t.Fatalf("assign published asset: %v", err)
	}

	service := NewService(pool)
	input := validWorkingAssetInput(organizationID)
	input.Value = "192.0.2.88"
	input.Name = "恢复后的资产"

	restored, err := service.RestorePublishedVersion(ctx, principal, publishedID, input, "req-asset-restore-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("restore into new working version: %v", err)
	}
	if restored.Version <= 0 || restored.LastEditedBy != userID {
		t.Fatalf("restored working version = version %d last_edited_by %q, want a versioned draft owned by the actor", restored.Version, restored.LastEditedBy)
	}

	update := input
	update.Name = "编辑后的资产"
	edited, err := service.Update(ctx, principal, restored.ID, withAssetVersion(update, restored.Version), "req-asset-restore-edit", "127.0.0.1")
	if err != nil {
		t.Fatalf("edit restored working version: %v", err)
	}
	if edited.Version != restored.Version+1 {
		t.Fatalf("version after edit = %d, want %d", edited.Version, restored.Version+1)
	}

	restoreInput := input
	restoreInput.Name = "再次恢复的资产"
	restoredAgain, err := service.RestorePublishedVersion(ctx, principal, publishedID, restoreInput, "req-asset-restore-replace", "127.0.0.1")
	if err != nil {
		t.Fatalf("restore over an existing working version: %v", err)
	}
	if restoredAgain.ID != restored.ID {
		t.Fatalf("restore created a second draft %s instead of replacing %s", restoredAgain.ID, restored.ID)
	}
	if restoredAgain.Version != edited.Version+1 {
		t.Fatalf("version after restore = %d, want %d", restoredAgain.Version, edited.Version+1)
	}

	var storedName string
	var storedVersion int
	if err := pool.QueryRow(ctx, `SELECT COALESCE(asset_name,''),version FROM inventory_asset_drafts WHERE id=$1::uuid`, restored.ID).Scan(&storedName, &storedVersion); err != nil {
		t.Fatalf("reload restored draft: %v", err)
	}
	if storedName != "再次恢复的资产" || storedVersion != edited.Version+1 {
		t.Fatalf("restored draft = (%q, v%d), want (%q, v%d)", storedName, storedVersion, "再次恢复的资产", edited.Version+1)
	}

	var restoreAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id::text=$1 AND action='asset.working_restore'`, restored.ID).Scan(&restoreAudits); err != nil {
		t.Fatalf("count restore audits: %v", err)
	}
	if restoreAudits != 2 {
		t.Fatalf("restore audit count = %d, want 2 (initial restore plus replacement)", restoreAudits)
	}
	var versionDetails int
	if err := pool.QueryRow(ctx, `
		SELECT (details->>'version_after')::int FROM audit_logs
		WHERE resource_id::text=$1 AND action='asset.working_restore'
		ORDER BY created_at DESC LIMIT 1`, restored.ID).Scan(&versionDetails); err != nil {
		t.Fatalf("read restore audit details: %v", err)
	}
	if versionDetails != edited.Version+1 {
		t.Fatalf("restore audit version_after = %d, want %d", versionDetails, edited.Version+1)
	}
}
