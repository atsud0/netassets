package securitytestrequest

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
	"netassets/internal/inventorytask"
	"netassets/internal/notification"
	"netassets/internal/textlength"
)

// BlockingFinding is one finding that prevents launch approval. The list is
// surfaced verbatim so the approver sees every blocker, per the design's
// "逐条列出阻塞项" requirement.
type BlockingFinding struct {
	Code   string `json:"code"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// OpenFindingsError reports the findings that still need closure before the
// request may be approved for launch.
type OpenFindingsError struct {
	Items []BlockingFinding
}

func (e *OpenFindingsError) Error() string {
	return fmt.Sprintf("security test request still has %d unclosed finding(s)", len(e.Items))
}

// UnbindableScopeError reports working versions that can no longer be bound
// to the launch release submission, for example because they were submitted
// through a regular inventory task in the meantime.
type UnbindableScopeError struct {
	Count int
}

func (e *UnbindableScopeError) Error() string {
	return fmt.Sprintf("%d scope working version(s) can no longer be reported", e.Count)
}

type ApproveLaunchInput struct {
	Comment string `json:"comment"`
}

// promoteIfReady moves a testing request to pending_launch once its converted
// batch is completed and every finding on that batch is closed or risk
// accepted. There is no scheduler: the promotion runs lazily when group
// security loads the request list or detail, which is the only place the new
// status matters. The call is idempotent and silently skips requests that are
// not ready yet.
func (s *Service) promoteIfReady(ctx context.Context, principal auth.Principal, item *Request) (bool, error) {
	if item.Status != "testing" || item.ConvertedBatchID == "" {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin promote security test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	locked, err := lockRequest(ctx, tx, item.ID)
	if err != nil {
		return false, err
	}
	if locked.Status != "testing" {
		return false, nil
	}
	var batchStatus string
	var openFindings int
	if err := tx.QueryRow(ctx, `
		SELECT b.status, (SELECT count(*) FROM security_findings f
		                  WHERE f.test_batch_id=b.id AND f.status IN ('pending','in_progress'))
		FROM security_test_batches b WHERE b.id=$1::uuid`, locked.ConvertedBatchID).Scan(&batchStatus, &openFindings); err != nil {
		return false, fmt.Errorf("check converted batch readiness: %w", err)
	}
	if batchStatus != "completed" || openFindings > 0 {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET status='pending_launch', updated_at=now()
		WHERE id=$1::uuid AND status='testing'`, locked.ID); err != nil {
		return false, fmt.Errorf("promote security test request: %w", err)
	}
	if err := writeEvent(ctx, tx, "", locked.ID, "pending_launch", "testing", "pending_launch", "",
		map[string]any{"batch_code": locked.ConvertedBatchCode}); err != nil {
		return false, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: "security_test_request.pending_launch", Category: notification.CategorySecurityTestRequest,
		TargetOrganizationID: locked.ApplicantOrganizationID, ActorUserID: principal.UserID,
		RequiredPermission: auth.PermissionSecurityTest,
		ResourceType:       "security_test_request", ResourceID: locked.ID, ResourceLabel: locked.RequestName,
		Details: map[string]any{"request_code": locked.Code, "batch_code": locked.ConvertedBatchCode},
	}); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit security test request promotion: %w", err)
	}
	return true, nil
}

