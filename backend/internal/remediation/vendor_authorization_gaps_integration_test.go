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

// Creating an external person is the only supported way to create a vendor grant
// from the UI. Keep the association invariant explicit here: the grant points to the
// organization-scoped vendors row, while the roster points to that row's global
// vendor_catalog identity. A test that only checks Detail can miss either side of
// this relationship if a query happens to fall back to a broad user lookup.
// Invitation acceptance is a separate production path from CreatePerson. It must
// materialize the same vendor association rather than creating a grant that looks
// external but has no catalog membership behind it. This test intentionally drives
// Invite and AcceptInvitation, then asks the notification service about the newly
// created principal: a database assertion alone would not prove that the identity
// can actually use the resulting authorization chain.
func TestAcceptVendorInvitationPersistsAssociationAndSelfNotification(t *testing.T) {
	f := setup(t)
	validUntil := time.Now().Add(48 * time.Hour)
	username := "invited_vendor_" + suffixOf(t)

	invitation, err := f.s.Invite(f.ctx, f.manager, remediation.PersonInput{
		OrganizationID:  f.org,
		SystemID:        f.system,
		VendorID:        vendorIDForGrant(t, f),
		ParticipantType: "external",
		Username:        username,
		DisplayName:     "受邀外部执行人员",
		ValidUntil:      validUntil,
	})
	must(t, err)
	token, ok := invitation["token"].(string)
	if !ok || token == "" {
		t.Fatalf("vendor invitation did not return an acceptance token: %v", invitation["token"])
	}
	invitationID, ok := invitation["id"].(string)
	if !ok || invitationID == "" {
		t.Fatalf("vendor invitation did not return an id: %v", invitation["id"])
	}
	wantedVendor := vendorIDForGrant(t, f)
	var invitationVendor, invitationParticipant string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT COALESCE(vendor_id::text,''), participant_type
		FROM remediation_invitations WHERE id=$1::uuid`, invitationID).
		Scan(&invitationVendor, &invitationParticipant); err != nil {
		t.Fatalf("load vendor invitation association: %v", err)
	}
	if invitationVendor != wantedVendor || invitationParticipant != "external" {
		t.Fatalf("invitation vendor association=(%q,%q), want (%q,external)", invitationVendor, invitationParticipant, wantedVendor)
	}

	accepted, err := f.s.AcceptInvitation(f.ctx, nil, remediation.AcceptInvitationInput{
		Token:    token,
		Password: "Invited-Vendor-Password-123",
	})
	must(t, err)
	userID, ok := accepted["user_id"].(string)
	if !ok || userID == "" {
		t.Fatalf("accepted vendor invitation returned user_id=%v", accepted["user_id"])
	}
	grantID, ok := accepted["id"].(string)
	if !ok || grantID == "" {
		t.Fatalf("accepted vendor invitation returned grant id=%v", accepted["id"])
	}

	var grantVendor, catalogID, membershipVendor, participantType string
	var membershipCount int
	if err := f.pool.QueryRow(f.ctx, `
		SELECT g.vendor_id::text, v.catalog_id::text, g.participant_type,
		       (SELECT count(*)::int FROM vendor_memberships vm
		          WHERE vm.vendor_id=v.catalog_id AND vm.user_id=g.user_id)
		FROM remediation_grants g
		JOIN vendors v ON v.id=g.vendor_id
		WHERE g.id=$1::uuid AND g.user_id=$2::uuid`, grantID, userID).
		Scan(&grantVendor, &catalogID, &participantType, &membershipCount); err != nil {
		t.Fatalf("load accepted vendor grant association: %v", err)
	}
	if grantVendor == "" || catalogID == "" {
		t.Fatal("accepted invitation created no organization vendor/catalog association")
	}
	if grantVendor != wantedVendor {
		t.Fatalf("accepted grant vendor=%q, want invited vendor=%q", grantVendor, wantedVendor)
	}
	if participantType != "external" {
		t.Fatalf("accepted grant participant_type=%q, want external", participantType)
	}
	if membershipCount != 1 {
		t.Fatalf("accepted grant has %d matching vendor membership rows, want exactly one", membershipCount)
	}
	if err := f.pool.QueryRow(f.ctx, `
		SELECT vendor_id::text FROM vendor_memberships
		WHERE vendor_id=$1::uuid AND user_id=$2::uuid`, catalogID, userID).Scan(&membershipVendor); err != nil {
		t.Fatalf("load accepted vendor membership: %v", err)
	}
	if membershipVendor != catalogID {
		t.Fatalf("accepted membership vendor=%q, want vendor_catalog.id=%q", membershipVendor, catalogID)
	}

	var valid bool
	if err := f.pool.QueryRow(f.ctx, `
		SELECT remediation_grant_valid(id,user_id,organization_id,business_system_id)
		FROM remediation_grants WHERE id=$1::uuid`, grantID).Scan(&valid); err != nil {
		t.Fatalf("check accepted vendor grant validity: %v", err)
	}
	if !valid {
		t.Fatal("accepted vendor invitation is not authorized while all association links are live")
	}

	vendorPrincipal := auth.Principal{UserID: userID}
	items, _, err := notification.NewService(f.pool).Search(f.ctx, vendorPrincipal, notification.SearchInput{Page: pageOf(100)})
	must(t, err)
	for _, item := range items.Items {
		if item.ResourceType == "remediation_grant" && item.ResourceID == grantID {
			return
		}
	}
	t.Fatalf("accepted vendor received no self authorization notification for grant %s", grantID)
}

func vendorIDForGrant(t *testing.T, f *fixture) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(f.ctx, `SELECT vendor_id::text FROM remediation_grants WHERE id=$1::uuid`, f.grant).Scan(&id); err != nil {
		t.Fatalf("load fixture vendor id: %v", err)
	}
	return id
}

func TestCreateExternalPersonPersistsTheCompleteVendorAssociation(t *testing.T) {
	f := setup(t)

	var grantVendor, vendorCatalog, vendorOrganization, memberVendor string
	var participantType string
	var memberCount int
	if err := f.pool.QueryRow(f.ctx, `
		SELECT g.vendor_id::text, v.catalog_id::text, v.organization_id::text,
		       g.participant_type, COUNT(vm.user_id)::int
		FROM remediation_grants g
		JOIN vendors v ON v.id=g.vendor_id
		LEFT JOIN vendor_memberships vm
		  ON vm.vendor_id=v.catalog_id AND vm.user_id=g.user_id
		WHERE g.id=$1::uuid
		GROUP BY g.vendor_id, v.catalog_id, v.organization_id, g.participant_type`, f.grant).
		Scan(&grantVendor, &vendorCatalog, &vendorOrganization, &participantType, &memberCount); err != nil {
		t.Fatalf("load external grant association: %v", err)
	}
	if grantVendor == "" || vendorCatalog == "" {
		t.Fatal("external grant has no vendor record or vendor catalog association")
	}
	if vendorOrganization != f.org {
		t.Fatalf("vendor organization = %q, want grant organization %q", vendorOrganization, f.org)
	}
	if participantType != "external" {
		t.Fatalf("grant participant_type = %q, want external", participantType)
	}
	if memberCount != 1 {
		t.Fatalf("external grant has %d matching vendor membership rows, want exactly one", memberCount)
	}
	if err := f.pool.QueryRow(f.ctx, `
		SELECT vm.vendor_id::text FROM vendor_memberships vm
		WHERE vm.user_id=(SELECT user_id FROM remediation_grants WHERE id=$1::uuid)
		  AND vm.vendor_id=$2::uuid`, f.grant, vendorCatalog).Scan(&memberVendor); err != nil {
		t.Fatalf("load vendor membership catalog link: %v", err)
	}
	if memberVendor != vendorCatalog {
		t.Fatalf("membership vendor = %q, want catalog %q (membership must use vendor_catalog.id)", memberVendor, vendorCatalog)
	}

	var valid bool
	if err := f.pool.QueryRow(f.ctx, `
		SELECT remediation_grant_valid(g.id,g.user_id,g.organization_id,g.business_system_id)
		FROM remediation_grants g WHERE g.id=$1::uuid`, f.grant).Scan(&valid); err != nil {
		t.Fatalf("check service-created vendor grant validity: %v", err)
	}
	if !valid {
		t.Fatal("service-created external vendor grant is not valid while all association links are live")
	}
}

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
		WHERE g.id=$1::uuid AND m.user_id=$2::uuid`, f.grant, f.worker.UserID).Scan(&membershipUntil); err != nil {
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
	foreign, err := f.s.SaveVendor(f.ctx, f.group, "", otherOrg, "外部厂商_"+suffixOf(t), "13800000018", "enabled")
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

// A renewal that cannot restore the vendor's access must be refused outright. The
// 修复人员 page offers 续期 next to 结束合作 and reports the result verbatim, so a
// renewal that succeeds while the vendor stays locked out is the worst outcome: the
// administrator believes the collaboration is running again and only the vendor -
// the one party that cannot restore access itself - finds out that it is not.
func TestGrantRenewalIsRefusedWhenItCannotRestoreTheVendor(t *testing.T) {
	cases := []struct {
		name   string
		broken func(t *testing.T, f *fixture)
	}{
		{
			name: "vendor membership disabled",
			broken: func(t *testing.T, f *fixture) {
				editFixture(t, f, `UPDATE vendor_memberships SET status='disabled' WHERE user_id=$1::uuid`, f.worker.UserID)
			},
		},
		{
			name: "vendor record disabled",
			broken: func(t *testing.T, f *fixture) {
				editFixture(t, f, `UPDATE vendors SET status='disabled' WHERE id=(SELECT vendor_id FROM remediation_grants WHERE id=$1::uuid)`, f.grant)
			},
		},
		{
			name: "vendor record moved to another organization",
			broken: func(t *testing.T, f *fixture) {
				var otherOrg string
				must(t, f.pool.QueryRow(f.ctx, `
					INSERT INTO organizations(organization_code,organization_name,organization_type)
					VALUES($1,'其他子公司','subsidiary') RETURNING id::text`, "VOTHER_"+suffixOf(t)).Scan(&otherOrg))
				editFixture(t, f, `UPDATE vendors SET organization_id=$1::uuid WHERE id=(SELECT vendor_id FROM remediation_grants WHERE id=$2::uuid)`, otherOrg, f.grant)
			},
		},
		{
			name: "vendor catalog entry disabled",
			broken: func(t *testing.T, f *fixture) {
				editFixture(t, f, `
					UPDATE vendor_catalog SET status='disabled' WHERE id=(
						SELECT v.catalog_id FROM vendors v JOIN remediation_grants g ON g.vendor_id=v.id WHERE g.id=$1::uuid)`, f.grant)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t)
			id := f.assign(t, 0, "vg-norestore-"+suffixOf(t), f.grant)
			tc.broken(t, f)

			must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "结束外协合作"))
			err := f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", time.Now().Add(30*24*time.Hour), "续期一个月")
			if !errors.Is(err, remediation.ErrConflict) {
				t.Fatalf("renewal that cannot restore access error = %v, want ErrConflict", err)
			}

			// A refusal must leave nothing half-applied: the grant is still revoked and
			// the vendor still has no access, so a retry after the chain is repaired
			// behaves the same as the first attempt.
			var status string
			if err := f.pool.QueryRow(f.ctx, `SELECT status FROM remediation_grants WHERE id=$1::uuid`, f.grant).Scan(&status); err != nil {
				t.Fatalf("load grant status: %v", err)
			}
			if status != "revoked" {
				t.Fatalf("grant status = %q after a refused renewal, want revoked", status)
			}
			if _, err := f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
				t.Fatalf("the vendor can open the task after a refused renewal: %v", err)
			}
		})
	}
}

