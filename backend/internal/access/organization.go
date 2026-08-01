package access

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// CanAccessOrganization checks whether any enabled organization appointment grants
// access to the requested organization. It is suitable for read-only context checks
// where no specific permission is required.
func CanAccessOrganization(ctx context.Context, pool *pgxpool.Pool, principal auth.Principal, organizationID string) (bool, error) {
	if principal.HasRole("group_admin") {
		return true, nil
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return false, nil
	}
	var allowed bool
	err := pool.QueryRow(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
            JOIN organizations root ON root.id = ur.organization_id AND root.status = 'enabled' AND root.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid
              AND ur.organization_id IS NOT NULL
              AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION
            SELECT child.id
            FROM organizations child
            JOIN descendants parent ON child.parent_id = parent.id
            WHERE child.status = 'enabled' AND child.deleted_at IS NULL
        ), accessible(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
            JOIN organizations direct ON direct.id = ur.organization_id AND direct.status = 'enabled' AND direct.deleted_at IS NULL
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

// CanOperateOrganization checks both a permission and the organization scope of
// the exact role that grants that permission. Checking these independently is not
// sufficient: a user can hold different roles in different organizations.
func CanOperateOrganization(ctx context.Context, pool *pgxpool.Pool, principal auth.Principal, organizationID, permission string) (bool, error) {
	if principal.HasRole("group_admin") {
		return true, nil
	}
	organizationID = strings.TrimSpace(organizationID)
	permission = strings.TrimSpace(permission)
	if organizationID == "" || permission == "" || !principal.HasPermission(permission) {
		return false, nil
	}

	var allowed bool
	err := pool.QueryRow(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
            JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = $3
            JOIN organizations root ON root.id = ur.organization_id AND root.status = 'enabled' AND root.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid
              AND ur.organization_id IS NOT NULL
              AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION
            SELECT child.id
            FROM organizations child
            JOIN descendants parent ON child.parent_id = parent.id
            WHERE child.status = 'enabled' AND child.deleted_at IS NULL
        ), accessible(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
            JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = $3
            JOIN organizations direct ON direct.id = ur.organization_id AND direct.status = 'enabled' AND direct.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid
              AND ur.organization_id IS NOT NULL
              AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION
            SELECT id FROM descendants
        )
        SELECT EXISTS (SELECT 1 FROM accessible WHERE id = $2::uuid)
    `, principal.UserID, organizationID, permission).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check organization operation permission: %w", err)
	}
	return allowed, nil
}
