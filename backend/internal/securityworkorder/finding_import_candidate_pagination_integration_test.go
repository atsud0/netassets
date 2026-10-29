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

// Only this test's fixtures are removed; failed capacity-import evidence in the
// shared isolated database must survive. A single import transaction gives all
// candidates the same created_at, which needs a unique pagination tie-breaker.
func TestFindingImportCandidatesStablePagination(t *testing.T) {
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
	stamp := fmt.Sprintf("candidate-pagination-%d", time.Now().UnixNano())
	var userID, importID string
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name) VALUES ($1,'not-used','候选分页回归') RETURNING id::text`, stamp).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID); err != nil {
			t.Errorf("clean own user: %v", err)
		}
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO security_finding_imports (file_name,file_type,status,total_count,success_count,failure_count,duplicate_count,pending_count,errors,created_by)
		VALUES ($1,'csv','partial',241,0,0,0,221,'[]',$2::uuid) RETURNING id::text`, stamp+".csv", userID).Scan(&importID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `DELETE FROM security_finding_imports WHERE id=$1::uuid`, importID); err != nil {
			t.Errorf("clean own import and candidates: %v", err)
		}
	}()
	if _, err := pool.Exec(ctx, `INSERT INTO security_finding_import_candidates
		(finding_import_id,source_row_number,asset_type,asset_value,normalized_value,title,severity,description,discovered_at,status,created_at)
		SELECT $1::uuid,i+1,'domain','missing-'||i||'.invalid','missing-'||i||'.invalid',$2::text||'-'||i,'high','分页同时间验证',now(),
		CASE WHEN i<=20 THEN 'ignored' ELSE 'pending' END,
		CASE WHEN i>220 THEN '2026-10-28 01:00:00+00'::timestamptz ELSE '2026-10-28 00:00:00+00'::timestamptz END
		FROM generate_series(1,241) i`, importID, stamp); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `SELECT id::text FROM security_finding_import_candidates WHERE finding_import_id=$1::uuid
		ORDER BY CASE WHEN status='pending' THEN 0 ELSE 1 END, created_at DESC, id DESC`, importID)
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
	if len(expected) != 241 {
		t.Fatalf("fixture count: %d", len(expected))
	}
	principal := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{auth.PermissionSecurityFinding}}
	service := NewService(pool, t.TempDir())
	for _, pageSize := range []int{20, 100} {
		for repeat := 0; repeat < 2; repeat++ {
			seen := map[string]bool{}
			for page := 1; page <= (len(expected)+pageSize-1)/pageSize; page++ {
				result, err := service.SearchFindingImportCandidates(ctx, principal, FindingImportCandidateFilter{Keyword: stamp, Page: pagination.Query{Page: page, PageSize: pageSize}})
				if err != nil {
					t.Fatal(err)
				}
				if result.Total != len(expected) {
					t.Fatalf("total = %d, want %d", result.Total, len(expected))
				}
				offset := (page - 1) * pageSize
				wantCount := min(pageSize, len(expected)-offset)
				if len(result.Items) != wantCount {
					t.Fatalf("page %d count = %d, want %d", page, len(result.Items), wantCount)
				}
				for index, item := range result.Items {
					if item.ID != expected[offset+index] || seen[item.ID] {
						t.Fatalf("size %d repeat %d page %d index %d: unstable or duplicated candidate %s, want %s", pageSize, repeat, page, index, item.ID, expected[offset+index])
					}
					seen[item.ID] = true
				}
			}
			if len(seen) != len(expected) {
				t.Fatalf("unique candidates = %d, want %d", len(seen), len(expected))
			}
		}
	}
}
