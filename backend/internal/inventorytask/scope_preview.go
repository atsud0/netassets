package inventorytask

import (
	"context"
	"fmt"
	"strings"

	"netassets/internal/auth"
)

// ScopePreviewInput describes the resource boundary a group administrator is
// considering before an inventory task is created.
type ScopePreviewInput struct {
	OrganizationIDs   []string `json:"organization_ids"`
	RequiredFields    []string `json:"required_fields"`
	AssetNetworkScope string   `json:"asset_network_scope"`
}

type ScopePreviewItem struct {
	OrganizationID   string `json:"organization_id"`
	OrganizationCode string `json:"organization_code"`
	OrganizationName string `json:"organization_name"`
	SystemCount      int64  `json:"system_count"`
	AssetCount       int64  `json:"asset_count"`
}

type ScopePreview struct {
	Items                  []ScopePreviewItem `json:"items"`
	OrganizationCount      int                `json:"organization_count"`
	SystemCount            int64              `json:"system_count"`
	AssetCount             int64              `json:"asset_count"`
	EmptyOrganizationCount int                `json:"empty_organization_count"`
}

func (s *Service) PreviewScope(ctx context.Context, principal auth.Principal, input ScopePreviewInput) (ScopePreview, error) {
	if !principal.HasRole("group_admin") {
		return ScopePreview{}, ErrForbidden
	}
	if err := normalizeScopePreviewInput(&input); err != nil {
		return ScopePreview{}, err
	}
	includeSystems, includeAssets := selectedResourceTypes(input.RequiredFields)

	rows, err := s.pool.Query(ctx, `
		WITH selected_organizations AS (
			SELECT id, organization_code, organization_name
			FROM organizations
			WHERE id::text = ANY($1)
			  AND status = 'enabled'
			  AND organization_type <> 'group'
		), system_counts AS (
			SELECT system.owner_organization_id AS organization_id, count(*)::bigint AS item_count
			FROM business_systems system
			JOIN selected_organizations organization ON organization.id = system.owner_organization_id
			WHERE $2::boolean AND system.deleted_at IS NULL
			GROUP BY system.owner_organization_id
		), asset_counts AS (
			SELECT owner.organization_id, count(*)::bigint AS item_count
			FROM asset_organizations owner
			JOIN selected_organizations organization ON organization.id = owner.organization_id
			JOIN assets asset ON asset.id = owner.asset_id
			WHERE $3::boolean
			  AND owner.is_primary
			  AND asset.deleted_at IS NULL
			  AND ($4 = 'all' OR asset.network_scope = $4)
			GROUP BY owner.organization_id
		)
		SELECT organization.id::text, organization.organization_code, organization.organization_name,
		       COALESCE(system_count.item_count, 0), COALESCE(asset_count.item_count, 0)
		FROM selected_organizations organization
		LEFT JOIN system_counts system_count ON system_count.organization_id = organization.id
		LEFT JOIN asset_counts asset_count ON asset_count.organization_id = organization.id
		ORDER BY organization.organization_code, organization.id
	`, input.OrganizationIDs, includeSystems, includeAssets, input.AssetNetworkScope)
	if err != nil {
		return ScopePreview{}, fmt.Errorf("preview inventory task scope: %w", err)
	}
	defer rows.Close()

	result := ScopePreview{Items: make([]ScopePreviewItem, 0, len(input.OrganizationIDs))}
	for rows.Next() {
		var item ScopePreviewItem
		if err := rows.Scan(&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.SystemCount, &item.AssetCount); err != nil {
			return ScopePreview{}, fmt.Errorf("scan inventory task scope preview: %w", err)
		}
		result.Items = append(result.Items, item)
		result.SystemCount += item.SystemCount
		result.AssetCount += item.AssetCount
		if item.SystemCount == 0 && item.AssetCount == 0 {
			result.EmptyOrganizationCount++
		}
	}
	if err := rows.Err(); err != nil {
		return ScopePreview{}, fmt.Errorf("iterate inventory task scope preview: %w", err)
	}
	if len(result.Items) != len(input.OrganizationIDs) {
		return ScopePreview{}, ErrNotFound
	}
	result.OrganizationCount = len(result.Items)
	return result, nil
}

func normalizeScopePreviewInput(input *ScopePreviewInput) error {
	input.AssetNetworkScope = strings.TrimSpace(input.AssetNetworkScope)
	if input.AssetNetworkScope == "" {
		input.AssetNetworkScope = "all"
	}
	input.OrganizationIDs = uniqueNonEmpty(input.OrganizationIDs)
	input.RequiredFields = uniqueNonEmpty(input.RequiredFields)
	if len(input.OrganizationIDs) == 0 || len(input.OrganizationIDs) > 1000 || len(input.RequiredFields) > 100 ||
		!validAssetNetworkScope(input.AssetNetworkScope) || !validRequiredFields(input.RequiredFields) {
		return ErrInvalid
	}
	return nil
}
