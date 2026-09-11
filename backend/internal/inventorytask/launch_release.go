package inventorytask

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
)

// LaunchReleaseInput carries everything needed to auto-report an approved
// security-test request scope into the group ledger review flow. The call
// runs inside the caller's transaction (the request approval) so a failure
// rolls the whole approval back.
type LaunchReleaseInput struct {
	RequestCode           string
	RequestName           string
	BatchCode             string
	OrganizationID        string
	ActorUserID           string // request contact user (fallback: approver)
	BusinessSystemDraftID string
	AssetDraftIDs         []string
	Note                  string
	RequestID             string // HTTP request id for audit logs
	IPAddress             string
}

// CreateLaunchReleaseSubmission creates a system-generated launch_release
// inventory task, binds the still-unreported working versions from the
// request scope, and immediately submits them for review. It intentionally
// bypasses the HTTP principal gates: the HTTP layer has already authorized
// the group approver, and the drafts' organization never acts through this
// path. Publication still requires the regular group review — this replaces
// only the subsidiary's manual select-and-submit action.
func CreateLaunchReleaseSubmission(ctx context.Context, tx pgx.Tx, input LaunchReleaseInput) (string, error) {
	input.RequestCode = strings.TrimSpace(input.RequestCode)
	input.OrganizationID = strings.TrimSpace(input.OrganizationID)
	input.ActorUserID = strings.TrimSpace(input.ActorUserID)
	input.BusinessSystemDraftID = strings.TrimSpace(input.BusinessSystemDraftID)
	input.Note = strings.TrimSpace(input.Note)
	assetDraftIDs := make([]string, 0, len(input.AssetDraftIDs))
	for _, id := range input.AssetDraftIDs {
		if id = strings.TrimSpace(id); id != "" {
			assetDraftIDs = append(assetDraftIDs, id)
		}
	}
	if input.OrganizationID == "" || input.ActorUserID == "" || (len(assetDraftIDs) == 0 && input.BusinessSystemDraftID == "") {
		return "", ErrInvalid
	}

	requiredFields := make([]string, 0, 2)
	if input.BusinessSystemDraftID != "" {
		requiredFields = append(requiredFields, "business_systems")
	}
	if len(assetDraftIDs) > 0 {
		requiredFields = append(requiredFields, "network_assets")
	}
	requiredFieldsJSON, err := json.Marshal(requiredFields)
	if err != nil {
		return "", fmt.Errorf("encode launch release required fields: %w", err)
	}

	taskName := "上线发布：" + input.RequestName
	if input.RequestCode != "" {
		taskName += "（" + input.RequestCode + "）"
	}
	if len(taskName) > 255 {
		taskName = taskName[:255]
	}
	var taskID string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_tasks
		(task_name, description, start_at, deadline_at, status, required_fields, asset_network_scope,
		 require_review, allow_late_submission, created_by, task_type)
		VALUES ($1, $2, now(), now() + interval '30 days', 'active', $3::jsonb, 'public', true, true, $4::uuid, 'launch_release')
		RETURNING id::text`, taskName, input.Note, requiredFieldsJSON, input.ActorUserID).Scan(&taskID)
	if err != nil {
		return "", fmt.Errorf("create launch release inventory task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO inventory_task_organizations (task_id, organization_id, status)
		VALUES ($1::uuid, $2::uuid, 'not_started')`, taskID, input.OrganizationID); err != nil {
		return "", fmt.Errorf("assign launch release organization: %w", err)
	}

	// Bind the working versions to the launch task. The conditional UPDATE is
	// the concurrency arbiter: if a draft was submitted or discarded elsewhere
	// in the meantime, the row count mismatch aborts the approval.
	if len(assetDraftIDs) > 0 {
		command, err := tx.Exec(ctx, `
			UPDATE inventory_asset_drafts SET task_id=$1::uuid, updated_by=$2::uuid, updated_at=now()
			WHERE id = ANY($3::uuid[]) AND organization_id=$4::uuid
			  AND task_id IS NULL AND draft_status IN ('editing','returned') AND operation <> 'delete'`,
			taskID, input.ActorUserID, assetDraftIDs, input.OrganizationID)
		if err != nil {
			return "", fmt.Errorf("bind launch release asset drafts: %w", err)
		}
		if command.RowsAffected() != int64(len(assetDraftIDs)) {
			return "", ErrConflict
		}
	}
	if input.BusinessSystemDraftID != "" {
		command, err := tx.Exec(ctx, `
			UPDATE inventory_business_system_drafts SET task_id=$1::uuid, updated_by=$2::uuid, updated_at=now()
			WHERE id=$3::uuid AND organization_id=$4::uuid
			  AND task_id IS NULL AND draft_status IN ('editing','returned') AND operation <> 'delete'`,
			taskID, input.ActorUserID, input.BusinessSystemDraftID, input.OrganizationID)
		if err != nil {
			return "", fmt.Errorf("bind launch release business system draft: %w", err)
		}
		if command.RowsAffected() != 1 {
			return "", ErrConflict
		}
	}

	principal := auth.Principal{UserID: input.ActorUserID}
	snapshot, err := createSubmissionSnapshot(ctx, tx, principal, taskID, input.OrganizationID, input.Note)
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_business_system_drafts
		SET draft_status='submitted', submitted_at=now(), updated_by=$3::uuid, updated_at=now()
		WHERE task_id=$1::uuid AND organization_id=$2::uuid AND draft_status IN ('editing','returned')
	`, taskID, input.OrganizationID, input.ActorUserID); err != nil {
		return "", fmt.Errorf("submit launch release business system drafts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_asset_drafts
		SET draft_status='submitted', submitted_at=now(), updated_by=$3::uuid, updated_at=now()
		WHERE task_id=$1::uuid AND organization_id=$2::uuid AND draft_status IN ('editing','returned')
	`, taskID, input.OrganizationID, input.ActorUserID); err != nil {
		return "", fmt.Errorf("submit launch release asset drafts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_task_organizations
		SET status='submitted', started_at=COALESCE(started_at, now()), submitted_by=$3::uuid, submitted_at=now(),
		    submission_note=NULLIF($4, ''), latest_submission_id=$5::uuid,
		    submitted_system_count=$6, submitted_asset_count=$7,
		    reviewed_by=NULL, reviewed_at=NULL, return_reason=NULL, updated_at=now()
		WHERE task_id=$1::uuid AND organization_id=$2::uuid`,
		taskID, input.OrganizationID, input.ActorUserID, input.Note, snapshot.ID, snapshot.SystemCount, snapshot.AssetCount); err != nil {
		return "", fmt.Errorf("submit launch release organization progress: %w", err)
	}
	if err := writeSubmissionAudit(ctx, tx, principal, input.OrganizationID, taskID, input.Note, "submitted", snapshot, input.RequestID, input.IPAddress); err != nil {
		return "", err
	}
	return taskID, nil
}
