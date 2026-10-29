package securityworkorder_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/securityworkorder"
)

type importFileFixture struct {
	pool                  *pgxpool.Pool
	url, org, user, value string
	actor                 auth.Principal
}

func newImportFileFixture(t *testing.T) importFileFixture {
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
	f := importFileFixture{pool: pool, url: url}
	t.Cleanup(func() { pool.Close() })
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM notification_events WHERE actor_user_id=$1::uuid`,
			`DELETE FROM audit_logs WHERE user_id=$1::uuid`,
			`DELETE FROM security_finding_imports WHERE created_by=$1::uuid`,
			`DELETE FROM asset_candidates WHERE created_by=$1::uuid`,
			`DELETE FROM security_findings WHERE created_by=$1::uuid`,
			`DELETE FROM assets WHERE created_by=$1::uuid`,
			`DELETE FROM users WHERE id=$1::uuid`,
		} {
			if f.user != "" {
				if _, err := pool.Exec(ctx, query, f.user); err != nil {
					t.Errorf("clean own file fixture: %v", err)
				}
			}
		}
		if f.org != "" {
			if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, f.org); err != nil {
				t.Errorf("clean own organization: %v", err)
			}
		}
	})
	stamp := fmt.Sprint(time.Now().UnixNano())
	id := func(query string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.org = id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'文件事务验证','subsidiary') RETURNING id::text`, "FILE_TX_"+stamp)
	f.user = id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','文件事务验证员') RETURNING id::text`, "file_tx_"+stamp)
	f.value = "file-tx-" + stamp + ".example.com"
	assetID := id(`INSERT INTO assets(asset_type,asset_value,normalized_value,created_by) VALUES('domain',$1,$1,$2::uuid) RETURNING id::text`, f.value, f.user)
	if _, err := pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, f.org); err != nil {
		t.Fatal(err)
	}
	f.actor = auth.Principal{UserID: f.user, Roles: []string{"security_operator"}, Permissions: []string{"security_finding.manage"}}
	return f
}

func (f importFileFixture) csv(rows ...string) string {
	text := "资产类型,资产值,漏洞标题,严重等级,漏洞描述\n"
	for _, row := range rows {
		text += "domain," + f.value + "," + row + "\n"
	}
	return text
}

func (f importFileFixture) assertCounts(t *testing.T, findings, imports, audits, items int) {
	t.Helper()
	var gotFindings, gotImports, gotAudits, gotItems int
	if err := f.pool.QueryRow(context.Background(), `SELECT
 (SELECT count(*) FROM security_findings WHERE created_by=$1::uuid),
 (SELECT count(*) FROM security_finding_imports WHERE created_by=$1::uuid),
 (SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid),
 (SELECT COALESCE(sum(item_count),0) FROM notification_events WHERE actor_user_id=$1::uuid)`, f.user).Scan(&gotFindings, &gotImports, &gotAudits, &gotItems); err != nil {
		t.Fatal(err)
	}
	if gotFindings != findings || gotImports != imports || gotAudits != audits || gotItems != items {
		t.Fatalf("persisted findings/imports/audits/notification items = %d/%d/%d/%d, want %d/%d/%d/%d", gotFindings, gotImports, gotAudits, gotItems, findings, imports, audits, items)
	}
}

