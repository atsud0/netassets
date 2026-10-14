package networkarea

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/pagination"
	"netassets/internal/textlength"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
)

type NetworkArea struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationCode string    `json:"organization_code"`
	OrganizationName string    `json:"organization_name"`
	Name             string    `json:"name"`
	Code             string    `json:"code,omitempty"`
	Status           string    `json:"status"`
	SortOrder        int       `json:"sort_order"`
	Notes            string    `json:"notes,omitempty"`
	AssetCount       int64     `json:"asset_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type Input struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Code           string `json:"code"`
	Status         string `json:"status"`
	SortOrder      int    `json:"sort_order"`
	Notes          string `json:"notes"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal, organizationID string) ([]NetworkArea, error) {
	if !principal.HasPermission("asset.manage") {
		return nil, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID != "" {
		allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrForbidden
		}
	}
	rows, err := s.pool.Query(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
            JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
            JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
        WHERE child.status='enabled' AND child.deleted_at IS NULL
        ), accessible(id) AS (
            SELECT ur.organization_id
            FROM user_roles ur
            JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
            JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
            JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
              AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION SELECT id FROM descendants
        )
        SELECT na.id::text, o.id::text, o.organization_code, o.organization_name,
               na.area_name, COALESCE(na.area_code, ''), na.status, na.sort_order,
               COALESCE(na.notes, ''),
               (SELECT count(*) FROM assets a WHERE a.network_area_id = na.id AND a.deleted_at IS NULL),
               na.created_at, na.updated_at
        FROM asset_network_areas na
        JOIN organizations o ON o.id = na.organization_id
        WHERE ($2::text = '' OR na.organization_id::text = $2)
          AND ($3::boolean OR o.id IN (SELECT id FROM accessible))
        ORDER BY o.organization_code, na.sort_order, na.area_name
    `, principal.UserID, organizationID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list network areas: %w", err)
	}
	defer rows.Close()
	items := make([]NetworkArea, 0)
	for rows.Next() {
		var item NetworkArea
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.Name, &item.Code, &item.Status, &item.SortOrder, &item.Notes, &item.AssetCount,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan network area: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate network areas: %w", err)
	}
	return items, nil
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, organizationID string, query pagination.Query) (pagination.Result[NetworkArea], error) {
	if !principal.HasPermission("asset.manage") {
		return pagination.Result[NetworkArea]{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID != "" {
		allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
		if err != nil {
			return pagination.Result[NetworkArea]{}, err
		}
		if !allowed {
			return pagination.Result[NetworkArea]{}, ErrForbidden
		}
	}
	rows, err := s.pool.Query(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled' JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
            JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
        WHERE child.status='enabled' AND child.deleted_at IS NULL
        ), accessible(id) AS (
            SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled' JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
            JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION SELECT id FROM descendants
        )
        SELECT na.id::text, o.id::text, o.organization_code, o.organization_name,
               na.area_name, COALESCE(na.area_code, ''), na.status, na.sort_order, COALESCE(na.notes, ''),
               (SELECT count(*) FROM assets a WHERE a.network_area_id = na.id AND a.deleted_at IS NULL), na.created_at, na.updated_at,
               count(*) OVER ()
        FROM asset_network_areas na JOIN organizations o ON o.id = na.organization_id
        WHERE ($2::text = '' OR na.organization_id::text = $2) AND ($3::boolean OR o.id IN (SELECT id FROM accessible))
        ORDER BY o.organization_code, na.sort_order, na.area_name LIMIT $4 OFFSET $5
    `, principal.UserID, organizationID, principal.HasRole("group_admin"), query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[NetworkArea]{}, fmt.Errorf("search network areas: %w", err)
	}
	defer rows.Close()
	items := make([]NetworkArea, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item NetworkArea
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.Name, &item.Code, &item.Status, &item.SortOrder, &item.Notes, &item.AssetCount, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[NetworkArea]{}, fmt.Errorf("scan network area page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[NetworkArea]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
            WITH RECURSIVE descendant_roots(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled' JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
                JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
            WHERE child.status='enabled' AND child.deleted_at IS NULL
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled' JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
                JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization', 'organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*)
            FROM asset_network_areas na JOIN organizations o ON o.id = na.organization_id
            WHERE ($2::text = '' OR na.organization_id::text = $2) AND ($3::boolean OR o.id IN (SELECT id FROM accessible))
        `, principal.UserID, organizationID, principal.HasRole("group_admin")).Scan(&total); err != nil {
			return pagination.Result[NetworkArea]{}, fmt.Errorf("count network areas: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input Input, requestID, ipAddress string) (NetworkArea, error) {
	if !principal.HasPermission("asset.manage") {
		return NetworkArea{}, ErrForbidden
	}
	normalize(&input)
	if !valid(input) {
		return NetworkArea{}, ErrInvalid
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, input.OrganizationID, "asset.manage")
	if err != nil {
		return NetworkArea{}, err
	}
	if !allowed {
		return NetworkArea{}, ErrForbidden
	}
	var organizationCode, organizationName, organizationStatus string
	if err := s.pool.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id::text = $1 AND deleted_at IS NULL`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return NetworkArea{}, ErrNotFound
	} else if err != nil {
		return NetworkArea{}, fmt.Errorf("load network area organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return NetworkArea{}, ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NetworkArea{}, fmt.Errorf("begin network area create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item NetworkArea
	err = tx.QueryRow(ctx, `
        INSERT INTO asset_network_areas (organization_id, area_name, area_code, status, sort_order, notes, created_by, updated_by)
        VALUES ($1::uuid, $2, NULLIF($3, ''), $4, $5, NULLIF($6, ''), $7::uuid, $7::uuid)
        RETURNING id::text, created_at, updated_at
    `, input.OrganizationID, input.Name, input.Code, input.Status, input.SortOrder, input.Notes, principal.UserID).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return NetworkArea{}, ErrConflict
		}
		return NetworkArea{}, fmt.Errorf("create network area: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
        VALUES ($1::uuid, $2::uuid, 'network_area.create', 'network_area', $3, '新增网络区域',
                jsonb_build_object('name', $4::text, 'code', $5::text, 'status', $6::text),
                NULLIF($7, '')::inet, NULLIF($8, ''))
    `, principal.UserID, input.OrganizationID, item.ID, input.Name, input.Code, input.Status, ipAddress, requestID); err != nil {
		return NetworkArea{}, fmt.Errorf("write network area audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return NetworkArea{}, fmt.Errorf("commit network area create: %w", err)
	}
	item.OrganizationID, item.OrganizationCode, item.OrganizationName = input.OrganizationID, organizationCode, organizationName
	item.Name, item.Code, item.Status, item.SortOrder, item.Notes = input.Name, input.Code, input.Status, input.SortOrder, input.Notes
	return item, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, id string, input Input, requestID, ipAddress string) (NetworkArea, error) {
	if !principal.HasPermission("asset.manage") {
		return NetworkArea{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	normalize(&input)
	if id == "" || !valid(input) {
		return NetworkArea{}, ErrInvalid
	}
	var currentOrganizationID string
	if err := s.pool.QueryRow(ctx, `SELECT organization_id::text FROM asset_network_areas WHERE id::text = $1`, id).Scan(&currentOrganizationID); errors.Is(err, pgx.ErrNoRows) {
		return NetworkArea{}, ErrNotFound
	} else if err != nil {
		return NetworkArea{}, fmt.Errorf("load network area organization: %w", err)
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, currentOrganizationID, "asset.manage")
	if err != nil {
		return NetworkArea{}, err
	}
	if !allowed {
		return NetworkArea{}, ErrForbidden
	}
	if input.OrganizationID != "" && input.OrganizationID != currentOrganizationID {
		return NetworkArea{}, ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NetworkArea{}, fmt.Errorf("begin network area update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var item NetworkArea
	err = tx.QueryRow(ctx, `
        UPDATE asset_network_areas na SET area_name = $2, area_code = NULLIF($3, ''), status = $4,
            sort_order = $5, notes = NULLIF($6, ''), updated_by = $7::uuid, updated_at = now()
        FROM organizations o
        WHERE na.id::text = $1 AND o.id = na.organization_id
        RETURNING na.id::text, o.id::text, o.organization_code, o.organization_name,
                  na.area_name, COALESCE(na.area_code, ''), na.status, na.sort_order,
                  COALESCE(na.notes, ''), na.created_at, na.updated_at
    `, id, input.Name, input.Code, input.Status, input.SortOrder, input.Notes, principal.UserID).Scan(
		&item.ID, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
		&item.Name, &item.Code, &item.Status, &item.SortOrder, &item.Notes, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return NetworkArea{}, ErrNotFound
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return NetworkArea{}, ErrConflict
		}
		return NetworkArea{}, fmt.Errorf("update network area: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM assets WHERE network_area_id = $1::uuid AND deleted_at IS NULL`, id).Scan(&item.AssetCount); err != nil {
		return NetworkArea{}, fmt.Errorf("count network area assets: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
        VALUES ($1::uuid, $2::uuid, 'network_area.update', 'network_area', $3, '编辑网络区域',
                jsonb_build_object('name', $4::text, 'code', $5::text, 'status', $6::text),
                NULLIF($7, '')::inet, NULLIF($8, ''))
    `, principal.UserID, item.OrganizationID, item.ID, item.Name, item.Code, item.Status, ipAddress, requestID); err != nil {
		return NetworkArea{}, fmt.Errorf("write network area update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return NetworkArea{}, fmt.Errorf("commit network area update: %w", err)
	}
	return item, nil
}

func normalize(input *Input) {
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.Name = strings.TrimSpace(input.Name)
	input.Code = strings.TrimSpace(input.Code)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if input.Status == "" {
		input.Status = "enabled"
	}
	input.Notes = strings.TrimSpace(input.Notes)
}

func valid(input Input) bool {
	return input.OrganizationID != "" && input.Name != "" && textlength.Count(input.Name) <= 128 && textlength.Count(input.Code) <= 64 &&
		textlength.Count(input.Notes) <= 5000 && input.SortOrder >= 0 && input.SortOrder <= 100000 &&
		(input.Status == "enabled" || input.Status == "disabled")
}
