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
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/securityworkorder"
)

// Cancel at a real database statement, not on a wall-clock race. Only the
// import pool has this tracer; fixture/verification queries stay usable.
type importCancelTracer struct {
	match      string
	occurrence int32
	seen       atomic.Int32
	fired      atomic.Bool
	cancel     context.CancelFunc
}

func (tracer *importCancelTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(strings.ToLower(data.SQL), tracer.match) && tracer.seen.Add(1) == tracer.occurrence {
		tracer.fired.Store(true)
		tracer.cancel()
	}
	return ctx
}
func (*importCancelTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestFindingImportCancellationRollsBackWholeFileAndCanRetry(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, stage := range []struct {
		name, query string
		occurrence  int32
	}{
		{"second finding", "insert into security_findings", 2},
		{"history", "insert into security_finding_imports", 1},
		{"asset candidate", "insert into asset_candidates", 1},
		{"finding candidate", "insert into security_finding_import_candidates", 1},
		{"import audit", "'security_finding.import'", 1},
		{"commit", "commit", 1},
	} {
		t.Run(stage.name, func(t *testing.T) {
			stamp := fmt.Sprint(time.Now().UnixNano())
			var org, user, assetID string
			defer func() {
				for _, query := range []string{
					`DELETE FROM notification_events WHERE actor_user_id=$1::uuid`,
					`DELETE FROM audit_logs WHERE user_id=$1::uuid`,
					`DELETE FROM security_finding_imports WHERE created_by=$1::uuid`,
					`DELETE FROM asset_candidates WHERE created_by=$1::uuid`,
					`DELETE FROM security_findings WHERE created_by=$1::uuid`,
					`DELETE FROM assets WHERE created_by=$1::uuid`,
					`DELETE FROM users WHERE id=$1::uuid`,
				} {
					if user != "" {
						if _, err := pool.Exec(ctx, query, user); err != nil {
							t.Errorf("clean own fixture: %v", err)
						}
					}
				}
				if org != "" {
					if _, err := pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, org); err != nil {
						t.Errorf("clean own fixture: %v", err)
					}
				}
			}()
			id := func(query string, args ...any) string {
				t.Helper()
				var result string
				if err := pool.QueryRow(ctx, query, args...).Scan(&result); err != nil {
					t.Fatal(err)
				}
				return result
			}
			org = id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'导入中断原子性','subsidiary') RETURNING id::text`, "IMPORT_ATOMIC_"+stamp)
			user = id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','导入中断测试员') RETURNING id::text`, "import_atomic_"+stamp)
			value := "atomic-" + stamp + ".example.com"
			assetID = id(`INSERT INTO assets(asset_type,asset_value,normalized_value,created_by) VALUES('domain',$1,$1,$2::uuid) RETURNING id::text`, value, user)
			if _, err := pool.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, org); err != nil {
				t.Fatal(err)
			}
			actor := auth.Principal{UserID: user, Roles: []string{"security_operator"}, Permissions: []string{"security_finding.manage"}}
			payload := "资产类型,资产值,漏洞标题,严重等级,漏洞描述\n" +
				"domain," + value + ",atomic first,high,已匹配漏洞一\n" +
				"domain," + value + ",atomic second,medium,已匹配漏洞二\n" +
				"domain,unknown-" + stamp + ".example.net,atomic unknown,low,待确认资产漏洞\n"
			config, err := pgxpool.ParseConfig(url)
			if err != nil {
				t.Fatal(err)
			}
			cancelCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			tracer := &importCancelTracer{match: stage.query, occurrence: stage.occurrence, cancel: cancel}
			config.ConnConfig.Tracer = tracer
			config.MaxConns = 1
			importPool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer importPool.Close()
			svc := securityworkorder.NewService(importPool)
			_, err = svc.ImportFindings(cancelCtx, actor, "", "atomic.csv", strings.NewReader(payload), "atomic-canceled", "")
			if !tracer.fired.Load() {
				t.Fatalf("cancellation stage not exercised: %v", err)
			}
			if err == nil || (!errors.Is(err, context.Canceled) && cancelCtx.Err() == nil) {
				t.Fatalf("canceled import returned %v", err)
			}
			var findings, imports, candidates, assetCandidates, audits, events, eventItems int
			counts := func() {
				t.Helper()
				if err := pool.QueryRow(ctx, `SELECT
     (SELECT count(*) FROM security_findings WHERE created_by=$1::uuid),
     (SELECT count(*) FROM security_finding_imports WHERE created_by=$1::uuid),
     (SELECT count(*) FROM security_finding_import_candidates WHERE finding_import_id IN (SELECT id FROM security_finding_imports WHERE created_by=$1::uuid)),
     (SELECT count(*) FROM asset_candidates WHERE created_by=$1::uuid),
     (SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid),
     (SELECT count(*) FROM notification_events WHERE actor_user_id=$1::uuid),
     (SELECT COALESCE(sum(item_count),0) FROM notification_events WHERE actor_user_id=$1::uuid)`, user).Scan(&findings, &imports, &candidates, &assetCandidates, &audits, &events, &eventItems); err != nil {
					t.Fatal(err)
				}
			}
			counts()
			if findings+imports+candidates+assetCandidates+audits+events != 0 {
				t.Errorf("canceled file left partial data: findings=%d imports=%d candidates=%d asset_candidates=%d audits=%d events=%d items=%d", findings, imports, candidates, assetCandidates, audits, events, eventItems)
			}
			// A canceled connection may be discarded; a fresh pool proves recovery is
			// persisted database state, not an in-process replay cache.
			retryService := securityworkorder.NewService(pool)
			result, err := retryService.ImportFindings(ctx, actor, "", "atomic.csv", strings.NewReader(payload), "atomic-retry", "")
			if err != nil {
				t.Fatal(err)
			}
			if result.SuccessCount != 2 || result.PendingCount != 1 || result.DuplicateCount != 0 || result.TotalCount != 3 || result.Status != "partial" || result.ID == "" {
				t.Errorf("retry did not import the whole file once: %+v", result)
			}
			counts()
			if findings != 2 || imports != 1 || candidates != 1 || assetCandidates != 1 || audits != 3 || events != 1 || eventItems != 2 {
				t.Errorf("retry persistence: findings=%d imports=%d candidates=%d asset_candidates=%d audits=%d events=%d items=%d", findings, imports, candidates, assetCandidates, audits, events, eventItems)
			}
			repeated, err := retryService.ImportFindings(ctx, actor, "", "atomic.csv", strings.NewReader(payload), "atomic-reupload", "")
			if err != nil {
				t.Fatal(err)
			}
			if repeated.SuccessCount != 0 || repeated.DuplicateCount != 2 || repeated.PendingCount != 1 {
				t.Errorf("successful reupload lost duplicate protection: %+v", repeated)
			}
			counts()
			if findings != 2 || imports != 2 || candidates != 2 || assetCandidates != 1 || audits != 4 || events != 1 || eventItems != 2 {
				t.Errorf("reupload persistence: findings=%d imports=%d candidates=%d asset_candidates=%d audits=%d events=%d items=%d", findings, imports, candidates, assetCandidates, audits, events, eventItems)
			}
		})
	}
}
