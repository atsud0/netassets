package assetcandidate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"netassets/internal/asset"
	"netassets/internal/auth"
)

// MaxBatchAcceptIDs bounds one batch request so a single click cannot hold the
// database through an unbounded loop of per-candidate transactions.
const MaxBatchAcceptIDs = 200

type BatchAcceptItem struct {
	CandidateID string      `json:"candidate_id"`
	Input       AcceptInput `json:"input"`
}

type BatchAcceptInput struct {
	Items []BatchAcceptItem `json:"items"`
}

// BatchAcceptItemResult reports one candidate. Failure is data, not an error: a
// batch of forty discoveries where one is a duplicate must still register the
// other thirty-nine, and the reviewer needs the per-row reason to fix the rest.
type BatchAcceptItemResult struct {
	CandidateID      string `json:"candidate_id"`
	Succeeded        bool   `json:"succeeded"`
	AlreadySettled   bool   `json:"already_settled,omitempty"`
	WorkingVersionID string `json:"working_version_id,omitempty"`
	OrganizationID   string `json:"organization_id,omitempty"`
	Code             string `json:"code,omitempty"`
	Message          string `json:"message,omitempty"`
}

type BatchAcceptResult struct {
	Total     int                     `json:"total"`
	Succeeded int                     `json:"succeeded"`
	Failed    int                     `json:"failed"`
	Skipped   int                     `json:"skipped"`
	Items     []BatchAcceptItemResult `json:"items"`
}

// BatchAccept confirms many asset candidates in one request. Each candidate runs
// in its own transaction through Accept, so one rejected row neither rolls back
// the confirmed ones nor blocks the reviewer from retrying the rest. Re-running
// the same batch is safe: candidates that already carry a working version are
// reported as settled instead of creating a second unpublished draft.
func (s *Service) BatchAccept(ctx context.Context, principal auth.Principal, in BatchAcceptInput, requestID, ip string) BatchAcceptResult {
	result := BatchAcceptResult{Items: make([]BatchAcceptItemResult, 0, len(in.Items))}
	seen := make(map[string]bool, len(in.Items))
	for _, item := range in.Items {
		id := strings.TrimSpace(item.CandidateID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		result.Total++
		if result.Total > MaxBatchAcceptIDs {
			result.Items = append(result.Items, BatchAcceptItemResult{
				Code:    "too_many_items",
				Message: fmt.Sprintf("单次最多确认 %d 条待确认资产", MaxBatchAcceptIDs),
			})
			result.Failed++
			break
		}
		outcome := s.batchAcceptOne(ctx, principal, id, item.Input, requestID, ip)
		result.Items = append(result.Items, outcome)
		switch {
		case outcome.Succeeded:
			result.Succeeded++
		case outcome.AlreadySettled:
			result.Skipped++
		default:
			result.Failed++
		}
	}
	return result
}

func (s *Service) batchAcceptOne(ctx context.Context, principal auth.Principal, id string, in AcceptInput, requestID, ip string) BatchAcceptItemResult {
	outcome := BatchAcceptItemResult{CandidateID: id}
	created, err := s.Accept(ctx, principal, id, in, requestID, ip)
	switch {
	case err == nil:
		outcome.Succeeded = true
		outcome.WorkingVersionID = created.ID
		outcome.OrganizationID = created.OrganizationID
		return outcome
	case errors.Is(err, ErrConflict):
		// Either the candidate was handled by someone else, or it already carries
		// a working version from an earlier attempt. Both mean the reviewer does
		// not need to act again.
		candidate, loadErr := s.listByID(ctx, id)
		if loadErr == nil && candidate.WorkingVersionDraftID != "" {
			outcome.AlreadySettled = true
			outcome.WorkingVersionID = candidate.WorkingVersionDraftID
			outcome.OrganizationID = candidate.OrganizationID
			outcome.Message = "该待确认资产已生成工作版本，无需重复确认"
			return outcome
		}
		if loadErr == nil {
			outcome.Code = "invalid_state"
			outcome.Message = "该待确认资产已处理"
		} else {
			outcome.Code = "not_found"
			outcome.Message = "待确认资产不存在"
		}
		return outcome
	default:
		outcome.Code, outcome.Message = describeBatchError(err)
		return outcome
	}
}

// describeBatchError keeps the single-item wording next to the batch code so a
// row in the results table reads exactly like the message the single-row dialog
// would have shown.
func describeBatchError(err error) (string, string) {
	switch {
	case errors.Is(err, ErrForbidden):
		return "forbidden", "无权处理该组织的待确认资产"
	case errors.Is(err, ErrSelfServiceClosed):
		return "self_service_closed", "该组织未开通自助确认，仍需集团管理员处理"
	case errors.Is(err, ErrNotFound):
		return "not_found", "待确认资产或关联正式资产不存在"
	case errors.Is(err, ErrInvalid):
		return "invalid_asset_candidate", "请检查资产类型、资产值、所属组织和处理信息"
	case errors.Is(err, asset.ErrConflict):
		return "invalid_state", "该待确认资产已处理，或正式资产存在重复"
	default:
		return "internal_error", "处理待确认资产失败"
	}
}
