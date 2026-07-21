package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid input")
	ErrState     = errors.New("invalid state")
)

type OrganizationProgress struct {
	OrganizationID       string     `json:"organization_id"`
	OrganizationCode     string     `json:"organization_code"`
	OrganizationName     string     `json:"organization_name"`
	Status               string     `json:"status"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	SubmittedAt          *time.Time `json:"submitted_at,omitempty"`
	ReviewedAt           *time.Time `json:"reviewed_at,omitempty"`
	SubmissionNote       string     `json:"submission_note,omitempty"`
	SubmittedSystemCount int64      `json:"submitted_system_count"`
	SubmittedAssetCount  int64      `json:"submitted_asset_count"`
	SubmittedByName      string     `json:"submitted_by_name,omitempty"`
	ReviewedByName       string     `json:"reviewed_by_name,omitempty"`
	ReturnReason         string     `json:"return_reason,omitempty"`
}

type Task struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description,omitempty"`
	StartAt             time.Time              `json:"start_at"`
	DeadlineAt          time.Time              `json:"deadline_at"`
	Status              string                 `json:"status"`
	RequiredFields      []string               `json:"required_fields"`
	QualityRules        []string               `json:"quality_rules"`
	RequireReview       bool                   `json:"require_review"`
	AllowLateSubmission bool                   `json:"allow_late_submission"`
	TargetCount         int64                  `json:"target_count"`
	SubmittedCount      int64                  `json:"submitted_count"`
	ApprovedCount       int64                  `json:"approved_count"`
	CreatedByName       string                 `json:"created_by_name,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
	Organizations       []OrganizationProgress `json:"organizations"`
}

type CreateInput struct {
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	StartAt             time.Time `json:"start_at"`
	DeadlineAt          time.Time `json:"deadline_at"`
	Status              string    `json:"status"`
	RequiredFields      []string  `json:"required_fields"`
	QualityRules        []string  `json:"quality_rules"`
	RequireReview       bool      `json:"require_review"`
	AllowLateSubmission bool      `json:"allow_late_submission"`
	OrganizationIDs     []string  `json:"organization_ids"`
}

type SubmissionInput struct {
	Note string `json:"note"`
}

type ReviewInput struct {
	Decision     string `json:"decision"`
	ReturnReason string `json:"return_reason"`
}

type StatusInput struct {
	Status string `json:"status"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT t.id::text, t.task_name, COALESCE(t.description, ''), t.start_at, t.deadline_at, t.status,
		       t.required_fields, t.quality_rules, t.require_review, t.allow_late_submission,
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id),
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status IN ('submitted', 'approved')),
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status = 'approved'),
		       COALESCE(u.display_name, ''), t.created_at, t.updated_at
		FROM inventory_tasks t
		LEFT JOIN users u ON u.id = t.created_by
		WHERE $2::boolean OR (
			t.status <> 'draft' AND EXISTS (
				SELECT 1 FROM inventory_task_organizations ito
				WHERE ito.task_id = t.id AND ito.organization_id IN (SELECT id FROM accessible)
			)
		)
		ORDER BY t.created_at DESC
	`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list inventory tasks: %w", err)
	}
	defer rows.Close()

	items := make([]Task, 0)
	for rows.Next() {
		var item Task
		var requiredFieldsJSON, qualityRulesJSON []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.StartAt, &item.DeadlineAt, &item.Status,
			&requiredFieldsJSON, &qualityRulesJSON, &item.RequireReview, &item.AllowLateSubmission,
			&item.TargetCount, &item.SubmittedCount, &item.ApprovedCount, &item.CreatedByName,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan inventory task: %w", err)
		}
		if err := json.Unmarshal(requiredFieldsJSON, &item.RequiredFields); err != nil {
			return nil, fmt.Errorf("decode required fields: %w", err)
		}
		if err := json.Unmarshal(qualityRulesJSON, &item.QualityRules); err != nil {
			return nil, fmt.Errorf("decode quality rules: %w", err)
		}
		organizations, err := s.listOrganizations(ctx, principal, item.ID)
		if err != nil {
			return nil, err
		}
		item.Organizations = organizations
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory tasks: %w", err)
	}
	return items, nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (Task, error) {
	if !principal.HasRole("group_admin") {
		return Task{}, ErrForbidden
	}
	if err := normalizeCreateInput(&input); err != nil {
		return Task{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("begin inventory task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	organizationRows, err := tx.Query(ctx, `
		SELECT id::text, organization_code, organization_name
		FROM organizations
		WHERE id::text = ANY($1) AND status = 'enabled' AND organization_type <> 'group'
		ORDER BY organization_code
	`, input.OrganizationIDs)
	if err != nil {
		return Task{}, fmt.Errorf("validate inventory task organizations: %w", err)
	}
	organizations := make([]OrganizationProgress, 0, len(input.OrganizationIDs))
	for organizationRows.Next() {
		var organization OrganizationProgress
		if err := organizationRows.Scan(&organization.OrganizationID, &organization.OrganizationCode, &organization.OrganizationName); err != nil {
			organizationRows.Close()
			return Task{}, fmt.Errorf("scan inventory task organization: %w", err)
		}
		organization.Status = "not_started"
		organizations = append(organizations, organization)
	}
	if err := organizationRows.Err(); err != nil {
		organizationRows.Close()
		return Task{}, fmt.Errorf("iterate inventory task organizations: %w", err)
	}
	organizationRows.Close()
	if len(organizations) != len(input.OrganizationIDs) {
		return Task{}, ErrNotFound
	}

	requiredFieldsJSON, _ := json.Marshal(input.RequiredFields)
	qualityRulesJSON, _ := json.Marshal(input.QualityRules)
	var item Task
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_tasks (
			task_name, description, start_at, deadline_at, status, required_fields, quality_rules,
			require_review, allow_late_submission, created_by
		) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6::jsonb, $7::jsonb, $8, $9, $10::uuid)
		RETURNING id::text, task_name, COALESCE(description, ''), start_at, deadline_at, status,
		          required_fields, quality_rules, require_review, allow_late_submission, created_at, updated_at
	`, input.Name, input.Description, input.StartAt, input.DeadlineAt, input.Status, requiredFieldsJSON, qualityRulesJSON,
		input.RequireReview, input.AllowLateSubmission, principal.UserID).Scan(
		&item.ID, &item.Name, &item.Description, &item.StartAt, &item.DeadlineAt, &item.Status,
		&requiredFieldsJSON, &qualityRulesJSON, &item.RequireReview, &item.AllowLateSubmission,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("insert inventory task: %w", err)
	}
	for _, organization := range organizations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_task_organizations (task_id, organization_id)
			VALUES ($1::uuid, $2::uuid)
		`, item.ID, organization.OrganizationID); err != nil {
			return Task{}, fmt.Errorf("attach inventory task organization: %w", err)
		}
	}
	if err := writeAudit(ctx, tx, principal, principalOrganizationID(principal), "inventory_task.create", item.ID,
		"创建填报任务", input.Name, input.Status, "", len(organizations), requestID, ipAddress); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit inventory task: %w", err)
	}
	item.RequiredFields = input.RequiredFields
	item.QualityRules = input.QualityRules
	item.TargetCount = int64(len(organizations))
	item.CreatedByName = principal.DisplayName
	item.Organizations = organizations
	return item, nil
}

func (s *Service) Start(ctx context.Context, principal auth.Principal, taskID, organizationID, requestID, ipAddress string) (OrganizationProgress, error) {
	if principal.HasRole("group_admin") {
		return OrganizationProgress{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("begin inventory task start: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, contributorUpdatePrefix+`
		UPDATE inventory_task_organizations ito
		SET status = 'in_progress', started_at = COALESCE(ito.started_at, now()), updated_at = now()
		FROM inventory_tasks t
		WHERE t.id = ito.task_id AND t.id::text = $2 AND ito.organization_id::text = $3
		  AND ito.organization_id IN (SELECT id FROM accessible)
		  AND t.status = 'active' AND t.start_at <= now()
		  AND (t.deadline_at >= now() OR t.allow_late_submission)
		  AND ito.status IN ('not_started', 'returned', 'overdue')
	`, principal.UserID, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("start inventory task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return OrganizationProgress{}, ErrState
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "inventory_task.start", taskID,
		"开始填报任务", "", "in_progress", "", 0, requestID, ipAddress); err != nil {
		return OrganizationProgress{}, err
	}
	item, err := loadOrganizationProgress(ctx, tx, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProgress{}, fmt.Errorf("commit inventory task start: %w", err)
	}
	return item, nil
}

func (s *Service) Submit(ctx context.Context, principal auth.Principal, taskID, organizationID string, input SubmissionInput, requestID, ipAddress string) (OrganizationProgress, error) {
	if principal.HasRole("group_admin") {
		return OrganizationProgress{}, ErrForbidden
	}
	input.Note = strings.TrimSpace(input.Note)
	if len(input.Note) > 5000 {
		return OrganizationProgress{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("begin inventory task submission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, contributorUpdatePrefix+`
		UPDATE inventory_task_organizations ito
		SET status = CASE WHEN t.require_review THEN 'submitted' ELSE 'approved' END,
		    started_at = COALESCE(ito.started_at, now()), submitted_by = $1::uuid, submitted_at = now(),
		    submission_note = NULLIF($4, ''),
		    submitted_system_count = (
				SELECT count(*) FROM business_systems bs
				WHERE bs.owner_organization_id = ito.organization_id AND bs.deleted_at IS NULL
			),
		    submitted_asset_count = (
				SELECT count(*) FROM assets a
				JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
				WHERE ao.organization_id = ito.organization_id AND a.deleted_at IS NULL
			),
		    reviewed_by = NULL,
		    reviewed_at = CASE WHEN t.require_review THEN NULL ELSE now() END,
		    return_reason = NULL,
		    updated_at = now()
		FROM inventory_tasks t
		WHERE t.id = ito.task_id AND t.id::text = $2 AND ito.organization_id::text = $3
		  AND ito.organization_id IN (SELECT id FROM accessible)
		  AND t.status = 'active' AND t.start_at <= now()
		  AND (t.deadline_at >= now() OR t.allow_late_submission)
		  AND ito.status IN ('not_started', 'in_progress', 'returned', 'overdue')
	`, principal.UserID, taskID, organizationID, input.Note)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("submit inventory task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return OrganizationProgress{}, ErrState
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "inventory_task.submit", taskID,
		"提交填报任务", input.Note, "submitted", "", 0, requestID, ipAddress); err != nil {
		return OrganizationProgress{}, err
	}
	if err := completeIfApproved(ctx, tx, taskID); err != nil {
		return OrganizationProgress{}, err
	}
	item, err := loadOrganizationProgress(ctx, tx, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProgress{}, fmt.Errorf("commit inventory task submission: %w", err)
	}
	return item, nil
}

func (s *Service) Review(ctx context.Context, principal auth.Principal, taskID, organizationID string, input ReviewInput, requestID, ipAddress string) (OrganizationProgress, error) {
	if !principal.HasRole("group_admin") {
		return OrganizationProgress{}, ErrForbidden
	}
	if err := normalizeReviewInput(&input); err != nil {
		return OrganizationProgress{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("begin inventory task review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, `
		UPDATE inventory_task_organizations ito
		SET status = $3,
		    reviewed_by = $4::uuid,
		    reviewed_at = now(),
		    return_reason = CASE WHEN $3 = 'returned' THEN $5 ELSE NULL END,
		    updated_at = now()
		FROM inventory_tasks t
		WHERE t.id = ito.task_id AND t.id::text = $1 AND ito.organization_id::text = $2
		  AND t.status = 'active' AND t.require_review AND ito.status = 'submitted'
	`, taskID, organizationID, input.Decision, principal.UserID, input.ReturnReason)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("review inventory task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return OrganizationProgress{}, ErrState
	}
	summary := "审核通过填报任务"
	if input.Decision == "returned" {
		summary = "退回填报任务"
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "inventory_task.review", taskID,
		summary, "", input.Decision, input.ReturnReason, 0, requestID, ipAddress); err != nil {
		return OrganizationProgress{}, err
	}
	if input.Decision == "approved" {
		if err := completeIfApproved(ctx, tx, taskID); err != nil {
			return OrganizationProgress{}, err
		}
	}
	item, err := loadOrganizationProgress(ctx, tx, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProgress{}, fmt.Errorf("commit inventory task review: %w", err)
	}
	return item, nil
}

func (s *Service) UpdateStatus(ctx context.Context, principal auth.Principal, taskID string, input StatusInput, requestID, ipAddress string) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status != "active" && input.Status != "closed" && input.Status != "archived" {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin inventory task status update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE inventory_tasks
		SET status = $2, updated_at = now()
		WHERE id::text = $1 AND (
			(status = 'draft' AND $2 = 'active') OR
			(status = 'active' AND $2 = 'closed') OR
			(status IN ('draft', 'closed', 'completed') AND $2 = 'archived')
		)
	`, taskID, input.Status)
	if err != nil {
		return fmt.Errorf("update inventory task status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrState
	}
	if err := writeAudit(ctx, tx, principal, principalOrganizationID(principal), "inventory_task.status_update", taskID,
		"更新填报任务状态", "", input.Status, "", 0, requestID, ipAddress); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inventory task status update: %w", err)
	}
	return nil
}

