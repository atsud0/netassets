package dataquality

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/assetobservation"
	"netassets/internal/backup"
)

type scheduledRefreshProbe struct {
	calls   int
	refresh func(context.Context) error
}

func (p *scheduledRefreshProbe) RefreshDue(ctx context.Context, _ assetobservation.BulkRefreshOptions) (assetobservation.BulkRefreshResult, error) {
	p.calls++
	return assetobservation.BulkRefreshResult{}, p.refresh(ctx)
}

func scheduledPreflightFixture(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// A uniquely conditioned rule belongs only to this fixture; never enable or
	// delete pre-existing rules in the retained development database.
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	var ruleID, date string
	err = pool.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,resource_type,rule_type,target_field,severity,expiry_warning_days,conditions,is_enabled)
 VALUES($1::text,'asset','expiry','domain_registration_expires_at','warning',30,
 jsonb_build_array(jsonb_build_object('field','value','operator','eq','values',jsonb_build_array($1::text))),true) RETURNING id::text`, "scheduled-preflight-"+stamp).Scan(&ruleID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM data_quality_rules WHERE id=$1::uuid`, ruleID); err != nil {
			t.Errorf("cleanup own rule: %v", err)
		}
	})
	if err := pool.QueryRow(ctx, `SELECT ('6000-01-01'::date + floor(random()*700000)::int)::text`).Scan(&date); err != nil {
		t.Fatal(err)
	}
	return pool, date
}

func insertCompletedPreflightBatch(t *testing.T, pool *pgxpool.Pool, date string) {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO data_quality_scan_batches(trigger_type,scheduled_for) VALUES('scheduled',$1::date) ON CONFLICT DO NOTHING RETURNING id::text`, date).Scan(&id); err != nil {
		t.Fatalf("insert own completed scheduled batch: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM data_quality_scan_batches WHERE id=$1::uuid`, id); err != nil {
			t.Errorf("cleanup own batch: %v", err)
		}
	})
}

func TestScheduledScanCompletedSkipsExternalRefresh(t *testing.T) {
	pool, date := scheduledPreflightFixture(t)
	insertCompletedPreflightBatch(t, pool, date)
	sentinel := errors.New("external network must not be touched")
	probe := &scheduledRefreshProbe{refresh: func(context.Context) error { return sentinel }}
	s := NewService(pool)
	s.observationRefresher = probe
	_, err := s.scan(context.Background(), "", ScanFilter{}, scanTrigger{Type: scanTriggerScheduled, ScheduledFor: date}, "scheduled-preflight-completed", "")
	if !errors.Is(err, errScheduledScanCompleted) {
		t.Errorf("completed scan error = %v, want already completed", err)
	}
	if probe.calls != 0 {
		t.Errorf("completed scan called external refresher %d times; want zero", probe.calls)
	}
}

func TestScheduledScanPreflightPreservesRefreshAndTransactionalGuards(t *testing.T) {
	pool, date := scheduledPreflightFixture(t)
	sentinel := errors.New("refresh sentinel")
	s := NewService(pool)
	probe := &scheduledRefreshProbe{refresh: func(context.Context) error { return sentinel }}
	s.observationRefresher = probe
	for _, trigger := range []scanTrigger{{Type: scanTriggerScheduled, ScheduledFor: date}, {Type: scanTriggerManual}} {
		_, err := s.scan(context.Background(), "", ScanFilter{}, trigger, "scheduled-preflight-refresh", "")
		if !errors.Is(err, sentinel) {
			t.Fatalf("uncompleted/manual scan did not refresh: %v", err)
		}
	}
	if probe.calls != 2 {
		t.Fatalf("refresh calls = %d, want 2", probe.calls)
	}
	t.Run("completion during refresh still checked in transaction", func(t *testing.T) {
		probe.refresh = func(context.Context) error { insertCompletedPreflightBatch(t, pool, date); return nil }
		_, err := s.scan(context.Background(), "", ScanFilter{}, scanTrigger{Type: scanTriggerScheduled, ScheduledFor: date}, "scheduled-preflight-race", "")
		if !errors.Is(err, errScheduledScanCompleted) {
			t.Fatalf("concurrent completion: %v", err)
		}
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM data_quality_scan_batches WHERE trigger_type='scheduled' AND scheduled_for=$1::date`, date).Scan(&count); err != nil || count != 1 {
			t.Fatalf("batch count = %d, err=%v", count, err)
		}
	})
	t.Run("advisory lock still excludes other instances", func(t *testing.T) {
		var otherDate string
		if err := pool.QueryRow(context.Background(), `SELECT ($1::date + 1)::text`, date).Scan(&otherDate); err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(context.Background())
		if _, err := tx.Exec(context.Background(), `SELECT pg_advisory_xact_lock($1)`, autoScanAdvisoryLockKey); err != nil {
			t.Fatal(err)
		}
		probe.refresh = func(context.Context) error { return nil }
		_, err = s.scan(context.Background(), "", ScanFilter{}, scanTrigger{Type: scanTriggerScheduled, ScheduledFor: otherDate}, "scheduled-preflight-lock", "")
		if !errors.Is(err, errScheduledScanLocked) {
			t.Fatalf("other instance lock: %v", err)
		}
	})
}

type scheduledBackupGateProbe struct {
	backup.WriteGate
	left chan struct{}
	once sync.Once
}

func (g *scheduledBackupGateProbe) LeaveWrite() {
	g.WriteGate.LeaveWrite()
	g.once.Do(func() { close(g.left) })
}

func TestCompletedScheduledScanReleasesBackupGateWithoutNetwork(t *testing.T) {
	pool, _ := scheduledPreflightFixture(t)
	// This dedicated development database has no running scheduler. Preserve any
	// pre-existing batch, and clean only a row actually inserted by this test.
	date := time.Now().UTC().Format("2006-01-02")
	var existing bool
	if err := pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM data_quality_scan_batches WHERE trigger_type='scheduled' AND scheduled_for=$1::date)`, date).Scan(&existing); err != nil {
		t.Fatal(err)
	}
	if !existing {
		insertCompletedPreflightBatch(t, pool, date)
	}
	s := NewService(pool)
	probe := &scheduledRefreshProbe{refresh: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}
	s.observationRefresher = probe
	schedule, err := NewAutoScanSchedule(true, 0, 0, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	gate := &scheduledBackupGateProbe{left: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runAutoScan(ctx, schedule, gate, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-gate.left:
		if !gate.TryBeginBackup() {
			t.Fatal("completed scheduled scan still holds backup gate")
		}
		gate.EndBackup()
		if probe.calls != 0 {
			t.Errorf("completed scheduler called external network %d times", probe.calls)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("completed scheduled scan waits for external network while holding backup gate")
	}
}
