package businesssystem

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInUse     = errors.New("in use")
	ErrInvalid   = errors.New("invalid input")
	codePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$`)
)

type BusinessSystem struct {
	ID                                   string    `json:"id"`
	Code                                 string    `json:"code"`
	Name                                 string    `json:"name"`
	ShortName                            string    `json:"short_name,omitempty"`
	OrganizationID                       string    `json:"organization_id"`
	OrganizationCode                     string    `json:"organization_code"`
	OrganizationName                     string    `json:"organization_name"`
	OwnerName                            string    `json:"owner_name,omitempty"`
	OwnerContact                         string    `json:"owner_contact,omitempty"`
	Status                               string    `json:"status"`
	Level                                string    `json:"level"`
	DeploymentType                       string    `json:"deployment_type,omitempty"`
	InternetExposure                     string    `json:"internet_exposure"`
	Description                          string    `json:"description,omitempty"`
	ContainsPersonalInformation          string    `json:"contains_personal_information"`
	ContainsSensitivePersonalInformation string    `json:"contains_sensitive_personal_information"`
	PersonalInformationRecordCount       *int64    `json:"personal_information_record_count"`
	DataVolumeOver100K                   string    `json:"data_volume_over_100k"`
	ContainsImportantData                string    `json:"contains_important_data"`
	ContainsCoreData                     string    `json:"contains_core_data"`
	DataStorageLocation                  string    `json:"data_storage_location,omitempty"`
	SecurityNotes                        string    `json:"security_notes,omitempty"`
	AssetCount                           int       `json:"asset_count,omitempty"`
	CreatedAt                            time.Time `json:"created_at"`
	UpdatedAt                            time.Time `json:"updated_at"`
}

type CreateInput struct {
	Code                                 string `json:"code"`
	Name                                 string `json:"name"`
	ShortName                            string `json:"short_name"`
	OrganizationID                       string `json:"organization_id"`
	OwnerName                            string `json:"owner_name"`
	OwnerContact                         string `json:"owner_contact"`
	Status                               string `json:"status"`
	Level                                string `json:"level"`
	DeploymentType                       string `json:"deployment_type"`
	InternetExposure                     string `json:"internet_exposure"`
	Description                          string `json:"description"`
	ContainsPersonalInformation          string `json:"contains_personal_information"`
	ContainsSensitivePersonalInformation string `json:"contains_sensitive_personal_information"`
	PersonalInformationRecordCount       *int64 `json:"personal_information_record_count"`
	DataVolumeOver100K                   string `json:"data_volume_over_100k"`
	ContainsImportantData                string `json:"contains_important_data"`
	ContainsCoreData                     string `json:"contains_core_data"`
	DataStorageLocation                  string `json:"data_storage_location"`
	SecurityNotes                        string `json:"security_notes"`
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

const MaxImportRows = 5000

// SearchFilter describes the supported ledger filters. Page is always bounded
// by the common pagination contract.
type SearchFilter struct {
	OrganizationID string
	Status         string
	Keyword        string
	Page           pagination.Query
}

type Service struct{ pool *pgxpool.Pool }

type fieldChange struct {
	Field  string `json:"field"`
	Label  string `json:"label"`
	Before string `json:"before"`
	After  string `json:"after"`
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]BusinessSystem, error) {
	if !principal.HasPermission("system.manage") {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'system.manage'
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
        ), accessible(id) AS (
            SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'system.manage'
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION SELECT id FROM descendants
        )
        SELECT bs.id::text, bs.system_code, bs.system_name, COALESCE(bs.short_name, ''),
               o.id::text, o.organization_code, o.organization_name,
               COALESCE(bs.owner_name, ''), COALESCE(bs.owner_contact, ''), bs.system_status, bs.system_level,
               COALESCE(bs.deployment_type, ''), bs.internet_exposure, COALESCE(bs.description, ''),
               p.contains_personal_information, p.contains_sensitive_personal_information,
               p.personal_information_record_count, p.data_volume_over_100k,
               p.contains_important_data, p.contains_core_data, COALESCE(p.data_storage_location, ''), COALESCE(p.notes, ''),
               bs.created_at, bs.updated_at
        FROM business_systems bs
        JOIN organizations o ON o.id = bs.owner_organization_id
        JOIN business_system_data_security_profiles p ON p.business_system_id = bs.id
        WHERE bs.deleted_at IS NULL
          AND ($2::boolean OR bs.owner_organization_id IN (SELECT id FROM accessible))
        ORDER BY o.organization_code, bs.system_code
    `, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list business systems: %w", err)
	}
	defer rows.Close()
	items := make([]BusinessSystem, 0)
	for rows.Next() {
		var item BusinessSystem
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.ShortName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.OwnerName, &item.OwnerContact, &item.Status, &item.Level, &item.DeploymentType,
			&item.InternetExposure, &item.Description, &item.ContainsPersonalInformation,
			&item.ContainsSensitivePersonalInformation, &item.PersonalInformationRecordCount,
			&item.DataVolumeOver100K, &item.ContainsImportantData, &item.ContainsCoreData,
			&item.DataStorageLocation, &item.SecurityNotes, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan business system: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate business systems: %w", err)
	}
	return items, nil
}

// Search returns only one database page of systems while retaining the real
// count for the caller's visible data scope.
func (s *Service) Search(ctx context.Context, principal auth.Principal, query pagination.Query) (pagination.Result[BusinessSystem], error) {
	return s.SearchWithFilter(ctx, principal, SearchFilter{Page: query})
}

