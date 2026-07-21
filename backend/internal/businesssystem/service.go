package businesssystem

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

	"netassets/internal/access"
	"netassets/internal/auth"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
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

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]BusinessSystem, error) {
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

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (BusinessSystem, error) {
	normalize(&input)
	if !validInput(input) {
		return BusinessSystem{}, ErrInvalid
	}
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, input.OrganizationID)
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
