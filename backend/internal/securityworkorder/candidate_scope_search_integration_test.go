package securityworkorder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
)

// Own fixtures only: no TRUNCATE, no assumptions about an empty database.
func TestCandidateScopeSearchBeyondLimit(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("requires dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	stamp := fmt.Sprintf("scope_%d", time.Now().UnixNano())
	var org, user, batch, importID string
	scalar := func(query string, args ...any) string {
		t.Helper()
		var result string
		if err := pool.QueryRow(ctx, query, args...).Scan(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Registered before fixture creation so partial setup failures clean up too.
	defer func() {
		for _, step := range []struct {
			q  string
			id string
		}{
			{`DELETE FROM security_finding_import_candidates WHERE finding_import_id=$1::uuid`, importID},
			{`DELETE FROM security_finding_imports WHERE id=$1::uuid`, importID},
			{`DELETE FROM security_test_scope_assets WHERE batch_id=$1::uuid`, batch},
			{`DELETE FROM security_test_batches WHERE id=$1::uuid`, batch},
			{`DELETE FROM assets WHERE created_by=$1::uuid`, user},
			{`DELETE FROM organizations WHERE id=$1::uuid`, org},
			{`DELETE FROM users WHERE id=$1::uuid`, user},
		} {
			if step.id != "" {
				if _, err := pool.Exec(ctx, step.q, step.id); err != nil {
					t.Errorf("fixture cleanup: %v", err)
				}
			}
		}
	}()
	user = scalar(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','搜索回归') RETURNING id::text`, stamp)
	org = scalar(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,$2,'subsidiary') RETURNING id::text`, stamp, "搜索组织"+stamp)
	exec(`INSERT INTO assets(asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by)
 SELECT 'domain','a-'||lpad(n::text,3,'0')||'.'||$1||'.invalid','a-'||lpad(n::text,3,'0')||'.'||$1||'.invalid','前置资产','production','public','active','normal','manual',$2::uuid,$2::uuid FROM generate_series(1,205) n`, stamp, user)
	targetValue := "zz-target." + stamp + ".invalid"
	target := scalar(`INSERT INTO assets(asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by) VALUES('domain',$1,$1,$2,'production','public','active','normal','manual',$3::uuid,$3::uuid) RETURNING id::text`, targetValue, "精确_%标记"+stamp, user)
	exec(`INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) SELECT id,$1::uuid,'owner',true FROM assets WHERE created_by=$2::uuid`, org, user)
	batch = scalar(`INSERT INTO security_test_batches(batch_code,batch_name,test_type,status,provider,authorization_note,authorized_by,authorized_at,start_at,end_at,created_by,updated_by) VALUES($1,'搜索回归','penetration','authorized','隔离测试','仅自有资产',$2::uuid,now(),now(),now()+interval '1 day',$2::uuid,$2::uuid) RETURNING id::text`, stamp, user)
	exec(`INSERT INTO security_test_scope_assets(batch_id,asset_id,asset_type_snapshot,asset_value_snapshot,asset_name_snapshot,organization_id,organization_code_snapshot,organization_name_snapshot,network_scope_snapshot,environment_snapshot,importance_snapshot,included_by)
 SELECT $1::uuid,a.id,a.asset_type,a.asset_value,a.asset_name,$2::uuid,$3,$4,'public','production','normal',$5::uuid FROM assets a WHERE a.created_by=$5::uuid`, batch, org, stamp, "搜索组织"+stamp, user)
	importID = scalar(`INSERT INTO security_finding_imports(file_name,file_type,status,total_count,success_count,failure_count,duplicate_count,pending_count,errors,created_by) VALUES($1,'csv','partial',2,0,0,0,2,'[]',$2::uuid) RETURNING id::text`, stamp+".csv", user)
	candidate := func(batchID string, row int) string {
		return scalar(`INSERT INTO security_finding_import_candidates(finding_import_id,test_batch_id,source_row_number,asset_type,asset_value,normalized_value,title,severity,description,discovered_at,status) VALUES($1::uuid,NULLIF($2,'')::uuid,$3,'domain','not-matched.invalid','not-matched.invalid','搜索回归','high','仅自有夹具',now(),'pending') RETURNING id::text`, importID, batchID, row)
	}
	globalCandidate, batchCandidate := candidate("", 2), candidate(batch, 3)
	snapshot := func() string {
		return scalar(`SELECT jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM assets a WHERE a.created_by=$1::uuid),
 (SELECT jsonb_agg(to_jsonb(c) ORDER BY c.id) FROM security_finding_import_candidates c WHERE c.finding_import_id=$2::uuid),
 (SELECT jsonb_agg(to_jsonb(i) ORDER BY i.id) FROM security_finding_imports i WHERE i.id=$2::uuid),
 (SELECT jsonb_agg(to_jsonb(a) ORDER BY a.id) FROM audit_logs a WHERE a.user_id=$1::uuid)
 )::text`, user, importID)
	}
	before := snapshot()
	service := NewService(pool, t.TempDir())
	actor := auth.Principal{UserID: user, Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding}}
	for _, testCase := range []struct{ name, id string }{{"global", globalCandidate}, {"batch", batchCandidate}} {
		t.Run(testCase.name, func(t *testing.T) {
			search := func(keyword string) []FindingImportScopeOption {
				t.Helper()
				items, err := service.SearchFindingImportScopeOptions(ctx, actor, testCase.id, keyword)
				if err != nil {
					t.Fatal(err)
				}
				return items
			}
			unfiltered := search("")
			if len(unfiltered) != 200 {
				t.Fatalf("bounded options=%d, want 200", len(unfiltered))
			}
			for _, item := range unfiltered {
				if item.AssetID == target {
					t.Fatal("fixture target unexpectedly inside first 200")
				}
			}
			for _, keyword := range []string{targetValue, "  " + strings.ToUpper(targetValue) + "  ", "精确_%标记" + stamp, "_%", "%"} {
				items := search(keyword)
				if len(items) != 1 || items[0].AssetID != target {
					t.Fatalf("search %q did not reach exact target: %#v", keyword, items)
				}
			}
			if got := search("absent_%" + stamp); len(got) != 0 {
				t.Fatalf("literal unmatched search returned %d rows", len(got))
			}
			first, second := search(stamp), search(stamp)
			if len(first) != 200 || len(second) != 200 {
				t.Fatal("matching search must remain bounded")
			}
			for i := range first {
				if first[i].ID != second[i].ID {
					t.Fatal("unstable ordering")
				}
			}
			if _, err := service.SearchFindingImportScopeOptions(ctx, auth.Principal{}, testCase.id, targetValue); !errors.Is(err, ErrForbidden) {
				t.Fatalf("unauthorized=%v", err)
			}
		})
	}
	if after := snapshot(); after != before {
		t.Fatal("read-only search changed assets, candidates, imports or audit records")
	}
	// Global matches must never escape the candidate's authorized batch.
	outsideValue := "outside." + stamp + ".invalid"
	outside := scalar(`INSERT INTO assets(asset_type,asset_value,normalized_value,environment,network_scope,status,importance,source,created_by,updated_by) VALUES('domain',$1,$1,'production','public','active','normal','manual',$2::uuid,$2::uuid) RETURNING id::text`, outsideValue, user)
	exec(`INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, outside, org)
	items, err := service.SearchFindingImportScopeOptions(ctx, actor, batchCandidate, outsideValue)
	if err != nil || len(items) != 0 {
		t.Fatalf("batch scope escaped: %v %v", items, err)
	}
	items, err = service.SearchFindingImportScopeOptions(ctx, actor, globalCandidate, outsideValue)
	if err != nil || len(items) != 1 || items[0].AssetID != outside {
		t.Fatalf("global outside target: %v %v", items, err)
	}
	exec(`UPDATE organizations SET status='disabled' WHERE id=$1::uuid`, org)
	for _, id := range []string{globalCandidate, batchCandidate} {
		items, err := service.SearchFindingImportScopeOptions(ctx, actor, id, targetValue)
		if err != nil || len(items) != 0 {
			t.Fatalf("disabled organization target visible: %v %v", items, err)
		}
	}
	exec(`UPDATE organizations SET status='enabled' WHERE id=$1::uuid`, org)
	exec(`UPDATE assets SET deleted_at=now() WHERE id=$1::uuid`, target)
	for _, id := range []string{globalCandidate, batchCandidate} {
		items, err := service.SearchFindingImportScopeOptions(ctx, actor, id, targetValue)
		if err != nil || len(items) != 0 {
			t.Fatalf("deleted target visible: %v %v", items, err)
		}
	}
}

func TestCandidateScopeSearchPermissionBeforeInput(t *testing.T) {
	service := NewService(nil, t.TempDir())
	if _, err := service.SearchFindingImportScopeOptions(context.Background(), auth.Principal{}, "invalid", "%_"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("permission=%v", err)
	}
	actor := auth.Principal{Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding}}
	if _, err := service.SearchFindingImportScopeOptions(context.Background(), actor, "invalid", "%_"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("input=%v", err)
	}
}
