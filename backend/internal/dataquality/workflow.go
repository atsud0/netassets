package dataquality

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

const qualityPermission = "data_quality.manage"

type Issue struct {
	ID                  string     `json:"id"`
	RuleID              string     `json:"rule_id"`
	RuleName            string     `json:"rule_name"`
	TargetField         string     `json:"target_field"`
	RuleType            string     `json:"rule_type"`
	StaleAfterDays      int        `json:"stale_after_days,omitempty"`
	ExpiryWarningDays   int        `json:"expiry_warning_days,omitempty"`
	Severity            string     `json:"severity"`
	ResourceType        string     `json:"resource_type"`
	ResourceID          string     `json:"resource_id"`
	ResourceName        string     `json:"resource_name"`
	OrganizationID      string     `json:"organization_id"`
	OrganizationCode    string     `json:"organization_code"`
	OrganizationName    string     `json:"organization_name"`
	Status              string     `json:"status"`
	AssigneeUserID      string     `json:"assignee_user_id,omitempty"`
	AssigneeDisplayName string     `json:"assignee_display_name,omitempty"`
	RectificationNote   string     `json:"rectification_note,omitempty"`
	SubmittedByName     string     `json:"submitted_by_name,omitempty"`
	SubmittedAt         *time.Time `json:"submitted_at,omitempty"`
	ReviewNote          string     `json:"review_note,omitempty"`
	ReviewedByName      string     `json:"reviewed_by_name,omitempty"`
	ReviewedAt          *time.Time `json:"reviewed_at,omitempty"`
	ExemptionReason     string     `json:"exemption_reason,omitempty"`
	ExemptionExpiresAt  *time.Time `json:"exemption_expires_at,omitempty"`
	ResolutionType      string     `json:"resolution_type,omitempty"`
	FirstDetectedAt     time.Time  `json:"first_detected_at"`
	LastDetectedAt      time.Time  `json:"last_detected_at"`
	OccurrenceCount     int        `json:"occurrence_count"`
	ClosedAt            *time.Time `json:"closed_at,omitempty"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

type IssueAssignee struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

type IssueEvent struct {
	ID               int64          `json:"id"`
	EventType        string         `json:"event_type"`
	FromStatus       string         `json:"from_status,omitempty"`
	ToStatus         string         `json:"to_status"`
	Note             string         `json:"note,omitempty"`
	Details          map[string]any `json:"details"`
	ActorUserID      string         `json:"actor_user_id,omitempty"`
	ActorDisplayName string         `json:"actor_display_name,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
}

type IssueDetail struct {
	Issue Issue `json:"issue"`
}

type IssueFilter struct {
	OrganizationID string
	Status         string
	Severity       string
	ResourceType   string
	Assignee       string
	// Limit is kept for internal compatibility; HTTP uses Page.
	Limit int
	Page  pagination.Query
}

type IssueSummary struct {
	Total         int64 `json:"total"`
	Pending       int64 `json:"pending"`
	InProgress    int64 `json:"in_progress"`
	PendingReview int64 `json:"pending_review"`
	Closed        int64 `json:"closed"`
	Exempted      int64 `json:"exempted"`
	Error         int64 `json:"error"`
	Warning       int64 `json:"warning"`
}

type StartInput struct {
	AssigneeUserID string `json:"assignee_user_id"`
	Note           string `json:"note"`
}

type SubmitInput struct {
	Note string `json:"note"`
}

type ReviewInput struct {
	Approved bool   `json:"approved"`
	Note     string `json:"note"`
}

type ExemptInput struct {
	Reason    string `json:"reason"`
	ExpiresAt string `json:"expires_at"`
}

