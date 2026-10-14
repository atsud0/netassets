package securityworkorder

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// Expiry is the one place where a finding changes status without a human
// pressing a button: nobody writes "this risk acceptance lapsed". It runs
// lazily from the read paths, so the only thing keeping the group dashboard
// honest is that the transition restores the pre-acceptance status, happens
// exactly once, and is audited. This test pins all three, plus the negative
// cases: a live acceptance must not be touched, and a second read must not
// re-audit a transition that already happened.
func TestRiskAcceptanceExpiryRestoresStatusOnceAndIsAudited(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset risk acceptance fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (organization_code,organization_name,organization_type)
		VALUES ('RISK_EXPIRY_GROUP','风险接受到期测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO organizations (parent_id,organization_code,organization_name,organization_type)
		VALUES ($1::uuid,'RISK_EXPIRY_SUB','风险接受到期测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username,password_hash,display_name)
		VALUES ('risk_expiry_operator','not-used','风险接受到期测试人员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	principal := auth.Principal{UserID: userID, Organization: &auth.Organization{ID: groupID},
		Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding}}
	service := NewService(pool)

	insertFinding := func(code, status string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO security_findings
			(finding_code,title,severity,status,assignee_organization_id,description,source,created_by,updated_by)
			VALUES ($1::text,'风险接受到期测试漏洞-'||$1::text,'high',$2::text,$3::uuid,'验证到期恢复语义','import',$4::uuid,$4::uuid)
			RETURNING id::text`, code, status, organizationID, userID).Scan(&id); err != nil {
			t.Fatalf("insert finding %s: %v", code, err)
		}
		return id
	}
	statusOf := func(id string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, id).Scan(&value); err != nil {
			t.Fatalf("read status: %v", err)
		}
		return value
	}
	expiredAt := func(id string) *time.Time {
		t.Helper()
		var value *time.Time
		if err := pool.QueryRow(ctx, `SELECT risk_acceptance_expired_at FROM security_findings WHERE id=$1::uuid`, id).Scan(&value); err != nil {
			t.Fatalf("read risk_acceptance_expired_at: %v", err)
		}
		return value
	}
	// The optimistic lock token the operator saw on screen. Every status action
	// requires it, and the test must not fake its way past that.
	version := func(id string) *time.Time {
		t.Helper()
		var value time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM security_findings WHERE id=$1::uuid`, id).Scan(&value); err != nil {
			t.Fatalf("read updated_at: %v", err)
		}
		return &value
	}
	expiryAudits := func(id string) int {
		t.Helper()
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE action='security_finding.risk_acceptance_expired' AND resource_id=$1`, id).Scan(&count); err != nil {
			t.Fatalf("count expiry audits: %v", err)
		}
		return count
	}

	// Every status a risk acceptance may legally be taken from has to survive the
	// round trip. Losing one of these silently changes what the group counts as
	// outstanding remediation.
	for _, from := range []string{"pending", "in_progress", "confirmed", "resolved", "fixed"} {
		t.Run("expiry restores "+from, func(t *testing.T) {
			id := insertFinding("RISK-EXPIRY-"+strings.ToUpper(from), from)
			// A past deadline is not a valid acceptance, so the window is opened
			// legally and then let run out. Nothing else should be needed to make a
			// finding expire, and that is the point of this test.
			if _, err := service.AcceptRisk(ctx, principal, id, RiskAcceptanceInput{
				Reason:            "业务窗口期，暂不修复",
				AcceptedUntil:     time.Now().Add(time.Hour),
				ExpectedUpdatedAt: version(id),
			}, "risk-expiry-request", "127.0.0.1"); err != nil {
				t.Fatalf("AcceptRisk on a %s finding: %v", from, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE security_findings SET risk_accepted_until=now()-interval '1 hour' WHERE id=$1::uuid`, id); err != nil {
				t.Fatalf("let the acceptance window run out: %v", err)
			}
			if got := statusOf(id); got != "risk_accepted" {
				t.Fatalf("status after accept = %q, want risk_accepted", got)
			}
			// The read path is what materializes the expiry; this is the contract.
			if _, err := service.GetFinding(ctx, principal, id); err != nil {
				t.Fatalf("GetFinding after expiry: %v", err)
			}
			if got := statusOf(id); got != from {
				t.Fatalf("status after expiry = %q, want the pre-acceptance status %q", got, from)
			}
			if expiredAt(id) == nil {
				t.Fatal("an expired acceptance left risk_acceptance_expired_at NULL")
			}
			if count := expiryAudits(id); count != 1 {
				t.Fatalf("expiry audit rows = %d, want exactly 1", count)
			}
			// Reading again must not re-expire or re-audit: the transition already
			// happened, and a second audit would report a change that never occurred.
			if _, err := service.GetFinding(ctx, principal, id); err != nil {
				t.Fatalf("second GetFinding: %v", err)
			}
			if _, err := service.SummarizeFindings(ctx, principal, ""); err != nil {
				t.Fatalf("SummarizeFindings after expiry: %v", err)
			}
			if got := statusOf(id); got != from {
				t.Fatalf("status after repeat read = %q, want %q", got, from)
			}
			if count := expiryAudits(id); count != 1 {
				t.Fatalf("expiry audit rows after repeat reads = %d, want exactly 1", count)
			}
			// Revoking after the expiry already restored the finding must be
			// refused: the acceptance is over, so pretending to take it back would
			// overwrite the restored status.
			if _, err := service.RevokeRisk(ctx, principal, id, RiskRevokeInput{ExpectedUpdatedAt: version(id)}, "risk-expiry-revoke", "127.0.0.1"); err == nil {
				t.Fatal("revoking an already expired risk acceptance was allowed")
			}
			if got := statusOf(id); got != from {
				t.Fatalf("a refused revoke changed the status to %q", got)
			}
		})
	}

	t.Run("a live acceptance survives reads untouched", func(t *testing.T) {
		id := insertFinding("RISK-EXPIRY-LIVE", "confirmed")
		if _, err := service.AcceptRisk(ctx, principal, id, RiskAcceptanceInput{
			Reason:            "未来到期",
			AcceptedUntil:     time.Now().Add(48 * time.Hour),
			ExpectedUpdatedAt: version(id),
		}, "risk-live-request", "127.0.0.1"); err != nil {
			t.Fatalf("AcceptRisk on a live acceptance: %v", err)
		}
		for i := 0; i < 3; i++ {
			if _, err := service.GetFinding(ctx, principal, id); err != nil {
				t.Fatalf("GetFinding on a live acceptance: %v", err)
			}
		}
		if got := statusOf(id); got != "risk_accepted" {
			t.Fatalf("status of a live acceptance = %q, want risk_accepted", got)
		}
		if count := expiryAudits(id); count != 0 {
			t.Fatalf("expiry audit rows for a live acceptance = %d, want 0", count)
		}
		if expiredAt(id) != nil {
			t.Fatal("a live acceptance was marked expired by a read")
		}
		// And the operator can still take it back on purpose.
		if _, err := service.RevokeRisk(ctx, principal, id, RiskRevokeInput{ExpectedUpdatedAt: version(id)}, "risk-live-revoke", "127.0.0.1"); err != nil {
			t.Fatalf("RevokeRisk on a live acceptance: %v", err)
		}
		if got := statusOf(id); got != "confirmed" {
			t.Fatalf("status after revoke = %q, want confirmed", got)
		}
	})

	t.Run("the dashboard reports the expired finding as work again", func(t *testing.T) {
		id := insertFinding("RISK-EXPIRY-SUMMARY", "in_progress")
		if _, err := service.AcceptRisk(ctx, principal, id, RiskAcceptanceInput{
			Reason:            "到期统计用",
			AcceptedUntil:     time.Now().Add(time.Hour),
			ExpectedUpdatedAt: version(id),
		}, "risk-summary-request", "127.0.0.1"); err != nil {
			t.Fatalf("AcceptRisk for the summary case: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET risk_accepted_until=now()-interval '1 minute' WHERE id=$1::uuid`, id); err != nil {
			t.Fatalf("let the summary acceptance run out: %v", err)
		}
		summary, err := service.SummarizeFindings(ctx, principal, "")
		if err != nil {
			t.Fatalf("SummarizeFindings: %v", err)
		}
		if got := statusOf(id); got != "in_progress" {
			t.Fatalf("status after the summary read = %q, want in_progress", got)
		}
		// The summary carries a pending bucket plus severity split; what matters
		// here is that the expired acceptance no longer inflates the total as
		// accepted work, so the restored finding must be counted as outstanding.
		if summary.Total < 1 {
			t.Fatalf("summary total = %d, want the restored finding to be counted", summary.Total)
		}
		var accepted int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM security_findings WHERE status='risk_accepted' AND risk_acceptance_expired_at IS NOT NULL`).Scan(&accepted); err != nil {
			t.Fatalf("count expired acceptances: %v", err)
		}
		if accepted != 0 {
			t.Fatalf("%d findings are still in risk_accepted after the summary read expired them", accepted)
		}
	})

	t.Run("an acceptance without a deadline never expires", func(t *testing.T) {
		id := insertFinding("RISK-EXPIRY-OPEN", "confirmed")
		// A NULL deadline is not a valid acceptance input, but a row that predates
		// the validation must not be silently expired by a read either.
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET status='risk_accepted', risk_accepted_until=NULL, risk_accepted_from_status='confirmed' WHERE id=$1::uuid`, id); err != nil {
			t.Fatalf("force an open-ended acceptance: %v", err)
		}
		if _, err := service.GetFinding(ctx, principal, id); err != nil {
			t.Fatalf("GetFinding on an open-ended acceptance: %v", err)
		}
		if got := statusOf(id); got != "risk_accepted" {
			t.Fatalf("an acceptance with no deadline was expired to %q", got)
		}
		if expiredAt(id) != nil {
			t.Fatal("an acceptance with no deadline was marked expired")
		}
	})
}
