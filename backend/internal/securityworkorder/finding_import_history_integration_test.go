package securityworkorder_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
	"netassets/internal/securityworkorder"
)

// The import history list scanned test_batch_id and the batch name columns out of a
// LEFT JOIN into plain strings. Every import uploaded without a test batch therefore
// aborted the whole query, so any page that opens the import history — the workbench
// shortcut to overdue work orders and security findings, the security work order
// screen itself — failed with a read error. A history row is allowed to have no
// batch, so the list must render it instead of failing.
//
// The fixture builds its own organization and import row and then asserts the very
// row it created is on the page. It must not skip when other suites have truncated
// the shared tables, or the coverage silently disappears.
// historyGroupPrincipal grants the fixture user the built-in group security role
// with the finding permission, mirroring how the product actually assembles a
// principal. Building the permission list by hand would not exercise the role gate.
func historyGroupPrincipal(t *testing.T, pool *pgxpool.Pool, userID string) auth.Principal {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id,role_id,data_scope)
		SELECT $1::uuid,id,'all' FROM roles WHERE role_key='group_admin'
		ON CONFLICT DO NOTHING`, userID); err != nil {
		t.Fatalf("grant group admin: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO role_permissions (role_id,permission_key)
		SELECT id,'security_finding.manage' FROM roles WHERE role_key='group_admin'
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("grant finding permission: %v", err)
	}
	return auth.Principal{UserID: userID, Roles: []string{"group_admin"}, Permissions: []string{"security_finding.manage"}}
}

func TestSearchFindingImportsRendersImportsWithoutTestBatch(t *testing.T) {
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

	suffix := strings.ReplaceAll(time.Now().Format("150405.000000"), ".", "")
	var organizationID, viewerID, importID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code,organization_name,organization_type)
		VALUES ($1::varchar,'导入历史测试集团_'||$1::varchar,'group') RETURNING id::text`, suffix).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ($1::varchar,'not-used','导入历史查看人_'||$1::varchar) RETURNING id::text`, suffix).Scan(&viewerID); err != nil {
		t.Fatalf("insert viewer: %v", err)
	}
	// test_batch_id is deliberately NULL: this is the row shape that used to break
	// the entire list.
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_finding_imports (test_batch_id,file_name,file_type,status,total_count,
		                                   success_count,failure_count,duplicate_count,pending_count,errors,created_by)
		VALUES (NULL,$1,'csv','completed',3,3,0,0,0,'[]'::jsonb,$2::uuid)
		RETURNING id::text`, "batchless-"+suffix+".csv", viewerID).Scan(&importID); err != nil {
		t.Fatalf("insert batchless import: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM security_finding_imports WHERE id=$1::uuid`, importID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, viewerID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM organizations WHERE id=$1::uuid`, organizationID)
	})

	// A row that does have a batch must stay intact; the fix must not empty the batch
	// columns of ordinary imports.
	var batchImportID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_test_batches (batch_code,batch_name,test_type,status,provider,authorization_note,
		                                   start_at,end_at,created_by)
		VALUES ($1::varchar,'导入历史测试批次_'||$1::varchar,'scan','authorized','导入历史测试厂商',
		        '仅用于导入历史列表的测试授权',now(),now()+interval '7 days',$2::uuid)
		RETURNING id::text`, suffix, viewerID).Scan(&batchImportID); err != nil {
		t.Fatalf("insert test batch: %v", err)
	}
	var batchedImportID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_finding_imports (test_batch_id,file_name,file_type,status,total_count,
		                                   success_count,failure_count,duplicate_count,pending_count,errors,created_by)
		VALUES ($1::uuid,$2,'csv','completed',1,1,0,0,0,'[]'::jsonb,$3::uuid)
		RETURNING id::text`, batchImportID, "batched-"+suffix+".csv", viewerID).Scan(&batchedImportID); err != nil {
		t.Fatalf("insert batched import: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM security_finding_imports WHERE id=$1::uuid`, batchedImportID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM security_test_batches WHERE id=$1::uuid`, batchImportID)
	})

	result, err := securityworkorder.NewService(pool).SearchFindingImports(ctx,
		historyGroupPrincipal(t, pool, viewerID),
		pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	if err != nil {
		t.Fatalf("SearchFindingImports must render batchless imports, got: %v", err)
	}

	byID := make(map[string]securityworkorder.FindingImportHistory, len(result.Items))
	for _, item := range result.Items {
		byID[item.ID] = item
	}
	batchless, ok := byID[importID]
	if !ok {
		t.Fatalf("the batchless import %s is missing from the %d row history page", importID, len(result.Items))
	}
	if batchless.TestBatchID != "" || batchless.TestBatchCode != "" || batchless.TestBatchName != "" {
		t.Fatalf("batchless import reports a test batch: %+v", batchless)
	}
	if batchless.FileName == "" || batchless.Errors == nil {
		t.Fatalf("batchless import lost its own fields: %+v", batchless)
	}
	batched, ok := byID[batchedImportID]
	if !ok {
		t.Fatalf("the batched import %s is missing from the history page", batchedImportID)
	}
	if batched.TestBatchID != batchImportID || batched.TestBatchCode == "" || batched.TestBatchName == "" {
		t.Fatalf("the fix emptied the batch columns of a normal import: %+v", batched)
	}
}
