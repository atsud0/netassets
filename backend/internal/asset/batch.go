package asset

import (
	"context"
	"errors"
	"strings"

	"netassets/internal/auth"
)

const MaxBatchItems = 200

type BatchUpdateInput struct {
	IDs           []string `json:"ids"`
	Status        *string  `json:"status"`
	NetworkAreaID *string  `json:"network_area_id"`
}

type BatchOperationItem struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Item    *Asset `json:"item,omitempty"`
}

type BatchOperationResult struct {
	TotalCount   int                  `json:"total_count"`
	SuccessCount int                  `json:"success_count"`
	FailureCount int                  `json:"failure_count"`
	Items        []BatchOperationItem `json:"items"`
}

func (s *Service) UpdateBatch(ctx context.Context, principal auth.Principal, input BatchUpdateInput, requestID, ipAddress string) (BatchOperationResult, error) {
	if !principal.HasPermission("asset.manage") {
		return BatchOperationResult{}, ErrForbidden
	}
	ids, status, networkAreaID, err := normalizeBatchUpdateInput(input)
	if err != nil {
		return BatchOperationResult{}, err
	}
	result := BatchOperationResult{TotalCount: len(ids), Items: make([]BatchOperationItem, 0, len(ids))}
	for _, id := range ids {
		current, itemErr := s.Get(ctx, principal, id)
		if itemErr == nil {
			updateInput := createInputFromAsset(current)
			if status != nil {
				updateInput.Status = *status
			}
			if networkAreaID != nil {
				updateInput.NetworkAreaID = *networkAreaID
			}
			var updated Asset
			updated, itemErr = s.Update(ctx, principal, id, updateInput, requestID, ipAddress)
			if itemErr == nil {
				result.SuccessCount++
				updatedCopy := updated
				result.Items = append(result.Items, BatchOperationItem{ID: id, Success: true, Message: "更新成功", Item: &updatedCopy})
				continue
			}
		}
		result.FailureCount++
		code, message := assetBatchError(itemErr)
		result.Items = append(result.Items, BatchOperationItem{ID: id, Code: code, Message: message})
	}
	return result, nil
}

func normalizeBatchUpdateInput(input BatchUpdateInput) ([]string, *string, *string, error) {
	ids, err := normalizeBatchIDs(input.IDs)
	if err != nil {
		return nil, nil, nil, err
	}
	var status *string
	if input.Status != nil {
		value := strings.ToLower(strings.TrimSpace(*input.Status))
		if !oneOf(value, "active", "pending", "disabled", "archived") {
			return nil, nil, nil, ErrInvalid
		}
		status = &value
	}
	var networkAreaID *string
	if input.NetworkAreaID != nil {
		value := strings.TrimSpace(*input.NetworkAreaID)
		networkAreaID = &value
	}
	if status == nil && networkAreaID == nil {
		return nil, nil, nil, ErrInvalid
	}
	return ids, status, networkAreaID, nil
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

func createInputFromAsset(item Asset) CreateInput {
	return CreateInput{
		Type: item.Type, Value: item.Value, Name: item.Name, OrganizationID: item.OrganizationID,
		BusinessSystemID: item.BusinessSystemID, Environment: item.Environment, NetworkScope: item.NetworkScope,
		NetworkAreaID: item.NetworkAreaID, Status: item.Status, Importance: item.Importance, OwnerName: item.OwnerName, Notes: item.Notes, ExpiresAt: item.ExpiresAt,
	}
}

func assetBatchError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权更新该网络资产"
	case errors.Is(err, ErrNotFound):
		return "not_found", "网络资产或所选网络区域不存在"
	case errors.Is(err, ErrInvalid):
		return "invalid", "资产编号、状态或网络区域不符合要求"
	case errors.Is(err, ErrConflict):
		return "conflict", "更新后与现有网络资产冲突"
	default:
		return "internal_error", "更新失败，请稍后重试"
	}
}
