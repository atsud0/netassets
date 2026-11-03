package securityworkorder

import (
	"context"
	"errors"
	"strings"
	"time"

	"netassets/internal/auth"
	"netassets/internal/findingstatus"
)

const MaxBatchItems = 200

type BatchItem struct {
	ID                string     `json:"id"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

type BatchInput struct {
	Items  []BatchItem `json:"items"`
	Status string      `json:"status"`
	Reason string      `json:"reason"`
}

type BatchItemResult struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type BatchResult struct {
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
	Items     []BatchItemResult `json:"items"`
}

func validateBatch(input BatchInput) error {
	if len(input.Items) == 0 || len(input.Items) > MaxBatchItems {
		return invalidField("单次批量操作请选择 1 至 200 项")
	}
	seen := make(map[string]bool, len(input.Items))
	for _, item := range input.Items {
		key := strings.ToLower(strings.TrimSpace(item.ID))
		if seen[key] {
			return invalidField("批量操作不能包含重复记录")
		}
		seen[key] = true
	}
	return nil
}

func runBatch(ctx context.Context, items []BatchItem, action func(BatchItem) error) BatchResult {
	result := BatchResult{Items: make([]BatchItemResult, 0, len(items))}
	for _, item := range items {
		err := ctx.Err()
		if err == nil {
			if !validUUID(item.ID) {
				err = ErrInvalid
			} else {
				err = action(item)
			}
		}
		entry := BatchItemResult{ID: item.ID, Success: err == nil}
		if err == nil {
			result.Succeeded++
		} else {
			result.Failed++
			entry.Code, entry.Message = batchError(err)
		}
		result.Items = append(result.Items, entry)
	}
	return result
}

func batchError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "当前账号无权操作该记录"
	case errors.Is(err, ErrNotFound):
		return "not_found", "记录不存在或不可见"
	case errors.Is(err, ErrEditConflict):
		return "edit_conflict", "记录已更新，请刷新后重试"
	case errors.Is(err, ErrVersionRequired):
		return "version_required", "缺少记录版本，请刷新后重试"
	case errors.Is(err, ErrState):
		return "invalid_state", "当前状态不允许此操作，请刷新后重试"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrIndividualWorkOrderAction):
		return "conflict", "记录受工单或专项流程约束，请单独处理"
	case errors.Is(err, ErrInvalid):
		var validation *InputValidationError
		if errors.As(err, &validation) {
			return "invalid_input", validation.Message
		}
		return "invalid_input", "记录或操作内容不正确"
	default:
		return "internal_error", "处理失败，请刷新后检查结果再重试"
	}
}

func (service *Service) BatchCancel(ctx context.Context, principal auth.Principal, input BatchInput, requestID, ipAddress string) (BatchResult, error) {
	if !principal.CanManageSecurityWorkOrders() {
		return BatchResult{}, ErrForbidden
	}
	if err := validateBatch(input); err != nil {
		return BatchResult{}, err
	}
	if err := validateWorkOrderComment(strings.TrimSpace(input.Reason), true); err != nil {
		return BatchResult{}, err
	}
	return runBatch(ctx, input.Items, func(item BatchItem) error {
		_, err := service.Cancel(ctx, principal, item.ID, CommentInput{Comment: input.Reason}, requestID, ipAddress)
		return err
	}), nil
}

func (service *Service) BatchIgnoreCandidates(ctx context.Context, principal auth.Principal, input BatchInput, requestID, ipAddress string) (BatchResult, error) {
	if !principal.CanManageSecurityFindings() {
		return BatchResult{}, ErrForbidden
	}
	if err := validateBatch(input); err != nil {
		return BatchResult{}, err
	}
	return runBatch(ctx, input.Items, func(item BatchItem) error {
		_, err := service.IgnoreFindingImportCandidate(ctx, principal, item.ID, FindingImportCandidateReviewInput{ReviewNote: input.Reason}, requestID, ipAddress)
		return err
	}), nil
}

func (service *Service) BatchUpdateFindingStatus(ctx context.Context, principal auth.Principal, input BatchInput, requestID, ipAddress string) (BatchResult, error) {
	if !principal.CanManageSecurityFindings() {
		return BatchResult{}, ErrForbidden
	}
	if err := validateBatch(input); err != nil {
		return BatchResult{}, err
	}
	if input.Status != findingstatus.Pending && input.Status != findingstatus.Confirmed {
		return BatchResult{}, invalidField("批量状态仅支持待处理或已确认，其他状态请使用单条专项流程")
	}
	return runBatch(ctx, input.Items, func(item BatchItem) error {
		current, err := service.GetFinding(ctx, principal, item.ID)
		if err != nil {
			return err
		}
		if current.Status == findingstatus.RiskAccepted || current.Status == findingstatus.Duplicate {
			return ErrConflict
		}
		if current.Status == input.Status {
			return ErrState
		}
		_, err = service.UpdateFinding(ctx, principal, item.ID, FindingInput{
			Title: current.Title, Severity: current.Severity, Status: input.Status,
			AssigneeOrganizationID: current.AssigneeOrganizationID, AssetID: current.AssetID,
			BusinessSystemID: current.BusinessSystemID, TestBatchID: current.TestBatchID,
			VulnerabilityType: current.VulnerabilityType, Description: current.Description,
			Evidence: current.Evidence, RemediationAdvice: current.RemediationAdvice,
			Source: current.Source, DiscoveredAt: current.DiscoveredAt, ExpectedUpdatedAt: item.ExpectedUpdatedAt,
		}, requestID, ipAddress)
		return err
	}), nil
}
