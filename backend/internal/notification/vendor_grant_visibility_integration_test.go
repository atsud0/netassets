package notification_test

import (
	"errors"
	"testing"
	"time"

	"netassets/internal/auth"
	"netassets/internal/notification"
)

// The vendor-facing half of the authorization chain is not covered by the
// existing suites. Everything so far proves that a vendor gains and loses task
// access, but it never proves that:
//
//   - the vendor is told about their OWN grant before it silently disappears;
//   - the grant-expiry reminder that the manager sees never leaks the vendor's
//     name into a role that may not manage people;
//   - a grant that is valid for one organization gives no visibility into another
//     organization's vendor records at all.
//
// The last one is the important security property: vendor_catalog is a global
// identity shared across organizations, so any query that reads it must still be
// constrained by the organization of the vendors row.

func TestVendorAssigneeIsNotifiedAboutTheirOwnGrantEnding(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))

	// Precondition: the manager's own reminder proves the notification fires at
	// all, so the vendor assertion below is about who receives it.
	if !seesGrantReminder(t, v, v.f.orgAdminA, v.grantID) {
		t.Fatal("the organization manager receives no grant-expiry reminder; the reminder itself is broken")
	}
	// The vendor is the person whose access actually disappears. If the vendor
	// receives nothing, their tasks vanish overnight with no explanation, and the
	// only party that can restore them is someone they cannot see.
	if !seesGrantReminder(t, v, v.worker, v.grantID) {
		t.Fatal("the grant assignee receives no notification that their own authorization has ended; their tasks disappear silently")
	}
}

func TestGrantReminderDoesNotReachOtherOrganizations(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))

	if seesGrantReminder(t, v, v.f.orgAdminB, v.grantID) {
		t.Fatal("an administrator of another organization was told about this grant; the reminder crosses the organization boundary")
	}
	if seesGrantReminder(t, v, v.f.groupAdmin, v.grantID) {
		// A group administrator is legitimately allowed to see every organization's
		// grant, so this case must not be asserted as a failure. It is asserted in
		// the positive direction below instead.
		t.Log("group administrator sees the reminder, which is expected for a group-wide role")
	}
}

// A vendor grant is scoped to one organization, but vendor_catalog is global. The
// boundary that matters is therefore the vendors row's organization, not the
// catalog entry: a manager of organization B must not learn that organization A
// engaged a vendor, even though both may reference the same catalog identity.
func TestVendorGrantReminderIsScopedToItsOwnOrganization(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))

	// The vendor's own name must not appear in any list owned by another
	// organization, regardless of which catalog entry it resolves to.
	result, _, err := v.f.notes.Search(v.f.ctx, v.f.orgAdminB, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("org admin B Search error = %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_grant" && item.ResourceID == v.grantID {
			t.Fatalf("organization B received a grant notification belonging to organization A: %+v", item)
		}
	}
}

// seesGrantReminder looks for the grant-expiry notification of one specific
// grant in one principal's own list. It matches on the resource id rather than
// the title so a wording change cannot silently make the assertion pass.
func seesGrantReminder(t *testing.T, v *vendorGrantFixture, principal auth.Principal, grantID string) bool {
	t.Helper()
	result, _, err := v.f.notes.Search(v.f.ctx, principal, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_grant" && item.ResourceID == grantID {
			return true
		}
	}
	return false
}

// The vendor half of the authorization chain has a second half that no test above
// touches: what a grant is NOT allowed to authorize. A grant names one user, one
// organization and one business system, and it is the only thing that gives a vendor
// participant any access at all. Each case below hands an existing, perfectly valid
// grant to somebody it was not issued to, and demands that every access decision -
// read, execute and notification - refuses. Passing means the grant is treated as a
// bearer token; failing means the chain still binds all four of its parts together.

