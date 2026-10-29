package businesssystem

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/notification"
)

// RestorePublishedVersion creates or replaces the organization-side daily
// working version for one published business system. Published group data is
// left untouched until the new working version is submitted and approved.
func (s *Service) RestorePublishedVersion(ctx context.Context, principal auth.Principal, systemID string, input CreateInput, requestID, ipAddress string) (BusinessSystem, error) {
	if principal.HasRole("group_admin") || !principal.HasPermission("system.manage") {
		return BusinessSystem{}, ErrForbidden
	}
	current, err := s.Get(ctx, principal, systemID)
	if err != nil {
		return BusinessSystem{}, err
	}
	if current.IsWorkingCopy {
		return BusinessSystem{}, invalidInput("恢复来源：请选择已发布的正式业务系统，不能选择工作版本")
	}
	if input.OrganizationID != current.OrganizationID {
		return BusinessSystem{}, ErrForbidden
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, current.OrganizationID, "system.manage")
	if err != nil {
		return BusinessSystem{}, err
	}
	if !allowed {
		return BusinessSystem{}, ErrForbidden
	}

	if input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		return BusinessSystem{}, ErrVersionRequired
	}
	if (*input.ExpectedVersion == 0) != (input.ExpectedWorkingVersionID == "") {
		return BusinessSystem{}, invalidInput("恢复版本：无工作版本时版本号必须为 0 且标识为空；已有工作版本时须同时提供其标识和正整数版本号")
	}

	working, err := s.getWorkingBySource(ctx, principal, current.ID)
	switch {
	case err == nil:
		if input.ExpectedWorkingVersionID != working.ID || *input.ExpectedVersion != working.Version {
			return BusinessSystem{}, ErrEditConflict
		}
		if working.TaskID != "" || working.DraftStatus == "submitted" {
			return BusinessSystem{}, ErrConflict
		}
		return s.restoreWorking(ctx, principal, working, input, requestID, ipAddress)
	case !errors.Is(err, ErrNotFound):
		return BusinessSystem{}, err
	default:
		if *input.ExpectedVersion != 0 {
			return BusinessSystem{}, ErrEditConflict
		}
		return s.createRestoredWorking(ctx, principal, current.BusinessSystem, input, requestID, ipAddress)
	}
}

func (s *Service) getWorkingBySource(ctx context.Context, principal auth.Principal, sourceSystemID string) (BusinessSystem, error) {
	if principal.HasRole("group_admin") || !principal.HasPermission("system.manage") {
		return BusinessSystem{}, ErrNotFound
	}
	var id string
	err := s.pool.QueryRow(ctx, systemWorkingAccessibleCTE+`
		SELECT draft.id::text
		FROM inventory_business_system_drafts draft
		WHERE draft.source_system_id::text=$2
		  AND draft.organization_id IN (SELECT id FROM accessible)
		  AND draft.draft_status IN ('editing','submitted','returned')
		ORDER BY draft.updated_at DESC
		LIMIT 1
	`, principal.UserID, sourceSystemID).Scan(&id)
	if err != nil {
		return BusinessSystem{}, mapWorkingNotFound(err)
	}
	return s.getWorking(ctx, principal, id)
}

func (s *Service) restoreWorking(ctx context.Context, principal auth.Principal, current BusinessSystem, input CreateInput, requestID, ipAddress string) (BusinessSystem, error) {
	if current.TaskID != "" || current.DraftStatus == "submitted" {
		return BusinessSystem{}, ErrConflict
	}
	if current.OrganizationID != input.OrganizationID {
		return BusinessSystem{}, ErrForbidden
	}
	if err := PrepareCreateInput(&input); err != nil {
		return BusinessSystem{}, err
	}
	customValues, err := s.validateWorkingValues(ctx, input.CustomValues)
	if err != nil {
		return BusinessSystem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A lock serializes writers but cannot authorize overwriting edits made
	// after the operator confirmed the restore. Compare the confirmed token.
	baseVersion, err := lockWorkingVersion(ctx, tx, current.ID)
	if err != nil {
		return BusinessSystem{}, err
	}
	if input.ExpectedWorkingVersionID != current.ID || input.ExpectedVersion == nil || baseVersion != *input.ExpectedVersion {
		return BusinessSystem{}, ErrEditConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE inventory_business_system_drafts
		SET operation='update', system_code=$2, system_name=$3, short_name=NULLIF($4,''),
		    owner_name=NULLIF($5,''), owner_contact=NULLIF($6,''), system_status=$7,
		    system_level=$8, deployment_type=NULLIF($9,''), internet_exposure=$10,
		    description=NULLIF($11,''), contains_personal_information=$12,
		    contains_sensitive_personal_information=$13, personal_information_record_count=$14,
		    data_volume_over_100k=$15, contains_important_data=$16, contains_core_data=$17,
		    data_storage_location=NULLIF($18,''), security_notes=NULLIF($19,''),
		    custom_values=$20::jsonb, draft_status='editing', version=version+1,
		    updated_by=$21::uuid, last_edited_by=$21::uuid, updated_at=now()
		WHERE id::text=$1 AND task_id IS NULL AND source_system_id IS NOT NULL
		  AND draft_status IN ('editing','returned')
		  AND version=$22::int
	`, current.ID, input.Code, input.Name, input.ShortName, input.OwnerName, input.OwnerContact,
		input.Status, input.Level, input.DeploymentType, input.InternetExposure, input.Description,
		input.ContainsPersonalInformation, input.ContainsSensitivePersonalInformation,
		input.PersonalInformationRecordCount, input.DataVolumeOver100K, input.ContainsImportantData,
		input.ContainsCoreData, input.DataStorageLocation, input.SecurityNotes, customValues, principal.UserID, baseVersion)
	if err != nil {
		return BusinessSystem{}, mapWorkingDB(err)
	}
	if result.RowsAffected() == 0 {
		var currentVersion int
		var status string
		if scanErr := tx.QueryRow(ctx, `SELECT version, draft_status FROM inventory_business_system_drafts WHERE id::text=$1`, current.ID).Scan(&currentVersion, &status); errors.Is(scanErr, pgx.ErrNoRows) {
			return BusinessSystem{}, ErrNotFound
		} else if scanErr != nil {
			return BusinessSystem{}, scanErr
		}
		if status == "editing" || status == "returned" {
			if currentVersion != baseVersion {
				return BusinessSystem{}, ErrEditConflict
			}
			return BusinessSystem{}, ErrConflict
		}
		return BusinessSystem{}, ErrConflict
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'business_system.working_restore','business_system',$3,'以历史版本恢复业务系统工作版本',
		       jsonb_build_object('published_id',$4::text,'version_before',$7::int,'version_after',$7::int + 1),NULLIF($5,'')::inet,NULLIF($6,''))
	`, principal.UserID, input.OrganizationID, current.ID, current.PublishedID, ipAddress, requestID, baseVersion); err != nil {
		return BusinessSystem{}, err
	}
	if err = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventBusinessSystemWorkingVersionRestored, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: input.OrganizationID, ActorUserID: principal.UserID, RequiredPermission: "system.manage",
		ResourceType: "business_system", ResourceID: current.ID, ResourceLabel: workingBusinessSystemLabel(input), BatchKey: requestID,
		Details: map[string]any{"published_id": current.PublishedID},
	}); err != nil {
		return BusinessSystem{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BusinessSystem{}, err
	}
	return s.getWorking(ctx, principal, current.ID)
}

