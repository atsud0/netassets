// Package remediation implements personal repair assignments without granting
// executors access to organization-wide inventories or security findings.
package remediation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrInvalid      = errors.New("invalid remediation input")
	ErrForbidden    = errors.New("remediation permission denied")
	ErrNotFound     = errors.New("remediation resource not found")
	ErrConflict     = errors.New("remediation state or version conflict")
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{2,63}$`)
)

type requestContextKey struct{}
type RequestInfo struct{ UserID, RequestID, IP, Method, Route string }

func WithRequest(ctx context.Context, info RequestInfo) context.Context {
	return context.WithValue(ctx, requestContextKey{}, info)
}
func requestInfo(ctx context.Context) RequestInfo {
	info, _ := ctx.Value(requestContextKey{}).(RequestInfo)
	return info
}

type Object map[string]any
type Service struct {
	pool       *pgxpool.Pool
	storageDir string
}

func NewService(pool *pgxpool.Pool, storageDir string) *Service { return &Service{pool, storageDir} }

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func validID(id string) bool { return uuidPattern.MatchString(id) }
func token() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func digest(value string) string { h := sha256.Sum256([]byte(value)); return hex.EncodeToString(h[:]) }
func dbError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			return ErrConflict
		case "23503", "23514", "22P02", "22001":
			return ErrInvalid
		}
	}
	return err
}
func readObject(ctx context.Context, q queryer, sql string, args ...any) (Object, error) {
	var data []byte
	if err := q.QueryRow(ctx, `SELECT row_to_json(record) FROM (`+sql+`) record`, args...).Scan(&data); err != nil {
		return nil, dbError(err)
	}
	var out Object
	err := json.Unmarshal(data, &out)
	return out, err
}
func readList(ctx context.Context, q queryer, sql string, args ...any) ([]Object, error) {
	rows, err := q.Query(ctx, `SELECT row_to_json(record) FROM (`+sql+`) record`, args...)
	if err != nil {
		return nil, dbError(err)
	}
	defer rows.Close()
	out := []Object{}
	for rows.Next() {
		var b []byte
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		var item Object
		if err := json.Unmarshal(b, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func pageObjects(ctx context.Context, q queryer, sql string, page pagination.Query, args ...any) (pagination.Result[Object], error) {
	var total int
	if err := q.QueryRow(ctx, `SELECT count(*) FROM (`+sql+`) records`, args...).Scan(&total); err != nil {
		return pagination.Result[Object]{}, dbError(err)
	}
	items, err := readList(ctx, q, sql+fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2), append(args, page.PageSize, page.Offset())...)
	return pagination.NewResult(items, total, page), err
}
func allowed(ctx context.Context, q queryer, p auth.Principal, org, permission string) error {
	if !validID(org) {
		return ErrInvalid
	}
	var ok bool
	err := q.QueryRow(ctx, `SELECT remediation_can_manage($1::uuid,$2::uuid,$3)`, p.UserID, org, permission).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	return nil
}
func security(ctx context.Context, q queryer, p auth.Principal) bool {
	var ok bool
	_ = q.QueryRow(ctx, `SELECT remediation_is_security($1::uuid)`, p.UserID).Scan(&ok)
	return ok
}

// Mutations serialize changes to assignments, grants, reviews and cancellation.
// Work-order integration takes the same lock before its row locks.
func (s *Service) begin(ctx context.Context) (pgx.Tx, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if _, err = tx.Exec(ctx, `SET LOCAL netassets.remediation_write = 'on'`); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func audit(ctx context.Context, tx pgx.Tx, p auth.Principal, org, id, action string, details any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_id,request_ip)
 VALUES($1::uuid,NULLIF($2,'')::uuid,$3::text,'remediation',$4,$3::text,$5::jsonb,$6,NULLIF($7,'')::inet)`, p.UserID, org, "remediation."+action, id, b, requestInfo(ctx).RequestID, requestInfo(ctx).IP)
	return err
}
func event(ctx context.Context, tx pgx.Tx, p auth.Principal, t Task, action, visibility string, details any) error {
	b, err := json.Marshal(details)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO remediation_task_events(task_id,actor_id,action,visibility,details) VALUES($1::uuid,$2::uuid,$3,$4,$5::jsonb)`, t.ID, p.UserID, action, visibility, b); err != nil {
		return err
	}
	after, err := loadTask(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	return audit(ctx, tx, p, t.OrganizationID, t.ID, action, Object{"before": t, "after": after, "change": details})
}
func idempotent(ctx context.Context, tx pgx.Tx, p auth.Principal, operation, key string, input any) (Object, string, error) {
	if len(key) < 8 || len(key) > 128 {
		return nil, "", ErrInvalid
	}
	b, err := json.Marshal(input)
	if err != nil {
		return nil, "", err
	}
	fingerprint := digest(string(b))
	var previous string
	var result []byte
	err = tx.QueryRow(ctx, `SELECT fingerprint,result FROM remediation_idempotency WHERE user_id=$1::uuid AND operation=$2 AND key=$3`, p.UserID, operation, key).Scan(&previous, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fingerprint, nil
	}
	if err != nil {
		return nil, "", err
	}
	if previous != fingerprint {
		return nil, "", ErrConflict
	}
	var out Object
	err = json.Unmarshal(result, &out)
	return out, fingerprint, err
}
func saveResult(ctx context.Context, tx pgx.Tx, p auth.Principal, operation, key, fingerprint string, result Object) error {
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO remediation_idempotency(user_id,operation,key,fingerprint,result) VALUES($1::uuid,$2,$3,$4,$5::jsonb)`, p.UserID, operation, key, fingerprint, b)
	return err
}
func textOK(value string, max int, required bool) bool {
	return len(value) <= max && (!required || strings.TrimSpace(value) != "")
}

