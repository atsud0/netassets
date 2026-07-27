package role

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInUse     = errors.New("in use")
	ErrInvalid   = errors.New("invalid input")
	roleKeyRule  = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
)

type Permission struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type Role struct {
	ID          string       `json:"id"`
	Key         string       `json:"key"`
	Name        string       `json:"name"`
	Scope       string       `json:"scope"`
	Description string       `json:"description,omitempty"`
	IsSystem    bool         `json:"is_system"`
	Status      string       `json:"status"`
	Permissions []Permission `json:"permissions"`
	UserCount   int          `json:"user_count"`
	CreatedAt   time.Time    `json:"created_at"`
	UpdatedAt   time.Time    `json:"updated_at"`
}

type Input struct {
	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	PermissionKeys []string `json:"permission_keys"`
	Status         string   `json:"status"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func canManage(principal auth.Principal) bool { return principal.HasRole("group_admin") }

func (s *Service) ListPermissions(ctx context.Context, principal auth.Principal) ([]Permission, error) {
	result, err := s.SearchPermissions(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}
func (s *Service) SearchPermissions(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[Permission], error) {
	if !canManage(principal) {
		return pagination.Result[Permission]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT permission_key,permission_name,COALESCE(description,''),count(*) OVER () FROM permissions ORDER BY permission_key LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Permission]{}, fmt.Errorf("search permissions: %w", err)
	}
	defer rows.Close()
	items := make([]Permission, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Permission
		if err := rows.Scan(&item.Key, &item.Name, &item.Description, &total); err != nil {
			return pagination.Result[Permission]{}, fmt.Errorf("scan permission: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Permission]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM permissions`).Scan(&total); err != nil {
			return pagination.Result[Permission]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Role, error) {
	result, err := s.Search(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}
func (s *Service) Search(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[Role], error) {
	if !canManage(principal) {
		return pagination.Result[Role]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT r.id::text,r.role_key,r.role_name,r.role_scope,COALESCE(r.description,''),r.is_system,r.status,count(DISTINCT ur.id),r.created_at,r.updated_at,count(*) OVER () FROM roles r LEFT JOIN user_roles ur ON ur.role_id=r.id GROUP BY r.id ORDER BY r.is_system DESC,r.role_scope,r.role_name LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Role]{}, fmt.Errorf("search roles: %w", err)
	}
	defer rows.Close()
	items := make([]Role, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Role
		if err := rows.Scan(&item.ID, &item.Key, &item.Name, &item.Scope, &item.Description, &item.IsSystem, &item.Status, &item.UserCount, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Role]{}, fmt.Errorf("scan role: %w", err)
		}
		permissions, err := s.permissionsForRole(ctx, item.ID)
		if err != nil {
			return pagination.Result[Role]{}, err
		}
		item.Permissions = permissions
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Role]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM roles`).Scan(&total); err != nil {
			return pagination.Result[Role]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

// ListAssignable exposes only enabled organization roles for user appointment
// assignment. Unlike role configuration, it is safe for scoped user managers.
func (s *Service) ListAssignable(ctx context.Context, principal auth.Principal) ([]Role, error) {
	result, err := s.SearchAssignable(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}
func (s *Service) SearchAssignable(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[Role], error) {
	if !principal.HasPermission("user.manage") {
		return pagination.Result[Role]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,role_key,role_name,role_scope,COALESCE(description,''),is_system,status,created_at,updated_at,count(*) OVER () FROM roles WHERE role_scope='organization' AND status='enabled' ORDER BY is_system DESC,role_name LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Role]{}, fmt.Errorf("search assignable roles: %w", err)
	}
	defer rows.Close()
	items := make([]Role, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Role
		if err := rows.Scan(&item.ID, &item.Key, &item.Name, &item.Scope, &item.Description, &item.IsSystem, &item.Status, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Role]{}, fmt.Errorf("scan assignable role: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Role]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM roles WHERE role_scope='organization' AND status='enabled'`).Scan(&total); err != nil {
			return pagination.Result[Role]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input Input, requestID, ipAddress string) (Role, error) {
	if !canManage(principal) {
		return Role{}, ErrForbidden
	}
	if err := normalizeAndValidate(&input, true); err != nil {
		return Role{}, err
	}
	if err := s.verifyPermissions(ctx, input.PermissionKeys); err != nil {
		return Role{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, fmt.Errorf("begin create role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item Role
	err = tx.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
		VALUES ($1, $2, 'organization', NULLIF($3, ''), false, $4)
		RETURNING id::text, role_key, role_name, role_scope, COALESCE(description, ''), is_system, status, created_at, updated_at
	`, input.Key, input.Name, input.Description, input.Status).Scan(&item.ID, &item.Key, &item.Name, &item.Scope, &item.Description, &item.IsSystem, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Role{}, ErrConflict
		}
		return Role{}, fmt.Errorf("create role: %w", err)
	}
	if err := assignPermissions(ctx, tx, item.ID, input.PermissionKeys); err != nil {
		return Role{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, 'role.create', 'role', $2, '新增自定义角色', jsonb_build_object('key', $3::text, 'permissions', $4::varchar[]), NULLIF($5, '')::inet, NULLIF($6, ''))
	`, principal.UserID, item.ID, item.Key, input.PermissionKeys, ipAddress, requestID); err != nil {
		return Role{}, fmt.Errorf("audit create role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Role{}, fmt.Errorf("commit create role: %w", err)
	}
	item.Permissions, err = s.permissionsForRole(ctx, item.ID)
	return item, err
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, roleID string, input Input, requestID, ipAddress string) (Role, error) {
	if !canManage(principal) {
		return Role{}, ErrForbidden
	}
	if err := normalizeAndValidate(&input, false); err != nil {
		return Role{}, err
	}
	if err := s.verifyPermissions(ctx, input.PermissionKeys); err != nil {
		return Role{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Role{}, fmt.Errorf("begin update role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var system bool
	if err := tx.QueryRow(ctx, `SELECT is_system FROM roles WHERE id = $1::uuid FOR UPDATE`, strings.TrimSpace(roleID)).Scan(&system); errors.Is(err, pgx.ErrNoRows) {
		return Role{}, ErrNotFound
	} else if err != nil {
		return Role{}, fmt.Errorf("load role: %w", err)
	}
	if system {
		return Role{}, ErrForbidden
	}
	var item Role
	err = tx.QueryRow(ctx, `
		UPDATE roles SET role_name=$2, description=NULLIF($3, ''), status=$4, updated_at=now()
		WHERE id=$1::uuid
		RETURNING id::text, role_key, role_name, role_scope, COALESCE(description, ''), is_system, status, created_at, updated_at
	`, roleID, input.Name, input.Description, input.Status).Scan(&item.ID, &item.Key, &item.Name, &item.Scope, &item.Description, &item.IsSystem, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return Role{}, fmt.Errorf("update role: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1::uuid`, roleID); err != nil {
		return Role{}, fmt.Errorf("clear role permissions: %w", err)
	}
	if err := assignPermissions(ctx, tx, roleID, input.PermissionKeys); err != nil {
		return Role{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET revoked_at=now() WHERE user_id IN (SELECT user_id FROM user_roles WHERE role_id=$1::uuid) AND revoked_at IS NULL`, roleID); err != nil {
		return Role{}, fmt.Errorf("revoke affected sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, 'role.update', 'role', $2, '编辑自定义角色', jsonb_build_object('permissions', $3::varchar[], 'status', $4::text), NULLIF($5, '')::inet, NULLIF($6, ''))
	`, principal.UserID, roleID, input.PermissionKeys, input.Status, ipAddress, requestID); err != nil {
		return Role{}, fmt.Errorf("audit update role: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Role{}, fmt.Errorf("commit update role: %w", err)
	}
	item.Permissions, err = s.permissionsForRole(ctx, item.ID)
	return item, err
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, roleID, requestID, ipAddress string) error {
	if !canManage(principal) {
		return ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete role: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var system bool
	var count int
	err = tx.QueryRow(ctx, `SELECT r.is_system, count(ur.id) FROM roles r LEFT JOIN user_roles ur ON ur.role_id=r.id WHERE r.id=$1::uuid GROUP BY r.id`, strings.TrimSpace(roleID)).Scan(&system, &count)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load role references: %w", err)
	}
	if system {
		return ErrForbidden
	}
	if count > 0 {
		return ErrInUse
	}
	if _, err := tx.Exec(ctx, `DELETE FROM roles WHERE id=$1::uuid`, roleID); err != nil {
		return fmt.Errorf("delete role: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs (user_id, action, resource_type, resource_id, summary, request_ip, request_id) VALUES ($1::uuid, 'role.delete', 'role', $2, '删除自定义角色', NULLIF($3, '')::inet, NULLIF($4, ''))`, principal.UserID, roleID, ipAddress, requestID); err != nil {
		return fmt.Errorf("audit delete role: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) permissionsForRole(ctx context.Context, roleID string) ([]Permission, error) {
	rows, err := s.pool.Query(ctx, `SELECT p.permission_key, p.permission_name, COALESCE(p.description,'') FROM role_permissions rp JOIN permissions p ON p.permission_key=rp.permission_key WHERE rp.role_id=$1::uuid ORDER BY p.permission_key`, roleID)
	if err != nil {
		return nil, fmt.Errorf("role permissions: %w", err)
	}
	defer rows.Close()
	items := make([]Permission, 0)
	for rows.Next() {
		var p Permission
		if err := rows.Scan(&p.Key, &p.Name, &p.Description); err != nil {
			return nil, fmt.Errorf("scan role permission: %w", err)
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role permissions: %w", err)
	}
	return items, nil
}

func (s *Service) verifyPermissions(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return ErrInvalid
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM permissions WHERE permission_key = ANY($1::varchar[])`, keys).Scan(&count); err != nil {
		return fmt.Errorf("verify permissions: %w", err)
	}
	if count != len(keys) {
		return ErrInvalid
	}
	return nil
}

func normalizeAndValidate(input *Input, creating bool) error {
	input.Key = strings.ToLower(strings.TrimSpace(input.Key))
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Status == "" {
		input.Status = "enabled"
	}
	seen := map[string]bool{}
	result := make([]string, 0, len(input.PermissionKeys))
	for _, key := range input.PermissionKeys {
		key = strings.TrimSpace(key)
		if key != "" && !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	sort.Strings(result)
	input.PermissionKeys = result
	if (creating && !roleKeyRule.MatchString(input.Key)) || input.Name == "" || len(input.Name) > 128 || len(input.Description) > 5000 || (input.Status != "enabled" && input.Status != "disabled") || len(input.PermissionKeys) == 0 {
		return ErrInvalid
	}
	return nil
}

func assignPermissions(ctx context.Context, tx pgx.Tx, roleID string, keys []string) error {
	for _, key := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_key) VALUES ($1::uuid,$2)`, roleID, key); err != nil {
			return fmt.Errorf("assign permission: %w", err)
		}
	}
	return nil
}
