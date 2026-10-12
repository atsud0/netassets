package inventorytask

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
)

// DraftTakeoverInput is intentionally separate from the content payload. A
// takeover is a responsibility change and must itself be protected by CAS.
type DraftTakeoverInput struct {
	ExpectedVersion int    `json:"expected_version"`
	Reason          string `json:"reason"`
}

func validateTakeover(principal auth.Principal, permission string, input DraftTakeoverInput) error {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission(permission) || principal.HasRole("group_admin") || !principal.HasRole("organization_admin") {
		return ErrForbidden
	}
	if input.ExpectedVersion <= 0 || strings.TrimSpace(input.Reason) == "" {
		return ErrInvalid
	}
	return nil
}

func (s *Service) TakeoverAssetDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID string, input DraftTakeoverInput, requestID, ipAddress string) (AssetDraft, error) {
	if err := validateTakeover(principal, "asset.manage", input); err != nil {
		return AssetDraft{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AssetDraft{}, fmt.Errorf("begin asset draft takeover: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "network_assets"); err != nil {
		return AssetDraft{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE inventory_asset_drafts
		SET assigned_to=$4::uuid,last_edited_by=$4::uuid,takeover_reason=$5,taken_over_at=now(),version=version+1,updated_by=$4::uuid,updated_at=now()
		WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3 AND draft_status IN ('editing','returned') AND version=$6`,
		draftID, taskID, organizationID, principal.UserID, strings.TrimSpace(input.Reason), input.ExpectedVersion)
	if err != nil {
		return AssetDraft{}, mapDraftDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return AssetDraft{}, draftCASFailure(ctx, tx, "inventory_asset_drafts", draftID, taskID, organizationID, input.ExpectedVersion)
	}
	if err := writeDraftTakeoverAudit(ctx, tx, principal, "asset", organizationID, taskID, draftID, strings.TrimSpace(input.Reason), requestID, ipAddress); err != nil {
		return AssetDraft{}, err
	}
	item, err := loadAssetDraft(ctx, tx, draftID)
	if err != nil {
		return AssetDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDraft{}, fmt.Errorf("commit asset draft takeover: %w", err)
	}
	return item, nil
}

func (s *Service) TakeoverBusinessSystemDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID string, input DraftTakeoverInput, requestID, ipAddress string) (BusinessSystemDraft, error) {
	if err := validateTakeover(principal, "system.manage", input); err != nil {
		return BusinessSystemDraft{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("begin business system draft takeover: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "business_systems"); err != nil {
		return BusinessSystemDraft{}, err
	}
	result, err := tx.Exec(ctx, `UPDATE inventory_business_system_drafts
		SET assigned_to=$4::uuid,last_edited_by=$4::uuid,takeover_reason=$5,taken_over_at=now(),version=version+1,updated_by=$4::uuid,updated_at=now()
		WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3 AND draft_status IN ('editing','returned') AND version=$6`,
		draftID, taskID, organizationID, principal.UserID, strings.TrimSpace(input.Reason), input.ExpectedVersion)
	if err != nil {
		return BusinessSystemDraft{}, mapBusinessSystemDraftDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return BusinessSystemDraft{}, draftCASFailure(ctx, tx, "inventory_business_system_drafts", draftID, taskID, organizationID, input.ExpectedVersion)
	}
	if err := writeDraftTakeoverAudit(ctx, tx, principal, "business_system", organizationID, taskID, draftID, strings.TrimSpace(input.Reason), requestID, ipAddress); err != nil {
		return BusinessSystemDraft{}, err
	}
	item, err := loadBusinessSystemDraft(ctx, tx, draftID)
	if err != nil {
		return BusinessSystemDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BusinessSystemDraft{}, fmt.Errorf("commit business system draft takeover: %w", err)
	}
	return item, nil
}

func draftCASFailure(ctx context.Context, tx pgx.Tx, table, draftID, taskID, organizationID string, expectedVersion int) error {
	var currentVersion int
	var status string
	err := tx.QueryRow(ctx, `SELECT version, draft_status FROM `+table+` WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3`, draftID, taskID, organizationID).Scan(&currentVersion, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("check draft version conflict: %w", err)
	}
	if status != "editing" && status != "returned" {
		return ErrState
	}
	if currentVersion != expectedVersion {
		return ErrEditConflict
	}
	// The caller's UPDATE has additional predicates (for example operation or
	// selection state). The row is otherwise current and editable, so this is a
	// state conflict rather than a stale-version conflict.
	return ErrState
}
