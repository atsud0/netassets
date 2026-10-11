package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"netassets/internal/auth"
	"netassets/internal/businesssystem"
	"netassets/internal/customfield"
	"netassets/internal/notification"
	"netassets/internal/pagination"
)

type BusinessSystemDraftInput struct {
	businesssystem.CreateInput
	CustomValues []customfield.Value `json:"custom_values"`
}

type BusinessSystemDraft struct {
	businesssystem.BusinessSystem
	TaskID         string              `json:"task_id"`
	SourceSystemID string              `json:"source_system_id,omitempty"`
	DraftStatus    string              `json:"draft_status"`
	Operation      string              `json:"operation"`
	IsDraft        bool                `json:"is_draft"`
	CustomValues   []customfield.Value `json:"custom_values"`
}

func businessSystemDraftLabel(input businesssystem.CreateInput) string {
	if label := strings.TrimSpace(input.Name); label != "" {
		return label
	}
	return strings.TrimSpace(input.Code)
}

func (s *Service) SearchBusinessSystemDrafts(ctx context.Context, principal auth.Principal, taskID, organizationID string, page pagination.Query) (pagination.Result[BusinessSystemDraft], error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("system.manage") {
		return pagination.Result[BusinessSystemDraft]{}, ErrForbidden
	}
	if _, err := s.loadDraftAssignment(ctx, s.pool, principal, taskID, organizationID, false, "business_systems"); err != nil {
		return pagination.Result[BusinessSystemDraft]{}, err
	}
	visibility := " AND draft.draft_status <> 'discarded'"
	if principal.HasRole("group_admin") {
		visibility = " AND draft.draft_status IN ('submitted','published')"
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM inventory_business_system_drafts draft WHERE draft.task_id::text=$1 AND draft.organization_id::text=$2`+visibility, taskID, organizationID).Scan(&total); err != nil {
		return pagination.Result[BusinessSystemDraft]{}, fmt.Errorf("count inventory business system drafts: %w", err)
	}
	rows, err := s.pool.Query(ctx, businessSystemDraftSelect+` WHERE draft.task_id::text=$1 AND draft.organization_id::text=$2`+visibility+` ORDER BY draft.updated_at DESC,draft.id DESC LIMIT $3 OFFSET $4`, taskID, organizationID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[BusinessSystemDraft]{}, fmt.Errorf("list inventory business system drafts: %w", err)
	}
	defer rows.Close()
	items := make([]BusinessSystemDraft, 0, page.PageSize)
	for rows.Next() {
		item, err := scanBusinessSystemDraft(rows)
		if err != nil {
			return pagination.Result[BusinessSystemDraft]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[BusinessSystemDraft]{}, fmt.Errorf("iterate inventory business system drafts: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) CreateBusinessSystemDraft(ctx context.Context, principal auth.Principal, taskID, organizationID string, input BusinessSystemDraftInput, requestID, ipAddress string) (BusinessSystemDraft, error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("system.manage") || principal.HasRole("group_admin") {
		return BusinessSystemDraft{}, ErrForbidden
	}
	input.OrganizationID = organizationID
	if err := businesssystem.PrepareCreateInput(&input.CreateInput); err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftError(err)
	}
	_, rawValues, err := s.validateBusinessSystemDraftCustomValues(ctx, input.CustomValues)
	if err != nil {
		return BusinessSystemDraft{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("begin inventory business system draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "business_systems"); err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := validateBusinessSystemDraftOrganization(ctx, tx, organizationID); err != nil {
		return BusinessSystemDraft{}, err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_systems WHERE system_code=$1 AND deleted_at IS NULL)`, input.Code).Scan(&duplicate); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("check formal business system duplicate: %w", err)
	}
	if duplicate {
		return BusinessSystemDraft{}, ErrConflict
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_business_system_drafts (
			task_id,organization_id,system_code,system_name,short_name,owner_name,owner_contact,
			system_status,system_level,deployment_type,internet_exposure,description,
			contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,
			data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,security_notes,
			custom_values,created_by,updated_by
		) VALUES ($1::uuid,$2::uuid,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''),$11,NULLIF($12,''),$13,$14,$15,$16,$17,$18,NULLIF($19,''),NULLIF($20,''),$21::jsonb,$22::uuid,$22::uuid)
		RETURNING id::text
	`, taskID, organizationID, input.Code, input.Name, input.ShortName, input.OwnerName, input.OwnerContact,
		input.Status, input.Level, input.DeploymentType, input.InternetExposure, input.Description,
		input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation, input.PersonalInformationRecordCount,
		input.DataVolumeOver100K, input.ContainsImportantData, input.ContainsCoreData, input.DataStorageLocation, input.SecurityNotes,
		rawValues, principal.UserID).Scan(&id)
	if err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftDatabaseError(err)
	}
	if err := writeBusinessSystemDraftAudit(ctx, tx, principal, organizationID, taskID, id, "inventory.business_system_draft.create", "新增填报业务系统草稿", input.Code, requestID, ipAddress); err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventBusinessSystemWorkingVersionCreated, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: organizationID, ActorUserID: principal.UserID, RequiredPermission: "system.manage",
		ResourceType: "business_system", ResourceID: id, ResourceLabel: businessSystemDraftLabel(input.CreateInput), BatchKey: requestID,
		Details: map[string]any{"operation": "create", "task_id": taskID},
	}); err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("commit inventory business system draft: %w", err)
	}
	return loadBusinessSystemDraft(ctx, s.pool, id)
}

func (s *Service) CreateBusinessSystemUpdateDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, sourceSystemID string, input BusinessSystemDraftInput, requestID, ipAddress string) (BusinessSystemDraft, error) {
	return s.createBusinessSystemChangeDraft(ctx, principal, taskID, organizationID, sourceSystemID, "update", input, requestID, ipAddress)
}

func (s *Service) CreateBusinessSystemDeleteDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, sourceSystemID, requestID, ipAddress string) (BusinessSystemDraft, error) {
	return s.createBusinessSystemChangeDraft(ctx, principal, taskID, organizationID, sourceSystemID, "delete", BusinessSystemDraftInput{}, requestID, ipAddress)
}

func (s *Service) createBusinessSystemChangeDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, sourceSystemID, operation string, input BusinessSystemDraftInput, requestID, ipAddress string) (BusinessSystemDraft, error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("system.manage") || principal.HasRole("group_admin") {
		return BusinessSystemDraft{}, ErrForbidden
	}
	var rawValues []byte
	if operation == "update" {
		input.OrganizationID = organizationID
		if err := businesssystem.PrepareCreateInput(&input.CreateInput); err != nil {
			return BusinessSystemDraft{}, mapBusinessSystemDraftError(err)
		}
		_, raw, err := s.validateBusinessSystemDraftCustomValues(ctx, input.CustomValues)
		if err != nil {
			return BusinessSystemDraft{}, err
		}
		rawValues = raw
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("begin inventory business system change draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "business_systems"); err != nil {
		return BusinessSystemDraft{}, err
	}
	var current BusinessSystemDraftInput
	var sourceOrganizationID string
	err = tx.QueryRow(ctx, `
		SELECT bs.system_code,bs.system_name,COALESCE(bs.short_name,''),bs.owner_organization_id::text,
		       COALESCE(bs.owner_name,''),COALESCE(bs.owner_contact,''),bs.system_status,bs.system_level,
		       COALESCE(bs.deployment_type,''),bs.internet_exposure,COALESCE(bs.description,''),
		       profile.contains_personal_information,profile.contains_sensitive_personal_information,
		       profile.personal_information_record_count,profile.data_volume_over_100k,
		       profile.contains_important_data,profile.contains_core_data,
		       COALESCE(profile.data_storage_location,''),COALESCE(profile.notes,'')
		FROM business_systems bs
		JOIN business_system_data_security_profiles profile ON profile.business_system_id=bs.id
		WHERE bs.id::text=$1 AND bs.deleted_at IS NULL
	`, sourceSystemID).Scan(&current.Code, &current.Name, &current.ShortName, &sourceOrganizationID,
		&current.OwnerName, &current.OwnerContact, &current.Status, &current.Level, &current.DeploymentType,
		&current.InternetExposure, &current.Description, &current.ContainsPersonalInformation,
		&current.ContainsSensitivePersonalInformation, &current.PersonalInformationRecordCount,
		&current.DataVolumeOver100K, &current.ContainsImportantData, &current.ContainsCoreData,
		&current.DataStorageLocation, &current.SecurityNotes)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystemDraft{}, ErrNotFound
	}
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("load formal business system for draft: %w", err)
	}
	if sourceOrganizationID != organizationID {
		return BusinessSystemDraft{}, ErrForbidden
	}
	if operation == "update" {
		current = input
	} else {
		current.CustomValues, err = customfield.ValuesInTx(ctx, tx, "business_system", sourceSystemID)
		if err != nil {
			return BusinessSystemDraft{}, fmt.Errorf("load formal business system custom values: %w", err)
		}
		_, rawValues, err = s.validateBusinessSystemDraftCustomValues(ctx, current.CustomValues)
		if err != nil {
			return BusinessSystemDraft{}, err
		}
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_business_system_drafts(
			task_id,organization_id,operation,source_system_id,system_code,system_name,short_name,
			owner_name,owner_contact,system_status,system_level,deployment_type,internet_exposure,description,
			contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,
			data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,security_notes,
			custom_values,created_by,updated_by
		) VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),$10,$11,
			NULLIF($12,''),$13,NULLIF($14,''),$15,$16,$17,$18,$19,$20,NULLIF($21,''),NULLIF($22,''),$23::jsonb,$24::uuid,$24::uuid)
		ON CONFLICT(organization_id,source_system_id) WHERE source_system_id IS NOT NULL AND draft_status IN ('editing','submitted','returned') DO UPDATE SET
			operation=EXCLUDED.operation,system_code=EXCLUDED.system_code,system_name=EXCLUDED.system_name,
			short_name=EXCLUDED.short_name,owner_name=EXCLUDED.owner_name,owner_contact=EXCLUDED.owner_contact,
			system_status=EXCLUDED.system_status,system_level=EXCLUDED.system_level,deployment_type=EXCLUDED.deployment_type,
			internet_exposure=EXCLUDED.internet_exposure,description=EXCLUDED.description,
			contains_personal_information=EXCLUDED.contains_personal_information,
			contains_sensitive_personal_information=EXCLUDED.contains_sensitive_personal_information,
			personal_information_record_count=EXCLUDED.personal_information_record_count,
			data_volume_over_100k=EXCLUDED.data_volume_over_100k,contains_important_data=EXCLUDED.contains_important_data,
			contains_core_data=EXCLUDED.contains_core_data,data_storage_location=EXCLUDED.data_storage_location,
			security_notes=EXCLUDED.security_notes,custom_values=EXCLUDED.custom_values,draft_status='editing',
			updated_by=EXCLUDED.updated_by,updated_at=now()
		WHERE inventory_business_system_drafts.draft_status IN ('editing','returned')
		RETURNING id::text
	`, taskID, organizationID, operation, sourceSystemID, current.Code, current.Name, current.ShortName,
		current.OwnerName, current.OwnerContact, current.Status, current.Level, current.DeploymentType,
		current.InternetExposure, current.Description, current.ContainsPersonalInformation,
		current.ContainsSensitivePersonalInformation, current.PersonalInformationRecordCount,
		current.DataVolumeOver100K, current.ContainsImportantData, current.ContainsCoreData,
		current.DataStorageLocation, current.SecurityNotes, rawValues, principal.UserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystemDraft{}, ErrState
	}
	if err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftDatabaseError(err)
	}
	summary := "创建已有业务系统修改草稿"
	if operation == "delete" {
		summary = "创建已有业务系统删除草稿"
	}
	if err := writeBusinessSystemDraftAudit(ctx, tx, principal, organizationID, taskID, id, "inventory.business_system_draft."+operation, summary, current.Code, requestID, ipAddress); err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventBusinessSystemWorkingVersionCreated, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: organizationID, ActorUserID: principal.UserID, RequiredPermission: "system.manage",
		ResourceType: "business_system", ResourceID: id, ResourceLabel: businessSystemDraftLabel(current.CreateInput), BatchKey: requestID,
		Details: map[string]any{"operation": operation, "task_id": taskID, "published_id": sourceSystemID},
	}); err != nil {
		return BusinessSystemDraft{}, err
	}
	item, err := loadBusinessSystemDraft(ctx, tx, id)
	if err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("commit inventory business system change draft: %w", err)
	}
	return item, nil
}

func (s *Service) UpdateBusinessSystemDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID string, input BusinessSystemDraftInput, requestID, ipAddress string) (BusinessSystemDraft, error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("system.manage") || principal.HasRole("group_admin") {
		return BusinessSystemDraft{}, ErrForbidden
	}
	input.OrganizationID = organizationID
	if err := businesssystem.PrepareCreateInput(&input.CreateInput); err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftError(err)
	}
	_, rawValues, err := s.validateBusinessSystemDraftCustomValues(ctx, input.CustomValues)
	if err != nil {
		return BusinessSystemDraft{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("begin update inventory business system draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "business_systems"); err != nil {
		return BusinessSystemDraft{}, err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_systems WHERE system_code=$1 AND id<>COALESCE((SELECT source_system_id FROM inventory_business_system_drafts WHERE id::text=$2),gen_random_uuid()) AND deleted_at IS NULL)`, input.Code, draftID).Scan(&duplicate); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("check formal business system duplicate: %w", err)
	}
	if duplicate {
		return BusinessSystemDraft{}, ErrConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE inventory_business_system_drafts SET system_code=$4,system_name=$5,short_name=NULLIF($6,''),owner_name=NULLIF($7,''),owner_contact=NULLIF($8,''),
			system_status=$9,system_level=$10,deployment_type=NULLIF($11,''),internet_exposure=$12,description=NULLIF($13,''),
			contains_personal_information=$14,contains_sensitive_personal_information=$15,personal_information_record_count=$16,
			data_volume_over_100k=$17,contains_important_data=$18,contains_core_data=$19,data_storage_location=NULLIF($20,''),security_notes=NULLIF($21,''),
			custom_values=$22::jsonb,draft_status='editing',updated_by=$23::uuid,updated_at=now()
		WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3 AND operation IN ('create','update') AND draft_status IN ('editing','returned')
	`, draftID, taskID, organizationID, input.Code, input.Name, input.ShortName, input.OwnerName, input.OwnerContact,
		input.Status, input.Level, input.DeploymentType, input.InternetExposure, input.Description,
		input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation, input.PersonalInformationRecordCount,
		input.DataVolumeOver100K, input.ContainsImportantData, input.ContainsCoreData, input.DataStorageLocation, input.SecurityNotes,
		rawValues, principal.UserID)
	if err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return BusinessSystemDraft{}, ErrNotFound
	}
	if err := writeBusinessSystemDraftAudit(ctx, tx, principal, organizationID, taskID, draftID, "inventory.business_system_draft.update", "更新填报业务系统草稿", input.Code, requestID, ipAddress); err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("commit update inventory business system draft: %w", err)
	}
	return loadBusinessSystemDraft(ctx, s.pool, draftID)
}

func (s *Service) DeleteBusinessSystemDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID, requestID, ipAddress string) error {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("system.manage") || principal.HasRole("group_admin") {
		return ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete inventory business system draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "business_systems"); err != nil {
		return err
	}
	var referencedAssets int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM inventory_asset_drafts WHERE business_system_draft_id=$1::uuid AND draft_status IN ('editing','submitted','returned')`, draftID).Scan(&referencedAssets); err != nil {
		return fmt.Errorf("check business system draft asset references: %w", err)
	}
	if referencedAssets > 0 {
		return ErrResourceInUse
	}
	var code, label string
	unselected := false
	if err := tx.QueryRow(ctx, `UPDATE inventory_business_system_drafts SET task_id=NULL,selected_at=NULL,updated_by=$4::uuid,updated_at=now() WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3 AND selected_at IS NOT NULL AND draft_status IN ('editing','returned') RETURNING system_code,system_name`, draftID, taskID, organizationID, principal.UserID).Scan(&code, &label); errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `UPDATE inventory_business_system_drafts SET draft_status='discarded',updated_by=$4::uuid,updated_at=now() WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3 AND draft_status IN ('editing','returned') RETURNING system_code`, draftID, taskID, organizationID, principal.UserID).Scan(&code)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("discard inventory business system draft: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("unselect inventory business system draft: %w", err)
	} else {
		unselected = true
	}
	if err := writeBusinessSystemDraftAudit(ctx, tx, principal, organizationID, taskID, draftID, "inventory.business_system_draft.delete", "删除填报业务系统草稿", code, requestID, ipAddress); err != nil {
		return err
	}
	if unselected {
		if err := notification.WriteEvent(ctx, tx, notification.EventInput{
			EventType: notification.EventBusinessSystemWorkingVersionUnselected, Category: notification.CategoryWorkingVersion,
			TargetOrganizationID: organizationID, ActorUserID: principal.UserID, RequiredPermission: "system.manage",
			ResourceType: "business_system", ResourceID: draftID, ResourceLabel: label, BatchKey: requestID,
			Details: map[string]any{"task_id": taskID},
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Service) validateBusinessSystemDraftCustomValues(ctx context.Context, values []customfield.Value) ([]customfield.Definition, []byte, error) {
	definitions, err := customfield.NewService(s.pool).EnabledDefinitions(ctx, "business_system")
	if err != nil {
		return nil, nil, fmt.Errorf("load business system custom fields: %w", err)
	}
	if _, _, err := customfield.ValidateValues(definitions, values); err != nil {
		return nil, nil, ErrInvalid
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, nil, fmt.Errorf("encode business system draft custom values: %w", err)
	}
	return definitions, raw, nil
}

func validateBusinessSystemDraftOrganization(ctx context.Context, q queryRower, organizationID string) error {
	var status string
	if err := q.QueryRow(ctx, `SELECT status FROM organizations WHERE id::text=$1 AND deleted_at IS NULL`, organizationID).Scan(&status); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("load business system draft organization: %w", err)
	}
	if status != "enabled" {
		return ErrInvalid
	}
	return nil
}

const businessSystemDraftSelect = `
	SELECT draft.id::text,draft.task_id::text,COALESCE(draft.source_system_id::text,''),draft.operation,draft.draft_status,
	       draft.system_code,draft.system_name,COALESCE(draft.short_name,''),organization.id::text,organization.organization_code,organization.organization_name,
	       COALESCE(draft.owner_name,''),COALESCE(draft.owner_contact,''),draft.system_status,draft.system_level,COALESCE(draft.deployment_type,''),draft.internet_exposure,COALESCE(draft.description,''),
	       draft.contains_personal_information,draft.contains_sensitive_personal_information,draft.personal_information_record_count,draft.data_volume_over_100k,
	       draft.contains_important_data,draft.contains_core_data,COALESCE(draft.data_storage_location,''),COALESCE(draft.security_notes,''),draft.custom_values,draft.created_at,draft.updated_at
	FROM inventory_business_system_drafts draft JOIN organizations organization ON organization.id=draft.organization_id`

func loadBusinessSystemDraft(ctx context.Context, q queryRower, draftID string) (BusinessSystemDraft, error) {
	return scanBusinessSystemDraft(q.QueryRow(ctx, businessSystemDraftSelect+` WHERE draft.id::text=$1`, draftID))
}

func scanBusinessSystemDraft(row rowScanner) (BusinessSystemDraft, error) {
	var item BusinessSystemDraft
	var raw []byte
	err := row.Scan(&item.ID, &item.TaskID, &item.SourceSystemID, &item.Operation, &item.DraftStatus,
		&item.Code, &item.Name, &item.ShortName, &item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
		&item.OwnerName, &item.OwnerContact, &item.Status, &item.Level, &item.DeploymentType, &item.InternetExposure, &item.Description,
		&item.ContainsPersonalInformation, &item.ContainsSensitivePersonalInformation, &item.PersonalInformationRecordCount, &item.DataVolumeOver100K,
		&item.ContainsImportantData, &item.ContainsCoreData, &item.DataStorageLocation, &item.SecurityNotes, &raw, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystemDraft{}, ErrNotFound
	}
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("scan inventory business system draft: %w", err)
	}
	if err := json.Unmarshal(raw, &item.CustomValues); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("decode inventory business system draft custom values: %w", err)
	}
	item.IsDraft = true
	return item, nil
}

func writeBusinessSystemDraftAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, organizationID, taskID, draftID, action, summary, code, requestID, ipAddress string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,$3,'inventory_business_system_draft',$4,$5,jsonb_build_object('task_id',$6::text,'system_code',$7::text),NULLIF($8,'')::inet,NULLIF($9,''))`, principal.UserID, organizationID, action, draftID, summary, taskID, code, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write inventory business system draft audit: %w", err)
	}
	return nil
}

