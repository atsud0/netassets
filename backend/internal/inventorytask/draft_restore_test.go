package inventorytask

import (
	"errors"
	"testing"

	"netassets/internal/auth"
)

func TestValidateDraftRestoreRequiresOrganizationAdminAndPermissions(t *testing.T) {
	base := auth.Principal{
		Roles:       []string{"organization_admin"},
		Permissions: []string{"inventory.manage", "asset.manage"},
	}
	if err := validateDraftRestore(base, "asset.manage", 3); err != nil {
		t.Fatalf("valid restore principal rejected: %v", err)
	}

	cases := []struct {
		name       string
		principal  auth.Principal
		permission string
		version    int
		want       error
	}{
		{
			name: "contributor cannot restore",
			principal: auth.Principal{
				Roles:       []string{"organization_user"},
				Permissions: []string{"inventory.manage", "asset.manage"},
			},
			permission: "asset.manage", version: 3, want: ErrForbidden,
		},
		{
			name: "missing resource permission",
			principal: auth.Principal{
				Roles:       []string{"organization_admin"},
				Permissions: []string{"inventory.manage"},
			},
			permission: "asset.manage", version: 3, want: ErrForbidden,
		},
		{
			name: "missing inventory permission",
			principal: auth.Principal{
				Roles:       []string{"organization_admin"},
				Permissions: []string{"asset.manage"},
			},
			permission: "asset.manage", version: 3, want: ErrForbidden,
		},
		{
			name:      "invalid version",
			principal: base, permission: "asset.manage", version: 0, want: ErrInvalid,
		},
		{
			name:      "wrong resource permission",
			principal: base, permission: "system.manage", version: 3, want: ErrForbidden,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateDraftRestore(tt.principal, tt.permission, tt.version); !errors.Is(err, tt.want) {
				t.Fatalf("validateDraftRestore() error = %v, want %v", err, tt.want)
			}
		})
	}
}
