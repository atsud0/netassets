package changehistory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/access"
	"netassets/internal/auditlog"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid input")
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const MaxItems = pagination.MaxPageSize

// Result is kept as an alias for callers that still use the original service
// methods. All change-history responses now follow the shared page contract.
type Result = pagination.Result[auditlog.Item]

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) ListAsset(ctx context.Context, principal auth.Principal, assetID string) (Result, error) {
	return s.SearchAsset(ctx, principal, assetID, pagination.Query{Page: 1, PageSize: MaxItems})
}

func (s *Service) SearchAsset(ctx context.Context, principal auth.Principal, assetID string, page pagination.Query) (pagination.Result[auditlog.Item], error) {
	organizationID, err := s.assetOrganization(ctx, assetID)
	if err != nil {
		return pagination.Result[auditlog.Item]{}, err
	}
	return s.search(ctx, principal, organizationID, "asset", assetID, page)
}

func (s *Service) ListBusinessSystem(ctx context.Context, principal auth.Principal, systemID string) (Result, error) {
	return s.SearchBusinessSystem(ctx, principal, systemID, pagination.Query{Page: 1, PageSize: MaxItems})
}

func (s *Service) SearchBusinessSystem(ctx context.Context, principal auth.Principal, systemID string, page pagination.Query) (pagination.Result[auditlog.Item], error) {
	organizationID, err := s.businessSystemOrganization(ctx, systemID)
	if err != nil {
		return pagination.Result[auditlog.Item]{}, err
	}
	return s.search(ctx, principal, organizationID, "business_system", systemID, page)
}

func (s *Service) assetOrganization(ctx context.Context, assetID string) (string, error) {
	if !validUUID(assetID) {
		return "", ErrInvalid
	}
	var organizationID string
	err := s.pool.QueryRow(ctx, `
		SELECT ao.organization_id::text
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		WHERE a.id = $1::uuid AND a.deleted_at IS NULL
	`, assetID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load asset organization: %w", err)
	}
	return organizationID, nil
}

func (s *Service) businessSystemOrganization(ctx context.Context, systemID string) (string, error) {
	if !validUUID(systemID) {
		return "", ErrInvalid
	}
	var organizationID string
	err := s.pool.QueryRow(ctx, `
		SELECT owner_organization_id::text
		FROM business_systems
		WHERE id = $1::uuid AND deleted_at IS NULL
	`, systemID).Scan(&organizationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("load business system organization: %w", err)
	}
	return organizationID, nil
}

func (s *Service) search(ctx context.Context, principal auth.Principal, organizationID, resourceType, resourceID string, page pagination.Query) (pagination.Result[auditlog.Item], error) {
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, organizationID)
	if err != nil {
		return pagination.Result[auditlog.Item]{}, err
	}
	if !allowed {
		return pagination.Result[auditlog.Item]{}, ErrForbidden
	}

	rows, err := s.pool.Query(ctx, `
		SELECT al.id, COALESCE(al.user_id::text, ''), COALESCE(u.username, ''), COALESCE(u.display_name, ''),
		       COALESCE(al.organization_id::text, ''), COALESCE(o.organization_code, ''), COALESCE(o.organization_name, ''),
		       al.action, COALESCE(al.resource_type, ''), COALESCE(al.resource_id, ''), COALESCE(al.summary, ''),
		       al.details, COALESCE(host(al.request_ip), ''), COALESCE(al.request_id, ''), al.result, al.created_at,
		       count(*) OVER ()
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.user_id
		LEFT JOIN organizations o ON o.id = al.organization_id
		WHERE al.resource_type = $1 AND al.resource_id = $2
		ORDER BY al.created_at DESC, al.id DESC
		LIMIT $3 OFFSET $4
	`, resourceType, resourceID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[auditlog.Item]{}, fmt.Errorf("list resource change history: %w", err)
	}
	defer rows.Close()

	items := make([]auditlog.Item, 0)
	total := 0
	for rows.Next() {
		var item auditlog.Item
		if err := rows.Scan(&item.ID, &item.UserID, &item.Username, &item.DisplayName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.Action, &item.ResourceType, &item.ResourceID, &item.Summary,
			&item.Details, &item.RequestIP, &item.RequestID, &item.Result, &item.CreatedAt, &total); err != nil {
			return pagination.Result[auditlog.Item]{}, fmt.Errorf("scan resource change history: %w", err)
		}
		if len(item.Details) == 0 {
			item.Details = json.RawMessage(`{}`)
		}
		item.ActionLabel = auditlog.ActionLabel(item.Action)
		item.ResourceTypeLabel = auditlog.ResourceTypeLabel(item.ResourceType)
		item.ResultLabel = auditlog.ResultLabel(item.Result)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[auditlog.Item]{}, fmt.Errorf("iterate resource change history: %w", err)
	}
	// count(*) OVER() is unavailable when an out-of-range page produces no rows.
	// Preserve a truthful total so the client can return to the preceding page.
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*)
			FROM audit_logs
			WHERE resource_type = $1 AND resource_id = $2
		`, resourceType, resourceID).Scan(&total); err != nil {
			return pagination.Result[auditlog.Item]{}, fmt.Errorf("count resource change history: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func validUUID(value string) bool {
	return uuidPattern.MatchString(strings.TrimSpace(value))
}
