package securitytestrequest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/notification"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid input")
	ErrState     = errors.New("invalid state")
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const maxScopeAssets = 200

// Request is a subsidiary launch security-test request. Scope assets are
// frozen at submit time and carried into the converted test batch.
type Request struct {
	ID                        string       `json:"id"`
	Code                      string       `json:"code"`
	RequestName               string       `json:"request_name"`
	ApplicantOrganizationID   string       `json:"applicant_organization_id"`
	ApplicantOrganizationCode string       `json:"applicant_organization_code"`
	ApplicantOrganizationName string       `json:"applicant_organization_name"`
	BusinessSystemID          string       `json:"business_system_id,omitempty"`
	BusinessSystemDraftID     string       `json:"business_system_draft_id,omitempty"`
	BusinessSystemCode        string       `json:"business_system_code,omitempty"`
	BusinessSystemName        string       `json:"business_system_name,omitempty"`
	TestType                  string       `json:"test_type"`
	ExpectedLaunchAt          time.Time    `json:"expected_launch_at"`
	TestWindowStartAt         *time.Time   `json:"test_window_start_at,omitempty"`
	TestWindowEndAt           *time.Time   `json:"test_window_end_at,omitempty"`
	ContactUserID             string       `json:"contact_user_id,omitempty"`
	ContactName               string       `json:"contact_name,omitempty"`
	Background                string       `json:"background"`
	Status                    string       `json:"status"`
	ScopeAssetCount           int          `json:"scope_asset_count"`
	ReviewedBy                string       `json:"reviewed_by,omitempty"`
	ReviewedByName            string       `json:"reviewed_by_name,omitempty"`
	ReviewedAt                *time.Time   `json:"reviewed_at,omitempty"`
	ReviewComment             string       `json:"review_comment,omitempty"`
	ConvertedBatchID          string       `json:"converted_batch_id,omitempty"`
	ConvertedBatchCode        string       `json:"converted_batch_code,omitempty"`
	ConvertedBatchStatus      string       `json:"converted_batch_status,omitempty"`
	LaunchDecidedBy           string       `json:"launch_decided_by,omitempty"`
	LaunchDecidedByName       string       `json:"launch_decided_by_name,omitempty"`
	LaunchDecidedAt           *time.Time   `json:"launch_decided_at,omitempty"`
	LaunchComment             string       `json:"launch_comment,omitempty"`
	ReportedTaskID            string       `json:"reported_task_id,omitempty"`
	HasUnreportedScope        bool         `json:"has_unreported_scope"`
	CreatedByName             string       `json:"created_by_name,omitempty"`
	CreatedAt                 time.Time    `json:"created_at"`
	UpdatedAt                 time.Time    `json:"updated_at"`
	ScopeAssets               []ScopeAsset `json:"scope_assets,omitempty"`
	OpenFindingCount          int          `json:"open_finding_count"`
}

// ScopeAsset is one frozen request scope row. Exactly one of PublishedAssetID
// and WorkingAssetID references the underlying ledger resource.
type ScopeAsset struct {
	ID                 string    `json:"id"`
	PublishedAssetID   string    `json:"published_asset_id,omitempty"`
	WorkingAssetID     string    `json:"working_asset_id,omitempty"`
	AssetType          string    `json:"asset_type"`
	AssetValue         string    `json:"asset_value"`
	AssetName          string    `json:"asset_name,omitempty"`
	OrganizationID     string    `json:"organization_id,omitempty"`
	OrganizationCode   string    `json:"organization_code"`
	OrganizationName   string    `json:"organization_name"`
	BusinessSystemID   string    `json:"business_system_id,omitempty"`
	BusinessSystemCode string    `json:"business_system_code,omitempty"`
	BusinessSystemName string    `json:"business_system_name,omitempty"`
	NetworkScope       string    `json:"network_scope"`
	NetworkAreaName    string    `json:"network_area_name,omitempty"`
	Environment        string    `json:"environment"`
	Importance         string    `json:"importance"`
	ReportAction       string    `json:"report_action"`
	SnapshotAt         time.Time `json:"snapshot_at"`
}

