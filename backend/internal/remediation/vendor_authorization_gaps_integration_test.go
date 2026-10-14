package remediation_test

import (
	"errors"
	"testing"
	"time"

	"netassets/internal/auth"
	"netassets/internal/notification"
	"netassets/internal/pagination"
	"netassets/internal/remediation"
)

// The vendor grant suite in the notification package proves the database chain -
// vendors, vendor_catalog, vendor_memberships and remediation_grants - breaks
// correctly when a row is edited underneath it. It never drives the two paths a
// customer actually uses: an administrator pressing "结束合作" / "续期" on the
// 修复人员 page, and an administrator opening the 厂商 list.
//
// Both are service-level code with their own permission checks, their own audit
// writes and their own side effect on vendor_memberships. Editing a row directly
// cannot reach them, so a regression there would pass every existing test.

// Grant management must be reachable through the service and must revoke access at
// the same instant. Editing the row by hand would not exercise the service's
// permission check, its audit record, or the notification the revoked person gets.
func TestUpdateGrantRevokeTakesEffectThroughTheService(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "vg-revoke-"+suffixOf(t), f.grant)

	// Precondition: the assignee can read and execute the task they were given.
	if _, err := f.s.Detail(f.ctx, f.worker, id, true); err != nil {
		t.Fatalf("assignee cannot read its own task before revocation: %v", err)
	}

	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "外协合同到期，结束合作"))

	if _, err := f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("revoked assignee can still read the task: %v", err)
	}
	list, err := f.s.List(f.ctx, f.worker, remediation.Filter{Mine: true, Page: pageOf(50)})
	must(t, err)
	for _, item := range list.Items {
		if item["id"] == id {
			t.Fatal("revoked assignee still sees the task in its own list")
		}
	}

	// The manager, who is not the assignee, keeps the task: revocation is about the
	// person who lost the authorization, not about deleting the work.
	if _, err = f.s.Detail(f.ctx, f.manager, id, false); err != nil {
		t.Fatalf("the managing administrator lost access to the task: %v", err)
	}
}

// Renewal is the other half of the same button. It must restore the whole chain,
// including the vendor membership whose deadline the first grant created, and it
// must refuse a deadline that does not actually extend the authorization - otherwise
// a renewal silently leaves the vendor unauthorized while the page shows success.
func TestUpdateGrantRenewalRestoresAccessAndRefusesShorterDeadline(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "vg-renew-"+suffixOf(t), f.grant)
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "临时结束合作"))
	if _, err := f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("revocation did not take effect: %v", err)
	}

	var currentUntil time.Time
	if err := f.pool.QueryRow(f.ctx, `SELECT valid_until FROM remediation_grants WHERE id=$1::uuid`, f.grant).Scan(&currentUntil); err != nil {
		t.Fatalf("load current deadline: %v", err)
	}

	// A renewal that would shorten the authorization must be refused outright.
	if err := f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", currentUntil.Add(-time.Hour), "试图缩短"); !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("shortening renewal error = %v, want ErrInvalid", err)
	}
	// A deadline in the past is not a renewal either.
	if err := f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", time.Now().Add(-time.Hour), "过期时间"); !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("past renewal error = %v, want ErrInvalid", err)
	}

	future := time.Now().Add(30 * 24 * time.Hour)
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", future, "续期一个月"))

	if _, err := f.s.Detail(f.ctx, f.worker, id, true); err != nil {
		t.Fatalf("the renewed assignee still cannot read the task: %v", err)
	}
	var membershipUntil time.Time
	if err := f.pool.QueryRow(f.ctx, `
		SELECT m.valid_until FROM vendor_memberships m
		JOIN remediation_grants g ON g.user_id=m.user_id
		JOIN vendors v ON v.catalog_id=m.vendor_id AND v.id=g.vendor_id
		WHERE g.id=$1::uuid`, f.grant).Scan(&membershipUntil); err != nil {
		t.Fatalf("load membership deadline: %v", err)
	}
	if !membershipUntil.After(future.Add(-time.Minute)) {
		t.Fatalf("vendor membership ends at %s, which is before the renewed grant deadline %s; the membership would expire first and silently revoke the vendor", membershipUntil, future)
	}
}

