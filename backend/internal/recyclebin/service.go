package recyclebin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/notification"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// Item is a soft-deleted resource that can be recovered by a group administrator.
type Item struct {
	ID               string    `json:"id"`
	ResourceType     string    `json:"resource_type"`
	DisplayName      string    `json:"display_name"`
	SecondaryText    string    `json:"secondary_text"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationCode string    `json:"organization_code"`
	OrganizationName string    `json:"organization_name"`
	Status           string    `json:"status"`
	DeletedAt        time.Time `json:"deleted_at"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// Search returns a bounded page of soft-deleted resources. A resource-type filter keeps
// each recycle-bin tab independently paginated instead of loading all deleted records
// into the browser and filtering them there.
func (s *Service) Search(ctx context.Context, principal auth.Principal, query pagination.Query, resourceType string) (pagination.Result[Item], error) {
	if !principal.HasRole("group_admin") {
		return pagination.Result[Item]{}, ErrForbidden
	}
	resourceType = strings.TrimSpace(resourceType)
	if resourceType != "" && resourceType != "asset" && resourceType != "business_system" {
		return pagination.Result[Item]{}, ErrInvalid
	}

	rows, err := s.pool.Query(ctx, `
		WITH recycle AS (
			SELECT a.id::text AS id, 'asset'::text AS resource_type,
			       COALESCE(NULLIF(a.asset_name, ''), a.asset_value) AS display_name,
			       a.asset_type || ' · ' || a.asset_value AS secondary_text,
			       o.id::text AS organization_id, o.organization_code, o.organization_name, a.status, a.deleted_at
			FROM assets a
			JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
			JOIN organizations o ON o.id = ao.organization_id
			WHERE a.deleted_at IS NOT NULL
			UNION ALL
			SELECT bs.id::text AS id, 'business_system'::text AS resource_type,
			       bs.system_name AS display_name, bs.system_code AS secondary_text,
			       o.id::text AS organization_id, o.organization_code, o.organization_name, bs.system_status, bs.deleted_at
			FROM business_systems bs
			JOIN organizations o ON o.id = bs.owner_organization_id
			WHERE bs.deleted_at IS NOT NULL
		)
		SELECT recycle.*, count(*) OVER ()
		FROM recycle
		WHERE ($1::text = '' OR resource_type = $1::text)
		ORDER BY deleted_at DESC, resource_type, display_name
		LIMIT $2 OFFSET $3
	`, resourceType, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Item]{}, fmt.Errorf("search recycle bin: %w", err)
	}
	defer rows.Close()

	items := make([]Item, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.ResourceType, &item.DisplayName, &item.SecondaryText, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.Status, &item.DeletedAt, &total); err != nil {
			return pagination.Result[Item]{}, fmt.Errorf("scan recycle bin page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Item]{}, fmt.Errorf("iterate recycle bin page: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
            WITH recycle AS (
                SELECT 'asset'::text AS resource_type FROM assets WHERE deleted_at IS NOT NULL
                UNION ALL
                SELECT 'business_system'::text AS resource_type FROM business_systems WHERE deleted_at IS NOT NULL
            )
            SELECT count(*) FROM recycle
            WHERE ($1::text = '' OR resource_type = $1::text)
        `, resourceType).Scan(&total); err != nil {
			return pagination.Result[Item]{}, fmt.Errorf("count recycle bin: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

// RestoreAsset restores a soft-deleted asset while preserving its original ownership, system
// associations and network-area attributes. An active duplicate is rejected rather than silently
// weakening the active-assets uniqueness rule.
func (s *Service) RestoreAsset(ctx context.Context, principal auth.Principal, assetID, requestID, ipAddress string) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	assetID = strings.TrimSpace(assetID)
	if !validUUID(assetID) {
		return ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin asset restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var organizationID, assetType, normalizedValue, value string
	err = tx.QueryRow(ctx, `
		SELECT o.id::text, a.asset_type, a.normalized_value, a.asset_value
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		JOIN organizations o ON o.id = ao.organization_id
		WHERE a.id = $1::uuid AND a.deleted_at IS NOT NULL
		FOR UPDATE OF a
	`, assetID).Scan(&organizationID, &assetType, &normalizedValue, &value)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load deleted asset: %w", err)
	}

	var hasConflict bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM assets
			WHERE asset_type = $1 AND normalized_value = $2 AND deleted_at IS NULL AND id <> $3::uuid
		)
	`, assetType, normalizedValue, assetID).Scan(&hasConflict); err != nil {
		return fmt.Errorf("check asset restore conflict: %w", err)
	}
	if hasConflict {
		return ErrConflict
	}

	command, err := tx.Exec(ctx, `
		UPDATE assets
		SET deleted_at = NULL, updated_by = $2::uuid, updated_at = now()
		WHERE id = $1::uuid AND deleted_at IS NOT NULL
	`, assetID, principal.UserID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return fmt.Errorf("restore asset: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.restore', 'asset', $3, '恢复网络资产',
		        jsonb_build_object('type', $4::text, 'value', $5::text), NULLIF($6, '')::inet, NULLIF($7, ''))
	`, principal.UserID, organizationID, assetID, assetType, value, ipAddress, requestID); err != nil {
		return fmt.Errorf("write asset restore audit: %w", err)
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetRestored, Category: notification.CategoryAssetChange,
		TargetOrganizationID: organizationID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: assetID, ResourceLabel: value, BatchKey: requestID, Level: "info",
		Details: map[string]any{"asset_type": assetType, "asset_value": value},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit asset restore: %w", err)
	}
	return nil
}

// RestoreBusinessSystem restores a soft-deleted system as archived. It intentionally does not
// reactivate the system, leaving the administrator to confirm business and security readiness.
func (s *Service) RestoreBusinessSystem(ctx context.Context, principal auth.Principal, systemID, requestID, ipAddress string) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	systemID = strings.TrimSpace(systemID)
	if !validUUID(systemID) {
		return ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin business system restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var organizationID, systemCode, systemName string
	err = tx.QueryRow(ctx, `
		SELECT owner_organization_id::text, system_code, system_name
		FROM business_systems
		WHERE id = $1::uuid AND deleted_at IS NOT NULL
		FOR UPDATE
	`, systemID).Scan(&organizationID, &systemCode, &systemName)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load deleted business system: %w", err)
	}

	var hasConflict bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM business_systems
			WHERE system_code = $1 AND deleted_at IS NULL AND id <> $2::uuid
		)
	`, systemCode, systemID).Scan(&hasConflict); err != nil {
		return fmt.Errorf("check business system restore conflict: %w", err)
	}
	if hasConflict {
		return ErrConflict
	}

	command, err := tx.Exec(ctx, `
		UPDATE business_systems
		SET deleted_at = NULL, system_status = 'archived', updated_by = $2::uuid, updated_at = now()
		WHERE id = $1::uuid AND deleted_at IS NOT NULL
	`, systemID, principal.UserID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrConflict
		}
		return fmt.Errorf("restore business system: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'business_system.restore', 'business_system', $3, '从回收站恢复业务系统',
		        jsonb_build_object('system_code', $4::text, 'system_name', $5::text, 'restored_status', 'archived'),
		        NULLIF($6, '')::inet, NULLIF($7, ''))
	`, principal.UserID, organizationID, systemID, systemCode, systemName, ipAddress, requestID); err != nil {
		return fmt.Errorf("write business system restore audit: %w", err)
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventBusinessSystemRestored, Category: notification.CategoryBusinessSystemChange,
		TargetOrganizationID: organizationID, ActorUserID: principal.UserID, RequiredPermission: "system.manage",
		ResourceType: "business_system", ResourceID: systemID, ResourceLabel: systemName, BatchKey: requestID, Level: "info",
		Details: map[string]any{"system_code": systemCode, "restored_status": "archived", "source": "recycle_bin"},
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit business system restore: %w", err)
	}
	return nil
}

func validUUID(value string) bool { return uuidPattern.MatchString(value) }
