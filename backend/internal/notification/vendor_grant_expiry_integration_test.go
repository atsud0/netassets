package notification_test

import (
	"testing"
	"time"

	"netassets/internal/auth"
	"netassets/internal/notification"
)

// An external vendor grant is a three-link chain: vendors -> vendor_catalog,
// vendor_memberships and remediation_grants. Every link can break on its own, and
// each break must take effect at the same instant, in all three places that decide
// access: remediation_grant_valid (reads and notifications), and
// remediation_task_execution_state (the execute button guard). These tests drive the
// real chain instead of the internal-participant shortcut, because the internal
// path has no vendor table to read and would pass even if those links were ignored.
type vendorGrantFixture struct {
	f          *notificationScopeFixture
	vendorID   string // vendors.id, organization scoped record
	catalogID  string // vendor_catalog.id, the cross-organization vendor identity
	worker     auth.Principal
	grantID    string
	taskID     string
	systemID   string
	memberFrom time.Time
	memberTo   time.Time
}

func setupVendorGrant(t *testing.T, grantFrom, grantUntil, memberFrom, memberUntil time.Time) *vendorGrantFixture {
	t.Helper()
	f := setupNotificationScope(t)
	ctx := f.ctx
	suffix := f.suffix + time.Now().Format("150405")

	v := &vendorGrantFixture{f: f, memberFrom: memberFrom, memberTo: memberUntil}
	v.worker = auth.Principal{UserID: createNotificationUser(t, f.pool, "vg_worker_"+suffix)}

	if err := f.pool.QueryRow(ctx, `
		INSERT INTO vendor_catalog (name,created_by) VALUES ($1,$2::uuid) RETURNING id::text`,
		"外协厂商_"+suffix, f.orgAdminA.UserID).Scan(&v.catalogID); err != nil {
		t.Fatalf("insert vendor catalog: %v", err)
	}
	// The vendor record must belong to the same organization as the grant. A
	// cross-organization vendor is exactly one of the ways access must be denied,
	// so the fixture pins the legitimate case here.
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO vendors (name,organization_id,contact,status,created_by,catalog_id)
		VALUES ($1,$2::uuid,'厂商联系人','enabled',$3::uuid,$4::uuid) RETURNING id::text`,
		"外协厂商_"+suffix, f.orgA, f.orgAdminA.UserID, v.catalogID).Scan(&v.vendorID); err != nil {
		t.Fatalf("insert vendor: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO vendor_memberships (vendor_id,user_id,status,valid_from,valid_until)
		VALUES ($1::uuid,$2::uuid,'enabled',$3,$4)`, v.catalogID, v.worker.UserID, memberFrom, memberUntil); err != nil {
		t.Fatalf("insert vendor membership: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO business_systems (system_code,system_name,owner_organization_id,system_status)
		VALUES ($1,'外协授权系统',$2::uuid,'running') RETURNING id::text`,
		"VG_SYS_"+suffix, f.orgA).Scan(&v.systemID); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	// participant_type='external' together with vendor_id is enforced by a CHECK, so
	// this row is the shape the product actually stores for a vendor assignee.
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO remediation_grants (user_id,organization_id,business_system_id,vendor_id,participant_type,
		                             status,valid_from,valid_until,sponsor_user_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'external','enabled',$5,$6,$7::uuid)
		RETURNING id::text`, v.worker.UserID, f.orgA, v.systemID, v.vendorID,
		grantFrom, grantUntil, f.orgAdminA.UserID).Scan(&v.grantID); err != nil {
		t.Fatalf("insert external vendor grant: %v", err)
	}

	var workOrderID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO security_work_orders (work_order_no,title,assignee_organization_id,severity,
		                                   description,remediation_requirement,status,execution_mode,
		                                   created_by,updated_by,issued_at,due_at)
		VALUES ($1,'外协授权到期工单',$2::uuid,'high','用于验证外协授权收权','完成整改',
		        'issued','individual',$3::uuid,$3::uuid,now(),now()+interval '2 days')
		RETURNING id::text`, "VG_WO_"+suffix, f.orgA, f.orgAdminA.UserID).Scan(&workOrderID); err != nil {
		t.Fatalf("insert work order: %v", err)
	}
	var findingID string
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO security_findings (finding_code,title,severity,status,assignee_organization_id,
		                               business_system_id,description,source,created_by,updated_by)
		VALUES ($1,'外协授权到期发现','high','confirmed',$2::uuid,$3::uuid,'用于验证外协授权收权','manual',$4::uuid,$4::uuid)
		RETURNING id::text`, "VG_F_"+suffix, f.orgA, v.systemID, f.orgAdminA.UserID).Scan(&findingID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO security_work_order_findings (work_order_id,finding_id) VALUES ($1::uuid,$2::uuid)`,
		workOrderID, findingID); err != nil {
		t.Fatalf("link finding: %v", err)
	}
	if err := f.pool.QueryRow(ctx, `
		INSERT INTO remediation_tasks (task_no,work_order_id,finding_id,organization_id,business_system_id,
		                               assignee_user_id,grant_id,status,due_at,original_due_at,
		                               target_environment,created_by)
		VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,'in_progress',
		        now()+interval '2 days',now()+interval '2 days','production',$8::uuid)
		RETURNING id::text`, "VG_T_"+suffix, workOrderID, findingID, f.orgA, v.systemID,
		v.worker.UserID, v.grantID, f.orgAdminA.UserID).Scan(&v.taskID); err != nil {
		t.Fatalf("insert vendor task: %v", err)
	}
	// The notification query reads the title through the task's current material
	// version, so an authorized vendor would be invisible without one. Assigning
	// also writes the participants-visible event that carries the assignment notice.
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO remediation_material_versions (task_id,revision,title,severity,description,requirement,approved_by)
		VALUES ($1::uuid,1,'外协授权到期材料','high','用于验证外协授权收权','完成整改',$2::uuid)`,
		v.taskID, f.orgAdminA.UserID); err != nil {
		t.Fatalf("insert material version: %v", err)
	}
	if _, err := f.pool.Exec(ctx, `
		INSERT INTO remediation_task_events (task_id,actor_id,action,visibility,details)
		VALUES ($1::uuid,$2::uuid,'assign','participants','{}'::jsonb)`, v.taskID, f.orgAdminA.UserID); err != nil {
		t.Fatalf("insert assign event: %v", err)
	}
	return v
}

