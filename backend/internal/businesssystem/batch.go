package businesssystem

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"netassets/internal/auth"
)

const MaxBatchItems = 200

type BatchArchiveInput struct {
	Items    []BatchArchiveItem `json:"items"`
	Archived bool               `json:"archived"`
}

type BatchArchiveItem struct {
	ID              string `json:"id"`
	ExpectedVersion int    `json:"expected_version"`
}

type BatchOperationItem struct {
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message"`
	Item    *BusinessSystem `json:"item,omitempty"`
}

type BatchOperationResult struct {
	TotalCount   int                  `json:"total_count"`
	SuccessCount int                  `json:"success_count"`
	FailureCount int                  `json:"failure_count"`
	Items        []BatchOperationItem `json:"items"`
}

func (s *Service) SetArchivedBatch(ctx context.Context, principal auth.Principal, input BatchArchiveInput, requestID, ipAddress string) (BatchOperationResult, error) {
	// Lifecycle changes affect the formal system record and intentionally do
	// not go through organization working versions. Reject the whole batch at
	// the boundary for non-group admins, rather than disguising authorization
	// failures as per-item validation failures.
	if !principal.HasRole(auth.RoleGroupAdmin) || !principal.HasPermission("system.manage") {
		return BatchOperationResult{}, ErrForbidden
	}
	items, err := normalizeBatchArchiveItems(input.Items)
	if err != nil {
		return BatchOperationResult{}, err
	}
	result := BatchOperationResult{TotalCount: len(items), Items: make([]BatchOperationItem, 0, len(items))}
	action := "归档"
	if !input.Archived {
		action = "恢复"
	}
	for _, target := range items {
		id := target.ID
		item, itemErr := s.SetArchived(ctx, principal, id, input.Archived, &target.ExpectedVersion, requestID, ipAddress)
		if itemErr == nil {
			result.SuccessCount++
			itemCopy := item
			result.Items = append(result.Items, BatchOperationItem{ID: id, Success: true, Message: action + "成功", Item: &itemCopy})
			continue
		}
		result.FailureCount++
		code, message := businessSystemBatchError(itemErr, action)
		result.Items = append(result.Items, BatchOperationItem{ID: id, Code: code, Message: message})
	}
	return result, nil
}

func normalizeBatchArchiveItems(values []BatchArchiveItem) ([]BatchArchiveItem, error) {
	if len(values) == 0 {
		return nil, invalidInput("批量处理业务系统：请至少选择一个业务系统")
	}
	if len(values) > MaxBatchItems {
		return nil, invalidInput(fmt.Sprintf("批量处理业务系统：单次最多选择 %d 个业务系统", MaxBatchItems))
	}
	items := make([]BatchArchiveItem, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		id := strings.TrimSpace(value.ID)
		if id == "" {
			return nil, invalidInput(fmt.Sprintf("批量处理业务系统：第 %d 项业务系统标识不能为空", index+1))
		}
		if value.ExpectedVersion <= 0 {
			return nil, invalidInput(fmt.Sprintf("批量处理业务系统：第 %d 项缺少有效版本号，请刷新后重试", index+1))
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		value.ID = id
		items = append(items, value)
	}
	if len(items) == 0 {
		return nil, invalidInput("批量处理业务系统：请至少选择一个业务系统")
	}
	return items, nil
}

func businessSystemBatchError(err error, action string) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权" + action + "该业务系统"
	case errors.Is(err, ErrNotFound):
		return "not_found", "业务系统不存在"
	case errors.Is(err, ErrInvalid):
		var validation *InputValidationError
		if errors.As(err, &validation) {
			return "invalid_id", validation.Message
		}
		return "invalid_id", "业务系统编号不正确"
	case errors.Is(err, ErrVersionRequired):
		return "expected_version_required", "缺少业务系统版本，请刷新后重试"
	case errors.Is(err, ErrFormalVersionConflict):
		return "formal_version_conflict", "业务系统已被其他人修改，请刷新后重试"
	default:
		return "internal_error", action + "失败，请稍后重试"
	}
}
