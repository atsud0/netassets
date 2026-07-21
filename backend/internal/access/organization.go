package access

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// CanAccessOrganization checks role assignments and their configured data scope.
func CanAccessOrganization(ctx context.Context, pool *pgxpool.Pool, principal auth.Principal, organizationID string) (bool, error) {
	if principal.HasRole("group_admin") {
		return true, nil
	}
	var allowed bool
	err := pool.QueryRow(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            WHERE ur.user_id = $1::uuid
              AND ur.organization_id IS NOT NULL
              AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION
            SELECT child.id
            FROM organizations child
            JOIN descendants parent ON child.parent_id = parent.id
        ), accessible(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            WHERE ur.user_id = $1::uuid
              AND ur.organization_id IS NOT NULL
              AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION
            SELECT id FROM descendants
        )
        SELECT EXISTS (SELECT 1 FROM accessible WHERE id = $2::uuid)
    `, principal.UserID, organizationID).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check organization access: %w", err)
	}
	return allowed, nil
}
