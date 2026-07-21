package organization

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
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
	codePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$`)
)

type Organization struct {
	ID               string    `json:"id"`
	ParentID         *string   `json:"parent_id"`
	ParentName       string    `json:"parent_name,omitempty"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	ShortName        string    `json:"short_name,omitempty"`
	Type             string    `json:"type"`
	Manager          string    `json:"manager,omitempty"`
	Contact          string    `json:"contact,omitempty"`
	Status           string    `json:"status"`
	Notes            string    `json:"notes,omitempty"`
	ChildCount       int64     `json:"child_count"`
	BusinessSysCount int64     `json:"business_system_count"`
	AssetCount       int64     `json:"asset_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type UpdateInput struct {
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Manager   string `json:"manager"`
	Contact   string `json:"contact"`
	Notes     string `json:"notes"`
}

type CreateInput struct {
	ParentID  string `json:"parent_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Type      string `json:"type"`
	Manager   string `json:"manager"`
	Contact   string `json:"contact"`
	Notes     string `json:"notes"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id
			FROM user_roles ur
			WHERE ur.user_id = $1::uuid
			  AND ur.organization_id IS NOT NULL
			  AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION
			SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		), accessible(id) AS (
			SELECT ur.organization_id
			FROM user_roles ur
			WHERE ur.user_id = $1::uuid
			  AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT o.id::text, o.parent_id::text, COALESCE(parent.organization_name, ''),
		       o.organization_code, o.organization_name, COALESCE(o.short_name, ''), o.organization_type,
		       COALESCE(o.manager, ''), COALESCE(o.contact, ''), o.status, COALESCE(o.notes, ''),
		       (SELECT count(*) FROM organizations child WHERE child.parent_id = o.id),
		       (SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id = o.id AND bs.deleted_at IS NULL),
		       (SELECT count(*) FROM asset_organizations ao WHERE ao.organization_id = o.id),
		       o.created_at, o.updated_at
		FROM organizations o
		LEFT JOIN organizations parent ON parent.id = o.parent_id
		WHERE $2::boolean OR o.id IN (SELECT id FROM accessible)
		ORDER BY CASE WHEN o.organization_type = 'group' THEN 0 ELSE 1 END, o.organization_code
	`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()
	items := make([]Organization, 0)
	for rows.Next() {
		var item Organization
		if err := rows.Scan(&item.ID, &item.ParentID, &item.ParentName, &item.Code, &item.Name, &item.ShortName, &item.Type,
			&item.Manager, &item.Contact, &item.Status, &item.Notes, &item.ChildCount, &item.BusinessSysCount,
			&item.AssetCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organizations: %w", err)
	}
	return items, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (Organization, error) {
	if !principal.HasRole("group_admin") {
		return Organization{}, ErrForbidden
	}
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.ShortName = strings.TrimSpace(input.ShortName)
	input.ParentID = strings.TrimSpace(input.ParentID)
	input.Type = strings.TrimSpace(input.Type)
	input.Manager = strings.TrimSpace(input.Manager)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Notes = strings.TrimSpace(input.Notes)
	if !codePattern.MatchString(input.Code) || input.Name == "" || len(input.Name) > 255 || input.ParentID == "" {
		return Organization{}, ErrInvalid
	}
	if input.Type != "subsidiary" && input.Type != "department" && input.Type != "other" {
		return Organization{}, ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin create organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var parentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id = $1::uuid`, input.ParentID).Scan(&parentStatus); errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	} else if err != nil {
		return Organization{}, fmt.Errorf("load parent organization: %w", err)
	}
	if parentStatus != "enabled" {
		return Organization{}, ErrInvalid
	}

	var item Organization
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (
			parent_id, organization_code, organization_name, short_name, organization_type,
			manager, contact, status, notes
		) VALUES (
			$1::uuid, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), NULLIF($7, ''), 'enabled', NULLIF($8, '')
		)
		RETURNING id::text, parent_id::text, organization_code, organization_name,
		          COALESCE(short_name, ''), organization_type, COALESCE(manager, ''), COALESCE(contact, ''),
		          status, COALESCE(notes, ''), created_at, updated_at
	`, input.ParentID, input.Code, input.Name, input.ShortName, input.Type, input.Manager, input.Contact, input.Notes).Scan(
		&item.ID, &item.ParentID, &item.Code, &item.Name, &item.ShortName, &item.Type,
		&item.Manager, &item.Contact, &item.Status, &item.Notes, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Organization{}, ErrConflict
		}
		return Organization{}, fmt.Errorf("insert organization: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT organization_name FROM organizations WHERE id = $1::uuid`, input.ParentID).Scan(&item.ParentName); err != nil {
		return Organization{}, fmt.Errorf("load parent organization name: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'organization.create', 'organization', $3, '新增组织机构',
		        jsonb_build_object('code', $4::text, 'name', $5::text, 'type', $6::text), NULLIF($7, '')::inet, NULLIF($8, ''))
	`, principal.UserID, input.ParentID, item.ID, item.Code, item.Name, item.Type, ipAddress, requestID); err != nil {
		return Organization{}, fmt.Errorf("write organization audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization: %w", err)
	}
	return item, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, organizationID string, input UpdateInput, requestID, ipAddress string) (Organization, error) {
	if !principal.HasRole("group_admin") {
		return Organization{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	input.Name = strings.TrimSpace(input.Name)
	input.ShortName = strings.TrimSpace(input.ShortName)
	input.Manager = strings.TrimSpace(input.Manager)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Notes = strings.TrimSpace(input.Notes)
	if organizationID == "" || input.Name == "" || len(input.Name) > 255 || len(input.ShortName) > 128 ||
		len(input.Manager) > 128 || len(input.Contact) > 255 || len(input.Notes) > 5000 {
		return Organization{}, ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin update organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var item Organization
	err = tx.QueryRow(ctx, `
		UPDATE organizations o SET
			organization_name = $2,
			short_name = NULLIF($3, ''),
			manager = NULLIF($4, ''),
			contact = NULLIF($5, ''),
			notes = NULLIF($6, ''),
			updated_at = now()
		WHERE o.id::text = $1
		RETURNING o.id::text, o.parent_id::text, o.organization_code, o.organization_name,
		          COALESCE(o.short_name, ''), o.organization_type, COALESCE(o.manager, ''),
		          COALESCE(o.contact, ''), o.status, COALESCE(o.notes, ''), o.created_at, o.updated_at
	`, organizationID, input.Name, input.ShortName, input.Manager, input.Contact, input.Notes).Scan(
		&item.ID, &item.ParentID, &item.Code, &item.Name, &item.ShortName, &item.Type,
		&item.Manager, &item.Contact, &item.Status, &item.Notes, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("update organization: %w", err)
	}
	if item.ParentID != nil {
		if err := tx.QueryRow(ctx, `SELECT organization_name FROM organizations WHERE id = $1::uuid`, *item.ParentID).Scan(&item.ParentName); err != nil {
			return Organization{}, fmt.Errorf("load parent organization name: %w", err)
		}
	}
	if err := tx.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM organizations child WHERE child.parent_id = $1::uuid),
			(SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id = $1::uuid AND bs.deleted_at IS NULL),
			(SELECT count(*) FROM asset_organizations ao WHERE ao.organization_id = $1::uuid)
	`, item.ID).Scan(&item.ChildCount, &item.BusinessSysCount, &item.AssetCount); err != nil {
		return Organization{}, fmt.Errorf("load organization counts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'organization.update', 'organization', $2, '编辑组织机构',
		        jsonb_build_object('name', $3::text, 'short_name', $4::text, 'manager', $5::text, 'contact', $6::text),
		        NULLIF($7, '')::inet, NULLIF($8, ''))
	`, principal.UserID, item.ID, item.Name, item.ShortName, item.Manager, item.Contact, ipAddress, requestID); err != nil {
		return Organization{}, fmt.Errorf("write organization update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization update: %w", err)
	}
	return item, nil
}
