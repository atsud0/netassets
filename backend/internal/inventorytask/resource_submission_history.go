package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var inventoryResourceUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type ResourceSubmissionHistoryItem struct {
	SubmissionID    string                  `json:"submission_id"`
	TaskID          string                  `json:"task_id"`
	TaskName        string                  `json:"task_name"`
	OrganizationID  string                  `json:"organization_id"`
	Version         int                     `json:"version"`
	Status          string                  `json:"status"`
	Operation       string                  `json:"operation"`
	SubmittedAt     time.Time               `json:"submitted_at"`
	SubmittedByName string                  `json:"submitted_by_name,omitempty"`
	ReviewedAt      *time.Time              `json:"reviewed_at,omitempty"`
	ReviewedByName  string                  `json:"reviewed_by_name,omitempty"`
	ReturnReason    string                  `json:"return_reason,omitempty"`
	Note            string                  `json:"note,omitempty"`
	ResourceKey     string                  `json:"resource_key"`
	ResourceName    string                  `json:"resource_name"`
	Changes         []SubmissionFieldChange `json:"changes"`
}

type resourceSubmissionTarget struct {
	RequestedID    string
	CanonicalID    string
	OrganizationID string
	TaskID         string
	DraftStatus    string
	IsDraft        bool
	ResourceKey    string
	ResourceName   string
}

type resourceSubmissionRow struct {
	Item         ResourceSubmissionHistoryItem
	Snapshot     map[string]any
	Previous     map[string]any
	RawOperation string
}