type Task struct {
	ID                  string          `json:"id"`
	WorkOrderID         string          `json:"work_order_id"`
	FindingID           string          `json:"finding_id"`
	OrganizationID      string          `json:"organization_id"`
	SystemID            string          `json:"business_system_id"`
	AssigneeID          string          `json:"assignee_user_id"`
	GrantID             string          `json:"grant_id"`
	Status              string          `json:"status"`
	Version             int             `json:"version"`
	MaterialRevision    int             `json:"material_revision"`
	SubmissionID        string          `json:"current_submission_id"`
	DueAt               time.Time       `json:"due_at"`
	TargetEnvironment   string          `json:"target_environment"`
	DeploymentRequired  bool            `json:"deployment_required"`
	ExpectedCompletion  *time.Time      `json:"expected_completion_at"`
	BlockedDependency   string          `json:"blocked_dependency"`
	BlockedUntil        *time.Time      `json:"blocked_until"`
	MaterialNeedsReview bool            `json:"material_needs_review"`
	SourceRevision      int             `json:"source_revision"`
	AssigneeSnapshot    json.RawMessage `json:"assignee_snapshot"`
	// AssetRetiredAt records that the task's target asset was soft deleted while
	// the task was still open. The task keeps its delivery history but stops being
	// current work for the assignee until the asset is restored or the task is
	// reassigned. SystemRetiredAt is the same contract on the business system
	// dimension; both are explicit because remediation_task_current() silently
	// reports false for either, which is not an explanation anyone can act on.
	AssetRetiredAt  *time.Time `json:"asset_retired_at,omitempty"`
	SystemRetiredAt *time.Time `json:"system_retired_at,omitempty"`
}

