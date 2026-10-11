package remediation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

type Material struct {
	Title            string   `json:"title"`
	Severity         string   `json:"severity"`
	AffectedLocation string   `json:"affected_location"`
	Description      string   `json:"description"`
	Evidence         string   `json:"evidence"`
	Advice           string   `json:"remediation_advice"`
	Requirement      string   `json:"requirement"`
	AttachmentIDs    []string `json:"attachment_ids"`
}

func (m Material) valid() bool {
	return textOK(m.Title, 255, true) && contains([]string{"critical", "high", "medium", "low", "info"}, m.Severity) &&
		textOK(m.Description, 20000, true) && textOK(m.Requirement, 20000, true) && textOK(m.Evidence, 20000, false) && textOK(m.Advice, 20000, false) && textOK(m.AffectedLocation, 2000, false)
}

type AssignInput struct {
	FindingID          string    `json:"finding_id"`
	GrantID            string    `json:"grant_id"`
	DueAt              time.Time `json:"due_at"`
	TargetEnvironment  string    `json:"target_environment"`
	DeploymentRequired bool      `json:"deployment_required"`
	Material           Material  `json:"material"`
}

func writeMaterial(ctx context.Context, tx pgx.Tx, p auth.Principal, id string, revision int, m Material) error {
	_, err := tx.Exec(ctx, `INSERT INTO remediation_material_versions(task_id,revision,title,severity,affected_location,description,evidence,remediation_advice,requirement,approved_by,source_revision,target_environment,deployment_required)
 SELECT $1::uuid,$2,$3,$4,$5,$6,$7,$8,$9,$10::uuid,source_revision,target_environment,deployment_required FROM remediation_tasks WHERE id=$1::uuid`, id, revision, m.Title, m.Severity, m.AffectedLocation, m.Description, m.Evidence, m.Advice, m.Requirement, p.UserID)
	if err != nil {
		return err
	}
	if len(m.AttachmentIDs) > 30 {
		return ErrInvalid
	}
	for _, file := range m.AttachmentIDs {
		if !validID(file) {
			return ErrInvalid
		}
		tag, e := tx.Exec(ctx, `INSERT INTO remediation_material_attachments(task_id,material_revision,attachment_id) SELECT $1::uuid,$2,id FROM remediation_attachments WHERE id=$3::uuid AND task_id=$1::uuid AND visibility='participants' ON CONFLICT DO NOTHING`, id, revision, file)
		if e != nil {
			return e
		}
		if tag.RowsAffected() != 1 {
			return ErrInvalid
		}
	}
	return nil
}
func (s *Service) Assign(ctx context.Context, p auth.Principal, orderID, key string, in AssignInput) (Object, error) {
	if !validID(orderID) || !validID(in.FindingID) || !validID(in.GrantID) || !in.Material.valid() || !textOK(in.TargetEnvironment, 255, true) || !in.DueAt.After(time.Now()) {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var org, state, mode string
	var orderDue time.Time
	err = tx.QueryRow(ctx, `SELECT assignee_organization_id::text,status,execution_mode,due_at FROM security_work_orders WHERE id=$1::uuid FOR UPDATE`, orderID).Scan(&org, &state, &mode, &orderDue)
	if err != nil {
		return nil, dbError(err)
	}
	if err = allowed(ctx, tx, p, org, "remediation.assign"); err != nil {
		return nil, err
	}
	previous, fingerprint, err := idempotent(ctx, tx, p, "assign:"+orderID, key, in)
	if err != nil {
		return nil, err
	}
	if previous != nil {
		return previous, nil
	}
	if mode != "individual" || state != "in_progress" || in.DueAt.After(orderDue) {
		return nil, ErrConflict
	}
	var system, severity string
	err = tx.QueryRow(ctx, `SELECT COALESCE(COALESCE(f.business_system_id,(SELECT sa.business_system_id FROM system_assets sa WHERE sa.asset_id=f.asset_id AND sa.is_primary ORDER BY sa.business_system_id LIMIT 1))::text,''),f.severity FROM security_work_order_findings l
 JOIN security_findings f ON f.id=l.finding_id LEFT JOIN assets a ON a.id=f.asset_id
 WHERE l.work_order_id=$1::uuid AND f.id=$2::uuid AND f.assignee_organization_id=$3::uuid AND f.status IN ('pending','in_progress') FOR UPDATE OF f`, orderID, in.FindingID, org).Scan(&system, &severity)
	if err != nil {
		return nil, dbError(err)
	}
	if !validID(system) || severity != in.Material.Severity {
		return nil, ErrInvalid
	}
	var assignee string
	err = tx.QueryRow(ctx, `SELECT user_id::text FROM remediation_grants WHERE id=$1::uuid AND remediation_grant_valid(id,user_id,$2::uuid,$3::uuid) AND valid_until >= $4`, in.GrantID, org, system, in.DueAt).Scan(&assignee)
	if err != nil {
		return nil, dbError(err)
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO remediation_tasks(task_no,work_order_id,finding_id,organization_id,business_system_id,assignee_user_id,grant_id,due_at,original_due_at,target_environment,deployment_required,created_by)
 VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8,$8,$9,$10,$11::uuid) RETURNING id::text`, "RT-"+strings.ToUpper(token()[:12]), orderID, in.FindingID, org, system, assignee, in.GrantID, in.DueAt, in.TargetEnvironment, in.DeploymentRequired, p.UserID).Scan(&id)
	if err != nil {
		return nil, dbError(err)
	}
	if err = writeMaterial(ctx, tx, p, id, 1, in.Material); err != nil {
		return nil, err
	}
	t, err := loadTask(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = event(ctx, tx, p, t, "assign", "participants", Object{"assignee_user_id": assignee, "grant_id": in.GrantID}); err != nil {
		return nil, err
	}
	out := Object{"id": id, "finding_id": in.FindingID}
	if err = saveResult(ctx, tx, p, "assign:"+orderID, key, fingerprint, out); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

const taskSelect = `SELECT t.id,t.task_no,t.work_order_id,w.work_order_no,w.status work_order_status,t.finding_id,t.organization_id,o.organization_name,
 t.business_system_id,b.system_name,t.assignee_user_id,t.assignee_snapshot->>'assignee_name' assignee_name,t.assignee_snapshot->>'vendor_name' vendor_name,
 t.status,t.completion_reason,t.version,t.material_revision,t.current_submission_id,t.due_at,t.original_due_at,
 t.target_environment,t.deployment_required,t.progress,t.blocked_reason,t.blocked_dependency,t.blocked_until,t.expected_completion_at,t.material_needs_review,t.source_revision,t.return_reason,t.accepted_at,t.completed_at,t.created_at,t.updated_at,
 (t.completion_reason='risk_accepted' AND f.status='risk_accepted' AND f.risk_accepted_until>now()) risk_acceptance_current,
 m.title,m.severity,m.affected_location,m.description,m.evidence,m.remediation_advice,m.requirement,
 (t.status NOT IN ('completed','cancelled') AND t.due_at<now()) overdue,
 (NOT remediation_grant_valid(t.grant_id,t.assignee_user_id,t.organization_id,t.business_system_id) OR NOT remediation_task_current(t.id)) needs_reassignment
 FROM remediation_tasks t JOIN security_findings f ON f.id=t.finding_id JOIN security_work_orders w ON w.id=t.work_order_id JOIN organizations o ON o.id=t.organization_id
 JOIN business_systems b ON b.id=t.business_system_id JOIN users u ON u.id=t.assignee_user_id
 JOIN remediation_grants g ON g.id=t.grant_id LEFT JOIN vendors v ON v.id=g.vendor_id
 JOIN remediation_material_versions m ON m.task_id=t.id AND m.revision=t.material_revision `

type Filter struct {
	OrderID, Status, Keyword, SystemID, Severity                                                                  string
	Mine, Overdue, DueSoon, Returned, NeedsReassignment, Blocked, PendingRequest, ReviewMine, MaterialNeedsReview bool
	Page                                                                                                          pagination.Query
}

func taskQuery(p auth.Principal, in Filter) (string, []any, error) {
	if in.OrderID != "" && !validID(in.OrderID) || in.SystemID != "" && !validID(in.SystemID) || len(in.Keyword) > 200 {
		return "", nil, ErrInvalid
	}
	if in.Status != "" && !contains([]string{"all", "active", "pending_acceptance", "in_progress", "pending_org_review", "pending_security_review", "completed", "cancelled"}, in.Status) {
		return "", nil, ErrInvalid
	}
	if in.Severity != "" && !contains([]string{"critical", "high", "medium", "low", "info"}, in.Severity) {
		return "", nil, ErrInvalid
	}
	if in.Mine && in.Status == "" {
		in.Status = "active"
	}
	sql := taskSelect + ` WHERE remediation_can_read($1::uuid,t.id)
 AND (NOT $2 OR (t.assignee_user_id=$1::uuid AND remediation_grant_valid(t.grant_id,$1::uuid,t.organization_id,t.business_system_id)))
 AND ($3='' OR t.work_order_id=NULLIF($3,'')::uuid)
 AND ($4 IN ('','all') OR ($4='active' AND t.status NOT IN ('completed','cancelled')) OR t.status=$4)
 AND ($5='' OR m.title ILIKE '%'||$5||'%' OR t.task_no ILIKE '%'||$5||'%')
 AND (NOT $6 OR (t.due_at<now() AND t.status NOT IN ('completed','cancelled')))
 AND ($7='' OR t.business_system_id=NULLIF($7,'')::uuid) AND ($8='' OR m.severity=$8)
 AND (NOT $9 OR (t.due_at>=now() AND t.due_at<now()+interval '3 days' AND t.status NOT IN ('completed','cancelled')))
 AND (NOT $10 OR (t.return_reason<>'' AND t.status NOT IN ('completed','cancelled')))
 AND (NOT $11 OR NOT remediation_grant_valid(t.grant_id,t.assignee_user_id,t.organization_id,t.business_system_id) OR NOT remediation_task_current(t.id))
 AND (NOT $12 OR t.blocked_reason<>'')
 AND (NOT $13 OR EXISTS(SELECT 1 FROM remediation_requests r WHERE r.task_id=t.id AND r.status='pending'))
 AND (NOT $14 OR (t.status='pending_org_review' AND remediation_can_manage($1::uuid,t.organization_id,'remediation.review')) OR (t.status='pending_security_review' AND remediation_is_security($1::uuid)))
 AND (NOT $15 OR t.material_needs_review)`
	return sql, []any{p.UserID, in.Mine, in.OrderID, in.Status, in.Keyword, in.Overdue, in.SystemID, in.Severity, in.DueSoon, in.Returned, in.NeedsReassignment, in.Blocked, in.PendingRequest, in.ReviewMine, in.MaterialNeedsReview}, nil
}
func (s *Service) List(ctx context.Context, p auth.Principal, in Filter) (pagination.Result[Object], error) {
	sql, args, err := taskQuery(p, in)
	if err != nil {
		return pagination.Result[Object]{}, err
	}
	sql += ` ORDER BY (t.return_reason<>'' AND t.status NOT IN ('completed','cancelled')) DESC,(t.due_at<now() AND t.status NOT IN ('completed','cancelled')) DESC,t.due_at,CASE m.severity WHEN 'critical' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END,t.id`
	return pageObjects(ctx, s.pool, sql, in.Page, args...)
}
func (s *Service) Detail(ctx context.Context, p auth.Principal, id string, executor bool) (Object, error) {
	if !validID(id) {
		return nil, ErrNotFound
	}
	t, err := loadTask(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	if err = authorizeTask(ctx, s.pool, p, t, executor); err != nil {
		return nil, err
	}
	out, err := readObject(ctx, s.pool, taskSelect+` WHERE t.id=$1::uuid AND remediation_can_read($2::uuid,t.id)`, id, p.UserID)
	if err != nil {
		return nil, err
	}
	out["can_assign"] = !executor && allowed(ctx, s.pool, p, t.OrganizationID, "remediation.assign") == nil
	out["can_review"] = !executor && allowed(ctx, s.pool, p, t.OrganizationID, "remediation.review") == nil
	out["can_verify"] = !executor && security(ctx, s.pool, p)
	out["can_execute"] = authorizeTask(ctx, s.pool, p, t, true) == nil
	if !executor && internal(ctx, s.pool, p, t) {
		source, e := readObject(ctx, s.pool, `SELECT title,severity,description,COALESCE(evidence,'') evidence,COALESCE(remediation_advice,'') remediation_advice FROM security_findings WHERE id=$1::uuid`, t.FindingID)
		if e != nil {
			return nil, e
		}
		out["source_material"] = source
	}
	ids, e := readList(ctx, s.pool, `SELECT a.id,a.original_name FROM remediation_material_attachments l JOIN remediation_attachments a ON a.id=l.attachment_id WHERE l.task_id=$1::uuid AND l.material_revision=$2 ORDER BY a.created_at`, id, t.MaterialRevision)
	if e != nil {
		return nil, e
	}
	out["material_attachments"] = ids
	return out, nil
}
func (s *Service) Children(ctx context.Context, p auth.Principal, id, kind string, executor bool, page pagination.Query) (pagination.Result[Object], error) {
	if !validID(id) {
		return pagination.Result[Object]{}, ErrNotFound
	}
	t, err := loadTask(ctx, s.pool, id)
	if err != nil {
		return pagination.Result[Object]{}, err
	}
	if err = authorizeTask(ctx, s.pool, p, t, executor); err != nil {
		return pagination.Result[Object]{}, err
	}
	isInternal := !executor && internal(ctx, s.pool, p, t)
	queries := map[string]string{
		"materials":   `SELECT m.*,($2 OR false) internal_view,COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'original_name',a.original_name)) FROM remediation_material_attachments l JOIN remediation_attachments a ON a.id=l.attachment_id WHERE l.task_id=m.task_id AND l.material_revision=m.revision),'[]'::jsonb) attachments FROM remediation_material_versions m WHERE m.task_id=$1::uuid ORDER BY m.revision DESC`,
		"comments":    `SELECT c.id,c.author_id,c.identity_snapshot->>'actor_name' author_name,c.visibility,c.body,c.created_at FROM remediation_comments c JOIN users u ON u.id=c.author_id WHERE c.task_id=$1::uuid AND ($2 OR c.visibility='participants') ORDER BY c.created_at DESC,c.id`,
		"events":      `SELECT e.id,e.action,e.details,e.created_at,e.identity_snapshot->>'actor_name' actor_name FROM remediation_task_events e JOIN users u ON u.id=e.actor_id WHERE e.task_id=$1::uuid AND ($2 OR e.visibility='participants') ORDER BY e.id DESC`,
		"attachments": `SELECT a.id,a.original_name,a.content_type,a.size_bytes,a.visibility,a.created_at,a.identity_snapshot->>'actor_name' uploaded_by_name FROM remediation_attachments a JOIN users u ON u.id=a.uploaded_by WHERE a.task_id=$1::uuid AND ($2 OR a.visibility='participants') ORDER BY a.created_at DESC,a.id`,
		"submissions": `SELECT s.*,s.identity_snapshot->>'actor_name' author_name,
 (SELECT jsonb_build_object('title',m.title,'description',m.description,'evidence',m.evidence,'requirement',m.requirement,'affected_location',m.affected_location,'remediation_advice',m.remediation_advice) FROM remediation_material_versions m WHERE m.task_id=s.task_id AND m.revision=s.material_revision) material,
COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'original_name',a.original_name)) FROM remediation_submission_attachments l JOIN remediation_attachments a ON a.id=l.attachment_id WHERE l.submission_id=s.id AND ($2 OR a.visibility='participants')),'[]'::jsonb) attachments,
 COALESCE((SELECT jsonb_agg(jsonb_build_object('stage',r.stage,'decision',r.decision,'public_feedback',r.public_feedback,'internal_note',CASE WHEN $2 THEN r.internal_note ELSE '' END,'reviewed_at',r.reviewed_at,'verified_environment',r.verified_environment,'verified_at',r.verified_at,'reviewer_name',r.identity_snapshot->>'actor_name')) FROM remediation_reviews r WHERE r.submission_id=s.id),'[]'::jsonb) reviews
 FROM remediation_submissions s JOIN users u ON u.id=s.author_id WHERE s.task_id=$1::uuid ORDER BY s.revision DESC`,
		"requests": `SELECT r.id,r.type,r.status,r.reason,r.mitigation,r.proposed_due_at,r.decision,r.created_at,r.decided_at FROM remediation_requests r WHERE r.task_id=$1::uuid AND ($2 OR true) ORDER BY r.created_at DESC,r.id`,
	}
	sql, ok := queries[kind]
	if !ok {
		return pagination.Result[Object]{}, ErrInvalid
	}
	// Recheck authorization inside the data statement, including download/list races.
	sql = `SELECT * FROM (` + sql + `) visible WHERE remediation_can_read($3::uuid,$1::uuid)`
	return pageObjects(ctx, s.pool, sql, page, id, isInternal, p.UserID)
}
func (s *Service) AssignableFindings(ctx context.Context, p auth.Principal, orderID string, page pagination.Query) (pagination.Result[Object], error) {
	if !validID(orderID) {
		return pagination.Result[Object]{}, ErrInvalid
	}
	var org string
	if err := s.pool.QueryRow(ctx, `SELECT assignee_organization_id::text FROM security_work_orders WHERE id=$1::uuid`, orderID).Scan(&org); err != nil {
		return pagination.Result[Object]{}, dbError(err)
	}
	if err := allowed(ctx, s.pool, p, org, "remediation.assign"); err != nil {
		return pagination.Result[Object]{}, err
	}
	return pageObjects(ctx, s.pool, `SELECT f.id,f.title,f.severity,f.description,COALESCE(f.evidence,'') evidence,COALESCE(f.remediation_advice,'') remediation_advice,
 COALESCE(f.business_system_id,(SELECT sa.business_system_id FROM system_assets sa WHERE sa.asset_id=f.asset_id AND sa.is_primary ORDER BY sa.business_system_id LIMIT 1)) business_system_id,COALESCE(a.asset_value,'') affected_location,w.remediation_requirement requirement,w.due_at,
 EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=w.id AND t.finding_id=f.id AND t.status<>'cancelled') assigned
 FROM security_work_order_findings l JOIN security_findings f ON f.id=l.finding_id JOIN security_work_orders w ON w.id=l.work_order_id LEFT JOIN assets a ON a.id=f.asset_id
 WHERE w.id=$1::uuid AND remediation_can_manage($2::uuid,w.assignee_organization_id,'remediation.assign') ORDER BY f.finding_code`, page, orderID, p.UserID)
}

type ActionInput struct {
	ExpectedVersion      int        `json:"expected_version"`
	ExpectedCompletion   *time.Time `json:"expected_completion_at"`
	BlockedDependency    string     `json:"blocked_dependency"`
	BlockedUntil         *time.Time `json:"blocked_until"`
	VerifiedEnvironment  string     `json:"verified_environment"`
	VerifiedAt           *time.Time `json:"verified_at"`
	NoDeploymentReason   string     `json:"no_deployment_reason"`
	InvalidateSubmission bool       `json:"invalidate_submission"`
	TargetEnvironment    string     `json:"target_environment"`
	DeploymentRequired   *bool      `json:"deployment_required"`
	Reason               string     `json:"reason"`
	Progress             string     `json:"progress"`
	BlockedReason        string     `json:"blocked_reason"`
	GrantID              string     `json:"grant_id"`
	Decision             string     `json:"decision"`
	InternalNote         string     `json:"internal_note"`
	SubmissionID         string     `json:"submission_id"`
	Summary              string     `json:"remediation_summary"`
	DeploymentStatus     string     `json:"deployment_status"`
	ChangeReference      string     `json:"change_reference"`
	DeployedAt           *time.Time `json:"deployed_at"`
	SelfTestResult       string     `json:"self_test_result"`
	AttachmentIDs        []string   `json:"attachment_ids"`
	Visibility           string     `json:"visibility"`
	Material             Material   `json:"material"`
}

func (s *Service) Act(ctx context.Context, p auth.Principal, id, action, key string, in ActionInput, executor bool) (Object, error) {
	if action == "source-update" {
		return s.UpdateSource(ctx, p, id, in, executor)
	}
	if !textOK(in.BlockedDependency, 2000, false) || !textOK(in.VerifiedEnvironment, 255, false) || !textOK(in.NoDeploymentReason, 2000, false) {
		return nil, ErrInvalid
	}
	if !textOK(in.Reason, 20000, false) || !textOK(in.InternalNote, 20000, false) || !textOK(in.Progress, 5000, false) || !textOK(in.BlockedReason, 2000, false) {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	t, err := lockTask(ctx, tx, p, id, executor)
	if err != nil {
		return nil, err
	}
	var fingerprint string
	if action == "submit" {
		var previous Object
		previous, fingerprint, err = idempotent(ctx, tx, p, "submit:"+id, key, in)
		if err != nil {
			return nil, err
		}
		if previous != nil {
			return previous, nil
		}
	}
	if t.MaterialNeedsReview && contains([]string{"submit", "organization-review", "security-review"}, action) {
		return nil, ErrConflict
	}
	if t.Version != in.ExpectedVersion {
		return nil, ErrConflict
	}
	next := t.Status
	details := Object{"reason": in.Reason}
	visibility := "participants"
	requireExecutor := func() error { return authorizeTask(ctx, tx, p, t, true) }
	requireManager := func(permission string) error {
		if executor {
			return ErrForbidden
		}
		return allowed(ctx, tx, p, t.OrganizationID, permission)
	}
	switch action {
	case "accept":
		if err = requireExecutor(); err != nil {
			return nil, err
		}
		if t.Status != "pending_acceptance" {
			return nil, ErrConflict
		}
		if in.ExpectedCompletion == nil || !in.ExpectedCompletion.After(time.Now()) {
			return nil, ErrInvalid
		}
		next = "in_progress"
		_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET accepted_at=now(),expected_completion_at=$2 WHERE id=$1::uuid`, id, in.ExpectedCompletion)
	case "progress":
		if err = requireExecutor(); err != nil {
			return nil, err
		}
		if t.Status != "in_progress" || !textOK(in.Progress, 5000, true) {
			return nil, ErrConflict
		}
		if in.ExpectedCompletion == nil || !in.ExpectedCompletion.After(time.Now()) {
			return nil, ErrInvalid
		}
		if strings.TrimSpace(in.BlockedReason) != "" && (!textOK(in.BlockedDependency, 2000, true) || in.BlockedUntil == nil || !in.BlockedUntil.After(time.Now())) {
			return nil, ErrInvalid
		}
		if strings.TrimSpace(in.BlockedReason) == "" {
			in.BlockedDependency = ""
			in.BlockedUntil = nil
		}
		_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET progress=$2,blocked_reason=$3,expected_completion_at=$4,blocked_dependency=$5,blocked_until=$6 WHERE id=$1::uuid`, id, in.Progress, in.BlockedReason, in.ExpectedCompletion, in.BlockedDependency, in.BlockedUntil)
		details["progress"] = in.Progress
		details["blocked_reason"] = in.BlockedReason
	case "comment":
		if t.Status == "completed" || t.Status == "cancelled" || !textOK(in.Reason, 10000, true) {
			return nil, ErrConflict
		}
		if in.Visibility != "participants" && in.Visibility != "internal" {
			return nil, ErrInvalid
		}
		if in.Visibility == "internal" && (executor || !internal(ctx, tx, p, t)) {
			return nil, ErrForbidden
		}
		visibility = in.Visibility
		_, err = tx.Exec(ctx, `INSERT INTO remediation_comments(task_id,author_id,visibility,body) VALUES($1::uuid,$2::uuid,$3,$4)`, id, p.UserID, visibility, in.Reason)
		details = Object{}
	case "submit":
		if err = requireExecutor(); err != nil {
			return nil, err
		}
		if t.Status != "in_progress" {
			return nil, ErrConflict
		}
		if !textOK(in.Summary, 20000, true) || !textOK(in.SelfTestResult, 20000, true) || !textOK(in.ChangeReference, 255, false) || len(in.AttachmentIDs) > 30 {
			return nil, ErrInvalid
		}
		if !contains([]string{"code_only", "test", "target", "not_applicable"}, in.DeploymentStatus) {
			return nil, ErrInvalid
		}
		if t.DeploymentRequired && (in.DeploymentStatus != "target" || strings.TrimSpace(in.ChangeReference) == "") {
			return nil, ErrInvalid
		}
		if (in.DeploymentStatus == "target" || in.DeploymentStatus == "test") && (in.DeployedAt == nil || in.DeployedAt.After(time.Now().Add(time.Minute))) {
			return nil, ErrInvalid
		}
		if in.DeploymentStatus == "not_applicable" && !textOK(in.NoDeploymentReason, 2000, true) {
			return nil, ErrInvalid
		}
		var submission string
		err = tx.QueryRow(ctx, `INSERT INTO remediation_submissions(task_id,revision,material_revision,author_id,remediation_summary,deployment_status,target_environment,change_reference,deployed_at,self_test_result,no_deployment_reason)
  VALUES($1::uuid,(SELECT COALESCE(max(revision),0)+1 FROM remediation_submissions WHERE task_id=$1::uuid),$2,$3::uuid,$4,$5,$6,$7,$8,$9,$10) RETURNING id::text`, id, t.MaterialRevision, p.UserID, in.Summary, in.DeploymentStatus, t.TargetEnvironment, in.ChangeReference, in.DeployedAt, in.SelfTestResult, in.NoDeploymentReason).Scan(&submission)
		if err != nil {
			return nil, err
		}
		for _, attachment := range in.AttachmentIDs {
			if !validID(attachment) {
				return nil, ErrInvalid
			}
			tag, e := tx.Exec(ctx, `INSERT INTO remediation_submission_attachments(submission_id,attachment_id) SELECT $1::uuid,id FROM remediation_attachments WHERE id=$2::uuid AND task_id=$3::uuid AND visibility='participants' ON CONFLICT DO NOTHING`, submission, attachment, id)
			if e != nil {
				return nil, e
			}
			if tag.RowsAffected() != 1 {
				return nil, ErrInvalid
			}
		}
		_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET current_submission_id=$2::uuid,return_reason='' WHERE id=$1::uuid`, id, submission)
		details["submission_id"] = submission
		next = "pending_org_review"
	case "withdraw":
		if err = requireExecutor(); err != nil {
			return nil, err
		}
		if t.Status != "pending_org_review" || in.SubmissionID != t.SubmissionID || strings.TrimSpace(in.Reason) == "" {
			return nil, ErrConflict
		}
		_, err = tx.Exec(ctx, `UPDATE remediation_submissions SET lifecycle_status='withdrawn' WHERE id=$1::uuid`, t.SubmissionID)
		next = "in_progress"
	case "organization-review", "security-review":
		expected := "pending_org_review"
		stage := "organization"
		if action == "organization-review" {
			err = requireManager("remediation.review")
		} else {
			expected = "pending_security_review"
			stage = "security"
			if executor || !security(ctx, tx, p) {
				return nil, ErrForbidden
			}
		}
		if err != nil {
			return nil, err
		}
		var current bool
		if err = tx.QueryRow(ctx, `SELECT remediation_task_current($1::uuid)`, t.ID).Scan(&current); err != nil {
			return nil, err
		}
		if in.Decision == "pass" && !current {
			return nil, ErrConflict
		}
		if t.Status != expected || t.SubmissionID != in.SubmissionID || !textOK(in.Reason, 20000, true) || !contains([]string{"pass", "return"}, in.Decision) {
			return nil, ErrConflict
		}
		if stage == "security" && (!textOK(in.VerifiedEnvironment, 255, true) || in.VerifiedAt == nil || in.VerifiedAt.After(time.Now())) {
			return nil, ErrInvalid
		}
		if stage == "security" {
			var submitted time.Time
			if err = tx.QueryRow(ctx, `SELECT submitted_at FROM remediation_submissions WHERE id=$1::uuid`, t.SubmissionID).Scan(&submitted); err != nil {
				return nil, err
			}
			if in.VerifiedAt.Before(submitted) {
				return nil, ErrInvalid
			}
		}
		_, err = tx.Exec(ctx, `INSERT INTO remediation_reviews(submission_id,stage,decision,public_feedback,internal_note,reviewer_id,verified_environment,verified_at) VALUES($1::uuid,$2,$3,$4,$5,$6::uuid,$7,$8)`, t.SubmissionID, stage, in.Decision, in.Reason, in.InternalNote, p.UserID, in.VerifiedEnvironment, in.VerifiedAt)
		if err != nil {
			return nil, dbError(err)
		}
		details["decision"] = in.Decision
		details["submission_id"] = t.SubmissionID
		if in.Decision == "return" {
			next = "in_progress"
			if _, err = tx.Exec(ctx, `UPDATE remediation_submissions SET lifecycle_status='returned' WHERE id=$1::uuid`, t.SubmissionID); err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET return_reason=$2 WHERE id=$1::uuid`, id, in.Reason)
		} else if stage == "organization" {
			next = "pending_security_review"
		} else {
			next = "completed"
			if _, err = tx.Exec(ctx, `UPDATE remediation_submissions SET lifecycle_status='completed' WHERE id=$1::uuid`, t.SubmissionID); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `UPDATE security_findings SET status='fixed',updated_at=now(),updated_by=$2::uuid WHERE id=$1::uuid AND status IN ('pending','in_progress')`, t.FindingID, p.UserID); err != nil {
				return nil, err
			}
			_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET completion_reason='verified_fixed',completed_at=now(),return_reason='' WHERE id=$1::uuid`, id)
		}
	case "reassign":
		if err = requireManager("remediation.assign"); err != nil {
			return nil, err
		}
		if !contains([]string{"pending_acceptance", "in_progress"}, t.Status) || !validID(in.GrantID) || strings.TrimSpace(in.Reason) == "" {
			return nil, ErrConflict
		}
		var assignee string
		err = tx.QueryRow(ctx, `SELECT user_id::text FROM remediation_grants WHERE id=$1::uuid AND remediation_grant_valid(id,user_id,$2::uuid,$3::uuid) AND valid_until >= $4`, in.GrantID, t.OrganizationID, t.SystemID, t.DueAt).Scan(&assignee)
		if err != nil {
			return nil, dbError(err)
		}
		details["from_user_id"] = t.AssigneeID
		details["to_user_id"] = assignee
		_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET assignee_user_id=$2::uuid,grant_id=$3::uuid,accepted_at=NULL,expected_completion_at=NULL,blocked_reason='',blocked_dependency='',blocked_until=NULL WHERE id=$1::uuid`, id, assignee, in.GrantID)
		next = "pending_acceptance"
	case "cancel":
		if err = requireManager("remediation.assign"); err != nil {
			return nil, err
		}
		if !contains([]string{"pending_acceptance", "in_progress"}, t.Status) || strings.TrimSpace(in.Reason) == "" {
			return nil, ErrConflict
		}
		next = "cancelled"
	case "materials":
		if err = requireManager("remediation.assign"); err != nil {
			return nil, err
		}
		if !contains([]string{"pending_acceptance", "in_progress", "pending_org_review", "pending_security_review"}, t.Status) || !in.Material.valid() || strings.TrimSpace(in.Reason) == "" {
			return nil, ErrConflict
		}
		if contains([]string{"pending_org_review", "pending_security_review"}, t.Status) {
			if !in.InvalidateSubmission {
				return nil, ErrConflict
			}
			if _, err = tx.Exec(ctx, `UPDATE remediation_submissions SET lifecycle_status='returned' WHERE id=$1::uuid`, t.SubmissionID); err != nil {
				return nil, err
			}
			if _, err = tx.Exec(ctx, `UPDATE remediation_tasks SET return_reason=$2 WHERE id=$1::uuid`, id, in.Reason); err != nil {
				return nil, err
			}
			next = "in_progress"
		}
		if in.TargetEnvironment != "" {
			if !textOK(in.TargetEnvironment, 255, true) || in.DeploymentRequired == nil {
				return nil, ErrInvalid
			}
			if _, err = tx.Exec(ctx, `UPDATE remediation_tasks SET target_environment=$2,deployment_required=$3 WHERE id=$1::uuid`, id, in.TargetEnvironment, *in.DeploymentRequired); err != nil {
				return nil, err
			}
		}
		var severity string
		if err = tx.QueryRow(ctx, `SELECT severity FROM security_findings WHERE id=$1::uuid`, t.FindingID).Scan(&severity); err != nil {
			return nil, err
		}
		if severity != in.Material.Severity {
			return nil, ErrInvalid
		}
		if err = writeMaterial(ctx, tx, p, id, t.MaterialRevision+1, in.Material); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET material_revision=material_revision+1,material_needs_review=false WHERE id=$1::uuid`, id)
	case "reopen-risk":
		if executor || !security(ctx, tx, p) {
			return nil, ErrForbidden
		}
		if t.Status != "completed" || !textOK(in.Reason, 2000, true) {
			return nil, ErrConflict
		}
		var reason string
		if err = tx.QueryRow(ctx, `SELECT completion_reason FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&reason); err != nil {
			return nil, err
		}
		if reason != "risk_accepted" {
			return nil, ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE security_findings SET status='in_progress',risk_accepted_at=NULL,risk_accepted_until=NULL,risk_acceptance_reason=NULL,risk_accepted_by=NULL,risk_accepted_from_status=NULL,updated_at=now(),updated_by=$2::uuid WHERE id=$1::uuid`, t.FindingID, p.UserID); err != nil {
			return nil, err
		}
		_, err = tx.Exec(ctx, `UPDATE remediation_tasks SET completion_reason=NULL,completed_at=NULL,current_submission_id=NULL,return_reason=$2 WHERE id=$1::uuid`, id, in.Reason)
		next = "in_progress"
	default:
		return nil, ErrInvalid
	}
	if err != nil {
		return nil, dbError(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE remediation_tasks SET status=$2,version=version+1,updated_at=now() WHERE id=$1::uuid`, id, next); err != nil {
		return nil, err
	}
	details["from_status"] = t.Status
	details["to_status"] = next
	if err = event(ctx, tx, p, t, action, visibility, details); err != nil {
		return nil, err
	}
	if err = refreshOrder(ctx, tx, t.WorkOrderID); err != nil {
		return nil, err
	}
	out := Object{"id": id, "status": next, "version": t.Version + 1}
	if action == "submit" {
		if err = saveResult(ctx, tx, p, "submit:"+id, key, fingerprint, out); err != nil {
			return nil, err
		}
	}
	return out, tx.Commit(ctx)
}
func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
func refreshOrder(ctx context.Context, tx pgx.Tx, id string) error {
	_, err := tx.Exec(ctx, `UPDATE security_work_orders w SET status=CASE WHEN NOT EXISTS(
 SELECT 1 FROM security_work_order_findings l WHERE l.work_order_id=w.id AND NOT EXISTS(
 SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=w.id AND t.finding_id=l.finding_id AND t.status IN ('completed','pending_security_review') AND NOT t.material_needs_review AND (t.status='completed' OR remediation_grant_valid(t.grant_id,t.assignee_user_id,t.organization_id,t.business_system_id))))
 THEN 'pending_verification' ELSE 'in_progress' END,updated_at=now() WHERE w.id=$1::uuid AND w.execution_mode='individual' AND w.status NOT IN ('draft','issued','closed','cancelled')`, id)
	return err
}
func (s *Service) OrderSummary(ctx context.Context, p auth.Principal, id string) (Object, error) {
	if !validID(id) {
		return nil, ErrInvalid
	}
	out, err := readObject(ctx, s.pool, `SELECT w.id,w.work_order_no,w.execution_mode,w.status,w.due_at,w.assignee_organization_id,
 (SELECT count(*) FROM security_work_order_findings l WHERE l.work_order_id=w.id) total,
 (SELECT count(*) FROM security_work_order_findings l WHERE l.work_order_id=w.id AND NOT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=w.id AND t.finding_id=l.finding_id AND t.status<>'cancelled')) unassigned,
 COALESCE((SELECT jsonb_object_agg(status,n) FROM (SELECT status,count(*) n FROM remediation_tasks WHERE work_order_id=w.id AND status<>'cancelled' GROUP BY status) counts),'{}'::jsonb) counts
 FROM security_work_orders w WHERE w.id=$1::uuid AND (remediation_is_security($2::uuid) OR remediation_can_manage($2::uuid,w.assignee_organization_id,'remediation.assign') OR remediation_can_manage($2::uuid,w.assignee_organization_id,'remediation.review'))`, id, p.UserID)
	return out, err
}
func (s *Service) Options(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[Object], error) {
	return pageObjects(ctx, s.pool, `SELECT b.id,b.system_name,b.owner_organization_id organization_id,o.organization_name FROM business_systems b JOIN organizations o ON o.id=b.owner_organization_id
 WHERE b.deleted_at IS NULL AND b.system_status<>'archived' AND (remediation_can_manage($1::uuid,o.id,'remediation.people.manage') OR remediation_can_manage($1::uuid,o.id,'remediation.assign') OR remediation_is_security($1::uuid)) ORDER BY o.organization_name,b.system_name`, page, p.UserID)
}

// CloseOrder is the only closing path for individual execution mode.
func (s *Service) CloseOrder(ctx context.Context, p auth.Principal, id, reason string) error {
	if !validID(id) || !textOK(reason, 10000, true) {
		return ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if !security(ctx, tx, p) {
		return ErrForbidden
	}
	var status, mode, org string
	if err = tx.QueryRow(ctx, `SELECT status,execution_mode,assignee_organization_id::text FROM security_work_orders WHERE id=$1::uuid FOR UPDATE`, id).Scan(&status, &mode, &org); err != nil {
		return dbError(err)
	}
	if mode != "individual" || !contains([]string{"in_progress", "pending_verification"}, status) {
		return ErrConflict
	}
	var unfinished bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM security_work_order_findings l JOIN security_findings f ON f.id=l.finding_id WHERE l.work_order_id=$1::uuid AND
 (NOT (f.status IN ('fixed','false_positive') OR (f.status='risk_accepted' AND f.risk_accepted_until>now()))
 OR NOT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=l.work_order_id AND t.finding_id=l.finding_id AND t.status='completed')))
 OR EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=$1::uuid AND t.status NOT IN ('completed','cancelled'))`, id).Scan(&unfinished)
	if err != nil {
		return err
	}
	if unfinished {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE security_work_orders SET status='closed',closed_at=now(),verified_at=now(),verification_result=$2,updated_at=now(),updated_by=$3::uuid WHERE id=$1::uuid`, id, reason, p.UserID); err != nil {
		return err
	}
	if err = audit(ctx, tx, p, org, id, "order.close", Object{"before": Object{"status": status}, "after": Object{"status": "closed"}, "reason": reason}); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO security_work_order_events(work_order_id,actor_user_id,action,from_status,to_status,comment) VALUES($1::uuid,$2::uuid,'close',$3,'closed',$4)`, id, p.UserID, status, reason)
	if err != nil {
		return fmt.Errorf("record close: %w", err)
	}
	return tx.Commit(ctx)
}

// Revocation must also remove the parent order from the ready-for-verification queue.
func refreshInvalidOrders(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `UPDATE security_work_orders w SET status='in_progress',updated_at=now()
 WHERE w.execution_mode='individual' AND w.status='pending_verification' AND EXISTS(
 SELECT 1 FROM remediation_tasks t WHERE t.work_order_id=w.id AND t.status='pending_security_review'
 AND (t.material_needs_review OR NOT remediation_grant_valid(t.grant_id,t.assignee_user_id,t.organization_id,t.business_system_id)))`)
	return err
}