// ResourceSubmissionHistory returns immutable submission versions for one
// resource. Formal resource IDs and working-version IDs are both accepted, but
// headquarters cannot use this endpoint to discover daily unreported drafts.
func (s *Service) ResourceSubmissionHistory(ctx context.Context, principal auth.Principal, resourceType, resourceID string, page pagination.Query) (pagination.Result[ResourceSubmissionHistoryItem], error) {
	resourceType = strings.TrimSpace(resourceType)
	resourceID = strings.TrimSpace(resourceID)
	if !validSubmissionDiffResourceType(resourceType) {
		return pagination.Result[ResourceSubmissionHistoryItem]{}, invalidInput("资源类型：请选择网络资产或业务系统")
	}
	if !inventoryResourceUUIDPattern.MatchString(resourceID) {
		if resourceType == "business_system" {
			return pagination.Result[ResourceSubmissionHistoryItem]{}, invalidInput("业务系统标识不正确")
		}
		return pagination.Result[ResourceSubmissionHistoryItem]{}, invalidInput("网络资产标识不正确")
	}

	permission := "asset.manage"
	if resourceType == "business_system" {
		permission = "system.manage"
	}
	if !principal.HasRole("group_admin") && !principal.HasPermission(permission) {
		return pagination.Result[ResourceSubmissionHistoryItem]{}, ErrForbidden
	}

	target, err := s.loadResourceSubmissionTarget(ctx, resourceType, resourceID)
	if err != nil {
		return pagination.Result[ResourceSubmissionHistoryItem]{}, err
	}
	if principal.HasRole("group_admin") {
		if target.IsDraft && (target.TaskID == "" || target.DraftStatus != "submitted") {
			return pagination.Result[ResourceSubmissionHistoryItem]{}, ErrForbidden
		}
	} else {
		allowed, accessErr := access.CanOperateOrganization(ctx, s.pool, principal, target.OrganizationID, permission)
		if accessErr != nil {
			return pagination.Result[ResourceSubmissionHistoryItem]{}, accessErr
		}
		if !allowed || (target.IsDraft && target.TaskID != "" && !principal.HasPermission("inventory.manage")) {
			return pagination.Result[ResourceSubmissionHistoryItem]{}, ErrForbidden
		}
	}

	rows, err := s.pool.Query(ctx, resourceSubmissionHistoryQuery, resourceType, target.RequestedID, target.CanonicalID, target.OrganizationID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[ResourceSubmissionHistoryItem]{}, fmt.Errorf("query resource submission history: %w", err)
	}
	defer rows.Close()

	items := make([]ResourceSubmissionHistoryItem, 0, page.PageSize)
	total := 0
	for rows.Next() {
		row, rowTotal, scanErr := scanResourceSubmissionHistoryRow(rows)
		if scanErr != nil {
			return pagination.Result[ResourceSubmissionHistoryItem]{}, scanErr
		}
		total = rowTotal
		row.Item.Operation, row.Item.Changes = buildResourceSubmissionChanges(resourceType, row.RawOperation, row.Snapshot, row.Previous)
		items = append(items, row.Item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[ResourceSubmissionHistoryItem]{}, fmt.Errorf("iterate resource submission history: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, resourceSubmissionHistoryCountQuery, resourceType, target.RequestedID, target.CanonicalID, target.OrganizationID).Scan(&total); err != nil {
			return pagination.Result[ResourceSubmissionHistoryItem]{}, fmt.Errorf("count resource submission history: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) loadResourceSubmissionTarget(ctx context.Context, resourceType, resourceID string) (resourceSubmissionTarget, error) {
	var row pgx.Row
	if resourceType == "network_asset" {
		row = s.pool.QueryRow(ctx, `
			SELECT requested_id, canonical_id, organization_id, task_id, draft_status, is_draft, resource_key, resource_name
			FROM (
			    SELECT asset.id::text requested_id, asset.id::text canonical_id, owner.organization_id::text organization_id,
			           '' task_id, '' draft_status, false is_draft,
			           asset.asset_type || ':' || asset.normalized_value resource_key,
			           COALESCE(NULLIF(asset.asset_name, ''), asset.asset_value) resource_name, 0 priority
			    FROM assets asset
			    JOIN asset_organizations owner ON owner.asset_id=asset.id AND owner.is_primary
			    WHERE asset.id::text=$1
			    UNION ALL
			    SELECT draft.id::text, COALESCE(draft.source_asset_id::text, draft.id::text), draft.organization_id::text,
			           COALESCE(draft.task_id::text, ''), draft.draft_status, true,
			           draft.asset_type || ':' || draft.normalized_value,
			           COALESCE(NULLIF(draft.asset_name, ''), draft.asset_value), 1
			    FROM inventory_asset_drafts draft
			    WHERE draft.id::text=$1
			) target
			ORDER BY priority
			LIMIT 1
		`, resourceID)
	} else {
		row = s.pool.QueryRow(ctx, `
			SELECT requested_id, canonical_id, organization_id, task_id, draft_status, is_draft, resource_key, resource_name
			FROM (
			    SELECT system.id::text requested_id, system.id::text canonical_id, system.owner_organization_id::text organization_id,
			           '' task_id, '' draft_status, false is_draft, system.system_code resource_key, system.system_name resource_name, 0 priority
			    FROM business_systems system WHERE system.id::text=$1
			    UNION ALL
			    SELECT draft.id::text, COALESCE(draft.source_system_id::text, draft.id::text), draft.organization_id::text,
			           COALESCE(draft.task_id::text, ''), draft.draft_status, true, draft.system_code, draft.system_name, 1
			    FROM inventory_business_system_drafts draft WHERE draft.id::text=$1
			) target
			ORDER BY priority
			LIMIT 1
		`, resourceID)
	}
	var target resourceSubmissionTarget
	if err := row.Scan(&target.RequestedID, &target.CanonicalID, &target.OrganizationID, &target.TaskID, &target.DraftStatus, &target.IsDraft, &target.ResourceKey, &target.ResourceName); errors.Is(err, pgx.ErrNoRows) {
		return resourceSubmissionTarget{}, ErrNotFound
	} else if err != nil {
		return resourceSubmissionTarget{}, fmt.Errorf("load resource submission target: %w", err)
	}
	return target, nil
}

const resourceSubmissionHistoryMatch = `
	item.resource_type=$1
	AND submission.organization_id=$4::uuid
	AND (
	    item.resource_id=$2::uuid
	    OR COALESCE(NULLIF(item.snapshot_data->>'_source_resource_id', ''), item.resource_id::text)=$3
	)`

const resourceSubmissionHistoryQuery = `
	SELECT submission.id::text, submission.task_id::text, task.task_name, submission.organization_id::text,
	       submission.submission_version, submission.review_status, submission.submitted_at,
	       COALESCE(submitter.display_name, ''), submission.reviewed_at, COALESCE(reviewer.display_name, ''),
	       COALESCE(submission.return_reason, ''), COALESCE(submission.submission_note, ''),
	       item.resource_key, item.resource_name, item.snapshot_data,
	       COALESCE(item.snapshot_data->>'_inventory_operation', ''),
	       COALESCE(previous_item.snapshot_data, '{}'::jsonb), count(*) OVER()
	FROM inventory_submission_items item
	JOIN inventory_submissions submission ON submission.id=item.submission_id
	JOIN inventory_tasks task ON task.id=submission.task_id
	LEFT JOIN users submitter ON submitter.id=submission.submitted_by
	LEFT JOIN users reviewer ON reviewer.id=submission.reviewed_by
	LEFT JOIN LATERAL (
	    SELECT candidate.snapshot_data
	    FROM inventory_submission_items candidate
	    JOIN inventory_submissions candidate_submission ON candidate_submission.id=candidate.submission_id
	    WHERE candidate.resource_type=item.resource_type
	      AND candidate_submission.organization_id=submission.organization_id
	      AND candidate_submission.submitted_at < submission.submitted_at
	      AND COALESCE(NULLIF(candidate.snapshot_data->>'_source_resource_id', ''), candidate.resource_id::text) =
	          COALESCE(NULLIF(item.snapshot_data->>'_source_resource_id', ''), item.resource_id::text)
	    ORDER BY candidate_submission.submitted_at DESC, candidate_submission.submission_version DESC
	    LIMIT 1
	) previous_item ON true
	WHERE ` + resourceSubmissionHistoryMatch + `
	ORDER BY submission.submitted_at DESC, submission.submission_version DESC
	LIMIT $5 OFFSET $6
`

const resourceSubmissionHistoryCountQuery = `
	SELECT count(*)
	FROM inventory_submission_items item
	JOIN inventory_submissions submission ON submission.id=item.submission_id
	WHERE ` + resourceSubmissionHistoryMatch

func scanResourceSubmissionHistoryRow(rows pgx.Rows) (resourceSubmissionRow, int, error) {
	var row resourceSubmissionRow
	var snapshotJSON, previousJSON []byte
	var total int
	if err := rows.Scan(
		&row.Item.SubmissionID, &row.Item.TaskID, &row.Item.TaskName, &row.Item.OrganizationID,
		&row.Item.Version, &row.Item.Status, &row.Item.SubmittedAt,
		&row.Item.SubmittedByName, &row.Item.ReviewedAt, &row.Item.ReviewedByName,
		&row.Item.ReturnReason, &row.Item.Note, &row.Item.ResourceKey, &row.Item.ResourceName,
		&snapshotJSON, &row.RawOperation, &previousJSON, &total,
	); err != nil {
		return resourceSubmissionRow{}, 0, fmt.Errorf("scan resource submission history: %w", err)
	}
	if err := json.Unmarshal(snapshotJSON, &row.Snapshot); err != nil {
		return resourceSubmissionRow{}, 0, fmt.Errorf("decode resource submission snapshot: %w", err)
	}
	if err := json.Unmarshal(previousJSON, &row.Previous); err != nil {
		return resourceSubmissionRow{}, 0, fmt.Errorf("decode previous resource submission snapshot: %w", err)
	}
	return row, total, nil
}

func buildResourceSubmissionChanges(resourceType, rawOperation string, snapshot, previous map[string]any) (string, []SubmissionFieldChange) {
	operation := strings.TrimSpace(rawOperation)
	before := previous
	if frozen, ok := snapshot[inventoryBeforeKey].(map[string]any); ok && (operation == "update" || operation == "delete") {
		before = frozen
	}
	switch operation {
	case "create":
		return "create", compareSnapshotData(resourceType, map[string]any{}, snapshot)
	case "update":
		return "update", compareSnapshotData(resourceType, before, snapshot)
	case "delete":
		return "delete", compareSnapshotData(resourceType, before, snapshot)
	default:
		if len(previous) == 0 {
			return "first_report", compareSnapshotData(resourceType, map[string]any{}, snapshot)
		}
		return "confirmed", compareSnapshotData(resourceType, previous, snapshot)
	}
}
