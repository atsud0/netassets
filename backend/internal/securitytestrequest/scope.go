package securitytestrequest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

// ScopeCandidate is one selectable public asset from the applicant
// organization's merged ledger: either a published asset with no pending
// working copy, or an unreported working version. Internal-network assets are
// excluded for now; the working_asset/report_action split keeps the door open
// for them once group-subsidiary network connectivity exists.
type ScopeCandidate struct {
	ID                 string    `json:"id"`
	Kind               string    `json:"kind"` // working | published
	AssetType          string    `json:"asset_type"`
	AssetValue         string    `json:"asset_value"`
	AssetName          string    `json:"asset_name"`
	OrganizationID     string    `json:"organization_id"`
	OrganizationCode   string    `json:"organization_code"`
	OrganizationName   string    `json:"organization_name"`
	BusinessSystemID   string    `json:"business_system_id,omitempty"`
	BusinessSystemCode string    `json:"business_system_code,omitempty"`
	BusinessSystemName string    `json:"business_system_name,omitempty"`
	NetworkScope       string    `json:"network_scope"`
	NetworkAreaName    string    `json:"network_area_name,omitempty"`
	Environment        string    `json:"environment"`
	Importance         string    `json:"importance"`
	Status             string    `json:"status"`
	OwnerName          string    `json:"owner_name,omitempty"`
	ReportingStatus    string    `json:"reporting_status"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type ScopeCandidateFilter struct {
	Type    string
	Keyword string
	Page    pagination.Query
}

type scopeQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

// SearchScopeCandidates lists the applicant organization's public assets that
// may be included in a request scope.
func (s *Service) SearchScopeCandidates(ctx context.Context, principal auth.Principal, filter ScopeCandidateFilter) (pagination.Result[ScopeCandidate], error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant {
		return pagination.Result[ScopeCandidate]{}, ErrForbidden
	}
	page := filter.Page
	if page.Page == 0 {
		page.Page = pagination.DefaultPage
	}
	if page.PageSize == 0 {
		page.PageSize = pagination.DefaultPageSize
	}
	keyword := strings.TrimSpace(filter.Keyword)
	assetType := strings.TrimSpace(filter.Type)
	if len(keyword) > 128 {
		return pagination.Result[ScopeCandidate]{}, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `
	WITH ledger AS (
		SELECT a.id::text id, 'published'::text kind, a.asset_type, a.asset_value, COALESCE(a.asset_name, '') asset_name,
		       o.id::text organization_id, o.organization_code, o.organization_name,
		       COALESCE(bs.id::text, '') business_system_id, COALESCE(bs.system_code, '') business_system_code, COALESCE(bs.system_name, '') business_system_name,
		       a.network_scope, COALESCE(na.area_name, '') network_area_name, a.environment, a.importance, a.status,
		       COALESCE(ao.owner_name, '') owner_name, 'reported'::text reporting_status, a.updated_at
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
		JOIN organizations o ON o.id=ao.organization_id AND o.id=$1::uuid
		LEFT JOIN asset_network_areas na ON na.id=a.network_area_id
		LEFT JOIN LATERAL (
			SELECT linked.id, linked.system_code, linked.system_name
			FROM system_assets sa JOIN business_systems linked ON linked.id=sa.business_system_id
			WHERE sa.asset_id=a.id AND linked.deleted_at IS NULL
			ORDER BY sa.is_primary DESC, linked.system_name LIMIT 1
		) bs ON true
		WHERE a.deleted_at IS NULL AND a.status <> 'archived' AND a.network_scope='public'
		  AND NOT EXISTS (SELECT 1 FROM inventory_asset_drafts d
		                  WHERE d.source_asset_id=a.id AND d.organization_id=o.id
		                    AND d.draft_status IN ('editing','submitted','returned'))
		UNION ALL
		SELECT d.id::text, 'working'::text, d.asset_type, d.asset_value, COALESCE(d.asset_name, ''),
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(b.id::text, ''), COALESCE(wb.system_code, b.system_code, ''), COALESCE(wb.system_name, b.system_name, ''),
		       d.network_scope, COALESCE(na.area_name, ''), d.environment, d.importance, d.status,
		       COALESCE(d.owner_name, ''),
		       CASE WHEN d.operation='update' THEN 'changed' ELSE 'unreported' END, d.updated_at
		FROM inventory_asset_drafts d
		JOIN organizations o ON o.id=d.organization_id AND o.id=$1::uuid
		LEFT JOIN asset_network_areas na ON na.id=d.network_area_id
		LEFT JOIN business_systems b ON b.id=d.business_system_id AND b.deleted_at IS NULL
		LEFT JOIN inventory_business_system_drafts wb ON wb.id=d.business_system_draft_id
		       AND wb.draft_status IN ('editing','submitted','returned')
		WHERE d.task_id IS NULL AND d.draft_status IN ('editing','returned')
		  AND d.operation <> 'delete' AND d.network_scope='public'
	)
	SELECT id, kind, asset_type, asset_value, asset_name, organization_id, organization_code, organization_name,
	       business_system_id, business_system_code, business_system_name, network_scope, network_area_name,
	       environment, importance, status, owner_name, reporting_status, updated_at, count(*) OVER ()
	FROM ledger
	WHERE ($2='' OR asset_type=$2)
	  AND ($3='' OR concat_ws(' ', asset_value, asset_name, business_system_name, business_system_code, network_area_name, owner_name) ILIKE '%'||$3||'%')
	ORDER BY updated_at DESC LIMIT $4 OFFSET $5`, orgID, assetType, keyword, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[ScopeCandidate]{}, fmt.Errorf("search request scope candidates: %w", err)
	}
	defer rows.Close()
	items := make([]ScopeCandidate, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item ScopeCandidate
		if err := rows.Scan(&item.ID, &item.Kind, &item.AssetType, &item.AssetValue, &item.AssetName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.BusinessSystemID, &item.BusinessSystemCode, &item.BusinessSystemName,
			&item.NetworkScope, &item.NetworkAreaName, &item.Environment, &item.Importance, &item.Status,
			&item.OwnerName, &item.ReportingStatus, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[ScopeCandidate]{}, fmt.Errorf("scan request scope candidate: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[ScopeCandidate]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		// The window function already reports the total for any page that
		// returned rows; only an empty page needs the fallback count.
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM (
			SELECT a.id FROM assets a
			JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
			JOIN organizations o ON o.id=ao.organization_id AND o.id=$1::uuid
			WHERE a.deleted_at IS NULL AND a.status <> 'archived' AND a.network_scope='public'
			  AND NOT EXISTS (SELECT 1 FROM inventory_asset_drafts d
			                  WHERE d.source_asset_id=a.id AND d.organization_id=o.id
			                    AND d.draft_status IN ('editing','submitted','returned'))
			UNION ALL
			SELECT d.id FROM inventory_asset_drafts d
			WHERE d.organization_id=$1::uuid AND d.task_id IS NULL
			  AND d.draft_status IN ('editing','returned') AND d.operation <> 'delete' AND d.network_scope='public'
		) ledger`, orgID).Scan(&total); err != nil {
			return pagination.Result[ScopeCandidate]{}, fmt.Errorf("count request scope candidates: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

// ContactCandidate is one enabled member of the applicant organization who
// may be named as the request contact. Organization editors cannot call
// GET /users (it requires user.manage), so the picker feeds from here instead.
type ContactCandidate struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// SearchContactCandidates lists the applicant organization's enabled users
// for the request contact picker.
func (s *Service) SearchContactCandidates(ctx context.Context, principal auth.Principal) ([]ContactCandidate, error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, u.username, COALESCE(u.display_name, '') FROM users u
		JOIN user_organizations uo ON uo.user_id=u.id
		WHERE uo.organization_id=$1::uuid AND u.deleted_at IS NULL AND u.status='enabled'
		ORDER BY COALESCE(NULLIF(u.display_name, ''), u.username)`, orgID)
	if err != nil {
		return nil, fmt.Errorf("search request contact candidates: %w", err)
	}
	defer rows.Close()
	items := make([]ContactCandidate, 0)
	for rows.Next() {
		var item ContactCandidate
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName); err != nil {
			return nil, fmt.Errorf("scan request contact candidate: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type businessSystemRef struct {
	ID      string // published business_systems.id, empty when the reference is a working version
	DraftID string // inventory_business_system_drafts.id, empty when published
	Code    string
	Name    string
}

// resolveBusinessSystem snapshots the request's business system reference,
// which must be either a published system owned by the applicant organization
// or one of its unreported working versions.
func resolveBusinessSystem(ctx context.Context, q scopeQuerier, orgID, publishedID, draftID string) (businessSystemRef, error) {
	if draftID != "" {
		var ref businessSystemRef
		err := q.QueryRow(ctx, `
			SELECT id::text, system_code, system_name FROM inventory_business_system_drafts
			WHERE id=$1::uuid AND organization_id=$2::uuid AND task_id IS NULL
			  AND draft_status IN ('editing','returned') AND operation <> 'delete'`, draftID, orgID).Scan(&ref.DraftID, &ref.Code, &ref.Name)
		if errors.Is(err, pgx.ErrNoRows) {
			return businessSystemRef{}, ErrConflict
		}
		if err != nil {
			return businessSystemRef{}, fmt.Errorf("resolve business system draft: %w", err)
		}
		return ref, nil
	}
	var ref businessSystemRef
	err := q.QueryRow(ctx, `
		SELECT id::text, system_code, system_name FROM business_systems
		WHERE id=$1::uuid AND owner_organization_id=$2::uuid AND deleted_at IS NULL`, publishedID, orgID).Scan(&ref.ID, &ref.Code, &ref.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return businessSystemRef{}, ErrConflict
	}
	if err != nil {
		return businessSystemRef{}, fmt.Errorf("resolve business system: %w", err)
	}
	return ref, nil
}

func ensureContactUser(ctx context.Context, q scopeQuerier, orgID, contactUserID string) error {
	var exists bool
	if err := q.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM user_organizations uo
		               JOIN users u ON u.id=uo.user_id AND u.deleted_at IS NULL AND u.status='enabled'
		               WHERE uo.user_id=$1::uuid AND uo.organization_id=$2::uuid)`, contactUserID, orgID).Scan(&exists); err != nil {
		return fmt.Errorf("check request contact user: %w", err)
	}
	if !exists {
		return ErrConflict
	}
	return nil
}

// replaceScope re-freezes the request scope from live ledger data. Published
// assets that gained an active working version since selection are redirected
// to that working version so the launch-time report always carries the newest
// unreported state. report_action records what launch approval must do:
// create/update for working versions, none for published assets.
func replaceScope(ctx context.Context, tx pgx.Tx, requestID, orgID, userID string, publishedIDs, workingIDs []string) error {
	publishedIDs = uniqueIDs(publishedIDs)
	workingIDs = uniqueIDs(workingIDs)
	if len(publishedIDs) > 0 {
		rows, err := tx.Query(ctx, `
			SELECT wd.id::text, wd.source_asset_id::text FROM inventory_asset_drafts wd
			WHERE wd.source_asset_id = ANY($1::uuid[]) AND wd.organization_id=$2::uuid AND wd.task_id IS NULL
			  AND wd.draft_status IN ('editing','returned') AND wd.operation <> 'delete'`, publishedIDs, orgID)
		if err != nil {
			return fmt.Errorf("redirect request scope to working versions: %w", err)
		}
		superseded := make(map[string]string) // published asset id -> working draft id
		for rows.Next() {
			var draftID, sourceID string
			if err := rows.Scan(&draftID, &sourceID); err != nil {
				rows.Close()
				return fmt.Errorf("scan working version redirect: %w", err)
			}
			superseded[sourceID] = draftID
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate working version redirects: %w", err)
		}
		rows.Close()
		if len(superseded) > 0 {
			kept := make([]string, 0, len(publishedIDs))
			for _, id := range publishedIDs {
				if draftID, ok := superseded[id]; ok {
					workingIDs = append(workingIDs, draftID)
					continue
				}
				kept = append(kept, id)
			}
			publishedIDs = kept
			workingIDs = uniqueIDs(workingIDs)
		}
	}
	if len(publishedIDs)+len(workingIDs) > maxScopeAssets {
		return ErrInvalid
	}
	var publishedCount, workingCount int
	if len(publishedIDs) > 0 {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM assets a
			JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
			WHERE a.id = ANY($1::uuid[]) AND ao.organization_id=$2::uuid
			  AND a.deleted_at IS NULL AND a.status <> 'archived' AND a.network_scope='public'`,
			publishedIDs, orgID).Scan(&publishedCount); err != nil {
			return fmt.Errorf("validate published request scope: %w", err)
		}
		if publishedCount != len(publishedIDs) {
			return ErrConflict
		}
	}
	if len(workingIDs) > 0 {
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM inventory_asset_drafts
			WHERE id = ANY($1::uuid[]) AND organization_id=$2::uuid AND task_id IS NULL
			  AND draft_status IN ('editing','returned') AND operation <> 'delete' AND network_scope='public'`,
			workingIDs, orgID).Scan(&workingCount); err != nil {
			return fmt.Errorf("validate working request scope: %w", err)
		}
		if workingCount != len(workingIDs) {
			return ErrConflict
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM security_test_request_scope_assets WHERE request_id=$1::uuid`, requestID); err != nil {
		return fmt.Errorf("clear request scope: %w", err)
	}
	if len(publishedIDs) > 0 {
		command, err := tx.Exec(ctx, `
			INSERT INTO security_test_request_scope_assets
			(request_id, published_asset_id, asset_type_snapshot, asset_value_snapshot, normalized_value_snapshot, asset_name_snapshot,
			 organization_id, organization_code_snapshot, organization_name_snapshot,
			 business_system_id, business_system_code_snapshot, business_system_name_snapshot,
			 network_scope_snapshot, network_area_name_snapshot, environment_snapshot, importance_snapshot, report_action, included_by)
			SELECT $1::uuid, a.id, a.asset_type, a.asset_value, a.normalized_value, a.asset_name,
			       o.id, o.organization_code, o.organization_name,
			       bs.id, bs.system_code, bs.system_name, a.network_scope, na.area_name, a.environment, a.importance, 'none', $2::uuid
			FROM assets a
			JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
			JOIN organizations o ON o.id=ao.organization_id
			LEFT JOIN LATERAL (
				SELECT linked.id, linked.system_code, linked.system_name
				FROM system_assets sa JOIN business_systems linked ON linked.id=sa.business_system_id
				WHERE sa.asset_id=a.id AND linked.deleted_at IS NULL
				ORDER BY sa.is_primary DESC, linked.system_name LIMIT 1
			) bs ON true
			LEFT JOIN asset_network_areas na ON na.id=a.network_area_id
			WHERE a.id = ANY($3::uuid[]) AND ao.organization_id=$4::uuid
			  AND a.deleted_at IS NULL AND a.status <> 'archived' AND a.network_scope='public'`,
			requestID, userID, publishedIDs, orgID)
		if err != nil {
			return fmt.Errorf("freeze published request scope: %w", err)
		}
		if command.RowsAffected() != int64(len(publishedIDs)) {
			return ErrConflict
		}
	}
	if len(workingIDs) > 0 {
		command, err := tx.Exec(ctx, `
			INSERT INTO security_test_request_scope_assets
			(request_id, working_asset_id, asset_type_snapshot, asset_value_snapshot, normalized_value_snapshot, asset_name_snapshot,
			 organization_id, organization_code_snapshot, organization_name_snapshot,
			 business_system_id, business_system_code_snapshot, business_system_name_snapshot,
			 network_scope_snapshot, network_area_name_snapshot, environment_snapshot, importance_snapshot, report_action, included_by)
			SELECT $1::uuid, d.id, d.asset_type, d.asset_value, d.normalized_value, d.asset_name,
			       o.id, o.organization_code, o.organization_name,
			       b.id, COALESCE(wb.system_code, b.system_code), COALESCE(wb.system_name, b.system_name),
			       d.network_scope, na.area_name, d.environment, d.importance,
			       CASE d.operation WHEN 'create' THEN 'create' ELSE 'update' END, $2::uuid
			FROM inventory_asset_drafts d
			JOIN organizations o ON o.id=d.organization_id
			LEFT JOIN asset_network_areas na ON na.id=d.network_area_id
			LEFT JOIN business_systems b ON b.id=d.business_system_id AND b.deleted_at IS NULL
			LEFT JOIN inventory_business_system_drafts wb ON wb.id=d.business_system_draft_id
			       AND wb.draft_status IN ('editing','submitted','returned')
			WHERE d.id = ANY($3::uuid[]) AND d.organization_id=$4::uuid AND d.task_id IS NULL
			  AND d.draft_status IN ('editing','returned') AND d.operation <> 'delete' AND d.network_scope='public'`,
			requestID, userID, workingIDs, orgID)
		if err != nil {
			return fmt.Errorf("freeze working request scope: %w", err)
		}
		if command.RowsAffected() != int64(len(workingIDs)) {
			return ErrConflict
		}
	}
	return nil
}
