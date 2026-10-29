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

type WorkspaceSelectionInput struct {
	ExpectedVersion int `json:"expected_version"`
}

type WorkspaceSelectionItem struct {
	ID              string `json:"id"`
	ExpectedVersion int    `json:"expected_version"`
}

type WorkspaceSelectionBatchInput struct {
	Items []WorkspaceSelectionItem `json:"items"`
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
func (s *Service) SelectWorkspaceAsset(ctx context.Context, p auth.Principal, taskID, organizationID, draftID string, expectedVersion int, requestID, ip string) (AssetDraft, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("asset.manage") || p.HasRole("group_admin") {
		return AssetDraft{}, ErrForbidden
	}
	if expectedVersion <= 0 {
		return AssetDraft{}, invalidInput("选择资产必须提供大于 0 的 expected_version")
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
	var currentVersion int
	e = tx.QueryRow(ctx, `SELECT network_scope,COALESCE(business_system_draft_id::text,''),version FROM inventory_asset_drafts WHERE id::text=$1 AND organization_id::text=$2 AND task_id IS NULL AND draft_status IN ('editing','returned')`, draftID, organizationID).Scan(&scope, &systemDraftID, &currentVersion)
	if errors.Is(e, pgx.ErrNoRows) {
		return AssetDraft{}, ErrNotFound
	}
	if e != nil {
		return AssetDraft{}, e
	}
	if currentVersion != expectedVersion {
		return AssetDraft{}, ErrEditConflict
	}
	if assignment.AssetNetworkScope != "all" && assignment.AssetNetworkScope != scope {
		return AssetDraft{}, invalidInput("资产网络范围与本次填报任务要求不一致")
	}
	if systemDraftID != "" {
		var selectedTaskID string
		var systemVersion int
		e = tx.QueryRow(ctx, `SELECT COALESCE(task_id::text,''), version FROM inventory_business_system_drafts WHERE id::text=$1 AND organization_id::text=$2 AND draft_status IN ('editing','returned') FOR UPDATE`, systemDraftID, organizationID).Scan(&selectedTaskID, &systemVersion)
		if errors.Is(e, pgx.ErrNoRows) {
			return AssetDraft{}, invalidInput("关联业务系统工作版本不存在或已不可编辑，请重新选择关联业务系统")
		}
		if e != nil {
			return AssetDraft{}, e
		}
		switch selectedTaskID {
		case "":
			if _, e = s.loadDraftAssignment(ctx, tx, p, taskID, organizationID, true, "business_systems"); e != nil {
				if errors.Is(e, ErrState) {
					return AssetDraft{}, invalidInput("关联业务系统尚未上报，当前任务不允许同时纳入该业务系统")
				}
				return AssetDraft{}, e
			}
			// The dependency draft is bound to this task in the same transaction as
			// the asset. Its version is read and matched inside that transaction so
			// a concurrent selection cannot be silently overwritten.
			r, updateErr := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET task_id=$2::uuid,selected_at=now(),version=version+1,last_edited_by=$3::uuid,updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned') AND version=$5::int`, systemDraftID, taskID, p.UserID, organizationID, systemVersion)
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
			return AssetDraft{}, invalidInput("关联业务系统已纳入其他填报任务，不能随此资产重复选择")
		}
	}
	r, e := tx.Exec(ctx, `UPDATE inventory_asset_drafts SET task_id=$2::uuid,selected_at=now(),version=version+1,last_edited_by=$3::uuid,updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned') AND version=$5`, draftID, taskID, p.UserID, organizationID, expectedVersion)
	if e != nil {
		return AssetDraft{}, e
	}
	if r.RowsAffected() == 0 {
		return AssetDraft{}, ErrEditConflict
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'inventory.workspace_asset.select','inventory_asset_draft',$3,'选择未上报网络资产纳入填报任务',jsonb_build_object('task_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, organizationID, draftID, taskID, ip, requestID); e != nil {
		return AssetDraft{}, fmt.Errorf("audit workspace asset selection: %w", e)
	}
	if e = tx.Commit(ctx); e != nil {
		return AssetDraft{}, e
	}
	return loadAssetDraft(ctx, s.pool, draftID)
}

func (s *Service) SelectWorkspaceBusinessSystem(ctx context.Context, p auth.Principal, taskID, organizationID, draftID string, expectedVersion int, requestID, ip string) (BusinessSystemDraft, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("system.manage") || p.HasRole("group_admin") {
		return BusinessSystemDraft{}, ErrForbidden
	}
	if expectedVersion <= 0 {
		return BusinessSystemDraft{}, invalidInput("选择业务系统必须提供大于 0 的 expected_version")
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return BusinessSystemDraft{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, e = s.loadDraftAssignment(ctx, tx, p, taskID, organizationID, true, "business_systems"); e != nil {
		return BusinessSystemDraft{}, e
	}
	r, e := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET task_id=$2::uuid,selected_at=now(),version=version+1,last_edited_by=$3::uuid,updated_by=$3::uuid,updated_at=now() WHERE id::text=$1 AND organization_id::text=$4 AND task_id IS NULL AND draft_status IN ('editing','returned') AND version=$5`, draftID, taskID, p.UserID, organizationID, expectedVersion)
	if e != nil {
		return BusinessSystemDraft{}, e
	}
	if r.RowsAffected() == 0 {
		return BusinessSystemDraft{}, ErrEditConflict
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
	items, err := normalizeWorkspaceSelectionItems(input.Items)
	if err != nil {
		return WorkspaceSelectionBatchResult{}, err
	}
	result := WorkspaceSelectionBatchResult{TotalCount: len(items), Items: make([]WorkspaceSelectionBatchItem, 0, len(items))}
	for _, item := range items {
		_, itemErr := s.SelectWorkspaceAsset(ctx, p, taskID, organizationID, item.ID, item.ExpectedVersion, requestID, ip)
		if itemErr == nil {
			result.SuccessCount++
			result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: item.ID, Success: true, Message: "已纳入本次上报"})
			continue
		}
		result.FailureCount++
		code, message := workspaceSelectionError(itemErr)
		result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: item.ID, Code: code, Message: message})
	}
	return result, nil
}

func (s *Service) SelectWorkspaceBusinessSystemsBatch(ctx context.Context, p auth.Principal, taskID, organizationID string, input WorkspaceSelectionBatchInput, requestID, ip string) (WorkspaceSelectionBatchResult, error) {
	if !p.HasPermission("inventory.manage") || !p.HasPermission("system.manage") || p.HasRole("group_admin") {
		return WorkspaceSelectionBatchResult{}, ErrForbidden
	}
	items, err := normalizeWorkspaceSelectionItems(input.Items)
	if err != nil {
		return WorkspaceSelectionBatchResult{}, err
	}
	result := WorkspaceSelectionBatchResult{TotalCount: len(items), Items: make([]WorkspaceSelectionBatchItem, 0, len(items))}
	for _, item := range items {
		_, itemErr := s.SelectWorkspaceBusinessSystem(ctx, p, taskID, organizationID, item.ID, item.ExpectedVersion, requestID, ip)
		if itemErr == nil {
			result.SuccessCount++
			result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: item.ID, Success: true, Message: "已纳入本次上报"})
			continue
		}
		result.FailureCount++
		code, message := workspaceSelectionError(itemErr)
		result.Items = append(result.Items, WorkspaceSelectionBatchItem{ID: item.ID, Code: code, Message: message})
	}
	return result, nil
}

func normalizeWorkspaceSelectionItems(values []WorkspaceSelectionItem) ([]WorkspaceSelectionItem, error) {
	if len(values) == 0 {
		return nil, invalidInput("请至少选择一项需要上报的资源")
	}
	if len(values) > MaxWorkspaceSelectionItems {
		return nil, invalidInput("单次最多选择 200 项需要上报的资源")
	}
	items := make([]WorkspaceSelectionItem, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		id := strings.TrimSpace(value.ID)
		if id == "" {
			return nil, invalidInput(fmt.Sprintf("第 %d 项资源标识不能为空", index+1))
		}
		if value.ExpectedVersion <= 0 {
			return nil, invalidInput(fmt.Sprintf("第 %d 项资源必须提供大于 0 的 expected_version", index+1))
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		items = append(items, WorkspaceSelectionItem{ID: id, ExpectedVersion: value.ExpectedVersion})
	}
	return items, nil
}

func workspaceSelectionError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权选择该资源"
	case errors.Is(err, ErrNotFound):
		return "not_found", "未上报工作版本不存在或已被其他操作处理"
	case errors.Is(err, ErrEditConflict):
		return "draft_edit_conflict", "资源内容已被其他人更新，请刷新后重试"
	case errors.Is(err, ErrState):
		return "invalid_state", "资源已纳入任务或当前任务状态不允许选择"
	case errors.Is(err, ErrInvalid):
		var validationError *InputValidationError
		if errors.As(err, &validationError) {
			return "invalid", validationError.Message
		}
		return "invalid", "资源不符合任务类型、网络范围或关联资源要求"
	default:
		return "internal_error", "选择失败，请稍后重试"
	}
}