type ScanBatch struct {
	ID               string    `json:"id"`
	OrganizationID   string    `json:"organization_id,omitempty"`
	OrganizationName string    `json:"organization_name,omitempty"`
	ResourceType     string    `json:"resource_type,omitempty"`
	Severity         string    `json:"severity,omitempty"`
	AssetCount       int64     `json:"asset_count"`
	SystemCount      int64     `json:"system_count"`
	RuleCount        int       `json:"enabled_rule_count"`
	IssueCount       int64     `json:"total_issue_count"`
	ErrorCount       int64     `json:"error_count"`
	WarningCount     int64     `json:"warning_count"`
	AutoClosedCount  int64     `json:"auto_closed_count"`
	TriggerType      string    `json:"trigger_type"`
	ScheduledFor     string    `json:"scheduled_for,omitempty"`
	CreatedByName    string    `json:"created_by_name,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

const issueFields = `
	i.id::text,i.rule_id::text,i.rule_name,i.target_field,quality_rule.rule_type,COALESCE(quality_rule.stale_after_days,0),COALESCE(quality_rule.expiry_warning_days,0),i.severity,i.resource_type,i.resource_id::text,i.resource_name,
	i.organization_id::text,i.organization_code,i.organization_name,i.status,COALESCE(i.assignee_user_id::text,''),
	COALESCE(assignee.display_name,''),COALESCE(i.rectification_note,''),COALESCE(submitter.display_name,''),i.submitted_at,
	COALESCE(i.review_note,''),COALESCE(reviewer.display_name,''),i.reviewed_at,COALESCE(i.exemption_reason,''),
	i.exemption_expires_at,COALESCE(i.resolution_type,''),i.first_detected_at,i.last_detected_at,i.occurrence_count,i.closed_at,i.updated_at`

const accessibleQualityOrganizationsCTE = `
WITH RECURSIVE descendant_roots(id) AS (
	SELECT ur.organization_id FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='data_quality.manage'
	JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), descendants(id) AS (
	SELECT id FROM descendant_roots
	UNION
	SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
WHERE child.status='enabled' AND child.deleted_at IS NULL
), accessible(id) AS (
	SELECT ur.organization_id FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='data_quality.manage'
	JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
	UNION SELECT id FROM descendants
)`

func validateIssueFilter(filter *IssueFilter) error {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Severity = strings.TrimSpace(filter.Severity)
	filter.ResourceType = strings.TrimSpace(filter.ResourceType)
	filter.Assignee = strings.TrimSpace(filter.Assignee)
	if filter.OrganizationID != "" && !scanUUIDPattern.MatchString(filter.OrganizationID) {
		return ErrInvalid
	}
	if filter.Status != "" && !validIssueStatus(filter.Status) {
		return ErrInvalid
	}
	if filter.Severity != "" && filter.Severity != "error" && filter.Severity != "warning" {
		return ErrInvalid
	}
	if filter.ResourceType != "" && filter.ResourceType != "asset" && filter.ResourceType != "business_system" {
		return ErrInvalid
	}
	if filter.Assignee != "" && filter.Assignee != "me" && !scanUUIDPattern.MatchString(filter.Assignee) {
		return ErrInvalid
	}
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 500 {
		filter.Limit = 500
	}
	if filter.Page.Page == 0 && filter.Page.PageSize == 0 {
		pageSize := filter.Limit
		if pageSize > pagination.MaxPageSize {
			pageSize = pagination.MaxPageSize
		}
		page, err := pagination.Normalize(1, pageSize)
		if err != nil {
			return ErrInvalid
		}
		filter.Page = page
	}
	return nil
}

func validIssueStatus(value string) bool {
	switch value {
	case "pending", "in_progress", "pending_review", "closed", "exempted":
		return true
	default:
		return false
	}
}

func canUseQualityWorkflow(p auth.Principal) bool {
	return p.HasRole("group_admin") || p.HasPermission(qualityPermission)
}

func (s *Service) ListIssues(ctx context.Context, p auth.Principal, filter IssueFilter) ([]Issue, IssueSummary, error) {
	result, summary, err := s.SearchIssues(ctx, p, filter)
	return result.Items, summary, err
}

func (s *Service) SearchIssues(ctx context.Context, p auth.Principal, filter IssueFilter) (pagination.Result[Issue], IssueSummary, error) {
	if !canUseQualityWorkflow(p) {
		return pagination.Result[Issue]{}, IssueSummary{}, ErrForbidden
	}
	if err := validateIssueFilter(&filter); err != nil {
		return pagination.Result[Issue]{}, IssueSummary{}, err
	}
	groupAdmin := p.HasRole("group_admin")
	assignee := filter.Assignee
	if assignee == "me" {
		assignee = p.UserID
	}
	where := ` WHERE ($2::boolean OR i.organization_id IN (SELECT id FROM accessible))
		AND (NULLIF($3::text,'') IS NULL OR i.organization_id=$3::uuid)
		AND (NULLIF($4::text,'') IS NULL OR i.status=$4)
		AND (NULLIF($5::text,'') IS NULL OR i.severity=$5)
		AND (NULLIF($6::text,'') IS NULL OR i.resource_type=$6)
		AND (NULLIF($7::text,'') IS NULL OR i.assignee_user_id=$7::uuid)`
	args := []any{p.UserID, groupAdmin, filter.OrganizationID, filter.Status, filter.Severity, filter.ResourceType, assignee}
	var summary IssueSummary
	if err := s.pool.QueryRow(ctx, accessibleQualityOrganizationsCTE+`
		SELECT count(*),count(*) FILTER(WHERE i.status='pending'),count(*) FILTER(WHERE i.status='in_progress'),
		count(*) FILTER(WHERE i.status='pending_review'),count(*) FILTER(WHERE i.status='closed'),
		count(*) FILTER(WHERE i.status='exempted'),count(*) FILTER(WHERE i.severity='error'),count(*) FILTER(WHERE i.severity='warning')
		FROM data_quality_issues i`+where, args...).Scan(&summary.Total, &summary.Pending, &summary.InProgress, &summary.PendingReview, &summary.Closed, &summary.Exempted, &summary.Error, &summary.Warning); err != nil {
		return pagination.Result[Issue]{}, IssueSummary{}, fmt.Errorf("summarize data quality issues: %w", err)
	}
	rows, err := s.pool.Query(ctx, accessibleQualityOrganizationsCTE+`
		SELECT `+issueFields+`
		FROM data_quality_issues i
		JOIN data_quality_rules quality_rule ON quality_rule.id=i.rule_id
		LEFT JOIN users assignee ON assignee.id=i.assignee_user_id
		LEFT JOIN users submitter ON submitter.id=i.submitted_by
		LEFT JOIN users reviewer ON reviewer.id=i.reviewed_by`+where+`
		ORDER BY CASE i.status WHEN 'pending_review' THEN 0 WHEN 'pending' THEN 1 WHEN 'in_progress' THEN 2 WHEN 'exempted' THEN 3 ELSE 4 END,
		         CASE i.severity WHEN 'error' THEN 0 ELSE 1 END,i.updated_at DESC LIMIT $8 OFFSET $9`, append(args, filter.Page.PageSize, filter.Page.Offset())...)
	if err != nil {
		return pagination.Result[Issue]{}, IssueSummary{}, fmt.Errorf("search data quality issues: %w", err)
	}
	defer rows.Close()
	items := make([]Issue, 0, filter.Page.PageSize)
	for rows.Next() {
		item, err := scanIssue(rows)
		if err != nil {
			return pagination.Result[Issue]{}, IssueSummary{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Issue]{}, IssueSummary{}, err
	}
	return pagination.NewResult(items, int(summary.Total), filter.Page), summary, nil
}

func (s *Service) ListIssueAssignees(ctx context.Context, p auth.Principal, issueID string) ([]IssueAssignee, error) {
	result, err := s.SearchIssueAssignees(ctx, p, issueID, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

// SearchIssueAssignees returns a bounded page of organization users eligible to
// receive the issue. Although normally a small selector, it follows the same
// collection contract so a large subsidiary cannot create an unbounded response.
func (s *Service) SearchIssueAssignees(ctx context.Context, p auth.Principal, issueID string, page pagination.Query) (pagination.Result[IssueAssignee], error) {
	if !canUseQualityWorkflow(p) {
		return pagination.Result[IssueAssignee]{}, ErrForbidden
	}
	issueID = strings.TrimSpace(issueID)
	if !scanUUIDPattern.MatchString(issueID) {
		return pagination.Result[IssueAssignee]{}, ErrInvalid
	}
	var organizationID string
	if err := s.pool.QueryRow(ctx, `SELECT organization_id::text FROM data_quality_issues WHERE id=$1::uuid`, issueID).Scan(&organizationID); errors.Is(err, pgx.ErrNoRows) {
		return pagination.Result[IssueAssignee]{}, ErrNotFound
	} else if err != nil {
		return pagination.Result[IssueAssignee]{}, err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, p, organizationID, qualityPermission)
	if err != nil {
		return pagination.Result[IssueAssignee]{}, err
	}
	if !allowed {
		return pagination.Result[IssueAssignee]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `
		WITH candidates AS (
			SELECT DISTINCT u.id,u.username,u.display_name
			FROM users u
			JOIN user_organizations uo ON uo.user_id=u.id
			WHERE uo.organization_id=$1::uuid AND u.status='enabled' AND u.deleted_at IS NULL
		)
		SELECT id::text,username,display_name,count(*) OVER ()
		FROM candidates
		ORDER BY display_name,username
		LIMIT $2 OFFSET $3`, organizationID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[IssueAssignee]{}, err
	}
	defer rows.Close()
	items := make([]IssueAssignee, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item IssueAssignee
		if err := rows.Scan(&item.ID, &item.Username, &item.DisplayName, &total); err != nil {
			return pagination.Result[IssueAssignee]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[IssueAssignee]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `
			SELECT count(DISTINCT u.id)
			FROM users u JOIN user_organizations uo ON uo.user_id=u.id
			WHERE uo.organization_id=$1::uuid AND u.status='enabled' AND u.deleted_at IS NULL
		`, organizationID).Scan(&total); err != nil {
			return pagination.Result[IssueAssignee]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) GetIssue(ctx context.Context, p auth.Principal, issueID string) (IssueDetail, error) {
	if !canUseQualityWorkflow(p) || !scanUUIDPattern.MatchString(strings.TrimSpace(issueID)) {
		if !canUseQualityWorkflow(p) {
			return IssueDetail{}, ErrForbidden
		}
		return IssueDetail{}, ErrInvalid
	}
	item, err := scanIssue(s.pool.QueryRow(ctx, `SELECT `+issueFields+` FROM data_quality_issues i
		JOIN data_quality_rules quality_rule ON quality_rule.id=i.rule_id
		LEFT JOIN users assignee ON assignee.id=i.assignee_user_id
		LEFT JOIN users submitter ON submitter.id=i.submitted_by
		LEFT JOIN users reviewer ON reviewer.id=i.reviewed_by WHERE i.id=$1::uuid`, issueID))
	if errors.Is(err, pgx.ErrNoRows) {
		return IssueDetail{}, ErrNotFound
	}
	if err != nil {
		return IssueDetail{}, fmt.Errorf("load data quality issue: %w", err)
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, p, item.OrganizationID, qualityPermission)
	if err != nil {
		return IssueDetail{}, err
	}
	if !allowed {
		return IssueDetail{}, ErrForbidden
	}
	return IssueDetail{Issue: item}, nil
}

// SearchIssueEvents keeps the issue detail response bounded while preserving
// the complete workflow history through the standard collection contract.
func (s *Service) SearchIssueEvents(ctx context.Context, p auth.Principal, issueID string, page pagination.Query) (pagination.Result[IssueEvent], error) {
	if _, err := s.GetIssue(ctx, p, issueID); err != nil {
		return pagination.Result[IssueEvent]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_issue_events WHERE issue_id=$1::uuid`, issueID).Scan(&total); err != nil {
		return pagination.Result[IssueEvent]{}, fmt.Errorf("count data quality issue events: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id,e.event_type,COALESCE(e.from_status,''),e.to_status,COALESCE(e.note,''),e.details,
		       COALESCE(e.actor_user_id::text,''),COALESCE(u.display_name,''),e.created_at
		FROM data_quality_issue_events e
		LEFT JOIN users u ON u.id=e.actor_user_id
		WHERE e.issue_id=$1::uuid
		ORDER BY e.created_at DESC,e.id DESC
		LIMIT $2 OFFSET $3
	`, issueID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[IssueEvent]{}, fmt.Errorf("list data quality issue events: %w", err)
	}
	defer rows.Close()
	items := make([]IssueEvent, 0, page.PageSize)
	for rows.Next() {
		var item IssueEvent
		if err := rows.Scan(&item.ID, &item.EventType, &item.FromStatus, &item.ToStatus, &item.Note, &item.Details, &item.ActorUserID, &item.ActorDisplayName, &item.CreatedAt); err != nil {
			return pagination.Result[IssueEvent]{}, fmt.Errorf("scan data quality issue event: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[IssueEvent]{}, fmt.Errorf("iterate data quality issue events: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) StartIssue(ctx context.Context, p auth.Principal, issueID string, input StartInput, requestID, ipAddress string) (Issue, error) {
	return s.startIssue(ctx, p, issueID, input, []string{"pending", "in_progress"}, requestID, ipAddress)
}

func (s *Service) startIssue(ctx context.Context, p auth.Principal, issueID string, input StartInput, allowedStatuses []string, requestID, ipAddress string) (Issue, error) {
	input.AssigneeUserID = strings.TrimSpace(input.AssigneeUserID)
	input.Note = strings.TrimSpace(input.Note)
	if input.AssigneeUserID == "" {
		input.AssigneeUserID = p.UserID
	}
	if !scanUUIDPattern.MatchString(input.AssigneeUserID) || len(input.Note) > 5000 {
		return Issue{}, ErrInvalid
	}
	return s.transitionIssue(ctx, p, issueID, requestID, ipAddress, "started", allowedStatuses, "in_progress", input.Note, func(ctx context.Context, tx pgx.Tx, item Issue) error {
		return updateIssueAssignee(ctx, tx, item, input.AssigneeUserID, p.UserID)
	})
}

func (s *Service) AssignIssue(ctx context.Context, p auth.Principal, issueID string, input StartInput, requestID, ipAddress string) (Issue, error) {
	input.AssigneeUserID = strings.TrimSpace(input.AssigneeUserID)
	input.Note = strings.TrimSpace(input.Note)
	if !scanUUIDPattern.MatchString(input.AssigneeUserID) || len(input.Note) > 5000 {
		return Issue{}, ErrInvalid
	}
	return s.transitionIssue(ctx, p, issueID, requestID, ipAddress, "assigned", []string{"pending", "in_progress"}, "", input.Note, func(ctx context.Context, tx pgx.Tx, item Issue) error {
		return updateIssueAssignee(ctx, tx, item, input.AssigneeUserID, p.UserID)
	})
}

func updateIssueAssignee(ctx context.Context, tx pgx.Tx, item Issue, assigneeUserID, actorUserID string) error {
	var valid bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_organizations uo ON uo.user_id=u.id WHERE u.id=$1::uuid AND uo.organization_id=$2::uuid AND u.status='enabled' AND u.deleted_at IS NULL)`, assigneeUserID, item.OrganizationID).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrInvalid
	}
	_, err := tx.Exec(ctx, `UPDATE data_quality_issues SET assignee_user_id=$2::uuid,updated_by=$3::uuid,updated_at=now() WHERE id=$1::uuid`, item.ID, assigneeUserID, actorUserID)
	return err
}

