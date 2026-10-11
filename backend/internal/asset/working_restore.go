package asset

import (
	"context"
	"errors"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/inventorydraft"
	"netassets/internal/notification"
)

// RestorePublishedVersion creates or replaces the organization-side daily
// working version for one published asset. It never changes the published
// ledger directly and refuses to overwrite a version already bound to an
// inventory task.
func (s *Service) RestorePublishedVersion(ctx context.Context, principal auth.Principal, assetID string, input CreateInput, requestID, ipAddress string) (Asset, error) {
	if principal.HasRole("group_admin") || !principal.HasPermission("asset.manage") {
		return Asset{}, ErrForbidden
	}
	current, err := s.Get(ctx, principal, assetID)
	if err != nil {
		return Asset{}, err
	}
	if current.IsWorkingCopy {
		return Asset{}, ErrInvalid
	}
	if input.OrganizationID != current.OrganizationID {
		return Asset{}, ErrForbidden
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, current.OrganizationID, "asset.manage")
	if err != nil {
		return Asset{}, err
	}
	if !allowed {
		return Asset{}, ErrForbidden
	}

	working, err := s.getWorkingBySource(ctx, principal, current.ID)
	switch {
	case err == nil:
		if working.TaskID != "" || working.DraftStatus == "submitted" {
			return Asset{}, ErrTaskBound
		}
		return s.restoreWorking(ctx, principal, working, input, requestID, ipAddress)
	case !errors.Is(err, ErrNotFound):
		return Asset{}, err
	default:
		return s.createRestoredWorking(ctx, principal, current, input, requestID, ipAddress)
	}
}

func (s *Service) restoreWorking(ctx context.Context, principal auth.Principal, current Asset, input CreateInput, requestID, ipAddress string) (Asset, error) {
	if current.TaskID != "" || current.DraftStatus == "submitted" {
		return Asset{}, ErrTaskBound
	}
	if current.OrganizationID != input.OrganizationID {
		return Asset{}, ErrForbidden
	}
	normalizedValue, err := PrepareCreateInput(&input)
	if err != nil {
		return Asset{}, err
	}
	customValues, err := s.validateWorkingValues(ctx, input.CustomValues)
	if err != nil {
		return Asset{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	businessSystemID, businessSystemDraftID, err := validateUnreportedReferences(ctx, tx, input)
	if err != nil {
		return Asset{}, err
	}
	// A restore rewrites the working version from a historical revision, which
	// may carry a different type or value than the current draft.
	if err = s.ensureWorkingValueAvailable(ctx, tx, input.Type, normalizedValue, current.ID); err != nil {
		return Asset{}, err
	}
	result, err := tx.Exec(ctx, `
		UPDATE inventory_asset_drafts
		SET operation='update', asset_type=$2, asset_value=$3, normalized_value=$4,
		    asset_name=NULLIF($5,''), environment=$6, network_scope=$7,
		    network_area_id=NULLIF($8,'')::uuid, business_system_id=NULLIF($9,'')::uuid,
		    business_system_draft_id=NULLIF($10,'')::uuid, status=$11, importance=$12,
		    owner_user_id=NULLIF($13,'')::uuid, owner_name=NULLIF($14,''), notes=NULLIF($15,''),
		    expires_at=$16, custom_values=$17::jsonb, draft_status='editing',
		    updated_by=$18::uuid, updated_at=now()
		WHERE id::text=$1 AND task_id IS NULL AND source_asset_id IS NOT NULL
		  AND draft_status IN `+inventorydraft.EditableSQL+`
		  AND ($19::timestamptz IS NULL OR updated_at=$19::timestamptz)
	`, current.ID, input.Type, input.Value, normalizedValue, input.Name, input.Environment, input.NetworkScope,
		input.NetworkAreaID, businessSystemID, businessSystemDraftID, input.Status, input.Importance,
		input.OwnerUserID, input.OwnerName, input.Notes, input.ExpiresAt, customValues, principal.UserID, input.ExpectedUpdatedAt)
	if err != nil {
		return Asset{}, mapUnreportedDB(err)
	}
	if result.RowsAffected() == 0 {
		if input.ExpectedUpdatedAt != nil {
			return Asset{}, ErrEditConflict
		}
		return Asset{}, ErrTaskBound
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'asset.working_restore','asset',$3,'以历史版本恢复网络资产工作版本',
		       jsonb_build_object('published_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))
	`, principal.UserID, input.OrganizationID, current.ID, current.PublishedID, ipAddress, requestID); err != nil {
		return Asset{}, err
	}
	if err = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetWorkingVersionRestored, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: input.OrganizationID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: current.ID, ResourceLabel: workingAssetLabel(input), BatchKey: requestID,
		Details: map[string]any{"published_id": current.PublishedID},
	}); err != nil {
		return Asset{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Asset{}, err
	}
	return s.getWorking(ctx, principal, current.ID)
}

// createRestoredWorking keeps the restore audit atomic with the new draft.
// The regular daily editor uses a different audit action, so restore creation
// must not delegate to it and lose the reason for the change.
func (s *Service) createRestoredWorking(ctx context.Context, principal auth.Principal, current Asset, input CreateInput, requestID, ipAddress string) (Asset, error) {
	if current.OrganizationID != input.OrganizationID {
		return Asset{}, ErrForbidden
	}
	normalized, err := PrepareCreateInput(&input)
	if err != nil {
		return Asset{}, err
	}
	raw, err := s.validateWorkingValues(ctx, input.CustomValues)
	if err != nil {
		return Asset{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bs, bsd, err := validateUnreportedReferences(ctx, tx, input)
	if err != nil {
		return Asset{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_asset_drafts(
			task_id,organization_id,operation,source_asset_id,asset_type,asset_value,
			normalized_value,asset_name,environment,network_scope,network_area_id,
			business_system_id,business_system_draft_id,status,importance,source,
			owner_user_id,owner_name,notes,expires_at,custom_values,draft_status,
			created_by,updated_by
		) VALUES(
			NULL,$1::uuid,'update',$2::uuid,$3,$4,$5,NULLIF($6,''),$7,$8,
			NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,NULLIF($11,'')::uuid,$12,$13,$14,
			NULLIF($15,'')::uuid,NULLIF($16,''),NULLIF($17,''),$18,$19::jsonb,
			'editing',$20::uuid,$20::uuid
		) RETURNING id::text
	`, input.OrganizationID, current.ID, input.Type, input.Value, normalized, input.Name,
		input.Environment, input.NetworkScope, input.NetworkAreaID, bs, bsd, input.Status,
		input.Importance, current.Source, input.OwnerUserID, input.OwnerName, input.Notes,
		input.ExpiresAt, raw, principal.UserID).Scan(&id)
	if err != nil {
		return Asset{}, mapUnreportedDB(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'asset.working_restore','asset',$3,
		       '以历史版本恢复网络资产工作版本',
		       jsonb_build_object('published_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))
	`, principal.UserID, input.OrganizationID, id, current.ID, ipAddress, requestID); err != nil {
		return Asset{}, err
	}
	if err = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetWorkingVersionRestored, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: input.OrganizationID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: id, ResourceLabel: workingAssetLabel(input), BatchKey: requestID,
		Details: map[string]any{"published_id": current.ID},
	}); err != nil {
		return Asset{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Asset{}, err
	}
	return s.getWorking(ctx, principal, id)
}
