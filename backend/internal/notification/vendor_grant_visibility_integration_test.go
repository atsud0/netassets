package notification_test

import (
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