// createRestoredWorking is intentionally separate from the regular daily
// working-version creator: the restore audit must be committed atomically with
// the newly-created draft, otherwise a failed audit could leave a restore that
// the caller cannot reliably explain or retry.
func (s *Service) createRestoredWorking(ctx context.Context, principal auth.Principal, current BusinessSystem, input CreateInput, requestID, ipAddress string) (BusinessSystem, error) {
	if current.OrganizationID != input.OrganizationID {
		return BusinessSystem{}, ErrForbidden
	}
	if err := PrepareCreateInput(&input); err != nil {
		return BusinessSystem{}, err
	}
	raw, err := s.validateWorkingValues(ctx, input.CustomValues)
	if err != nil {
		return BusinessSystem{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystem{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_business_system_drafts(
			task_id,organization_id,operation,source_system_id,source_system_version,system_code,system_name,
			short_name,owner_name,owner_contact,system_status,system_level,deployment_type,
			internet_exposure,description,contains_personal_information,
			contains_sensitive_personal_information,personal_information_record_count,
			data_volume_over_100k,contains_important_data,contains_core_data,
			data_storage_location,security_notes,custom_values,draft_status,created_by,updated_by,version,assigned_to,last_edited_by
		) VALUES(
			NULL,$1::uuid,'update',$2::uuid,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),NULLIF($8,''),
			$9,$10,NULLIF($11,''),$12,NULLIF($13,''),$14,$15,$16,$17,$18,$19,
			NULLIF($20,''),NULLIF($21,''),$22::jsonb,'editing',$23::uuid,$23::uuid,1,$23::uuid,$23::uuid
		) RETURNING id::text
	`, input.OrganizationID, current.ID, current.Version, input.Code, input.Name, input.ShortName,
		input.OwnerName, input.OwnerContact, input.Status, input.Level, input.DeploymentType,
		input.InternetExposure, input.Description, input.ContainsPersonalInformation,
		input.ContainsSensitivePersonalInformation, input.PersonalInformationRecordCount,
		input.DataVolumeOver100K, input.ContainsImportantData, input.ContainsCoreData,
		input.DataStorageLocation, input.SecurityNotes, raw, principal.UserID).Scan(&id)
	if err != nil {
		return BusinessSystem{}, mapWorkingDB(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'business_system.working_restore','business_system',$3,
		       '以历史版本恢复业务系统工作版本',
		       jsonb_build_object('published_id',$4::text,'version_before',0,'version_after',1),NULLIF($5,'')::inet,NULLIF($6,''))
	`, principal.UserID, input.OrganizationID, id, current.ID, ipAddress, requestID); err != nil {
		return BusinessSystem{}, err
	}
	if err = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventBusinessSystemWorkingVersionRestored, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: input.OrganizationID, ActorUserID: principal.UserID, RequiredPermission: "system.manage",
		ResourceType: "business_system", ResourceID: id, ResourceLabel: workingBusinessSystemLabel(input), BatchKey: requestID,
		Details: map[string]any{"published_id": current.ID},
	}); err != nil {
		return BusinessSystem{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return BusinessSystem{}, err
	}
	return s.getWorking(ctx, principal, id)
}

func mapWorkingNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// lockWorkingVersion re-reads the working draft version inside the caller's
// transaction and holds a row lock until commit. The caller must also compare
// this version against the operator-confirmed restore token.
func lockWorkingVersion(ctx context.Context, tx pgx.Tx, id string) (int, error) {
	var version int
	err := tx.QueryRow(ctx, `SELECT version FROM inventory_business_system_drafts WHERE id::text=$1 FOR UPDATE`, id).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return version, nil
}