func (v *vendorGrantFixture) grantValid(t *testing.T) bool {
	t.Helper()
	var valid bool
	if err := v.f.pool.QueryRow(v.f.ctx, `
		SELECT remediation_grant_valid($1::uuid,$2::uuid,$3::uuid,$4::uuid)`,
		v.grantID, v.worker.UserID, v.f.orgA, v.systemID).Scan(&valid); err != nil {
		t.Fatalf("remediation_grant_valid: %v", err)
	}
	return valid
}

func (v *vendorGrantFixture) canRead(t *testing.T) bool {
	t.Helper()
	var ok bool
	if err := v.f.pool.QueryRow(v.f.ctx, `SELECT remediation_can_read($1::uuid,$2::uuid)`,
		v.worker.UserID, v.taskID).Scan(&ok); err != nil {
		t.Fatalf("remediation_can_read: %v", err)
	}
	return ok
}

func (v *vendorGrantFixture) canExecute(t *testing.T) (bool, string) {
	t.Helper()
	var ok bool
	var reason *string
	if err := v.f.pool.QueryRow(v.f.ctx, `SELECT can_execute,reason FROM remediation_task_execution_state($1::uuid)`,
		v.taskID).Scan(&ok, &reason); err != nil {
		t.Fatalf("remediation_task_execution_state: %v", err)
	}
	if reason == nil {
		return ok, ""
	}
	return ok, *reason
}

