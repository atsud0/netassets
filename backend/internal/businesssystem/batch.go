package businesssystem

import (
	"context"
	"errors"
	"strings"

	"netassets/internal/auth"
)

const MaxBatchItems = 200

type BatchArchiveInput struct {
	IDs      []string `json:"ids"`
	Archived bool     `json:"archived"`
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
	if !principal.HasPermission("system.manage") {
		return BatchOperationResult{}, ErrForbidden
	}
	ids, err := normalizeBatchIDs(input.IDs)
	if err != nil {
		return BatchOperationResult{}, err
	}
	result := BatchOperationResult{TotalCount: len(ids), Items: make([]BatchOperationItem, 0, len(ids))}
	action := "归档"
	if !input.Archived {
		action = "恢复"
	}
	for _, id := range ids {
		item, itemErr := s.SetArchived(ctx, principal, id, input.Archived, requestID, ipAddress)
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

func normalizeBatchIDs(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > MaxBatchItems {
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

func businessSystemBatchError(err error, action string) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权" + action + "该业务系统"
	case errors.Is(err, ErrNotFound):
		return "not_found", "业务系统不存在"
	case errors.Is(err, ErrInvalid):
		return "invalid_id", "业务系统编号不正确"
	default:
		return "internal_error", action + "失败，请稍后重试"
	}
}
