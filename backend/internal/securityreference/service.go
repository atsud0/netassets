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

const (
	maxSelectionIDs      = 100
	selectionResultLimit = 201
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
	NetworkAreaID      string `json:"network_area_id,omitempty"`
	NetworkAreaName    string `json:"network_area_name,omitempty"`
	Environment        string `json:"environment"`
	Importance         string `json:"importance"`
	Status             string `json:"status"`
}

type BusinessSystem struct {
	ID               string `json:"id"`
	Code             string `json:"code"`
	Name             string `json:"name"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	Status           string `json:"status"`
}

type NetworkArea struct {
	ID               string `json:"id"`
	Code             string `json:"code,omitempty"`
	Name             string `json:"name"`
	OrganizationID   string `json:"organization_id"`
	OrganizationName string `json:"organization_name"`
	Status           string `json:"status"`
}

type Filter struct {
	Page           pagination.Query
	OrganizationID string
	Keyword        string
	PublicOnly     bool
}

type AssetSelectionInput struct {
	Dimension string   `json:"dimension"`
	IDs       []string `json:"ids"`
}

type AssetSelectionResult struct {
	Items []Asset `json:"items"`
	Total int     `json:"total"`
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

func normalizeAssetSelection(input *AssetSelectionInput) {
	input.Dimension = strings.TrimSpace(input.Dimension)
	seen := make(map[string]struct{}, len(input.IDs))
	ids := make([]string, 0, len(input.IDs))
	for _, raw := range input.IDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	input.IDs = ids
}

func validAssetSelection(input AssetSelectionInput) bool {
	if input.Dimension != "organization" && input.Dimension != "business_system" && input.Dimension != "network_area" {
		return false
	}
	if len(input.IDs) < 1 || len(input.IDs) > maxSelectionIDs {
		return false
	}
	for _, id := range input.IDs {
		if !uuidPattern.MatchString(id) {
			return false
		}
	}
	return true
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

const assetReferenceFrom = `
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
	) bs ON true`

const assetReferenceSelect = `
	SELECT a.id::text, a.asset_type, a.asset_value, COALESCE(a.asset_name, ''),
	       o.id::text, o.organization_name, COALESCE(bs.id::text, ''), COALESCE(bs.system_name, ''),
	       a.network_scope, COALESCE(na.id::text, ''), COALESCE(na.area_name, ''),
	       a.environment, a.importance, a.status`

func (s *Service) SearchAssets(ctx context.Context, principal auth.Principal, filter Filter) (pagination.Result[Asset], error) {
	if !canRead(principal) {
		return pagination.Result[Asset]{}, ErrForbidden
	}
	if err := validateFilter(filter); err != nil {
		return pagination.Result[Asset]{}, err
	}
	organizationID := strings.TrimSpace(filter.OrganizationID)
	keyword := strings.TrimSpace(filter.Keyword)
	const where = `
		WHERE a.deleted_at IS NULL
		  AND a.status <> 'archived'
		  AND ($1 = '' OR o.id::text = $1)
		  AND (NOT $2::boolean OR a.network_scope = 'public')
		  AND ($3 = '' OR concat_ws(' ', a.asset_value, a.asset_name, o.organization_name, bs.system_name, na.area_name) ILIKE '%' || $3 || '%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+assetReferenceFrom+where, organizationID, filter.PublicOnly, keyword).Scan(&total); err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("count security reference assets: %w", err)
	}
	rows, err := s.pool.Query(ctx, assetReferenceSelect+assetReferenceFrom+where+`
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
		if err := scanAsset(rows, &item); err != nil {
			return pagination.Result[Asset]{}, fmt.Errorf("scan security reference asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("iterate security reference assets: %w", err)
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

func (s *Service) ResolvePublicAssets(ctx context.Context, principal auth.Principal, input AssetSelectionInput) (AssetSelectionResult, error) {
	if !canRead(principal) {
		return AssetSelectionResult{}, ErrForbidden
	}
	normalizeAssetSelection(&input)
	if !validAssetSelection(input) {
		return AssetSelectionResult{}, ErrInvalid
	}
	predicate := ""
	switch input.Dimension {
	case "organization":
		predicate = `o.id = ANY($1::uuid[])`
	case "business_system":
		predicate = `EXISTS (
			SELECT 1 FROM system_assets selected_sa
			JOIN business_systems selected_bs ON selected_bs.id = selected_sa.business_system_id
			WHERE selected_sa.asset_id = a.id AND selected_bs.id = ANY($1::uuid[])
			  AND selected_bs.deleted_at IS NULL AND selected_bs.system_status <> 'archived'
		)`
	case "network_area":
		predicate = `a.network_area_id = ANY($1::uuid[]) AND na.status = 'enabled'`
	}
	where := ` WHERE a.deleted_at IS NULL AND a.status <> 'archived' AND a.network_scope = 'public' AND (` + predicate + `)`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+assetReferenceFrom+where, input.IDs).Scan(&total); err != nil {
		return AssetSelectionResult{}, fmt.Errorf("count resolved public assets: %w", err)
	}
	rows, err := s.pool.Query(ctx, assetReferenceSelect+assetReferenceFrom+where+`
		ORDER BY o.organization_code, a.asset_type, a.normalized_value
		LIMIT $2`, input.IDs, selectionResultLimit)
	if err != nil {
		return AssetSelectionResult{}, fmt.Errorf("resolve public assets: %w", err)
	}
	defer rows.Close()
	items := make([]Asset, 0, min(total, selectionResultLimit))
	for rows.Next() {
		var item Asset
		if err := scanAsset(rows, &item); err != nil {
			return AssetSelectionResult{}, fmt.Errorf("scan resolved public asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return AssetSelectionResult{}, fmt.Errorf("iterate resolved public assets: %w", err)
	}
	return AssetSelectionResult{Items: items, Total: total}, nil
}

func scanAsset(row interface{ Scan(dest ...any) error }, item *Asset) error {
	return row.Scan(&item.ID, &item.Type, &item.Value, &item.Name, &item.OrganizationID, &item.OrganizationName,
		&item.BusinessSystemID, &item.BusinessSystemName, &item.NetworkScope, &item.NetworkAreaID, &item.NetworkAreaName,
		&item.Environment, &item.Importance, &item.Status)
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
		JOIN organizations o ON o.id = bs.owner_organization_id
		WHERE bs.deleted_at IS NULL AND bs.system_status <> 'archived'
		  AND ($1 = '' OR bs.owner_organization_id::text = $1)
		  AND ($2 = '' OR concat_ws(' ', bs.system_code, bs.system_name, bs.short_name, o.organization_name) ILIKE '%' || $2 || '%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+where, organizationID, keyword).Scan(&total); err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("count security reference business systems: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT bs.id::text, bs.system_code, bs.system_name, bs.owner_organization_id::text, o.organization_name, bs.system_status
	`+where+`
		ORDER BY o.organization_code, bs.system_code
		LIMIT $3 OFFSET $4
	`, organizationID, keyword, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("search security reference business systems: %w", err)
	}
	defer rows.Close()
	items := make([]BusinessSystem, 0, filter.Page.PageSize)
	for rows.Next() {
		var item BusinessSystem
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.OrganizationID, &item.OrganizationName, &item.Status); err != nil {
			return pagination.Result[BusinessSystem]{}, fmt.Errorf("scan security reference business system: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("iterate security reference business systems: %w", err)
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

func (s *Service) SearchNetworkAreas(ctx context.Context, principal auth.Principal, filter Filter) (pagination.Result[NetworkArea], error) {
	if !canRead(principal) {
		return pagination.Result[NetworkArea]{}, ErrForbidden
	}
	if err := validateFilter(filter); err != nil {
		return pagination.Result[NetworkArea]{}, err
	}
	organizationID := strings.TrimSpace(filter.OrganizationID)
	keyword := strings.TrimSpace(filter.Keyword)
	const where = `
		FROM asset_network_areas na
		JOIN organizations o ON o.id = na.organization_id
		WHERE na.status = 'enabled' AND o.status = 'enabled'
		  AND ($1 = '' OR na.organization_id::text = $1)
		  AND ($2 = '' OR concat_ws(' ', na.area_code, na.area_name, o.organization_name) ILIKE '%' || $2 || '%')`
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) `+where, organizationID, keyword).Scan(&total); err != nil {
		return pagination.Result[NetworkArea]{}, fmt.Errorf("count security reference network areas: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT na.id::text, COALESCE(na.area_code, ''), na.area_name, na.organization_id::text, o.organization_name, na.status
	`+where+`
		ORDER BY o.organization_code, na.sort_order, na.area_name
		LIMIT $3 OFFSET $4
	`, organizationID, keyword, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[NetworkArea]{}, fmt.Errorf("search security reference network areas: %w", err)
	}
	defer rows.Close()
	items := make([]NetworkArea, 0, filter.Page.PageSize)
	for rows.Next() {
		var item NetworkArea
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.OrganizationID, &item.OrganizationName, &item.Status); err != nil {
			return pagination.Result[NetworkArea]{}, fmt.Errorf("scan security reference network area: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[NetworkArea]{}, fmt.Errorf("iterate security reference network areas: %w", err)
	}
	return pagination.NewResult(items, total, filter.Page), nil
}
