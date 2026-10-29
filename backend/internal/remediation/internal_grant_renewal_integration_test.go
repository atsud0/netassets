package remediation_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"netassets/internal/auth"
	"netassets/internal/remediation"
)

func TestInternalGrantRenewalDoesNotRequireVendorMembership(t *testing.T) {
	f := setup(t)
	for index, letter := range []string{"因", "R"} {
		t.Run(fmt.Sprintf("language_%d", index), func(t *testing.T) {
			until := time.Now().Add(24 * time.Hour)
			person, err := f.s.CreatePerson(f.ctx, f.manager, remediation.PersonInput{
				OrganizationID: f.org, SystemID: f.system, ParticipantType: "internal",
				Username: fmt.Sprintf("internal_%s_%d", suffixOf(t), index), DisplayName: "内部续期人员",
				Password: "Internal-Renewal-123!", ValidUntil: until,
			})
			must(t, err)
			id, userID := person["id"].(string), person["user_id"].(string)
			future := until.Add(30 * 24 * time.Hour)
			snapshot := func() string {
				t.Helper()
				var value string
				must(t, f.pool.QueryRow(f.ctx, `SELECT row_to_json(g)::text FROM remediation_grants g WHERE id=$1::uuid`, id).Scan(&value))
				return value
			}
			before := snapshot()
			for _, reason := range []string{"", strings.Repeat(letter, 2001)} {
				if err := f.s.UpdateGrant(f.ctx, f.manager, id, "enabled", future, reason); !errors.Is(err, remediation.ErrInvalid) {
					t.Fatalf("invalid reason: %v", err)
				}
				if snapshot() != before {
					t.Fatal("rejected reason changed the grant")
				}
			}
			if err := f.s.UpdateGrant(f.ctx, auth.Principal{UserID: f.other.UserID}, id, "enabled", future, "无管理权限"); !errors.Is(err, remediation.ErrForbidden) {
				t.Fatalf("unauthorized renewal: %v", err)
			}
			if snapshot() != before {
				t.Fatal("unauthorized renewal changed the grant")
			}
			// Internal accounts have no vendor, catalog or membership; renewal must still
			// restore an expired grant without weakening the common account/system gates.
			_, err = f.pool.Exec(f.ctx, `UPDATE remediation_grants SET valid_from=now()-interval '2 days',valid_until=now()-interval '1 day' WHERE id=$1::uuid`, id)
			must(t, err)
			reason := strings.Repeat(letter, 2000)
			must(t, f.s.UpdateGrant(f.ctx, f.manager, id, "enabled", future, reason))
			var valid bool
			var savedUntil time.Time
			var savedReason string
			var events, memberships int
			must(t, f.pool.QueryRow(f.ctx, `SELECT remediation_grant_valid(id,user_id,organization_id,business_system_id),valid_until FROM remediation_grants WHERE id=$1::uuid`, id).Scan(&valid, &savedUntil))
			if !valid || savedUntil.Sub(future).Abs() > time.Millisecond {
				t.Fatalf("renewal not effective: valid=%v deadline=%s", valid, savedUntil)
			}
			must(t, f.pool.QueryRow(f.ctx, `SELECT count(*),max(details->>'reason') FROM audit_logs WHERE resource_id=$1 AND action='remediation.grant.update'`, id).Scan(&events, &savedReason))
			if events != 1 || savedReason != reason {
				t.Fatalf("audit mismatch: events=%d reason length=%d", events, len([]rune(savedReason)))
			}
			must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM vendor_memberships WHERE user_id=$1::uuid`, userID).Scan(&memberships))
			if memberships != 0 {
				t.Fatal("internal renewal created a vendor membership")
			}
			for _, mutation := range []struct{ disable, restore string }{
				{`UPDATE users SET status='disabled' WHERE id=$1::uuid`, `UPDATE users SET status='enabled' WHERE id=$1::uuid`},
				{`UPDATE business_systems SET system_status='archived' WHERE id=$1::uuid`, `UPDATE business_systems SET system_status='running' WHERE id=$1::uuid`},
				{`UPDATE organizations SET status='disabled' WHERE id=$1::uuid`, `UPDATE organizations SET status='enabled' WHERE id=$1::uuid`},
			} {
				target := userID
				if strings.Contains(mutation.disable, "business_systems") {
					target = f.system
				}
				if strings.Contains(mutation.disable, "organizations") {
					target = f.org
				}
				_, err = f.pool.Exec(f.ctx, mutation.disable, target)
				must(t, err)
				before := snapshot()
				renewalErr := f.s.UpdateGrant(f.ctx, f.manager, id, "enabled", future.Add(time.Hour), "不可用依赖不得续期")
				_, restoreErr := f.pool.Exec(f.ctx, mutation.restore, target)
				must(t, restoreErr)
				if !errors.Is(renewalErr, remediation.ErrConflict) && !errors.Is(renewalErr, remediation.ErrForbidden) {
					t.Fatalf("unavailable dependency renewal: %v", renewalErr)
				}
				if snapshot() != before {
					t.Fatal("blocked renewal changed the grant")
				}
			}
		})
	}
}
