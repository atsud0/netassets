package inventorytask

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"netassets/internal/auth"
)

const MaxWorkspaceSelectionItems = 200

type WorkspaceSelectionBatchInput struct {
	IDs []string `json:"ids"`
}

type WorkspaceSelectionBatchItem struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
}

type WorkspaceSelectionBatchResult struct {
	TotalCount   int                           `json:"total_count"`
	SuccessCount int                           `json:"success_count"`
	FailureCount int                           `json:"failure_count"`
	Items        []WorkspaceSelectionBatchItem `json:"items"`
}

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
		var selectedTaskID string
		e = tx.QueryRow(ctx, `SELECT COALESCE(task_id::text,'') FROM inventory_business_system_drafts WHERE id::text=$1 AND organization_id::text=$2 AND draft_status IN ('editing','returned')`, systemDraftID, organizationID).Scan(&selectedTaskID)
		if errors.Is(e, pgx.ErrNoRows) {
			return AssetDraft{}, ErrInvalid
		}
		if e != nil {
			return AssetDraft{}, e
		}
		switch selectedTaskID {
		case "":
			if _, e = s.loadDraftAssignment(ctx, tx, p, taskID, organizationID, true, "business_systems"); e != nil {
				return AssetDraft{}, ErrInvalid
			}
			r, updateErr := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET task_id=$2::uuid,selected_at=now(),updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned')`, systemDraftID, taskID, p.UserID, organizationID)
			if updateErr != nil {
				return AssetDraft{}, updateErr
			}
			if r.RowsAffected() == 0 {
				return AssetDraft{}, ErrState
			}
		case taskID:
			// Multiple assets may reference the same unreported business system.
			// Once the first asset has selected it into this task, subsequent
			// assets can reuse that dependency without treating it as a conflict.
		default:
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

func (s *Service) SelectWorkspaceAssetsBatch(ctx context.Context, p auth.Principal, taskID, organizationID string, input WorkspaceSelectionBatchInput, requestID, ip string) (WorkspaceSelectionBatchResult, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("asset.manage") || p.HasRole("group_admin") {
		return WorkspaceSelectionBatchResult{}, ErrForbidden
	}
	ids, err := normalizeWorkspaceSelectionIDs(input.IDs)
	if err != nil {
		return WorkspaceSelectionBatchResult{}, err
	}
	result := WorkspaceSelectionBatchResult{TotalCount: len(ids), Items: make([]WorkspaceSelectionBatchItem, 0, len(ids))}
	for _, id := range ids {
		_, itemErr := s.SelectWorkspaceAsset(ctx, p, taskID, organizationID, id, requestID, ip)
		if itemErr == nil {
			result.SuccessCount++
			result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: id, Success: true, Message: "已纳入本次上报"})
			continue
		}
		result.FailureCount++
		code, message := workspaceSelectionError(itemErr)
		result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: id, Code: code, Message: message})
	}
	return result, nil
}

func (s *Service) SelectWorkspaceBusinessSystemsBatch(ctx context.Context, p auth.Principal, taskID, organizationID string, input WorkspaceSelectionBatchInput, requestID, ip string) (WorkspaceSelectionBatchResult, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("system.manage") || p.HasRole("group_admin") {
		return WorkspaceSelectionBatchResult{}, ErrForbidden
	}
	ids, err := normalizeWorkspaceSelectionIDs(input.IDs)
	if err != nil {
		return WorkspaceSelectionBatchResult{}, err
	}
	result := WorkspaceSelectionBatchResult{TotalCount: len(ids), Items: make([]WorkspaceSelectionBatchItem, 0, len(ids))}
	for _, id := range ids {
		_, itemErr := s.SelectWorkspaceBusinessSystem(ctx, p, taskID, organizationID, id, requestID, ip)
		if itemErr == nil {
			result.SuccessCount++
			result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: id, Success: true, Message: "已纳入本次上报"})
			continue
		}
		result.FailureCount++
		code, message := workspaceSelectionError(itemErr)
		result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: id, Code: code, Message: message})
	}
	return result, nil
}

func normalizeWorkspaceSelectionIDs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > MaxWorkspaceSelectionItems {
		return nil, ErrInvalid
	}
	ids := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		id := strings.TrimSpace(value)
		if id == "" {
			return nil, ErrInvalid
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, ErrInvalid
	}
	return ids, nil
}

func workspaceSelectionError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权选择该资源"
	case errors.Is(err, ErrNotFound):
		return "not_found", "未上报工作版本不存在或已被其他操作处理"
	case errors.Is(err, ErrState):
		return "invalid_state", "资源已纳入任务或当前任务状态不允许选择"
	case errors.Is(err, ErrInvalid):
		return "invalid", "资源不符合任务类型、网络范围或关联资源要求"
	default:
		return "internal_error", "选择失败，请稍后重试"
	}
}