func (s *Service) listOrganizations(ctx context.Context, principal auth.Principal, taskID string) ([]OrganizationProgress, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT o.id::text, o.organization_code, o.organization_name, ito.status,
		       ito.started_at, ito.submitted_at, ito.reviewed_at, COALESCE(ito.submission_note, ''),
		       ito.submitted_system_count, ito.submitted_asset_count,
		       COALESCE(submitter.display_name, ''), COALESCE(reviewer.display_name, ''), COALESCE(ito.return_reason, '')
		FROM inventory_task_organizations ito
		JOIN organizations o ON o.id = ito.organization_id
		LEFT JOIN users submitter ON submitter.id = ito.submitted_by
		LEFT JOIN users reviewer ON reviewer.id = ito.reviewed_by
		WHERE ito.task_id::text = $2 AND ($3::boolean OR o.id IN (SELECT id FROM accessible))
		ORDER BY o.organization_code
	`, principal.UserID, taskID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list inventory task organizations: %w", err)
	}
	defer rows.Close()
	items := make([]OrganizationProgress, 0)
	for rows.Next() {
		var item OrganizationProgress
		if err := scanOrganizationProgress(rows, &item); err != nil {
			return nil, fmt.Errorf("scan inventory task organization progress: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory task organization progress: %w", err)
	}
	return items, nil
}

const contributorUpdatePrefix = `
	WITH RECURSIVE descendant_roots(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
	), descendants(id) AS (
		SELECT id FROM descendant_roots
		UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
	), accessible(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
		  AND ur.data_scope IN ('organization', 'organization_and_descendants')
		UNION SELECT id FROM descendants
	)
`

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadOrganizationProgress(ctx context.Context, q queryRower, taskID, organizationID string) (OrganizationProgress, error) {
	var item OrganizationProgress
	err := q.QueryRow(ctx, `
		SELECT o.id::text, o.organization_code, o.organization_name, ito.status,
		       ito.started_at, ito.submitted_at, ito.reviewed_at, COALESCE(ito.submission_note, ''),
		       ito.submitted_system_count, ito.submitted_asset_count,
		       COALESCE(submitter.display_name, ''), COALESCE(reviewer.display_name, ''), COALESCE(ito.return_reason, '')
		FROM inventory_task_organizations ito
		JOIN organizations o ON o.id = ito.organization_id
		LEFT JOIN users submitter ON submitter.id = ito.submitted_by
		LEFT JOIN users reviewer ON reviewer.id = ito.reviewed_by
		WHERE ito.task_id::text = $1 AND ito.organization_id::text = $2
	`, taskID, organizationID)
	if err := scanOrganizationProgress(err, &item); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrganizationProgress{}, ErrNotFound
		}
		return OrganizationProgress{}, fmt.Errorf("load inventory task organization progress: %w", err)
	}
	return item, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanOrganizationProgress(row rowScanner, item *OrganizationProgress) error {
	return row.Scan(&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.Status,
		&item.StartedAt, &item.SubmittedAt, &item.ReviewedAt, &item.SubmissionNote,
		&item.SubmittedSystemCount, &item.SubmittedAssetCount, &item.SubmittedByName,
		&item.ReviewedByName, &item.ReturnReason)
}

func completeIfApproved(ctx context.Context, tx pgx.Tx, taskID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_tasks t
		SET status = 'completed', updated_at = now()
		WHERE t.id::text = $1 AND t.status = 'active'
		  AND NOT EXISTS (
			SELECT 1 FROM inventory_task_organizations ito
			WHERE ito.task_id = t.id AND ito.status <> 'approved'
		  )
	`, taskID); err != nil {
		return fmt.Errorf("complete inventory task: %w", err)
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, organizationID, action, resourceID, summary, note, status, reason string, targetCount int, requestID, ipAddress string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, 'inventory_task', $4, $5,
		        jsonb_build_object('note', $6::text, 'status', $7::text, 'return_reason', $8::text, 'target_count', $9::int),
		        NULLIF($10, '')::inet, NULLIF($11, ''))
	`, principal.UserID, organizationID, action, resourceID, summary, note, status, reason, targetCount, ipAddress, requestID); err != nil {
		return fmt.Errorf("write inventory task audit: %w", err)
	}
	return nil
}

func normalizeCreateInput(input *CreateInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.TrimSpace(input.Status)
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.Name == "" || len(input.Name) > 255 || len(input.Description) > 5000 || input.StartAt.IsZero() ||
		input.DeadlineAt.IsZero() || !input.DeadlineAt.After(input.StartAt) ||
		(input.Status != "draft" && input.Status != "active") {
		return ErrInvalid
	}
	input.OrganizationIDs = uniqueNonEmpty(input.OrganizationIDs)
	input.RequiredFields = uniqueNonEmpty(input.RequiredFields)
	input.QualityRules = uniqueNonEmpty(input.QualityRules)
	if len(input.OrganizationIDs) == 0 || len(input.OrganizationIDs) > 1000 || len(input.RequiredFields) > 100 || len(input.QualityRules) > 100 {
		return ErrInvalid
	}
	return nil
}

func normalizeReviewInput(input *ReviewInput) error {
	input.Decision = strings.TrimSpace(input.Decision)
	input.ReturnReason = strings.TrimSpace(input.ReturnReason)
	if input.Decision != "approved" && input.Decision != "returned" {
		return ErrInvalid
	}
	if input.Decision == "returned" && (input.ReturnReason == "" || len(input.ReturnReason) > 2000) {
		return ErrInvalid
	}
	if input.Decision == "approved" {
		input.ReturnReason = ""
	}
	return nil
}

func principalOrganizationID(principal auth.Principal) string {
	if principal.Organization == nil {
		return ""
	}
	return principal.Organization.ID
}

func uniqueNonEmpty(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
