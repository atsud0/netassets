package securityreference

import (
	"testing"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestSecurityReferenceReadBoundary(t *testing.T) {
	operator := auth.Principal{Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding}}
	if !canRead(operator) {
		t.Fatal("security operator with a security permission should read security references")
	}
	organizationRole := auth.Principal{Roles: []string{"organization_security"}, Permissions: []string{auth.PermissionSecurityFinding}}
	if canRead(organizationRole) {
		t.Fatal("organization custom role must not read group-wide security references")
	}
}

func TestValidateFilter(t *testing.T) {
	if err := validateFilter(Filter{Page: pagination.Query{Page: 1, PageSize: 20}, OrganizationID: "not-a-uuid"}); err == nil {
		t.Fatal("invalid organization id should be rejected")
	}
	if err := validateFilter(Filter{Page: pagination.Query{Page: 1, PageSize: 20}, Keyword: string(make([]byte, 201))}); err == nil {
		t.Fatal("oversized keyword should be rejected")
	}
}
