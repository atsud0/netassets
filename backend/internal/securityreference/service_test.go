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

func TestNormalizeAndValidateAssetSelection(t *testing.T) {
	input := AssetSelectionInput{
		Dimension: " organization ",
		IDs: []string{
			"11111111-1111-1111-1111-111111111111",
			"11111111-1111-1111-1111-111111111111",
			" ",
		},
	}
	normalizeAssetSelection(&input)
	if input.Dimension != "organization" || len(input.IDs) != 1 {
		t.Fatalf("selection was not normalized: %+v", input)
	}
	if !validAssetSelection(input) {
		t.Fatal("normalized organization selection should be valid")
	}
	input.Dimension = "asset"
	if validAssetSelection(input) {
		t.Fatal("asset is not a supported bulk selection dimension")
	}
}

func TestRejectsInvalidAssetSelectionIDs(t *testing.T) {
	for _, input := range []AssetSelectionInput{
		{Dimension: "organization"},
		{Dimension: "business_system", IDs: []string{"not-a-uuid"}},
		{Dimension: "network_area", IDs: make([]string, maxSelectionIDs+1)},
	} {
		if validAssetSelection(input) {
			t.Fatalf("selection should be invalid: %+v", input)
		}
	}
}
