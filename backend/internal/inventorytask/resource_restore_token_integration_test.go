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

func TestResourceRestoreStateAndConfirmedTokenWorkflow(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `TRUNCATE organizations,users CASCADE`); err != nil {
		t.Fatal(err)
	}
	query := func(sql string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatalf("fixture: %v", err)
		}
		return id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	group := query(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES('TOKEN_G','Token group','group') RETURNING id::text`)
	org := query(`INSERT INTO organizations(parent_id,organization_code,organization_name,organization_type) VALUES($1::uuid,'TOKEN_O','Token organization','subsidiary') RETURNING id::text`, group)
	user := query(`INSERT INTO users(username,password_hash,display_name) VALUES('restore_token_editor','unused','Token editor') RETURNING id::text`)
	outsider := query(`INSERT INTO users(username,password_hash,display_name) VALUES('restore_token_outsider','unused','Outsider') RETURNING id::text`)
	role := query(`INSERT INTO roles(role_key,role_name,role_scope,is_system,status) VALUES('restore_token_role','Restore token role','organization',false,'enabled') ON CONFLICT(role_key) DO UPDATE SET status='enabled' RETURNING id::text`)
	for _, perm := range []string{"inventory.manage", "asset.manage", "system.manage"} {
		exec(`INSERT INTO permissions(permission_key,permission_name) VALUES($1,$1) ON CONFLICT DO NOTHING`, perm)
		exec(`INSERT INTO role_permissions(role_id,permission_key) VALUES($1::uuid,$2) ON CONFLICT DO NOTHING`, role, perm)
	}
	exec(`INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,'organization')`, user, role, org)
	p := auth.Principal{UserID: user, Roles: []string{"organization_editor"}, Permissions: []string{"inventory.manage", "asset.manage", "system.manage"}}
	task := query(`INSERT INTO inventory_tasks(task_name,start_at,deadline_at,status,created_by) VALUES('Restore token task',now()-interval '1 day',now()+interval '1 day','active',$1::uuid) RETURNING id::text`, user)
	submission := query(`INSERT INTO inventory_submissions(task_id,organization_id,submission_version,review_status,submitted_by) VALUES($1::uuid,$2::uuid,1,'approved',$3::uuid) RETURNING id::text`, task, org, user)
	system := query(`INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status,created_by) VALUES('TOKEN_SYS','Formal system',$1::uuid,'running',$2::uuid) RETURNING id::text`, org, user)
	exec(`INSERT INTO business_system_data_security_profiles(business_system_id) VALUES($1::uuid)`, system)
	asset := query(`INSERT INTO assets(asset_type,asset_value,normalized_value,asset_name,created_by) VALUES('ip','192.0.2.91','192.0.2.91','Formal asset',$1::uuid) RETURNING id::text`, user)
	exec(`INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, asset, org)
	svc := NewService(pool)
	for _, tc := range []struct{ typ, id, table, nameCol, snapshot string }{
		{"network_asset", asset, "inventory_asset_drafts", "asset_name", `{"asset_type":"ip","asset_value":"192.0.2.91","asset_name":"Historical asset","environment":"production","network_scope":"public","status":"active","importance":"normal"}`},
		{"business_system", system, "inventory_business_system_drafts", "system_name", `{"system_code":"TOKEN_SYS","system_name":"Historical system","system_status":"running","system_level":"normal","internet_exposure":"unknown","contains_personal_information":"unknown","contains_sensitive_personal_information":"unknown","data_volume_over_100k":"unknown","contains_important_data":"unknown","contains_core_data":"unknown"}`},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			exec(`INSERT INTO inventory_submission_items(submission_id,resource_type,resource_id,resource_key,resource_name,snapshot_data) VALUES($1::uuid,$2,$3::uuid,'token-resource','Historical resource',$4::jsonb)`, submission, tc.typ, tc.id, tc.snapshot)
			for _, actor := range []auth.Principal{
				{UserID: user, Roles: []string{"group_admin"}, Permissions: p.Permissions},
				{UserID: outsider, Roles: p.Roles, Permissions: p.Permissions},
				{UserID: user, Roles: p.Roles, Permissions: []string{"inventory.manage"}},
			} {
				if _, err := svc.ResourceSubmissionRestoreState(ctx, actor, tc.typ, tc.id); !errors.Is(err, ErrForbidden) {
					t.Fatalf("unauthorized state=%v", err)
				}
			}
			token, err := svc.ResourceSubmissionRestoreState(ctx, p, tc.typ, tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if token.ExpectedVersion == nil || *token.ExpectedVersion != 0 || token.WorkingVersionID != "" {
				t.Fatalf("initial state=%+v", token)
			}
			if _, err := svc.RestoreResourceSubmission(ctx, p, tc.typ, tc.id, submission, ResourceSubmissionRestoreInput{}, "missing-token", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("missing token=%v", err)
			}
			restored, err := svc.RestoreResourceSubmission(ctx, p, tc.typ, tc.id, submission, token, "token-create", "127.0.0.1")
			if err != nil {
				t.Fatal(err)
			}
			if restored.WorkingVersionID == "" || restored.PublishedID != tc.id {
				t.Fatalf("restore result=%+v", restored)
			}
			if _, err := svc.RestoreResourceSubmission(ctx, p, tc.typ, tc.id, submission, token, "token-replay", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
				t.Fatalf("replayed absence token=%v", err)
			}
			token, err = svc.ResourceSubmissionRestoreState(ctx, p, tc.typ, tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if token.WorkingVersionID != restored.WorkingVersionID || token.ExpectedVersion == nil || *token.ExpectedVersion != 1 {
				t.Fatalf("draft state=%+v", token)
			}
			exec(`UPDATE `+tc.table+` SET `+tc.nameCol+`='Concurrent editor',version=version+1 WHERE id=$1::uuid`, restored.WorkingVersionID)
			if _, err := svc.RestoreResourceSubmission(ctx, p, tc.typ, tc.id, submission, token, "token-stale", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
				t.Fatalf("stale restore=%v", err)
			}
			var name string
			var version int
			if err := pool.QueryRow(ctx, `SELECT `+tc.nameCol+`,version FROM `+tc.table+` WHERE id=$1::uuid`, restored.WorkingVersionID).Scan(&name, &version); err != nil {
				t.Fatal(err)
			}
			if name != "Concurrent editor" || version != 2 {
				t.Fatalf("editor overwritten: %s v%d", name, version)
			}
			token, err = svc.ResourceSubmissionRestoreState(ctx, p, tc.typ, tc.id)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := svc.RestoreResourceSubmission(ctx, p, tc.typ, tc.id, submission, token, "token-reconfirmed", "127.0.0.1"); err != nil {
				t.Fatal(err)
			}
			exec(`UPDATE `+tc.table+` SET draft_status='submitted',version=version+1 WHERE id=$1::uuid`, restored.WorkingVersionID)
			if _, err := svc.ResourceSubmissionRestoreState(ctx, p, tc.typ, tc.id); !errors.Is(err, ErrConflict) {
				t.Fatalf("submitted state=%v", err)
			}
		})
	}
}