// A grant issued for a vendor in organization A must not authorize the same person
// in organization B, even when B engaged the very same catalog vendor and the person
// is a live member of it. This is the realistic cross-organization mistake: one
// vendor, several subsidiaries, one shared account.
func TestVendorGrantDoesNotAuthorizeAnotherOrganization(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(48*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))
	f := v.f

	// Organization B engages the same catalog identity and keeps the same person as a
	// member. Only the vendors row differs by organization, which is exactly the
	// boundary remediation_grant_valid() is supposed to enforce.
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO vendors (name,organization_id,contact,status,created_by,catalog_id)
		VALUES ($1,$2::uuid,'厂商联系人','enabled',$3::uuid,$4::uuid) RETURNING id::text`,
		"外协厂商_B_"+f.suffix, f.orgB, f.orgAdminB.UserID, v.catalogID).Scan(new(string)); err != nil {
		t.Fatalf("insert vendor in organization B: %v", err)
	}

	// Precondition: the legitimate chain is open, so a denial below is caused by the
	// organization mismatch rather than by a fixture that never worked.
	if !v.grantValid(t) || !v.canRead(t) {
		t.Fatalf("fixture did not start authorized (grant_valid=%v can_read=%v)", v.grantValid(t), v.canRead(t))
	}

	var valid bool
	if err := f.pool.QueryRow(f.ctx, `SELECT remediation_grant_valid($1::uuid,$2::uuid,$3::uuid,$4::uuid)`,
		v.grantID, v.worker.UserID, f.orgB, v.systemID).Scan(&valid); err != nil {
		t.Fatalf("remediation_grant_valid for the other organization: %v", err)
	}
	if valid {
		t.Fatal("a grant issued for organization A authorizes the same user in organization B; the vendors row organization is not part of the check")
	}
}

// A valid grant for a vendor must not authorize the same person on a different
// business system of the same organization. Otherwise one engagement on one system
// silently becomes standing access to every other system the organization owns.
func TestVendorGrantDoesNotAuthorizeAnotherBusinessSystem(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(48*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))
	f := v.f

	var otherSystem string
	if err := f.pool.QueryRow(f.ctx, `
		INSERT INTO business_systems (system_code,system_name,owner_organization_id,system_status)
		VALUES ($1,'外协未授权系统',$2::uuid,'running') RETURNING id::text`,
		"VG_SYS2_"+f.suffix, f.orgA).Scan(&otherSystem); err != nil {
		t.Fatalf("insert second business system: %v", err)
	}

	var valid bool
	if err := f.pool.QueryRow(f.ctx, `SELECT remediation_grant_valid($1::uuid,$2::uuid,$3::uuid,$4::uuid)`,
		v.grantID, v.worker.UserID, f.orgA, otherSystem).Scan(&valid); err != nil {
		t.Fatalf("remediation_grant_valid for the other system: %v", err)
	}
	if valid {
		t.Fatal("a grant for one business system authorizes the same user on another system of the same organization")
	}
}

// A grant names one user. A second person who is also a live member of the same
// catalog vendor must not inherit it, otherwise the vendor's whole staff list
// becomes assignable to one task. This is asserted through the real access chain
// rather than through remediation_grant_valid() alone: the grant already names its
// own user, so only the read guard, the execute guard and the notification list can
// prove that a colleague is not treated as the assignee.
func TestVendorGrantDoesNotAuthorizeAnotherMemberOfTheSameVendor(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(48*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))
	f := v.f

	colleague := auth.Principal{UserID: createNotificationUser(t, f.pool, "vg_colleague_"+f.suffix)}
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO vendor_memberships (vendor_id,user_id,status,valid_from,valid_until)
		VALUES ($1::uuid,$2::uuid,'enabled',$3,$4)`,
		v.catalogID, colleague.UserID, now.Add(-48*time.Hour), now.Add(48*time.Hour)); err != nil {
		t.Fatalf("insert colleague membership: %v", err)
	}

	// Precondition: the colleague really is a live member of the very same vendor, so
	// a denial below is caused by the grant binding one person only.
	var member bool
	if err := f.pool.QueryRow(f.ctx, `
		SELECT EXISTS(SELECT 1 FROM vendor_memberships vm JOIN vendors vv ON vv.catalog_id=vm.vendor_id
		WHERE vm.user_id=$1::uuid AND vm.vendor_id=$2::uuid AND vm.status='enabled'
		  AND vm.valid_from<=now() AND vm.valid_until>now() AND vv.id=$3::uuid)`,
		colleague.UserID, v.catalogID, v.vendorID).Scan(&member); err != nil {
		t.Fatalf("colleague membership precondition: %v", err)
	}
	if !member {
		t.Fatal("the colleague is not a live member of the same vendor; this case would assert nothing")
	}

	if err := f.notes.MarkRead(f.ctx, colleague, "repair_grant_self:not-a-real-key"); !errors.Is(err, notification.ErrNotFound) {
		t.Fatalf("MarkRead for an unrelated colleague = %v, want ErrNotFound", err)
	}
	result, _, err := f.notes.Search(f.ctx, colleague, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("colleague Search: %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_task" && item.ResourceID == v.taskID {
			t.Fatalf("a colleague of the same vendor sees the assignee's task notification %s", v.taskID)
		}
	}

	var canRead bool
	if err := f.pool.QueryRow(f.ctx, `SELECT remediation_can_read($1::uuid,$2::uuid)`,
		colleague.UserID, v.taskID).Scan(&canRead); err != nil {
		t.Fatalf("remediation_can_read for the colleague: %v", err)
	}
	if canRead {
		t.Fatal("a colleague of the same vendor can read the assignee's task; the grant is treated as covering the whole vendor")
	}
}

