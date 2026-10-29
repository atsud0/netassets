package securityworkorder

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

// File imports share transaction timestamps. Every page must keep an identical,
// unique order even when thousands of records have both timestamps in common.
func TestFindingsStablePagination(t *testing.T) {
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
	stamp := fmt.Sprintf("finding-page-%d", time.Now().UnixNano())
	var orgID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'正式漏洞分页回归','subsidiary') RETURNING id::text`, stamp).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','正式漏洞分页验证员') RETURNING id::text`, stamp).Scan(&userID); err != nil {
		// The organization has no dependent records at this point.
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, orgID)
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID); err != nil {
			t.Errorf("clean own user: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM security_findings WHERE assignee_organization_id=$1::uuid`, orgID); err != nil {
			t.Errorf("clean own findings: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, orgID); err != nil {
			t.Errorf("clean own organization: %v", err)
		}
	}()
	// Last three rows exercise a partial final page. Distinct discovery/creation
	// groups additionally ensure the new tie-breaker preserves primary ordering.
	const count = 5003
	if _, err := pool.Exec(ctx, `INSERT INTO security_findings(finding_code,title,severity,status,assignee_organization_id,description,source,discovered_at,created_at)
 SELECT $2::text||'-'||i,$2::text||'-'||i,'high','pending',$1::uuid,'分页原文-'||i,'import',
 '2026-10-28 00:00:00+00'::timestamptz + CASE WHEN i>5000 THEN interval '1 day' ELSE interval '0 day' END,
 '2026-10-28 00:00:00+00'::timestamptz + CASE WHEN i>4990 THEN interval '1 hour' ELSE interval '0 hour' END
 FROM generate_series(1,5003) i`, orgID, stamp); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT id::text FROM security_findings WHERE assignee_organization_id=$1::uuid ORDER BY discovered_at DESC,created_at DESC,id DESC`, orgID)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		expected = append(expected, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(expected) != count {
		t.Fatalf("fixture count=%d, want %d", len(expected), count)
	}
	svc := NewService(pool, t.TempDir())
	principal := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{auth.PermissionSecurityFinding}}
	for _, size := range []int{20, 100} {
		for repeat := 0; repeat < 2; repeat++ {
			seen := map[string]bool{}
			var got []string
			for page := 1; page <= (count+size-1)/size; page++ {
				result, err := svc.SearchFindings(ctx, principal, orgID, "pending", "", pagination.Query{Page: page, PageSize: size})
				if err != nil {
					t.Fatal(err)
				}
				offset := (page - 1) * size
				if result.Total != count || len(result.Items) != min(size, count-offset) {
					t.Fatalf("page %d total/count=%d/%d", page, result.Total, len(result.Items))
				}
				for index, item := range result.Items {
					if seen[item.ID] {
						t.Fatalf("size=%d repeat=%d page=%d index=%d: duplicate finding %s", size, repeat, page, index, item.ID)
					}
					got = append(got, item.ID)
					if item.AssigneeOrganizationID != orgID || !strings.HasPrefix(item.Title, stamp+"-") || !strings.HasPrefix(item.Description, "分页原文-") {
						t.Fatalf("finding fields altered: %+v", item)
					}
					seen[item.ID] = true
				}
			}
			for index, id := range got {
				if id != expected[index] {
					t.Fatalf("size=%d repeat=%d index=%d: unstable order %s, want %s", size, repeat, index, id, expected[index])
				}
			}
			if len(seen) != count {
				t.Fatalf("unique findings=%d, want %d", len(seen), count)
			}
			beyond, err := svc.SearchFindings(ctx, principal, orgID, "pending", "", pagination.Query{Page: (count+size-1)/size + 1, PageSize: size})
			if err != nil {
				t.Fatal(err)
			}
			if beyond.Total != count || len(beyond.Items) != 0 {
				t.Fatalf("beyond-last-page total/count=%d/%d", beyond.Total, len(beyond.Items))
			}
		}
	}
}
