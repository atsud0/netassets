package securityworkorder_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"
	"netassets/internal/securityworkorder"
)

// Opt-in diagnostic, not a replacement for browser 5000-row assertions. Never
// record SQL, bind arguments, credentials, finding text or connection strings.
type importProfileKey struct{}
type importProfileStart struct {
	class string
	at    time.Time
}
type importProfileStat struct {
	Class   string         `json:"class"`
	Count   int            `json:"count"`
	TotalMS float64        `json:"total_ms"`
	MaxMS   float64        `json:"max_ms"`
	Errors  map[string]int `json:"errors,omitempty"`
}
type importStatementProfiler struct {
	mu    sync.Mutex
	stats map[string]*importProfileStat
}

func (*importStatementProfiler) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	sql := strings.ToLower(strings.Join(strings.Fields(data.SQL), " "))
	class := "other"
	switch {
	case strings.HasPrefix(sql, "savepoint"):
		class = "row_savepoint"
	case strings.HasPrefix(sql, "release savepoint"):
		class = "row_release"
	case strings.HasPrefix(sql, "rollback"):
		class = "rollback"
	case strings.HasPrefix(sql, "begin"):
		class = "file_begin"
	case strings.HasPrefix(sql, "commit"):
		class = "file_commit"
	case strings.Contains(sql, "insert into security_findings"):
		class = "finding_insert_with_triggers"
	case strings.Contains(sql, "insert into audit_logs"):
		class = "audit_insert"
	case strings.Contains(sql, "insert into notification_events"):
		class = "notification_upsert"
	case strings.Contains(sql, "select owner_user_id::text"):
		class = "owner_lookup"
	case strings.Contains(sql, "select exists(select 1 from security_findings"):
		class = "duplicate_lookup"
	case strings.Contains(sql, "select exists") && strings.Contains(sql, "from organizations"):
		class = "organization_validation"
	case strings.Contains(sql, "select exists") && strings.Contains(sql, "from assets"):
		class = "asset_validation"
	case strings.Contains(sql, "select exists") && strings.Contains(sql, "from security_test_batches"):
		class = "batch_scope_validation"
	case strings.Contains(sql, "insert into security_finding_imports"):
		class = "history_insert"
	}
	return context.WithValue(ctx, importProfileKey{}, importProfileStart{class: class, at: time.Now()})
}
func (p *importStatementProfiler) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	start, ok := ctx.Value(importProfileKey{}).(importProfileStart)
	if !ok {
		return
	}
	ms := float64(time.Since(start.at).Microseconds()) / 1000
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stats == nil {
		p.stats = map[string]*importProfileStat{}
	}
	stat := p.stats[start.class]
	if stat == nil {
		stat = &importProfileStat{Class: start.class}
		p.stats[start.class] = stat
	}
	stat.Count++
	stat.TotalMS += ms
	if ms > stat.MaxMS {
		stat.MaxMS = ms
	}
	if data.Err != nil {
		category := "other"
		var pgErr *pgconn.PgError
		if errors.Is(data.Err, context.DeadlineExceeded) {
			category = "deadline_exceeded"
		} else if errors.Is(data.Err, context.Canceled) {
			category = "canceled"
		} else if errors.As(data.Err, &pgErr) {
			category = "pg_" + pgErr.Code
		}
		if stat.Errors == nil {
			stat.Errors = map[string]int{}
		}
		stat.Errors[category]++
	}
}
func (p *importStatementProfiler) summary() []importProfileStat {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]importProfileStat, 0, len(p.stats))
	for _, stat := range p.stats {
		result = append(result, *stat)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TotalMS > result[j].TotalMS })
	return result
}