func (s *Service) SubmitIssue(ctx context.Context, p auth.Principal, issueID string, input SubmitInput, requestID, ipAddress string) (Issue, error) {
	return s.submitIssue(ctx, p, issueID, input, []string{"pending", "in_progress"}, requestID, ipAddress)
}

func (s *Service) submitIssue(ctx context.Context, p auth.Principal, issueID string, input SubmitInput, allowedStatuses []string, requestID, ipAddress string) (Issue, error) {
	input.Note = strings.TrimSpace(input.Note)
	if input.Note == "" || len(input.Note) > 5000 {
		return Issue{}, ErrInvalid
	}
	return s.transitionIssue(ctx, p, issueID, requestID, ipAddress, "submitted", allowedStatuses, "pending_review", input.Note, func(ctx context.Context, tx pgx.Tx, item Issue) error {
		_, err := tx.Exec(ctx, `UPDATE data_quality_issues SET rectification_note=$2,submitted_by=$3::uuid,submitted_at=now(),assignee_user_id=COALESCE(assignee_user_id,$3::uuid),updated_by=$3::uuid,updated_at=now() WHERE id=$1::uuid`, item.ID, input.Note, p.UserID)
		return err
	})
}

func (s *Service) ReviewIssue(ctx context.Context, p auth.Principal, issueID string, input ReviewInput, requestID, ipAddress string) (Issue, error) {
	if !p.HasRole("group_admin") {
		return Issue{}, ErrForbidden
	}
	input.Note = strings.TrimSpace(input.Note)
	if (!input.Approved && input.Note == "") || len(input.Note) > 5000 {
		return Issue{}, ErrInvalid
	}
	eventType, next, resolution := "returned", "in_progress", ""
	if input.Approved {
		eventType, next, resolution = "approved", "closed", "corrected"
	}
	return s.transitionIssue(ctx, p, issueID, requestID, ipAddress, eventType, []string{"pending_review"}, next, input.Note, func(ctx context.Context, tx pgx.Tx, item Issue) error {
		_, err := tx.Exec(ctx, `UPDATE data_quality_issues SET review_note=$2,reviewed_by=$3::uuid,reviewed_at=now(),resolution_type=NULLIF($4,''),closed_at=CASE WHEN $5='closed' THEN now() ELSE NULL END,updated_by=$3::uuid,updated_at=now() WHERE id=$1::uuid`, item.ID, input.Note, p.UserID, resolution, next)
		return err
	})
}