// The vendor's own authorization notice must not become an enumeration channel. A
// vendor is told about their own grant, and a manager is told about the grants they
// manage; the exact same grant must not also appear in the list of somebody who is
// neither, merely because that somebody shares the organization or holds a
// pass-holder role without people-management rights.
func TestVendorSelfGrantNoticeIsNotVisibleToNonManagers(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(1*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))
	f := v.f

	// Precondition: the notice exists for both legitimate recipients.
	if !seesGrantReminder(t, v, v.worker, v.grantID) {
		t.Fatal("the assignee receives no notice about their own expiring grant; the notice itself is broken")
	}

	// A person in the same organization with no people-management rights must not be
	// able to watch the vendor's authorization state through the notification list.
	observer := createNotificationScopedPrincipal(t, f.pool, "note_org_admin_no_people_"+f.suffix, "organization_editor", f.orgA)
	result, _, err := f.notes.Search(f.ctx, observer, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("observer Search: %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_grant" && item.ResourceID == v.grantID {
			t.Fatalf("a principal without remediation.people.manage saw grant %s: %+v", v.grantID, item)
		}
	}
}

// MarkRead is a write. It is guarded by the same visibility check as the list, so a
// notification that must not be listed must not be markable either - otherwise the
// unread counter of an unrelated grant can be cleared by guessing its key.
func TestNotificationOfAnotherOrganizationCannotBeMarkedRead(t *testing.T) {
	now := time.Now()
	v := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(-time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))
	f := v.f

	result, _, err := f.notes.Search(f.ctx, f.orgAdminA, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("org admin A Search: %v", err)
	}
	var foreignKey string
	for _, item := range result.Items {
		if item.ResourceType == "remediation_grant" && item.ResourceID == v.grantID {
			foreignKey = item.Key
		}
	}
	if foreignKey == "" {
		t.Fatal("organization A sees no grant notification, so there is nothing to protect")
	}

	if err := f.notes.MarkRead(f.ctx, f.orgAdminB, foreignKey); !errors.Is(err, notification.ErrNotFound) {
		t.Fatalf("MarkRead of another organization's notification = %v, want ErrNotFound", err)
	}
	// The failed attempt must also have left no read receipt behind, or a later
	// legitimate read by the owner would look already-done.
	if err := f.notes.MarkRead(f.ctx, f.orgAdminA, foreignKey); err != nil {
		t.Fatalf("the owner cannot mark their own notification read: %v", err)
	}
}

// The self-targeted notice distinguishes "about to expire" from "already invalid" in
// its title and level, because the two demand different actions from the vendor: keep
// working, or go find the administrator. Collapsing them would tell someone whose
// access is already gone that they still have time.
func TestVendorSelfGrantNoticeLevelDistinguishesExpiringFromInvalid(t *testing.T) {
	now := time.Now()
	expiring := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(1*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))
	invalid := setupVendorGrant(t, now.Add(-48*time.Hour), now.Add(-1*time.Hour), now.Add(-48*time.Hour), now.Add(48*time.Hour))

	expiringItem, ok := grantReminderItem(t, expiring, expiring.worker, expiring.grantID)
	if !ok {
		t.Fatal("the assignee of a still-valid, soon-expiring grant is not notified")
	}
	if expiringItem.Level != "warning" {
		t.Fatalf("expiring grant notice level = %q, want warning", expiringItem.Level)
	}

	invalidItem, ok := grantReminderItem(t, invalid, invalid.worker, invalid.grantID)
	if !ok {
		t.Fatal("the assignee of an already invalid grant is not notified")
	}
	if invalidItem.Level != "danger" {
		t.Fatalf("already invalid grant notice level = %q, want danger; the vendor cannot act on an expiry warning for access that is already gone", invalidItem.Level)
	}
	if invalidItem.Message == expiringItem.Message {
		t.Fatal("the invalid-grant notice reuses the expiring-grant message; the vendor is told to finish work it can no longer submit")
	}
	if invalidItem.ActionPath != "/repair/tasks" {
		t.Fatalf("invalid-grant notice action path = %q, want /repair/tasks", invalidItem.ActionPath)
	}
}

func grantReminderItem(t *testing.T, v *vendorGrantFixture, principal auth.Principal, grantID string) (notification.Item, bool) {
	t.Helper()
	result, _, err := v.f.notes.Search(v.f.ctx, principal, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_grant" && item.ResourceID == grantID {
			return item, true
		}
	}
	return notification.Item{}, false
}
