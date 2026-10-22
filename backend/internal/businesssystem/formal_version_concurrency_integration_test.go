package businesssystem

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/customfield"
)

// Formal records are edited directly by group administrators. This test pins
// the optimistic-lock contract for every formal lifecycle mutation, rather
// than only testing the working-version publication path: a stale request must
// never overwrite a newer formal row or change its lifecycle state.
func TestFormalBusinessSystemMutationsRequireCurrentVersion(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE custom_field_definitions, organizations, users CASCADE`); err != nil {
		t.Fatalf("reset formal business system fixtures: %v", err)
	}

	var organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('BS_FORMAL_CAS', '正式版本并发测试集团', 'group') RETURNING id::text`).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ('bs_formal_cas_operator', 'not-used', '正式版本并发测试管理员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	principal := auth.Principal{
		UserID: userID, Username: "bs_formal_cas_operator", DisplayName: "正式版本并发测试管理员",
		Roles: []string{"group_admin"}, Permissions: []string{"system.manage"},
	}
	service := NewService(pool)
	input := validBusinessSystemInput()
	input.Code = "BS_FORMAL_CAS_001"
	input.OrganizationID = organizationID

	created, err := service.Create(ctx, principal, input, "formal-cas-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("create formal system: %v", err)
	}
	if created.IsWorkingCopy || created.Version != 1 {
		t.Fatalf("created formal system = working=%v version=%d, want false/1", created.IsWorkingCopy, created.Version)
	}

	// Custom fields are a separate write endpoint. They must participate in the
	// same formal version ledger, otherwise an old dialog can still overwrite a
	// newer custom value after the main form passed its own CAS check.
	if _, err := pool.Exec(ctx, `
		INSERT INTO custom_field_definitions(resource_type, field_key, field_name, field_type, options, created_by)
		VALUES ('business_system', 'business_owner', '业务负责人', 'text', '[]'::jsonb, $1::uuid)`, userID); err != nil {
		t.Fatalf("insert custom field definition: %v", err)
	}
	customInput := input
	customInput.Code = "BS_FORMAL_CAS_CUSTOM"
	customCreated, err := service.Create(ctx, principal, customInput, "formal-cas-custom-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("create custom-field formal system: %v", err)
	}
	customVersion := customCreated.Version
	customService := customfield.NewService(pool)
	if err := customService.SaveValues(ctx, principal, "business_system", customCreated.ID, customfield.ValuesInput{
		Values: []customfield.Value{{FieldKey: "business_owner", Value: "甲方"}}, ExpectedVersion: &customVersion,
	}, "formal-cas-custom-save", "127.0.0.1"); err != nil {
		t.Fatalf("save formal custom field: %v", err)
	}
	if customVersion == 0 {
		t.Fatal("custom field fixture started without a version")
	}
	if err := customService.SaveValues(ctx, principal, "business_system", customCreated.ID, customfield.ValuesInput{
		Values: []customfield.Value{{FieldKey: "business_owner", Value: "旧请求"}}, ExpectedVersion: &customVersion,
	}, "formal-cas-custom-stale", "127.0.0.1"); !errors.Is(err, customfield.ErrVersionConflict) {
		t.Fatalf("stale formal custom field save = %v, want ErrVersionConflict", err)
	}
	var customVersionInDB int
	if err := pool.QueryRow(ctx, `SELECT version FROM business_systems WHERE id=$1::uuid`, customCreated.ID).Scan(&customVersionInDB); err != nil {
		t.Fatalf("reload custom-field system: %v", err)
	}
	if customVersionInDB != customVersion+1 {
		t.Fatalf("custom-field version after stale save = %d, want %d", customVersionInDB, customVersion+1)
	}

	// The same endpoint is used for formal assets; protect that second ledger
	// too, otherwise asset edits remain a bypass around the concurrency policy.
	if _, err := pool.Exec(ctx, `
		INSERT INTO custom_field_definitions(resource_type, field_key, field_name, field_type, options, created_by)
		VALUES ('asset', 'asset_owner', '资产负责人', 'text', '[]'::jsonb, $1::uuid)`, userID); err != nil {
		t.Fatalf("insert asset custom field definition: %v", err)
	}
	var assetID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO assets(asset_type, asset_value, normalized_value, asset_name, created_by, updated_by)
		VALUES ('ip', '192.0.2.250', '192.0.2.250', '并发测试资产', $1::uuid, $1::uuid) RETURNING id::text`, userID).Scan(&assetID); err != nil {
		t.Fatalf("insert formal asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_organizations(asset_id, organization_id, relationship_type, is_primary)
		VALUES ($1::uuid, $2::uuid, 'owner', true)`, assetID, organizationID); err != nil {
		t.Fatalf("link formal asset organization: %v", err)
	}
	assetVersion := 1
	if err := customService.SaveValues(ctx, principal, "asset", assetID, customfield.ValuesInput{
		Values: []customfield.Value{{FieldKey: "asset_owner", Value: "甲方"}}, ExpectedVersion: &assetVersion,
	}, "formal-cas-asset-custom-save", "127.0.0.1"); err != nil {
		t.Fatalf("save formal asset custom field: %v", err)
	}
	if err := customService.SaveValues(ctx, principal, "asset", assetID, customfield.ValuesInput{
		Values: []customfield.Value{{FieldKey: "asset_owner", Value: "旧请求"}}, ExpectedVersion: &assetVersion,
	}, "formal-cas-asset-custom-stale", "127.0.0.1"); !errors.Is(err, customfield.ErrVersionConflict) {
		t.Fatalf("stale formal asset custom field save = %v, want ErrVersionConflict", err)
	}
	var assetVersionInDB int
	if err := pool.QueryRow(ctx, `SELECT version FROM assets WHERE id=$1::uuid`, assetID).Scan(&assetVersionInDB); err != nil {
		t.Fatalf("reload formal asset: %v", err)
	}
	if assetVersionInDB != 2 {
		t.Fatalf("asset custom-field version after stale save = %d, want 2", assetVersionInDB)
	}

	if _, err := service.Update(ctx, principal, created.ID, input, "formal-cas-missing", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("formal update without expected_version = %v, want ErrVersionRequired", err)
	}

	firstUpdate := input
	firstUpdate.Name = "正式系统第一次修改"
	updated, err := service.Update(ctx, principal, created.ID, withVersion(firstUpdate, created.Version), "formal-cas-update-1", "127.0.0.1")
	if err != nil {
		t.Fatalf("first formal update: %v", err)
	}
	if updated.Version != 2 || updated.Name != firstUpdate.Name {
		t.Fatalf("first formal update = version=%d name=%q, want 2/%q", updated.Version, updated.Name, firstUpdate.Name)
	}

	staleUpdate := input
	staleUpdate.Name = "旧请求不应覆盖"
	if _, err := service.Update(ctx, principal, created.ID, withVersion(staleUpdate, created.Version), "formal-cas-update-stale", "127.0.0.1"); !errors.Is(err, ErrFormalVersionConflict) {
		t.Fatalf("stale formal update = %v, want ErrFormalVersionConflict", err)
	}
	assertFormalNameAndVersion(t, ctx, pool, created.ID, firstUpdate.Name, 2)

	archived, err := service.SetArchived(ctx, principal, created.ID, true, versionPtr(2), "formal-cas-archive", "127.0.0.1")
	if err != nil {
		t.Fatalf("archive formal system: %v", err)
	}
	if archived.Status != "archived" || archived.Version != 3 {
		t.Fatalf("archived formal system = status=%q version=%d, want archived/3", archived.Status, archived.Version)
	}
	if _, err := service.SetArchived(ctx, principal, created.ID, false, versionPtr(2), "formal-cas-restore-stale", "127.0.0.1"); !errors.Is(err, ErrFormalVersionConflict) {
		t.Fatalf("stale formal restore = %v, want ErrFormalVersionConflict", err)
	}
	assertFormalNameAndVersion(t, ctx, pool, created.ID, firstUpdate.Name, 3)

	restored, err := service.SetArchived(ctx, principal, created.ID, false, versionPtr(3), "formal-cas-restore", "127.0.0.1")
	if err != nil {
		t.Fatalf("restore formal system: %v", err)
	}
	if restored.Status != "running" || restored.Version != 4 {
		t.Fatalf("restored formal system = status=%q version=%d, want running/4", restored.Status, restored.Version)
	}
	if err := service.Delete(ctx, principal, created.ID, versionPtr(3), "formal-cas-delete-stale", "127.0.0.1"); !errors.Is(err, ErrFormalVersionConflict) {
		t.Fatalf("stale formal delete = %v, want ErrFormalVersionConflict", err)
	}
	assertFormalNameAndVersion(t, ctx, pool, created.ID, firstUpdate.Name, 4)

	if err := service.Delete(ctx, principal, created.ID, versionPtr(4), "formal-cas-delete", "127.0.0.1"); err != nil {
		t.Fatalf("current formal delete: %v", err)
	}
	var deleted bool
	var finalVersion int
	if err := pool.QueryRow(ctx, `SELECT deleted_at IS NOT NULL, version FROM business_systems WHERE id=$1::uuid`, created.ID).Scan(&deleted, &finalVersion); err != nil {
		t.Fatalf("reload deleted formal system: %v", err)
	}
	if !deleted || finalVersion != 5 {
		t.Fatalf("deleted formal system = deleted=%v version=%d, want true/5", deleted, finalVersion)
	}
}

func assertFormalNameAndVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id, wantName string, wantVersion int) {
	t.Helper()
	var name string
	var version int
	if err := pool.QueryRow(ctx, `SELECT system_name, version FROM business_systems WHERE id=$1::uuid`, id).Scan(&name, &version); err != nil {
		t.Fatalf("reload formal business system: %v", err)
	}
	if name != wantName || version != wantVersion {
		t.Fatalf("formal system after stale write = name=%q version=%d, want %q/%d", name, version, wantName, wantVersion)
	}
}