func (s *Service) ExemptIssue(ctx context.Context, p auth.Principal, issueID string, input ExemptInput, requestID, ipAddress string) (Issue, error) {
	if !p.HasRole("group_admin") {
		return Issue{}, ErrForbidden
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.ExpiresAt = strings.TrimSpace(input.ExpiresAt)
	if input.Reason == "" || len(input.Reason) > 5000 {
		return Issue{}, ErrInvalid
	}
	var expiresAt *time.Time
	if input.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, input.ExpiresAt)
		if err != nil || !parsed.After(time.Now()) {
			return Issue{}, ErrInvalid
		}
		expiresAt = &parsed
	}
	return s.transitionIssue(ctx, p, issueID, requestID, ipAddress, "exempted", []string{"pending", "in_progress", "pending_review"}, "exempted", input.Reason, func(ctx context.Context, tx pgx.Tx, item Issue) error {
		_, err := tx.Exec(ctx, `UPDATE data_quality_issues SET exemption_reason=$2,exemption_expires_at=$3,resolution_type='exempted',closed_at=now(),reviewed_by=$4::uuid,reviewed_at=now(),updated_by=$4::uuid,updated_at=now() WHERE id=$1::uuid`, item.ID, input.Reason, expiresAt, p.UserID)
		return err
	})
}

