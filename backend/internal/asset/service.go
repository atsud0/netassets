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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/xuri/excelize/v2"

	"netassets/internal/access"
	"netassets/internal/auth"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
)

const MaxImportRows = 5000

type Asset struct {
	ID                 string    `json:"id"`
	Type               string    `json:"type"`
	Value              string    `json:"value"`
	NormalizedValue    string    `json:"normalized_value"`
	Name               string    `json:"name,omitempty"`
	OrganizationID     string    `json:"organization_id"`
	OrganizationCode   string    `json:"organization_code"`
	OrganizationName   string    `json:"organization_name"`
	BusinessSystemID   string    `json:"business_system_id,omitempty"`
	BusinessSystemCode string    `json:"business_system_code,omitempty"`
	BusinessSystemName string    `json:"business_system_name,omitempty"`
	Environment        string    `json:"environment"`
	NetworkScope       string    `json:"network_scope"`
	Status             string    `json:"status"`
	Importance         string    `json:"importance"`
	Source             string    `json:"source"`
	OwnerName          string    `json:"owner_name,omitempty"`
	Notes              string    `json:"notes,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type CreateInput struct {
	Type             string `json:"type"`
	Value            string `json:"value"`
	Name             string `json:"name"`
	OrganizationID   string `json:"organization_id"`
	BusinessSystemID string `json:"business_system_id"`
	Environment      string `json:"environment"`
	NetworkScope     string `json:"network_scope"`
	Status           string `json:"status"`
	Importance       string `json:"importance"`
	OwnerName        string `json:"owner_name"`
	Notes            string `json:"notes"`
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
	Status         string        `json:"status"`
	TotalCount     int           `json:"total_count"`
	SuccessCount   int           `json:"success_count"`
	FailureCount   int           `json:"failure_count"`
	DuplicateCount int           `json:"duplicate_count"`
	Errors         []ImportError `json:"errors"`
}

type ImportHistory struct {
	ImportResult
	OrganizationID   string    `json:"organization_id"`
	OrganizationCode string    `json:"organization_code"`
	OrganizationName string    `json:"organization_name"`
	CreatedByName    string    `json:"created_by_name,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Asset, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT a.id::text, a.asset_type, a.asset_value, a.normalized_value, COALESCE(a.asset_name, ''),
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       a.environment, a.network_scope, a.status, a.importance, a.source,
		       COALESCE(ao.owner_name, ''), COALESCE(a.notes, ''), a.created_at, a.updated_at
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
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
			&item.Environment, &item.NetworkScope, &item.Status, &item.Importance, &item.Source,
			&item.OwnerName, &item.Notes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate assets: %w", err)
	}
	return items, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, source, requestID, ipAddress string) (Asset, error) {
	normalizeInput(&input)
	normalizedValue, err := normalizeValue(input.Type, input.Value)
	if err != nil || !validInput(input) {
		return Asset{}, ErrInvalid
	}
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, input.OrganizationID)
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.create', 'asset', $3, '新增网络资产',
		        jsonb_build_object('type', $4::text, 'value', $5::text, 'source', $6::text),
		        NULLIF($7, '')::inet, NULLIF($8, ''))
	`, principal.UserID, input.OrganizationID, item.ID, item.Type, item.Value, source, ipAddress, requestID); err != nil {
		return Asset{}, fmt.Errorf("write asset audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset: %w", err)
	}
	return item, nil
}

func createInTx(ctx context.Context, tx pgx.Tx, principal auth.Principal, input CreateInput, normalizedValue, source string) (Asset, error) {
	var organizationCode, organizationName, organizationStatus string
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id = $1::uuid`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	} else if err != nil {
		return Asset{}, fmt.Errorf("load asset organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return Asset{}, ErrInvalid
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

	var item Asset
	err := tx.QueryRow(ctx, `
		INSERT INTO assets (asset_type, asset_value, normalized_value, asset_name, environment, network_scope,
		                    status, importance, source, notes, created_by, updated_by, first_seen_at, last_seen_at)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, $7, $8, $9, NULLIF($10, ''), $11::uuid, $11::uuid, now(), now())
		RETURNING id::text, created_at, updated_at
	`, input.Type, input.Value, normalizedValue, input.Name, input.Environment, input.NetworkScope,
		input.Status, input.Importance, source, input.Notes, principal.UserID).Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Asset{}, ErrConflict
		}
		return Asset{}, fmt.Errorf("insert asset: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary, owner_name)
		VALUES ($1::uuid, $2::uuid, 'owner', true, NULLIF($3, ''))
	`, item.ID, input.OrganizationID, input.OwnerName); err != nil {
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
	item.Environment, item.NetworkScope, item.Status, item.Importance = input.Environment, input.NetworkScope, input.Status, input.Importance
	item.Source, item.OwnerName, item.Notes = source, input.OwnerName, input.Notes
	return item, nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, assetID string) (Asset, error) {
	if _, err := s.authorizeAsset(ctx, principal, assetID); err != nil {
		return Asset{}, err
	}
	return queryAsset(ctx, s.pool, assetID)
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, assetID string, input CreateInput, requestID, ipAddress string) (Asset, error) {
	current, err := s.Get(ctx, principal, assetID)
	if err != nil {
		return Asset{}, err
	}
	normalizeInput(&input)
	normalizedValue, err := normalizeValue(input.Type, input.Value)
	if err != nil || !validInput(input) {
		return Asset{}, ErrInvalid
	}
	if input.OrganizationID != current.OrganizationID && !principal.HasRole("group_admin") {
		return Asset{}, ErrForbidden
	}
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, input.OrganizationID)
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
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id::text = $1`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	} else if err != nil {
		return Asset{}, fmt.Errorf("load update organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return Asset{}, ErrInvalid
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
		                  environment = $6, network_scope = $7, status = $8, importance = $9,
		                  notes = NULLIF($10, ''), updated_by = $11::uuid, updated_at = now()
		WHERE id::text = $1 AND deleted_at IS NULL
	`, assetID, input.Type, input.Value, normalizedValue, input.Name, input.Environment, input.NetworkScope,
		input.Status, input.Importance, input.Notes, principal.UserID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Asset{}, ErrConflict
		}
		return Asset{}, fmt.Errorf("update asset: %w", err)
	}
	if command.RowsAffected() == 0 {
		return Asset{}, ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM asset_organizations WHERE asset_id::text = $1 AND is_primary`, assetID); err != nil {
		return Asset{}, fmt.Errorf("clear primary asset organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary, owner_name)
		VALUES ($1::uuid, $2::uuid, 'owner', true, NULLIF($3, ''))
	`, assetID, input.OrganizationID, input.OwnerName); err != nil {
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.update', 'asset', $3, '编辑网络资产',
		        jsonb_build_object('old_type', $4::text, 'old_value', $5::text, 'old_organization_id', $6::text,
		                           'type', $7::text, 'value', $8::text, 'status', $9::text),
		        NULLIF($10, '')::inet, NULLIF($11, ''))
	`, principal.UserID, input.OrganizationID, assetID, current.Type, current.Value, current.OrganizationID,
		input.Type, input.Value, input.Status, ipAddress, requestID); err != nil {
		return Asset{}, fmt.Errorf("write asset update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset update: %w", err)
	}
	return s.Get(ctx, principal, assetID)
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, assetID, requestID, ipAddress string) error {
	current, err := s.Get(ctx, principal, assetID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin asset delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
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
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.delete', 'asset', $3, '删除网络资产',
		        jsonb_build_object('type', $4::text, 'value', $5::text), NULLIF($6, '')::inet, NULLIF($7, ''))
	`, principal.UserID, current.OrganizationID, assetID, current.Type, current.Value, ipAddress, requestID); err != nil {
		return fmt.Errorf("write asset delete audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit asset delete: %w", err)
	}
	return nil
}

func (s *Service) ListImports(ctx context.Context, principal auth.Principal) ([]ImportHistory, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT ai.id::text, ai.file_name, ai.file_type, ai.status, ai.total_count, ai.success_count,
		       ai.failure_count, ai.duplicate_count, ai.errors::text, o.id::text, o.organization_code,
		       o.organization_name, COALESCE(u.display_name, ''), ai.created_at
		FROM asset_imports ai
		JOIN organizations o ON o.id = ai.organization_id
		LEFT JOIN users u ON u.id = ai.created_by
		WHERE $2::boolean OR o.id IN (SELECT id FROM accessible)
		ORDER BY ai.created_at DESC
		LIMIT 100
	`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list asset imports: %w", err)
	}
	defer rows.Close()
	items := make([]ImportHistory, 0)
	for rows.Next() {
		var item ImportHistory
		var errorsText string
		if err := rows.Scan(&item.ID, &item.FileName, &item.FileType, &item.Status, &item.TotalCount,
			&item.SuccessCount, &item.FailureCount, &item.DuplicateCount, &errorsText,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.CreatedByName, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan asset import: %w", err)
		}
		item.Errors = make([]ImportError, 0)
		if err := json.Unmarshal([]byte(errorsText), &item.Errors); err != nil {
			return nil, fmt.Errorf("decode asset import errors: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate asset imports: %w", err)
	}
	return items, nil
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
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, organizationID)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", ErrForbidden
	}
	return organizationID, nil
}

