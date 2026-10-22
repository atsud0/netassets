package businesssystem

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/customfield"
	"os"
	"strings"
	"testing"
)

func dailySystemFixture(t *testing.T) (*pgxpool.Pool, auth.Principal, string, string) {
	t.Helper()
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

	return pool, principal, organizationID, publishedID
}

func TestDailySystemUpdateFromFormalPreservesFieldsAndSource(t *testing.T) {
	pool, p, org, source := dailySystemFixture(t)
	ctx := context.Background()
	in := validBusinessSystemInput()
	in.OrganizationID, in.Code, in.Name = org, "BS_RESTORE_001", "Updated daily system"
	in.DataStorageLocation, in.SecurityNotes = "Shanghai", "Reviewed security notes"
	draft, err := NewService(pool).Update(ctx, p, source, withVersion(in, 1), "daily-update", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if !draft.IsWorkingCopy || draft.Name != in.Name || draft.DataStorageLocation != in.DataStorageLocation || draft.SecurityNotes != in.SecurityNotes {
		t.Fatalf("incorrect working fields: %+v", draft)
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT source_system_version FROM inventory_business_system_drafts WHERE id=$1::uuid`, draft.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("source version=%d", version)
	}
	var customValuesType string
	if err := pool.QueryRow(ctx, `SELECT jsonb_typeof(custom_values) FROM inventory_business_system_drafts WHERE id=$1::uuid`, draft.ID).Scan(&customValuesType); err != nil {
		t.Fatal(err)
	}
	if customValuesType != "array" {
		t.Fatalf("empty working custom values JSON type = %q, want array", customValuesType)
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT system_name FROM business_systems WHERE id=$1::uuid`, source).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name == in.Name {
		t.Fatal("working change modified formal record")
	}
}

func TestDailySystemDeleteFromFormal(t *testing.T) {
	pool, p, _, source := dailySystemFixture(t)
	if err := NewService(pool).Delete(context.Background(), p, source, versionPtr(1), "daily-delete", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	var operation string
	var version int
	if err := pool.QueryRow(context.Background(), `SELECT operation,source_system_version FROM inventory_business_system_drafts WHERE source_system_id=$1::uuid`, source).Scan(&operation, &version); err != nil {
		t.Fatal(err)
	}
	if operation != "delete" || version != 1 {
		t.Fatalf("operation=%s source version=%d", operation, version)
	}
}

func TestDailySystemRejectsOtherOrganizationBeforeWrite(t *testing.T) {
	pool, p, _, source := dailySystemFixture(t)
	ctx := context.Background()
	var other string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES('DAILY_OTHER','Other organization','subsidiary') RETURNING id::text`).Scan(&other); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE business_systems SET owner_organization_id=$2::uuid WHERE id=$1::uuid`, source, other); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool)
	in := validBusinessSystemInput()
	in.OrganizationID = other
	if _, err := svc.Update(ctx, p, source, withVersion(in, 1), "unauthorized-update", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("update=%v, want forbidden", err)
	}
	if err := svc.Delete(ctx, p, source, versionPtr(1), "unauthorized-delete", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("delete=%v, want forbidden", err)
	}
	var drafts, events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inventory_business_system_drafts WHERE source_system_id=$1::uuid`, source).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_events WHERE actor_user_id=$1::uuid`, p.UserID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 || events != 0 {
		t.Fatalf("unauthorized writes: drafts=%d events=%d", drafts, events)
	}
}

func TestDailySystemRejectsStaleFormalInput(t *testing.T) {
	pool, p, org, source := dailySystemFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE business_systems SET version=version+1 WHERE id=$1::uuid`, source); err != nil {
		t.Fatal(err)
	}
	in := validBusinessSystemInput()
	in.OrganizationID = org
	svc := NewService(pool)
	if _, err := svc.Update(ctx, p, source, withVersion(in, 1), "stale-update", "127.0.0.1"); !errors.Is(err, ErrFormalVersionConflict) {
		t.Fatalf("stale update=%v", err)
	}
	if err := svc.Delete(ctx, p, source, versionPtr(1), "stale-delete", "127.0.0.1"); !errors.Is(err, ErrFormalVersionConflict) {
		t.Fatalf("stale delete=%v", err)
	}
	var drafts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM inventory_business_system_drafts WHERE source_system_id=$1::uuid`, source).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 {
		t.Fatalf("stale requests created %d drafts", drafts)
	}
}

func TestOrganizationEditorCannotArchiveFormalBusinessSystem(t *testing.T) {
	pool, p, _, source := dailySystemFixture(t)
	ctx := context.Background()
	version := 1
	if _, err := NewService(pool).SetArchived(ctx, p, source, true, &version, "daily-archive-review", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("organization editor archive = %v, want ErrForbidden", err)
	}
	for _, archived := range []bool{true, false} {
		if _, err := NewService(pool).SetArchivedBatch(ctx, p, BatchArchiveInput{
			Items: []BatchArchiveItem{{ID: source, ExpectedVersion: version}}, Archived: archived,
		}, "daily-batch-archive-review", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("organization editor batch archive=%t: %v, want ErrForbidden", archived, err)
		}
	}
	var status string
	var storedVersion int
	if err := pool.QueryRow(ctx, `SELECT system_status,version FROM business_systems WHERE id=$1::uuid`, source).Scan(&status, &storedVersion); err != nil {
		t.Fatal(err)
	}
	if status != "running" || storedVersion != version {
		t.Fatalf("formal system after rejected archive = (%q, v%d), want (running, v%d)", status, storedVersion, version)
	}
}

func TestOrganizationEditorCannotSaveFormalCustomFieldsDirectly(t *testing.T) {
	pool, p, _, source := dailySystemFixture(t)
	ctx := context.Background()
	version := 1
	err := customfield.NewService(pool).SaveValues(ctx, p, "business_system", source, customfield.ValuesInput{
		Values: []customfield.Value{{FieldKey: "owner", Value: "unauthorized"}}, ExpectedVersion: &version,
	}, "daily-custom-review", "127.0.0.1")
	if !errors.Is(err, customfield.ErrForbidden) {
		t.Fatalf("organization editor direct custom field save = %v, want ErrForbidden", err)
	}
	var dbVersion int
	if err := pool.QueryRow(ctx, `SELECT version FROM business_systems WHERE id=$1::uuid`, source).Scan(&dbVersion); err != nil {
		t.Fatal(err)
	}
	if dbVersion != version {
		t.Fatalf("formal system version after rejected custom field save = %d, want %d", dbVersion, version)
	}
}