// SearchWithFilter returns only one filtered database page of systems while
// retaining the real count for the caller's visible data scope.
func (s *Service) SearchWithFilter(ctx context.Context, principal auth.Principal, filter SearchFilter) (pagination.Result[BusinessSystem], error) {
	if !principal.HasPermission("system.manage") {
		return pagination.Result[BusinessSystem]{}, ErrForbidden
	}
	query := filter.Page
	rows, err := s.pool.Query(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'system.manage'
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
        ), accessible(id) AS (
            SELECT ur.organization_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
			JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'system.manage'
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION SELECT id FROM descendants
        )
        SELECT bs.id::text, bs.system_code, bs.system_name, COALESCE(bs.short_name, ''),
               o.id::text, o.organization_code, o.organization_name,
               COALESCE(bs.owner_name, ''), COALESCE(bs.owner_contact, ''), bs.system_status, bs.system_level,
               COALESCE(bs.deployment_type, ''), bs.internet_exposure, COALESCE(bs.description, ''),
               p.contains_personal_information, p.contains_sensitive_personal_information,
               p.personal_information_record_count, p.data_volume_over_100k,
               p.contains_important_data, p.contains_core_data, COALESCE(p.data_storage_location, ''), COALESCE(p.notes, ''),
               bs.created_at, bs.updated_at, count(*) OVER ()
        FROM business_systems bs
        JOIN organizations o ON o.id = bs.owner_organization_id
        JOIN business_system_data_security_profiles p ON p.business_system_id = bs.id
        WHERE bs.deleted_at IS NULL
          AND ($2::boolean OR bs.owner_organization_id IN (SELECT id FROM accessible))
          AND ($3 = '' OR bs.owner_organization_id::text = $3)
          AND ($4 = '' OR bs.system_status = $4)
          AND ($5 = '' OR concat_ws(' ', bs.system_code, bs.system_name, bs.short_name, o.organization_name, bs.owner_name, bs.owner_contact) ILIKE '%' || $5 || '%')
        ORDER BY o.organization_code, bs.system_code
        LIMIT $6 OFFSET $7
    `, principal.UserID, principal.HasRole("group_admin"), strings.TrimSpace(filter.OrganizationID), strings.TrimSpace(filter.Status), strings.TrimSpace(filter.Keyword), query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("search business systems: %w", err)
	}
	defer rows.Close()
	items := make([]BusinessSystem, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item BusinessSystem
		if err := rows.Scan(&item.ID, &item.Code, &item.Name, &item.ShortName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.OwnerName, &item.OwnerContact, &item.Status, &item.Level, &item.DeploymentType,
			&item.InternetExposure, &item.Description, &item.ContainsPersonalInformation,
			&item.ContainsSensitivePersonalInformation, &item.PersonalInformationRecordCount,
			&item.DataVolumeOver100K, &item.ContainsImportantData, &item.ContainsCoreData,
			&item.DataStorageLocation, &item.SecurityNotes, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[BusinessSystem]{}, fmt.Errorf("scan business system page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("iterate business system page: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
            WITH RECURSIVE descendant_roots(id) AS (
                SELECT ur.organization_id FROM user_roles ur
                JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
                JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'system.manage'
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots
                UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur
                JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
                JOIN role_permissions rp ON rp.role_id = r.id AND rp.permission_key = 'system.manage'
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
                  AND ur.data_scope IN ('organization', 'organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*)
            FROM business_systems bs
            JOIN organizations o ON o.id = bs.owner_organization_id
            JOIN business_system_data_security_profiles p ON p.business_system_id = bs.id
            WHERE bs.deleted_at IS NULL
              AND ($2::boolean OR bs.owner_organization_id IN (SELECT id FROM accessible))
              AND ($3 = '' OR bs.owner_organization_id::text = $3)
              AND ($4 = '' OR bs.system_status = $4)
              AND ($5 = '' OR concat_ws(' ', bs.system_code, bs.system_name, bs.short_name, o.organization_name, bs.owner_name, bs.owner_contact) ILIKE '%' || $5 || '%')
        `, principal.UserID, principal.HasRole("group_admin"), strings.TrimSpace(filter.OrganizationID), strings.TrimSpace(filter.Status), strings.TrimSpace(filter.Keyword)).Scan(&total); err != nil {
			return pagination.Result[BusinessSystem]{}, fmt.Errorf("count business systems: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Export(ctx context.Context, principal auth.Principal, format, requestID, ipAddress string) ([]byte, string, string, int, error) {
	items, err := s.List(ctx, principal)
	if err != nil {
		return nil, "", "", 0, err
	}
	customService := customfield.NewService(s.pool)
	definitions, err := customService.EnabledDefinitions(ctx, "business_system")
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("load business system custom field definitions: %w", err)
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	customValues, err := customService.ValuesForResources(ctx, "business_system", ids)
	if err != nil {
		return nil, "", "", 0, fmt.Errorf("load business system custom field values: %w", err)
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
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'business_system.export', 'business_system', '导出业务系统台账',
		        jsonb_build_object('format', $3::text, 'count', $4::int), NULLIF($5, '')::inet, NULLIF($6, ''))
	`, principal.UserID, organizationID, strings.ToLower(strings.TrimPrefix(format, ".")), len(items), ipAddress, requestID); err != nil {
		return nil, "", "", 0, fmt.Errorf("write business system export audit: %w", err)
	}
	return data, contentType, fileName, len(items), nil
}

func buildExport(items []BusinessSystem, format string) ([]byte, string, string, error) {
	return buildExportWithCustomFields(items, nil, nil, format)
}

func buildExportWithCustomFields(items []BusinessSystem, definitions []customfield.Definition, values map[string]map[string]any, format string) ([]byte, string, string, error) {
	headers := []string{"所属组织编码", "所属组织名称", "系统编码", "系统名称", "系统简称", "运行状态", "系统等级", "负责人", "联系方式", "互联网暴露情况", "部署方式", "系统说明", "是否包含个人信息", "是否包含敏感个人信息", "个人信息记录数", "数据量是否超过10万条", "是否包含重要数据", "是否包含核心数据", "数据存储位置", "数据安全备注", "创建时间", "更新时间"}
	for _, definition := range definitions {
		headers = append(headers, "自定义："+definition.FieldName)
	}
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		recordCount := ""
		if item.PersonalInformationRecordCount != nil {
			recordCount = strconv.FormatInt(*item.PersonalInformationRecordCount, 10)
		}
		row := []string{
			item.OrganizationCode, item.OrganizationName, item.Code, item.Name, item.ShortName,
			businessSystemExportLabel("status", item.Status), businessSystemExportLabel("level", item.Level), item.OwnerName, item.OwnerContact,
			businessSystemExportLabel("yes_no", item.InternetExposure), item.DeploymentType, item.Description,
			businessSystemExportLabel("yes_no", item.ContainsPersonalInformation), businessSystemExportLabel("yes_no", item.ContainsSensitivePersonalInformation),
			recordCount, businessSystemExportLabel("yes_no", item.DataVolumeOver100K), businessSystemExportLabel("classification", item.ContainsImportantData),
			businessSystemExportLabel("classification", item.ContainsCoreData), item.DataStorageLocation, item.SecurityNotes,
			formatExportTime(item.CreatedAt), formatExportTime(item.UpdatedAt),
		}
		for _, definition := range definitions {
			row = append(row, customfield.ExportValue(definition, values[item.ID][definition.FieldKey]))
		}
		rows = append(rows, row)
	}
	data, contentType, fileName, err := exportfile.Build(format, "业务系统台账", headers, rows)
	if errors.Is(err, exportfile.ErrInvalidFormat) {
		return nil, "", "", ErrInvalid
	}
	return data, contentType, fileName, err
}

func businessSystemExportLabel(kind, value string) string {
	labels := map[string]map[string]string{
		"status":         {"planning": "规划中", "running": "运行中", "suspended": "暂停", "offline": "已下线", "archived": "已归档"},
		"level":          {"normal": "一般", "important": "重要", "core": "核心"},
		"yes_no":         {"yes": "是", "no": "否", "partial": "部分", "unknown": "待确认"},
		"classification": {"yes": "是", "no": "否", "pending": "待认定", "unknown": "待确认"},
	}
	if label, ok := labels[kind][value]; ok {
		return label
	}
	return value
}

func formatExportTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05Z07:00")
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (BusinessSystem, error) {
	return s.create(ctx, principal, input, requestID, ipAddress, nil)
}

func (s *Service) create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string, afterCreate func(pgx.Tx, BusinessSystem) error) (BusinessSystem, error) {
	if !principal.HasPermission("system.manage") {
		return BusinessSystem{}, ErrForbidden
	}
	normalize(&input)
	if !validInput(input) {
		return BusinessSystem{}, ErrInvalid
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, input.OrganizationID, "system.manage")
	if err != nil {
		return BusinessSystem{}, err
	}
	if !allowed {
		return BusinessSystem{}, ErrForbidden
	}

	if input.PersonalInformationRecordCount != nil {
		if *input.PersonalInformationRecordCount >= 100000 {
			input.DataVolumeOver100K = "yes"
		} else {
			input.DataVolumeOver100K = "no"
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("begin business system: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var organizationCode, organizationName, organizationStatus string
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id = $1::uuid`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystem{}, ErrNotFound
	} else if err != nil {
		return BusinessSystem{}, fmt.Errorf("load system organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return BusinessSystem{}, ErrInvalid
	}

	var item BusinessSystem
	err = tx.QueryRow(ctx, `
        INSERT INTO business_systems (
            system_code, system_name, short_name, owner_organization_id, owner_name, owner_contact,
            system_status, system_level, deployment_type, internet_exposure, description, created_by, updated_by
        ) VALUES ($1, $2, NULLIF($3, ''), $4::uuid, NULLIF($5, ''), NULLIF($6, ''), $7, $8,
                  NULLIF($9, ''), $10, NULLIF($11, ''), $12::uuid, $12::uuid)
        RETURNING id::text, created_at, updated_at
    `, input.Code, input.Name, input.ShortName, input.OrganizationID, input.OwnerName, input.OwnerContact,
		input.Status, input.Level, input.DeploymentType, input.InternetExposure, input.Description, principal.UserID).
		Scan(&item.ID, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return BusinessSystem{}, ErrConflict
		}
		return BusinessSystem{}, fmt.Errorf("insert business system: %w", err)
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO business_system_data_security_profiles (
            business_system_id, contains_personal_information, contains_sensitive_personal_information,
            personal_information_record_count, data_volume_over_100k, contains_important_data, contains_core_data,
            data_storage_location, reported_by, notes
        ) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9::uuid, NULLIF($10, ''))
    `, item.ID, input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation,
		input.PersonalInformationRecordCount, input.DataVolumeOver100K, input.ContainsImportantData,
		input.ContainsCoreData, input.DataStorageLocation, principal.UserID, input.SecurityNotes); err != nil {
		return BusinessSystem{}, fmt.Errorf("insert data security profile: %w", err)
	}
	if afterCreate != nil {
		if err := afterCreate(tx, item); err != nil {
			return BusinessSystem{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
        INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
        VALUES ($1::uuid, $2::uuid, 'business_system.create', 'business_system', $3, '新增业务系统',
                jsonb_build_object('code', $4::text, 'name', $5::text, 'contains_personal_information', $6::text,
                                   'data_volume_over_100k', $7::text), NULLIF($8, '')::inet, NULLIF($9, ''))
    `, principal.UserID, input.OrganizationID, item.ID, input.Code, input.Name, input.ContainsPersonalInformation,
		input.DataVolumeOver100K, ipAddress, requestID); err != nil {
		return BusinessSystem{}, fmt.Errorf("write business system audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystem{}, fmt.Errorf("commit business system: %w", err)
	}

	item.Code, item.Name, item.ShortName = input.Code, input.Name, input.ShortName
	item.OrganizationID, item.OrganizationCode, item.OrganizationName = input.OrganizationID, organizationCode, organizationName
	item.OwnerName, item.OwnerContact, item.Status, item.Level = input.OwnerName, input.OwnerContact, input.Status, input.Level
	item.DeploymentType, item.InternetExposure, item.Description = input.DeploymentType, input.InternetExposure, input.Description
	item.ContainsPersonalInformation, item.ContainsSensitivePersonalInformation = input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation
	item.PersonalInformationRecordCount, item.DataVolumeOver100K = input.PersonalInformationRecordCount, input.DataVolumeOver100K
	item.ContainsImportantData, item.ContainsCoreData = input.ContainsImportantData, input.ContainsCoreData
	item.DataStorageLocation, item.SecurityNotes = input.DataStorageLocation, input.SecurityNotes
	return item, nil
}