func queryAsset(ctx context.Context, querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, assetID string) (Asset, error) {
	var item Asset
	err := querier.QueryRow(ctx, `
		SELECT a.id::text, a.asset_type, a.asset_value, a.normalized_value, COALESCE(a.asset_name, ''),
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       a.environment, a.network_scope, a.status, a.importance, a.source,
		       COALESCE(ao.owner_name, ''), COALESCE(a.notes, ''), a.created_at, a.updated_at
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
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
		&item.Environment, &item.NetworkScope, &item.Status, &item.Importance, &item.Source,
		&item.OwnerName, &item.Notes, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, fmt.Errorf("load asset: %w", err)
	}
	return item, nil
}

func Template(format string) ([]byte, string, string, error) {
	headers := []string{"资产类型", "资产值", "资产名称", "业务系统编码", "环境", "网络范围", "重要程度", "负责人", "备注"}
	example := []string{"domain", "portal.example.com", "门户网站域名", "", "production", "public", "important", "张三", "支持 domain/ip/url/service/other"}
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
		_ = file.SetColWidth(sheet, "D", "I", 18)
		var buffer bytes.Buffer
		if err := file.Write(&buffer); err != nil {
			return nil, "", "", err
		}
		return buffer.Bytes(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "网络资产导入模板.xlsx", nil
	default:
		return nil, "", "", ErrInvalid
	}
}

func (s *Service) Import(ctx context.Context, principal auth.Principal, organizationID, fileName string, reader io.Reader, requestID, ipAddress string) (ImportResult, error) {
	organizationID = strings.TrimSpace(organizationID)
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, organizationID)
	if err != nil {
		return ImportResult{}, err
	}
	if !allowed {
		return ImportResult{}, ErrForbidden
	}
	rows, fileType, err := readImportRows(fileName, reader)
	if err != nil {
		return ImportResult{}, ErrInvalid
	}
	if len(rows) < 2 || len(rows)-1 > MaxImportRows {
		return ImportResult{}, ErrInvalid
	}
	headers := mapHeaders(rows[0])
	if headers["type"] < 0 || headers["value"] < 0 {
		return ImportResult{}, ErrInvalid
	}

	result := ImportResult{FileName: filepath.Base(fileName), FileType: fileType, Errors: make([]ImportError, 0), TotalCount: len(rows) - 1}
	for index, row := range rows[1:] {
		rowNumber := index + 2
		input := importInput(row, headers, organizationID)
		if input.BusinessSystemID != "" {
			var systemID string
			err := s.pool.QueryRow(ctx, `
				SELECT id::text FROM business_systems
				WHERE lower(system_code) = lower($1) AND owner_organization_id = $2::uuid AND deleted_at IS NULL
			`, input.BusinessSystemID, organizationID).Scan(&systemID)
			if errors.Is(err, pgx.ErrNoRows) {
				result.FailureCount++
				result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "system_not_found", Message: "业务系统编码不存在或不属于当前组织"})
				continue
			}
			if err != nil {
				return ImportResult{}, fmt.Errorf("resolve import business system: %w", err)
			}
			input.BusinessSystemID = systemID
		}
		_, err := s.Create(ctx, principal, input, "import", requestID, ipAddress)
		switch {
		case err == nil:
			result.SuccessCount++
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
		INSERT INTO asset_imports (organization_id, file_name, file_type, status, total_count, success_count,
		                           failure_count, duplicate_count, errors, created_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::uuid)
		RETURNING id::text
	`, organizationID, result.FileName, result.FileType, result.Status, result.TotalCount, result.SuccessCount,
		result.FailureCount, result.DuplicateCount, string(errorsJSON), principal.UserID).Scan(&importID); err != nil {
		return "", fmt.Errorf("save import result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'asset.import', 'asset_import', $3, '批量导入网络资产',
		        jsonb_build_object('file_name', $4::text, 'total', $5::int, 'success', $6::int,
		                           'failure', $7::int, 'duplicate', $8::int), NULLIF($9, '')::inet, NULLIF($10, ''))
	`, principal.UserID, organizationID, importID, result.FileName, result.TotalCount, result.SuccessCount,
		result.FailureCount, result.DuplicateCount, ipAddress, requestID); err != nil {
		return "", fmt.Errorf("write import audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit import result: %w", err)
	}
	return importID, nil
}

func normalizeInput(input *CreateInput) {
	input.Type = strings.ToLower(strings.TrimSpace(input.Type))
	input.Value = strings.TrimSpace(input.Value)
	input.Name = strings.TrimSpace(input.Name)
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.BusinessSystemID = strings.TrimSpace(input.BusinessSystemID)
	input.Environment = defaultValue(strings.ToLower(strings.TrimSpace(input.Environment)), "production")
	input.NetworkScope = defaultValue(strings.ToLower(strings.TrimSpace(input.NetworkScope)), "unknown")
	input.Status = defaultValue(strings.ToLower(strings.TrimSpace(input.Status)), "active")
	input.Importance = defaultValue(strings.ToLower(strings.TrimSpace(input.Importance)), "normal")
	input.OwnerName = strings.TrimSpace(input.OwnerName)
	input.Notes = strings.TrimSpace(input.Notes)
}
func validInput(input CreateInput) bool {
	return input.OrganizationID != "" && oneOf(input.Type, "domain", "ip", "url", "service", "other") && input.Value != "" &&
		oneOf(input.Environment, "production", "staging", "testing", "development", "office", "other") &&
		oneOf(input.NetworkScope, "public", "private", "unknown") && oneOf(input.Status, "active", "pending", "disabled", "archived") &&
		oneOf(input.Importance, "normal", "important", "core")
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
		"网络范围": "network_scope", "network_scope": "network_scope", "重要程度": "importance", "importance": "importance",
		"负责人": "owner_name", "owner_name": "owner_name", "备注": "notes", "notes": "notes",
	}
	result := map[string]int{"type": -1, "value": -1, "name": -1, "business_system_code": -1, "environment": -1, "network_scope": -1, "importance": -1, "owner_name": -1, "notes": -1}
	for index, header := range row {
		if key, ok := aliases[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")))]; ok {
			result[key] = index
		}
	}
	return result
}
func importInput(row []string, headers map[string]int, organizationID string) CreateInput {
	get := func(key string) string {
		index := headers[key]
		if index < 0 || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}
	return CreateInput{Type: translateAssetType(get("type")), Value: get("value"), Name: get("name"), OrganizationID: organizationID,
		BusinessSystemID: get("business_system_code"), Environment: translateEnvironment(get("environment")), NetworkScope: translateScope(get("network_scope")),
		Status: "active", Importance: translateImportance(get("importance")), OwnerName: get("owner_name"), Notes: get("notes")}
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