func mapBusinessSystemDraftError(err error) error {
	switch {
	case errors.Is(err, businesssystem.ErrInvalid), errors.Is(err, customfield.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, businesssystem.ErrConflict):
		return ErrConflict
	case errors.Is(err, businesssystem.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}

func mapBusinessSystemDraftDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("write inventory business system draft: %w", err)
}

func publishBusinessSystemDrafts(ctx context.Context, tx pgx.Tx, principal auth.Principal, taskID, organizationID, requestID, ipAddress string) error {
	rows, err := tx.Query(ctx, businessSystemDraftSelect+` WHERE draft.task_id::text=$1 AND draft.organization_id::text=$2 AND draft.draft_status='submitted' ORDER BY draft.created_at FOR UPDATE`, taskID, organizationID)
	if err != nil {
		return fmt.Errorf("load submitted business system drafts: %w", err)
	}
	items := make([]BusinessSystemDraft, 0)
	for rows.Next() {
		item, err := scanBusinessSystemDraft(rows)
		if err != nil {
			rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate submitted business system drafts: %w", err)
	}
	rows.Close()
	definitions, err := customfield.EnabledDefinitionsInTx(ctx, tx, "business_system")
	if err != nil {
		return fmt.Errorf("load publication custom fields: %w", err)
	}
	for _, item := range items {
		resourceID := item.ID
		action, summary := "business_system.create", "审核发布填报业务系统"
		switch item.Operation {
		case "create":
			if _, err := tx.Exec(ctx, `INSERT INTO business_systems(id,system_code,system_name,short_name,owner_organization_id,owner_name,owner_contact,system_status,system_level,deployment_type,internet_exposure,description,created_by,updated_by) VALUES($1::uuid,$2,$3,NULLIF($4,''),$5::uuid,NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''),$11,NULLIF($12,''),$13::uuid,$13::uuid)`, item.ID, item.Code, item.Name, item.ShortName, organizationID, item.OwnerName, item.OwnerContact, item.Status, item.Level, item.DeploymentType, item.InternetExposure, item.Description, principal.UserID); err != nil {
				return mapBusinessSystemDraftDatabaseError(err)
			}
		case "update":
			if item.SourceSystemID == "" {
				return ErrInvalid
			}
			resourceID = item.SourceSystemID
			action, summary = "business_system.update", "审核发布填报业务系统变更"
			result, err := tx.Exec(ctx, `UPDATE business_systems SET system_code=$2,system_name=$3,short_name=NULLIF($4,''),owner_name=NULLIF($5,''),owner_contact=NULLIF($6,''),system_status=$7,system_level=$8,deployment_type=NULLIF($9,''),internet_exposure=$10,description=NULLIF($11,''),updated_by=$12::uuid,updated_at=now() WHERE id::text=$1 AND owner_organization_id::text=$13 AND deleted_at IS NULL`, item.SourceSystemID, item.Code, item.Name, item.ShortName, item.OwnerName, item.OwnerContact, item.Status, item.Level, item.DeploymentType, item.InternetExposure, item.Description, principal.UserID, organizationID)
			if err != nil {
				return mapBusinessSystemDraftDatabaseError(err)
			}
			if result.RowsAffected() == 0 {
				return ErrNotFound
			}
		case "delete":
			if item.SourceSystemID == "" {
				return ErrInvalid
			}
			resourceID = item.SourceSystemID
			action, summary = "business_system.delete", "审核发布填报业务系统删除"
			var linkedAssets int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM system_assets link JOIN assets formal ON formal.id=link.asset_id
                WHERE link.business_system_id=$1::uuid AND formal.deleted_at IS NULL
                  AND NOT EXISTS (SELECT 1 FROM inventory_asset_drafts ad WHERE ad.task_id::text=$2 AND ad.organization_id::text=$3 AND ad.source_asset_id=link.asset_id AND ad.draft_status='submitted' AND (ad.operation='delete' OR (ad.operation='update' AND COALESCE(ad.business_system_id::text,'') <> $1::text)))`, resourceID, taskID, organizationID).Scan(&linkedAssets); err != nil {
				return fmt.Errorf("check published business system assets: %w", err)
			}
			if linkedAssets > 0 {
				return ErrResourceInUse
			}
			result, err := tx.Exec(ctx, `UPDATE business_systems SET deleted_at=now(),system_status='archived',updated_by=$2::uuid,updated_at=now() WHERE id::text=$1 AND owner_organization_id::text=$3 AND deleted_at IS NULL`, resourceID, principal.UserID, organizationID)
			if err != nil {
				return fmt.Errorf("delete published business system: %w", err)
			}
			if result.RowsAffected() == 0 {
				return ErrNotFound
			}
		default:
			return ErrInvalid
		}
		if item.Operation != "delete" {
			if _, err := tx.Exec(ctx, `INSERT INTO business_system_data_security_profiles(business_system_id,contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,reported_by,notes) VALUES($1::uuid,$2,$3,$4,$5,$6,$7,NULLIF($8,''),$9::uuid,NULLIF($10,'')) ON CONFLICT(business_system_id) DO UPDATE SET contains_personal_information=EXCLUDED.contains_personal_information,contains_sensitive_personal_information=EXCLUDED.contains_sensitive_personal_information,personal_information_record_count=EXCLUDED.personal_information_record_count,data_volume_over_100k=EXCLUDED.data_volume_over_100k,contains_important_data=EXCLUDED.contains_important_data,contains_core_data=EXCLUDED.contains_core_data,data_storage_location=EXCLUDED.data_storage_location,reported_by=EXCLUDED.reported_by,notes=EXCLUDED.notes,updated_at=now()`, resourceID, item.ContainsPersonalInformation, item.ContainsSensitivePersonalInformation, item.PersonalInformationRecordCount, item.DataVolumeOver100K, item.ContainsImportantData, item.ContainsCoreData, item.DataStorageLocation, principal.UserID, item.SecurityNotes); err != nil {
				return fmt.Errorf("publish business system data profile: %w", err)
			}
			if err := customfield.SaveImportedValuesInTx(ctx, tx, "business_system", resourceID, definitions, item.CustomValues, principal.UserID); err != nil {
				return mapBusinessSystemDraftError(err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,$3,'business_system',$4,$5,jsonb_build_object('task_id',$6::text,'code',$7::text,'name',$8::text,'source','inventory_task'),NULLIF($9,'')::inet,NULLIF($10,''))`, principal.UserID, organizationID, action, resourceID, summary, taskID, item.Code, item.Name, ipAddress, requestID); err != nil {
			return fmt.Errorf("write published business system audit: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET draft_status='published',published_at=now(),updated_by=$3::uuid,updated_at=now() WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status='submitted'`, taskID, organizationID, principal.UserID); err != nil {
		return fmt.Errorf("mark business system drafts published: %w", err)
	}
	return nil
}
