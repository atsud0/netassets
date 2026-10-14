package notification_test

import (
	"strings"
	"testing"
	"time"

	"netassets/internal/notification"
)

// Two expiry driven behaviours have to stay consistent with the data they read:
// the risk acceptance reminders must not re-notify on every scan, and a vendor
// grant that has run out must take effect immediately rather than when somebody
// next refreshes a cache.
func TestRiskAcceptanceNotificationsAreKeyedAndNotRepeated(t *testing.T) {
	f := setupNotificationScope(t)
	ctx := f.ctx

	var systemID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO business_systems (system_code,system_name,owner_organization_id,system_status)
		VALUES ($1,'风险接受通知系统',$2::uuid,'running')
		RETURNING id::text`, "RISK_SYS_"+time.Now().Format("150405"), f.orgA).Scan(&systemID); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	code := "RISK_F_" + time.Now().Format("150405")
	var findingID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO security_findings (finding_code,title,severity,status,assignee_organization_id,
		                               business_system_id,description,source,created_by,updated_by,
		                               risk_accepted_until,risk_accepted_from_status)
		VALUES ($1,'风险接受到期通知验证','high','risk_accepted',$2::uuid,$3::uuid,'用于验证通知去重','manual',$4::uuid,$4::uuid,
		        now()-interval '1 day','confirmed')
		RETURNING id::text`, code, f.orgA, systemID, f.groupAdmin.UserID).Scan(&findingID); err != nil {
		t.Fatalf("insert risk accepted finding: %v", err)
	}

	// Repeated scans must not create new rows: the notification key is stable and
	// marking read is idempotent, so the row count stays at one per key.
	countRiskReads := func(prefix string) int {
		t.Helper()
		var count int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM notification_reads WHERE notification_key LIKE $1`, prefix+"%").Scan(&count); err != nil {
			t.Fatalf("count risk notification read rows: %v", err)
		}
		return count
	}

	// The expired acceptance is read again on every scan; the notification key is
	// derived from the acceptance deadline, so repeated scans produce the same
	// key instead of a fresh item each time.
	first, _, err := f.notes.Search(f.ctx, f.securityAdmin, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("first Search error = %v", err)
	}
	expiredKey := "finding:risk_expired:" + findingID + ":"
	foundExpired := false
	for _, item := range first.Items {
		if strings.HasPrefix(item.Key, expiredKey) {
			foundExpired = true
		}
	}
	if !foundExpired {
		t.Fatalf("expired risk acceptance produced no notification; keys=%+v", keysOf(first.Items))
	}
	second, _, err := f.notes.Search(f.ctx, f.securityAdmin, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("second Search error = %v", err)
	}
	seen := map[string]bool{}
	for _, item := range second.Items {
		if seen[item.Key] {
			t.Fatalf("duplicate notification key %q in one page; keys=%+v", item.Key, keysOf(second.Items))
		}
		seen[item.Key] = true
	}
	if err := f.notes.MarkAllRead(f.ctx, f.securityAdmin); err != nil {
		t.Fatalf("MarkAllRead error = %v", err)
	}
	before := countRiskReads(expiredKey)
	for range 2 {
		if _, _, err := f.notes.Search(f.ctx, f.securityAdmin, notification.SearchInput{Page: notificationPage()}); err != nil {
			t.Fatalf("repeat Search error = %v", err)
		}
		if err := f.notes.MarkAllRead(f.ctx, f.securityAdmin); err != nil {
			t.Fatalf("repeat MarkAllRead error = %v", err)
		}
	}
	if after := countRiskReads(expiredKey); after != before {
		t.Fatalf("risk notification read rows changed from %d to %d across scans; keys must be stable", before, after)
	}
}

func keysOf(items []notification.Item) []string {
	keys := make([]string, 0, len(items))
	for _, item := range items {
		keys = append(keys, item.Key)
	}
	return keys
}

func TestExpiredGrantRemovesAccessImmediately(t *testing.T) {
	f := setupNotificationScope(t)
	ctx := f.ctx

	systemID := systemOfTask(t, f)
	assigneeUser := f.vendorA.UserID
	var grantID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO remediation_grants (user_id,organization_id,business_system_id,participant_type,
		                             status,valid_from,valid_until,sponsor_user_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'internal','enabled',now()-interval '5 days',
		        now()+interval '2 days',$4::uuid)
		RETURNING id::text`, assigneeUser, f.orgA, systemID, f.orgAdminA.UserID).Scan(&grantID); err != nil {
		t.Fatalf("insert vendor grant: %v", err)
	}
	suffix := time.Now().Format("150405")
	var workOrderID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO security_work_orders (work_order_no,title,assignee_organization_id,severity,
		                                   description,remediation_requirement,status,execution_mode,
		                                   created_by,updated_by,issued_at,due_at)
		VALUES ($1,'授权到期通知验证工单',$2::uuid,'high','用于验证授权到期收权','完成整改','issued','individual',
		        $3::uuid,$3::uuid,now(),now()+interval '2 days')
		RETURNING id::text`, "GRANT_WO_"+suffix, f.orgA, f.orgAdminA.UserID).Scan(&workOrderID); err != nil {
		t.Fatalf("insert vendor work order: %v", err)
	}
	var findingID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO security_findings (finding_code,title,severity,status,assignee_organization_id,
		                               business_system_id,description,source,created_by,updated_by)
		VALUES ($1,'授权到期通知验证发现','high','confirmed',$2::uuid,$3::uuid,'用于验证授权到期收权','manual',$4::uuid,$4::uuid)
		RETURNING id::text`, "GRANT_F_"+suffix, f.orgA, systemID, f.orgAdminA.UserID).Scan(&findingID); err != nil {
		t.Fatalf("insert vendor finding: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO security_work_order_findings (work_order_id,finding_id) VALUES ($1::uuid,$2::uuid)`,
		workOrderID, findingID); err != nil {
		t.Fatalf("link vendor finding: %v", err)
	}
	var taskID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO remediation_tasks (task_no,work_order_id,finding_id,organization_id,business_system_id,
		                               assignee_user_id,grant_id,status,due_at,original_due_at,
		                               target_environment,created_by)
		VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,'in_progress',
		        now()+interval '2 days',now()+interval '2 days','production',$8::uuid)
		RETURNING id::text`, "GRANT_T_"+suffix, workOrderID, findingID, f.orgA, systemID,
		assigneeUser, grantID, f.orgAdminA.UserID).Scan(&taskID); err != nil {
		t.Fatalf("insert vendor task: %v", err)
	}

	// While the grant is inside its window the assignee can read the task.
	var canRead bool
	if err := f.pool.QueryRow(ctx, `SELECT remediation_can_read($1::uuid,$2::uuid)`, assigneeUser, taskID).Scan(&canRead); err != nil {
		t.Fatalf("check remediation_can_read before expiry: %v", err)
	}
	if !canRead {
		t.Fatal("an assignee inside a valid grant window cannot read its own task")
	}

	// Expire the grant in place. No status change and no cache invalidation: the
	// permission functions are evaluated against the row every time, which is the
	// behaviour that makes an expiry take effect immediately.
	if _, err := f.pool.Exec(ctx, `UPDATE remediation_grants SET valid_until = now() - interval '1 minute' WHERE id = $1::uuid`, grantID); err != nil {
		t.Fatalf("expire grant: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT remediation_can_read($1::uuid,$2::uuid)`, assigneeUser, taskID).Scan(&canRead); err != nil {
		t.Fatalf("check remediation_can_read after expiry: %v", err)
	}
	if canRead {
		t.Fatal("an expired grant still grants task access; access must stop at the deadline")
	}
	var grantValid bool
	if err := f.pool.QueryRow(ctx, `SELECT remediation_grant_valid($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, grantID, assigneeUser, f.orgA, systemID).Scan(&grantValid); err != nil {
		t.Fatalf("check remediation_grant_valid: %v", err)
	}
	if grantValid {
		t.Fatal("remediation_grant_valid still reports an expired grant as valid")
	}

	// The assignee must also stop receiving task notifications at the same moment.
	result, _, err := f.notes.Search(ctx, f.vendorA, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("vendor Search error = %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_task" && item.ResourceID == taskID {
			t.Fatalf("expired grant still produced a task notification: %+v", item)
		}
	}

	// Re-granting access requires an explicit action; it must not revive itself.
	if _, err := f.pool.Exec(ctx, `
		UPDATE remediation_grants SET status='revoked' WHERE id=$1::uuid`, grantID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT remediation_grant_valid($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, grantID, assigneeUser, f.orgA, systemID).Scan(&grantValid); err != nil {
		t.Fatalf("recheck remediation_grant_valid: %v", err)
	}
	if grantValid {
		t.Fatal("a revoked grant reports itself as valid")
	}
	// Extending only the deadline is not enough: a revoked grant stays revoked
	// until somebody re-grants it, so access never revives on its own.
	if _, err := f.pool.Exec(ctx, `UPDATE remediation_grants SET valid_until = now() + interval '30 days' WHERE id=$1::uuid`, grantID); err != nil {
		t.Fatalf("extend grant deadline: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `SELECT remediation_grant_valid($1::uuid,$2::uuid,$3::uuid,$4::uuid)`, grantID, assigneeUser, f.orgA, systemID).Scan(&grantValid); err != nil {
		t.Fatalf("recheck remediation_grant_valid after extension: %v", err)
	}
	if grantValid {
		t.Fatal("a revoked grant revived itself once its deadline moved; restoration must be explicit")
	}
}

func systemOfTask(t *testing.T, f *notificationScopeFixture) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(f.ctx, `SELECT business_system_id::text FROM remediation_tasks WHERE id=$1::uuid`, f.taskA).Scan(&id); err != nil {
		t.Fatalf("load system of task %s: %v", f.taskA, err)
	}
	return id
}
