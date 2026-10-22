package securityworkorder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFindingEditRechecksWorkOrderConstraintsAfterWaiting(t *testing.T) {
	pool, p, org, fid := reviewFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	svc := NewService(pool, t.TempDir())
	f, err := svc.GetFinding(ctx, p, fid)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 1)
	go func() {
		_, e := svc.UpdateFinding(ctx, p, fid, FindingInput{Title: f.Title, Severity: f.Severity, Status: "false_positive", AssigneeOrganizationID: org, BusinessSystemID: f.BusinessSystemID, Description: f.Description, Source: f.Source, ExpectedUpdatedAt: &f.UpdatedAt}, "review-edit-race", "127.0.0.1")
		results <- e
	}()
	waiting := 0
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%pg_advisory_xact_lock%'`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if waiting == 0 {
		t.Fatal("edit did not reach orchestration lock")
	}
	order, err := svc.CreateWorkOrder(ctx, p, reviewOrder(org, fid), "review-create-race", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-results
	if !errors.Is(err, ErrConflict) && !errors.Is(err, ErrEditConflict) {
		var status string
		if e := pool.QueryRow(ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, fid).Scan(&status); e != nil {
			t.Fatal(e)
		}
		t.Fatalf("edit result=%v; active draft order=%s; finding status=%s; want conflict", err, order.ID, status)
	}
	var status string
	if e := pool.QueryRow(ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, fid).Scan(&status); e != nil {
		t.Fatal(e)
	}
	if status != f.Status {
		t.Fatalf("finding status changed to %s despite conflict", status)
	}
	var links int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM security_work_order_findings WHERE work_order_id=$1::uuid AND finding_id=$2::uuid`, order.ID, fid).Scan(&links); e != nil {
		t.Fatal(e)
	}
	if links != 1 {
		t.Fatalf("work order lost its finding link: %d", links)
	}
	var audits int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id='review-edit-race'`).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if audits != 0 {
		t.Fatalf("rejected finding edit wrote %d audits", audits)
	}

}

// A transaction holding the orchestration/finding locks must use its own
// connection for validation rather than waiting for a second pool connection.
func TestFindingEditWorksWithSingleDatabaseConnection(t *testing.T) {
	pool, p, org, id := reviewFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	config := pool.Config()
	config.MaxConns = 1
	config.MinConns = 0
	single, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer single.Close()
	svc := NewService(single, t.TempDir())
	f, err := svc.GetFinding(ctx, p, id)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := svc.UpdateFinding(ctx, p, id, FindingInput{
		Title: "Updated through a single connection", Severity: f.Severity, Status: f.Status,
		AssigneeOrganizationID: org, BusinessSystemID: f.BusinessSystemID,
		Description: f.Description, Source: f.Source, ExpectedUpdatedAt: &f.UpdatedAt,
	}, "single-connection-edit", "127.0.0.1")
	if err != nil {
		t.Fatalf("single-connection update: %v", err)
	}
	if updated.Title != "Updated through a single connection" {
		t.Fatalf("unexpected title: %s", updated.Title)
	}
}