// RelatedAsset is the compact asset representation shown on the business-system detail page.
type RelatedAsset struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Value          string `json:"value"`
	Name           string `json:"name,omitempty"`
	OrganizationID string `json:"organization_id"`
	Status         string `json:"status"`
	Importance     string `json:"importance"`
	NetworkScope   string `json:"network_scope"`
	OwnerName      string `json:"owner_name,omitempty"`
}

type Detail struct {
	BusinessSystem
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, systemID string) (Detail, error) {
	if !principal.HasPermission("system.manage") {
		return Detail{}, ErrForbidden
	}
	systemID = strings.TrimSpace(systemID)
	if systemID == "" {
		return Detail{}, ErrInvalid
	}
	item, err := queryBusinessSystem(ctx, s.pool, systemID, false)
	if err != nil {
		return Detail{}, err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, item.OrganizationID, "system.manage")
	if err != nil {
		return Detail{}, err
	}
	if !allowed {
		return Detail{}, ErrForbidden
	}
	return Detail{BusinessSystem: item}, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, systemID string, input CreateInput, requestID, ipAddress string) (BusinessSystem, error) {
	if !principal.HasPermission("system.manage") {
		return BusinessSystem{}, ErrForbidden
	}
	systemID = strings.TrimSpace(systemID)
	if systemID == "" {
		return BusinessSystem{}, ErrInvalid
	}
	normalize(&input)
	if !validInput(input) {
		return BusinessSystem{}, ErrInvalid
	}
	if input.PersonalInformationRecordCount != nil {
		if *input.PersonalInformationRecordCount >= 100000 {
			input.DataVolumeOver100K = "yes"
		} else {
			input.DataVolumeOver100K = "no"
		}
	}
	current, err := queryBusinessSystem(ctx, s.pool, systemID, false)
	if err != nil {
		return BusinessSystem{}, err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, current.OrganizationID, "system.manage")
	if err != nil {
		return BusinessSystem{}, err
	}
	if !allowed {
		return BusinessSystem{}, ErrForbidden
	}
	allowed, err = access.CanOperateOrganization(ctx, s.pool, principal, input.OrganizationID, "system.manage")
	if err != nil {
		return BusinessSystem{}, err
	}
	if !allowed {
		return BusinessSystem{}, ErrForbidden
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("begin business system update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var organizationCode, organizationName, organizationStatus string
	if err := tx.QueryRow(ctx, `SELECT organization_code, organization_name, status FROM organizations WHERE id = $1::uuid`, input.OrganizationID).
		Scan(&organizationCode, &organizationName, &organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystem{}, ErrNotFound
	} else if err != nil {
		return BusinessSystem{}, fmt.Errorf("load updated system organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return BusinessSystem{}, ErrInvalid
	}
	if current.OrganizationID != input.OrganizationID {
		var linkedAssets int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM system_assets sa JOIN assets a ON a.id = sa.asset_id WHERE sa.business_system_id = $1::uuid AND a.deleted_at IS NULL`, systemID).Scan(&linkedAssets); err != nil {
			return BusinessSystem{}, fmt.Errorf("check linked assets: %w", err)
		}
		if linkedAssets > 0 {
			return BusinessSystem{}, ErrInUse
		}
	}
	_, err = tx.Exec(ctx, `
		UPDATE business_systems SET system_code=$1, system_name=$2, short_name=NULLIF($3,''),
			owner_organization_id=$4::uuid, owner_name=NULLIF($5,''), owner_contact=NULLIF($6,''),
			system_status=$7, system_level=$8, deployment_type=NULLIF($9,''), internet_exposure=$10,
			description=NULLIF($11,''), updated_by=$12::uuid, updated_at=now()
		WHERE id=$13::uuid AND deleted_at IS NULL
	`, input.Code, input.Name, input.ShortName, input.OrganizationID, input.OwnerName, input.OwnerContact,
		input.Status, input.Level, input.DeploymentType, input.InternetExposure, input.Description, principal.UserID, systemID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return BusinessSystem{}, ErrConflict
		}
		return BusinessSystem{}, fmt.Errorf("update business system: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE business_system_data_security_profiles SET contains_personal_information=$1,
			contains_sensitive_personal_information=$2, personal_information_record_count=$3,
			data_volume_over_100k=$4, contains_important_data=$5, contains_core_data=$6,
			data_storage_location=NULLIF($7,''), reported_by=$8::uuid, notes=NULLIF($9,''), updated_at=now()
		WHERE business_system_id=$10::uuid
	`, input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation, input.PersonalInformationRecordCount,
		input.DataVolumeOver100K, input.ContainsImportantData, input.ContainsCoreData, input.DataStorageLocation,
		principal.UserID, input.SecurityNotes, systemID)
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("update business system security profile: %w", err)
	}
	changesJSON, err := json.Marshal(businessSystemFieldChanges(current, input, organizationName))
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("encode business system changes: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'business_system.update', 'business_system', $3, '编辑业务系统',
			jsonb_build_object('changes', $4::jsonb), NULLIF($5,'')::inet, NULLIF($6,''))
	`, principal.UserID, input.OrganizationID, systemID, changesJSON, ipAddress, requestID); err != nil {
		return BusinessSystem{}, fmt.Errorf("write business system update audit: %w", err)
	}
	item, err := queryBusinessSystem(ctx, tx, systemID, false)
	if err != nil {
		return BusinessSystem{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystem{}, fmt.Errorf("commit business system update: %w", err)
	}
	item.AssetCount = current.AssetCount
	return item, nil
}

func (s *Service) SetArchived(ctx context.Context, principal auth.Principal, systemID string, archived bool, requestID, ipAddress string) (BusinessSystem, error) {
	if !principal.HasPermission("system.manage") {
		return BusinessSystem{}, ErrForbidden
	}
	systemID = strings.TrimSpace(systemID)
	if systemID == "" {
		return BusinessSystem{}, ErrInvalid
	}
	current, err := queryBusinessSystem(ctx, s.pool, systemID, false)
	if err != nil {
		return BusinessSystem{}, err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, current.OrganizationID, "system.manage")
	if err != nil {
		return BusinessSystem{}, err
	}
	if !allowed {
		return BusinessSystem{}, ErrForbidden
	}
	status := "running"
	action, summary := "business_system.restore", "恢复业务系统"
	if archived {
		status = "archived"
		action, summary = "business_system.archive", "归档业务系统"
	}
	_, err = s.pool.Exec(ctx, `UPDATE business_systems SET system_status=$1, updated_by=$2::uuid, updated_at=now() WHERE id=$3::uuid AND deleted_at IS NULL`, status, principal.UserID, systemID)
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("set business system status: %w", err)
	}
	changesJSON, err := json.Marshal([]fieldChange{{Field: "system_status", Label: "运行状态", Before: businessSystemStatusLabel(current.Status), After: businessSystemStatusLabel(status)}})
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("encode business system status changes: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, $3, 'business_system', $4, $5, jsonb_build_object('changes', $6::jsonb), NULLIF($7,'')::inet, NULLIF($8,''))
	`, principal.UserID, current.OrganizationID, action, systemID, summary, changesJSON, ipAddress, requestID); err != nil {
		return BusinessSystem{}, fmt.Errorf("write business system status audit: %w", err)
	}
	item, err := queryBusinessSystem(ctx, s.pool, systemID, false)
	if err != nil {
		return BusinessSystem{}, err
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, systemID, requestID, ipAddress string) error {
	if !principal.HasPermission("system.manage") {
		return ErrForbidden
	}
	systemID = strings.TrimSpace(systemID)
	if systemID == "" {
		return ErrInvalid
	}
	current, err := queryBusinessSystem(ctx, s.pool, systemID, false)
	if err != nil {
		return err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, current.OrganizationID, "system.manage")
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	var linkedAssets int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM system_assets sa JOIN assets a ON a.id=sa.asset_id WHERE sa.business_system_id=$1::uuid AND a.deleted_at IS NULL`, systemID).Scan(&linkedAssets); err != nil {
		return fmt.Errorf("check business system assets: %w", err)
	}
	if linkedAssets > 0 {
		return ErrInUse
	}
	_, err = s.pool.Exec(ctx, `UPDATE business_systems SET deleted_at=now(), system_status='archived', updated_by=$1::uuid, updated_at=now() WHERE id=$2::uuid AND deleted_at IS NULL`, principal.UserID, systemID)
	if err != nil {
		return fmt.Errorf("delete business system: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'business_system.delete', 'business_system', $3, '删除业务系统', '{}'::jsonb, NULLIF($4,'')::inet, NULLIF($5,''))
	`, principal.UserID, current.OrganizationID, systemID, ipAddress, requestID)
	return err
}

func businessSystemFieldChanges(current BusinessSystem, input CreateInput, organizationName string) []fieldChange {
	changes := make([]fieldChange, 0, 19)
	add := func(field, label, before, after string) {
		if before != after {
			changes = append(changes, fieldChange{Field: field, Label: label, Before: before, After: after})
		}
	}
	add("system_code", "系统编码", businessSystemDisplay(current.Code, "未填写"), businessSystemDisplay(input.Code, "未填写"))
	add("system_name", "系统名称", businessSystemDisplay(current.Name, "未填写"), businessSystemDisplay(input.Name, "未填写"))
	add("short_name", "系统简称", businessSystemDisplay(current.ShortName, "未填写"), businessSystemDisplay(input.ShortName, "未填写"))
	add("organization", "所属组织", businessSystemDisplay(current.OrganizationName, "未关联"), businessSystemDisplay(organizationName, "未关联"))
	add("owner_name", "负责人", businessSystemDisplay(current.OwnerName, "未填写"), businessSystemDisplay(input.OwnerName, "未填写"))
	add("owner_contact", "联系方式", businessSystemDisplay(current.OwnerContact, "未填写"), businessSystemDisplay(input.OwnerContact, "未填写"))
	add("system_status", "运行状态", businessSystemStatusLabel(current.Status), businessSystemStatusLabel(input.Status))
	add("system_level", "系统等级", businessSystemLevelLabel(current.Level), businessSystemLevelLabel(input.Level))
	add("deployment_type", "部署方式", businessSystemDisplay(current.DeploymentType, "未填写"), businessSystemDisplay(input.DeploymentType, "未填写"))
	add("internet_exposure", "互联网暴露", businessSystemYesNoLabel(current.InternetExposure), businessSystemYesNoLabel(input.InternetExposure))
	add("description", "系统说明", businessSystemDisplay(current.Description, "未填写"), businessSystemDisplay(input.Description, "未填写"))
	add("contains_personal_information", "包含个人信息", businessSystemYesNoLabel(current.ContainsPersonalInformation), businessSystemYesNoLabel(input.ContainsPersonalInformation))
	add("contains_sensitive_personal_information", "包含敏感个人信息", businessSystemYesNoLabel(current.ContainsSensitivePersonalInformation), businessSystemYesNoLabel(input.ContainsSensitivePersonalInformation))
	add("personal_information_record_count", "个人信息记录数", businessSystemCount(current.PersonalInformationRecordCount), businessSystemCount(input.PersonalInformationRecordCount))
	add("data_volume_over_100k", "数据量是否超过 10 万条", businessSystemYesNoLabel(current.DataVolumeOver100K), businessSystemYesNoLabel(input.DataVolumeOver100K))
	add("contains_important_data", "包含重要数据", businessSystemYesNoLabel(current.ContainsImportantData), businessSystemYesNoLabel(input.ContainsImportantData))
	add("contains_core_data", "包含核心数据", businessSystemYesNoLabel(current.ContainsCoreData), businessSystemYesNoLabel(input.ContainsCoreData))
	add("data_storage_location", "数据存储位置", businessSystemDisplay(current.DataStorageLocation, "未填写"), businessSystemDisplay(input.DataStorageLocation, "未填写"))
	add("security_notes", "数据安全备注", businessSystemDisplay(current.SecurityNotes, "未填写"), businessSystemDisplay(input.SecurityNotes, "未填写"))
	return changes
}

func businessSystemDisplay(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func businessSystemCount(value *int64) string {
	if value == nil {
		return "未填写"
	}
	return strconv.FormatInt(*value, 10)
}

func businessSystemStatusLabel(value string) string {
	if label := map[string]string{"planning": "规划中", "running": "运行中", "suspended": "暂停", "offline": "已下线", "archived": "已归档"}[value]; label != "" {
		return label
	}
	return businessSystemDisplay(value, "待确认")
}

func businessSystemLevelLabel(value string) string {
	if label := map[string]string{"normal": "一般", "important": "重要", "core": "核心"}[value]; label != "" {
		return label
	}
	return businessSystemDisplay(value, "待确认")
}

func businessSystemYesNoLabel(value string) string {
	if label := map[string]string{"yes": "是", "no": "否", "unknown": "待确认", "pending": "待认定", "public": "公网", "private": "内网", "partial": "部分"}[value]; label != "" {
		return label
	}
	return businessSystemDisplay(value, "待确认")
}

func queryBusinessSystem(ctx context.Context, querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, systemID string, includeDeleted bool) (BusinessSystem, error) {
	var item BusinessSystem
	deletedFilter := "AND bs.deleted_at IS NULL"
	if includeDeleted {
		deletedFilter = ""
	}
	err := querier.QueryRow(ctx, fmt.Sprintf(`
		SELECT bs.id::text, bs.system_code, bs.system_name, COALESCE(bs.short_name,''),
			o.id::text, o.organization_code, o.organization_name, COALESCE(bs.owner_name,''), COALESCE(bs.owner_contact,''),
			bs.system_status, bs.system_level, COALESCE(bs.deployment_type,''), bs.internet_exposure, COALESCE(bs.description,''),
			p.contains_personal_information, p.contains_sensitive_personal_information, p.personal_information_record_count,
			p.data_volume_over_100k, p.contains_important_data, p.contains_core_data, COALESCE(p.data_storage_location,''), COALESCE(p.notes,''),
			(SELECT count(*) FROM system_assets sa JOIN assets a ON a.id = sa.asset_id AND a.deleted_at IS NULL WHERE sa.business_system_id = bs.id),
			bs.created_at, bs.updated_at
		FROM business_systems bs JOIN organizations o ON o.id=bs.owner_organization_id
		JOIN business_system_data_security_profiles p ON p.business_system_id=bs.id
		WHERE bs.id::text=$1 %s
	`, deletedFilter), systemID).Scan(
		&item.ID, &item.Code, &item.Name, &item.ShortName, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
		&item.OwnerName, &item.OwnerContact, &item.Status, &item.Level, &item.DeploymentType, &item.InternetExposure,
		&item.Description, &item.ContainsPersonalInformation, &item.ContainsSensitivePersonalInformation, &item.PersonalInformationRecordCount,
		&item.DataVolumeOver100K, &item.ContainsImportantData, &item.ContainsCoreData, &item.DataStorageLocation, &item.SecurityNotes,
		&item.AssetCount, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystem{}, ErrNotFound
	}
	if err != nil {
		return BusinessSystem{}, fmt.Errorf("load business system: %w", err)
	}
	return item, nil
}

// SearchRelatedAssets returns one bounded page of assets related to a business
// system. Get is intentionally called first so existence, permission and
// organization-scope checks remain identical to the detail endpoint.
func (s *Service) SearchRelatedAssets(ctx context.Context, principal auth.Principal, systemID string, page pagination.Query) (pagination.Result[RelatedAsset], error) {
	if _, err := s.Get(ctx, principal, systemID); err != nil {
		return pagination.Result[RelatedAsset]{}, err
	}

	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM system_assets sa
		JOIN assets a ON a.id = sa.asset_id AND a.deleted_at IS NULL
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		WHERE sa.business_system_id = $1::uuid
	`, systemID).Scan(&total); err != nil {
		return pagination.Result[RelatedAsset]{}, fmt.Errorf("count related assets: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT a.id::text, a.asset_type, a.asset_value, COALESCE(a.asset_name,''), ao.organization_id::text,
			a.status, a.importance, a.network_scope, COALESCE(ao.owner_name,'')
		FROM system_assets sa
		JOIN assets a ON a.id = sa.asset_id AND a.deleted_at IS NULL
		JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
		WHERE sa.business_system_id = $1::uuid
		ORDER BY a.asset_type, a.asset_value, a.id
		LIMIT $2 OFFSET $3
	`, systemID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[RelatedAsset]{}, fmt.Errorf("list related assets: %w", err)
	}
	defer rows.Close()
	items := make([]RelatedAsset, 0, page.PageSize)
	for rows.Next() {
		var item RelatedAsset
		if err := rows.Scan(&item.ID, &item.Type, &item.Value, &item.Name, &item.OrganizationID, &item.Status, &item.Importance, &item.NetworkScope, &item.OwnerName); err != nil {
			return pagination.Result[RelatedAsset]{}, fmt.Errorf("scan related asset: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[RelatedAsset]{}, fmt.Errorf("iterate related assets: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

// Template returns the standard business-system import template.
func Template(format string) ([]byte, string, string, error) { return buildTemplate(format, nil) }

// Template builds a template scoped to an organization and its maintenance permission.
func (s *Service) Template(ctx context.Context, principal auth.Principal, organizationID, format string) ([]byte, string, string, error) {
	definitions, err := customfield.NewService(s.pool).ActiveDefinitionsForOrganization(ctx, principal, "business_system", organizationID)
	if err != nil {
		return nil, "", "", err
	}
	return buildTemplate(format, definitions)
}

func buildTemplate(format string, definitions []customfield.Definition) ([]byte, string, string, error) {
	headers := []string{"系统编码", "系统名称", "系统简称", "运行状态", "系统等级", "负责人", "联系方式", "互联网暴露情况", "部署方式", "系统说明", "是否包含个人信息", "是否包含敏感个人信息", "个人信息记录数", "数据量是否超过10万条", "是否包含重要数据", "是否包含核心数据", "数据存储位置", "数据安全备注"}
	example := []string{"CRM_001", "客户关系管理系统", "CRM", "运行中", "一般", "张三", "13800000000", "待确认", "私有云", "集团客户管理系统", "是", "否", "50000", "否", "待确认", "待确认", "集团私有云", "待完善安全分类"}
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
		return buffer.Bytes(), "text/csv; charset=utf-8", "业务系统导入模板.csv", nil
	case "xlsx", "":
		file := excelize.NewFile()
		defer file.Close()
		sheet := file.GetSheetName(0)
		for i, value := range headers {
			cell, _ := excelize.CoordinatesToCellName(i+1, 1)
			_ = file.SetCellValue(sheet, cell, value)
		}
		for i, value := range example {
			cell, _ := excelize.CoordinatesToCellName(i+1, 2)
			_ = file.SetCellValue(sheet, cell, value)
		}
		_ = file.SetColWidth(sheet, "A", "B", 20)
		_ = file.SetColWidth(sheet, "C", "Z", 18)
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
		return buffer.Bytes(), "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "业务系统导入模板.xlsx", nil
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
	case "select", "multiselect":
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

func (s *Service) Import(ctx context.Context, principal auth.Principal, organizationID, fileName string, reader io.Reader, requestID, ipAddress string) (ImportResult, error) {
	if !principal.HasPermission("system.manage") {
		return ImportResult{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, "system.manage")
	if err != nil {
		return ImportResult{}, err
	}
	if !allowed {
		return ImportResult{}, ErrForbidden
	}
	rows, fileType, err := readImportRows(fileName, reader)
	if err != nil || len(rows) < 2 || len(rows)-1 > MaxImportRows {
		return ImportResult{}, ErrInvalid
	}
	headers := mapHeaders(rows[0])
	if headers["code"] < 0 || headers["name"] < 0 {
		return ImportResult{}, ErrInvalid
	}
	customService := customfield.NewService(s.pool)
	definitions, err := customService.ActiveDefinitionsForOrganization(ctx, principal, "business_system", organizationID)
	if err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{FileName: filepath.Base(fileName), FileType: fileType, Errors: make([]ImportError, 0), TotalCount: len(rows) - 1}
	for index, row := range rows[1:] {
		rowNumber := index + 2
		input, rowErr := importInput(row, headers, organizationID)
		if rowErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: rowErr.code, Message: rowErr.message})
			continue
		}
		customValues, customErr := customfield.ImportValues(definitions, rows[0], row)
		if customErr != nil {
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid_custom_field", Message: customErr.Error()})
			continue
		}
		_, err := s.create(ctx, principal, input, requestID, ipAddress, func(tx pgx.Tx, item BusinessSystem) error {
			return customfield.SaveImportedValuesInTx(ctx, tx, "business_system", item.ID, definitions, customValues, principal.UserID)
		})
		switch {
		case err == nil:
			result.SuccessCount++
		case errors.Is(err, ErrConflict):
			result.DuplicateCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "duplicate", Message: "系统编码已存在，未覆盖原记录"})
		case errors.Is(err, ErrForbidden):
			return ImportResult{}, ErrForbidden
		case errors.Is(err, ErrNotFound):
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "organization_not_found", Message: "所属组织不存在或已停用"})
		case errors.Is(err, ErrInvalid):
			result.FailureCount++
			result.Errors = append(result.Errors, ImportError{Row: rowNumber, Code: "invalid", Message: "业务系统字段格式不正确"})
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
		return "", fmt.Errorf("encode business system import errors: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin business system import result: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var importID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO business_system_imports (organization_id, file_name, file_type, status, total_count, success_count, failure_count, duplicate_count, errors, created_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9::jsonb, $10::uuid) RETURNING id::text
	`, organizationID, result.FileName, result.FileType, result.Status, result.TotalCount, result.SuccessCount, result.FailureCount, result.DuplicateCount, string(errorsJSON), principal.UserID).Scan(&importID); err != nil {
		return "", fmt.Errorf("save business system import result: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, $2::uuid, 'business_system.import', 'business_system_import', $3, '批量导入业务系统',
		        jsonb_build_object('file_name', $4::text, 'total', $5::int, 'success', $6::int, 'failure', $7::int, 'duplicate', $8::int), NULLIF($9, '')::inet, NULLIF($10, ''))
	`, principal.UserID, organizationID, importID, result.FileName, result.TotalCount, result.SuccessCount, result.FailureCount, result.DuplicateCount, ipAddress, requestID); err != nil {
		return "", fmt.Errorf("write business system import audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit business system import result: %w", err)
	}
	return importID, nil
}

func (s *Service) ListImports(ctx context.Context, principal auth.Principal) ([]ImportHistory, error) {
	result, err := s.SearchImports(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) SearchImports(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[ImportHistory], error) {
	if !principal.HasPermission("system.manage") {
		return pagination.Result[ImportHistory]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='system.manage'
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='system.manage'
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT i.id::text,i.file_name,i.file_type,i.status,i.total_count,i.success_count,i.failure_count,i.duplicate_count,i.errors::text,
		       o.id::text,o.organization_code,o.organization_name,COALESCE(u.display_name,''),i.created_at,count(*) OVER ()
		FROM business_system_imports i JOIN organizations o ON o.id=i.organization_id LEFT JOIN users u ON u.id=i.created_by
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
		if err := rows.Scan(&item.ID, &item.FileName, &item.FileType, &item.Status, &item.TotalCount, &item.SuccessCount, &item.FailureCount, &item.DuplicateCount, &errorsText, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.CreatedByName, &item.CreatedAt, &total); err != nil {
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
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='system.manage'
                WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='system.manage'
                WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*) FROM business_system_imports i JOIN organizations o ON o.id=i.organization_id
            WHERE $2::boolean OR o.id IN (SELECT id FROM accessible)
        `, principal.UserID, principal.HasRole("group_admin")).Scan(&total); err != nil {
			return pagination.Result[ImportHistory]{}, fmt.Errorf("count imports: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

type importRowError struct{ code, message string }

func readImportRows(fileName string, reader io.Reader) ([][]string, string, error) {
	switch strings.ToLower(filepath.Ext(fileName)) {
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
		"系统编码": "code", "code": "code", "system_code": "code",
		"系统名称": "name", "name": "name", "system_name": "name",
		"系统简称": "short_name", "short_name": "short_name",
		"运行状态": "status", "系统状态": "status", "status": "status",
		"系统等级": "level", "level": "level",
		"负责人": "owner_name", "系统负责人": "owner_name", "owner_name": "owner_name",
		"联系方式": "owner_contact", "owner_contact": "owner_contact",
		"互联网暴露情况": "internet_exposure", "互联网暴露": "internet_exposure", "internet_exposure": "internet_exposure",
		"部署方式": "deployment_type", "deployment_type": "deployment_type",
		"系统说明": "description", "系统描述": "description", "description": "description",
		"是否包含个人信息": "contains_personal_information", "contains_personal_information": "contains_personal_information",
		"是否包含敏感个人信息": "contains_sensitive_personal_information", "contains_sensitive_personal_information": "contains_sensitive_personal_information",
		"个人信息记录数": "personal_information_record_count", "personal_information_record_count": "personal_information_record_count",
		"数据量是否超过10万条": "data_volume_over_100k", "数据量是否超过 10 万条": "data_volume_over_100k", "data_volume_over_100k": "data_volume_over_100k",
		"是否包含重要数据": "contains_important_data", "contains_important_data": "contains_important_data",
		"是否包含核心数据": "contains_core_data", "contains_core_data": "contains_core_data",
		"数据存储位置": "data_storage_location", "data_storage_location": "data_storage_location",
		"数据安全备注": "security_notes", "security_notes": "security_notes",
	}
	keys := []string{"code", "name", "short_name", "status", "level", "owner_name", "owner_contact", "internet_exposure", "deployment_type", "description", "contains_personal_information", "contains_sensitive_personal_information", "personal_information_record_count", "data_volume_over_100k", "contains_important_data", "contains_core_data", "data_storage_location", "security_notes"}
	result := make(map[string]int, len(keys))
	for _, key := range keys {
		result[key] = -1
	}
	for i, header := range row {
		normalized := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(header, "\ufeff")))
		if key, ok := aliases[normalized]; ok {
			result[key] = i
		}
	}
	return result
}

func importInput(row []string, headers map[string]int, organizationID string) (CreateInput, *importRowError) {
	get := func(key string) string {
		i := headers[key]
		if i < 0 || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}
	input := CreateInput{Code: get("code"), Name: get("name"), ShortName: get("short_name"), OrganizationID: organizationID, OwnerName: get("owner_name"), OwnerContact: get("owner_contact"), Status: translateSystemStatus(get("status")), Level: translateSystemLevel(get("level")), DeploymentType: get("deployment_type"), InternetExposure: translateYesNo(get("internet_exposure")), Description: get("description"), ContainsPersonalInformation: translateYesNo(get("contains_personal_information")), ContainsSensitivePersonalInformation: translateYesNo(get("contains_sensitive_personal_information")), DataVolumeOver100K: translateYesNo(get("data_volume_over_100k")), ContainsImportantData: translateClassification(get("contains_important_data")), ContainsCoreData: translateClassification(get("contains_core_data")), DataStorageLocation: get("data_storage_location"), SecurityNotes: get("security_notes")}
	if input.Code == "" {
		return CreateInput{}, &importRowError{"missing_code", "系统编码不能为空"}
	}
	if input.Name == "" {
		return CreateInput{}, &importRowError{"missing_name", "系统名称不能为空"}
	}
	if value := get("personal_information_record_count"); value != "" {
		count, err := strconv.ParseInt(strings.ReplaceAll(value, ",", ""), 10, 64)
		if err != nil || count < 0 {
			return CreateInput{}, &importRowError{"invalid_record_count", "个人信息记录数必须是非负整数"}
		}
		input.PersonalInformationRecordCount = &count
	}
	if input.Status == "" {
		input.Status = "running"
	}
	if input.Level == "" {
		input.Level = "normal"
	}
	if input.InternetExposure == "" {
		input.InternetExposure = "unknown"
	}
	if input.ContainsPersonalInformation == "" {
		input.ContainsPersonalInformation = "unknown"
	}
	if input.ContainsSensitivePersonalInformation == "" {
		input.ContainsSensitivePersonalInformation = "unknown"
	}
	if input.DataVolumeOver100K == "" {
		input.DataVolumeOver100K = "unknown"
	}
	if input.ContainsImportantData == "" {
		input.ContainsImportantData = "unknown"
	}
	if input.ContainsCoreData == "" {
		input.ContainsCoreData = "unknown"
	}
	normalize(&input)
	if !validInput(input) {
		return CreateInput{}, &importRowError{"invalid_fields", "系统状态、等级或数据安全属性不合法"}
	}
	return input, nil
}

func translateSystemStatus(value string) string {
	return translateEnum(value, map[string]string{"规划中": "planning", "运行中": "running", "暂停": "suspended", "已下线": "offline", "已归档": "archived"})
}
func translateSystemLevel(value string) string {
	return translateEnum(value, map[string]string{"一般": "normal", "重要": "important", "核心": "core"})
}
func translateYesNo(value string) string {
	return translateEnum(value, map[string]string{"是": "yes", "否": "no", "部分": "partial", "待确认": "unknown", "待认定": "pending"})
}
func translateClassification(value string) string {
	return translateEnum(value, map[string]string{"是": "yes", "否": "no", "待确认": "unknown", "待认定": "pending"})
}
func translateEnum(value string, values map[string]string) string {
	value = strings.TrimSpace(value)
	if mapped, ok := values[value]; ok {
		return mapped
	}
	return strings.ToLower(value)
}

func normalize(input *CreateInput) {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.ShortName = strings.TrimSpace(input.ShortName)
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.OwnerName = strings.TrimSpace(input.OwnerName)
	input.OwnerContact = strings.TrimSpace(input.OwnerContact)
	input.Status = strings.TrimSpace(input.Status)
	input.Level = strings.TrimSpace(input.Level)
	input.DeploymentType = strings.TrimSpace(input.DeploymentType)
	input.InternetExposure = strings.TrimSpace(input.InternetExposure)
	input.Description = strings.TrimSpace(input.Description)
	input.ContainsPersonalInformation = strings.TrimSpace(input.ContainsPersonalInformation)
	input.ContainsSensitivePersonalInformation = strings.TrimSpace(input.ContainsSensitivePersonalInformation)
	input.DataVolumeOver100K = strings.TrimSpace(input.DataVolumeOver100K)
	input.ContainsImportantData = strings.TrimSpace(input.ContainsImportantData)
	input.ContainsCoreData = strings.TrimSpace(input.ContainsCoreData)
	input.DataStorageLocation = strings.TrimSpace(input.DataStorageLocation)
	input.SecurityNotes = strings.TrimSpace(input.SecurityNotes)
}
func validInput(input CreateInput) bool {
	if !codePattern.MatchString(input.Code) || input.Name == "" || len(input.Name) > 255 || input.OrganizationID == "" {
		return false
	}
	if !oneOf(input.Status, "planning", "running", "suspended", "offline", "archived") || !oneOf(input.Level, "normal", "important", "core") {
		return false
	}
	if !oneOf(input.InternetExposure, "yes", "no", "partial", "unknown") {
		return false
	}
	if !oneOf(input.ContainsPersonalInformation, "yes", "no", "unknown") || !oneOf(input.ContainsSensitivePersonalInformation, "yes", "no", "unknown") {
		return false
	}
	if input.ContainsPersonalInformation == "no" && input.ContainsSensitivePersonalInformation == "yes" {
		return false
	}
	if input.PersonalInformationRecordCount != nil && *input.PersonalInformationRecordCount < 0 {
		return false
	}
	if !oneOf(input.DataVolumeOver100K, "yes", "no", "unknown") {
		return false
	}
	if !oneOf(input.ContainsImportantData, "yes", "no", "unknown", "pending") || !oneOf(input.ContainsCoreData, "yes", "no", "unknown", "pending") {
		return false
	}
	return true
}
func oneOf(value string, values ...string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