func (s *Service) ReopenIssue(ctx context.Context, p auth.Principal, issueID, note, requestID, ipAddress string) (Issue, error) {
	if !p.HasRole("group_admin") {
		return Issue{}, ErrForbidden
	}
	note = strings.TrimSpace(note)
	if len(note) > 5000 {
		return Issue{}, ErrInvalid
	}
	return s.transitionIssue(ctx, p, issueID, requestID, ipAddress, "reopened", []string{"closed", "exempted"}, "pending", note, func(ctx context.Context, tx pgx.Tx, item Issue) error {
		_, err := tx.Exec(ctx, `UPDATE data_quality_issues SET resolution_type=NULL,closed_at=NULL,exemption_reason=NULL,exemption_expires_at=NULL,review_note=NULL,reviewed_by=NULL,reviewed_at=NULL,updated_by=$2::uuid,updated_at=now() WHERE id=$1::uuid`, item.ID, p.UserID)
		return err
	})
}

func (s *Service) transitionIssue(ctx context.Context, p auth.Principal, issueID, requestID, ipAddress, eventType string, allowedStatuses []string, nextStatus, note string, apply func(context.Context, pgx.Tx, Issue) error) (Issue, error) {
	if !canUseQualityWorkflow(p) {
		return Issue{}, ErrForbidden
	}
	issueID = strings.TrimSpace(issueID)
	if !scanUUIDPattern.MatchString(issueID) {
		return Issue{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Issue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanIssue(tx.QueryRow(ctx, `SELECT `+issueFields+` FROM data_quality_issues i
		JOIN data_quality_rules quality_rule ON quality_rule.id=i.rule_id
		LEFT JOIN users assignee ON assignee.id=i.assignee_user_id
		LEFT JOIN users submitter ON submitter.id=i.submitted_by
		LEFT JOIN users reviewer ON reviewer.id=i.reviewed_by WHERE i.id=$1::uuid FOR UPDATE OF i`, issueID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Issue{}, ErrNotFound
	}
	if err != nil {
		return Issue{}, err
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, p, item.OrganizationID, qualityPermission)
	if err != nil {
		return Issue{}, err
	}
	if !allowed {
		return Issue{}, ErrForbidden
	}
	if !containsString(allowedStatuses, item.Status) {
		return Issue{}, ErrInvalidTransition
	}
	fromStatus := item.Status
	if nextStatus == "" {
		nextStatus = fromStatus
	}
	if apply != nil {
		if err := apply(ctx, tx, item); err != nil {
			return Issue{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE data_quality_issues SET status=$2,updated_by=$3::uuid,updated_at=now() WHERE id=$1::uuid`, issueID, nextStatus, p.UserID); err != nil {
		return Issue{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO data_quality_issue_events(issue_id,actor_user_id,event_type,from_status,to_status,note) VALUES($1::uuid,$2::uuid,$3,$4,$5,NULLIF($6,''))`, issueID, p.UserID, eventType, fromStatus, nextStatus, note); err != nil {
		return Issue{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,$3,'data_quality_issue',$4,$5,jsonb_build_object('from_status',$6::text,'to_status',$7::text),NULLIF($8,'')::inet,NULLIF($9,''))`, p.UserID, item.OrganizationID, "data_quality_issue."+eventType, issueID, qualityEventLabel(eventType), fromStatus, nextStatus, ipAddress, requestID); err != nil {
		return Issue{}, err
	}
	updated, err := scanIssue(tx.QueryRow(ctx, `SELECT `+issueFields+` FROM data_quality_issues i
		JOIN data_quality_rules quality_rule ON quality_rule.id=i.rule_id
		LEFT JOIN users assignee ON assignee.id=i.assignee_user_id
		LEFT JOIN users submitter ON submitter.id=i.submitted_by
		LEFT JOIN users reviewer ON reviewer.id=i.reviewed_by WHERE i.id=$1::uuid`, issueID))
	if err != nil {
		return Issue{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Issue{}, err
	}
	return updated, nil
}

func (s *Service) ListScanBatches(ctx context.Context, p auth.Principal, limit int) ([]ScanBatch, error) {
	page, _ := pagination.Normalize(1, limit)
	result, err := s.SearchScanBatches(ctx, p, page)
	return result.Items, err
}

func (s *Service) SearchScanBatches(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[ScanBatch], error) {
	if !p.HasRole("group_admin") {
		return pagination.Result[ScanBatch]{}, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT b.id::text,COALESCE(b.organization_id::text,''),COALESCE(o.organization_name,''),COALESCE(b.resource_type,''),COALESCE(b.severity,''),b.asset_count,b.system_count,b.enabled_rule_count,b.total_issue_count,b.error_count,b.warning_count,b.auto_closed_count,b.trigger_type,COALESCE(to_char(b.scheduled_for,'YYYY-MM-DD'),''),COALESCE(u.display_name,''),b.created_at,count(*) OVER () FROM data_quality_scan_batches b LEFT JOIN organizations o ON o.id=b.organization_id LEFT JOIN users u ON u.id=b.created_by ORDER BY b.created_at DESC LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[ScanBatch]{}, err
	}
	defer rows.Close()
	items := make([]ScanBatch, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item ScanBatch
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.OrganizationName, &item.ResourceType, &item.Severity, &item.AssetCount, &item.SystemCount, &item.RuleCount, &item.IssueCount, &item.ErrorCount, &item.WarningCount, &item.AutoClosedCount, &item.TriggerType, &item.ScheduledFor, &item.CreatedByName, &item.CreatedAt, &total); err != nil {
			return pagination.Result[ScanBatch]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[ScanBatch]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_scan_batches`).Scan(&total); err != nil {
			return pagination.Result[ScanBatch]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func scanIssue(row pgx.Row) (Issue, error) {
	var item Issue
	err := row.Scan(&item.ID, &item.RuleID, &item.RuleName, &item.TargetField, &item.RuleType, &item.StaleAfterDays, &item.ExpiryWarningDays, &item.Severity, &item.ResourceType, &item.ResourceID, &item.ResourceName,
		&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.Status, &item.AssigneeUserID, &item.AssigneeDisplayName,
		&item.RectificationNote, &item.SubmittedByName, &item.SubmittedAt, &item.ReviewNote, &item.ReviewedByName, &item.ReviewedAt,
		&item.ExemptionReason, &item.ExemptionExpiresAt, &item.ResolutionType, &item.FirstDetectedAt, &item.LastDetectedAt,
		&item.OccurrenceCount, &item.ClosedAt, &item.UpdatedAt)
	return item, err
}

func containsString(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}

func qualityEventLabel(eventType string) string {
	return map[string]string{
		"assigned": "调整数据质量问题负责人", "started": "开始整改数据质量问题", "submitted": "提交数据质量问题复核", "approved": "复核关闭数据质量问题",
		"returned": "退回数据质量问题整改", "exempted": "豁免数据质量问题", "reopened": "重新打开数据质量问题",
	}[eventType]
}
