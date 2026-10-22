package inventorytask

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// These regressions exercise the actual publication functions (not just a
// version-comparison helper): an old submitted draft must fail atomically after
// its formal resource has advanced to a newer revision.
func TestSubmittedDraftsRejectStaleFormalSourceVersions(t *testing.T) {
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
		t.Fatalf("reset source-version fixtures: %v", err)
	}

	var groupID, organizationID, userID, taskID, systemID, assetID string
	mustQuery := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("insert fixture: %v\n%s", err, query)
		}
		return id
	}
	groupID = mustQuery(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES('SRCVER_G','来源版本测试集团','group') RETURNING id::text`)
	organizationID = mustQuery(`INSERT INTO organizations(parent_id,organization_code,organization_name,organization_type) VALUES($1::uuid,'SRCVER_O','来源版本测试组织','subsidiary') RETURNING id::text`, groupID)
	userID = mustQuery(`INSERT INTO users(username,password_hash,display_name) VALUES('source_version_test','unused','来源版本测试员') RETURNING id::text`)
	taskID = mustQuery(`INSERT INTO inventory_tasks(task_name,start_at,deadline_at,status,created_by) VALUES('来源版本回归任务',now()-interval '1 day',now()+interval '1 day','active',$1::uuid) RETURNING id::text`, userID)
	if _, err := pool.Exec(ctx, `INSERT INTO inventory_task_organizations(task_id,organization_id,status) VALUES($1::uuid,$2::uuid,'submitted')`, taskID, organizationID); err != nil {
		t.Fatalf("assign task: %v", err)
	}
	systemID = mustQuery(`INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status,created_by) VALUES('SRCVER_SYS','正式系统旧名称',$1::uuid,'running',$2::uuid) RETURNING id::text`, organizationID, userID)
	if _, err := pool.Exec(ctx, `INSERT INTO business_system_data_security_profiles(business_system_id) VALUES($1::uuid)`, systemID); err != nil {
		t.Fatalf("insert system profile: %v", err)
	}
	assetID = mustQuery(`INSERT INTO assets(asset_type,asset_value,normalized_value,asset_name,created_by) VALUES('ip','192.0.2.210','192.0.2.210','正式资产旧名称',$1::uuid) RETURNING id::text`, userID)

	t.Run("asset source version", func(t *testing.T) {
		var draftID string
		if err := pool.QueryRow(ctx, `INSERT INTO inventory_asset_drafts(task_id,organization_id,operation,source_asset_id,source_asset_version,asset_type,asset_value,normalized_value,asset_name,draft_status,created_by,updated_by) VALUES($1::uuid,$2::uuid,'update',$3::uuid,(SELECT version FROM assets WHERE id=$3::uuid),'ip','192.0.2.210','192.0.2.210','旧草稿尝试覆盖名称','submitted',$4::uuid,$4::uuid) RETURNING id::text`, taskID, organizationID, assetID, userID).Scan(&draftID); err != nil {
			t.Fatalf("create submitted asset draft: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE assets SET asset_name='正式资产新名称',version=version+1 WHERE id=$1::uuid`, assetID); err != nil {
			t.Fatalf("advance formal asset: %v", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin asset publication: %v", err)
		}
		principal := auth.Principal{UserID: userID}
		err = publishAssetDrafts(ctx, tx, principal, taskID, organizationID, "source-version-asset", "127.0.0.1")
		_ = tx.Rollback(ctx)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("publishing stale asset draft = %v, want ErrConflict", err)
		}
		var name string
		var version int
		if err := pool.QueryRow(ctx, `SELECT asset_name,version FROM assets WHERE id=$1::uuid`, assetID).Scan(&name, &version); err != nil {
			t.Fatalf("reload formal asset: %v", err)
		}
		if name != "正式资产新名称" || version != 2 {
			t.Fatalf("stale draft changed formal asset: name=%q version=%d", name, version)
		}
		var draftStatus string
		if err := pool.QueryRow(ctx, `SELECT draft_status FROM inventory_asset_drafts WHERE id=$1::uuid`, draftID).Scan(&draftStatus); err != nil {
			t.Fatalf("reload submitted asset draft: %v", err)
		}
		if draftStatus != "submitted" {
			t.Fatalf("draft status after rejected publish = %q, want submitted", draftStatus)
		}
	})

	t.Run("business system source version", func(t *testing.T) {
		var draftID string
		if err := pool.QueryRow(ctx, `INSERT INTO inventory_business_system_drafts(task_id,organization_id,operation,source_system_id,source_system_version,system_code,system_name,system_status,system_level,draft_status,created_by,updated_by,last_edited_by) VALUES($1::uuid,$2::uuid,'update',$3::uuid,(SELECT version FROM business_systems WHERE id=$3::uuid),'SRCVER_SYS','旧草稿系统名称','running','normal','submitted',$4::uuid,$4::uuid,$4::uuid) RETURNING id::text`, taskID, organizationID, systemID, userID).Scan(&draftID); err != nil {
			t.Fatalf("create submitted business system draft: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE business_systems SET system_name='正式系统新名称',version=version+1 WHERE id=$1::uuid`, systemID); err != nil {
			t.Fatalf("advance formal business system: %v", err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin business system publication: %v", err)
		}
		principal := auth.Principal{UserID: userID}
		err = publishBusinessSystemDrafts(ctx, tx, principal, taskID, organizationID, "source-version-system", "127.0.0.1")
		_ = tx.Rollback(ctx)
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("publishing stale business system draft = %v, want ErrConflict", err)
		}
		var name string
		var version int
		if err := pool.QueryRow(ctx, `SELECT system_name,version FROM business_systems WHERE id=$1::uuid`, systemID).Scan(&name, &version); err != nil {
			t.Fatalf("reload formal business system: %v", err)
		}
		if name != "正式系统新名称" || version != 2 {
			t.Fatalf("stale draft changed formal business system: name=%q version=%d", name, version)
		}
		var draftStatus string
		if err := pool.QueryRow(ctx, `SELECT draft_status FROM inventory_business_system_drafts WHERE id=$1::uuid`, draftID).Scan(&draftStatus); err != nil {
			t.Fatalf("reload submitted business system draft: %v", err)
		}
		if draftStatus != "submitted" {
			t.Fatalf("draft status after rejected publish = %q, want submitted", draftStatus)
		}
	})
}