func loadTask(ctx context.Context, q queryer, id string) (Task, error) {
	var t Task
	err := q.QueryRow(ctx, `SELECT id::text,work_order_id::text,finding_id::text,organization_id::text,business_system_id::text,
 assignee_user_id::text,grant_id::text,status,version,material_revision,COALESCE(current_submission_id::text,''),due_at,target_environment,deployment_required,expected_completion_at,blocked_dependency,blocked_until,material_needs_review,source_revision,assignee_snapshot,asset_retired_at,system_retired_at
 FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&t.ID, &t.WorkOrderID, &t.FindingID, &t.OrganizationID, &t.SystemID, &t.AssigneeID, &t.GrantID, &t.Status, &t.Version, &t.MaterialRevision, &t.SubmissionID, &t.DueAt, &t.TargetEnvironment, &t.DeploymentRequired, &t.ExpectedCompletion, &t.BlockedDependency, &t.BlockedUntil, &t.MaterialNeedsReview, &t.SourceRevision, &t.AssigneeSnapshot, &t.AssetRetiredAt, &t.SystemRetiredAt)
	return t, dbError(err)
}

// executionBlock asks the single executability predicate why an assignee action
// cannot proceed. It returns the machine reason (asset_retired,
// authorization_expired, ...) so the audit trail explains the refusal instead of
// leaving a bare conflict. A closed task is not blocked here: the state machine
// reports that with a more precise error.
func executionBlock(ctx context.Context, q queryer, p auth.Principal, t Task) (string, bool, error) {
	var canExecute bool
	var reason *string
	if err := q.QueryRow(ctx, `SELECT can_execute, reason FROM remediation_task_execution_state($1::uuid,$2::uuid)`, p.UserID, t.ID).Scan(&canExecute, &reason); err != nil {
		return "", false, dbError(err)
	}
	if canExecute || reason == nil {
		return "", false, nil
	}
	return *reason, true, nil
}

func authorizeTask(ctx context.Context, q queryer, p auth.Principal, t Task, executor bool) error {
	var ok bool
	sql := `SELECT remediation_can_read($1::uuid,$2::uuid)`
	if executor {
		// Executor authorization is a read gate, not an executability gate: a
		// retired task's assignee must still open its history. Whether the task
		// can actually be progressed is decided by one place,
		// remediation_task_execution_state, via requireExecutable below.
		sql = `SELECT assignee_user_id=$1::uuid AND ((remediation_grant_valid(grant_id,$1::uuid,organization_id,business_system_id) AND remediation_task_current(id)) OR asset_retired_at IS NOT NULL OR system_retired_at IS NOT NULL) FROM remediation_tasks WHERE id=$2::uuid`
	}
	if err := q.QueryRow(ctx, sql, p.UserID, t.ID).Scan(&ok); err != nil {
		return dbError(err)
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}
func internal(ctx context.Context, q queryer, p auth.Principal, t Task) bool {
	return security(ctx, q, p) || allowed(ctx, q, p, t.OrganizationID, "remediation.assign") == nil || allowed(ctx, q, p, t.OrganizationID, "remediation.review") == nil
}
func lockTask(ctx context.Context, tx pgx.Tx, p auth.Principal, id string, executor bool) (Task, error) {
	if !validID(id) {
		return Task{}, ErrNotFound
	}
	var workOrderID string
	if err := tx.QueryRow(ctx, `SELECT work_order_id::text FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&workOrderID); err != nil {
		return Task{}, dbError(err)
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT status FROM security_work_orders WHERE id=$1::uuid FOR UPDATE`, workOrderID).Scan(&state); err != nil {
		return Task{}, dbError(err)
	}
	if state == "closed" || state == "cancelled" {
		return Task{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM remediation_tasks WHERE id=$1::uuid FOR UPDATE`, id); err != nil {
		return Task{}, dbError(err)
	}
	t, err := loadTask(ctx, tx, id)
	if err != nil {
		return t, err
	}
	if err = authorizeTask(ctx, tx, p, t, executor); err != nil {
		return t, err
	}
	return t, nil
}

// Denied requests contain route templates, not sensitive query strings or bodies.
func (s *Service) RecordDenial(ctx context.Context, code string) error {
	i := requestInfo(ctx)
	if !validID(i.UserID) {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_logs(user_id,action,resource_type,summary,details,request_id,request_ip,result) VALUES($1::uuid,'remediation.access_denied','remediation','修复接口访问被拒绝',jsonb_build_object('method',$2::text,'route',$3::text,'code',$4::text),$5,NULLIF($6,'')::inet,'failure')`, i.UserID, i.Method, i.Route, code, i.RequestID, i.IP)
	return err
}
func groupAdmin(ctx context.Context, q queryer, p auth.Principal) bool {
	var ok bool
	_ = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1::uuid AND u.status='enabled' AND u.deleted_at IS NULL AND r.role_key='group_admin' AND r.status='enabled')`, p.UserID).Scan(&ok)
	return ok
}
