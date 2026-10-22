package businesssystem

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// A restore rewrites the working version from an immutable submission snapshot.
// It previously wrote without touching the draft version, so the version ledger
// could not show that the row had changed and a concurrent editor's work was
// overwritten without any conflict signal. This pins the CAS and the version
// increment against a migrated database.
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
		t.Fatalf("reset business system restore fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('BS_RESTORE_GROUP','业务系统恢复测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'BS_RESTORE_SUB','业务系统恢复测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('bs_restore_operator','not-used','业务系统恢复填报员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key,permission_name)
		VALUES ('system.manage','管理系统信息') ON CONFLICT (permission_key) DO NOTHING`); err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	var roleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key,role_name,role_scope,description,is_system,status)
		VALUES ('bs_restore_role','业务系统恢复测试角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text`).Scan(&roleID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id,permission_key) VALUES ($1::uuid,'system.manage')
		ON CONFLICT DO NOTHING`, roleID); err != nil {
		t.Fatalf("grant permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'organization')`, userID, roleID, organizationID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	principal := auth.Principal{
		UserID: userID, Username: "bs_restore_operator", DisplayName: "业务系统恢复填报员",
		Roles: []string{"organization_editor"}, Permissions: []string{"system.manage"},
	}

	var publishedID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO business_systems (
			system_code,system_name,owner_organization_id,system_status,system_level,
			internet_exposure,created_by,updated_by
		) VALUES (
			'BS_RESTORE_001','待恢复业务系统',$1::uuid,'running','normal',
			'unknown',$2::uuid,$2::uuid
		) RETURNING id::text`, organizationID, userID).Scan(&publishedID); err != nil {
		t.Fatalf("insert published business system: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO business_system_data_security_profiles (business_system_id)
		VALUES ($1::uuid) ON CONFLICT (business_system_id) DO NOTHING`, publishedID); err != nil {
		t.Fatalf("insert data security profile: %v", err)
	}

	service := NewService(pool)
	input := validBusinessSystemInput()
	input.Code = "BS_RESTORE_001"
	input.Name = "恢复后的业务系统"
	input.OrganizationID = organizationID
	zeroVersion := 0
	input.ExpectedVersion = &zeroVersion

	restored, err := service.RestorePublishedVersion(ctx, principal, publishedID, input, "req-bs-restore-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("restore into new working version: %v", err)
	}
	if restored.Version <= 0 || restored.LastEditedBy != userID {
		t.Fatalf("restored working version = version %d last_edited_by %q, want a versioned draft owned by the actor", restored.Version, restored.LastEditedBy)
	}
	var formalVersion, sourceVersion int
	if err := pool.QueryRow(ctx, `SELECT bs.version, draft.source_system_version FROM business_systems bs JOIN inventory_business_system_drafts draft ON draft.id=$2::uuid WHERE bs.id=$1::uuid`, publishedID, restored.ID).Scan(&formalVersion, &sourceVersion); err != nil {
		t.Fatalf("load restored working version source revision: %v", err)
	}
	if sourceVersion != formalVersion {
		t.Fatalf("restored working source version = %d, want current formal version %d", sourceVersion, formalVersion)
	}

	update := input
	update.Name = "编辑后的业务系统"
	edited, err := service.Update(ctx, principal, restored.ID, withVersion(update, restored.Version), "req-bs-restore-edit", "127.0.0.1")
	if err != nil {
		t.Fatalf("edit restored working version: %v", err)
	}
	if edited.Version != restored.Version+1 {
		t.Fatalf("version after edit = %d, want %d", edited.Version, restored.Version+1)
	}

	restoreInput := input
	restoreInput.Name = "再次恢复的业务系统"
	restoreInput.ExpectedVersion = &edited.Version
	restoreInput.ExpectedWorkingVersionID = restored.ID
	restoredAgain, err := service.RestorePublishedVersion(ctx, principal, publishedID, restoreInput, "req-bs-restore-replace", "127.0.0.1")
	if err != nil {
		t.Fatalf("restore over an existing working version: %v", err)
	}
	if restoredAgain.ID != restored.ID {
		t.Fatalf("restore created a second draft %s instead of replacing %s", restoredAgain.ID, restored.ID)
	}
	if restoredAgain.Version != edited.Version+1 {
		t.Fatalf("version after restore = %d, want %d", restoredAgain.Version, edited.Version+1)
	}
	if err := pool.QueryRow(ctx, `SELECT source_system_version FROM inventory_business_system_drafts WHERE id=$1::uuid`, restoredAgain.ID).Scan(&sourceVersion); err != nil {
		t.Fatalf("reload restored draft source version: %v", err)
	}
	if sourceVersion != formalVersion {
		t.Fatalf("source version after restoring existing draft = %d, want preserved base version %d", sourceVersion, formalVersion)
	}

	var storedName string
	var storedVersion int
	if err := pool.QueryRow(ctx, `SELECT system_name,version FROM inventory_business_system_drafts WHERE id=$1::uuid`, restored.ID).Scan(&storedName, &storedVersion); err != nil {
		t.Fatalf("reload restored draft: %v", err)
	}
	if storedName != "再次恢复的业务系统" || storedVersion != edited.Version+1 {
		t.Fatalf("restored draft = (%q, v%d), want (%q, v%d)", storedName, storedVersion, "再次恢复的业务系统", edited.Version+1)
	}

	var restoreAudits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id=$1 AND action='business_system.working_restore'`, restored.ID).Scan(&restoreAudits); err != nil {
		t.Fatalf("count restore audits: %v", err)
	}
	if restoreAudits != 2 {
		t.Fatalf("restore audit count = %d, want 2 (initial restore plus replacement)", restoreAudits)
	}
}
