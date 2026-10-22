package securityworkorder

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"os"
	"strings"
	"testing"
	"time"
)

func reviewFixture(t *testing.T) (*pgxpool.Pool, auth.Principal, string, string) {
	t.Helper()
	ctx := context.Background()
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires migrated test database")
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatal(err)
	}
	p03Bootstrap(t, pool, "REVIEW")
	org := p03InsertOrg(t, pool, "", "REVIEW_O", "Review org", "subsidiary")
	user := p03InsertUser(t, pool, "review_admin", "Review admin")
	p03AssignRole(t, pool, user, "group_admin", "", "all")
	p := p03Principal(t, pool, user, []string{"group_admin"})
	var system string
	if err := pool.QueryRow(ctx, `INSERT INTO business_systems(system_code,system_name,owner_organization_id,created_by,system_status) VALUES('REVIEW_SYS','Review system',$1::uuid,$2::uuid,'running') RETURNING id::text`, org, user).Scan(&system); err != nil {
		t.Fatal(err)
	}
	finding, err := NewService(pool, t.TempDir()).CreateFinding(ctx, p, FindingInput{Title: "Review finding", Severity: "high", Status: "confirmed", AssigneeOrganizationID: org, BusinessSystemID: system, Description: "Review fixture", Source: "manual"}, "review-finding", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	return pool, p, org, finding.ID
}
func reviewOrder(org, finding string) WorkOrderInput {
	return WorkOrderInput{Title: "Review work order", AssigneeOrganizationID: org, RemediationRequirement: "Fix reviewed issue", DueAt: time.Now().Add(24 * time.Hour), FindingIDs: []string{finding}}
}

func TestConcurrentWorkOrderCreationIsExclusive(t *testing.T) {
	pool, p, org, finding := reviewFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(ctx, `SELECT id FROM security_findings WHERE id=$1::uuid FOR UPDATE`, finding); err != nil {
		t.Fatal(err)
	}
	svc := NewService(pool, t.TempDir())
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := svc.CreateWorkOrder(ctx, p, reviewOrder(org, finding), "review-race", "127.0.0.1")
			results <- err
		}()
	}
	// Wait until both requests are blocked on the finding row. With the old
	// check-before-lock implementation, both have already passed the active-order
	// check at this point; the fixed implementation checks only after locking.
	waiting := 0
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%SELECT id::text, severity%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if waiting != 2 {
		t.Fatalf("could not establish two blocked creation requests, waiting=%d", waiting)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		} else if !errors.Is(err, ErrConflict) {
			t.Fatalf("concurrent request error = %v, want ErrConflict", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent creations = %d, want exactly 1", successes)
	}
	var active int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_work_order_findings l JOIN security_work_orders w ON w.id=l.work_order_id WHERE finding_id=$1::uuid AND w.status NOT IN('closed','cancelled')`, finding).Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("same finding has %d active work orders after %d successful concurrent requests", active, successes)
	}
}

func TestLegacyIssuedWorkOrderCancellationRestoresFinding(t *testing.T) {
	pool, p, org, finding := reviewFixture(t)
	ctx := context.Background()
	svc := NewService(pool, t.TempDir())
	order, err := svc.CreateWorkOrder(ctx, p, reviewOrder(org, finding), "review-create", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Issue(ctx, p, order.ID, CommentInput{}, "review-issue", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	// Pre-00083 links have no saved before-issue status after the additive migration.
	if _, err := pool.Exec(ctx, `UPDATE security_work_order_findings SET finding_status_before_issue=NULL WHERE work_order_id=$1::uuid`, order.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Cancel(ctx, p, order.ID, CommentInput{Comment: "Cancel historical work order"}, "review-cancel", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, finding).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "confirmed" {
		t.Fatalf("historical finding status after cancellation = %q, want conservative fallback confirmed", status)
	}
}

// New links have an exact pre-issue snapshot; only legacy issued links use the
// confirmed fallback. In particular, neither a draft cancellation nor issuing
// a finding that was already in progress should manufacture a status change.
func TestWorkOrderCancellationPreservesKnownFindingState(t *testing.T) {
	for _, status := range []string{"pending", "confirmed", "in_progress"} {
		for _, issue := range []bool{false, true} {
			name := status + "/draft"
			if issue {
				name = status + "/issued"
			}
			t.Run(name, func(t *testing.T) {
				pool, p, org, finding := reviewFixture(t)
				ctx := context.Background()
				if _, err := pool.Exec(ctx, `UPDATE security_findings SET status=$2 WHERE id=$1::uuid`, finding, status); err != nil {
					t.Fatal(err)
				}
				svc := NewService(pool, t.TempDir())
				order, err := svc.CreateWorkOrder(ctx, p, reviewOrder(org, finding), "cancel-create", "127.0.0.1")
				if err != nil {
					t.Fatal(err)
				}
				if issue {
					if _, err := svc.Issue(ctx, p, order.ID, CommentInput{}, "cancel-issue", "127.0.0.1"); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := svc.Cancel(ctx, p, order.ID, CommentInput{Comment: "Cancel test work order"}, "cancel-order", "127.0.0.1"); err != nil {
					t.Fatal(err)
				}
				var got string
				if err := pool.QueryRow(ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, finding).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != status {
					t.Fatalf("finding after cancellation = %q, want original %q", got, status)
				}
			})
		}
	}
}
