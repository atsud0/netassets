package securityreference

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid input")
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type Organization struct {
	ID     string `json:"id"`
	Code   string `json:"code"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type Asset struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	Value              string `json:"value"`
	Name               string `json:"name,omitempty"`
	OrganizationID     string `json:"organization_id"`
	OrganizationName   string `json:"organization_name"`
	BusinessSystemID   string `json:"business_system_id,omitempty"`
	BusinessSystemName string `json:"business_system_name,omitempty"`
	NetworkScope       string `json:"network_scope"`
	NetworkAreaName    string `json:"network_area_name,omitempty"`
	Environment        string `json:"environment"`
	Importance         string `json:"importance"`
	Status             string `json:"status"`
}

type BusinessSystem struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"`
}

type Filter struct {
	Page           pagination.Query
	OrganizationID string
	Keyword        string
	PublicOnly     bool
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func canRead(principal auth.Principal) bool {
	return principal.HasGroupSecurityRole() && (principal.CanManageSecurityTests() ||
		principal.CanManageSecurityFindings() ||
		principal.CanManageSecurityWorkOrders())
}

func validateFilter(filter Filter) error {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	if filter.OrganizationID != "" && !uuidPattern.MatchString(filter.OrganizationID) {
		return ErrInvalid
	}
	if len(strings.TrimSpace(filter.Keyword)) > 200 {
		return ErrInvalid
	}
	return nil
}

func (s *Service) SearchOrganizations(ctx context.Context, principal auth.Principal, filter Filter) (pagination.Result[Organization], error) {
	if !canRead(principal) {
		return pagination.Result[Organization]{}, ErrForbidden
	}
	if err := validateFilter(filter); err != nil {
		return pagination.Result[Organization]{}, err
	}
	keyword := strings.TrimSpace(filter.Keyword)
	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM organizations
		WHERE status = 'enabled'
		  AND ($1 = '' OR concat_ws(' ', organization_code, organization_name, short_name) ILIKE '%' || $1 || '%')
	`, keyword).Scan(&total); err != nil {
		return pagination.Result[Organization]{}, fmt.Errorf("count security reference organizations: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, organization_code, organization_name, organization_type, status
		FROM organizations
		WHERE status = 'enabled'
		  AND ($1 = '' OR concat_ws(' ', organization_code, organization_name, short_name) ILIKE '%' || $1 || '%')
		ORDER BY CASE WHEN organization_type = 'group' THEN 0 ELSE 1 END, organization_code
		LIMIT $2 OFFSET $3
	`, keyword, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[Organization]{}, fmt.Errorf("search security reference organizations: %w", err)
	}
	defer rows.Close()
	items := make([]Organization, 0, filter.Page.PageSize)
	for rows.Next() {
		var item Organization
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.Type, &item.Status); err != nil {
			return pagination.Result[Organization]{}, fmt.Errorf("scan security reference organization: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Organization]{}, fmt.Errorf("iterate security reference organizations: %w", err)
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

func (s *Service) SearchAssets(ctx context.Context, principal auth.Principal, filter Filter) (pagination.Result[Asset], error) {
	if !canRead(principal) {
		return pagination.Result[Asset]{}, ErrForbidden
	}
	if err := validateFilter(filter); err != nil {
		return pagination.Result[Asset]{}, err
	}
	organizationID := strings.TrimSpace(filter.OrganizationID)
	keyword := strings.TrimSpace(filter.Keyword)
	const fromWhere = `
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		JOIN organizations o ON o.id = ao.organization_id
		LEFT JOIN asset_network_areas na ON na.id = a.network_area_id
		LEFT JOIN LATERAL (
			SELECT bs.id, bs.system_name
			FROM system_assets sa
			JOIN business_systems bs ON bs.id = sa.business_system_id AND bs.deleted_at IS NULL
			WHERE sa.asset_id = a.id
			ORDER BY sa.is_primary DESC, sa.created_at
			LIMIT 1
		) bs ON true
		WHERE a.deleted_at IS NULL
		  AND a.status <> 'archived'
		  AND ($1 = '' OR o.id::text = $1)
		  AND (NOT $2::boolean OR a.network_scope = 'public')
		  AND ($3 = '' OR concat_ws(' ', a.asset_value, a.asset_name, o.organization_name, bs.system_name, na.area_name) ILIKE '%' || $3 || '%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+fromWhere, organizationID, filter.PublicOnly, keyword).Scan(&total); err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("count security reference assets: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id::text, a.asset_type, a.asset_value, COALESCE(a.asset_name, ''),
		       o.id::text, o.organization_name, COALESCE(bs.id::text, ''), COALESCE(bs.system_name, ''),
		       a.network_scope, COALESCE(na.area_name, ''), a.environment, a.importance, a.status
	`+fromWhere+`
		ORDER BY o.organization_code, a.asset_type, a.normalized_value
		LIMIT $4 OFFSET $5
	`, organizationID, filter.PublicOnly, keyword, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("search security reference assets: %w", err)
	}
	defer rows.Close()
	items := make([]Asset, 0, filter.Page.PageSize)
	for rows.Next() {
		var item Asset
		if err := rows.Scan(&item.ID, &item.Type, &item.Value, &item.Name, &item.OrganizationID, &item.OrganizationName,
			&item.BusinessSystemID, &item.BusinessSystemName, &item.NetworkScope, &item.NetworkAreaName,
			&item.Environment, &item.Importance, &item.Status); err != nil {
			return pagination.Result[Asset]{}, fmt.Errorf("scan security reference asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("iterate security reference assets: %w", err)
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

func (s *Service) SearchBusinessSystems(ctx context.Context, principal auth.Principal, filter Filter) (pagination.Result[BusinessSystem], error) {
	if !canRead(principal) {
		return pagination.Result[BusinessSystem]{}, ErrForbidden
	}
	if err := validateFilter(filter); err != nil {
		return pagination.Result[BusinessSystem]{}, err
	}
	organizationID := strings.TrimSpace(filter.OrganizationID)
	keyword := strings.TrimSpace(filter.Keyword)
	const where = `
		FROM business_systems bs
		WHERE bs.deleted_at IS NULL AND bs.system_status <> 'archived'
		  AND ($1 = '' OR bs.owner_organization_id::text = $1)
		  AND ($2 = '' OR concat_ws(' ', bs.system_code, bs.system_name, bs.short_name) ILIKE '%' || $2 || '%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+where, organizationID, keyword).Scan(&total); err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("count security reference business systems: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT bs.id::text, bs.system_code, bs.system_name, bs.owner_organization_id::text, bs.system_status
	`+where+`
		ORDER BY bs.system_code
		LIMIT $3 OFFSET $4
	`, organizationID, keyword, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("search security reference business systems: %w", err)
	}
	defer rows.Close()
	items := make([]BusinessSystem, 0, filter.Page.PageSize)
	for rows.Next() {
		var item BusinessSystem
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.OrganizationID, &item.Status); err != nil {
			return pagination.Result[BusinessSystem]{}, fmt.Errorf("scan security reference business system: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("iterate security reference business systems: %w", err)
	}
	return pagination.NewResult(items, total, filter.Page), nil
}
