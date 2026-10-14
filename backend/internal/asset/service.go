package asset

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/customfield"
	"netassets/internal/exportfile"
	"netassets/internal/importmapping"
	"netassets/internal/inventorydraft"
	"netassets/internal/notification"
	"netassets/internal/pagination"
	"netassets/internal/remediationretire"
)

var (
	ErrForbidden    = errors.New("forbidden")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrEditConflict = errors.New("edit conflict")
	// ErrVersionRequired is returned when a working-version write arrives without
	// the optimistic-lock value. Silently treating a missing version as "no lock"
	// is what let two填报员 overwrite each other before the CAS existed.
	ErrVersionRequired = errors.New("expected_version is required")
	ErrInvalid         = errors.New("invalid input")
	ErrTaskBound       = errors.New("asset is bound to an inventory task")
	ErrExportTooLarge  = errors.New("synchronous xlsx export too large")
)

const MaxImportRows = 5000
const MaxSelectedExportRows = 200

var assetUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

const (
	ImportModeSkipDuplicates = "skip_duplicates"
	ImportModeUpdateWorking  = "update_working"
)

type Asset struct {
	ID                   string              `json:"id"`
	Type                 string              `json:"type"`
	Value                string              `json:"value"`
	NormalizedValue      string              `json:"normalized_value"`
	Name                 string              `json:"name,omitempty"`
	OrganizationID       string              `json:"organization_id"`
	OrganizationCode     string              `json:"organization_code"`
	OrganizationName     string              `json:"organization_name"`
	BusinessSystemID     string              `json:"business_system_id,omitempty"`
	BusinessSystemCode   string              `json:"business_system_code,omitempty"`
	BusinessSystemName   string              `json:"business_system_name,omitempty"`
	Environment          string              `json:"environment"`
	NetworkScope         string              `json:"network_scope"`
	NetworkAreaID        string              `json:"network_area_id,omitempty"`
	NetworkAreaName      string              `json:"network_area_name,omitempty"`
	Status               string              `json:"status"`
	Importance           string              `json:"importance"`
	Source               string              `json:"source"`
	OwnerUserID          string              `json:"owner_user_id,omitempty"`
	OwnerUsername        string              `json:"owner_username,omitempty"`
	OwnerUserDisplayName string              `json:"owner_user_display_name,omitempty"`
	OwnerName            string              `json:"owner_name,omitempty"`
	Notes                string              `json:"notes,omitempty"`
	ExpiresAt            *time.Time          `json:"expires_at,omitempty"`
	CreatedAt            time.Time           `json:"created_at"`
	UpdatedAt            time.Time           `json:"updated_at"`
	ReportingStatus      string              `json:"reporting_status"`
	IsWorkingCopy        bool                `json:"is_working_copy"`
	PublishedID          string              `json:"published_id,omitempty"`
	TaskID               string              `json:"task_id,omitempty"`
	DraftStatus          string              `json:"draft_status,omitempty"`
	Operation            string              `json:"operation,omitempty"`
	Version              int                 `json:"version"`
	AssignedTo           string              `json:"assigned_to,omitempty"`
	LastEditedBy         string              `json:"last_edited_by,omitempty"`
	TakeoverReason       string              `json:"takeover_reason,omitempty"`
	CustomValues         []customfield.Value `json:"custom_values,omitempty"`
}

type CreateInput struct {
	Type              string              `json:"type"`
	Value             string              `json:"value"`
	Name              string              `json:"name"`
	OrganizationID    string              `json:"organization_id"`
	BusinessSystemID  string              `json:"business_system_id"`
	Environment       string              `json:"environment"`
	NetworkScope      string              `json:"network_scope"`
	NetworkAreaID     string              `json:"network_area_id"`
	Status            string              `json:"status"`
	Importance        string              `json:"importance"`
	OwnerUserID       string              `json:"owner_user_id"`
	OwnerName         string              `json:"owner_name"`
	Notes             string              `json:"notes"`
	ExpiresAt         *time.Time          `json:"expires_at"`
	CustomValues      []customfield.Value `json:"custom_values,omitempty"`
	ExpectedUpdatedAt *time.Time          `json:"expected_updated_at,omitempty"`
	ExpectedVersion   *int                `json:"expected_version,omitempty"`
	// DirectChangeReason is required when a group administrator writes the
	// formal ledger outside the inventory task. It is stored on the recheck task
	// that is raised for the owning organization.
	DirectChangeReason string `json:"direct_change_reason,omitempty"`
}

type ImportError struct {
	Row     int    `json:"row"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ImportResult struct {
	ID             string        `json:"id"`
	FileName       string        `json:"file_name"`
	FileType       string        `json:"file_type"`
	ImportMode     string        `json:"import_mode"`
	Status         string        `json:"status"`
	TotalCount     int           `json:"total_count"`
	SuccessCount   int           `json:"success_count"`
	CreatedCount   int           `json:"created_count"`
	UpdatedCount   int           `json:"updated_count"`
	FailureCount   int           `json:"failure_count"`
	DuplicateCount int           `json:"duplicate_count"`
	Errors         []ImportError `json:"errors"`
}

type ColumnMapping = importmapping.Mapping
type ImportColumnAnalysis = importmapping.Analysis

type ImportHistory struct {
	ImportResult
	OrganizationID   string    `json:"organization_id"`
	OrganizationCode string    `json:"organization_code"`
	OrganizationName string    `json:"organization_name"`
	CreatedByName    string    `json:"created_by_name,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type ExportFilter struct {
	OrganizationID string
	Type           string
	Status         string
	Keyword        string
	SelectedIDs    []string
}

// SearchFilter describes the server-side filters available on the asset ledger.
// It deliberately shares the visible filter names with the HTTP API, while page
// controls the bounded result window.
type SearchFilter struct {
	OrganizationID string
	Type           string
	Status         string
	Keyword        string
	SelectedIDs    []string
	Page           pagination.Query
}

type OwnerOption struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email,omitempty"`
	MobilePhone string `json:"mobile_phone,omitempty"`
}

type Service struct{ pool *pgxpool.Pool }

