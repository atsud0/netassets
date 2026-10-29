package securityworkorder

import (
	"context"
	"errors"
	"testing"

	"netassets/internal/auth"
)

func TestFindingMutationsAuthorizeBeforePersonalTaskLookup(t *testing.T) {
	// A nil pool deliberately makes any pre-authorization database access fail.
	// A forbidden caller must not learn whether an ID has a personal task.
	service := NewService(nil)
	ctx := context.Background()
	mutations := map[string]func(auth.Principal, string) error{
		"update": func(p auth.Principal, id string) error {
			_, err := service.UpdateFinding(ctx, p, id, FindingInput{}, "", "")
			return err
		},
		"accept-risk": func(p auth.Principal, id string) error {
			_, err := service.AcceptRisk(ctx, p, id, RiskAcceptanceInput{}, "", "")
			return err
		},
		"revoke-risk": func(p auth.Principal, id string) error {
			_, err := service.RevokeRisk(ctx, p, id, RiskRevokeInput{}, "", "")
			return err
		},
		"review-risk": func(p auth.Principal, id string) error {
			_, err := service.ReviewRisk(ctx, p, id, RiskReviewInput{}, "", "")
			return err
		},
	}
	principals := map[string]auth.Principal{
		"no-role":                            {},
		"organization-admin":                 {Roles: []string{"organization_admin"}},
		"organization-admin-with-permission": {Roles: []string{"organization_admin"}, Permissions: []string{auth.PermissionSecurityFinding}},
		"operator-without-permission":        {Roles: []string{auth.RoleSecurityOperator}},
		"group-admin-without-permission":     {Roles: []string{auth.RoleGroupAdmin}},
	}
	for name, mutate := range mutations {
		for role, principal := range principals {
			for _, id := range []string{"", "not-a-uuid", "11111111-1111-4111-8111-111111111111"} {
				t.Run(name+"/"+role+"/"+id, func(t *testing.T) {
					if err := mutate(principal, id); !errors.Is(err, ErrForbidden) {
						t.Fatalf("got %v, want ErrForbidden before validation or database lookup", err)
					}
				})
			}
		}
		for _, role := range []string{auth.RoleGroupAdmin, auth.RoleSecurityOperator} {
			t.Run(name+"/authorized-invalid-id/"+role, func(t *testing.T) {
				p := auth.Principal{Roles: []string{role}, Permissions: []string{auth.PermissionSecurityFinding}}
				if err := mutate(p, "not-a-uuid"); !errors.Is(err, ErrInvalid) {
					t.Fatalf("got %v, want ErrInvalid for authorized caller", err)
				}
			})
		}
	}
}
