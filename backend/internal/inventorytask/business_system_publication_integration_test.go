package inventorytask

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// Exercise the successful UPDATE statement, not only its stale-source guard.
// PostgreSQL must infer one type for the status parameter shared by SET and CASE.
func TestBusinessSystemDraftPublicationStatusTransitions(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	for _, tc := range []struct {
		name, before, after, previous string
	}{
		{"running edit", "running", "running", ""},
		{"archive", "running", "archived", "running"},
		{"archived edit", "archived", "archived", "running"},
		{"unarchive", "archived", "running", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			queryID := func(query string, args ...any) string {
				t.Helper()
				var id string
				if err := tx.QueryRow(ctx, query, args...).Scan(&id); err != nil {
					t.Fatalf("fixture: %v", err)
				}
				return id
			}
			orgID := queryID(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES('PUB_STATUS','发布状态测试组织','group') RETURNING id::text`)
			userID := queryID(`INSERT INTO users(username,password_hash,display_name) VALUES('pub_status','unused','发布状态测试员') RETURNING id::text`)
			taskID := queryID(`INSERT INTO inventory_tasks(task_name,start_at,deadline_at,status,created_by) VALUES('发布状态回归',now()-interval '1 day',now()+interval '1 day','active',$1::uuid) RETURNING id::text`, userID)
			systemID := queryID(`INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status,status_before_archive,created_by) VALUES('PUB_SYS','发布前名称',$1::uuid,$2::text,CASE WHEN $2::text='archived' THEN 'running' ELSE NULL END,$3::uuid) RETURNING id::text`, orgID, tc.before, userID)
			draftID := queryID(`INSERT INTO inventory_business_system_drafts(task_id,organization_id,operation,source_system_id,source_system_version,system_code,system_name,system_status,draft_status,created_by,updated_by) VALUES($1::uuid,$2::uuid,'update',$3::uuid,1,'PUB_SYS','审核后的名称',$4,'submitted',$5::uuid,$5::uuid) RETURNING id::text`, taskID, orgID, systemID, tc.after, userID)
			if err := publishBusinessSystemDrafts(ctx, tx, auth.Principal{UserID: userID}, taskID, orgID, "publication-status", "127.0.0.1"); err != nil {
				t.Fatalf("publish status %s -> %s: %v", tc.before, tc.after, err)
			}
			var name, status, previous, draftStatus, publishedID string
			var version, auditCount int
			var published bool
			if err := tx.QueryRow(ctx, `SELECT system_name,system_status,COALESCE(status_before_archive,''),version FROM business_systems WHERE id=$1::uuid`, systemID).Scan(&name, &status, &previous, &version); err != nil {
				t.Fatal(err)
			}
			if name != "审核后的名称" || status != tc.after || previous != tc.previous || version != 2 {
				t.Fatalf("published system: name=%q status=%q previous=%q version=%d", name, status, previous, version)
			}
			if err := tx.QueryRow(ctx, `SELECT draft_status,source_system_id::text,published_at IS NOT NULL FROM inventory_business_system_drafts WHERE id=$1::uuid`, draftID).Scan(&draftStatus, &publishedID, &published); err != nil {
				t.Fatal(err)
			}
			if draftStatus != "published" || publishedID != systemID || !published {
				t.Fatalf("draft publication: status=%q published_id=%q", draftStatus, publishedID)
			}
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_type='business_system' AND resource_id=$1 AND action='business_system.update'`, systemID).Scan(&auditCount); err != nil {
				t.Fatal(err)
			}
			if auditCount != 1 {
				t.Fatalf("publication audit count = %d, want 1", auditCount)
			}
		})
	}
}