type RequestEvent struct {
	ID         int64          `json:"id"`
	Action     string         `json:"action"`
	FromStatus string         `json:"from_status,omitempty"`
	ToStatus   string         `json:"to_status,omitempty"`
	Comment    string         `json:"comment,omitempty"`
	Details    map[string]any `json:"details"`
	ActorName  string         `json:"actor_name,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

type Input struct {
	RequestName           string     `json:"request_name"`
	BusinessSystemID      string     `json:"business_system_id"`
	BusinessSystemDraftID string     `json:"business_system_draft_id"`
	TestType              string     `json:"test_type"`
	ExpectedLaunchAt      time.Time  `json:"expected_launch_at"`
	TestWindowStartAt     *time.Time `json:"test_window_start_at"`
	TestWindowEndAt       *time.Time `json:"test_window_end_at"`
	ContactUserID         string     `json:"contact_user_id"`
	Background            string     `json:"background"`
	WorkingAssetIDs       []string   `json:"working_asset_ids"`
	PublishedAssetIDs     []string   `json:"published_asset_ids"`
}

type TransitionInput struct {
	Comment string `json:"comment"`
}

type ApproveInput struct {
	Comment           string     `json:"comment"`
	AuthorizationNote string     `json:"authorization_note"`
	TestWindowStartAt *time.Time `json:"test_window_start_at"`
	TestWindowEndAt   *time.Time `json:"test_window_end_at"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// canManage reports whether the principal may review, approve and close any
// subsidiary request. Group security data stays limited to built-in group roles.
func canManage(principal auth.Principal) bool {
	return principal.CanManageSecurityTests()
}

// applicantOrg returns the organization a subsidiary-side principal may act
// for. Group security roles create test batches directly instead of requests.
func applicantOrg(principal auth.Principal) (string, bool) {
	if principal.HasGroupSecurityRole() || !principal.HasPermission(auth.PermissionSecurityTestRequest) || principal.Organization == nil {
		return "", false
	}
	return principal.Organization.ID, true
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, status, keyword string, query pagination.Query) (pagination.Result[Request], error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant && !canManage(principal) {
		return pagination.Result[Request]{}, ErrForbidden
	}
	status = strings.TrimSpace(status)
	if status != "" && !validStatus(status) {
		return pagination.Result[Request]{}, ErrInvalid
	}
	keyword = strings.TrimSpace(keyword)
	if len(keyword) > 128 {
		return pagination.Result[Request]{}, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, requestColumns+`, count(*) OVER ()`+requestFrom+`
		WHERE ($1::text = '' OR r.status = $1)
		  AND ($2::text = '' OR r.applicant_organization_id = $2::uuid)
		  AND ($3::text = '' OR concat_ws(' ', r.request_code, r.request_name, r.business_system_name_snapshot, o.organization_name) ILIKE '%'||$3||'%')
		ORDER BY r.created_at DESC
		LIMIT $4 OFFSET $5`, status, orgID, keyword, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Request]{}, fmt.Errorf("search security test requests: %w", err)
	}
	defer rows.Close()
	items := make([]Request, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item Request
		if err := rows.Scan(requestScanTargets(&item, &total)...); err != nil {
			return pagination.Result[Request]{}, fmt.Errorf("scan security test request page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Request]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
			SELECT count(*) FROM security_test_requests r
			JOIN organizations o ON o.id=r.applicant_organization_id
			WHERE ($1::text = '' OR r.status = $1)
			  AND ($2::text = '' OR r.applicant_organization_id = $2::uuid)
			  AND ($3::text = '' OR concat_ws(' ', r.request_code, r.request_name, r.business_system_name_snapshot, o.organization_name) ILIKE '%'||$3||'%')`,
			status, orgID, keyword).Scan(&total); err != nil {
			return pagination.Result[Request]{}, fmt.Errorf("count security test requests: %w", err)
		}
	}
	// Completed batches with fully closed findings promote testing requests to
	// pending_launch lazily when reviewers see them, so no separate scheduler
	// is needed. Failures fall through: the row simply stays testing.
	for i := range items {
		if items[i].Status == "testing" {
			if promoted, err := s.promoteIfReady(ctx, principal, &items[i]); err == nil && promoted {
				items[i].Status = "pending_launch"
			}
		}
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, id string) (Request, error) {
	if !validUUID(id) {
		return Request{}, ErrInvalid
	}
	item, err := s.get(ctx, id)
	if err != nil {
		return Request{}, err
	}
	if err := ensureViewable(principal, item); err != nil {
		return Request{}, err
	}
	if item.Status == "testing" {
		if promoted, err := s.promoteIfReady(ctx, principal, &item); err == nil && promoted {
			item.Status = "pending_launch"
		}
	}
	item.ScopeAssets, err = s.loadScope(ctx, id)
	if err != nil {
		return Request{}, err
	}
	return item, nil
}

// SearchEvents returns the newest request workflow events first.
func (s *Service) SearchEvents(ctx context.Context, principal auth.Principal, id string, query pagination.Query) (pagination.Result[RequestEvent], error) {
	if !validUUID(id) {
		return pagination.Result[RequestEvent]{}, ErrInvalid
	}
	item, err := s.get(ctx, id)
	if err != nil {
		return pagination.Result[RequestEvent]{}, err
	}
	if err := ensureViewable(principal, item); err != nil {
		return pagination.Result[RequestEvent]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_test_request_events WHERE request_id=$1::uuid`, id).Scan(&total); err != nil {
		return pagination.Result[RequestEvent]{}, fmt.Errorf("count security test request events: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.action, COALESCE(e.from_status, ''), COALESCE(e.to_status, ''), COALESCE(e.comment, ''),
		       e.details, COALESCE(u.display_name, ''), e.created_at
		FROM security_test_request_events e LEFT JOIN users u ON u.id=e.actor_user_id
		WHERE e.request_id=$1::uuid
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $2 OFFSET $3`, id, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[RequestEvent]{}, fmt.Errorf("list security test request events: %w", err)
	}
	defer rows.Close()
	items := make([]RequestEvent, 0, query.PageSize)
	for rows.Next() {
		var item RequestEvent
		if err := rows.Scan(&item.ID, &item.Action, &item.FromStatus, &item.ToStatus, &item.Comment, &item.Details, &item.ActorName, &item.CreatedAt); err != nil {
			return pagination.Result[RequestEvent]{}, fmt.Errorf("scan security test request event: %w", err)
		}
		items = append(items, item)
	}
	return pagination.NewResult(items, total, query), rows.Err()
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input Input, requestID, ipAddress string) (Request, error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant {
		return Request{}, ErrForbidden
	}
	normalizeInput(&input)
	if !validInput(input) {
		return Request{}, ErrInvalid
	}
	code, err := newCode()
	if err != nil {
		return Request{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin create security test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	system, err := resolveBusinessSystem(ctx, tx, orgID, input.BusinessSystemID, input.BusinessSystemDraftID)
	if err != nil {
		return Request{}, err
	}
	if err := ensureContactUser(ctx, tx, orgID, input.ContactUserID); err != nil {
		return Request{}, err
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO security_test_requests
		(request_code, applicant_organization_id, request_name, business_system_id, business_system_draft_id,
		 business_system_code_snapshot, business_system_name_snapshot, test_type, expected_launch_at,
		 test_window_start_at, test_window_end_at, contact_user_id, background, status, created_by, updated_by)
		VALUES ($1, $2::uuid, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid, $6, $7, $8, $9, $10, $11, $12::uuid, $13, 'draft', $14::uuid, $14::uuid)
		RETURNING id::text`,
		code, orgID, input.RequestName, input.BusinessSystemID, input.BusinessSystemDraftID,
		system.Code, system.Name, input.TestType, input.ExpectedLaunchAt,
		input.TestWindowStartAt, input.TestWindowEndAt, input.ContactUserID, input.Background, principal.UserID).Scan(&id)
	if err != nil {
		return Request{}, fmt.Errorf("create security test request: %w", err)
	}
	if err := replaceScope(ctx, tx, id, orgID, principal.UserID, input.PublishedAssetIDs, input.WorkingAssetIDs); err != nil {
		return Request{}, err
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "create", "", "draft", "", map[string]any{"asset_count": len(input.PublishedAssetIDs) + len(input.WorkingAssetIDs)}); err != nil {
		return Request{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_request.create", id, "创建上线安全测试申请 "+input.RequestName,
		map[string]any{"request_code": code, "asset_count": len(input.PublishedAssetIDs) + len(input.WorkingAssetIDs)}, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, id string, input Input, requestID, ipAddress string) (Request, error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant {
		return Request{}, ErrForbidden
	}
	if !validUUID(id) {
		return Request{}, ErrInvalid
	}
	normalizeInput(&input)
	if !validInput(input) {
		return Request{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin update security test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if item.ApplicantOrganizationID != orgID {
		return Request{}, ErrForbidden
	}
	if item.Status != "draft" && item.Status != "rejected" {
		return Request{}, ErrState
	}
	system, err := resolveBusinessSystem(ctx, tx, orgID, input.BusinessSystemID, input.BusinessSystemDraftID)
	if err != nil {
		return Request{}, err
	}
	if err := ensureContactUser(ctx, tx, orgID, input.ContactUserID); err != nil {
		return Request{}, err
	}
	command, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET
			request_name=$2, business_system_id=NULLIF($3, '')::uuid, business_system_draft_id=NULLIF($4, '')::uuid,
			business_system_code_snapshot=$5, business_system_name_snapshot=$6, test_type=$7, expected_launch_at=$8,
			test_window_start_at=$9, test_window_end_at=$10, contact_user_id=$11::uuid, background=$12,
			updated_by=$13::uuid, updated_at=now()
		WHERE id=$1::uuid AND status IN ('draft', 'rejected')`,
		id, input.RequestName, input.BusinessSystemID, input.BusinessSystemDraftID, system.Code, system.Name,
		input.TestType, input.ExpectedLaunchAt, input.TestWindowStartAt, input.TestWindowEndAt,
		input.ContactUserID, input.Background, principal.UserID)
	if err != nil {
		return Request{}, fmt.Errorf("update security test request: %w", err)
	}
	if command.RowsAffected() == 0 {
		return Request{}, ErrState
	}
	if err := replaceScope(ctx, tx, id, orgID, principal.UserID, input.PublishedAssetIDs, input.WorkingAssetIDs); err != nil {
		return Request{}, err
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "update", item.Status, item.Status, "",
		map[string]any{"asset_count": len(input.PublishedAssetIDs) + len(input.WorkingAssetIDs)}); err != nil {
		return Request{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_request.update", id, "更新上线安全测试申请 "+input.RequestName,
		map[string]any{"asset_count": len(input.PublishedAssetIDs) + len(input.WorkingAssetIDs)}, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request update: %w", err)
	}
	return s.Get(ctx, principal, id)
}

// Submit freezes the scope snapshot against live data, enforces the scope
// completeness rules and hands the request to group security for review.
func (s *Service) Submit(ctx context.Context, principal auth.Principal, id string, requestID, ipAddress string) (Request, error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant {
		return Request{}, ErrForbidden
	}
	if !validUUID(id) {
		return Request{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin submit security test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if item.ApplicantOrganizationID != orgID {
		return Request{}, ErrForbidden
	}
	if item.Status != "draft" {
		return Request{}, ErrState
	}
	publishedIDs, workingIDs, err := storedScopeRefs(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if len(publishedIDs)+len(workingIDs) == 0 {
		return Request{}, ErrInvalid
	}
	if err := replaceScope(ctx, tx, id, orgID, principal.UserID, publishedIDs, workingIDs); err != nil {
		return Request{}, err
	}
	quality, err := runScopeQualityCheck(ctx, tx, item)
	if err != nil {
		return Request{}, err
	}
	if !quality.Passed {
		return Request{}, &ScopeQualityError{Check: quality}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET status='submitted', updated_by=$2::uuid, updated_at=now()
		WHERE id=$1::uuid AND status='draft'`, id, principal.UserID); err != nil {
		return Request{}, fmt.Errorf("submit security test request: %w", err)
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "submit", "draft", "submitted", "",
		map[string]any{"asset_count": quality.AssetCount, "warning_count": quality.WarningCount}); err != nil {
		return Request{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_request.submit", id, "提交上线安全测试申请 "+item.RequestName,
		map[string]any{"request_code": item.Code, "asset_count": quality.AssetCount, "warning_count": quality.WarningCount}, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: "security_test_request.submitted", Category: notification.CategorySecurityTestRequest,
		TargetOrganizationID: orgID, ActorUserID: principal.UserID, RequiredPermission: auth.PermissionSecurityTest,
		ResourceType: "security_test_request", ResourceID: id, ResourceLabel: item.RequestName,
		Details: map[string]any{"request_code": item.Code, "request_name": item.RequestName, "organization_name": item.ApplicantOrganizationName},
	}); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request submit: %w", err)
	}
	return s.Get(ctx, principal, id)
}

// Withdraw pulls a submitted request back to draft before group review.
func (s *Service) Withdraw(ctx context.Context, principal auth.Principal, id string, requestID, ipAddress string) (Request, error) {
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant {
		return Request{}, ErrForbidden
	}
	return s.applicantTransition(ctx, principal, id, orgID, []string{"submitted"}, "draft", "withdraw", "", requestID, ipAddress, nil)
}

// Cancel abandons a request: applicants drop drafts and rejected requests,
// group security can cancel before testing starts. A testing request is only
// cancellable while its converted batch has not been started.
func (s *Service) Cancel(ctx context.Context, principal auth.Principal, id string, input TransitionInput, requestID, ipAddress string) (Request, error) {
	input.Comment = strings.TrimSpace(input.Comment)
	if len(input.Comment) > 2000 {
		return Request{}, ErrInvalid
	}
	if orgID, isApplicant := applicantOrg(principal); isApplicant {
		if input.Comment == "" {
			return Request{}, ErrInvalid
		}
		return s.applicantTransition(ctx, principal, id, orgID, []string{"draft", "rejected"}, "cancelled", "cancel", input.Comment, requestID, ipAddress, nil)
	}
	if !canManage(principal) {
		return Request{}, ErrForbidden
	}
	if input.Comment == "" {
		return Request{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin cancel security test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if item.Status != "submitted" && item.Status != "testing" {
		return Request{}, ErrState
	}
	if item.Status == "testing" && item.ConvertedBatchStatus != "" && item.ConvertedBatchStatus != "authorized" {
		return Request{}, ErrState
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET status='cancelled', updated_by=$2::uuid, updated_at=now()
		WHERE id=$1::uuid`, id, principal.UserID); err != nil {
		return Request{}, fmt.Errorf("cancel security test request: %w", err)
	}
	if err := s.finishTransition(ctx, tx, principal, item, "cancelled", "cancel", input.Comment, nil, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: "security_test_request.cancelled", Category: notification.CategorySecurityTestRequest,
		TargetOrganizationID: item.ApplicantOrganizationID, ActorUserID: principal.UserID, RequiredPermission: auth.PermissionSecurityTestRequest,
		ResourceType: "security_test_request", ResourceID: id, ResourceLabel: item.RequestName,
		Details: map[string]any{"request_code": item.Code, "comment": input.Comment},
	}); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request cancel: %w", err)
	}
	return s.Get(ctx, principal, id)
}

// Reject returns a submitted request with review comments for rework.
func (s *Service) Reject(ctx context.Context, principal auth.Principal, id string, input TransitionInput, requestID, ipAddress string) (Request, error) {
	if !canManage(principal) {
		return Request{}, ErrForbidden
	}
	input.Comment = strings.TrimSpace(input.Comment)
	if input.Comment == "" || len(input.Comment) > 2000 || !validUUID(id) {
		return Request{}, ErrInvalid
	}
	return s.managerTransition(ctx, principal, id, []string{"submitted"}, "rejected", "reject", input.Comment, requestID, ipAddress, `
		reviewed_by=$3::uuid, reviewed_at=now(), review_comment=$4`)
}

// Approve converts a submitted request into an authorized security test
// batch within one transaction: the batch inherits type, window and scope
// snapshot, and records the source request for traceability.
func (s *Service) Approve(ctx context.Context, principal auth.Principal, id string, input ApproveInput, requestID, ipAddress string) (Request, error) {
	if !canManage(principal) {
		return Request{}, ErrForbidden
	}
	if !validUUID(id) {
		return Request{}, ErrInvalid
	}
	input.Comment = strings.TrimSpace(input.Comment)
	input.AuthorizationNote = strings.TrimSpace(input.AuthorizationNote)
	if input.AuthorizationNote == "" || len(input.AuthorizationNote) > 20000 || len(input.Comment) > 2000 {
		return Request{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin approve security test request: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if item.Status != "submitted" {
		return Request{}, ErrState
	}
	startAt, endAt := item.TestWindowStartAt, item.TestWindowEndAt
	if input.TestWindowStartAt != nil {
		startAt = input.TestWindowStartAt
	}
	if input.TestWindowEndAt != nil {
		endAt = input.TestWindowEndAt
	}
	if startAt == nil || endAt == nil || !endAt.After(*startAt) {
		return Request{}, ErrInvalid
	}
	batchCode, err := newBatchCode()
	if err != nil {
		return Request{}, err
	}
	batchName := item.RequestName + "（" + item.Code + "）"
	if len(batchName) > 255 {
		batchName = batchName[:255]
	}
	var batchID string
	err = tx.QueryRow(ctx, `
		INSERT INTO security_test_batches
		(batch_code, batch_name, test_type, status, provider, authorization_note, authorized_by, authorized_at,
		 start_at, end_at, notes, created_by, updated_by, source_request_id)
		VALUES ($1, $2, $3, 'authorized', $4, $5, $6::uuid, now(), $7, $8, $9, $6::uuid, $6::uuid, $10::uuid)
		RETURNING id::text`,
		batchCode, batchName, item.TestType, item.ApplicantOrganizationName, input.AuthorizationNote,
		principal.UserID, *startAt, *endAt, input.Comment, id).Scan(&batchID)
	if err != nil {
		return Request{}, fmt.Errorf("create security test batch from request: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO security_test_scope_assets
		(batch_id, asset_id, working_version_id, asset_type_snapshot, asset_value_snapshot, asset_name_snapshot,
		 organization_id, organization_code_snapshot, organization_name_snapshot,
		 business_system_id, business_system_code_snapshot, business_system_name_snapshot,
		 network_scope_snapshot, network_area_name_snapshot, environment_snapshot, importance_snapshot, included_by)
		SELECT $1::uuid, r.published_asset_id, r.working_asset_id, r.asset_type_snapshot, r.asset_value_snapshot, r.asset_name_snapshot,
		       r.organization_id, r.organization_code_snapshot, r.organization_name_snapshot,
		       r.business_system_id, r.business_system_code_snapshot, r.business_system_name_snapshot,
		       r.network_scope_snapshot, r.network_area_name_snapshot, r.environment_snapshot, r.importance_snapshot, $2::uuid
		FROM security_test_request_scope_assets r WHERE r.request_id=$3::uuid`, batchID, principal.UserID, id); err != nil {
		return Request{}, fmt.Errorf("copy request scope into test batch: %w", err)
	}
	if err := writeBatchEvent(ctx, tx, principal.UserID, batchID, "create", "", "draft", "", map[string]any{"source_request_code": item.Code}); err != nil {
		return Request{}, err
	}
	if err := writeBatchEvent(ctx, tx, principal.UserID, batchID, "authorize", "draft", "authorized", input.AuthorizationNote,
		map[string]any{"source_request_code": item.Code, "comment": input.Comment}); err != nil {
		return Request{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET
			status='testing', reviewed_by=$2::uuid, reviewed_at=now(), review_comment=$3,
			converted_batch_id=$4::uuid, updated_by=$2::uuid, updated_at=now()
		WHERE id=$1::uuid AND status='submitted'`, id, principal.UserID, input.Comment, batchID); err != nil {
		return Request{}, fmt.Errorf("approve security test request: %w", err)
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "approve", "submitted", "testing", input.Comment,
		map[string]any{"batch_id": batchID, "batch_code": batchCode}); err != nil {
		return Request{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_request.approve", id, "审批通过上线安全测试申请 "+item.RequestName,
		map[string]any{"request_code": item.Code, "batch_code": batchCode, "batch_id": batchID}, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: "security_test_request.approved", Category: notification.CategorySecurityTestRequest,
		TargetOrganizationID: item.ApplicantOrganizationID, ActorUserID: principal.UserID, RequiredPermission: auth.PermissionSecurityTestRequest,
		ResourceType: "security_test_request", ResourceID: id, ResourceLabel: item.RequestName,
		Details: map[string]any{"request_code": item.Code, "batch_code": batchCode, "comment": input.Comment},
	}); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request approval: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) applicantTransition(ctx context.Context, principal auth.Principal, id, orgID string, fromStatuses []string, toStatus, action, comment, requestID, ipAddress string, details map[string]any) (Request, error) {
	if !validUUID(id) {
		return Request{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin security test request transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if item.ApplicantOrganizationID != orgID {
		return Request{}, ErrForbidden
	}
	if !contains(fromStatuses, item.Status) {
		return Request{}, ErrState
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_test_requests SET status=$2, updated_by=$3::uuid, updated_at=now()
		WHERE id=$1::uuid`, id, toStatus, principal.UserID); err != nil {
		return Request{}, fmt.Errorf("transition security test request: %w", err)
	}
	if err := s.finishTransition(ctx, tx, principal, item, toStatus, action, comment, details, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if item.Status == "submitted" {
		// Withdrawals reach group security through the same dynamic candidate
		// message used for submissions.
		if err := notification.WriteEvent(ctx, tx, notification.EventInput{
			EventType: "security_test_request.withdrawn", Category: notification.CategorySecurityTestRequest,
			TargetOrganizationID: orgID, ActorUserID: principal.UserID, RequiredPermission: auth.PermissionSecurityTest,
			ResourceType: "security_test_request", ResourceID: id, ResourceLabel: item.RequestName,
			Details: map[string]any{"request_code": item.Code},
		}); err != nil {
			return Request{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request transition: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) managerTransition(ctx context.Context, principal auth.Principal, id string, fromStatuses []string, toStatus, action, comment, requestID, ipAddress, extraSet string) (Request, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Request{}, fmt.Errorf("begin security test request transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := lockRequest(ctx, tx, id)
	if err != nil {
		return Request{}, err
	}
	if !contains(fromStatuses, item.Status) {
		return Request{}, ErrState
	}
	query := `UPDATE security_test_requests SET status=$2, updated_by=$3::uuid, updated_at=now(), ` + extraSet + ` WHERE id=$1::uuid`
	if _, err := tx.Exec(ctx, query, id, toStatus, principal.UserID, comment); err != nil {
		return Request{}, fmt.Errorf("transition security test request: %w", err)
	}
	if err := s.finishTransition(ctx, tx, principal, item, toStatus, action, comment, nil, requestID, ipAddress); err != nil {
		return Request{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: "security_test_request." + action, Category: notification.CategorySecurityTestRequest,
		TargetOrganizationID: item.ApplicantOrganizationID, ActorUserID: principal.UserID, RequiredPermission: auth.PermissionSecurityTestRequest,
		ResourceType: "security_test_request", ResourceID: id, ResourceLabel: item.RequestName,
		Details: map[string]any{"request_code": item.Code, "comment": comment},
	}); err != nil {
		return Request{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Request{}, fmt.Errorf("commit security test request transition: %w", err)
	}
	return s.Get(ctx, principal, id)
}

// finishTransition is the shared tail of every status change: request event,
// audit log entry. The status UPDATE itself must already have run.
func (s *Service) finishTransition(ctx context.Context, tx pgx.Tx, principal auth.Principal, item Request, toStatus, action, comment string, details map[string]any, requestID, ipAddress string) error {
	if details == nil {
		details = map[string]any{}
	}
	if err := writeEvent(ctx, tx, principal.UserID, item.ID, action, item.Status, toStatus, comment, details); err != nil {
		return err
	}
	return writeAudit(ctx, tx, principal, "security_test_request."+action, item.ID,
		"上线安全测试申请状态变更："+item.RequestName,
		map[string]any{"request_code": item.Code, "from_status": item.Status, "to_status": toStatus, "comment": comment}, requestID, ipAddress)
}

func (s *Service) get(ctx context.Context, id string) (Request, error) {
	var item Request
	var total int
	err := s.pool.QueryRow(ctx, requestColumns+`, 1`+requestFrom+` WHERE r.id::text=$1`, id).Scan(requestScanTargets(&item, &total)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("get security test request: %w", err)
	}
	return item, nil
}

func ensureViewable(principal auth.Principal, item Request) error {
	if canManage(principal) {
		return nil
	}
	orgID, isApplicant := applicantOrg(principal)
	if !isApplicant || orgID != item.ApplicantOrganizationID {
		return ErrForbidden
	}
	return nil
}

func lockRequest(ctx context.Context, tx pgx.Tx, id string) (Request, error) {
	var item Request
	var total int
	err := tx.QueryRow(ctx, requestColumns+`, 1`+requestFrom+` WHERE r.id::text=$1 FOR UPDATE OF r`, id).Scan(requestScanTargets(&item, &total)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, fmt.Errorf("lock security test request: %w", err)
	}
	return item, nil
}

func (s *Service) loadScope(ctx context.Context, id string) ([]ScopeAsset, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(published_asset_id::text, ''), COALESCE(working_asset_id::text, ''),
		       asset_type_snapshot, asset_value_snapshot, COALESCE(asset_name_snapshot, ''),
		       COALESCE(organization_id::text, ''), organization_code_snapshot, organization_name_snapshot,
		       COALESCE(business_system_id::text, ''), COALESCE(business_system_code_snapshot, ''), COALESCE(business_system_name_snapshot, ''),
		       network_scope_snapshot, COALESCE(network_area_name_snapshot, ''), environment_snapshot, importance_snapshot,
		       report_action, snapshot_at
		FROM security_test_request_scope_assets WHERE request_id=$1::uuid
		ORDER BY organization_code_snapshot, asset_type_snapshot, asset_value_snapshot`, id)
	if err != nil {
		return nil, fmt.Errorf("load security test request scope: %w", err)
	}
	defer rows.Close()
	items := make([]ScopeAsset, 0)
	for rows.Next() {
		var item ScopeAsset
		if err := rows.Scan(&item.ID, &item.PublishedAssetID, &item.WorkingAssetID, &item.AssetType, &item.AssetValue, &item.AssetName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.BusinessSystemID,
			&item.BusinessSystemCode, &item.BusinessSystemName, &item.NetworkScope, &item.NetworkAreaName,
			&item.Environment, &item.Importance, &item.ReportAction, &item.SnapshotAt); err != nil {
			return nil, fmt.Errorf("scan security test request scope: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// storedScopeRefs reloads the scope references kept on a saved request so
// Submit can re-freeze snapshots and recompute report actions from live data.
func storedScopeRefs(ctx context.Context, tx pgx.Tx, requestID string) (publishedIDs, workingIDs []string, err error) {
	rows, err := tx.Query(ctx, `
		SELECT COALESCE(published_asset_id::text, ''), COALESCE(working_asset_id::text, '')
		FROM security_test_request_scope_assets WHERE request_id=$1::uuid`, requestID)
	if err != nil {
		return nil, nil, fmt.Errorf("load security test request scope refs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var published, working string
		if err := rows.Scan(&published, &working); err != nil {
			return nil, nil, fmt.Errorf("scan security test request scope ref: %w", err)
		}
		if published != "" {
			publishedIDs = append(publishedIDs, published)
		} else {
			workingIDs = append(workingIDs, working)
		}
	}
	return publishedIDs, workingIDs, rows.Err()
}

// requestColumns and requestFrom make up the request row; callers append
// their own total column (count(*) OVER () for pages, a constant for single
// rows) because FOR UPDATE cannot be combined with window functions.
const requestColumns = `
SELECT r.id::text, r.request_code, r.request_name, r.applicant_organization_id::text,
       o.organization_code, o.organization_name,
       COALESCE(r.business_system_id::text, ''), COALESCE(r.business_system_draft_id::text, ''),
       COALESCE(r.business_system_code_snapshot, ''), COALESCE(r.business_system_name_snapshot, ''),
       r.test_type, r.expected_launch_at, r.test_window_start_at, r.test_window_end_at,
       COALESCE(r.contact_user_id::text, ''), COALESCE(cu.display_name, cu.username, ''),
       r.background, r.status,
       COALESCE(r.reviewed_by::text, ''), COALESCE(ru.display_name, ''), r.reviewed_at, COALESCE(r.review_comment, ''),
       COALESCE(r.converted_batch_id::text, ''), COALESCE(b.batch_code, ''), COALESCE(b.status, ''),
       COALESCE(r.launch_decided_by::text, ''), COALESCE(lu.display_name, ''), r.launch_decided_at, COALESCE(r.launch_comment, ''),
       COALESCE(r.reported_task_id::text, ''),
       EXISTS (SELECT 1 FROM security_test_request_scope_assets u WHERE u.request_id=r.id AND u.working_asset_id IS NOT NULL),
       COALESCE(cr.display_name, ''), r.created_at, r.updated_at,
       (SELECT count(*) FROM security_test_request_scope_assets s WHERE s.request_id=r.id),
       (SELECT count(*) FROM security_findings f WHERE f.test_batch_id=r.converted_batch_id AND f.status IN ('open', 'assigned'))`

const requestFrom = `
FROM security_test_requests r
JOIN organizations o ON o.id=r.applicant_organization_id
LEFT JOIN users cu ON cu.id=r.contact_user_id
LEFT JOIN users ru ON ru.id=r.reviewed_by
LEFT JOIN security_test_batches b ON b.id=r.converted_batch_id
LEFT JOIN users lu ON lu.id=r.launch_decided_by
LEFT JOIN users cr ON cr.id=r.created_by`

func requestScanTargets(item *Request, total *int) []any {
	return []any{&item.ID, &item.Code, &item.RequestName, &item.ApplicantOrganizationID,
		&item.ApplicantOrganizationCode, &item.ApplicantOrganizationName,
		&item.BusinessSystemID, &item.BusinessSystemDraftID, &item.BusinessSystemCode, &item.BusinessSystemName,
		&item.TestType, &item.ExpectedLaunchAt, &item.TestWindowStartAt, &item.TestWindowEndAt,
		&item.ContactUserID, &item.ContactName, &item.Background, &item.Status,
		&item.ReviewedBy, &item.ReviewedByName, &item.ReviewedAt, &item.ReviewComment,
		&item.ConvertedBatchID, &item.ConvertedBatchCode, &item.ConvertedBatchStatus,
		&item.LaunchDecidedBy, &item.LaunchDecidedByName, &item.LaunchDecidedAt, &item.LaunchComment,
		&item.ReportedTaskID, &item.HasUnreportedScope, &item.CreatedByName, &item.CreatedAt, &item.UpdatedAt,
		&item.ScopeAssetCount, &item.OpenFindingCount, total}
}

func writeEvent(ctx context.Context, tx pgx.Tx, userID, requestID, action, fromStatus, toStatus, comment string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode security test request event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO security_test_request_events (request_id, actor_user_id, action, from_status, to_status, comment, details)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7::jsonb)`,
		requestID, userID, action, fromStatus, toStatus, comment, payload)
	if err != nil {
		return fmt.Errorf("write security test request event: %w", err)
	}
	return nil
}

func writeBatchEvent(ctx context.Context, tx pgx.Tx, userID, batchID, action, fromStatus, toStatus, comment string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode security test batch event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO security_test_batch_events (batch_id, actor_user_id, action, from_status, to_status, comment, details)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7::jsonb)`,
		batchID, userID, action, fromStatus, toStatus, comment, payload)
	if err != nil {
		return fmt.Errorf("write security test batch event: %w", err)
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, action, resourceID, summary string, details map[string]any, requestID, ipAddress string) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode security test request audit: %w", err)
	}
	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, 'security_test_request', $4, $5, $6::jsonb, NULLIF($7, '')::inet, NULLIF($8, ''))`,
		principal.UserID, organizationID, action, resourceID, summary, payload, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write security test request audit: %w", err)
	}
	return nil
}

func normalizeInput(input *Input) {
	input.RequestName = strings.TrimSpace(input.RequestName)
	input.TestType = strings.TrimSpace(input.TestType)
	input.ContactUserID = strings.TrimSpace(input.ContactUserID)
	input.Background = strings.TrimSpace(input.Background)
	input.BusinessSystemID = strings.TrimSpace(input.BusinessSystemID)
	input.BusinessSystemDraftID = strings.TrimSpace(input.BusinessSystemDraftID)
	input.WorkingAssetIDs = uniqueIDs(input.WorkingAssetIDs)
	input.PublishedAssetIDs = uniqueIDs(input.PublishedAssetIDs)
}

func validInput(input Input) bool {
	published, draft := strings.TrimSpace(input.BusinessSystemID), strings.TrimSpace(input.BusinessSystemDraftID)
	if (published == "") == (draft == "") {
		return false
	}
	if input.RequestName == "" || len(input.RequestName) > 255 || input.Background == "" || len(input.Background) > 10000 ||
		!validTestType(input.TestType) || input.ExpectedLaunchAt.IsZero() || !validUUID(input.ContactUserID) ||
		(published != "" && !validUUID(published)) || (draft != "" && !validUUID(draft)) {
		return false
	}
	if input.TestWindowStartAt != nil && input.TestWindowEndAt != nil && !input.TestWindowEndAt.After(*input.TestWindowStartAt) {
		return false
	}
	total := len(input.WorkingAssetIDs) + len(input.PublishedAssetIDs)
	if total < 1 || total > maxScopeAssets {
		return false
	}
	for _, id := range append(input.WorkingAssetIDs, input.PublishedAssetIDs...) {
		if !validUUID(id) {
			return false
		}
	}
	return true
}

func validTestType(value string) bool {
	return contains([]string{"penetration", "manual", "scan", "retest", "other"}, value)
}

func validStatus(value string) bool {
	return contains([]string{"draft", "submitted", "rejected", "testing", "pending_launch", "cancelled", "closed"}, value)
}

func validUUID(value string) bool { return uuidPattern.MatchString(value) }

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func uniqueIDs(values []string) []string {
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

func newCode() (string, error) {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate security test request code: %w", err)
	}
	return "TR-" + time.Now().Format("20060102") + "-" + strings.ToUpper(hex.EncodeToString(buffer)), nil
}

func newBatchCode() (string, error) {
	buffer := make([]byte, 4)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("generate security test batch code: %w", err)
	}
	return "ST-" + time.Now().Format("20060102") + "-" + strings.ToUpper(hex.EncodeToString(buffer)), nil
}