// ApproveLaunch grants launch permission and auto-reports the request scope:
// every still-unreported working version is bound to a system-generated
// launch_release inventory task and submitted into the existing group ledger
// review flow. The request closes at approval; the submission's further
// progress is tracked through reported_task_id and the task review pages.
func (s *Service) ApproveLaunch(ctx context.Context, principal auth.Principal, id string, input ApproveLaunchInput, requestID, ipAddress string) (Request, error) {
	if !canManage(principal) {
		return Request{}, ErrForbidden
	}
	if !validUUID(id) {
		return Request{}, invalidInput("申请标识格式不正确")
	}
	input.Comment = strings.TrimSpace(input.Comment)
	if textlength.Count(input.Comment) > 20000 {
		return Request{}, invalidInput("上线说明不能超过 20000 个字符")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin approve security test request launch: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if item.Status != "testing" && item.Status != "pending_launch" {
		return Request{}, ErrState
	}
	if item.ConvertedBatchID == "" {
		return Request{}, ErrState
	}
	var batchStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM security_test_batches WHERE id=$1::uuid`, item.ConvertedBatchID).Scan(&batchStatus); err != nil {
		return Request{}, fmt.Errorf("check converted batch status: %w", err)
	}
	if batchStatus != "completed" {
		return Request{}, ErrState
	}
	blockers, err := openFindings(ctx, tx, item.ConvertedBatchID)
	if err != nil {
		return Request{}, err
	}
	if len(blockers) > 0 {
		return Request{}, &OpenFindingsError{Items: blockers}
	}
	bindable, publishedMeanwhile, err := bindableScopeDrafts(ctx, tx, item)
	if err != nil {
		return Request{}, err
	}
	actor := item.ContactUserID
	if actor == "" {
		actor = principal.UserID
	}
	reportedTaskID := ""
	if len(bindable.AssetDraftIDs) > 0 || bindable.BusinessSystemDraftID != "" {
		reportedTaskID, err = inventorytask.CreateLaunchReleaseSubmission(ctx, tx, inventorytask.LaunchReleaseInput{
			RequestCode:           item.Code,
			RequestName:           item.RequestName,
			BatchCode:             item.ConvertedBatchCode,
			OrganizationID:        item.ApplicantOrganizationID,
			ActorUserID:           actor,
			BusinessSystemDraftID: bindable.BusinessSystemDraftID,
			AssetDraftIDs:         bindable.AssetDraftIDs,
			Note:                  "上线发布自动上报：来源申请 " + item.Code + "（测试批次 " + item.ConvertedBatchCode + "）。",
		})
		if err != nil {
			return Request{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET
			status='closed', launch_decided_by=$2::uuid, launch_decided_at=now(), launch_comment=NULLIF($3, ''),
			reported_task_id=NULLIF($4, '')::uuid, updated_by=$2::uuid, updated_at=now()
		WHERE id=$1::uuid AND status IN ('testing', 'pending_launch')`,
		id, principal.UserID, input.Comment, reportedTaskID); err != nil {
		return Request{}, fmt.Errorf("approve security test request launch: %w", err)
	}
	if item.Status == "testing" {
		if err := writeEvent(ctx, tx, principal.UserID, id, "pending_launch", "testing", "pending_launch", "",
			map[string]any{"batch_code": item.ConvertedBatchCode}); err != nil {
			return Request{}, err
		}
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "approve_launch", pendingLaunchFrom(item.Status), "closed", input.Comment,
		map[string]any{
			"task_id": reportedTaskID, "reported_count": len(bindable.AssetDraftIDs),
			"already_published_count": publishedMeanwhile, "batch_code": item.ConvertedBatchCode,
		}); err != nil {
		return Request{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_request.approve_launch", id, "批准上线安全测试申请 "+item.RequestName,
		map[string]any{
			"request_code": item.Code, "batch_code": item.ConvertedBatchCode, "task_id": reportedTaskID,
			"reported_count": len(bindable.AssetDraftIDs), "already_published_count": publishedMeanwhile,
		}, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: "security_test_request.launch_approved", Category: notification.CategorySecurityTestRequest,
		TargetOrganizationID: item.ApplicantOrganizationID, ActorUserID: principal.UserID,
		RequiredPermission: auth.PermissionSecurityTestRequest,
		ResourceType:       "security_test_request", ResourceID: id, ResourceLabel: item.RequestName,
		Details: map[string]any{
			"request_code": item.Code, "batch_code": item.ConvertedBatchCode,
			"reported_count": len(bindable.AssetDraftIDs), "task_id": reportedTaskID, "comment": input.Comment,
		},
	}); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request launch approval: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func pendingLaunchFrom(status string) string {
	if status == "pending_launch" {
		return "pending_launch"
	}
	return "testing"
}

func openFindings(ctx context.Context, q scopeQuerier, batchID string) ([]BlockingFinding, error) {
	rows, err := q.Query(ctx, `
		SELECT finding_code, title, status FROM security_findings
		WHERE test_batch_id=$1::uuid AND status IN ('pending','in_progress')
		ORDER BY discovered_at LIMIT 50`, batchID)
	if err != nil {
		return nil, fmt.Errorf("load unclosed findings for launch approval: %w", err)
	}
	defer rows.Close()
	items := make([]BlockingFinding, 0)
	for rows.Next() {
		var item BlockingFinding
		if err := rows.Scan(&item.Code, &item.Title, &item.Status); err != nil {
			return nil, fmt.Errorf("scan unclosed finding: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type bindableScope struct {
	BusinessSystemDraftID string
	AssetDraftIDs         []string
}

// bindableScopeDrafts re-verifies the frozen scope against the live working
// ledger at launch time. Working versions published through a regular task in
// the meantime are skipped (already reported); anything stuck in review or
// discarded blocks approval instead of silently dropping scope.
func bindableScopeDrafts(ctx context.Context, tx pgx.Tx, item Request) (bindableScope, int, error) {
	var result bindableScope
	publishedMeanwhile := 0
	rows, err := tx.Query(ctx, `
		SELECT d.id::text, d.draft_status, COALESCE(d.task_id::text, '')
		FROM security_test_request_scope_assets r
		JOIN inventory_asset_drafts d ON d.id=r.working_asset_id
		WHERE r.request_id=$1::uuid AND r.working_asset_id IS NOT NULL
		ORDER BY d.created_at`, item.ID)
	if err != nil {
		return bindableScope{}, 0, fmt.Errorf("load scope working versions for launch: %w", err)
	}
	unbindable := 0
	for rows.Next() {
		var draftID, draftStatus, taskID string
		if err := rows.Scan(&draftID, &draftStatus, &taskID); err != nil {
			rows.Close()
			return bindableScope{}, 0, fmt.Errorf("scan scope working version: %w", err)
		}
		switch {
		case draftStatus == "published":
			publishedMeanwhile++
		case (draftStatus == "editing" || draftStatus == "returned") && taskID == "":
			result.AssetDraftIDs = append(result.AssetDraftIDs, draftID)
		default:
			unbindable++
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return bindableScope{}, 0, fmt.Errorf("iterate scope working versions: %w", err)
	}
	rows.Close()
	if item.BusinessSystemDraftID != "" {
		var draftStatus, taskID string
		if err := tx.QueryRow(ctx, `
			SELECT draft_status, COALESCE(task_id::text, '') FROM inventory_business_system_drafts
			WHERE id=$1::uuid`, item.BusinessSystemDraftID).Scan(&draftStatus, &taskID); err != nil {
			return bindableScope{}, 0, fmt.Errorf("load scope business system draft for launch: %w", err)
		}
		switch {
		case draftStatus == "published":
		case (draftStatus == "editing" || draftStatus == "returned") && taskID == "":
			result.BusinessSystemDraftID = item.BusinessSystemDraftID
		default:
			unbindable++
		}
	}
	if unbindable > 0 {
		return bindableScope{}, 0, &UnbindableScopeError{Count: unbindable}
	}
	return result, publishedMeanwhile, nil
}
