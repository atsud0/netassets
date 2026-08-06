package businesssystem

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/customfield"
	"netassets/internal/pagination"
)

func usesWorkingInventory(p auth.Principal) bool { return !p.HasRole("group_admin") }

const systemWorkingAccessibleCTE = `
WITH RECURSIVE roots(id) AS (
 SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='system.manage'
 JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), descendants(id) AS (SELECT id FROM roots UNION SELECT o.id FROM organizations o JOIN descendants p ON o.parent_id=p.id WHERE o.status='enabled' AND o.deleted_at IS NULL), accessible(id) AS (
 SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled' JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='system.manage'
 JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants') UNION SELECT id FROM descendants
)`

func (s *Service) searchWorkingCreates(ctx context.Context, p auth.Principal, f SearchFilter) (pagination.Result[BusinessSystem], error) {
	if !p.HasPermission("system.manage") {
		return pagination.Result[BusinessSystem]{}, ErrForbidden
	}
	page := f.Page
	if page.Page == 0 {
		page.Page = pagination.DefaultPage
	}
	if page.PageSize == 0 {
		page.PageSize = pagination.DefaultPageSize
	}
	rows, err := s.pool.Query(ctx, systemWorkingAccessibleCTE+`, ledger AS (
 SELECT b.id::text id,b.system_code,b.system_name,COALESCE(b.short_name,'') short_name,o.id::text organization_id,o.organization_code,o.organization_name,
 COALESCE(b.owner_name,'') owner_name,COALESCE(b.owner_contact,'') owner_contact,b.system_status,b.system_level,COALESCE(b.deployment_type,'') deployment_type,b.internet_exposure,COALESCE(b.description,'') description,
 p.contains_personal_information,p.contains_sensitive_personal_information,p.personal_information_record_count,p.data_volume_over_100k,p.contains_important_data,p.contains_core_data,
 COALESCE(p.data_storage_location,'') data_storage_location,COALESCE(p.notes,'') security_notes,(SELECT count(*)::int FROM system_assets sa JOIN assets a ON a.id=sa.asset_id AND a.deleted_at IS NULL WHERE sa.business_system_id=b.id) asset_count,
 b.created_at,b.updated_at,'reported'::text reporting_status,false is_working_copy,''::text published_id,''::text task_id,''::text draft_status,''::text operation,'[]'::jsonb custom_values
 FROM business_systems b JOIN organizations o ON o.id=b.owner_organization_id JOIN business_system_data_security_profiles p ON p.business_system_id=b.id
 WHERE b.deleted_at IS NULL AND b.owner_organization_id IN (SELECT id FROM accessible)
 AND NOT EXISTS(SELECT 1 FROM inventory_business_system_drafts d WHERE d.source_system_id=b.id AND d.organization_id=b.owner_organization_id AND d.draft_status IN ('editing','submitted','returned'))
 UNION ALL
 SELECT d.id::text,d.system_code,d.system_name,COALESCE(d.short_name,''),o.id::text,o.organization_code,o.organization_name,COALESCE(d.owner_name,''),COALESCE(d.owner_contact,''),
 d.system_status,d.system_level,COALESCE(d.deployment_type,''),d.internet_exposure,COALESCE(d.description,''),d.contains_personal_information,d.contains_sensitive_personal_information,
 d.personal_information_record_count,d.data_volume_over_100k,d.contains_important_data,d.contains_core_data,COALESCE(d.data_storage_location,''),COALESCE(d.security_notes,''),
 CASE WHEN d.source_system_id IS NULL THEN 0 ELSE (SELECT count(*)::int FROM system_assets sa JOIN assets a ON a.id=sa.asset_id AND a.deleted_at IS NULL WHERE sa.business_system_id=d.source_system_id) END,
 d.created_at,d.updated_at,CASE WHEN d.draft_status='submitted' THEN 'pending_review' WHEN d.draft_status='returned' THEN 'returned' WHEN d.operation='delete' THEN 'pending_offline' WHEN d.operation='update' THEN 'changed' ELSE 'unreported' END,
 true,COALESCE(d.source_system_id::text,''),COALESCE(d.task_id::text,''),d.draft_status,d.operation,d.custom_values
 FROM inventory_business_system_drafts d JOIN organizations o ON o.id=d.organization_id
 WHERE d.organization_id IN (SELECT id FROM accessible) AND d.draft_status IN ('editing','submitted','returned')
)
SELECT id,system_code,system_name,short_name,organization_id,organization_code,organization_name,owner_name,owner_contact,system_status,system_level,deployment_type,internet_exposure,description,
 contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,security_notes,asset_count,
 created_at,updated_at,reporting_status,is_working_copy,published_id,task_id,draft_status,operation,custom_values,count(*) OVER()
FROM ledger WHERE ($2='' OR organization_id=$2) AND ($3='' OR system_status=$3) AND ($4='' OR concat_ws(' ',system_code,system_name,short_name,organization_name,owner_name,owner_contact) ILIKE '%'||$4||'%')
ORDER BY organization_code,system_code LIMIT $5 OFFSET $6`, p.UserID, strings.TrimSpace(f.OrganizationID), strings.TrimSpace(f.Status), strings.TrimSpace(f.Keyword), page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[BusinessSystem]{}, fmt.Errorf("search organization working systems: %w", err)
	}
	defer rows.Close()
	items := make([]BusinessSystem, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var b BusinessSystem
		var raw []byte
		if err = rows.Scan(&b.ID, &b.Code, &b.Name, &b.ShortName, &b.OrganizationID, &b.OrganizationCode, &b.OrganizationName, &b.OwnerName, &b.OwnerContact, &b.Status, &b.Level, &b.DeploymentType, &b.InternetExposure, &b.Description, &b.ContainsPersonalInformation, &b.ContainsSensitivePersonalInformation, &b.PersonalInformationRecordCount, &b.DataVolumeOver100K, &b.ContainsImportantData, &b.ContainsCoreData, &b.DataStorageLocation, &b.SecurityNotes, &b.AssetCount, &b.CreatedAt, &b.UpdatedAt, &b.ReportingStatus, &b.IsWorkingCopy, &b.PublishedID, &b.TaskID, &b.DraftStatus, &b.Operation, &raw, &total); err != nil {
			return pagination.Result[BusinessSystem]{}, err
		}
		if err = json.Unmarshal(raw, &b.CustomValues); err != nil {
			return pagination.Result[BusinessSystem]{}, err
		}
		items = append(items, b)
	}
	if err = rows.Err(); err != nil {
		return pagination.Result[BusinessSystem]{}, err
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) getWorking(ctx context.Context, p auth.Principal, id string) (BusinessSystem, error) {
	if p.HasRole("group_admin") || !p.HasPermission("system.manage") {
		return BusinessSystem{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, systemWorkingAccessibleCTE+`
SELECT d.id::text,d.system_code,d.system_name,COALESCE(d.short_name,''),o.id::text,o.organization_code,o.organization_name,COALESCE(d.owner_name,''),COALESCE(d.owner_contact,''),d.system_status,d.system_level,
COALESCE(d.deployment_type,''),d.internet_exposure,COALESCE(d.description,''),d.contains_personal_information,d.contains_sensitive_personal_information,d.personal_information_record_count,d.data_volume_over_100k,d.contains_important_data,d.contains_core_data,
COALESCE(d.data_storage_location,''),COALESCE(d.security_notes,''),CASE WHEN d.source_system_id IS NULL THEN 0 ELSE (SELECT count(*)::int FROM system_assets sa JOIN assets a ON a.id=sa.asset_id AND a.deleted_at IS NULL WHERE sa.business_system_id=d.source_system_id) END,
d.created_at,d.updated_at,CASE WHEN d.draft_status='submitted' THEN 'pending_review' WHEN d.draft_status='returned' THEN 'returned' WHEN d.operation='delete' THEN 'pending_offline' WHEN d.operation='update' THEN 'changed' ELSE 'unreported' END,
true,COALESCE(d.source_system_id::text,''),COALESCE(d.task_id::text,''),d.draft_status,d.operation,d.custom_values
FROM inventory_business_system_drafts d JOIN organizations o ON o.id=d.organization_id WHERE d.id::text=$2 AND d.organization_id IN(SELECT id FROM accessible) AND d.draft_status IN('editing','submitted','returned')`, p.UserID, id)
	var b BusinessSystem
	var raw []byte
	err := row.Scan(&b.ID, &b.Code, &b.Name, &b.ShortName, &b.OrganizationID, &b.OrganizationCode, &b.OrganizationName, &b.OwnerName, &b.OwnerContact, &b.Status, &b.Level, &b.DeploymentType, &b.InternetExposure, &b.Description, &b.ContainsPersonalInformation, &b.ContainsSensitivePersonalInformation, &b.PersonalInformationRecordCount, &b.DataVolumeOver100K, &b.ContainsImportantData, &b.ContainsCoreData, &b.DataStorageLocation, &b.SecurityNotes, &b.AssetCount, &b.CreatedAt, &b.UpdatedAt, &b.ReportingStatus, &b.IsWorkingCopy, &b.PublishedID, &b.TaskID, &b.DraftStatus, &b.Operation, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystem{}, ErrNotFound
	}
	if err != nil {
		return BusinessSystem{}, err
	}
	if err = json.Unmarshal(raw, &b.CustomValues); err != nil {
		return BusinessSystem{}, err
	}
	return b, nil
}

func (s *Service) validateWorkingValues(ctx context.Context, values []customfield.Value) ([]byte, error) {
	defs, err := customfield.NewService(s.pool).EnabledDefinitions(ctx, "business_system")
	if err != nil {
		return nil, err
	}
	if _, _, err = customfield.ValidateValues(defs, values); err != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(values)
}

func (s *Service) createUnreported(ctx context.Context, p auth.Principal, in CreateInput, requestID, ip string) (BusinessSystem, error) {
	if !p.HasPermission("system.manage") {
		return BusinessSystem{}, ErrForbidden
	}
	if err := PrepareCreateInput(&in); err != nil {
		return BusinessSystem{}, err
	}
	ok, err := access.CanOperateOrganization(ctx, s.pool, p, in.OrganizationID, "system.manage")
	if err != nil {
		return BusinessSystem{}, err
	}
	if !ok {
		return BusinessSystem{}, ErrForbidden
	}
	raw, err := s.validateWorkingValues(ctx, in.CustomValues)
	if err != nil {
		return BusinessSystem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id::text=$1 AND deleted_at IS NULL`, in.OrganizationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return BusinessSystem{}, ErrNotFound
	}
	if err != nil {
		return BusinessSystem{}, err
	}
	if status != "enabled" {
		return BusinessSystem{}, ErrInvalid
	}
	var duplicate bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_systems WHERE system_code=$1 AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM inventory_business_system_drafts WHERE system_code=$1 AND draft_status IN('editing','submitted','returned'))`, in.Code).Scan(&duplicate)
	if err != nil {
		return BusinessSystem{}, err
	}
	if duplicate {
		return BusinessSystem{}, ErrConflict
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO inventory_business_system_drafts(task_id,organization_id,operation,system_code,system_name,short_name,owner_name,owner_contact,system_status,system_level,deployment_type,internet_exposure,description,contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,security_notes,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'create',$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,$8,NULLIF($9,''),$10,NULLIF($11,''),$12,$13,$14,$15,$16,$17,NULLIF($18,''),NULLIF($19,''),$20::jsonb,'editing',$21::uuid,$21::uuid) RETURNING id::text`, in.OrganizationID, in.Code, in.Name, in.ShortName, in.OwnerName, in.OwnerContact, in.Status, in.Level, in.DeploymentType, in.InternetExposure, in.Description, in.ContainsPersonalInformation, in.ContainsSensitivePersonalInformation, in.PersonalInformationRecordCount, in.DataVolumeOver100K, in.ContainsImportantData, in.ContainsCoreData, in.DataStorageLocation, in.SecurityNotes, raw, p.UserID).Scan(&id)
	if err != nil {
		return BusinessSystem{}, mapWorkingDB(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'business_system.working_create','business_system',$3,'新增未上报业务系统',jsonb_build_object('reporting_status','unreported'),NULLIF($4,'')::inet,NULLIF($5,''))`, p.UserID, in.OrganizationID, id, ip, requestID); err != nil {
		return BusinessSystem{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BusinessSystem{}, err
	}
	return s.getWorking(ctx, p, id)
}

func (s *Service) updateWorking(ctx context.Context, p auth.Principal, id string, in CreateInput, requestID, ip string) (BusinessSystem, error) {
	cur, err := s.getWorking(ctx, p, id)
	if err != nil {
		return BusinessSystem{}, err
	}
	if cur.TaskID != "" || cur.DraftStatus == "submitted" || cur.Operation == "delete" {
		return BusinessSystem{}, ErrInvalid
	}
	if cur.OrganizationID != in.OrganizationID {
		return BusinessSystem{}, ErrForbidden
	}
	if err = PrepareCreateInput(&in); err != nil {
		return BusinessSystem{}, err
	}
	raw, err := s.validateWorkingValues(ctx, in.CustomValues)
	if err != nil {
		return BusinessSystem{}, err
	}
	r, err := s.pool.Exec(ctx, `UPDATE inventory_business_system_drafts SET system_code=$2,system_name=$3,short_name=NULLIF($4,''),owner_name=NULLIF($5,''),owner_contact=NULLIF($6,''),system_status=$7,system_level=$8,deployment_type=NULLIF($9,''),internet_exposure=$10,description=NULLIF($11,''),contains_personal_information=$12,contains_sensitive_personal_information=$13,personal_information_record_count=$14,data_volume_over_100k=$15,contains_important_data=$16,contains_core_data=$17,data_storage_location=NULLIF($18,''),security_notes=NULLIF($19,''),custom_values=$20::jsonb,draft_status='editing',updated_by=$21::uuid,updated_at=now() WHERE id::text=$1 AND task_id IS NULL AND operation IN('create','update') AND draft_status IN('editing','returned')`, id, in.Code, in.Name, in.ShortName, in.OwnerName, in.OwnerContact, in.Status, in.Level, in.DeploymentType, in.InternetExposure, in.Description, in.ContainsPersonalInformation, in.ContainsSensitivePersonalInformation, in.PersonalInformationRecordCount, in.DataVolumeOver100K, in.ContainsImportantData, in.ContainsCoreData, in.DataStorageLocation, in.SecurityNotes, raw, p.UserID)
	if err != nil {
		return BusinessSystem{}, mapWorkingDB(err)
	}
	if r.RowsAffected() == 0 {
		return BusinessSystem{}, ErrNotFound
	}
	return s.getWorking(ctx, p, id)
}

func (s *Service) createWorkingUpdate(ctx context.Context, p auth.Principal, current BusinessSystem, in CreateInput, requestID, ip string) (BusinessSystem, error) {
	if current.OrganizationID != in.OrganizationID {
		return BusinessSystem{}, ErrForbidden
	}
	if err := PrepareCreateInput(&in); err != nil {
		return BusinessSystem{}, err
	}
	raw, err := s.validateWorkingValues(ctx, in.CustomValues)
	if err != nil {
		return BusinessSystem{}, err
	}
	var id string
	err = s.pool.QueryRow(ctx, `INSERT INTO inventory_business_system_drafts(task_id,organization_id,operation,source_system_id,system_code,system_name,short_name,owner_name,owner_contact,system_status,system_level,deployment_type,internet_exposure,description,contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,security_notes,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'update',$2::uuid,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''),$11,NULLIF($12,''),$13,$14,$15,$16,$17,$18,NULLIF($19,''),NULLIF($20,''),$21::jsonb,'editing',$22::uuid,$22::uuid) RETURNING id::text`, in.OrganizationID, current.ID, in.Code, in.Name, in.ShortName, in.OwnerName, in.OwnerContact, in.Status, in.Level, in.DeploymentType, in.InternetExposure, in.Description, in.ContainsPersonalInformation, in.ContainsSensitivePersonalInformation, in.PersonalInformationRecordCount, in.DataVolumeOver100K, in.ContainsImportantData, in.ContainsCoreData, in.DataStorageLocation, in.SecurityNotes, raw, p.UserID).Scan(&id)
	if err != nil {
		return BusinessSystem{}, mapWorkingDB(err)
	}
	return s.getWorking(ctx, p, id)
}

func (s *Service) deleteWorking(ctx context.Context, p auth.Principal, id string) error {
	cur, err := s.getWorking(ctx, p, id)
	if err != nil {
		return err
	}
	if cur.TaskID != "" || cur.DraftStatus == "submitted" {
		return ErrInvalid
	}
	ok, err := access.CanOperateOrganization(ctx, s.pool, p, cur.OrganizationID, "system.manage")
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	if cur.Operation == "create" {
		var linked bool
		err = s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_asset_drafts WHERE business_system_draft_id::text=$1 AND draft_status IN('editing','submitted','returned'))`, id).Scan(&linked)
		if err != nil {
			return err
		}
		if linked {
			return ErrInUse
		}
		r, err := s.pool.Exec(ctx, `UPDATE inventory_business_system_drafts SET draft_status='discarded',updated_by=$2::uuid,updated_at=now() WHERE id::text=$1 AND task_id IS NULL AND operation='create' AND draft_status IN('editing','returned')`, id, p.UserID)
		if err != nil {
			return err
		}
		if r.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	}
	r, err := s.pool.Exec(ctx, `UPDATE inventory_business_system_drafts SET operation='delete',draft_status='editing',updated_by=$2::uuid,updated_at=now() WHERE id::text=$1 AND task_id IS NULL AND source_system_id IS NOT NULL AND draft_status IN('editing','returned')`, id, p.UserID)
	if err != nil {
		return err
	}
	if r.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) createWorkingDelete(ctx context.Context, p auth.Principal, current BusinessSystem) error {
	ok, err := access.CanOperateOrganization(ctx, s.pool, p, current.OrganizationID, "system.manage")
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	var linked int
	if err = s.pool.QueryRow(ctx, `SELECT count(*) FROM system_assets sa JOIN assets a ON a.id=sa.asset_id WHERE sa.business_system_id=$1::uuid AND a.deleted_at IS NULL`, current.ID).Scan(&linked); err != nil {
		return err
	}
	if linked > 0 {
		return ErrInUse
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	values, err := customfield.ValuesInTx(ctx, tx, "business_system", current.ID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO inventory_business_system_drafts(task_id,organization_id,operation,source_system_id,system_code,system_name,short_name,owner_name,owner_contact,system_status,system_level,deployment_type,internet_exposure,description,contains_personal_information,contains_sensitive_personal_information,personal_information_record_count,data_volume_over_100k,contains_important_data,contains_core_data,data_storage_location,security_notes,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'delete',$2::uuid,$3,$4,NULLIF($5,''),NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''),$11,NULLIF($12,''),$13,$14,$15,$16,$17,$18,NULLIF($19,''),NULLIF($20,''),$21::jsonb,'editing',$22::uuid,$22::uuid)`, current.OrganizationID, current.ID, current.Code, current.Name, current.ShortName, current.OwnerName, current.OwnerContact, current.Status, current.Level, current.DeploymentType, current.InternetExposure, current.Description, current.ContainsPersonalInformation, current.ContainsSensitivePersonalInformation, current.PersonalInformationRecordCount, current.DataVolumeOver100K, current.ContainsImportantData, current.ContainsCoreData, current.DataStorageLocation, current.SecurityNotes, raw, p.UserID); err != nil {
		return mapWorkingDB(err)
	}
	return tx.Commit(ctx)
}
func mapWorkingDB(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		return ErrConflict
	}
	return err
}