func (v *vendorGrantFixture) seesTaskNotification(t *testing.T) bool {
	t.Helper()
	result, _, err := v.f.notes.Search(v.f.ctx, v.worker, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("worker Search: %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_task" && item.ResourceID == v.taskID {
			return true
		}
	}
	return false
}

// assertFullyRevoked is the invariant every break in the chain must reach: reads,
// execution and notifications all lose access at the same moment, and they agree on
// the same reason. A partial revocation is a real defect, so all three are checked.
func (v *vendorGrantFixture) assertFullyRevoked(t *testing.T, wantReason string) {
	t.Helper()
	if v.grantValid(t) {
		t.Fatalf("remediation_grant_valid still true; the vendor chain is still treated as authorized (reason %q)", wantReason)
	}
	if v.canRead(t) {
		t.Fatal("remediation_can_read still true; the assignee keeps reading a task it is no longer authorized for")
	}
	canExecute, reason := v.canExecute(t)
	if canExecute {
		t.Fatalf("remediation_task_execution_state still reports can_execute=true with reason %q; the execute guard disagrees with the read guard", reason)
	}
	if wantReason != "" && reason != wantReason {
		t.Fatalf("execution state reason = %q, want %q; the displayed reason must match the real cause", reason, wantReason)
	}
	if v.seesTaskNotification(t) {
		t.Fatal("the vendor still receives task notifications after losing authorization")
	}
}

func TestExternalVendorGrantGrantsAccessThroughTheWholeChain(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(48*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))

	if !v.grantValid(t) {
		t.Fatal("a fully linked external vendor grant reports itself invalid; the chain is broken when it should be open")
	}
	if !v.canRead(t) {
		t.Fatal("an external vendor assignee cannot read its own task while grant, vendor and membership are all valid")
	}
	canExecute, reason := v.canExecute(t)
	if !canExecute {
		t.Fatalf("an external vendor assignee cannot execute its own task: can_execute=false reason=%q", reason)
	}
	if !v.seesTaskNotification(t) {
		t.Fatal("an authorized external vendor receives no task notification; the notification scope is narrower than the access rule")
	}
}

