package dataquality

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
)

func TestExpiredExemptionsReopenWithoutScan(t *testing.T) {
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
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	var orgID, batchID, ruleID string
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM audit_logs WHERE organization_id=$1::uuid`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_issues WHERE rule_id=$1::uuid`, ruleID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_rules WHERE id=$1::uuid`, ruleID)
		_, _ = pool.Exec(ctx, `DELETE FROM data_quality_scan_batches WHERE id=$1::uuid`, batchID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, orgID)
	})
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'豁免到期回归','subsidiary') RETURNING id::text`, "EXP_"+stamp).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_scan_batches(organization_id) VALUES($1::uuid) RETURNING id::text`, orgID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	// Disabled rule and nonexistent resource prove that expiry does not depend on a rule re-match.
	if err := pool.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,resource_type,rule_type,target_field,severity,is_enabled) VALUES('到期回归','asset','required',$1,'warning',false) RETURNING id::text`, "custom:expiry_"+stamp).Scan(&ruleID); err != nil {
		t.Fatal(err)
	}
	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	cases := []struct {
		name, status string
		deadline     *time.Time
		want         string
	}{
		{"expired", "exempted", ptrTime(cutoff.Add(-time.Second)), "pending"},
		{"exact boundary", "exempted", &cutoff, "pending"},
		{"future", "exempted", ptrTime(cutoff.Add(time.Hour)), "exempted"},
		{"permanent", "exempted", nil, "exempted"},
		{"closed", "closed", ptrTime(cutoff.Add(-time.Hour)), "closed"},
	}
	ids := make([]string, len(cases))
	for i, tc := range cases {
		if err := pool.QueryRow(ctx, `INSERT INTO data_quality_issues(rule_id,resource_type,resource_id,resource_name,organization_id,organization_code,organization_name,rule_name,target_field,severity,status,exemption_reason,exemption_expires_at,resolution_type,closed_at,first_detected_batch_id,last_detected_batch_id)
   SELECT id,resource_type,gen_random_uuid(),$1,$2::uuid,'EXP','到期组织',rule_name,target_field,severity,$3,'原豁免原因',$4,'exempted',now(),$5::uuid,$5::uuid FROM data_quality_rules WHERE id=$6::uuid RETURNING id::text`, tc.name, orgID, tc.status, tc.deadline, batchID, ruleID).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	s := NewService(pool)
	// Concurrent worker instances must not duplicate events or audit entries.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.expireExemptions(ctx, cutoff); err != nil {
				t.Errorf("expire: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := s.expireExemptions(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{UserID: "00000000-0000-4000-8000-000000000001", Roles: []string{auth.RoleGroupAdmin}}
	for i, tc := range cases {
		detail, err := s.GetIssue(ctx, p, ids[i])
		if err != nil {
			t.Fatal(err)
		}
		item := detail.Issue
		if item.Status != tc.want {
			t.Errorf("%s: status=%s, want %s", tc.name, item.Status, tc.want)
		}
		var eventCount, auditCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_issue_events WHERE issue_id=$1::uuid AND event_type='reopened' AND actor_user_id IS NULL`, ids[i]).Scan(&eventCount); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE resource_id=$1 AND action='data_quality_issue.exemption_expired' AND user_id IS NULL`, ids[i]).Scan(&auditCount); err != nil {
			t.Fatal(err)
		}
		wantEvents := 0
		if tc.want == "pending" {
			wantEvents = 1
			if item.ClosedAt != nil || item.ResolutionType != "" || item.ExemptionExpiresAt != nil || item.ExemptionReason != "" {
				t.Errorf("%s: stale exemption metadata: %+v", tc.name, item)
			}
			var reason string
			if err := pool.QueryRow(ctx, `SELECT details->>'exemption_reason' FROM data_quality_issue_events WHERE issue_id=$1::uuid AND event_type='reopened'`, ids[i]).Scan(&reason); err != nil || reason != "原豁免原因" {
				t.Errorf("historical reason=%q, err=%v", reason, err)
			}
		}
		if eventCount != wantEvents || auditCount != wantEvents {
			t.Errorf("%s: events=%d audits=%d, want %d", tc.name, eventCount, auditCount, wantEvents)
		}
	}
	result, summary, err := s.SearchIssues(ctx, p, IssueFilter{OrganizationID: orgID, Status: "pending"})
	if err != nil || result.Total != 2 || summary.Pending != 2 {
		t.Fatalf("pending search: total=%d summary=%+v err=%v", result.Total, summary, err)
	}
	t.Run("worker respects backup gate and retries", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE data_quality_issues SET status='exempted',exemption_reason='备份门禁恢复',exemption_expires_at=$2,resolution_type='exempted',closed_at=now() WHERE id=$1::uuid`, ids[0], cutoff); err != nil {
			t.Fatal(err)
		}
		gate := &expiryTestGate{attempted: make(chan struct{}, 1)}
		workerCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			s.runExemptionExpiry(workerCtx, gate, slog.New(slog.NewTextHandler(io.Discard, nil)), 10*time.Millisecond)
		}()
		defer func() { cancel(); <-done }()
		select {
		case <-gate.attempted:
		case <-time.After(5 * time.Second):
			t.Fatal("worker did not start")
		}
		item, err := s.GetIssue(ctx, p, ids[0])
		if err != nil || item.Issue.Status != "exempted" {
			t.Fatalf("blocked worker wrote: %+v %v", item, err)
		}
		gate.allowed.Store(true)
		deadline := time.Now().Add(5 * time.Second)
		for {
			item, err = s.GetIssue(ctx, p, ids[0])
			if err != nil {
				t.Fatal(err)
			}
			if item.Issue.Status == "pending" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("worker did not retry after backup gate opened")
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		<-done
		if gate.entered.Load() != gate.left.Load() {
			t.Fatalf("gate leak: entered=%d left=%d", gate.entered.Load(), gate.left.Load())
		}
	})
}

type expiryTestGate struct {
	allowed   atomic.Bool
	entered   atomic.Int64
	left      atomic.Int64
	attempted chan struct{}
}

func (g *expiryTestGate) TryEnterWrite() bool {
	select {
	case g.attempted <- struct{}{}:
	default:
	}
	if !g.allowed.Load() {
		return false
	}
	g.entered.Add(1)
	return true
}
func (g *expiryTestGate) LeaveWrite() { g.left.Add(1) }

func ptrTime(t time.Time) *time.Time { return &t }
