package inventorytask

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
)

// RestoreAssetDraft reopens a discarded task draft without deleting its audit
// history. Only the organization administrator may perform this recovery; a
// contributor must not be able to resurrect a record after it was discarded.
func (s *Service) RestoreAssetDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID string, expectedVersion int, requestID, ipAddress string) (AssetDraft, error) {
	if err := validateDraftRestore(principal, "asset.manage", expectedVersion); err != nil {
		return AssetDraft{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AssetDraft{}, fmt.Errorf("begin asset draft restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "network_assets"); err != nil {
		return AssetDraft{}, err
	}
	if err := ensureDraftOrganizationAdmin(ctx, tx, principal, "inventory_asset_drafts", draftID, organizationID); err != nil {
		return AssetDraft{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE inventory_asset_drafts
		SET draft_status='editing', version=version+1, assigned_to=$4::uuid,
		    last_edited_by=$4::uuid, updated_by=$4::uuid, updated_at=now()
		WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3
		  AND draft_status='discarded' AND version=$5`, draftID, taskID, organizationID, principal.UserID, expectedVersion)
	if err != nil {
		return AssetDraft{}, mapDraftDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return AssetDraft{}, draftCASFailure(ctx, tx, "inventory_asset_drafts", draftID, taskID, organizationID, expectedVersion)
	}
	if err := writeDraftAudit(ctx, tx, principal, organizationID, taskID, draftID, "inventory_asset_draft.restore", "恢复填报资产草稿", "", requestID, ipAddress); err != nil {
		return AssetDraft{}, err
	}
	item, err := loadAssetDraft(ctx, tx, draftID)
	if err != nil {
		return AssetDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDraft{}, fmt.Errorf("commit asset draft restore: %w", err)
	}
	return item, nil
}

// RestoreBusinessSystemDraft is the business-system counterpart of
// RestoreAssetDraft. It follows the same ownership and CAS rules.
func (s *Service) RestoreBusinessSystemDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID string, expectedVersion int, requestID, ipAddress string) (BusinessSystemDraft, error) {
	if err := validateDraftRestore(principal, "system.manage", expectedVersion); err != nil {
		return BusinessSystemDraft{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("begin business system draft restore: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "business_systems"); err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := ensureDraftOrganizationAdmin(ctx, tx, principal, "inventory_business_system_drafts", draftID, organizationID); err != nil {
		return BusinessSystemDraft{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE inventory_business_system_drafts
		SET draft_status='editing', version=version+1, assigned_to=$4::uuid,
		    last_edited_by=$4::uuid, updated_by=$4::uuid, updated_at=now()
		WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3
		  AND draft_status='discarded' AND version=$5`, draftID, taskID, organizationID, principal.UserID, expectedVersion)
	if err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return BusinessSystemDraft{}, draftCASFailure(ctx, tx, "inventory_business_system_drafts", draftID, taskID, organizationID, expectedVersion)
	}
	if err := writeBusinessSystemDraftAudit(ctx, tx, principal, organizationID, taskID, draftID, "inventory.business_system_draft.restore", "恢复填报业务系统草稿", "", requestID, ipAddress); err != nil {
		return BusinessSystemDraft{}, err
	}
	item, err := loadBusinessSystemDraft(ctx, tx, draftID)
	if err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("commit business system draft restore: %w", err)
	}
	return item, nil
}

func validateDraftRestore(principal auth.Principal, permission string, expectedVersion int) error {
	if !principal.HasRole("organization_admin") || !principal.HasPermission("inventory.manage") || !principal.HasPermission(permission) {
		return ErrForbidden
	}
	if expectedVersion <= 0 {
		return invalidInput("恢复工作版本必须提供大于 0 的 expected_version")
	}
	return nil
}

func ensureDraftOrganizationAdmin(ctx context.Context, q queryRower, principal auth.Principal, table, draftID, organizationID string) error {
	if !principal.HasRole("organization_admin") {
		return ErrForbidden
	}
	var draftOrganization string
	if err := q.QueryRow(ctx, `SELECT organization_id::text FROM `+table+` WHERE id::text=$1`, strings.TrimSpace(draftID)).Scan(&draftOrganization); err == pgx.ErrNoRows {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("load draft restore organization: %w", err)
	}
	if draftOrganization != organizationID {
		return ErrForbidden
	}
	return nil
}