type fieldChange struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Asset, error) {
	if usesWorkingInventory(principal) {
		result, err := s.searchWorkingCreates(ctx, principal, SearchFilter{Page: pagination.Query{Page: 1, PageSize: pagination.MaxPageSize}})
		return result.Items, err
	}
	if !principal.HasPermission("asset.manage") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT a.id::text, a.asset_type, a.asset_value, a.normalized_value, COALESCE(a.asset_name, ''),
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       a.environment, a.network_scope, COALESCE(na.id::text, ''), COALESCE(na.area_name, ''),
		       a.status, a.importance, a.source,
		       COALESCE(ao.owner_user_id::text, ''), COALESCE(owner_user.username, ''), COALESCE(owner_user.display_name, ''),
		       COALESCE(ao.owner_name, ''), COALESCE(a.notes, ''), a.expires_at, a.created_at, a.updated_at
		FROM assets a
		LEFT JOIN asset_network_areas na ON na.id = a.network_area_id
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		LEFT JOIN users owner_user ON owner_user.id = ao.owner_user_id
		JOIN organizations o ON o.id = ao.organization_id
		LEFT JOIN LATERAL (
			SELECT system.id, system.system_code, system.system_name
			FROM system_assets sa JOIN business_systems system ON system.id = sa.business_system_id
			WHERE sa.asset_id = a.id AND system.deleted_at IS NULL
			ORDER BY sa.is_primary DESC, sa.created_at LIMIT 1
		) bs ON true
		WHERE a.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
		ORDER BY o.organization_code, a.asset_type, a.normalized_value
	`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list assets: %w", err)
	}
	defer rows.Close()
	items := make([]Asset, 0)
	for rows.Next() {
		var item Asset
		if err := rows.Scan(&item.ID, &item.Type, &item.Value, &item.NormalizedValue, &item.Name,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.BusinessSystemID, &item.BusinessSystemCode, &item.BusinessSystemName,
			&item.Environment, &item.NetworkScope, &item.NetworkAreaID, &item.NetworkAreaName,
			&item.Status, &item.Importance, &item.Source, &item.OwnerUserID, &item.OwnerUsername, &item.OwnerUserDisplayName,
			&item.OwnerName, &item.Notes, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assets: %w", err)
	}
	return items, nil
}

// Search returns a bounded database page of assets. The window total is computed
// before LIMIT/OFFSET, so callers receive the real scoped result count without
// materializing the complete asset inventory in the application process.
func (s *Service) Search(ctx context.Context, principal auth.Principal, query pagination.Query) (pagination.Result[Asset], error) {
	return s.SearchWithFilter(ctx, principal, SearchFilter{Page: query})
}

// SearchOwnerOptions returns enabled users appointed to the selected asset
// organization. It intentionally lives in the asset domain so asset editors do
// not need the broader user.manage permission just to bind an accountable user.
func (s *Service) SearchOwnerOptions(ctx context.Context, principal auth.Principal, organizationID, keyword string, query pagination.Query) (pagination.Result[OwnerOption], error) {
	if !principal.HasPermission("asset.manage") {
		return pagination.Result[OwnerOption]{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return pagination.Result[OwnerOption]{}, ErrInvalid
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
	if err != nil {
		return pagination.Result[OwnerOption]{}, err
	}
	if !allowed {
		return pagination.Result[OwnerOption]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, u.username, u.display_name, COALESCE(u.email, ''), COALESCE(u.mobile_phone, ''), count(*) OVER ()
		FROM users u
		JOIN user_organizations uo ON uo.user_id = u.id
		WHERE uo.organization_id = $1::uuid
		  AND u.status = 'enabled'
		  AND u.deleted_at IS NULL
		  AND ($2 = '' OR concat_ws(' ', u.username, u.display_name, u.email, u.mobile_phone) ILIKE '%' || $2 || '%')
		ORDER BY u.display_name, u.username
		LIMIT $3 OFFSET $4
	`, organizationID, strings.TrimSpace(keyword), query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[OwnerOption]{}, fmt.Errorf("search asset owner options: %w", err)
	}
	defer rows.Close()
	items := make([]OwnerOption, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item OwnerOption
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &item.Email, &item.MobilePhone, &total); err != nil {
			return pagination.Result[OwnerOption]{}, fmt.Errorf("scan asset owner option: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[OwnerOption]{}, fmt.Errorf("iterate asset owner options: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*)
			FROM users u
			JOIN user_organizations uo ON uo.user_id = u.id
			WHERE uo.organization_id = $1::uuid
			  AND u.status = 'enabled'
			  AND u.deleted_at IS NULL
			  AND ($2 = '' OR concat_ws(' ', u.username, u.display_name, u.email, u.mobile_phone) ILIKE '%' || $2 || '%')
		`, organizationID, strings.TrimSpace(keyword)).Scan(&total); err != nil {
			return pagination.Result[OwnerOption]{}, fmt.Errorf("count asset owner options: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

// SearchWithFilter returns a bounded database page of assets. Filtering is
// applied in SQL before count(*) OVER(), so total always represents the real
// number of records visible to the caller under the active filters.
func (s *Service) SearchWithFilter(ctx context.Context, principal auth.Principal, filter SearchFilter) (pagination.Result[Asset], error) {
	if usesWorkingInventory(principal) {
		return s.searchWorkingCreates(ctx, principal, filter)
	}
	if !principal.HasPermission("asset.manage") {
		return pagination.Result[Asset]{}, ErrForbidden
	}
	query := filter.Page
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT a.id::text, a.asset_type, a.asset_value, a.normalized_value, COALESCE(a.asset_name, ''),
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       a.environment, a.network_scope, COALESCE(na.id::text, ''), COALESCE(na.area_name, ''),
		       a.status, a.importance, a.source,
		       COALESCE(ao.owner_user_id::text, ''), COALESCE(owner_user.username, ''), COALESCE(owner_user.display_name, ''),
		       COALESCE(ao.owner_name, ''), COALESCE(a.notes, ''), a.expires_at, a.created_at, a.updated_at,
		       count(*) OVER ()
		FROM assets a
		LEFT JOIN asset_network_areas na ON na.id = a.network_area_id
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		LEFT JOIN users owner_user ON owner_user.id = ao.owner_user_id
		JOIN organizations o ON o.id = ao.organization_id
		LEFT JOIN LATERAL (
			SELECT system.id, system.system_code, system.system_name
			FROM system_assets sa JOIN business_systems system ON system.id = sa.business_system_id
			WHERE sa.asset_id = a.id AND system.deleted_at IS NULL
			ORDER BY sa.is_primary DESC, sa.created_at LIMIT 1
		) bs ON true
		WHERE a.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
		  AND ($3 = '' OR a.asset_type = $3)
		  AND ($4 = '' OR a.status = $4)
		  AND ($5 = '' OR o.id::text = $5)
		  AND ($6 = '' OR concat_ws(' ', a.asset_value, a.asset_name, o.organization_name, bs.system_name, bs.system_code, na.area_name, ao.owner_name, owner_user.username, owner_user.display_name) ILIKE '%' || $6 || '%')
		  AND (COALESCE(cardinality($7::uuid[]), 0) = 0 OR a.id = ANY($7::uuid[]))
		ORDER BY o.organization_code, a.asset_type, a.normalized_value
		LIMIT $8 OFFSET $9
	`, principal.UserID, principal.HasRole("group_admin"), strings.TrimSpace(filter.Type), strings.TrimSpace(filter.Status), strings.TrimSpace(filter.OrganizationID), strings.TrimSpace(filter.Keyword), filter.SelectedIDs, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("search assets: %w", err)
	}
	defer rows.Close()
	items := make([]Asset, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item Asset
		if err := rows.Scan(&item.ID, &item.Type, &item.Value, &item.NormalizedValue, &item.Name,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.BusinessSystemID, &item.BusinessSystemCode, &item.BusinessSystemName,
			&item.Environment, &item.NetworkScope, &item.NetworkAreaID, &item.NetworkAreaName,
			&item.Status, &item.Importance, &item.Source, &item.OwnerUserID, &item.OwnerUsername, &item.OwnerUserDisplayName,
			&item.OwnerName, &item.Notes, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Asset]{}, fmt.Errorf("scan asset page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("iterate asset page: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
            WITH RECURSIVE descendant_roots(id) AS (
                SELECT ur.organization_id FROM user_roles ur
                JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
                JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
                JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots
                UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
            WHERE child.status='enabled' AND child.deleted_at IS NULL
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur
                JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
                JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'asset.manage'
                JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
                  AND ur.data_scope IN ('organization', 'organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*)
            FROM assets a
            LEFT JOIN asset_network_areas na ON na.id = a.network_area_id
            JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
			LEFT JOIN users owner_user ON owner_user.id = ao.owner_user_id
            JOIN organizations o ON o.id = ao.organization_id
            LEFT JOIN LATERAL (
                SELECT system.id, system.system_code, system.system_name
                FROM system_assets sa JOIN business_systems system ON system.id = sa.business_system_id
                WHERE sa.asset_id = a.id AND system.deleted_at IS NULL
                ORDER BY sa.is_primary DESC, sa.created_at LIMIT 1
            ) bs ON true
            WHERE a.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
	              AND ($3 = '' OR a.asset_type = $3)
	              AND ($4 = '' OR a.status = $4)
	              AND ($5 = '' OR o.id::text = $5)
	              AND ($6 = '' OR concat_ws(' ', a.asset_value, a.asset_name, o.organization_name, bs.system_name, bs.system_code, na.area_name, ao.owner_name, owner_user.username, owner_user.display_name) ILIKE '%' || $6 || '%')
	              AND (COALESCE(cardinality($7::uuid[]), 0) = 0 OR a.id = ANY($7::uuid[]))
	        `, principal.UserID, principal.HasRole("group_admin"), strings.TrimSpace(filter.Type), strings.TrimSpace(filter.Status), strings.TrimSpace(filter.OrganizationID), strings.TrimSpace(filter.Keyword), filter.SelectedIDs).Scan(&total); err != nil {
			return pagination.Result[Asset]{}, fmt.Errorf("count assets: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Export(ctx context.Context, principal auth.Principal, filter ExportFilter, format, requestID, ipAddress string) ([]byte, string, string, int, error) {
	selectedIDs, err := normalizeSelectedExportIDs(filter.SelectedIDs)
	if err != nil {
		return nil, "", "", 0, ErrInvalid
	}
	filter.SelectedIDs = selectedIDs
	page := pagination.Query{Page: 1, PageSize: pagination.MaxPageSize}
	result, err := s.SearchWithFilter(ctx, principal, SearchFilter{
		OrganizationID: filter.OrganizationID,
		Type:           filter.Type,
		Status:         filter.Status,
		Keyword:        filter.Keyword,
		SelectedIDs:    filter.SelectedIDs,
		Page:           page,
	})
	if err != nil {
		return nil, "", "", 0, err
	}
	if err := exportfile.ValidateSynchronousExport(format, result.Total); err != nil {
		if errors.Is(err, exportfile.ErrSynchronousXLSXTooLarge) {
			return nil, "", "", 0, ErrExportTooLarge
		}
		return nil, "", "", 0, ErrInvalid
	}
	items := append([]Asset(nil), result.Items...)
	for len(items) < result.Total && len(result.Items) == page.PageSize {
		page.Page++
		result, err = s.SearchWithFilter(ctx, principal, SearchFilter{
			OrganizationID: filter.OrganizationID,
			Type:           filter.Type,
			Status:         filter.Status,
			Keyword:        filter.Keyword,
			SelectedIDs:    filter.SelectedIDs,
			Page:           page,
		})
		if err != nil {
			return nil, "", "", 0, err
		}
		items = append(items, result.Items...)
	}
	customService := customfield.NewService(s.pool)
	definitions, err := customService.EnabledDefinitions(ctx, "asset")
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("load asset custom field definitions: %w", err)
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	customValues, err := customService.ValuesForResources(ctx, "asset", ids)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("load asset custom field values: %w", err)
	}
	data, contentType, fileName, err := buildExportWithCustomFields(items, definitions, customValues, format)
	if err != nil {
		return nil, "", "", 0, err
	}
	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'asset.export', 'asset', '生成网络资产导出文件',
		        jsonb_build_object('format', $3::text, 'count', $4::int, 'organization_id', $5::text, 'type', $6::text, 'status', $7::text, 'keyword', $8::text,
		                           'selected_count', $9::int, 'result', 'generated', 'delivery_state', 'unconfirmed'),
		        NULLIF($10, '')::inet, NULLIF($11, ''))
	`, principal.UserID, organizationID, strings.ToLower(strings.TrimPrefix(format, ".")), len(items), filter.OrganizationID, filter.Type, filter.Status, filter.Keyword, len(filter.SelectedIDs), ipAddress, requestID); err != nil {
		return nil, "", "", 0, fmt.Errorf("write asset export audit: %w", err)
	}
	return data, contentType, fileName, len(items), nil
}

// StreamCSVExport writes the scoped asset ledger page by page. Only one bounded
// database page and its custom-field values are retained at a time.
func (s *Service) StreamCSVExport(ctx context.Context, principal auth.Principal, filter ExportFilter, output io.Writer, ready exportfile.ReadyFunc, requestID, ipAddress string) (int, error) {
	selectedIDs, err := normalizeSelectedExportIDs(filter.SelectedIDs)
	if err != nil {
		return 0, ErrInvalid
	}
	filter.SelectedIDs = selectedIDs
	customService := customfield.NewService(s.pool)
	definitions, err := customService.EnabledDefinitions(ctx, "asset")
	if err != nil {
		return 0, fmt.Errorf("load asset custom field definitions: %w", err)
	}

	page := pagination.Query{Page: 1, PageSize: pagination.MaxPageSize}
	result, err := s.SearchWithFilter(ctx, principal, SearchFilter{
		OrganizationID: filter.OrganizationID,
		Type:           filter.Type,
		Status:         filter.Status,
		Keyword:        filter.Keyword,
		SelectedIDs:    filter.SelectedIDs,
		Page:           page,
	})
	if err != nil {
		return 0, err
	}
	if ready != nil {
		ready(exportfile.Metadata{ContentType: exportfile.CSVContentType, FileName: "网络资产台账.csv", Count: result.Total})
	}
	stream, err := exportfile.NewCSVStream(output, assetExportHeaders(definitions))
	if err != nil {
		return 0, fmt.Errorf("start asset csv export: %w", err)
	}

	written := 0
	for {
		ids := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		customValues, err := customService.ValuesForResources(ctx, "asset", ids)
		if err != nil {
			return written, fmt.Errorf("load asset custom field values: %w", err)
		}
		for _, item := range result.Items {
			if written >= result.Total {
				break
			}
			if err := stream.Write(assetExportRow(item, definitions, customValues)); err != nil {
				return written, fmt.Errorf("write asset csv row: %w", err)
			}
			written++
		}
		if err := stream.Flush(); err != nil {
			return written, fmt.Errorf("flush asset csv export: %w", err)
		}
		if written >= result.Total || len(result.Items) < page.PageSize {
			break
		}
		page.Page++
		result, err = s.SearchWithFilter(ctx, principal, SearchFilter{
			OrganizationID: filter.OrganizationID,
			Type:           filter.Type,
			Status:         filter.Status,
			Keyword:        filter.Keyword,
			SelectedIDs:    filter.SelectedIDs,
			Page:           page,
		})
		if err != nil {
			return written, err
		}
	}

	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'asset.export', 'asset', '生成网络资产导出文件',
		        jsonb_build_object('format', 'csv', 'count', $3::int, 'organization_id', $4::text, 'type', $5::text, 'status', $6::text, 'keyword', $7::text,
		                           'selected_count', $8::int, 'result', 'generated', 'delivery_state', 'response_written_unconfirmed'),
		        NULLIF($9, '')::inet, NULLIF($10, ''))
	`, principal.UserID, organizationID, written, filter.OrganizationID, filter.Type, filter.Status, filter.Keyword, len(filter.SelectedIDs), ipAddress, requestID); err != nil {
		return written, fmt.Errorf("write asset export audit: %w", err)
	}
	return written, nil
}

// ExportRowCount validates the frozen authorization/filter scope and returns the
// number of rows a background spreadsheet job will generate.
func (s *Service) ExportRowCount(ctx context.Context, principal auth.Principal, filter ExportFilter) (int, error) {
	selectedIDs, err := normalizeSelectedExportIDs(filter.SelectedIDs)
	if err != nil {
		return 0, ErrInvalid
	}
	filter.SelectedIDs = selectedIDs
	result, err := s.SearchWithFilter(ctx, principal, SearchFilter{
		OrganizationID: filter.OrganizationID,
		Type:           filter.Type,
		Status:         filter.Status,
		Keyword:        filter.Keyword,
		SelectedIDs:    filter.SelectedIDs,
		Page:           pagination.Query{Page: 1, PageSize: 1},
	})
	if err != nil {
		return 0, err
	}
	return result.Total, nil
}

// WriteXLSXExport generates an authorization-scoped workbook page by page.
func (s *Service) WriteXLSXExport(ctx context.Context, principal auth.Principal, filter ExportFilter, outputPath string, progress exportfile.ProgressFunc) (exportfile.Metadata, error) {
	selectedIDs, err := normalizeSelectedExportIDs(filter.SelectedIDs)
	if err != nil {
		return exportfile.Metadata{}, ErrInvalid
	}
	filter.SelectedIDs = selectedIDs
	customService := customfield.NewService(s.pool)
	definitions, err := customService.EnabledDefinitions(ctx, "asset")
	if err != nil {
		return exportfile.Metadata{}, fmt.Errorf("load asset custom field definitions: %w", err)
	}
	page := pagination.Query{Page: 1, PageSize: pagination.MaxPageSize}
	result, err := s.SearchWithFilter(ctx, principal, SearchFilter{
		OrganizationID: filter.OrganizationID,
		Type:           filter.Type,
		Status:         filter.Status,
		Keyword:        filter.Keyword,
		SelectedIDs:    filter.SelectedIDs,
		Page:           page,
	})
	if err != nil {
		return exportfile.Metadata{}, err
	}
	stream, err := exportfile.NewXLSXStream(outputPath, "网络资产", assetExportHeaders(definitions))
	if err != nil {
		return exportfile.Metadata{}, fmt.Errorf("start asset xlsx export: %w", err)
	}
	defer stream.Abort()
	written := 0
	if progress != nil {
		if err := progress(result.Total, written); err != nil {
			return exportfile.Metadata{}, err
		}
	}
	for {
		ids := make([]string, 0, len(result.Items))
		for _, item := range result.Items {
			ids = append(ids, item.ID)
		}
		values, err := customService.ValuesForResources(ctx, "asset", ids)
		if err != nil {
			return exportfile.Metadata{}, fmt.Errorf("load asset custom field values: %w", err)
		}
		for _, item := range result.Items {
			if written >= result.Total {
				break
			}
			if err := stream.Write(assetExportRow(item, definitions, values)); err != nil {
				return exportfile.Metadata{}, fmt.Errorf("write asset xlsx row: %w", err)
			}
			written++
		}
		if progress != nil {
			if err := progress(result.Total, written); err != nil {
				return exportfile.Metadata{}, err
			}
		}
		if written >= result.Total || len(result.Items) < page.PageSize {
			break
		}
		page.Page++
		result, err = s.SearchWithFilter(ctx, principal, SearchFilter{
			OrganizationID: filter.OrganizationID,
			Type:           filter.Type,
			Status:         filter.Status,
			Keyword:        filter.Keyword,
			SelectedIDs:    filter.SelectedIDs,
			Page:           page,
		})
		if err != nil {
			return exportfile.Metadata{}, err
		}
	}
	if err := stream.Close(); err != nil {
		return exportfile.Metadata{}, fmt.Errorf("finish asset xlsx export: %w", err)
	}
	return exportfile.Metadata{ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", FileName: "网络资产台账.xlsx", Count: written}, nil
}

func filterExportAssets(items []Asset, filter ExportFilter) []Asset {
	keyword := strings.ToLower(strings.TrimSpace(filter.Keyword))
	selected := make(map[string]struct{}, len(filter.SelectedIDs))
	for _, id := range filter.SelectedIDs {
		selected[strings.TrimSpace(id)] = struct{}{}
	}
	result := make([]Asset, 0, len(items))
	for _, item := range items {
		if len(selected) > 0 {
			if _, ok := selected[item.ID]; !ok {
				continue
			}
		}
		if filter.OrganizationID != "" && item.OrganizationID != filter.OrganizationID {
			continue
		}
		if filter.Type != "" && item.Type != filter.Type {
			continue
		}
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if keyword != "" {
			values := []string{item.Value, item.Name, item.OrganizationCode, item.OrganizationName, item.BusinessSystemCode, item.BusinessSystemName, item.OwnerName}
			matched := false
			for _, value := range values {
				if strings.Contains(strings.ToLower(value), keyword) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

func normalizeSelectedExportIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	result := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, rawID := range ids {
		id := strings.TrimSpace(rawID)
		if !assetUUIDPattern.MatchString(id) {
			return nil, ErrInvalid
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
		if len(result) > MaxSelectedExportRows {
			return nil, ErrInvalid
		}
	}
	return result, nil
}

func buildExport(items []Asset, format string) ([]byte, string, string, error) {
	return buildExportWithCustomFields(items, nil, nil, format)
}

func buildExportWithCustomFields(items []Asset, definitions []customfield.Definition, values map[string]map[string]any, format string) ([]byte, string, string, error) {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		rows = append(rows, assetExportRow(item, definitions, values))
	}
	data, contentType, fileName, err := exportfile.Build(format, "网络资产台账", assetExportHeaders(definitions), rows)
	if errors.Is(err, exportfile.ErrInvalidFormat) {
		return nil, "", "", ErrInvalid
	}
	if errors.Is(err, exportfile.ErrSynchronousXLSXTooLarge) {
		return nil, "", "", ErrExportTooLarge
	}
	return data, contentType, fileName, err
}

func assetExportHeaders(definitions []customfield.Definition) []string {
	headers := []string{"所属组织编码", "所属组织名称", "资产类型", "资产值", "资产名称", "业务系统编码", "业务系统名称", "环境", "网络暴露范围", "网络区域", "状态", "重要程度", "来源", "责任人账号", "负责人", "备注", "有效期至", "创建时间", "更新时间"}
	for _, definition := range definitions {
		headers = append(headers, "自定义："+definition.FieldName)
	}
	return headers
}

func assetExportRow(item Asset, definitions []customfield.Definition, values map[string]map[string]any) []string {
	row := []string{
		item.OrganizationCode, item.OrganizationName, assetExportLabel("type", item.Type), item.Value, item.Name,
		item.BusinessSystemCode, item.BusinessSystemName, assetExportLabel("environment", item.Environment), assetExportLabel("scope", item.NetworkScope), item.NetworkAreaName,
		assetExportLabel("status", item.Status), assetExportLabel("importance", item.Importance), assetExportLabel("source", item.Source),
		item.OwnerUsername, item.OwnerName, item.Notes, formatOptionalExportTime(item.ExpiresAt), formatExportTime(item.CreatedAt), formatExportTime(item.UpdatedAt),
	}
	for _, definition := range definitions {
		row = append(row, customfield.ExportValue(definition, values[item.ID][definition.FieldKey]))
	}
	return row
}

func assetExportLabel(kind, value string) string {
	labels := map[string]map[string]string{
		"type":        {"domain": "域名", "ip": "IP 地址", "url": "URL", "service": "网络服务", "other": "其他"},
		"environment": {"production": "生产", "staging": "预发布", "testing": "测试", "development": "开发", "office": "办公", "other": "其他"},
		"scope":       {"public": "公网", "private": "内网", "unknown": "未知"},
		"status":      {"active": "启用", "pending": "待确认", "disabled": "停用", "archived": "已归档"},
		"importance":  {"normal": "一般", "important": "重要", "core": "核心"},
		"source":      {"manual": "手工", "import": "导入", "api": "API", "plugin": "插件", "finding_import": "漏洞导入"},
	}
	if label, ok := labels[kind][value]; ok {
		return label
	}
	return value
}

func formatOptionalExportTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatExportTime(*value)
}

func formatExportTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05Z07:00")
}

// PrepareCreateInput normalizes and validates the fields shared by formal assets
// and inventory-task drafts. Reference authorization is intentionally handled by
// the caller because drafts are scoped by their task assignment.
func PrepareCreateInput(input *CreateInput) (string, error) {
	normalizeInput(input)
	normalizedValue, err := normalizeValue(input.Type, input.Value)
	if err != nil || !validInput(*input) {
		return "", ErrInvalid
	}
	return normalizedValue, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, source, requestID, ipAddress string) (Asset, error) {
	if usesWorkingInventory(principal) {
		return s.createUnreported(ctx, principal, input, requestID, ipAddress)
	}
	return s.create(ctx, principal, input, source, requestID, ipAddress, nil)
}

// CreateWithAfterCreate is the transactional variant used by workflows that
// must update their review record together with the newly created asset. The
// callback runs before the asset transaction commits; returning an error rolls
// the entire operation back.
func (s *Service) CreateWithAfterCreate(ctx context.Context, principal auth.Principal, input CreateInput, source, requestID, ipAddress string, afterCreate func(pgx.Tx, Asset) error) (Asset, error) {
	if usesWorkingInventory(principal) {
		return Asset{}, ErrInvalid
	}
	return s.create(ctx, principal, input, source, requestID, ipAddress, afterCreate)
}

func (s *Service) create(ctx context.Context, principal auth.Principal, input CreateInput, source, requestID, ipAddress string, afterCreate func(pgx.Tx, Asset) error) (Asset, error) {
	if !principal.HasPermission("asset.manage") {
		return Asset{}, ErrForbidden
	}
	normalizeInput(&input)
	if err := validateDirectChangeReason(input.DirectChangeReason); err != nil {
		return Asset{}, err
	}
	normalizedValue, err := normalizeValue(input.Type, input.Value)
	if err != nil || !validInput(input) {
		return Asset{}, ErrInvalid
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, input.OrganizationID, "asset.manage")
	if err != nil {
		return Asset{}, err
	}
	if !allowed {
		return Asset{}, ErrForbidden
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, fmt.Errorf("begin asset: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := createInTx(ctx, tx, principal, input, normalizedValue, source)
	if err != nil {
		return Asset{}, err
	}
	if afterCreate != nil {
		if err := afterCreate(tx, item); err != nil {
			return Asset{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.create', 'asset', $3, '新增网络资产',
		        jsonb_build_object('type', $4::text, 'value', $5::text, 'source', $6::text, 'direct_change_reason', NULLIF($7, '')),
		        NULLIF($8, '')::inet, NULLIF($9, ''))
	`, principal.UserID, input.OrganizationID, item.ID, item.Type, item.Value, source, input.DirectChangeReason, ipAddress, requestID); err != nil {
		return Asset{}, fmt.Errorf("write asset audit: %w", err)
	}
	if err := s.writeDirectChangeRecheck(ctx, tx, principal, DirectChangeCreate, item.ID, input.OrganizationID, input.DirectChangeReason,
		map[string]any{"after": item}, nil, requestID, ipAddress); err != nil {
		return Asset{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset: %w", err)
	}
	return item, nil
}

func createInTx(ctx context.Context, tx pgx.Tx, principal auth.Principal, input CreateInput, normalizedValue, source string) (Asset, error) {
	var organizationCode, organizationName, organizationStatus string
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id = $1::uuid AND deleted_at IS NULL`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	} else if err != nil {
		return Asset{}, fmt.Errorf("load asset organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return Asset{}, ErrInvalid
	}
	networkAreaName, err := validateNetworkArea(ctx, tx, input.NetworkAreaID, input.OrganizationID, false)
	if err != nil {
		return Asset{}, err
	}
	ownerUsername, ownerDisplayName, err := validateOwnerUser(ctx, tx, input.OwnerUserID, input.OrganizationID)
	if err != nil {
		return Asset{}, err
	}

	var systemCode, systemName string
	if input.BusinessSystemID != "" {
		var systemOrganizationID string
		if err := tx.QueryRow(ctx, `SELECT owner_organization_id::text, system_code, system_name FROM business_systems WHERE id = $1::uuid AND deleted_at IS NULL`, input.BusinessSystemID).
			Scan(&systemOrganizationID, &systemCode, &systemName); errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, ErrNotFound
		} else if err != nil {
			return Asset{}, fmt.Errorf("load asset business system: %w", err)
		}
		if systemOrganizationID != input.OrganizationID {
			return Asset{}, ErrInvalid
		}
	}

	// Formal assets are unique per (asset_type, normalized_value) only inside the
	// assets table. An organization may already have reserved the same value in
	// an active working version that has not been reported yet, and PostgreSQL
	// cannot express that cross-table rule, so check it here for every path that
	// writes a formal asset (manual create, headquarters import, and acceptance
	// of an imported asset candidate).
	var workingReserved bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_asset_drafts WHERE asset_type=$1 AND normalized_value=$2 AND draft_status IN `+inventorydraft.ActiveSQL+`)`, input.Type, normalizedValue).Scan(&workingReserved); err != nil {
		return Asset{}, fmt.Errorf("check asset working version reservation: %w", err)
	}
	if workingReserved {
		return Asset{}, ErrConflict
	}

	var item Asset
	err = tx.QueryRow(ctx, `
		INSERT INTO assets (asset_type, asset_value, normalized_value, asset_name, environment, network_scope, network_area_id,
		                    status, importance, source, notes, expires_at, created_by, updated_by, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, '')::uuid, $8, $9, $10, NULLIF($11, ''), $12, $13::uuid, $13::uuid, now(), now())
		RETURNING id::text, created_at, updated_at
	`, input.Type, input.Value, normalizedValue, input.Name, input.Environment, input.NetworkScope, input.NetworkAreaID,
		input.Status, input.Importance, source, input.Notes, input.ExpiresAt, principal.UserID).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Asset{}, ErrConflict
		}
		return Asset{}, fmt.Errorf("insert asset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary, owner_user_id, owner_name)
		VALUES ($1::uuid, $2::uuid, 'owner', true, NULLIF($3, '')::uuid, NULLIF($4, ''))
	`, item.ID, input.OrganizationID, input.OwnerUserID, input.OwnerName); err != nil {
		return Asset{}, fmt.Errorf("attach asset organization: %w", err)
	}
	if input.BusinessSystemID != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO system_assets (business_system_id, asset_id, relationship_type, is_primary, created_by)
			VALUES ($1::uuid, $2::uuid, 'uses', true, $3::uuid)
		`, input.BusinessSystemID, item.ID, principal.UserID); err != nil {
			return Asset{}, fmt.Errorf("attach asset system: %w", err)
		}
	}
	item.Type, item.Value, item.NormalizedValue, item.Name = input.Type, input.Value, normalizedValue, input.Name
	item.OrganizationID, item.OrganizationCode, item.OrganizationName = input.OrganizationID, organizationCode, organizationName
	item.BusinessSystemID, item.BusinessSystemCode, item.BusinessSystemName = input.BusinessSystemID, systemCode, systemName
	item.Environment, item.NetworkScope, item.NetworkAreaID, item.NetworkAreaName = input.Environment, input.NetworkScope, input.NetworkAreaID, networkAreaName
	item.Status, item.Importance = input.Status, input.Importance
	item.OwnerUserID, item.OwnerUsername, item.OwnerUserDisplayName = input.OwnerUserID, ownerUsername, ownerDisplayName
	item.Source, item.OwnerName, item.Notes, item.ExpiresAt = source, input.OwnerName, input.Notes, input.ExpiresAt
	return item, nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, assetID string) (Asset, error) {
	if usesWorkingInventory(principal) {
		if item, err := s.getWorking(ctx, principal, assetID); err == nil {
			return item, nil
		}
	}
	if !principal.HasPermission("asset.manage") {
		return Asset{}, ErrForbidden
	}
	if _, err := s.authorizeAsset(ctx, principal, assetID); err != nil {
		return Asset{}, err
	}
	return queryAsset(ctx, s.pool, assetID)
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, assetID string, input CreateInput, requestID, ipAddress string) (Asset, error) {
	if usesWorkingInventory(principal) {
		if _, err := s.getWorking(ctx, principal, assetID); err == nil {
			return s.updateWorking(ctx, principal, assetID, input, requestID, ipAddress)
		}
	}
	if !principal.HasPermission("asset.manage") {
		return Asset{}, ErrForbidden
	}
	current, err := s.Get(ctx, principal, assetID)
	if err != nil {
		return Asset{}, err
	}
	if usesWorkingInventory(principal) {
		return s.createWorkingUpdate(ctx, principal, current, input, requestID, ipAddress)
	}
	normalizeInput(&input)
	normalizedValue, err := normalizeValue(input.Type, input.Value)
	if err != nil || !validInput(input) {
		return Asset{}, ErrInvalid
	}
	if err := validateDirectChangeReason(input.DirectChangeReason); err != nil {
		return Asset{}, err
	}
	if input.OrganizationID != current.OrganizationID && !principal.HasRole("group_admin") {
		return Asset{}, ErrForbidden
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, input.OrganizationID, "asset.manage")
	if err != nil {
		return Asset{}, err
	}
	if !allowed {
		return Asset{}, ErrForbidden
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, fmt.Errorf("begin asset update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var organizationCode, organizationName, organizationStatus string
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id::text = $1 AND deleted_at IS NULL`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	} else if err != nil {
		return Asset{}, fmt.Errorf("load update organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return Asset{}, ErrInvalid
	}
	networkAreaName, err := validateNetworkArea(ctx, tx, input.NetworkAreaID, input.OrganizationID, current.NetworkAreaID == input.NetworkAreaID)
	if err != nil {
		return Asset{}, err
	}
	ownerUsername, ownerDisplayName, err := validateOwnerUser(ctx, tx, input.OwnerUserID, input.OrganizationID)
	if err != nil {
		return Asset{}, err
	}

	var systemCode, systemName string
	if input.BusinessSystemID != "" {
		var systemOrganizationID string
		if err := tx.QueryRow(ctx, `
			SELECT owner_organization_id::text, system_code, system_name
			FROM business_systems WHERE id::text = $1 AND deleted_at IS NULL
		`, input.BusinessSystemID).Scan(&systemOrganizationID, &systemCode, &systemName); errors.Is(err, pgx.ErrNoRows) {
			return Asset{}, ErrNotFound
		} else if err != nil {
			return Asset{}, fmt.Errorf("load update business system: %w", err)
		}
		if systemOrganizationID != input.OrganizationID {
			return Asset{}, ErrInvalid
		}
	}

	command, err := tx.Exec(ctx, `
		UPDATE assets SET asset_type = $2, asset_value = $3, normalized_value = $4, asset_name = NULLIF($5, ''),
		                  environment = $6, network_scope = $7, network_area_id = NULLIF($8, '')::uuid, status = $9, importance = $10,
		                  notes = NULLIF($11, ''), expires_at = $12, updated_by = $13::uuid, updated_at = now()
		WHERE id::text = $1 AND deleted_at IS NULL AND ($14::timestamptz IS NULL OR updated_at = $14::timestamptz)
	`, assetID, input.Type, input.Value, normalizedValue, input.Name, input.Environment, input.NetworkScope, input.NetworkAreaID,
		input.Status, input.Importance, input.Notes, input.ExpiresAt, principal.UserID, input.ExpectedUpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Asset{}, ErrConflict
		}
		return Asset{}, fmt.Errorf("update asset: %w", err)
	}
	if command.RowsAffected() == 0 {
		if input.ExpectedUpdatedAt != nil {
			return Asset{}, ErrEditConflict
		}
		return Asset{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM asset_organizations WHERE asset_id::text = $1 AND is_primary`, assetID); err != nil {
		return Asset{}, fmt.Errorf("clear primary asset organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary, owner_user_id, owner_name)
		VALUES ($1::uuid, $2::uuid, 'owner', true, NULLIF($3, '')::uuid, NULLIF($4, ''))
	`, assetID, input.OrganizationID, input.OwnerUserID, input.OwnerName); err != nil {
		return Asset{}, fmt.Errorf("update asset organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM system_assets WHERE asset_id::text = $1 AND is_primary`, assetID); err != nil {
		return Asset{}, fmt.Errorf("clear primary asset system: %w", err)
	}
	if input.BusinessSystemID != "" {
		if _, err := tx.Exec(ctx, `
			INSERT INTO system_assets (business_system_id, asset_id, relationship_type, is_primary, created_by)
			VALUES ($1::uuid, $2::uuid, 'uses', true, $3::uuid)
		`, input.BusinessSystemID, assetID, principal.UserID); err != nil {
			return Asset{}, fmt.Errorf("update asset system: %w", err)
		}
	}
	changes := assetFieldChanges(current, input, organizationName, systemName, networkAreaName, ownerUsername, ownerDisplayName)
	changesJSON, err := json.Marshal(changes)
	if err != nil {
		return Asset{}, fmt.Errorf("encode asset changes: %w", err)
	}
	// The before snapshot is the asset as this caller read it; recording it next
	// to the field-level diff is what lets the owning organization judge the
	// change without trusting the group administrator's own description.
	beforeSnapshot := current
	// Read the after-state through the transaction: a pool-level read would not
	// see this transaction's own UPDATE and would record a snapshot equal to the
	// before-state.
	updated, err := queryAsset(ctx, tx, assetID)
	if err != nil {
		return Asset{}, err
	}
	if err := s.writeDirectChangeRecheck(ctx, tx, principal, DirectChangeUpdate, assetID, input.OrganizationID, input.DirectChangeReason,
		map[string]any{"before": beforeSnapshot, "after": updated}, changes, requestID, ipAddress); err != nil {
		return Asset{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.update', 'asset', $3, '编辑网络资产',
		        jsonb_build_object('changes', $4::jsonb, 'direct_change_reason', NULLIF($5, '')), NULLIF($6, '')::inet, NULLIF($7, ''))
	`, principal.UserID, input.OrganizationID, assetID, changesJSON, input.DirectChangeReason, ipAddress, requestID); err != nil {
		return Asset{}, fmt.Errorf("write asset update audit: %w", err)
	}
	resourceLabel := strings.TrimSpace(input.Name)
	if resourceLabel == "" {
		resourceLabel = input.Value
	}
	if current.Status != input.Status {
		eventType := notification.EventAssetStatusChanged
		level := "warning"
		if input.Status == "archived" {
			eventType = notification.EventAssetArchived
		}
		if err := notification.WriteEvent(ctx, tx, notification.EventInput{
			EventType: eventType, Category: notification.CategoryAssetChange,
			TargetOrganizationID: input.OrganizationID, TargetUserID: input.OwnerUserID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
			ResourceType: "asset", ResourceID: assetID, ResourceLabel: resourceLabel, BatchKey: requestID, Level: level,
			Details: map[string]any{"before": current.Status, "after": input.Status},
		}); err != nil {
			return Asset{}, err
		}
	}
	if current.NetworkAreaID != input.NetworkAreaID {
		if err := notification.WriteEvent(ctx, tx, notification.EventInput{
			EventType: notification.EventAssetNetworkAreaChanged, Category: notification.CategoryAssetChange,
			TargetOrganizationID: input.OrganizationID, TargetUserID: input.OwnerUserID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
			ResourceType: "asset", ResourceID: assetID, ResourceLabel: resourceLabel, BatchKey: requestID, Level: "info",
			Details: map[string]any{"before": current.NetworkAreaName, "after": networkAreaName},
		}); err != nil {
			return Asset{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset update: %w", err)
	}
	return s.Get(ctx, principal, assetID)
}

// Delete removes an asset from the point of view of the caller. For
// organization users that means the working ledger (draft discard or an offline
// request), which is why the call accepts the optimistic-lock value the client
// last saw; group administrators still delete the published ledger directly.
func (s *Service) Delete(ctx context.Context, principal auth.Principal, assetID string, expectedVersion *int, directChangeReason, requestID, ipAddress string) error {
	if usesWorkingInventory(principal) {
		if _, err := s.getWorking(ctx, principal, assetID); err == nil {
			return s.deleteWorking(ctx, principal, assetID, expectedVersion, requestID, ipAddress)
		}
	}
	if !principal.HasPermission("asset.manage") {
		return ErrForbidden
	}
	current, err := s.Get(ctx, principal, assetID)
	if err != nil {
		return err
	}
	if usesWorkingInventory(principal) {
		return s.createWorkingDelete(ctx, principal, current, requestID, ipAddress)
	}
	if err := validateDirectChangeReason(directChangeReason); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin asset delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize asset retirement with remediation assignment and review writes.
	// Without the shared lock, an assignment that starts while this delete is in
	// flight could create a task after the retirement UPDATE has already taken its
	// snapshot, leaving a live task on a deleted target.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return fmt.Errorf("lock remediation work during asset delete: %w", err)
	}
	command, err := tx.Exec(ctx, `
		UPDATE assets SET deleted_at = now(), updated_at = now(), updated_by = $2::uuid
		WHERE id::text = $1 AND deleted_at IS NULL
	`, assetID, principal.UserID)
	if err != nil {
		return fmt.Errorf("delete asset: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Retire the remediation work that hangs on this asset instead of cancelling
	// it. The delivery history has to survive an audit, but the assignee must not
	// keep working a task whose target no longer exists, and the reviewer must be
	// able to see why the task stopped being current.
	retiredTaskIDs, err := remediationretire.Retire(ctx, tx, principal.UserID, remediationretire.AssetColumn,
		"finding_id IN (SELECT id FROM security_findings WHERE asset_id = $1::uuid)", assetID,
		"asset_retired", map[string]any{"asset_id": assetID, "reason": "关联资产已删除，整改任务待改派"})
	if err != nil {
		return err
	}
	if len(retiredTaskIDs) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
			VALUES ($1::uuid, $2::uuid, 'asset.delete', 'asset', $3, '删除网络资产导致整改任务待改派',
			        jsonb_build_object('retired_remediation_tasks', $4::int), NULLIF($5, '')::inet, NULLIF($6, ''))
		`, principal.UserID, current.OrganizationID, assetID, len(retiredTaskIDs), ipAddress, requestID); err != nil {
			return fmt.Errorf("write asset delete remediation audit: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.delete', 'asset', $3, '删除网络资产',
		        jsonb_build_object('type', $4::text, 'value', $5::text, 'direct_change_reason', NULLIF($6, '')), NULLIF($7, '')::inet, NULLIF($8, ''))
	`, principal.UserID, current.OrganizationID, assetID, current.Type, current.Value, directChangeReason, ipAddress, requestID); err != nil {
		return fmt.Errorf("write asset delete audit: %w", err)
	}
	if err := s.writeDirectChangeRecheck(ctx, tx, principal, DirectChangeDelete, assetID, current.OrganizationID, directChangeReason,
		map[string]any{"before": current, "deleted": true}, nil, requestID, ipAddress); err != nil {
		return err
	}
	resourceLabel := strings.TrimSpace(current.Name)
	if resourceLabel == "" {
		resourceLabel = current.Value
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetDeleted, Category: notification.CategoryAssetChange,
		TargetOrganizationID: current.OrganizationID, TargetUserID: current.OwnerUserID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: assetID, ResourceLabel: resourceLabel, BatchKey: requestID, Level: "danger",
		Details: map[string]any{"asset_type": current.Type, "asset_value": current.Value},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit asset delete: %w", err)
	}
	return nil
}

func (s *Service) ListImports(ctx context.Context, principal auth.Principal) ([]ImportHistory, error) {
	result, err := s.SearchImports(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) SearchImports(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[ImportHistory], error) {
	if !principal.HasPermission("asset.manage") {
		return pagination.Result[ImportHistory]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT i.id::text,i.file_name,i.file_type,i.import_mode,i.status,i.total_count,i.success_count,i.created_count,i.updated_count,i.failure_count,i.duplicate_count,i.errors::text,
		       o.id::text,o.organization_code,o.organization_name,COALESCE(u.display_name,''),i.created_at,count(*) OVER ()
		FROM asset_imports i JOIN organizations o ON o.id=i.organization_id LEFT JOIN users u ON u.id=i.created_by
		WHERE $2::boolean OR o.id IN (SELECT id FROM accessible) ORDER BY i.created_at DESC LIMIT $3 OFFSET $4`, principal.UserID, principal.HasRole("group_admin"), page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[ImportHistory]{}, fmt.Errorf("search imports: %w", err)
	}
	defer rows.Close()
	items := make([]ImportHistory, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item ImportHistory
		var errorsText string
		if err := rows.Scan(&item.ID, &item.FileName, &item.FileType, &item.ImportMode, &item.Status, &item.TotalCount, &item.SuccessCount, &item.CreatedCount, &item.UpdatedCount, &item.FailureCount, &item.DuplicateCount, &errorsText, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.CreatedByName, &item.CreatedAt, &total); err != nil {
			return pagination.Result[ImportHistory]{}, fmt.Errorf("scan import: %w", err)
		}
		item.Errors = make([]ImportError, 0)
		if err := json.Unmarshal([]byte(errorsText), &item.Errors); err != nil {
			return pagination.Result[ImportHistory]{}, fmt.Errorf("decode import errors: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[ImportHistory]{}, fmt.Errorf("iterate imports: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `
            WITH RECURSIVE descendant_roots(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
                JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
                WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
            WHERE child.status='enabled' AND child.deleted_at IS NULL
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
                JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
                WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*) FROM asset_imports i JOIN organizations o ON o.id=i.organization_id
            WHERE $2::boolean OR o.id IN (SELECT id FROM accessible)
        `, principal.UserID, principal.HasRole("group_admin")).Scan(&total); err != nil {
			return pagination.Result[ImportHistory]{}, fmt.Errorf("count imports: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) authorizeAsset(ctx context.Context, principal auth.Principal, assetID string) (string, error) {
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return "", ErrInvalid
	}
	var organizationID string
	if err := s.pool.QueryRow(ctx, `
		SELECT ao.organization_id::text
		FROM assets a JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		WHERE a.id::text = $1 AND a.deleted_at IS NULL
	`, assetID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	} else if err != nil {
		return "", fmt.Errorf("load asset authorization: %w", err)
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrForbidden
	}
	return organizationID, nil
}

func validateNetworkArea(ctx context.Context, tx pgx.Tx, networkAreaID, organizationID string, allowDisabled bool) (string, error) {
	networkAreaID = strings.TrimSpace(networkAreaID)
	if networkAreaID == "" {
		return "", nil
	}
	var areaOrganizationID, areaName, status string
	err := tx.QueryRow(ctx, `
		SELECT organization_id::text, area_name, status
		FROM asset_network_areas
		WHERE id::text = $1
	`, networkAreaID).Scan(&areaOrganizationID, &areaName, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load asset network area: %w", err)
	}
	if areaOrganizationID != organizationID || (status != "enabled" && !allowDisabled) {
		return "", ErrInvalid
	}
	return areaName, nil
}

func validateOwnerUser(ctx context.Context, tx pgx.Tx, ownerUserID, organizationID string) (string, string, error) {
	ownerUserID = strings.TrimSpace(ownerUserID)
	if ownerUserID == "" {
		return "", "", nil
	}
	var username, displayName string
	err := tx.QueryRow(ctx, `
		SELECT u.username, u.display_name
		FROM users u
		JOIN user_organizations uo ON uo.user_id = u.id
		WHERE u.id::text = $1
		  AND uo.organization_id = $2::uuid
		  AND u.status = 'enabled'
		  AND u.deleted_at IS NULL
	`, ownerUserID, organizationID).Scan(&username, &displayName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrInvalid
	}
	if err != nil {
		return "", "", fmt.Errorf("validate asset owner user: %w", err)
	}
	return username, displayName, nil
}

func queryAsset(ctx context.Context, querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, assetID string) (Asset, error) {
	var item Asset
	err := querier.QueryRow(ctx, `
		SELECT a.id::text, a.asset_type, a.asset_value, a.normalized_value, COALESCE(a.asset_name, ''),
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       a.environment, a.network_scope, COALESCE(na.id::text, ''), COALESCE(na.area_name, ''),
		       a.status, a.importance, a.source,
		       COALESCE(ao.owner_user_id::text, ''), COALESCE(owner_user.username, ''), COALESCE(owner_user.display_name, ''),
		       COALESCE(ao.owner_name, ''), COALESCE(a.notes, ''), a.expires_at, a.created_at, a.updated_at
		FROM assets a
		LEFT JOIN asset_network_areas na ON na.id = a.network_area_id
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		LEFT JOIN users owner_user ON owner_user.id = ao.owner_user_id
		JOIN organizations o ON o.id = ao.organization_id
		LEFT JOIN LATERAL (
			SELECT system.id, system.system_code, system.system_name
			FROM system_assets sa JOIN business_systems system ON system.id = sa.business_system_id
			WHERE sa.asset_id = a.id AND sa.is_primary AND system.deleted_at IS NULL
			ORDER BY sa.created_at LIMIT 1
		) bs ON true
		WHERE a.id::text = $1 AND a.deleted_at IS NULL
	`, assetID).Scan(&item.ID, &item.Type, &item.Value, &item.NormalizedValue, &item.Name,
		&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
		&item.BusinessSystemID, &item.BusinessSystemCode, &item.BusinessSystemName,
		&item.Environment, &item.NetworkScope, &item.NetworkAreaID, &item.NetworkAreaName,
		&item.Status, &item.Importance, &item.Source, &item.OwnerUserID, &item.OwnerUsername, &item.OwnerUserDisplayName,
		&item.OwnerName, &item.Notes, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("load asset: %w", err)
	}
	return item, nil
}

func Template(format string) ([]byte, string, string, error) {
	return buildTemplate(format, nil)
}

// Template builds an organization-scoped import template containing every
// currently enabled custom field that the caller may maintain.
func (s *Service) Template(ctx context.Context, principal auth.Principal, organizationID, format string) ([]byte, string, string, error) {
	definitions, err := customfield.NewService(s.pool).ActiveDefinitionsForOrganization(ctx, principal, "asset", organizationID)
	if err != nil {
		return nil, "", "", err
	}
	return buildTemplate(format, definitions)
}

func buildTemplate(format string, definitions []customfield.Definition) ([]byte, string, string, error) {
	headers := []string{"资产类型", "资产值", "资产名称", "业务系统编码", "环境", "网络暴露范围", "网络区域", "重要程度", "责任人账号", "负责人", "有效期至", "备注"}
	example := []string{"domain", "portal.example.com", "门户网站域名", "", "production", "public", "上海办事处", "important", "zhangsan", "张三/信息化团队", "2027-12-31", "责任人账号必须是所属组织内已启用用户；网络区域按所属组织下已配置的名称填写；有效期支持 YYYY-MM-DD、YYYY-MM-DD HH:mm:ss 或 RFC3339；无时区值按 UTC+8 解释"}
	for _, definition := range definitions {
		headers = append(headers, customfield.ImportHeader(definition))
		example = append(example, customFieldTemplateExample(definition))
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "csv":
		var buffer bytes.Buffer
		buffer.WriteString("\xEF\xBB\xBF")
		writer := csv.NewWriter(&buffer)
		if err := writer.Write(headers); err != nil {
			return nil, "", "", err
		}
		if err := writer.Write(example); err != nil {
			return nil, "", "", err
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "text/csv; charset=utf-8", "网络资产导入模板.csv", nil
	case "xlsx", "":
		file := excelize.NewFile()
		defer file.Close()
		sheet := file.GetSheetName(0)
		for index, value := range headers {
			cell, _ := excelize.CoordinatesToCellName(index+1, 1)
			_ = file.SetCellValue(sheet, cell, value)
		}
		for index, value := range example {
			cell, _ := excelize.CoordinatesToCellName(index+1, 2)
			_ = file.SetCellValue(sheet, cell, value)
		}
		_ = file.SetColWidth(sheet, "A", "A", 14)
		_ = file.SetColWidth(sheet, "B", "C", 26)
		_ = file.SetColWidth(sheet, "D", "Z", 18)
		if len(definitions) > 0 {
			_, err := file.NewSheet("自定义字段说明")
			if err != nil {
				return nil, "", "", err
			}
			_ = file.SetSheetRow("自定义字段说明", "A1", &[]any{"导入列", "字段名称", "类型", "是否必填", "说明 / 可选项"})
			for index, definition := range definitions {
				row := index + 2
				_ = file.SetSheetRow("自定义字段说明", fmt.Sprintf("A%d", row), &[]any{customfield.ImportHeader(definition), definition.FieldName, definition.FieldType, map[bool]string{true: "是", false: "否"}[definition.IsRequired], customFieldTemplateHint(definition)})
			}
			_ = file.SetColWidth("自定义字段说明", "A", "A", 28)
			_ = file.SetColWidth("自定义字段说明", "B", "E", 24)
		}
		var buffer bytes.Buffer
		if err := file.Write(&buffer); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "网络资产导入模板.xlsx", nil
	default:
		return nil, "", "", ErrInvalid
	}
}

func customFieldTemplateExample(definition customfield.Definition) string {
	switch definition.FieldType {
	case "number":
		return "100"
	case "date":
		return "2026-01-01"
	case "boolean":
		return "是"
	case "select":
		if len(definition.Options) > 0 {
			return definition.Options[0]
		}
	case "multiselect":
		if len(definition.Options) > 0 {
			return definition.Options[0]
		}
	}
	return ""
}
func customFieldTemplateHint(definition customfield.Definition) string {
	if definition.FieldType == "select" || definition.FieldType == "multiselect" {
		return "可选项：" + strings.Join(definition.Options, "、")
	}
	if definition.FieldType == "boolean" {
		return "填写 是/否（也支持 true/false、1/0）"
	}
	if definition.FieldType == "date" {
		return "格式：YYYY-MM-DD"
	}
	return definition.Description
}

type importUpdateTarget struct {
	ID, Kind, DraftStatus, TaskID, Operation string
}

func normalizeImportMode(principal auth.Principal, mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = ImportModeSkipDuplicates
	}
	if mode != ImportModeSkipDuplicates && mode != ImportModeUpdateWorking {
		return "", ErrInvalid
	}
	if mode == ImportModeUpdateWorking && !usesWorkingInventory(principal) {
		return "", ErrInvalid
	}
	return mode, nil
}

func (s *Service) findImportUpdateTarget(ctx context.Context, organizationID, assetType, normalizedValue string) (importUpdateTarget, bool, error) {
	var target importUpdateTarget
	err := s.pool.QueryRow(ctx, `SELECT id::text,'draft',draft_status,COALESCE(task_id::text,''),operation FROM inventory_asset_drafts WHERE organization_id=$1::uuid AND asset_type=$2 AND normalized_value=$3 AND draft_status IN`+inventorydraft.ActiveSQL+` ORDER BY updated_at DESC LIMIT 1`, organizationID, assetType, normalizedValue).Scan(&target.ID, &target.Kind, &target.DraftStatus, &target.TaskID, &target.Operation)
	if err == nil {
		return target, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return target, false, err
	}
	var formalID string
	err = s.pool.QueryRow(ctx, `SELECT a.id::text FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary=true AND ao.relationship_type='owner' WHERE ao.organization_id=$1::uuid AND a.asset_type=$2 AND a.normalized_value=$3 AND a.deleted_at IS NULL LIMIT 1`, organizationID, assetType, normalizedValue).Scan(&formalID)
	if err == nil {
		err = s.pool.QueryRow(ctx, `SELECT id::text,'draft',draft_status,COALESCE(task_id::text,''),operation FROM inventory_asset_drafts WHERE source_asset_id=$1::uuid AND draft_status IN`+inventorydraft.ActiveSQL+` ORDER BY updated_at DESC LIMIT 1`, formalID).Scan(&target.ID, &target.Kind, &target.DraftStatus, &target.TaskID, &target.Operation)
		if err == nil {
			return target, true, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return target, false, err
		}
		return importUpdateTarget{ID: formalID, Kind: "formal"}, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return target, false, err
	}
	var duplicate bool
	err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM inventory_asset_drafts WHERE asset_type=$1 AND normalized_value=$2 AND draft_status IN`+inventorydraft.ActiveSQL+`)`, assetType, normalizedValue).Scan(&duplicate)
	return target, duplicate, err
}

func importTargetMutable(target importUpdateTarget) bool {
	return target.Kind != "draft" || (target.TaskID == "" && target.DraftStatus != "submitted" && target.Operation != "delete")
}

func assetImportFields(definitions []customfield.Definition) []importmapping.Field {
	fields := []importmapping.Field{
		{Key: "type", Name: "资产类型", Required: true, Aliases: []string{"资产类型", "type"}},
		{Key: "value", Name: "资产值", Required: true, Aliases: []string{"资产值", "value"}},
		{Key: "name", Name: "资产名称", Aliases: []string{"资产名称", "name"}},
		{Key: "business_system_code", Name: "业务系统编码", Aliases: []string{"业务系统编码", "business_system_code"}},
		{Key: "environment", Name: "环境", Aliases: []string{"环境", "environment"}},
		{Key: "network_scope", Name: "网络范围", Aliases: []string{"网络范围", "网络暴露范围", "network_scope"}},
		{Key: "network_area", Name: "网络区域", Aliases: []string{"网络区域", "网络归属", "network_area"}},
		{Key: "importance", Name: "重要程度", Aliases: []string{"重要程度", "importance"}},
		{Key: "owner_username", Name: "责任人账号", Aliases: []string{"责任人账号", "负责人账号", "owner_username"}},
		{Key: "owner_name", Name: "负责人", Aliases: []string{"负责人", "owner_name"}},
		{Key: "expires_at", Name: "有效期至", Aliases: []string{"有效期至", "到期时间", "expires_at"}},
		{Key: "notes", Name: "备注", Aliases: []string{"备注", "notes"}},
	}
	for _, definition := range definitions {
		key := customfield.ImportHeaderPrefix + definition.FieldKey
		fields = append(fields, importmapping.Field{
			Key: key, Name: definition.FieldName, Required: definition.IsRequired, Custom: true,
			Aliases: []string{key, definition.FieldKey, definition.FieldName},
		})
	}
	return fields
}

func resolveAssetImportHeaders(headers []string, mapping ColumnMapping, definitions []customfield.Definition) (map[string]int, []string, error) {
	return importmapping.Resolve(headers, mapping, assetImportFields(definitions))
}

// AnalyzeImportColumns reads an import file without writing data and returns
// source-column samples plus automatic mapping suggestions.
func (s *Service) AnalyzeImportColumns(ctx context.Context, principal auth.Principal, organizationID, fileName string, reader io.Reader) (ImportColumnAnalysis, error) {
	if !principal.HasPermission("asset.manage") {
		return ImportColumnAnalysis{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
	if err != nil {
		return ImportColumnAnalysis{}, err
	}
	if !allowed {
		return ImportColumnAnalysis{}, ErrForbidden
	}
	rows, _, err := readImportRows(fileName, reader)
	if err != nil || len(rows) == 0 || len(rows)-1 > MaxImportRows {
		return ImportColumnAnalysis{}, ErrInvalid
	}
	definitions, err := customfield.NewService(s.pool).ActiveDefinitionsForOrganization(ctx, principal, "asset", organizationID)
	if err != nil {
		return ImportColumnAnalysis{}, err
	}
	analysis, err := importmapping.Analyze(dropAssetTemplateExampleRows(rows, definitions), assetImportFields(definitions))
	if err != nil {
		return ImportColumnAnalysis{}, ErrInvalid
	}
	return analysis, nil
}

// PreviewImport validates an asset import without writing ledger rows, import history, or audit records.
func (s *Service) PreviewImport(ctx context.Context, principal auth.Principal, organizationID, fileName string, reader io.Reader, rawMode string, mapping ColumnMapping) (ImportResult, error) {
	if !principal.HasPermission("asset.manage") {
		return ImportResult{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
	if err != nil {
		return ImportResult{}, err
	}
	if !allowed {
		return ImportResult{}, ErrForbidden
	}

	if err != nil {
		return ImportResult{}, err
	}
	mode, err := normalizeImportMode(principal, rawMode)
	if err != nil {
		return ImportResult{}, err
	}
	rows, fileType, err := readImportRows(fileName, reader)
	if err != nil || len(rows) < 2 || len(rows)-1 > MaxImportRows {
		return ImportResult{}, ErrInvalid
	}
	customService := customfield.NewService(s.pool)
	definitions, err := customService.ActiveDefinitionsForOrganization(ctx, principal, "asset", organizationID)
	if err != nil {
		return ImportResult{}, err
	}
	headers, mappedHeaders, err := resolveAssetImportHeaders(rows[0], mapping, definitions)
	if err != nil {
		return ImportResult{}, ErrInvalid
	}

	result := ImportResult{FileName: filepath.Base(fileName), FileType: fileType, ImportMode: mode, Errors: make([]ImportError, 0), TotalCount: countAssetImportDataRows(rows, headers)}
	seen := make(map[string]struct{}, result.TotalCount)
	for index, row := range rows[1:] {
		rowNumber := index + 2
		if isAssetTemplateExample(row, headers) {
			continue
		}
		input, inputErr := importInput(row, headers, organizationID)
		if inputErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid_expiry", Message: "有效期格式不正确，请使用 YYYY-MM-DD、YYYY-MM-DD HH:mm:ss 或 RFC3339"})
			continue
		}
		if _, customErr := customfield.ImportValues(definitions, mappedHeaders, row); customErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid_custom_field", Message: customErr.Error()})
			continue
		}
		if input.BusinessSystemID != "" {
			systemID, err := s.resolveImportBusinessSystem(ctx, organizationID, input.BusinessSystemID, usesWorkingInventory(principal))
			if errors.Is(err, ErrNotFound) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "system_not_found", Message: "业务系统编码不存在或不属于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, fmt.Errorf("resolve preview business system: %w", err)
			}
			input.BusinessSystemID = systemID
		}
		if input.NetworkAreaID != "" {
			var networkAreaID string
			err := s.pool.QueryRow(ctx, `
				SELECT id::text FROM asset_network_areas
				WHERE organization_id = $1::uuid AND lower(area_name) = lower($2) AND status = 'enabled'
			`, organizationID, input.NetworkAreaID).Scan(&networkAreaID)
			if errors.Is(err, pgx.ErrNoRows) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "network_area_not_found", Message: "网络区域不存在、未启用或不属于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, fmt.Errorf("resolve preview network area: %w", err)
			}
			input.NetworkAreaID = networkAreaID
		}
		if input.OwnerUserID != "" {
			ownerUserID, err := s.resolveImportOwnerUser(ctx, organizationID, input.OwnerUserID)
			if errors.Is(err, ErrNotFound) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "owner_user_not_found", Message: "责任人账号不存在、已停用或未任职于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, err
			}
			input.OwnerUserID = ownerUserID
		}

		normalizeInput(&input)
		normalizedValue, normalizeErr := normalizeValue(input.Type, input.Value)
		if normalizeErr != nil || !validInput(input) {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid", Message: "资产字段格式不正确"})
			continue
		}
		key := input.Type + "\x00" + normalizedValue
		_, duplicateInFile := seen[key]
		if duplicateInFile {
			result.DuplicateCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "duplicate_in_file", Message: "文件内资产类型和值重复"})
			continue
		}
		seen[key] = struct{}{}
		if mode == ImportModeUpdateWorking {
			target, duplicate, err := s.findImportUpdateTarget(ctx, organizationID, input.Type, normalizedValue)
			if err != nil {
				return ImportResult{}, fmt.Errorf("resolve preview asset import target: %w", err)
			}
			if target.ID != "" {
				if !importTargetMutable(target) {
					result.FailureCount++
					result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "working_version_locked", Message: "已有工作版本已纳入任务或提交审核，暂不能覆盖"})
					continue
				}
				result.SuccessCount++
				result.UpdatedCount++
				continue
			}
			if duplicate {
				result.DuplicateCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "duplicate", Message: "资产已存在于其他组织，不能覆盖"})
				continue
			}
			result.SuccessCount++
			result.CreatedCount++
			continue
		}
		var duplicateInLedger bool
		{
			if err := s.pool.QueryRow(ctx, `SELECT EXISTS (
				SELECT 1 FROM assets WHERE asset_type = $1 AND normalized_value = $2 AND deleted_at IS NULL
			) OR EXISTS (
				SELECT 1 FROM inventory_asset_drafts WHERE asset_type = $1 AND normalized_value = $2 AND draft_status IN `+inventorydraft.ActiveSQL+`
			)`, input.Type, normalizedValue).Scan(&duplicateInLedger); err != nil {
				return ImportResult{}, fmt.Errorf("check preview asset duplicate: %w", err)
			}
		}
		if duplicateInLedger {
			result.DuplicateCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "duplicate", Message: "资产类型和值已存在"})
			continue
		}
		result.SuccessCount++
		result.CreatedCount++
	}
	result.Status = "ready"
	if result.SuccessCount == 0 {
		result.Status = "failed"
	} else if result.FailureCount+result.DuplicateCount > 0 {
		result.Status = "partial"
	}
	return result, nil
}

// Import writes formal ledger rows in bulk. For a group administrator that is
// still a direct write, so it takes the same mandatory reason as Create/Update
// and raises the same recheck items instead of becoming an unaudited side door.
func (s *Service) Import(ctx context.Context, principal auth.Principal, organizationID, fileName string, reader io.Reader, rawMode, directChangeReason, requestID, ipAddress string, mapping ColumnMapping) (ImportResult, error) {
	if !principal.HasPermission("asset.manage") {
		return ImportResult{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "asset.manage")
	if err != nil {
		return ImportResult{}, err
	}
	if !allowed {
		return ImportResult{}, ErrForbidden
	}
	if !usesWorkingInventory(principal) {
		if err := validateDirectChangeReason(directChangeReason); err != nil {
			return ImportResult{}, err
		}
	}
	mode, err := normalizeImportMode(principal, rawMode)
	if err != nil {
		return ImportResult{}, err
	}
	rows, fileType, err := readImportRows(fileName, reader)
	if err != nil {
		return ImportResult{}, ErrInvalid
	}
	if len(rows) < 2 || len(rows)-1 > MaxImportRows {
		return ImportResult{}, ErrInvalid
	}
	customService := customfield.NewService(s.pool)
	definitions, err := customService.ActiveDefinitionsForOrganization(ctx, principal, "asset", organizationID)
	if err != nil {
		return ImportResult{}, err
	}
	headers, mappedHeaders, err := resolveAssetImportHeaders(rows[0], mapping, definitions)
	if err != nil {
		return ImportResult{}, ErrInvalid
	}

	result := ImportResult{FileName: filepath.Base(fileName), FileType: fileType, ImportMode: mode, Errors: make([]ImportError, 0), TotalCount: countAssetImportDataRows(rows, headers)}
	seen := make(map[string]struct{}, result.TotalCount)
	for index, row := range rows[1:] {
		rowNumber := index + 2
		if isAssetTemplateExample(row, headers) {
			continue
		}
		input, inputErr := importInput(row, headers, organizationID)
		if inputErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid_expiry", Message: "有效期格式不正确，请使用 YYYY-MM-DD、YYYY-MM-DD HH:mm:ss 或 RFC3339"})
			continue
		}
		customValues, customErr := customfield.ImportValues(definitions, mappedHeaders, row)
		if customErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid_custom_field", Message: customErr.Error()})
			continue
		}
		if input.BusinessSystemID != "" {
			systemID, err := s.resolveImportBusinessSystem(ctx, organizationID, input.BusinessSystemID, usesWorkingInventory(principal))
			if errors.Is(err, ErrNotFound) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "system_not_found", Message: "业务系统编码不存在或不属于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, fmt.Errorf("resolve import business system: %w", err)
			}
			input.BusinessSystemID = systemID
		}
		if input.NetworkAreaID != "" {
			var networkAreaID string
			err := s.pool.QueryRow(ctx, `
				SELECT id::text FROM asset_network_areas
				WHERE organization_id = $1::uuid AND lower(area_name) = lower($2) AND status = 'enabled'
			`, organizationID, input.NetworkAreaID).Scan(&networkAreaID)
			if errors.Is(err, pgx.ErrNoRows) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "network_area_not_found", Message: "网络区域不存在、未启用或不属于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, fmt.Errorf("resolve import network area: %w", err)
			}
			input.NetworkAreaID = networkAreaID
		}
		if input.OwnerUserID != "" {
			ownerUserID, err := s.resolveImportOwnerUser(ctx, organizationID, input.OwnerUserID)
			if errors.Is(err, ErrNotFound) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "owner_user_not_found", Message: "责任人账号不存在、已停用或未任职于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, err
			}
			input.OwnerUserID = ownerUserID
		}
		input.CustomValues = customValues
		normalized, prepareErr := PrepareCreateInput(&input)
		if prepareErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid", Message: "资产字段格式不正确"})
			continue
		}
		key := input.Type + "\x00" + normalized
		if _, exists := seen[key]; exists {
			result.DuplicateCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "duplicate_in_file", Message: "文件内资产类型和值重复"})
			continue
		}
		seen[key] = struct{}{}
		var operation string
		var imported Asset
		if mode == ImportModeUpdateWorking {
			target, duplicate, targetErr := s.findImportUpdateTarget(ctx, organizationID, input.Type, normalized)
			if targetErr != nil {
				return ImportResult{}, targetErr
			}
			if target.ID != "" && !importTargetMutable(target) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "working_version_locked", Message: "已有工作版本已纳入任务或提交审核，暂不能覆盖"})
				continue
			}
			if target.ID != "" {
				input.DirectChangeReason = directChangeReason
				imported, err = s.Update(ctx, principal, target.ID, input, requestID, ipAddress)
				operation = "update"
			} else if duplicate {
				err = ErrConflict
			} else {
				input.DirectChangeReason = directChangeReason
				imported, err = s.createImported(ctx, principal, input, definitions, customValues, requestID, ipAddress)
				operation = "create"
			}
		} else {
			input.DirectChangeReason = directChangeReason
			imported, err = s.createImported(ctx, principal, input, definitions, customValues, requestID, ipAddress)
			operation = "create"
		}
		_ = imported
		switch {
		case err == nil:
			result.SuccessCount++
			if operation == "update" {
				result.UpdatedCount++
			} else {
				result.CreatedCount++
			}
		case errors.Is(err, ErrConflict):
			result.DuplicateCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "duplicate", Message: "资产类型和值已存在"})
		case errors.Is(err, ErrNotFound):
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "reference_not_found", Message: "所属组织或业务系统不存在"})
		case errors.Is(err, ErrForbidden):
			return ImportResult{}, ErrForbidden
		case errors.Is(err, ErrInvalid):
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid", Message: "资产字段格式不正确"})
		default:
			return ImportResult{}, err
		}
	}
	result.Status = "completed"
	if result.SuccessCount == 0 {
		result.Status = "failed"
	} else if result.FailureCount+result.DuplicateCount > 0 {
		result.Status = "partial"
	}
	importID, err := s.saveImportResult(ctx, principal, organizationID, result, requestID, ipAddress)
	if err != nil {
		return ImportResult{}, err
	}
	result.ID = importID
	return result, nil
}

func (s *Service) createImported(ctx context.Context, principal auth.Principal, input CreateInput, definitions []customfield.Definition, customValues []customfield.Value, requestID, ipAddress string) (Asset, error) {
	if usesWorkingInventory(principal) {
		input.CustomValues = customValues
		return s.createUnreportedWithSource(ctx, principal, input, "import", requestID, ipAddress)
	}

	// Working-version reservations are checked inside create, which every formal
	// asset write shares.
	return s.create(ctx, principal, input, "import", requestID, ipAddress, func(tx pgx.Tx, item Asset) error {
		return customfield.SaveImportedValuesInTx(ctx, tx, "asset", item.ID, definitions, customValues, principal.UserID)
	})
}

func (s *Service) resolveImportBusinessSystem(ctx context.Context, organizationID, systemCode string, includeWorking bool) (string, error) {
	systemCode = strings.TrimSpace(systemCode)
	if includeWorking {
		var draftID string
		err := s.pool.QueryRow(ctx, `
			SELECT id::text
			FROM inventory_business_system_drafts
			WHERE lower(system_code)=lower($1) AND organization_id=$2::uuid
			  AND operation<>'delete' AND draft_status IN `+inventorydraft.ActiveSQL+`
			ORDER BY CASE WHEN source_system_id IS NULL THEN 0 ELSE 1 END, updated_at DESC
			LIMIT 1
		`, systemCode, organizationID).Scan(&draftID)
		if err == nil {
			return draftID, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
	}

	var systemID string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM business_systems
		WHERE lower(system_code)=lower($1) AND owner_organization_id=$2::uuid AND deleted_at IS NULL
	`, systemCode, organizationID).Scan(&systemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return systemID, err
}

func (s *Service) saveImportResult(ctx context.Context, principal auth.Principal, organizationID string, result ImportResult, requestID, ipAddress string) (string, error) {
	errorsJSON, err := json.Marshal(result.Errors)
	if err != nil {
		return "", fmt.Errorf("encode import errors: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin import result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var importID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO asset_imports (organization_id, file_name, file_type, import_mode, status, total_count, success_count,
		                           created_count, updated_count, failure_count, duplicate_count, errors, created_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12::jsonb, $13::uuid)
		RETURNING id::text
	`, organizationID, result.FileName, result.FileType, result.ImportMode, result.Status, result.TotalCount, result.SuccessCount,
		result.CreatedCount, result.UpdatedCount, result.FailureCount, result.DuplicateCount, string(errorsJSON), principal.UserID).Scan(&importID); err != nil {
		return "", fmt.Errorf("save import result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.import', 'asset_import', $3, '批量导入网络资产',
		        jsonb_build_object('file_name', $4::text, 'import_mode', $5::text, 'total', $6::int,
		                           'success', $7::int, 'created', $8::int, 'updated', $9::int,
		                           'failure', $10::int, 'duplicate', $11::int), NULLIF($12, '')::inet, NULLIF($13, ''))
	`, principal.UserID, organizationID, importID, result.FileName, result.ImportMode, result.TotalCount, result.SuccessCount,
		result.CreatedCount, result.UpdatedCount, result.FailureCount, result.DuplicateCount, ipAddress, requestID); err != nil {
		return "", fmt.Errorf("write import audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit import result: %w", err)
	}
	return importID, nil
}

func (s *Service) resolveImportOwnerUser(ctx context.Context, organizationID, username string) (string, error) {
	var userID string
	err := s.pool.QueryRow(ctx, `
		SELECT u.id::text
		FROM users u
		JOIN user_organizations uo ON uo.user_id = u.id
		WHERE lower(u.username) = lower($1)
		  AND uo.organization_id = $2::uuid
		  AND u.status = 'enabled'
		  AND u.deleted_at IS NULL
	`, strings.TrimSpace(username), organizationID).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("resolve import asset owner user: %w", err)
	}
	return userID, nil
}

func assetFieldChanges(current Asset, input CreateInput, organizationName, systemName, networkAreaName, ownerUsername, ownerDisplayName string) []fieldChange {
	changes := make([]fieldChange, 0, 12)
	add := func(field, label, before, after string) {
		if before != after {
			changes = append(changes, fieldChange{Field: field, Label: label, Before: before, After: after})
		}
	}
	add("asset_type", "资产类型", assetTypeLabel(current.Type), assetTypeLabel(input.Type))
	add("asset_value", "资产值", displayValue(current.Value, "未填写"), displayValue(input.Value, "未填写"))
	add("asset_name", "资产名称", displayValue(current.Name, "未填写"), displayValue(input.Name, "未填写"))
	add("organization", "所属组织", displayValue(current.OrganizationName, "未关联"), displayValue(organizationName, "未关联"))
	add("business_system", "关联业务系统", displayValue(current.BusinessSystemName, "未关联"), displayValue(systemName, "未关联"))
	add("owner_user_id", "责任人账号", ownerUserLabel(current.OwnerUsername, current.OwnerUserDisplayName), ownerUserLabel(ownerUsername, ownerDisplayName))
	add("owner_name", "负责人", displayValue(current.OwnerName, "未填写"), displayValue(input.OwnerName, "未填写"))
	add("environment", "部署环境", assetEnvironmentLabel(current.Environment), assetEnvironmentLabel(input.Environment))
	add("network_scope", "网络暴露范围", assetNetworkScopeLabel(current.NetworkScope), assetNetworkScopeLabel(input.NetworkScope))
	add("network_area", "网络区域", displayValue(current.NetworkAreaName, "未设置"), displayValue(networkAreaName, "未设置"))
	add("status", "资产状态", assetStatusLabel(current.Status), assetStatusLabel(input.Status))
	add("importance", "重要程度", assetImportanceLabel(current.Importance), assetImportanceLabel(input.Importance))
	add("expires_at", "有效期至", formatOptionalDisplayTime(current.ExpiresAt), formatOptionalDisplayTime(input.ExpiresAt))
	add("notes", "备注", displayValue(current.Notes, "未填写"), displayValue(input.Notes, "未填写"))
	return changes
}

func ownerUserLabel(username, displayName string) string {
	username = strings.TrimSpace(username)
	displayName = strings.TrimSpace(displayName)
	if username == "" {
		return "未绑定"
	}
	if displayName == "" || displayName == username {
		return username
	}
	return displayName + "（" + username + "）"
}

func formatOptionalDisplayTime(value *time.Time) string {
	if value == nil {
		return "长期有效"
	}
	return value.Format("2006-01-02 15:04:05Z07:00")
}

func displayValue(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func assetTypeLabel(value string) string {
	return map[string]string{"domain": "域名", "ip": "IP", "url": "URL", "service": "服务", "other": "其他"}[value]
}

func assetEnvironmentLabel(value string) string {
	if label := map[string]string{"production": "生产", "staging": "预发布", "testing": "测试", "development": "开发", "unknown": "待确认"}[value]; label != "" {
		return label
	}
	return displayValue(value, "待确认")
}

func assetNetworkScopeLabel(value string) string {
	if label := map[string]string{"public": "公网", "private": "内网", "unknown": "待确认"}[value]; label != "" {
		return label
	}
	return displayValue(value, "待确认")
}

func assetStatusLabel(value string) string {
	if label := map[string]string{"active": "正常", "pending": "待确认", "disabled": "已停用", "archived": "已归档"}[value]; label != "" {
		return label
	}
	return displayValue(value, "待确认")
}

func assetImportanceLabel(value string) string {
	if label := map[string]string{"normal": "一般", "important": "重要", "critical": "关键"}[value]; label != "" {
		return label
	}
	return displayValue(value, "待确认")
}

func normalizeInput(input *CreateInput) {
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Value = strings.TrimSpace(input.Value)
	input.Name = strings.TrimSpace(input.Name)
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.BusinessSystemID = strings.TrimSpace(input.BusinessSystemID)
	input.Environment = defaultValue(strings.ToLower(strings.TrimSpace(input.Environment)), "production")
	input.NetworkScope = defaultValue(strings.ToLower(strings.TrimSpace(input.NetworkScope)), "unknown")
	input.NetworkAreaID = strings.TrimSpace(input.NetworkAreaID)
	input.Status = defaultValue(strings.ToLower(strings.TrimSpace(input.Status)), "active")
	input.Importance = defaultValue(strings.ToLower(strings.TrimSpace(input.Importance)), "normal")
	input.OwnerUserID = strings.TrimSpace(input.OwnerUserID)
	input.OwnerName = strings.TrimSpace(input.OwnerName)
	input.Notes = strings.TrimSpace(input.Notes)
}
func validInput(input CreateInput) bool {
	return input.OrganizationID != "" && oneOf(input.Type, "domain", "ip", "url", "service", "other") && input.Value != "" &&
		oneOf(input.Environment, "production", "staging", "testing", "development", "office", "other") &&
		oneOf(input.NetworkScope, "public", "private", "unknown") && oneOf(input.Status, "active", "pending", "disabled", "archived") &&
		oneOf(input.Importance, "normal", "important", "core")
}

// NormalizeValue converts an asset value to its canonical form using the same rules as the network asset ledger.
func NormalizeValue(assetType, value string) (string, error) {
	return normalizeValue(assetType, value)
}

// SplitHostPort extracts the port from an endpoint value such as
// "192.168.1.10:443". It reports whether one was present, so callers that match
// scan output against the asset register can keep the port as evidence instead
// of discarding it when the address normalises onto an existing asset.
func SplitHostPort(value string) (string, string, bool) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return "", "", false
	}
	portNumber, convErr := strconv.Atoi(port)
	if convErr != nil || portNumber < 1 || portNumber > 65535 {
		return "", "", false
	}
	return host, strconv.Itoa(portNumber), true
}

func normalizeValue(assetType, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch assetType {
	case "domain":
		domain := strings.TrimSuffix(strings.ToLower(value), ".")
		if domain == "" || strings.ContainsAny(domain, " /:@") || !strings.Contains(domain, ".") {
			return "", ErrInvalid
		}
		return domain, nil
	case "ip":
		// Scan exports routinely record an endpoint as host:port. An IP asset is
		// identified by its address alone, so the port is stripped before
		// normalisation: 192.168.1.10:443 and 192.168.1.10 must collapse onto the
		// same asset instead of creating one row per listening port.
		if host, port, err := net.SplitHostPort(value); err == nil {
			portNumber, convErr := strconv.Atoi(port)
			if convErr != nil || portNumber < 1 || portNumber > 65535 {
				return "", ErrInvalid
			}
			value = host
		}
		ip := net.ParseIP(value)
		if ip == nil {
			return "", ErrInvalid
		}
		return ip.String(), nil
	case "url":
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
			return "", ErrInvalid
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Fragment = ""
		return parsed.String(), nil
	case "service":
		host, port, err := net.SplitHostPort(value)
		if err != nil || host == "" {
			return "", ErrInvalid
		}
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", ErrInvalid
		}
		return net.JoinHostPort(strings.ToLower(host), strconv.Itoa(portNumber)), nil
	case "other":
		if value == "" {
			return "", ErrInvalid
		}
		return strings.ToLower(value), nil
	default:
		return "", ErrInvalid
	}
}
func readImportRows(fileName string, reader io.Reader) ([][]string, string, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".csv":
		csvReader := csv.NewReader(reader)
		csvReader.FieldsPerRecord = -1
		csvReader.TrimLeadingSpace = true
		rows, err := csvReader.ReadAll()
		return rows, "csv", err
	case ".xlsx":
		file, err := excelize.OpenReader(reader)
		if err != nil {
			return nil, "", err
		}
		defer file.Close()
		sheets := file.GetSheetList()
		if len(sheets) == 0 {
			return nil, "", ErrInvalid
		}
		rows, err := file.GetRows(sheets[0])
		return rows, "xlsx", err
	default:
		return nil, "", ErrInvalid
	}
}
func mapHeaders(row []string) map[string]int {
	aliases := map[string]string{
		"资产类型": "type", "type": "type", "资产值": "value", "value": "value", "资产名称": "name", "name": "name",
		"业务系统编码": "business_system_code", "business_system_code": "business_system_code", "环境": "environment", "environment": "environment",
		"网络范围": "network_scope", "网络暴露范围": "network_scope", "network_scope": "network_scope", "网络区域": "network_area", "网络归属": "network_area", "network_area": "network_area", "重要程度": "importance", "importance": "importance",
		"责任人账号": "owner_username", "owner_username": "owner_username", "负责人账号": "owner_username",
		"负责人": "owner_name", "owner_name": "owner_name", "有效期至": "expires_at", "到期时间": "expires_at", "expires_at": "expires_at", "备注": "notes", "notes": "notes",
	}
	result := map[string]int{"type": -1, "value": -1, "name": -1, "business_system_code": -1, "environment": -1, "network_scope": -1, "network_area": -1, "importance": -1, "owner_username": -1, "owner_name": -1, "expires_at": -1, "notes": -1}
	for index, header := range row {
		if key, ok := aliases[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")))]; ok {
			result[key] = index
		}
	}
	return result
}
func importInput(row []string, headers map[string]int, organizationID string) (CreateInput, error) {
	get := func(key string) string {
		index := headers[key]
		if index < 0 || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}
	expiresAt, err := parseImportExpiry(get("expires_at"))
	if err != nil {
		return CreateInput{}, err
	}
	return CreateInput{Type: translateAssetType(get("type")), Value: get("value"), Name: get("name"), OrganizationID: organizationID,
		BusinessSystemID: get("business_system_code"), Environment: translateEnvironment(get("environment")), NetworkScope: translateScope(get("network_scope")),
		NetworkAreaID: get("network_area"), Status: "active", Importance: translateImportance(get("importance")), OwnerUserID: get("owner_username"), OwnerName: get("owner_name"), Notes: get("notes"), ExpiresAt: expiresAt}, nil
}