// The refusal above must not turn a recoverable situation into a permanently
// unusable grant. An expired membership is the recoverable case and it is the whole
// point of a renewal: the same button that the previous test refuses must, once the
// unrepairable links are intact, both reopen the vendor's window and hand the task
// back - without the administrator having to know which table to edit.
func TestGrantRenewalReopensAnExpiredVendorMembership(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "vg-repair-"+suffixOf(t), f.grant)

	// The vendor's own roster window has not opened yet - a re-onboarding roster
	// entered with the wrong start date. Extending only the end would leave the
	// window shut for another ten days, so the renewal has to pull the start back
	// as well or the vendor is told they are working again while the catalog still
	// says they are not on the roster yet.
	editFixture(t, f, `
		UPDATE vendor_memberships SET valid_from=now()+interval '10 days', valid_until=now()+interval '20 days'
		WHERE user_id=$1::uuid`, f.worker.UserID)
	if _, err := f.s.Detail(f.ctx, f.worker, id, true); err == nil {
		t.Fatal("the vendor can open the task while their roster window has not opened yet; the precondition would assert nothing")
	}
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "结束外协合作"))

	future := time.Now().Add(30 * 24 * time.Hour)
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", future, "续期一个月"))

	if _, err := f.s.Detail(f.ctx, f.worker, id, true); err != nil {
		t.Fatalf("the vendor still cannot open the task after the renewal: %v", err)
	}

	// The membership window is what the vendor catalog shows, so it must be reopened
	// and must not stop short of the renewed grant deadline.
	var membershipFrom, membershipUntil time.Time
	if err := f.pool.QueryRow(f.ctx, `
		SELECT m.valid_from, m.valid_until FROM vendor_memberships m
		JOIN vendors v ON v.catalog_id=m.vendor_id
		JOIN remediation_grants g ON g.vendor_id=v.id
		WHERE g.id=$1::uuid AND m.user_id=g.user_id AND m.user_id=$2::uuid`, f.grant, f.worker.UserID).Scan(&membershipFrom, &membershipUntil); err != nil {
		t.Fatalf("load membership window: %v", err)
	}
	// The renewed window must have already opened, not merely been moved.
	if membershipFrom.After(time.Now().Add(time.Minute)) {
		t.Fatalf("membership starts at %s, still in the future; the renewal reopened nothing", membershipFrom)
	}
	if !membershipUntil.After(future.Add(-time.Minute)) {
		t.Fatalf("membership ends at %s, which is before the renewed grant deadline %s", membershipUntil, future)
	}
}

