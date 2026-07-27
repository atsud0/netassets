package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

type ReviewCommentInput struct {
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Comment      string `json:"comment"`
}

type ReviewResource struct {
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	ResourceKey  string         `json:"resource_key"`
	ResourceName string         `json:"resource_name"`
	SnapshotData map[string]any `json:"snapshot_data"`
}

type ReviewComment struct {
	ID                string    `json:"id"`
	SubmissionID      string    `json:"submission_id"`
	SubmissionVersion int       `json:"submission_version"`
	ResourceType      string    `json:"resource_type"`
	ResourceID        string    `json:"resource_id"`
	ResourceKey       string    `json:"resource_key"`
	ResourceName      string    `json:"resource_name"`
	Comment           string    `json:"comment"`
	CreatedByName     string    `json:"created_by_name,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
}

type ReviewContext struct {
	Submission SubmissionMetadata `json:"submission"`
}

func (s *Service) ReviewContext(ctx context.Context, principal auth.Principal, taskID, organizationID string) (ReviewContext, error) {
	current, _, err := s.loadSubmissionMetadata(ctx, principal, taskID, organizationID)
	if err != nil {
		return ReviewContext{}, err
	}
	return ReviewContext{Submission: current}, nil
}

// SearchReviewResources returns resources from the latest submission snapshot.
// Keeping this collection separate prevents a large subsidiary submission from
// making the review-context response unbounded.
func (s *Service) SearchReviewResources(ctx context.Context, principal auth.Principal, taskID, organizationID, keyword string, page pagination.Query) (pagination.Result[ReviewResource], error) {
	current, _, err := s.loadSubmissionMetadata(ctx, principal, taskID, organizationID)
	if err != nil {
		return pagination.Result[ReviewResource]{}, err
	}
	keyword = strings.TrimSpace(keyword)
	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM inventory_submission_items item
		WHERE item.submission_id = $1::uuid
		  AND ($2::text = '' OR item.resource_name ILIKE '%' || $2 || '%' OR item.resource_key ILIKE '%' || $2 || '%')
	`, current.ID, keyword).Scan(&total); err != nil {
		return pagination.Result[ReviewResource]{}, fmt.Errorf("count inventory review resources: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT item.resource_type, item.resource_id::text, item.resource_key, item.resource_name, item.snapshot_data
		FROM inventory_submission_items item
		WHERE item.submission_id = $1::uuid
		  AND ($2::text = '' OR item.resource_name ILIKE '%' || $2 || '%' OR item.resource_key ILIKE '%' || $2 || '%')
		ORDER BY item.resource_type, item.resource_key, item.resource_id
		LIMIT $3 OFFSET $4
	`, current.ID, keyword, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[ReviewResource]{}, fmt.Errorf("list inventory review resources: %w", err)
	}
	defer rows.Close()
	items := make([]ReviewResource, 0, page.PageSize)
	for rows.Next() {
		var item ReviewResource
		var snapshotData []byte
		if err := rows.Scan(&item.ResourceType, &item.ResourceID, &item.ResourceKey, &item.ResourceName, &snapshotData); err != nil {
			return pagination.Result[ReviewResource]{}, fmt.Errorf("scan inventory review resource: %w", err)
		}
		if err := json.Unmarshal(snapshotData, &item.SnapshotData); err != nil {
			return pagination.Result[ReviewResource]{}, fmt.Errorf("decode inventory review resource: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[ReviewResource]{}, fmt.Errorf("iterate inventory review resources: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

// SearchReviewComments returns historical per-resource review comments newest
// submission version first, without embedding the complete history in context.
func (s *Service) SearchReviewComments(ctx context.Context, principal auth.Principal, taskID, organizationID string, page pagination.Query) (pagination.Result[ReviewComment], error) {
	if _, _, err := s.loadSubmissionMetadata(ctx, principal, taskID, organizationID); err != nil {
		return pagination.Result[ReviewComment]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM inventory_submission_review_comments comment
		JOIN inventory_submissions submission ON submission.id = comment.submission_id
		WHERE submission.task_id::text = $1 AND submission.organization_id::text = $2
	`, taskID, organizationID).Scan(&total); err != nil {
		return pagination.Result[ReviewComment]{}, fmt.Errorf("count inventory review comments: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT comment.id::text, submission.id::text, submission.submission_version,
		       comment.resource_type, comment.resource_id::text, comment.resource_key,
		       comment.resource_name, comment.comment, COALESCE(creator.display_name, ''), comment.created_at
		FROM inventory_submission_review_comments comment
		JOIN inventory_submissions submission ON submission.id = comment.submission_id
		LEFT JOIN users creator ON creator.id = comment.created_by
		WHERE submission.task_id::text = $1 AND submission.organization_id::text = $2
		ORDER BY submission.submission_version DESC, comment.created_at DESC, comment.id DESC
		LIMIT $3 OFFSET $4
	`, taskID, organizationID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[ReviewComment]{}, fmt.Errorf("list inventory review comments: %w", err)
	}
	defer rows.Close()
	items := make([]ReviewComment, 0, page.PageSize)
	for rows.Next() {
		var item ReviewComment
		if err := rows.Scan(
			&item.ID, &item.SubmissionID, &item.SubmissionVersion,
			&item.ResourceType, &item.ResourceID, &item.ResourceKey,
			&item.ResourceName, &item.Comment, &item.CreatedByName, &item.CreatedAt,
		); err != nil {
			return pagination.Result[ReviewComment]{}, fmt.Errorf("scan inventory review comment: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[ReviewComment]{}, fmt.Errorf("iterate inventory review comments: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func insertReviewComments(ctx context.Context, tx pgx.Tx, principal auth.Principal, taskID, organizationID string, comments []ReviewCommentInput) error {
	if len(comments) == 0 {
		return nil
	}
	var submissionID string
	err := tx.QueryRow(ctx, `
		SELECT progress.latest_submission_id::text
		FROM inventory_task_organizations progress
		JOIN inventory_tasks task ON task.id = progress.task_id
		WHERE progress.task_id::text = $1 AND progress.organization_id::text = $2
		  AND task.status = 'active' AND task.require_review
		  AND progress.status = 'submitted' AND progress.latest_submission_id IS NOT NULL
		FOR UPDATE OF progress
	`, taskID, organizationID).Scan(&submissionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrState
	}
	if err != nil {
		return fmt.Errorf("load inventory review submission: %w", err)
	}
	for _, comment := range comments {
		result, err := tx.Exec(ctx, `
			INSERT INTO inventory_submission_review_comments (
				submission_id, resource_type, resource_id, resource_key, resource_name, comment, created_by
			)
			SELECT item.submission_id, item.resource_type, item.resource_id,
			       item.resource_key, item.resource_name, $4, $5::uuid
			FROM inventory_submission_items item
			WHERE item.submission_id::text = $1
			  AND item.resource_type = $2 AND item.resource_id::text = $3
		`, submissionID, comment.ResourceType, comment.ResourceID, comment.Comment, principal.UserID)
		if err != nil {
			return fmt.Errorf("create inventory review comment: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrInvalid
		}
	}
	return nil
}

func normalizeReviewComments(comments []ReviewCommentInput) ([]ReviewCommentInput, error) {
	if len(comments) > 100 {
		return nil, ErrInvalid
	}
	normalized := make([]ReviewCommentInput, 0, len(comments))
	seen := make(map[string]struct{}, len(comments))
	for _, item := range comments {
		item.ResourceType = strings.TrimSpace(item.ResourceType)
		item.ResourceID = strings.TrimSpace(item.ResourceID)
		item.Comment = strings.TrimSpace(item.Comment)
		if (item.ResourceType != "business_system" && item.ResourceType != "network_asset") ||
			item.ResourceID == "" || item.Comment == "" || len(item.Comment) > 2000 {
			return nil, ErrInvalid
		}
		key := item.ResourceType + ":" + item.ResourceID
		if _, exists := seen[key]; exists {
			return nil, ErrInvalid
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
	}
	return normalized, nil
}
