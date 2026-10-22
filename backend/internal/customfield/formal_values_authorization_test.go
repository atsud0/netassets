package customfield

import (
	"context"
	"errors"
	"testing"

	"netassets/internal/auth"
)

// No database is provided: formal writes must be rejected before querying or
// modifying any resource, even when the caller has ordinary manage permission.
func TestFormalValuesRequireGroupAdministrator(t *testing.T) {
	for _, role := range []string{"organization_admin", "organization_editor", "security_operator", "vendor"} {
		for _, resource := range []string{"asset", "business_system"} {
			t.Run(role+"/"+resource, func(t *testing.T) {
				p := auth.Principal{Roles: []string{role}, Permissions: []string{"asset.manage", "system.manage"}}
				version := 1
				err := NewService(nil).SaveValues(context.Background(), p, resource, "00000000-0000-0000-0000-000000000001", ValuesInput{
					ExpectedVersion: &version,
				}, "forbidden-formal-values", "127.0.0.1")
				if !errors.Is(err, ErrForbidden) {
					t.Fatalf("formal custom field write = %v, want ErrForbidden", err)
				}
			})
		}
	}
}