// Each case breaks exactly one link of the chain, so a fix that hard-codes one
// predicate (or that only checks vendors.status) cannot pass all of them.
func TestExternalVendorGrantLosesAccessWhenEachLinkExpires(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name       string
		grantFrom  time.Time
		grantUntil time.Time
		memberFrom time.Time
		memberTill time.Time
		breakLink  func(t *testing.T, v *vendorGrantFixture)
		wantReason string
	}{
		{
			name: "grant deadline passed", grantFrom: now.Add(-72 * time.Hour), grantUntil: now.Add(-time.Hour),
			memberFrom: now.Add(-72 * time.Hour), memberTill: now.Add(72 * time.Hour),
			breakLink:  func(t *testing.T, v *vendorGrantFixture) {},
			wantReason: "authorization_expired",
		},
		{
			name: "vendor membership expired", grantFrom: now.Add(-72 * time.Hour), grantUntil: now.Add(72 * time.Hour),
			memberFrom: now.Add(-72 * time.Hour), memberTill: now.Add(-time.Hour),
			breakLink:  func(t *testing.T, v *vendorGrantFixture) {},
			wantReason: "authorization_expired",
		},
		{
			name: "vendor membership revoked", grantFrom: now.Add(-72 * time.Hour), grantUntil: now.Add(72 * time.Hour),
			memberFrom: now.Add(-72 * time.Hour), memberTill: now.Add(72 * time.Hour),
			breakLink: func(t *testing.T, v *vendorGrantFixture) {
				execVendorGrant(t, v, `UPDATE vendor_memberships SET status='disabled' WHERE vendor_id=$1::uuid AND user_id=$2::uuid`, v.catalogID, v.worker.UserID)
			},
			wantReason: "authorization_expired",
		},
		{
			name: "vendor record disabled", grantFrom: now.Add(-72 * time.Hour), grantUntil: now.Add(72 * time.Hour),
			memberFrom: now.Add(-72 * time.Hour), memberTill: now.Add(72 * time.Hour),
			breakLink: func(t *testing.T, v *vendorGrantFixture) {
				execVendorGrant(t, v, `UPDATE vendors SET status='disabled' WHERE id=$1::uuid`, v.vendorID)
			},
			wantReason: "authorization_expired",
		},
		{
			name: "vendor catalog entry disabled", grantFrom: now.Add(-72 * time.Hour), grantUntil: now.Add(72 * time.Hour),
			memberFrom: now.Add(-72 * time.Hour), memberTill: now.Add(72 * time.Hour),
			breakLink: func(t *testing.T, v *vendorGrantFixture) {
				execVendorGrant(t, v, `UPDATE vendor_catalog SET status='disabled' WHERE id=$1::uuid`, v.catalogID)
			},
			wantReason: "authorization_expired",
		},
		{
			name: "vendor record moved to another organization", grantFrom: now.Add(-72 * time.Hour), grantUntil: now.Add(72 * time.Hour),
			memberFrom: now.Add(-72 * time.Hour), memberTill: now.Add(72 * time.Hour),
			breakLink: func(t *testing.T, v *vendorGrantFixture) {
				execVendorGrant(t, v, `UPDATE vendors SET organization_id=$1::uuid WHERE id=$2::uuid`, v.f.orgB, v.vendorID)
			},
			wantReason: "authorization_expired",
		},
		{
			name: "assignee account disabled", grantFrom: now.Add(-48 * time.Hour), grantUntil: now.Add(48 * time.Hour),
			memberFrom: now.Add(-48 * time.Hour), memberTill: now.Add(48 * time.Hour),
			breakLink: func(t *testing.T, v *vendorGrantFixture) {
				execVendorGrant(t, v, `UPDATE users SET status='disabled' WHERE id=$1::uuid`, v.worker.UserID)
			},
			wantReason: "authorization_expired",
		},
		{
			name: "grant revoked", grantFrom: now.Add(-48 * time.Hour), grantUntil: now.Add(48 * time.Hour),
			memberFrom: now.Add(-48 * time.Hour), memberTill: now.Add(48 * time.Hour),
			breakLink: func(t *testing.T, v *vendorGrantFixture) {
				execVendorGrant(t, v, `UPDATE remediation_grants SET status='revoked' WHERE id=$1::uuid`, v.grantID)
			},
			wantReason: "authorization_expired",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := setupVendorGrant(t, tc.grantFrom, tc.grantUntil, tc.memberFrom, tc.memberTill)
			// Precondition: the chain starts open, so a later denial is caused by the
			// break and not by a fixture that was never authorized.
			if !v.grantValid(t) || !v.canRead(t) {
				t.Skip("fixture did not start authorized; the vendor chain shape changed and this case needs an update")
			}
			tc.breakLink(t, v)
			v.assertFullyRevoked(t, tc.wantReason)
		})
	}
}

func TestExternalVendorGrantDoesNotReviveOnItsOwn(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-72*time.Hour), now.Add(-time.Hour), now.Add(-72*time.Hour), now.Add(72*time.Hour))
	if v.grantValid(t) {
		t.Fatal("an already expired vendor grant reports itself valid")
	}

	// Extending the grant deadline must not help while the membership is the broken
	// link, and a revoked grant must stay revoked: restoration is an explicit action.
	if _, err := v.f.pool.Exec(v.f.ctx, `UPDATE remediation_grants SET status='revoked' WHERE id=$1::uuid`, v.grantID); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
	if _, err := v.f.pool.Exec(v.f.ctx, `UPDATE remediation_grants SET valid_until=now()+interval '30 days' WHERE id=$1::uuid`, v.grantID); err != nil {
		t.Fatalf("extend grant: %v", err)
	}
	if _, err := v.f.pool.Exec(v.f.ctx, `UPDATE vendor_memberships SET valid_until=now()+interval '30 days' WHERE vendor_id=$1::uuid AND user_id=$2::uuid`, v.catalogID, v.worker.UserID); err != nil {
		t.Fatalf("extend membership: %v", err)
	}
	v.assertFullyRevoked(t, "authorization_expired")
}

func execVendorGrant(t *testing.T, v *vendorGrantFixture, sql string, args ...any) {
	t.Helper()
	if _, err := v.f.pool.Exec(v.f.ctx, sql, args...); err != nil {
		t.Fatalf("break vendor link (%s): %v", sql, err)
	}
}
