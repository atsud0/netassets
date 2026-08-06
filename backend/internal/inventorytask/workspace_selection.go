package inventorytask

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"netassets/internal/auth"
)

// SelectWorkspaceAsset binds an organization-local unreported asset to one
// inventory assignment. The asset remains invisible in the published ledger
// until the existing submit/review/publish pipeline completes.
func (s *Service) SelectWorkspaceAsset(ctx context.Context, p auth.Principal, taskID, organizationID, draftID, requestID, ip string) (AssetDraft, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("asset.manage") || p.HasRole("group_admin") {
		return AssetDraft{}, ErrForbidden
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return AssetDraft{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	assignment, e := s.loadDraftAssignment(ctx, tx, p, taskID, organizationID, true, "network_assets")
	if e != nil {
		return AssetDraft{}, e
	}
	var scope, systemDraftID string
	e = tx.QueryRow(ctx, `SELECT network_scope,COALESCE(business_system_draft_id::text,'') FROM inventory_asset_drafts WHERE id::text=$1 AND organization_id::text=$2 AND task_id IS NULL AND draft_status IN ('editing','returned')`, draftID, organizationID).Scan(&scope, &systemDraftID)
	if errors.Is(e, pgx.ErrNoRows) {
		return AssetDraft{}, ErrNotFound
	}
	if e != nil {
		return AssetDraft{}, e
	}
	if assignment.AssetNetworkScope != "all" && assignment.AssetNetworkScope != scope {
		return AssetDraft{}, ErrInvalid
	}
	if systemDraftID != "" {
		if _, e = s.loadDraftAssignment(ctx, tx, p, taskID, organizationID, true, "business_systems"); e != nil {
			return AssetDraft{}, ErrInvalid
		}
		r, e := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET task_id=$2::uuid,selected_at=now(),updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned')`, systemDraftID, taskID, p.UserID, organizationID)
		if e != nil {
			return AssetDraft{}, e
		}
		if r.RowsAffected() == 0 {
			return AssetDraft{}, ErrInvalid
		}
	}
	r, e := tx.Exec(ctx, `UPDATE inventory_asset_drafts SET task_id=$2::uuid,selected_at=now(),updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned')`, draftID, taskID, p.UserID, organizationID)
	if e != nil {
		return AssetDraft{}, e
	}
	if r.RowsAffected() == 0 {
		return AssetDraft{}, ErrState
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'inventory.workspace_asset.select','inventory_asset_draft',$3,'选择未上报网络资产纳入填报任务',jsonb_build_object('task_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, organizationID, draftID, taskID, ip, requestID); e != nil {
		return AssetDraft{}, fmt.Errorf("audit workspace asset selection: %w", e)
	}
	if e = tx.Commit(ctx); e != nil {
		return AssetDraft{}, e
	}
	return loadAssetDraft(ctx, s.pool, draftID)
}

func (s *Service) SelectWorkspaceBusinessSystem(ctx context.Context, p auth.Principal, taskID, organizationID, draftID, requestID, ip string) (BusinessSystemDraft, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("system.manage") || p.HasRole("group_admin") {
		return BusinessSystemDraft{}, ErrForbidden
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return BusinessSystemDraft{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, e = s.loadDraftAssignment(ctx, tx, p, taskID, organizationID, true, "business_systems"); e != nil {
		return BusinessSystemDraft{}, e
	}
	r, e := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET task_id=$2::uuid,selected_at=now(),updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned')`, draftID, taskID, p.UserID, organizationID)
	if e != nil {
		return BusinessSystemDraft{}, e
	}
	if r.RowsAffected() == 0 {
		return BusinessSystemDraft{}, ErrState
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'inventory.workspace_system.select','inventory_business_system_draft',$3,'选择未上报业务系统纳入填报任务',jsonb_build_object('task_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, organizationID, draftID, taskID, ip, requestID); e != nil {
		return BusinessSystemDraft{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return BusinessSystemDraft{}, e
	}
	return loadBusinessSystemDraft(ctx, s.pool, draftID)
}
