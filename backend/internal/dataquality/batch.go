package dataquality

import (
	"context"
	"errors"
	"strings"

	"netassets/internal/auth"
)

const MaxIssueBatchItems = 200

const (
	BatchIssueActionAssign = "assign"
	BatchIssueActionStart  = "start"
	BatchIssueActionSubmit = "submit"
)

type BatchIssueInput struct {
	IDs            []string `json:"ids"`
	Action         string   `json:"action"`
	AssigneeUserID string   `json:"assignee_user_id"`
	Note           string   `json:"note"`
}

type BatchIssueOperationItem struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message"`
	Item    *Issue `json:"item,omitempty"`
}

type BatchIssueOperationResult struct {
	TotalCount   int                       `json:"total_count"`
	SuccessCount int                       `json:"success_count"`
	FailureCount int                       `json:"failure_count"`
	Items        []BatchIssueOperationItem `json:"items"`
}

func (s *Service) OperateIssuesBatch(ctx context.Context, principal auth.Principal, input BatchIssueInput, requestID, ipAddress string) (BatchIssueOperationResult, error) {
	if !canUseQualityWorkflow(principal) {
		return BatchIssueOperationResult{}, ErrForbidden
	}
	ids, action, assigneeUserID, note, err := normalizeBatchIssueInput(input)
	if err != nil {
		return BatchIssueOperationResult{}, err
	}
	result := BatchIssueOperationResult{TotalCount: len(ids), Items: make([]BatchIssueOperationItem, 0, len(ids))}
	for _, id := range ids {
		var item Issue
		var itemErr error
		switch action {
		case BatchIssueActionAssign:
			item, itemErr = s.AssignIssue(ctx, principal, id, StartInput{AssigneeUserID: assigneeUserID, Note: note}, requestID, ipAddress)
		case BatchIssueActionStart:
			item, itemErr = s.startIssue(ctx, principal, id, StartInput{AssigneeUserID: assigneeUserID, Note: note}, []string{"pending"}, requestID, ipAddress)
		case BatchIssueActionSubmit:
			item, itemErr = s.submitIssue(ctx, principal, id, SubmitInput{Note: note}, []string{"in_progress"}, requestID, ipAddress)
		}
		if itemErr == nil {
			result.SuccessCount++
			itemCopy := item
			result.Items = append(result.Items, BatchIssueOperationItem{ID: id, Success: true, Message: batchIssueSuccessMessage(action), Item: &itemCopy})
			continue
		}
		result.FailureCount++
		code, message := batchIssueError(itemErr, action)
		result.Items = append(result.Items, BatchIssueOperationItem{ID: id, Code: code, Message: message})
	}
	return result, nil
}

func normalizeBatchIssueInput(input BatchIssueInput) ([]string, string, string, string, error) {
	if len(input.IDs) == 0 || len(input.IDs) > MaxIssueBatchItems {
		return nil, "", "", "", ErrInvalid
	}
	ids := make([]string, 0, len(input.IDs))
	seen := make(map[string]struct{}, len(input.IDs))
	for _, value := range input.IDs {
		id := strings.TrimSpace(value)
		if !scanUUIDPattern.MatchString(id) {
			return nil, "", "", "", ErrInvalid
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, "", "", "", ErrInvalid
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	if action != BatchIssueActionAssign && action != BatchIssueActionStart && action != BatchIssueActionSubmit {
		return nil, "", "", "", ErrInvalid
	}
	assigneeUserID := strings.TrimSpace(input.AssigneeUserID)
	note := strings.TrimSpace(input.Note)
	if len(note) > 5000 {
		return nil, "", "", "", ErrInvalid
	}
	if action == BatchIssueActionAssign || action == BatchIssueActionStart {
		if !scanUUIDPattern.MatchString(assigneeUserID) {
			return nil, "", "", "", ErrInvalid
		}
	}
	if action == BatchIssueActionSubmit && note == "" {
		return nil, "", "", "", ErrInvalid
	}
	return ids, action, assigneeUserID, note, nil
}

func batchIssueSuccessMessage(action string) string {
	return map[string]string{
		BatchIssueActionAssign: "指派成功",
		BatchIssueActionStart:  "开始整改成功",
		BatchIssueActionSubmit: "提交复核成功",
	}[action]
}

func batchIssueError(err error, action string) (string, string) {
	actionLabel := map[string]string{
		BatchIssueActionAssign: "指派",
		BatchIssueActionStart:  "开始整改",
		BatchIssueActionSubmit: "提交复核",
	}[action]
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权处理该组织的数据质量问题"
	case errors.Is(err, ErrNotFound):
		return "not_found", "数据质量问题不存在"
	case errors.Is(err, ErrInvalidTransition):
		return "invalid_status_transition", "当前状态不允许" + actionLabel
	case errors.Is(err, ErrInvalid):
		return "invalid", "整改负责人或处理说明不符合要求"
	default:
		return "internal_error", actionLabel + "失败，请稍后重试"
	}
}