// A revoked vendor is the party that most needs to be told, because the manager who
// revoked the grant is not someone they can see. UpdateGrant is also the only path
// that writes the audit trail for the revocation.
func TestRevokedVendorIsToldAndTheAuditTrailRecordsWhy(t *testing.T) {
	f := setup(t)
	f.assign(t, 0, "vg-audit-"+suffixOf(t), f.grant)

	// Precondition: nothing is pending before the revocation, so the notification
	// found afterwards cannot be a leftover of the assignment.
	page := pageOf(100)
	before, _, err := notification.NewService(f.pool).Search(f.ctx, f.worker, notification.SearchInput{Page: page})
	must(t, err)
	for _, item := range before.Items {
		if item.Category == "remediation" && item.Title == "我的协作授权已失效" {
			t.Fatal("the worker already has an invalid-grant notice before any revocation")
		}
	}

	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "外协合同到期，结束合作"))

	after, _, err := notification.NewService(f.pool).Search(f.ctx, f.worker, notification.SearchInput{Page: page})
	must(t, err)
	found := false
	for _, item := range after.Items {
		if item.Category == "remediation" && item.ResourceType == "remediation_grant" && item.ResourceID == f.grant {
			found = true
			if item.Level != "danger" {
				t.Fatalf("revocation notice level = %q, want danger; the vendor's access is already gone", item.Level)
			}
		}
	}
	if !found {
		t.Fatal("the revoked vendor receives no notice; their task disappears with no explanation")
	}

	// The audit trail is what lets the group security manager answer "why did this
	// vendor lose access", so the reason must be stored, not just accepted.
	var reason string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT details->>'reason' FROM audit_logs
		WHERE action='remediation.grant.update' AND resource_id=$1 ORDER BY id DESC LIMIT 1`, f.grant).Scan(&reason); err != nil {
		t.Fatalf("read grant audit log: %v", err)
	}
	if reason != "外协合同到期，结束合作" {
		t.Fatalf("audit reason = %q, want the reason the administrator typed", reason)
	}
}

// vendor_catalog is a cross-organization identity, so the vendors list is the place
// where a missing organization predicate leaks one group's vendor engagement -
// including its contact name - into another group's screen.
func TestVendorListIsScopedToTheAdministratorOrganization(t *testing.T) {
	f := setup(t)
	page := pageOf(100)

	own, err := f.s.Vendors(f.ctx, f.manager, page)
	must(t, err)
	if own.Total == 0 {
		t.Fatal("the organization administrator sees no vendor at all; the list would assert nothing")
	}

	var otherOrg string
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO organizations(organization_code,organization_name,organization_type)
		VALUES($1,'其他子公司','subsidiary') RETURNING id::text`, "RORG2_"+suffixOf(t)).Scan(&otherOrg); err != nil {
		t.Fatalf("insert other organization: %v", err)
	}
	var otherAdminID string
	if err := f.pool.QueryRow(f.ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'not-used','其他组织管理员') RETURNING id::text`, "other_mgr_"+suffixOf(t)).Scan(&otherAdminID); err != nil {
		t.Fatalf("insert other administrator: %v", err)
	}
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO user_roles(user_id,role_id,organization_id,data_scope)
		SELECT $1::uuid,id,$2::uuid,'organization' FROM roles WHERE role_key='organization_admin'`, otherAdminID, otherOrg); err != nil {
		t.Fatalf("assign other administrator: %v", err)
	}
	otherAdmin := auth.Principal{UserID: otherAdminID, Permissions: []string{"remediation.people.manage", "vendor.manage"}}
	foreign, err := f.s.SaveVendor(f.ctx, f.group, "", otherOrg, "外部厂商_"+suffixOf(t), "外部联系人", "enabled")
	must(t, err)

	seen, err := f.s.Vendors(f.ctx, otherAdmin, page)
	must(t, err)
	if seen.Total != 1 {
		t.Fatalf("the other administrator sees %d vendors, want only their own", seen.Total)
	}
	for _, item := range seen.Items {
		if item["id"] == foreign["id"] {
			continue
		}
		t.Fatalf("a vendor of another organization leaked into the list: %+v", item)
	}

	// The group role is the one that is legitimately global, and it is asserted in
	// the positive direction so a future "scope everything" fix cannot pass by
	// simply breaking the group administrator too.
	group, err := f.s.Vendors(f.ctx, f.group, page)
	must(t, err)
	foundForeign := false
	for _, item := range group.Items {
		if item["id"] == foreign["id"] {
			foundForeign = true
		}
	}
	if !foundForeign {
		t.Fatal("the group administrator cannot see every organization's vendor; scoping was applied too widely")
	}
}

func pageOf(size int) pagination.Query {
	page, _ := pagination.Normalize(1, size)
	return page
}

func suffixOf(t *testing.T) string {
	t.Helper()
	return time.Now().Format("150405.000000000")
}
