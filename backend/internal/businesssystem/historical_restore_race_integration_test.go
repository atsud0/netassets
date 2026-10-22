package businesssystem

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestHistoricalRestoreRejectsEditorCommittedWhileWaitingForLock(t *testing.T) {
	pool, p, org, source := dailySystemFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	svc := NewService(pool)
	input := validBusinessSystemInput()
	input.OrganizationID = org
	input.Code = "BS_RESTORE_001"
	input.Name = "Draft before restore"
	zero := 0
	input.ExpectedVersion = &zero
	draft, err := svc.RestorePublishedVersion(ctx, p, source, input, "review-initial", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err = blocker.Exec(ctx, `SELECT id FROM inventory_business_system_drafts WHERE id=$1::uuid FOR UPDATE`, draft.ID); err != nil {
		t.Fatal(err)
	}
	input.Name = "Historical snapshot"
	input.ExpectedVersion = &draft.Version
	input.ExpectedWorkingVersionID = draft.ID
	results := make(chan error, 1)
	go func() {
		_, e := svc.RestorePublishedVersion(ctx, p, source, input, "review-restore-race", "127.0.0.1")
		results <- e
	}()
	waiting := 0
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%SELECT version FROM inventory_business_system_drafts%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if waiting == 0 {
		t.Fatal("restore did not reach draft lock")
	}
	if _, err = blocker.Exec(ctx, `UPDATE inventory_business_system_drafts SET system_name='Concurrent editor saved',version=version+1,updated_at=now() WHERE id=$1::uuid`, draft.ID); err != nil {
		t.Fatal(err)
	}
	if err = blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	err = <-results
	var name string
	var version int
	if e := pool.QueryRow(ctx, `SELECT system_name,version FROM inventory_business_system_drafts WHERE id=$1::uuid`, draft.ID).Scan(&name, &version); e != nil {
		t.Fatal(e)
	}
	var audits int
	if e := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id='review-restore-race'`).Scan(&audits); e != nil {
		t.Fatal(e)
	}
	if audits != 0 {
		t.Fatalf("conflicting restore wrote %d audits", audits)
	}
	if !errors.Is(err, ErrEditConflict) || name != "Concurrent editor saved" || version != draft.Version+1 {
		t.Fatalf("restore result=%v, saved name=%q, version=%d: concurrent editor was overwritten", err, name, version)
	}
}

// Tokens bind a restore to both identity and revision, including the absence
// of a draft. No stale token may silently select a new replacement target.
func TestHistoricalRestoreRequiresConfirmedWorkingVersion(t *testing.T) {
	pool, p, org, source := dailySystemFixture(t)
	ctx := context.Background()
	svc := NewService(pool)
	input := validBusinessSystemInput()
	input.OrganizationID = org
	input.Code = "BS_RESTORE_001"
	if _, err := svc.RestorePublishedVersion(ctx, p, source, input, "missing-token", "127.0.0.1"); !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("missing token=%v", err)
	}
	zero := 0
	input.ExpectedVersion = &zero
	draft, err := svc.RestorePublishedVersion(ctx, p, source, input, "initial-token", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, id string
		version  int
	}{
		{"draft appeared after confirmation", "", 0},
		{"wrong draft identity", "11111111-1111-4111-8111-111111111111", draft.Version},
		{"stale draft version", draft.ID, draft.Version + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := input
			in.ExpectedVersion = &tc.version
			in.ExpectedWorkingVersionID = tc.id
			if _, err := svc.RestorePublishedVersion(ctx, p, source, in, "stale-token", "127.0.0.1"); !errors.Is(err, ErrEditConflict) {
				t.Fatalf("restore=%v, want edit conflict", err)
			}
		})
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT version FROM inventory_business_system_drafts WHERE id=$1::uuid`, draft.ID).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != draft.Version {
		t.Fatalf("rejected restore changed version: %d", version)
	}
}
