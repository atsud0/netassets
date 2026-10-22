package asset

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// A restore rewrites the whole working version from an immutable submission
// snapshot. It used to compare an optional updated_at and never bumped the draft
// version, so a restore could silently overwrite an editor's concurrent work and
// left the row looking untouched in the version ledger. This pins the CAS and
// version increment against a migrated database.
func reviewAssetFixture(t *testing.T) (*pgxpool.Pool, auth.Principal, string, string) {
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

	return pool, principal, organizationID, publishedID
}

func TestHistoricalRestoreRejectsEditorCommittedWhileWaitingForLock(t *testing.T) {
	pool, p, org, source := reviewAssetFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	svc := NewService(pool)
	input := validWorkingAssetInput(org)
	input.Value = "192.0.2.88"
	input.Name = "Draft before restore"
	zero := 0
	input.ExpectedVersion = &zero
	draft, err := svc.RestorePublishedVersion(ctx, p, source, input, "review-initial", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT id FROM inventory_asset_drafts WHERE id=$1::uuid FOR UPDATE`, draft.ID); err != nil {
		t.Fatal(err)
	}
	input.Name = "Historical snapshot"
	input.ExpectedVersion = &draft.Version
	input.ExpectedWorkingVersionID = draft.ID
	results := make(chan error, 1)
	go func() {
		_, e := svc.RestorePublishedVersion(ctx, p, source, input, "review-restore-race", "127.0.0.1")
		results <- e
	}()
	waiting := 0
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%SELECT version FROM inventory_asset_drafts%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if waiting == 0 {
		t.Fatal("restore did not reach draft lock")
	}
	if _, err = blocker.Exec(ctx, `UPDATE inventory_asset_drafts SET asset_name='Concurrent editor saved',version=version+1,updated_at=now() WHERE id=$1::uuid`, draft.ID); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-results
	var name string
	var version int
	if e := pool.QueryRow(ctx, `SELECT asset_name,version FROM inventory_asset_drafts WHERE id=$1::uuid`, draft.ID).Scan(&name, &version); e != nil {
		t.Fatal(e)
	}
	var audits int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id='review-restore-race'`).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if audits != 0 {
		t.Fatalf("conflicting restore wrote %d audits", audits)
	}
	if !errors.Is(err, ErrEditConflict) || name != "Concurrent editor saved" || version != draft.Version+1 {
		t.Fatalf("restore result=%v, saved name=%q, version=%d: concurrent editor was overwritten", err, name, version)
	}
}

// Tokens bind a restore to both identity and revision, including the absence
// of a draft. No stale token may silently select a new replacement target.
func TestHistoricalRestoreRequiresConfirmedWorkingVersion(t *testing.T) {
	pool, p, org, source := reviewAssetFixture(t)
	ctx := context.Background()
	svc := NewService(pool)
	input := validWorkingAssetInput(org)
	input.Value = "192.0.2.88"
	if _, err := svc.RestorePublishedVersion(ctx, p, source, input, "missing-token", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("missing token=%v", err)
	}
	zero := 0
	input.ExpectedVersion = &zero
	draft, err := svc.RestorePublishedVersion(ctx, p, source, input, "initial-token", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, id string
		version  int
	}{
		{"draft appeared after confirmation", "", 0},
		{"wrong draft identity", "11111111-1111-4111-8111-111111111111", draft.Version},
		{"stale draft version", draft.ID, draft.Version + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := input
			in.ExpectedVersion = &tc.version
			in.ExpectedWorkingVersionID = tc.id
			if _, err := svc.RestorePublishedVersion(ctx, p, source, in, "stale-token", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
				t.Fatalf("restore=%v, want edit conflict", err)
			}
		})
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT version FROM inventory_asset_drafts WHERE id=$1::uuid`, draft.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != draft.Version {
		t.Fatalf("rejected restore changed version: %d", version)
	}
}