func TestFindingImportStatementProfile(t *testing.T) {
	if os.Getenv("NETASSETS_PROFILE_FINDING_IMPORT") != "1" {
		t.Skip("opt-in statement diagnostic; run only after full browser freeze ends")
	}
	for _, format := range []string{"csv", "xlsx"} {
		t.Run(format, func(t *testing.T) {
			f := newImportFileFixture(t)
			ctx := context.Background()
			var assetID, batchID string
			if err := f.pool.QueryRow(ctx, `SELECT id::text FROM assets WHERE created_by=$1::uuid AND asset_value=$2`, f.user, f.value).Scan(&assetID); err != nil {
				t.Fatal(err)
			}
			if err := f.pool.QueryRow(ctx, `INSERT INTO security_test_batches(batch_code,batch_name,test_type,status,provider,authorization_note,start_at,end_at,created_by) VALUES($1,'导入语句耗时诊断','penetration','authorized','隔离诊断','仅限新建自有夹具',now()-interval '1 hour',now()+interval '1 day',$2::uuid) RETURNING id::text`, "PROFILE_"+fmt.Sprint(time.Now().UnixNano()), f.user).Scan(&batchID); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := f.pool.Exec(context.Background(), `DELETE FROM security_test_batches WHERE id=$1::uuid`, batchID); err != nil {
					t.Errorf("clean own profile batch: %v", err)
				}
			})
			if _, err := f.pool.Exec(ctx, `INSERT INTO security_test_scope_assets(batch_id,asset_id,asset_type_snapshot,asset_value_snapshot,organization_id,organization_code_snapshot,organization_name_snapshot,network_scope_snapshot,environment_snapshot,importance_snapshot)
   SELECT $1::uuid,$2::uuid,'domain',$3,id,organization_code,organization_name,'public','testing','normal' FROM organizations WHERE id=$4::uuid`, batchID, assetID, f.value, f.org); err != nil {
				t.Fatal(err)
			}
			headers := []string{"资产类型", "资产值", "漏洞标题", "严重等级", "漏洞描述", "发现时间"}
			var buffer bytes.Buffer
			if format == "csv" {
				buffer.WriteString(strings.Join(headers, ",") + "\n")
				for i := 0; i < 5000; i++ {
					fmt.Fprintf(&buffer, "domain,%s,语句诊断%d,high,完整原文中🙂,2026-10-28T00:00:00Z\n", f.value, i)
				}
			} else {
				file := excelize.NewFile()
				defer file.Close()
				sheet := file.GetSheetName(0)
				first := make([]any, len(headers))
				for i, h := range headers {
					first[i] = h
				}
				if err := file.SetSheetRow(sheet, "A1", &first); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 5000; i++ {
					row := []any{"domain", f.value, fmt.Sprintf("语句诊断%d", i), "high", "完整原文中🙂", "2026-10-28T00:00:00Z"}
					if err := file.SetSheetRow(sheet, fmt.Sprintf("A%d", i+2), &row); err != nil {
						t.Fatal(err)
					}
				}
				if err := file.Write(&buffer); err != nil {
					t.Fatal(err)
				}
			}
			config, err := pgxpool.ParseConfig(f.url)
			if err != nil {
				t.Fatal(err)
			}
			if config.ConnConfig.RuntimeParams == nil {
				config.ConnConfig.RuntimeParams = map[string]string{}
			}
			config.ConnConfig.RuntimeParams["jit"] = "off"
			profiler := &importStatementProfiler{}
			config.ConnConfig.Tracer = profiler
			pool, err := pgxpool.NewWithConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			// Match the existing HTTP execution budget; no relaxed deadline or retry.
			importCtx, cancel := context.WithTimeout(ctx, 110*time.Second)
			defer cancel()
			started := time.Now()
			result, importErr := securityworkorder.NewService(pool).ImportFindings(importCtx, f.actor, batchID, "profile."+format, &buffer, "profile-"+batchID, "")
			report := struct {
				Format    string              `json:"format"`
				Rows      int                 `json:"rows"`
				ElapsedMS int64               `json:"elapsed_ms"`
				Succeeded bool                `json:"succeeded"`
				Deadline  bool                `json:"deadline"`
				Queries   []importProfileStat `json:"queries"`
			}{format, 5000, time.Since(started).Milliseconds(), importErr == nil, errors.Is(importErr, context.DeadlineExceeded), profiler.summary()}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("IMPORT_STATEMENT_PROFILE %s", encoded)
			if importErr != nil {
				f.assertCounts(t, 0, 0, 0, 0)
				t.Fatalf("profile import failed after %dms (see classified timings above)", report.ElapsedMS)
			}
			if result.TotalCount != 5000 || result.SuccessCount != 5000 || result.DuplicateCount != 0 || result.FailureCount != 0 || result.PendingCount != 0 {
				t.Fatalf("unexpected profile import counts: total=%d success=%d duplicate=%d failure=%d pending=%d", result.TotalCount, result.SuccessCount, result.DuplicateCount, result.FailureCount, result.PendingCount)
			}
			f.assertCounts(t, 5000, 1, 5001, 5000)
		})
	}
}