func TestFindingImportFileTransactionKeepsRowBusinessErrors(t *testing.T) {
	f := newImportFileFixture(t)
	svc := securityworkorder.NewService(f.pool)
	result, err := svc.ImportFindings(context.Background(), f.actor, "", "mixed.csv", strings.NewReader(f.csv(
		"first,high,合法一",
		" FIRST ,medium,同一文件规范化重复",
		"invalid,not-a-severity,非法严重等级",
		"second,low,合法二",
	)), "file-mixed", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 4 || result.SuccessCount != 2 || result.DuplicateCount != 1 || result.FailureCount != 1 || result.Status != "partial" || len(result.Errors) != 2 {
		t.Fatalf("mixed result: %+v", result)
	}
	f.assertCounts(t, 2, 1, 3, 2)
	var total, success, duplicate, failure int
	if err := f.pool.QueryRow(context.Background(), `SELECT total_count,success_count,duplicate_count,failure_count FROM security_finding_imports WHERE id=$1::uuid`, result.ID).Scan(&total, &success, &duplicate, &failure); err != nil {
		t.Fatal(err)
	}
	if total != 4 || success != 2 || duplicate != 1 || failure != 1 {
		t.Fatalf("history counts: %d/%d/%d/%d", total, success, duplicate, failure)
	}
}

// Force both real transactions past the pre-insert duplicate check. One INSERT
// must then wait for the competing commit and receive PostgreSQL's actual 23505;
// its savepoint must recover so the remaining rows and history can still commit.
type importInsertBarrier struct {
	arrived    chan struct{}
	release    <-chan struct{}
	once       atomic.Bool
	seen       atomic.Int32
	occurrence int32
	deadlocks  atomic.Int32
	match      string
}

func (b *importInsertBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	match := b.match
	if match == "" {
		match = "insert into security_findings"
	}
	if strings.Contains(strings.ToLower(data.SQL), match) && b.seen.Add(1) == b.occurrence && b.once.CompareAndSwap(false, true) {
		b.arrived <- struct{}{}
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (b *importInsertBarrier) TraceQueryEnd(_ context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	var pgError *pgconn.PgError
	if errors.As(data.Err, &pgError) && pgError.Code == "40P01" {
		b.deadlocks.Add(1)
	}
}

func TestFindingImportConcurrentFileTransactionsRecoverUniqueRace(t *testing.T) {
	runFindingImportFileRace(t, false, false)
}
func TestFindingImportConcurrentFileTransactionsRecoverReversedOrderDeadlock(t *testing.T) {
	runFindingImportFileRace(t, true, false)
}
func TestFindingImportConcurrentFileCandidateTransactionsRecoverReversedOrderDeadlock(t *testing.T) {
	runFindingImportFileRace(t, true, true)
}
func runFindingImportFileRace(t *testing.T, reverse, candidates bool) {
	f := newImportFileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	type outcome struct {
		result securityworkorder.FindingImportResult
		err    error
	}
	results := make(chan outcome, 2)
	tracers := make([]*importInsertBarrier, 0, 2)
	var lockTx pgx.Tx
	applicationName := "import-file-race-" + f.user
	if reverse && !candidates {
		var err error
		lockTx, err = f.pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = lockTx.Rollback(context.Background()) }()
		if _, err := lockTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		config, err := pgxpool.ParseConfig(f.url)
		if err != nil {
			t.Fatal(err)
		}
		config.MaxConns = 1
		config.ConnConfig.RuntimeParams["application_name"] = applicationName
		occurrence := int32(1)
		match := "insert into security_findings"
		if candidates {
			occurrence = 2
			match = "insert into asset_candidates"
		}
		tracer := &importInsertBarrier{arrived: arrived, release: release, occurrence: occurrence, match: match}
		tracers = append(tracers, tracer)
		config.ConnConfig.Tracer = tracer
		pool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		// Close after both workers exit, even if the assertion below fails.
		defer pool.Close()
		go func(pool *pgxpool.Pool, index int) {
			rows := []string{"first,high,并发一", "second,medium,并发二"}
			if reverse && index == 1 {
				rows[0], rows[1] = rows[1], rows[0]
			}
			payload := f.csv(rows...)
			if candidates {
				candidateRows := []string{"domain,unknown-one-" + f.value + ",first,high,未知资产一", "domain,unknown-two-" + f.value + ",second,low,未知资产二"}
				if index == 1 {
					candidateRows[0], candidateRows[1] = candidateRows[1], candidateRows[0]
				}
				payload = "资产类型,资产值,漏洞标题,严重等级,漏洞描述\n" + strings.Join(candidateRows, "\n") + "\n"
			}
			result, err := securityworkorder.NewService(pool).ImportFindings(ctx, f.actor, "", "race.csv", strings.NewReader(payload), fmt.Sprintf("file-race-%d", index), "")
			results <- outcome{result, err}
		}(pool, i)
	}
	ready := 0
	for ready < 2 {
		select {
		case <-arrived:
			ready++
		case <-ctx.Done():
			close(release)
			t.Fatal("both imports did not reach the INSERT barrier")
		}
	}
	close(release)
	if reverse && !candidates {
		// The existing derived-status trigger takes a global remediation lock.
		// Hold it until both INSERTs have entered the trigger: both identity index
		// entries then exist, and releasing it deterministically creates the cycle.
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting int
			if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid WHERE a.application_name=$1 AND l.locktype='advisory' AND NOT l.granted`, applicationName).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting == 2 {
				break
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				t.Fatal("both inserts did not block on the derived-status lock")
			}
		}
		if err := lockTx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
	}
	success, duplicates := 0, 0
	for i := 0; i < 2; i++ {
		var out outcome
		select {
		case out = <-results:
		case <-ctx.Done():
			t.Fatal("imports did not finish after releasing the barrier")
		}
		if out.err != nil {
			t.Fatalf("file transaction failed after real uniqueness race: %v", out.err)
		}
		accounted := out.result.SuccessCount + out.result.DuplicateCount
		if candidates {
			accounted = out.result.PendingCount
		}
		if out.result.TotalCount != 2 || accounted != 2 || out.result.ID == "" || out.result.FailureCount != 0 {
			t.Fatalf("race result: %+v", out.result)
		}
		success += out.result.SuccessCount
		duplicates += out.result.DuplicateCount
	}
	if candidates {
		if success+duplicates != 0 {
			t.Fatalf("unknown candidates became findings/duplicates: %d/%d", success, duplicates)
		}
		f.assertCounts(t, 0, 2, 2, 0)
		var assetCandidates, findingCandidates int
		if err := f.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM asset_candidates WHERE created_by=$1::uuid),(SELECT count(*) FROM security_finding_import_candidates WHERE finding_import_id IN (SELECT id FROM security_finding_imports WHERE created_by=$1::uuid))`, f.user).Scan(&assetCandidates, &findingCandidates); err != nil {
			t.Fatal(err)
		}
		if assetCandidates != 2 || findingCandidates != 4 {
			t.Fatalf("candidate deadlock retry leaked/missed rows: assets=%d findings=%d", assetCandidates, findingCandidates)
		}
	} else {
		if success != 2 || duplicates != 2 {
			t.Fatalf("race totals success=%d duplicate=%d", success, duplicates)
		}
		f.assertCounts(t, 2, 2, 4, 2)
	}
	if reverse && tracers[0].deadlocks.Load()+tracers[1].deadlocks.Load() == 0 {
		t.Fatal("reversed import did not exercise an actual PostgreSQL deadlock")
	}
}

func TestFindingImportDatabaseErrorDoesNotBecomeRowFailure(t *testing.T) {
	f := newImportFileFixture(t)
	// Invalid inet input is a genuine PostgreSQL audit-write error, not an
	// anticipated finding validation/duplicate error. No finding may survive it.
	_, err := securityworkorder.NewService(f.pool).ImportFindings(context.Background(), f.actor, "", "error.csv", strings.NewReader(f.csv("first,high,合法字段")), "file-db-error", "invalid-ip-address")
	if err == nil {
		t.Fatal("database audit error was swallowed")
	}
	f.assertCounts(t, 0, 0, 0, 0)
	result, err := securityworkorder.NewService(f.pool).ImportFindings(context.Background(), f.actor, "", "error.csv", strings.NewReader(f.csv("first,high,合法字段")), "file-db-retry", "")
	if err != nil || result.SuccessCount != 1 || result.DuplicateCount != 0 {
		t.Fatalf("retry after database error: %+v, %v", result, err)
	}
	f.assertCounts(t, 1, 1, 2, 1)
}

// Explicit capacity run: keep thousands of audited writes out of every quick
// package invocation, while retaining a reproducible database regression.
func TestFindingImportFileTransactionCapacityAndMidFileCancellation(t *testing.T) {
	if os.Getenv("NETASSETS_TEST_IMPORT_CAPACITY") != "1" {
		t.Skip("set NETASSETS_TEST_IMPORT_CAPACITY=1 for the 5000-row database capacity regression")
	}
	f := newImportFileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var csv strings.Builder
	csv.WriteString("资产类型,资产值,漏洞标题,严重等级,漏洞描述\n")
	description := strings.Repeat("x", 2000)
	for i := 0; i < securityworkorder.MaxFindingImportRows; i++ {
		fmt.Fprintf(&csv, "domain,%s,capacity-%04d,high,%s\n", f.value, i, description)
	}
	payload := csv.String()
	if len(payload) < 9*1024*1024 || len(payload) > 10*1024*1024 {
		t.Fatalf("capacity payload must be near, but below, 10MiB: %d", len(payload))
	}
	config, err := pgxpool.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	canceledCtx, cancelMidFile := context.WithCancel(ctx)
	defer cancelMidFile()
	tracer := &importCancelTracer{match: "insert into security_findings", occurrence: 2501, cancel: cancelMidFile}
	config.MaxConns = 1
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	started := time.Now()
	_, err = securityworkorder.NewService(pool).ImportFindings(canceledCtx, f.actor, "", "capacity.csv", strings.NewReader(payload), "capacity-canceled", "")
	if err == nil || !tracer.fired.Load() {
		t.Fatalf("2501st-row cancellation was not exercised: %v", err)
	}
	f.assertCounts(t, 0, 0, 0, 0)
	t.Logf("%d bytes / 5000 rows: canceled at finding 2501, complete rollback in %s", len(payload), time.Since(started))
	svc := securityworkorder.NewService(f.pool)
	started = time.Now()
	result, err := svc.ImportFindings(ctx, f.actor, "", "capacity.csv", strings.NewReader(payload), "capacity-retry", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalCount != 5000 || result.SuccessCount != 5000 || result.Status != "completed" || result.DuplicateCount+result.FailureCount+result.PendingCount != 0 {
		t.Fatalf("capacity retry result: %+v", result)
	}
	f.assertCounts(t, 5000, 1, 5001, 5000)
	t.Logf("5000 exact-match rows retried and committed in %s", time.Since(started))
	started = time.Now()
	result, err = svc.ImportFindings(ctx, f.actor, "", "capacity.csv", strings.NewReader(payload), "capacity-repeat", "")
	if err != nil || result.SuccessCount != 0 || result.DuplicateCount != 5000 || result.TotalCount != 5000 || result.FailureCount != 0 {
		t.Fatalf("capacity reupload result: %+v, %v", result, err)
	}
	f.assertCounts(t, 5000, 2, 5002, 5000)
	t.Logf("5000-row successful reupload deduplicated in %s", time.Since(started))
	_, err = svc.ImportFindings(ctx, f.actor, "", "capacity.csv", strings.NewReader(payload+"domain,"+f.value+",over-limit,high,超过行数\n"), "capacity-5001", "")
	if err == nil {
		t.Fatal("5001 rows accepted")
	}
	f.assertCounts(t, 5000, 2, 5002, 5000)
}

type importReferenceChangeTracer struct {
	change func() error
	once   atomic.Bool
	err    error
}

func (tracer *importReferenceChangeTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.EqualFold(strings.TrimSpace(data.SQL), "begin") && tracer.once.CompareAndSwap(false, true) {
		tracer.err = tracer.change()
	}
	return ctx
}
func (*importReferenceChangeTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {
}

func TestFindingImportFileTransactionRevalidatesReferencesInsideTransaction(t *testing.T) {
	f := newImportFileFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	config, err := pgxpool.ParseConfig(f.url)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 1
	tracer := &importReferenceChangeTracer{change: func() error {
		_, err := f.pool.Exec(ctx, `UPDATE organizations SET status='disabled' WHERE id=$1::uuid`, f.org)
		return err
	}}
	config.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	// The scope snapshot included this organization while it was enabled. Disable
	// it only after the snapshot was loaded, before the file transaction begins.
	result, err := securityworkorder.NewService(pool).ImportFindings(ctx, f.actor, "", "changed.csv", strings.NewReader(f.csv("first,high,引用一", "second,low,引用二")), "file-reference-changed", "")
	if err != nil || tracer.err != nil || !tracer.once.Load() {
		t.Fatalf("reference change test: result=%+v err=%v tracer=%v", result, err, tracer.err)
	}
	if result.Status != "failed" || result.SuccessCount != 0 || result.FailureCount != 2 || len(result.Errors) != 2 {
		t.Fatalf("changed references were accepted: %+v", result)
	}
	for _, row := range result.Errors {
		if row.Code != "reference_changed" {
			t.Fatalf("reference change reported as %q", row.Code)
		}
	}
	f.assertCounts(t, 0, 1, 1, 0)
	if _, err := f.pool.Exec(ctx, `UPDATE organizations SET status='enabled' WHERE id=$1::uuid`, f.org); err != nil {
		t.Fatal(err)
	}
	result, err = securityworkorder.NewService(f.pool).ImportFindings(ctx, f.actor, "", "changed.csv", strings.NewReader(f.csv("first,high,引用一", "second,low,引用二")), "file-reference-restored", "")
	if err != nil || result.SuccessCount != 2 || result.FailureCount != 0 {
		t.Fatalf("restored reference retry: %+v, %v", result, err)
	}
	f.assertCounts(t, 2, 2, 4, 2)
}

func TestFindingImportDatabaseErrorAfterEarlierRowRollsBackWholeFile(t *testing.T) {
	f := newImportFileFixture(t)
	svc := securityworkorder.NewService(f.pool)
	payload := f.csv("first,high,前行已执行写入", "second,low,数据库文本包含\x00字节")
	_, err := svc.ImportFindings(context.Background(), f.actor, "", "db-text-error.csv", strings.NewReader(payload), "file-db-error-after-first", "")
	var pgError *pgconn.PgError
	if !errors.As(err, &pgError) || pgError.Code != "22021" {
		t.Fatalf("expected genuine PostgreSQL text error on the second row, got %v", err)
	}
	f.assertCounts(t, 0, 0, 0, 0)
	result, err := svc.ImportFindings(context.Background(), f.actor, "", "db-text-error.csv", strings.NewReader(strings.ReplaceAll(payload, "\x00", "已更正")), "file-db-text-retry", "")
	if err != nil || result.SuccessCount != 2 || result.DuplicateCount != 0 || result.FailureCount != 0 {
		t.Fatalf("retry after second-row database error: %+v, %v", result, err)
	}
	f.assertCounts(t, 2, 1, 3, 2)
}
