package businesssystem

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// The direct working space (outside an inventory task) used to write drafts with
// no optimistic lock at all: two editors silently overwrote each other, and a
// stale delete could discard somebody else's freshly typed changes. This test
// pins the CAS contract end to end against a migrated database.
func TestDirectWorkingVersionUpdateAndDeleteRequireMatchingVersion(t *testing.T) {
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
		t.Fatalf("reset business system fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('BS_CAS_GROUP','业务系统并发测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'BS_CAS_SUB','业务系统并发测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('bs_cas_operator','not-used','业务系统并发填报员') RETURNING id::text`).Scan(&userID); err != nil {
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
		VALUES ('bs_cas_role','业务系统并发填报角色','organization','',false,'enabled')
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
		UserID: userID, Username: "bs_cas_operator", DisplayName: "业务系统并发填报员",
		Roles: []string{"organization_editor"}, Permissions: []string{"system.manage"},
	}
	service := NewService(pool)

	input := validBusinessSystemInput()
	input.Code = "BS_CAS_001"
	input.OrganizationID = organizationID
	created, err := service.Create(ctx, principal, input, "req-cas-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("create working system: %v", err)
	}
	if !created.IsWorkingCopy || created.Version <= 0 {
		t.Fatalf("created working version = %+v, want a working copy with a version", created)
	}

	stale := created.Version
	if _, err = service.Update(ctx, principal, created.ID, input, "req-cas-no-version", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("update without expected_version = %v, want ErrVersionRequired", err)
	}

	update := input
	update.Name = "客户关系管理系统（第一次修改）"
	updated, err := service.Update(ctx, principal, created.ID, withVersion(update, created.Version), "req-cas-update-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("first working update: %v", err)
	}
	if updated.Version != created.Version+1 {
		t.Fatalf("version after update = %d, want %d", updated.Version, created.Version+1)
	}

	update.Name = "客户关系管理系统（第二次修改）"
	if _, err = service.Update(ctx, principal, created.ID, withVersion(update, stale), "req-cas-update-2", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale working update = %v, want ErrEditConflict", err)
	}
	var persistedName string
	if err := pool.QueryRow(ctx, `SELECT system_name FROM inventory_business_system_drafts WHERE id=$1::uuid`, created.ID).Scan(&persistedName); err != nil {
		t.Fatalf("reload draft name: %v", err)
	}
	if persistedName != "客户关系管理系统（第一次修改）" {
		t.Fatalf("stale update leaked through: stored name = %q", persistedName)
	}

	if err := service.Delete(ctx, principal, created.ID, nil, "req-cas-delete-no-version", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("delete without expected_version = %v, want ErrVersionRequired", err)
	}
	if err := service.Delete(ctx, principal, created.ID, versionPtr(stale), "req-cas-delete-stale", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("stale working delete = %v, want ErrEditConflict", err)
	}

	currentVersion := updated.Version
	if err := service.Delete(ctx, principal, created.ID, versionPtr(currentVersion), "req-cas-delete", "127.0.0.1"); err != nil {
		t.Fatalf("current working delete: %v", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT draft_status FROM inventory_business_system_drafts WHERE id=$1::uuid`, created.ID).Scan(&status); err != nil {
		t.Fatalf("reload draft status: %v", err)
	}
	if status != "discarded" {
		t.Fatalf("draft status after delete = %q, want discarded", status)
	}

	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id=$1 AND action='business_system.working_update'`, created.ID).Scan(&audits); err != nil {
		t.Fatalf("count working update audits: %v", err)
	}
	if audits != 1 {
		t.Fatalf("working update audit count = %d, want exactly 1", audits)
	}
}

// withVersion marks an edit as carrying the optimistic-lock value the client
// last saw; without it the service layer must refuse the write.
func withVersion(input CreateInput, version int) CreateInput {
	input.ExpectedVersion = &version
	return input
}

func versionPtr(value int) *int { return &value }