func parseImportExpiry(value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	zone := time.FixedZone("UTC+8", 8*60*60)
	if parsed, err := time.ParseInLocation("2006-01-02 15:04:05", value, zone); err == nil {
		return &parsed, nil
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, zone)
	if err != nil {
		return nil, err
	}
	// Date-only imports represent validity through the end of that UTC+8 calendar day.
	expiresAt := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, zone)
	return &expiresAt, nil
}

func translateAssetType(value string) string {
	values := map[string]string{"域名": "domain", "IP": "ip", "IP地址": "ip", "IP 地址": "ip", "网址": "url", "网络服务": "service", "服务": "service", "其他": "other"}
	if v := values[value]; v != "" {
		return v
	}
	return strings.ToLower(value)
}

func translateEnvironment(value string) string {
	values := map[string]string{"生产": "production", "预发布": "staging", "测试": "testing", "开发": "development", "办公": "office", "其他": "other"}
	if v := values[value]; v != "" {
		return v
	}
	return value
}
func translateScope(value string) string {
	values := map[string]string{"公网": "public", "内网": "private", "未知": "unknown"}
	if v := values[value]; v != "" {
		return v
	}
	return value
}
func translateImportance(value string) string {
	values := map[string]string{"一般": "normal", "重要": "important", "核心": "core"}
	if v := values[value]; v != "" {
		return v
	}
	return value
}
func defaultValue(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

// assetTemplateExampleRow is the sample data row written as the second line of
// every generated network-asset import template.
//
// Users routinely fill the template in without deleting that sample row, and
// the importer used to treat it as ordinary data, so the preview counted it and
// the import wrote "domain / portal.example.com / 门户网站域名" into the real
// asset ledger. Both paths now drop it.
//
// The match is structural rather than positional because explicit column
// mapping decides where each value lives, and it requires the identifying cells
// to be untouched: once a user edits the type, the value or the name the row is
// real data and must be imported rather than silently discarded.
var assetTemplateExampleRow = []string{"domain", "portal.example.com", "门户网站域名"}

// isAssetTemplateExample reports whether one raw row is the shipped sample row
// of the network-asset import template.
func isAssetTemplateExample(row []string, indexes map[string]int) bool {
	pick := func(key string) string {
		index, ok := indexes[key]
		if !ok || index < 0 || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(strings.TrimPrefix(row[index], "\ufeff"))
	}
	return pick("type") == assetTemplateExampleRow[0] &&
		pick("value") == assetTemplateExampleRow[1] &&
		pick("name") == assetTemplateExampleRow[2]
}

// countAssetImportDataRows counts the rows that will really be considered, so
// the preview never promises a record that the import deliberately skips.
func countAssetImportDataRows(rows [][]string, headers map[string]int) int {
	total := 0
	for _, row := range rows[1:] {
		if isAssetTemplateExample(row, headers) {
			continue
		}
		total++
	}
	return total
}

// dropAssetTemplateExampleRows removes the template sample row before column
// analysis, otherwise the mapping dialog shows "portal.example.com" as the
// recognized content of the user's file instead of their actual first record.
func dropAssetTemplateExampleRows(rows [][]string, definitions []customfield.Definition) [][]string {
	if len(rows) < 2 {
		return rows
	}
	headers, _, err := importmapping.Resolve(rows[0], nil, assetImportFields(definitions))
	if err != nil {
		return rows
	}
	kept := make([][]string, 0, len(rows))
	kept = append(kept, rows[0])
	for _, row := range rows[1:] {
		if isAssetTemplateExample(row, headers) {
			continue
		}
		kept = append(kept, row)
	}
	return kept
}