// The refusal is also reachable after the membership was disabled, so repairing the
// roster the way the 厂商 page does must make the very same renewal succeed. This
// guards against a fix that simply refuses every renewal.
func TestGrantRenewalSucceedsOnceTheRosterIsRepaired(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "vg-repair2-"+suffixOf(t), f.grant)

	editFixture(t, f, `UPDATE vendor_memberships SET status='disabled' WHERE user_id=$1::uuid`, f.worker.UserID)
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "结束外协合作"))
	if err := f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", time.Now().Add(30*24*time.Hour), "续期一个月"); !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("renewal error = %v, want ErrConflict while the person is off the vendor roster", err)
	}

	editFixture(t, f, `
		UPDATE vendor_memberships SET status='enabled', valid_from=now()-interval '1 day', valid_until=now()+interval '365 days'
		WHERE user_id=$1::uuid`, f.worker.UserID)
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "enabled", time.Now().Add(30*24*time.Hour), "修复厂商名册后续期"))

	if _, err := f.s.Detail(f.ctx, f.worker, id, true); err != nil {
		t.Fatalf("the repaired vendor still cannot open the task: %v", err)
	}
}

// editFixture applies one fixture edit. It exists because a pgx Exec returns a
// command tag alongside its error, which cannot be forwarded into a single-error
// helper; wrapping it here keeps each case of a table readable as one statement.
func editFixture(t *testing.T, f *fixture, sql string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, sql, args...); err != nil {
		t.Fatalf("fixture edit %q: %v", sql, err)
	}
}
