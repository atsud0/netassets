package securityworkorder

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

	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/findingstatus"
	"netassets/internal/notification"
	"netassets/internal/pagination"
	"netassets/internal/textlength"
)

var (
	ErrForbidden                 = errors.New("forbidden")
	ErrNotFound                  = errors.New("not found")
	ErrConflict                  = errors.New("conflict")
	ErrIndividualWorkOrderAction = errors.New("individual work order requires remediation task")
	ErrInvalid                   = errors.New("invalid input")
	ErrState                     = errors.New("invalid state")
	// ErrEditConflict reports that another user changed the row after this caller
	// read it. It is a distinct error so the API can answer 409 with a retry hint
	// instead of lumping it together with workflow conflicts.
	ErrEditConflict = errors.New("security finding has been updated by another user")
	// ErrVersionRequired reports a read-modify-write caller that forgot to send
	// the row version it read. Rejecting it is stricter than silently
	// overwriting: an unconditional UPDATE is exactly how two operators lose
	// each other's status decisions.
	ErrVersionRequired = errors.New("expected_updated_at is required")
	uuidPattern        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type Finding struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Title    string `json:"title"`
	Severity string `json:"severity"`
	Status   string `json:"status"`
	// Status is the vulnerability/risk state. These fields are independent dimensions.
	AssetAssociationStatus    string     `json:"asset_association_status"`
	RemediationStatus         string     `json:"remediation_status"`
	AssigneeOrganizationID    string     `json:"assignee_organization_id"`
	AssigneeOrganizationCode  string     `json:"assignee_organization_code"`
	AssigneeOrganizationName  string     `json:"assignee_organization_name"`
	AssetID                   string     `json:"asset_id,omitempty"`
	AssetValue                string     `json:"asset_value,omitempty"`
	AssetName                 string     `json:"asset_name,omitempty"`
	BusinessSystemID          string     `json:"business_system_id,omitempty"`
	BusinessSystemCode        string     `json:"business_system_code,omitempty"`
	BusinessSystemName        string     `json:"business_system_name,omitempty"`
	TestBatchID               string     `json:"test_batch_id,omitempty"`
	TestBatchCode             string     `json:"test_batch_code,omitempty"`
	TestBatchName             string     `json:"test_batch_name,omitempty"`
	VulnerabilityType         string     `json:"vulnerability_type,omitempty"`
	Description               string     `json:"description"`
	Evidence                  string     `json:"evidence,omitempty"`
	RemediationAdvice         string     `json:"remediation_advice,omitempty"`
	Source                    string     `json:"source"`
	DiscoveredAt              time.Time  `json:"discovered_at"`
	CreatedByName             string     `json:"created_by_name,omitempty"`
	CreatedAt                 time.Time  `json:"created_at"`
	UpdatedAt                 time.Time  `json:"updated_at"`
	RiskAcceptedAt            *time.Time `json:"risk_accepted_at,omitempty"`
	RiskAcceptedUntil         *time.Time `json:"risk_accepted_until,omitempty"`
	RiskAcceptanceReason      string     `json:"risk_acceptance_reason,omitempty"`
	RiskAcceptedByName        string     `json:"risk_accepted_by_name,omitempty"`
	RiskAcceptedFromStatus    string     `json:"risk_accepted_from_status,omitempty"`
	RiskReviewedAt            *time.Time `json:"risk_reviewed_at,omitempty"`
	RiskReviewedByName        string     `json:"risk_reviewed_by_name,omitempty"`
	RiskReviewComment         string     `json:"risk_review_comment,omitempty"`
	RiskAcceptanceExpired     bool       `json:"risk_acceptance_expired"`
	ImportStatusRaw           string     `json:"import_status_raw,omitempty"`
	ImportStatusNormalized    string     `json:"import_status_normalized,omitempty"`
	ImportStatusMappingReason string     `json:"import_status_mapping_reason,omitempty"`
	DuplicateOfFindingID      string     `json:"duplicate_of_finding_id,omitempty"`
	// PendingAssetDraftID is the unpublished working version this finding waits
	// on. It is echoed back to the UI so a pending row can say which draft will
	// resolve it once published.
	PendingAssetDraftID string `json:"pending_asset_draft_id,omitempty"`
	// AssetDeleted reports that the asset this finding hangs on has been soft
	// deleted. The finding is not unlinked and not lost: the vulnerability was
	// real, the asset was retired. Without this flag the list shows an empty
	// asset cell that is indistinguishable from a finding that was never linked,
	// so the row looks broken and nobody can tell which case it is.
	AssetDeleted bool `json:"asset_deleted,omitempty"`
	// AssetLinkState is the single word the UI needs: live, pending (waiting on an
	// unpublished working version), retired (asset soft deleted) or unlinked (no
	// asset was ever decided).
	AssetLinkState string `json:"asset_link_state,omitempty"`
}

type FindingInput struct {
	Title                  string    `json:"title"`
	Severity               string    `json:"severity"`
	Status                 string    `json:"status"`
	AssigneeOrganizationID string    `json:"assignee_organization_id"`
	AssetID                string    `json:"asset_id"`
	BusinessSystemID       string    `json:"business_system_id"`
	TestBatchID            string    `json:"test_batch_id"`
	VulnerabilityType      string    `json:"vulnerability_type"`
	Description            string    `json:"description"`
	Evidence               string    `json:"evidence"`
	RemediationAdvice      string    `json:"remediation_advice"`
	Source                 string    `json:"source"`
	DiscoveredAt           time.Time `json:"discovered_at"`
	// PendingAssetDraftID attaches the finding to an unpublished organization
	// working version. An imported vulnerability is real the moment it arrives,
	// so it is recorded and counted even when the asset register has not caught
	// up yet; publishing the draft fills in AssetID.
	PendingAssetDraftID string `json:"pending_asset_draft_id,omitempty"`
	// Import status evidence is accepted only from the import pipeline. Generic
	// API callers cannot use these fields to bypass the status workflow.
	ImportStatusRaw           string `json:"-"`
	ImportStatusNormalized    string `json:"-"`
	ImportStatusMappingReason string `json:"-"`
	DuplicateOfFindingID      string `json:"-"`
	// ExpectedUpdatedAt is the optimistic-lock token of a generic finding edit.
	// Two security operators editing the same finding is a normal event, not an
	// edge case, so the caller must send back the row version it read instead of
	// letting a stale form overwrite a newer status transition. Create does not
	// need it; every PATCH and every status action must send it, so a caller
	// cannot bypass the lock by omitting the field.
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// validFindingAssetLinkState keeps the API filter vocabulary identical to the
// values findingAssetLinkState can emit, so a bad filter is rejected instead of
// silently returning an empty page.
func validFindingAssetLinkState(state string) bool {
	switch state {
	case "live", "pending", "retired", "unlinked":
		return true
	default:
		return false
	}
}

// findingAssetLinkStateFilter mirrors findingAssetLinkState in SQL so filtering
// by asset_link_state returns exactly the rows the UI would label with that
// state. The placeholder carries the requested value; an empty value means no filtering.
func findingAssetLinkStateFilter(findingAlias, assetAlias, placeholder string) string {
	return "AND (" + placeholder + "::text = ''\n" +
		"    OR (" + placeholder + "::text = 'live' AND " + findingAlias + ".asset_id IS NOT NULL AND " + assetAlias + ".deleted_at IS NULL)\n" +
		"    OR (" + placeholder + "::text = 'retired' AND " + findingAlias + ".asset_id IS NOT NULL AND " + assetAlias + ".deleted_at IS NOT NULL)\n" +
		"    OR (" + placeholder + "::text = 'pending' AND " + findingAlias + ".asset_id IS NULL AND " + findingAlias + ".pending_asset_draft_id IS NOT NULL)\n" +
		"    OR (" + placeholder + "::text = 'unlinked' AND " + findingAlias + ".asset_id IS NULL AND " + findingAlias + ".pending_asset_draft_id IS NULL)\n" +
		"  )"
}

func findingAssetLinkState(assetID, pendingDraftID string, assetDeleted bool) string {
	switch {
	case assetID != "" && assetDeleted:
		return "retired"
	case assetID != "":
		return "live"
	case pendingDraftID != "":
		return "pending"
	default:
		return "unlinked"
	}
}

// FindingSummaryStats keeps "pending" strictly equal to the vulnerability
// lifecycle status (FindingPending). Every other waiting state lives in its own
// counter so a group manager can never read one number as five different
// backlogs: asset association, remediation acceptance, organization review,
// security review, accepted risk and expired accepted risk each have their own
// field, and severity counters are counted over the whole visible scope rather
// than over the pending subset.
type FindingSummaryStats struct {
	Total    int `json:"total"`
	Pending  int `json:"pending"`
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`

	PendingAssetLink          int `json:"pending_asset_link"`
	PendingAcceptance         int `json:"pending_acceptance"`
	PendingOrganizationReview int `json:"pending_organization_review"`
	PendingSecurityReview     int `json:"pending_security_review"`
	RiskAccepted              int `json:"risk_accepted"`
	RiskAcceptedExpired       int `json:"risk_accepted_expired"`
}

type FindingSummary struct {
	ID             string `json:"id"`
	Code           string `json:"code"`
	Title          string `json:"title"`
	Severity       string `json:"severity"`
	Status         string `json:"status"`
	AssetID        string `json:"asset_id,omitempty"`
	AssetValue     string `json:"asset_value,omitempty"`
	AssetName      string `json:"asset_name,omitempty"`
	AssetLinkState string `json:"asset_link_state,omitempty"`
	// PendingAssetDraftID and AssetDeleted are the inputs to AssetLinkState; the
	// work order finding list surfaces them so a row can explain its own state.
	PendingAssetDraftID string `json:"pending_asset_draft_id,omitempty"`
	AssetDeleted        bool   `json:"asset_deleted,omitempty"`
	BusinessSystemID    string `json:"business_system_id,omitempty"`
	BusinessSystemName  string `json:"business_system_name,omitempty"`
}

type Event struct {
	ID                    int64           `json:"id"`
	Action                string          `json:"action"`
	FromStatus            string          `json:"from_status,omitempty"`
	ToStatus              string          `json:"to_status,omitempty"`
	Comment               string          `json:"comment,omitempty"`
	Details               json.RawMessage `json:"details"`
	ActorUserID           string          `json:"actor_user_id,omitempty"`
	ActorName             string          `json:"actor_name,omitempty"`
	ActorOrganizationID   string          `json:"actor_organization_id,omitempty"`
	ActorOrganizationName string          `json:"actor_organization_name,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
}

type WorkOrder struct {
	ExecutionMode            string           `json:"execution_mode"`
	RemediationCounts        json.RawMessage  `json:"remediation_counts"`
	ID                       string           `json:"id"`
	Number                   string           `json:"number"`
	Title                    string           `json:"title"`
	AssigneeOrganizationID   string           `json:"assignee_organization_id"`
	AssigneeOrganizationCode string           `json:"assignee_organization_code"`
	AssigneeOrganizationName string           `json:"assignee_organization_name"`
	Severity                 string           `json:"severity"`
	Status                   string           `json:"status"`
	Description              string           `json:"description,omitempty"`
	RemediationRequirement   string           `json:"remediation_requirement"`
	DueAt                    time.Time        `json:"due_at"`
	Overdue                  bool             `json:"overdue"`
	IssuedAt                 *time.Time       `json:"issued_at,omitempty"`
	AcceptedAt               *time.Time       `json:"accepted_at,omitempty"`
	SubmittedAt              *time.Time       `json:"submitted_at,omitempty"`
	VerifiedAt               *time.Time       `json:"verified_at,omitempty"`
	ClosedAt                 *time.Time       `json:"closed_at,omitempty"`
	CancelledAt              *time.Time       `json:"cancelled_at,omitempty"`
	RemediationProgress      string           `json:"remediation_progress,omitempty"`
	RemediationResult        string           `json:"remediation_result,omitempty"`
	VerificationResult       string           `json:"verification_result,omitempty"`
	ReturnReason             string           `json:"return_reason,omitempty"`
	CreatedByName            string           `json:"created_by_name,omitempty"`
	FindingCount             int64            `json:"finding_count"`
	Findings                 []FindingSummary `json:"findings,omitempty"`
	Events                   []Event          `json:"events,omitempty"`
	Attachments              []Attachment     `json:"attachments,omitempty"`
	CreatedAt                time.Time        `json:"created_at"`
	UpdatedAt                time.Time        `json:"updated_at"`
}

type WorkOrderSearchFilter struct {
	OrganizationID string
	Status         string
	OverdueOnly    bool
	Page           pagination.Query
}

type WorkOrderInput struct {
	ExecutionMode          string    `json:"execution_mode"`
	Title                  string    `json:"title"`
	AssigneeOrganizationID string    `json:"assignee_organization_id"`
	Description            string    `json:"description"`
	RemediationRequirement string    `json:"remediation_requirement"`
	DueAt                  time.Time `json:"due_at"`
	FindingIDs             []string  `json:"finding_ids"`
}

type ProgressInput struct {
	Progress string `json:"progress"`
	Comment  string `json:"comment"`
}

type SubmitInput struct {
	RemediationResult string `json:"remediation_result"`
	Comment           string `json:"comment"`
}

type ReviewInput struct {
	Decision           string `json:"decision"`
	VerificationResult string `json:"verification_result"`
	ReturnReason       string `json:"return_reason"`
}

type CommentInput struct {
	Comment string `json:"comment"`
}

type RiskAcceptanceInput struct {
	Reason        string    `json:"reason"`
	AcceptedUntil time.Time `json:"accepted_until"`
	// ExpectedUpdatedAt carries the row version the operator saw. A status
	// transition is a read-modify-write just like a field edit, so it takes the
	// same optimistic lock; otherwise an accept could silently revert a
	// duplicate link or a description fix made a minute earlier.
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

type RiskReviewInput struct {
	Comment           string     `json:"comment"`
	AcceptedUntil     *time.Time `json:"accepted_until"`
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// RiskRevokeInput only carries the optimistic-lock token; revoking restores the
// previous status, so there is nothing else for the caller to decide.
type RiskRevokeInput struct {
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

// DuplicateFindingInput is deliberately separate from FindingInput. A
// duplicate relationship is evidence-bearing and must never be created by a
// generic PATCH or import row without an explicit master finding.
type DuplicateFindingInput struct {
	MasterFindingID string `json:"master_finding_id"`
	Reason          string `json:"reason"`
	// ExpectedUpdatedAt protects the duplicate relationship from being written
	// on top of a status or asset change the caller never saw.
	ExpectedUpdatedAt *time.Time `json:"expected_updated_at,omitempty"`
}

type Service struct {
	pool       *pgxpool.Pool
	storageDir string
}

func NewService(pool *pgxpool.Pool, storageDirs ...string) *Service {
	storageDir := "../data"
	if len(storageDirs) > 0 && strings.TrimSpace(storageDirs[0]) != "" {
		storageDir = strings.TrimSpace(storageDirs[0])
	}
	return &Service{pool: pool, storageDir: storageDir}
}

// SummarizeFindings returns counts using the same visibility rules as the findings list.
func (s *Service) SummarizeFindings(ctx context.Context, principal auth.Principal, organizationID string) (FindingSummaryStats, error) {
	if err := s.expireDueRiskAcceptances(ctx); err != nil {
		return FindingSummaryStats{}, err
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID != "" {
		if !validUUID(organizationID) {
			return FindingSummaryStats{}, ErrInvalid
		}
		allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, organizationID)
		if err != nil {
			return FindingSummaryStats{}, err
		}
		if !allowed {
			return FindingSummaryStats{}, ErrForbidden
		}
	}
	var result FindingSummaryStats
	err := s.pool.QueryRow(ctx, accessibleCTE+`
		SELECT count(*)::int,
		       count(*) FILTER (WHERE f.status = 'pending')::int,
		       count(*) FILTER (WHERE f.severity = 'critical')::int,
		       count(*) FILTER (WHERE f.severity = 'high')::int,
		       count(*) FILTER (WHERE f.severity = 'medium')::int,
		       count(*) FILTER (WHERE f.severity = 'low')::int,
		       count(*) FILTER (WHERE f.asset_link_status IN ('pending', 'unlinked'))::int,
		       count(*) FILTER (WHERE f.remediation_status = 'pending_acceptance')::int,
		       count(*) FILTER (WHERE f.remediation_status = 'pending_org_review')::int,
		       count(*) FILTER (WHERE f.remediation_status = 'pending_security_review')::int,
		       count(*) FILTER (WHERE f.status = 'risk_accepted')::int,
		       count(*) FILTER (WHERE f.risk_acceptance_expired_at IS NOT NULL)::int
		FROM security_findings f
		WHERE (
		        $2::boolean
		        OR (
		            (
		                (f.asset_id IS NOT NULL AND EXISTS (SELECT 1 FROM finding_permission_accessible scope WHERE scope.permission_key = 'asset.manage' AND scope.id = f.assignee_organization_id))
		                OR (f.asset_id IS NULL AND f.business_system_id IS NOT NULL AND EXISTS (SELECT 1 FROM finding_permission_accessible scope WHERE scope.permission_key = 'system.manage' AND scope.id = f.assignee_organization_id))
                        OR (f.pending_asset_draft_id IS NOT NULL AND EXISTS (
                            SELECT 1 FROM inventory_asset_drafts pending_draft
                            JOIN finding_permission_accessible scope ON scope.permission_key = 'asset.manage' AND scope.id = pending_draft.organization_id
                            WHERE pending_draft.id = f.pending_asset_draft_id
                              AND pending_draft.organization_id = f.assignee_organization_id
                              AND pending_draft.draft_status IN ('editing','submitted','returned')
                        ))
		            )
		            OR (
		                f.assignee_organization_id IN (SELECT id FROM accessible)
		                AND EXISTS (
		                    SELECT 1 FROM security_work_order_findings visible_link
		                    JOIN security_work_orders visible_order ON visible_order.id = visible_link.work_order_id
		                    WHERE visible_link.finding_id = f.id AND visible_order.issued_at IS NOT NULL
		                )
		    )
		  )
		)
		AND ($3::text = '' OR f.assignee_organization_id::text = $3)`,
		principal.UserID, principal.CanManageSecurityFindings(), organizationID).Scan(
		&result.Total, &result.Pending, &result.Critical, &result.High, &result.Medium, &result.Low,
		&result.PendingAssetLink, &result.PendingAcceptance, &result.PendingOrganizationReview,
		&result.PendingSecurityReview, &result.RiskAccepted, &result.RiskAcceptedExpired)
	if err != nil {
		return FindingSummaryStats{}, fmt.Errorf("summarize security findings: %w", err)
	}
	return result, nil
}

func (s *Service) ListFindings(ctx context.Context, principal auth.Principal, organizationID, status string) ([]Finding, error) {
	// Kept for internal compatibility. HTTP collection endpoints must use SearchFindings.
	result, err := s.SearchFindings(ctx, principal, organizationID, status, "", pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

// SearchFindings applies database pagination before findings are hydrated and serialized.
func (s *Service) SearchFindings(ctx context.Context, principal auth.Principal, organizationID, status, assetLinkState string, page pagination.Query) (pagination.Result[Finding], error) {
	if err := s.expireDueRiskAcceptances(ctx); err != nil {
		return pagination.Result[Finding]{}, err
	}
	organizationID = strings.TrimSpace(organizationID)
	status = strings.TrimSpace(status)
	assetLinkState = strings.TrimSpace(assetLinkState)
	if organizationID != "" {
		if !validUUID(organizationID) {
			return pagination.Result[Finding]{}, ErrInvalid
		}
		allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, organizationID)
		if err != nil {
			return pagination.Result[Finding]{}, err
		}
		if !allowed {
			return pagination.Result[Finding]{}, ErrForbidden
		}
	}
	if status != "" && !validFindingStatus(status) {
		return pagination.Result[Finding]{}, ErrInvalid
	}
	if assetLinkState != "" && !validFindingAssetLinkState(assetLinkState) {
		return pagination.Result[Finding]{}, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, accessibleCTE+`
		SELECT f.id::text, f.finding_code, f.title, f.severity, f.status, f.asset_link_status, f.remediation_status,
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(a.id::text, ''), COALESCE(a.asset_value, ''), COALESCE(a.asset_name, ''),
		       COALESCE(f.pending_asset_draft_id::text, ''), (a.id IS NOT NULL AND a.deleted_at IS NOT NULL),
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       COALESCE(tb.id::text, ''), COALESCE(tb.batch_code, ''), COALESCE(tb.batch_name, ''),
		       COALESCE(f.vulnerability_type, ''), f.description, COALESCE(f.evidence, ''),
		       COALESCE(f.remediation_advice, ''), f.source, f.discovered_at,
		       COALESCE(u.display_name, ''), f.created_at, f.updated_at,
		       f.risk_accepted_at, f.risk_accepted_until, COALESCE(f.risk_acceptance_reason, ''),
		       COALESCE(risk_acceptor.display_name, ''), COALESCE(f.risk_accepted_from_status, ''),
		       f.risk_reviewed_at, COALESCE(risk_reviewer.display_name, ''), COALESCE(f.risk_review_comment, ''),
		       (f.status = 'risk_accepted' AND f.risk_accepted_until IS NOT NULL AND f.risk_accepted_until < now()),
		       COALESCE(f.import_status_raw,''), COALESCE(f.import_status_normalized,''), COALESCE(f.import_status_mapping_reason,''),
		       COALESCE(f.duplicate_of_finding_id::text,''), count(*) OVER ()
		FROM security_findings f
		JOIN organizations o ON o.id = f.assignee_organization_id
		LEFT JOIN assets a ON a.id = f.asset_id
		LEFT JOIN business_systems bs ON bs.id = f.business_system_id
		LEFT JOIN security_test_batches tb ON tb.id = f.test_batch_id
		LEFT JOIN users u ON u.id = f.created_by
		LEFT JOIN users risk_acceptor ON risk_acceptor.id = f.risk_accepted_by
		LEFT JOIN users risk_reviewer ON risk_reviewer.id = f.risk_reviewed_by
		WHERE (
		        $2::boolean
		        OR (
		            (
		                (f.asset_id IS NOT NULL AND EXISTS (
		                    SELECT 1 FROM finding_permission_accessible scope
		                    WHERE scope.permission_key = 'asset.manage' AND scope.id = f.assignee_organization_id
		                ))
		                OR (f.asset_id IS NULL AND f.business_system_id IS NOT NULL AND EXISTS (
		                    SELECT 1 FROM finding_permission_accessible scope
		                    WHERE scope.permission_key = 'system.manage' AND scope.id = f.assignee_organization_id
		                ))
		                OR (f.pending_asset_draft_id IS NOT NULL AND EXISTS (
		                    SELECT 1 FROM inventory_asset_drafts pending_draft
		                    JOIN finding_permission_accessible scope
		                      ON scope.permission_key = 'asset.manage' AND scope.id = pending_draft.organization_id
		                    WHERE pending_draft.id = f.pending_asset_draft_id
		                      AND pending_draft.organization_id = f.assignee_organization_id
		                      AND pending_draft.draft_status IN ('editing','submitted','returned')
		                ))
		            )
		            OR (
		                f.assignee_organization_id IN (SELECT id FROM accessible)
		                AND EXISTS (
		                    SELECT 1 FROM security_work_order_findings visible_link
		                    JOIN security_work_orders visible_order ON visible_order.id = visible_link.work_order_id
		                    WHERE visible_link.finding_id = f.id AND visible_order.issued_at IS NOT NULL
		                )
		            )
		        )
		      )
		  AND ($3::text = '' OR f.assignee_organization_id::text = $3)
		  AND ($4::text = '' OR f.status = $4)
		  `+findingAssetLinkStateFilter("f", "a", "$5")+`
		ORDER BY f.discovered_at DESC, f.created_at DESC
		LIMIT $6 OFFSET $7`, principal.UserID, principal.CanManageSecurityFindings(), organizationID, status, assetLinkState, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Finding]{}, fmt.Errorf("search security findings: %w", err)
	}
	defer rows.Close()
	items := make([]Finding, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Finding
		if err := scanFindingWithTotal(rows, &item, &total); err != nil {
			return pagination.Result[Finding]{}, fmt.Errorf("scan security finding: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Finding]{}, fmt.Errorf("iterate security findings: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, accessibleCTE+`
            SELECT count(*) FROM security_findings f
            LEFT JOIN assets a ON a.id = f.asset_id
            WHERE (
                    $2::boolean
                    OR (
                        (
                            (f.asset_id IS NOT NULL AND EXISTS (
                                SELECT 1 FROM finding_permission_accessible scope
                                WHERE scope.permission_key = 'asset.manage' AND scope.id = f.assignee_organization_id
                            ))
                            OR (f.asset_id IS NULL AND f.business_system_id IS NOT NULL AND EXISTS (
                                SELECT 1 FROM finding_permission_accessible scope
                                WHERE scope.permission_key = 'system.manage' AND scope.id = f.assignee_organization_id
                            ))
                            OR (f.pending_asset_draft_id IS NOT NULL AND EXISTS (
                                SELECT 1 FROM inventory_asset_drafts pending_draft
                                JOIN finding_permission_accessible scope
                                  ON scope.permission_key = 'asset.manage' AND scope.id = pending_draft.organization_id
                                WHERE pending_draft.id = f.pending_asset_draft_id
                                  AND pending_draft.organization_id = f.assignee_organization_id
                                  AND pending_draft.draft_status IN ('editing','submitted','returned')
                            ))
                        )
                        OR (
                            f.assignee_organization_id IN (SELECT id FROM accessible)
                            AND EXISTS (
                                SELECT 1 FROM security_work_order_findings visible_link
                                JOIN security_work_orders visible_order ON visible_order.id = visible_link.work_order_id
                                WHERE visible_link.finding_id = f.id AND visible_order.issued_at IS NOT NULL
                            )
                        )
                    )
                  )
              AND ($3::text = '' OR f.assignee_organization_id::text = $3)
              AND ($4::text = '' OR f.status = $4)
              `+findingAssetLinkStateFilter("f", "a", "$5")+`
        `, principal.UserID, principal.CanManageSecurityFindings(), organizationID, status, assetLinkState).Scan(&total); err != nil {
			return pagination.Result[Finding]{}, fmt.Errorf("count security findings: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) CreateFinding(ctx context.Context, principal auth.Principal, input FindingInput, requestID, ipAddress string) (Finding, error) {
	if !principal.CanManageSecurityFindings() {
		return Finding{}, ErrForbidden
	}
	normalizeFindingInput(&input)
	if !validFindingInput(input) {
		return Finding{}, ErrInvalid
	}
	if input.PendingAssetDraftID != "" {
		// A pending asset link is an import-pipeline transition, not a public
		// finding-edit escape hatch. Otherwise a caller could point a finding at
		// another organization's unpublished draft.
		return Finding{}, ErrInvalid
	}
	if input.Source != "import" && (input.ImportStatusRaw != "" || input.ImportStatusNormalized != "" || input.ImportStatusMappingReason != "") {
		return Finding{}, ErrInvalid
	}
	if input.Source == "import" {
		if !findingstatus.Valid(input.ImportStatusNormalized) || input.Status != input.ImportStatusNormalized || input.ImportStatusMappingReason == "" {
			return Finding{}, ErrInvalid
		}
	} else if input.Status == findingstatus.RiskAccepted {
		return Finding{}, ErrConflict
	}
	if input.Status == findingstatus.Duplicate || input.DuplicateOfFindingID != "" {
		return Finding{}, ErrConflict
	}
	if err := s.validateReferences(ctx, input.AssigneeOrganizationID, input.AssetID, input.BusinessSystemID); err != nil {
		return Finding{}, err
	}
	if input.TestBatchID != "" {
		if err := s.validateFindingBatchScope(ctx, input.TestBatchID, input.AssigneeOrganizationID, input.AssetID); err != nil {
			return Finding{}, err
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, fmt.Errorf("begin create finding: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, _, err := s.createFindingInTx(ctx, tx, principal, input, requestID, ipAddress)
	if err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit create finding: %w", err)
	}
	return s.getFinding(ctx, id)
}

func (s *Service) createFindingInTx(ctx context.Context, tx pgx.Tx, principal auth.Principal, input FindingInput, requestID, ipAddress string) (string, string, error) {
	if input.PendingAssetDraftID != "" {
		if input.Source != "import" || input.AssetID != "" || !validUUID(input.PendingAssetDraftID) {
			return "", "", ErrInvalid
		}
		var draftMatches bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM inventory_asset_drafts
			WHERE id=$1::uuid AND organization_id=$2::uuid AND operation='create'
			  AND draft_status IN ('editing','submitted','returned')
		)`, input.PendingAssetDraftID, input.AssigneeOrganizationID).Scan(&draftMatches); err != nil {
			return "", "", fmt.Errorf("validate finding pending asset draft in transaction: %w", err)
		}
		if !draftMatches {
			return "", "", ErrConflict
		}
	}
	code, err := newCode("F")
	if err != nil {
		return "", "", fmt.Errorf("generate finding code: %w", err)
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO security_findings (
			finding_code, title, severity, status, assignee_organization_id, asset_id, pending_asset_draft_id, business_system_id, test_batch_id,
			vulnerability_type, description, evidence, remediation_advice, source, discovered_at, created_by, updated_by,
			import_status_raw, import_status_normalized, import_status_mapping_reason, duplicate_of_finding_id
		) VALUES ($1, $2, $3, $4, $5::uuid, NULLIF($6, '')::uuid, NULLIF($16, '')::uuid, NULLIF($7, '')::uuid, NULLIF($8, '')::uuid,
		          NULLIF($9, ''), $10, NULLIF($11, ''), NULLIF($12, ''), $13, $14, $15::uuid, $15::uuid,
		          NULLIF($17, ''), NULLIF($18, ''), NULLIF($19, ''), NULLIF($20, '')::uuid)
		RETURNING id::text`, code, input.Title, input.Severity, input.Status, input.AssigneeOrganizationID,
		input.AssetID, input.BusinessSystemID, input.TestBatchID, input.VulnerabilityType, input.Description, input.Evidence,
		input.RemediationAdvice, input.Source, input.DiscoveredAt, principal.UserID, input.PendingAssetDraftID,
		input.ImportStatusRaw, input.ImportStatusNormalized, input.ImportStatusMappingReason, input.DuplicateOfFindingID).Scan(&id); err != nil {
		return "", "", fmt.Errorf("insert security finding: %w", err)
	}
	if err := writeAudit(ctx, tx, principal, input.AssigneeOrganizationID, "security_finding.create", "security_finding", id,
		"登记安全发现 "+code, map[string]any{"severity": input.Severity, "asset_id": input.AssetID, "business_system_id": input.BusinessSystemID, "test_batch_id": input.TestBatchID,
			"import_status_raw": input.ImportStatusRaw, "import_status_normalized": input.ImportStatusNormalized, "import_status_mapping_reason": input.ImportStatusMappingReason}, requestID, ipAddress); err != nil {
		return "", "", err
	}
	eventType := notification.EventFindingCreatedOnSystem
	requiredPermission := "system.manage"
	targetUserID := ""
	if input.AssetID != "" {
		eventType = notification.EventFindingCreatedOnAsset
		requiredPermission = "asset.manage"
		// The primary organization row carries the accountable owner, but nothing in
		// the schema guarantees that every asset has one: the uniqueness rule is on
		// (asset_id, organization_id, relationship_type), not on "exactly one primary".
		// An asset whose primary row is missing must still produce a finding and a
		// notification, so a missing row means "no targeted owner" rather than a
		// failed insert. Reading the row as an optional lookup also keeps a multi-row
		// primary configuration from aborting the registration.
		var ownerUserID *string
		if err := tx.QueryRow(ctx, `
			SELECT owner_user_id::text
			FROM asset_organizations
			WHERE asset_id = $1::uuid AND is_primary
			ORDER BY created_at
			LIMIT 1
		`, input.AssetID).Scan(&ownerUserID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", "", fmt.Errorf("load finding asset owner: %w", err)
		} else if ownerUserID != nil {
			targetUserID = *ownerUserID
		}
	} else if input.PendingAssetDraftID != "" {
		// An unpublished network asset is still an asset, not a business
		// system. Asset-only operators must receive its findings as well.
		eventType = notification.EventFindingCreatedOnAsset
		requiredPermission = "asset.manage"
		var ownerUserID *string
		if err := tx.QueryRow(ctx, `SELECT owner_user_id::text FROM inventory_asset_drafts
			WHERE id=$1::uuid AND organization_id=$2::uuid`, input.PendingAssetDraftID, input.AssigneeOrganizationID).Scan(&ownerUserID); err != nil {
			return "", "", fmt.Errorf("load finding draft asset owner: %w", err)
		}
		if ownerUserID != nil {
			targetUserID = *ownerUserID
		}
	}
	level := "info"
	if input.Severity == "medium" {
		level = "warning"
	} else if input.Severity == "high" || input.Severity == "critical" {
		level = "danger"
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: eventType, Category: notification.CategoryFindingReceived,
		TargetOrganizationID: input.AssigneeOrganizationID, TargetUserID: targetUserID, ActorUserID: principal.UserID, RequiredPermission: requiredPermission,
		ResourceType: "security_finding", ResourceID: id, ResourceLabel: code + "《" + input.Title + "》", BatchKey: requestID, Level: level,
		Details: map[string]any{"severity": input.Severity, "asset_id": input.AssetID, "pending_asset_draft_id": input.PendingAssetDraftID, "business_system_id": input.BusinessSystemID, "test_batch_id": input.TestBatchID},
	}); err != nil {
		return "", "", err
	}
	return id, code, nil
}

func (s *Service) UpdateFinding(ctx context.Context, principal auth.Principal, id string, input FindingInput, requestID, ipAddress string) (Finding, error) {
	if err := s.guardPersonalFinding(ctx, id); err != nil {
		return Finding{}, err
	}
	if !principal.CanManageSecurityFindings() {
		return Finding{}, ErrForbidden
	}
	if !validUUID(id) {
		return Finding{}, ErrInvalid
	}
	// UpdateFinding is the only read-modify-write entry for a finding row, so a
	// missing optimistic-lock token is a client bug, not a convenience.
	if input.ExpectedUpdatedAt == nil {
		return Finding{}, ErrVersionRequired
	}
	normalizeFindingInput(&input)
	if input.PendingAssetDraftID != "" {
		return Finding{}, ErrInvalid
	}
	if !validFindingInput(input) {
		return Finding{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, fmt.Errorf("begin update finding: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Match remediation's lock order: orchestration lock, then finding row.
	// Work-order creation also locks the finding before inserting its links.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return Finding{}, err
	}
	var currentOrganizationID, currentStatus string
	var updatedAt time.Time
	if err := tx.QueryRow(ctx, `
		SELECT assignee_organization_id::text, status, updated_at
		FROM security_findings WHERE id=$1::uuid FOR UPDATE`, id).
		Scan(&currentOrganizationID, &currentStatus, &updatedAt); errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	} else if err != nil {
		return Finding{}, fmt.Errorf("lock finding update constraints: %w", err)
	}
	if err := checkFindingVersion(id, input.ExpectedUpdatedAt, updatedAt); err != nil {
		return Finding{}, err
	}
	// Use a new statement AFTER the row lock: a creator we waited for may have
	// committed links without changing the finding's updated_at.
	var linked, activelyLinked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM security_work_order_findings WHERE finding_id=$1::uuid),
		       EXISTS (SELECT 1 FROM security_work_order_findings link
		               JOIN security_work_orders wo ON wo.id=link.work_order_id
		               WHERE link.finding_id=$1::uuid AND wo.status NOT IN ('closed','cancelled'))`, id).
		Scan(&linked, &activelyLinked); err != nil {
		return Finding{}, fmt.Errorf("load finding work order constraints: %w", err)
	}
	if linked && input.AssigneeOrganizationID != currentOrganizationID {
		return Finding{}, ErrConflict
	}
	if activelyLinked && input.Status != currentStatus {
		return Finding{}, ErrConflict
	}
	// Risk acceptance is a controlled workflow and must not be bypassed by the
	// generic edit endpoint. Metadata can still be edited while risk_accepted
	// remains the current status.
	if (currentStatus == "risk_accepted" && input.Status != "risk_accepted") ||
		(currentStatus != "risk_accepted" && input.Status == "risk_accepted") {
		return Finding{}, ErrConflict
	}
	if input.Status != currentStatus && !findingstatus.CanTransition(currentStatus, input.Status) {
		return Finding{}, ErrState
	}
	if input.Status == findingstatus.Duplicate || input.DuplicateOfFindingID != "" {
		return Finding{}, ErrConflict
	}
	if err := validateFindingReferences(ctx, tx, input.AssigneeOrganizationID, input.AssetID, input.BusinessSystemID); err != nil {
		return Finding{}, err
	}
	if input.TestBatchID != "" {
		if err := validateFindingBatchScope(ctx, tx, input.TestBatchID, input.AssigneeOrganizationID, input.AssetID); err != nil {
			return Finding{}, err
		}
	}
	var personalLinked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id WHERE t.finding_id=$1::uuid AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled'))`, id).Scan(&personalLinked); err != nil {
		return Finding{}, err
	}
	if personalLinked {
		return Finding{}, ErrConflict
	}

	command, err := tx.Exec(ctx, `
		UPDATE security_findings SET title = $2, severity = $3, status = $4,
		       assignee_organization_id = $5::uuid, asset_id = NULLIF($6, '')::uuid,
		       business_system_id = NULLIF($7, '')::uuid, test_batch_id = NULLIF($8, '')::uuid, vulnerability_type = NULLIF($9, ''),
		       description = $10, evidence = NULLIF($11, ''), remediation_advice = NULLIF($12, ''),
		       source = $13, discovered_at = $14, updated_by = $15::uuid, updated_at = now()
		WHERE id::text = $1
		  AND updated_at = $16::timestamptz`, id, input.Title, input.Severity, input.Status, input.AssigneeOrganizationID,
		input.AssetID, input.BusinessSystemID, input.TestBatchID, input.VulnerabilityType, input.Description, input.Evidence,
		input.RemediationAdvice, input.Source, input.DiscoveredAt, principal.UserID, input.ExpectedUpdatedAt)
	if err != nil {
		return Finding{}, fmt.Errorf("update security finding: %w", err)
	}
	if command.RowsAffected() == 0 {
		// Distinguish "row vanished" from "row moved on". A stale lock value is a
		// recoverable conflict the caller retries with a fresh read; a missing row
		// is not something a retry can fix.
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM security_findings WHERE id::text = $1)`, id).Scan(&exists); err != nil {
			return Finding{}, fmt.Errorf("check finding existence after stale edit: %w", err)
		}
		if exists {
			return Finding{}, ErrEditConflict
		}
		return Finding{}, ErrNotFound
	}
	if err := writeAudit(ctx, tx, principal, input.AssigneeOrganizationID, "security_finding.update", "security_finding", id,
		"更新安全发现", map[string]any{"severity": input.Severity, "status": input.Status}, requestID, ipAddress); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit update finding: %w", err)
	}
	return s.getFinding(ctx, id)
}

// GetFinding returns one finding when it is visible to the current principal.
// Group security operators can inspect all findings. Organization users can inspect
// pre-work-order findings when the role covering that organization grants management
// of the referenced asset/system; issued work orders retain their existing visibility.
func (s *Service) GetFinding(ctx context.Context, principal auth.Principal, id string) (Finding, error) {
	if !validUUID(id) {
		return Finding{}, ErrInvalid
	}
	if err := s.expireDueRiskAcceptances(ctx); err != nil {
		return Finding{}, err
	}
	item, err := s.getFinding(ctx, id)
	if err != nil {
		return Finding{}, err
	}
	if principal.CanManageSecurityFindings() {
		return item, nil
	}
	requiredPermission := "system.manage"
	if item.AssetID != "" {
		requiredPermission = "asset.manage"
	}
	canManageLedger, err := access.CanOperateOrganization(ctx, s.pool, principal, item.AssigneeOrganizationID, requiredPermission)
	if err != nil {
		return Finding{}, err
	}
	if canManageLedger {
		return item, nil
	}
	allowed, err := access.CanAccessOrganization(ctx, s.pool, principal, item.AssigneeOrganizationID)
	if err != nil {
		return Finding{}, err
	}
	if !allowed {
		return Finding{}, ErrForbidden
	}
	var visible bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM security_work_order_findings link
			JOIN security_work_orders wo ON wo.id = link.work_order_id
			WHERE link.finding_id = $1::uuid AND wo.issued_at IS NOT NULL
		)`, id).Scan(&visible); err != nil {
		return Finding{}, fmt.Errorf("check security finding visibility: %w", err)
	}
	if !visible {
		return Finding{}, ErrNotFound
	}
	return item, nil
}

// LinkDuplicateFinding marks id as a duplicate of an existing master finding.
// It is a dedicated action because a duplicate needs an explicit, auditable
// relationship; generic finding edits and external imports cannot invent one.
func (s *Service) LinkDuplicateFinding(ctx context.Context, principal auth.Principal, id string, input DuplicateFindingInput, requestID, ipAddress string) (Finding, error) {
	if !principal.CanManageSecurityFindings() {
		return Finding{}, ErrForbidden
	}
	input.MasterFindingID = strings.TrimSpace(input.MasterFindingID)
	input.Reason = strings.TrimSpace(input.Reason)
	if !validUUID(id) || !validUUID(input.MasterFindingID) || id == input.MasterFindingID || input.Reason == "" || textlength.Count(input.Reason) > 5000 {
		return Finding{}, ErrInvalid
	}
	if err := s.guardPersonalFinding(ctx, id); err != nil {
		return Finding{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, fmt.Errorf("begin link duplicate finding: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return Finding{}, err
	}

	var sourceOrganization, sourceStatus, existingMaster string
	var currentVersion time.Time
	if err := tx.QueryRow(ctx, `
		SELECT assignee_organization_id::text, status, COALESCE(duplicate_of_finding_id::text, ''), updated_at
		FROM security_findings WHERE id=$1::uuid FOR UPDATE`, id).
		Scan(&sourceOrganization, &sourceStatus, &existingMaster, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	} else if err != nil {
		return Finding{}, fmt.Errorf("load duplicate finding: %w", err)
	}
	if err := checkFindingVersion(id, input.ExpectedUpdatedAt, currentVersion); err != nil {
		return Finding{}, err
	}
	if existingMaster != "" || !canLinkDuplicateFromStatus(sourceStatus) {
		return Finding{}, ErrState
	}
	var masterStatus, masterDuplicate string
	if err := tx.QueryRow(ctx, `
		SELECT status, COALESCE(duplicate_of_finding_id::text, '')
		FROM security_findings WHERE id=$1::uuid FOR UPDATE`, input.MasterFindingID).
		Scan(&masterStatus, &masterDuplicate); errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	} else if err != nil {
		return Finding{}, fmt.Errorf("load duplicate master finding: %w", err)
	}
	// Do not create chains. A master must itself remain the canonical finding.
	if masterDuplicate != "" || masterStatus == findingstatus.Duplicate || masterStatus == findingstatus.FalsePositive || masterStatus == findingstatus.Ignored {
		return Finding{}, ErrState
	}
	var activeTask bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM remediation_tasks t
			JOIN security_work_orders w ON w.id=t.work_order_id
			WHERE t.finding_id=$1::uuid AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled')
		)`, id).Scan(&activeTask); err != nil {
		return Finding{}, fmt.Errorf("check duplicate finding tasks: %w", err)
	}
	if activeTask {
		return Finding{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_findings
		SET status='duplicate', duplicate_of_finding_id=$2::uuid, updated_at=now(), updated_by=$3::uuid
		WHERE id=$1::uuid`, id, input.MasterFindingID, principal.UserID); err != nil {
		return Finding{}, fmt.Errorf("link duplicate finding: %w", err)
	}
	if err := writeAudit(ctx, tx, principal, sourceOrganization, "security_finding.link_duplicate", "security_finding", id,
		"将安全发现标记为重复", map[string]any{
			"master_finding_id": input.MasterFindingID,
			"previous_status":   sourceStatus,
			"new_status":        findingstatus.Duplicate,
			"reason":            input.Reason,
		}, requestID, ipAddress); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit link duplicate finding: %w", err)
	}
	return s.getFinding(ctx, id)
}

func (s *Service) AcceptRisk(ctx context.Context, principal auth.Principal, id string, input RiskAcceptanceInput, requestID, ipAddress string) (Finding, error) {
	if err := s.guardPersonalFinding(ctx, id); err != nil {
		return Finding{}, err
	}
	if !principal.CanManageSecurityFindings() {
		return Finding{}, ErrForbidden
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if !validUUID(id) || !validRiskAcceptanceInput(input) {
		return Finding{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, fmt.Errorf("begin accept finding risk: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return Finding{}, err
	}
	var personalLinked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id WHERE t.finding_id=$1::uuid AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled'))`, id).Scan(&personalLinked); err != nil {
		return Finding{}, err
	}
	if personalLinked {
		return Finding{}, ErrConflict
	}

	var organizationID, previousStatus, previousFromStatus string
	var activelyLinked bool
	var currentVersion time.Time
	if err := tx.QueryRow(ctx, `
		SELECT f.assignee_organization_id::text, f.status, COALESCE(f.risk_accepted_from_status, ''),
		       EXISTS (
			SELECT 1 FROM security_work_order_findings link
			JOIN security_work_orders wo ON wo.id = link.work_order_id
			WHERE link.finding_id = f.id AND wo.status NOT IN ('closed', 'cancelled')
		       ),
		       f.updated_at
		FROM security_findings f WHERE f.id::text = $1 FOR UPDATE`, id).Scan(&organizationID, &previousStatus, &previousFromStatus, &activelyLinked, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	} else if err != nil {
		return Finding{}, fmt.Errorf("load finding risk acceptance: %w", err)
	}
	if err := checkFindingVersion(id, input.ExpectedUpdatedAt, currentVersion); err != nil {
		return Finding{}, err
	}
	if previousStatus != "pending" && previousStatus != "in_progress" && previousStatus != "confirmed" && previousStatus != "resolved" && previousStatus != "fixed" && previousStatus != "risk_accepted" {
		return Finding{}, ErrState
	}
	if activelyLinked && previousStatus != "risk_accepted" {
		return Finding{}, ErrConflict
	}
	fromStatus := previousStatus
	if previousStatus == "risk_accepted" && previousFromStatus != "" {
		fromStatus = previousFromStatus
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_findings
		SET status = 'risk_accepted', risk_accepted_at = now(), risk_accepted_until = $2,
		    risk_acceptance_expired_at = NULL, risk_acceptance_reason = $3, risk_accepted_by = $4::uuid, risk_accepted_from_status = $5,
		    risk_reviewed_at = NULL, risk_reviewed_by = NULL, risk_review_comment = NULL,
		    updated_by = $4::uuid, updated_at = now()
		WHERE id::text = $1`, id, input.AcceptedUntil, input.Reason, principal.UserID, fromStatus); err != nil {
		return Finding{}, fmt.Errorf("accept finding risk: %w", err)
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "security_finding.accept_risk", "security_finding", id,
		"接受安全发现风险", map[string]any{
			"finding_id": id, "previous_status": previousStatus, "new_status": "risk_accepted",
			"reason": input.Reason, "risk_accepted_until": input.AcceptedUntil,
		}, requestID, ipAddress); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit accept finding risk: %w", err)
	}
	return s.getFinding(ctx, id)
}

func (s *Service) RevokeRisk(ctx context.Context, principal auth.Principal, id string, input RiskRevokeInput, requestID, ipAddress string) (Finding, error) {
	if err := s.guardPersonalFinding(ctx, id); err != nil {
		return Finding{}, err
	}
	if !principal.CanManageSecurityFindings() {
		return Finding{}, ErrForbidden
	}
	if !validUUID(id) {
		return Finding{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, fmt.Errorf("begin revoke finding risk: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return Finding{}, err
	}
	var personalLinked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id WHERE t.finding_id=$1::uuid AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled'))`, id).Scan(&personalLinked); err != nil {
		return Finding{}, err
	}
	if personalLinked {
		return Finding{}, ErrConflict
	}

	var organizationID, previousStatus, fromStatus string
	var acceptedUntil *time.Time
	var currentVersion time.Time
	if err := tx.QueryRow(ctx, `
		SELECT assignee_organization_id::text, status, COALESCE(risk_accepted_from_status, ''), risk_accepted_until, updated_at
		FROM security_findings WHERE id::text = $1 FOR UPDATE`, id).Scan(&organizationID, &previousStatus, &fromStatus, &acceptedUntil, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	} else if err != nil {
		return Finding{}, fmt.Errorf("load finding risk revocation: %w", err)
	}
	if err := checkFindingVersion(id, input.ExpectedUpdatedAt, currentVersion); err != nil {
		return Finding{}, err
	}
	if previousStatus != "risk_accepted" {
		return Finding{}, ErrState
	}
	if fromStatus != "pending" && fromStatus != "in_progress" && fromStatus != "confirmed" && fromStatus != "resolved" && fromStatus != "fixed" {
		fromStatus = "pending"
	}
	if _, err := tx.Exec(ctx, `UPDATE security_findings SET status = $2, updated_by = $3::uuid, updated_at = now() WHERE id::text = $1`, id, fromStatus, principal.UserID); err != nil {
		return Finding{}, fmt.Errorf("revoke finding risk: %w", err)
	}
	details := map[string]any{"finding_id": id, "previous_status": previousStatus, "new_status": fromStatus}
	if acceptedUntil != nil {
		details["risk_accepted_until"] = acceptedUntil
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "security_finding.revoke_risk", "security_finding", id,
		"撤销安全发现风险接受", details, requestID, ipAddress); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit revoke finding risk: %w", err)
	}
	return s.getFinding(ctx, id)
}

func (s *Service) ReviewRisk(ctx context.Context, principal auth.Principal, id string, input RiskReviewInput, requestID, ipAddress string) (Finding, error) {
	if err := s.guardPersonalFinding(ctx, id); err != nil {
		return Finding{}, err
	}
	if !principal.CanManageSecurityFindings() {
		return Finding{}, ErrForbidden
	}
	input.Comment = strings.TrimSpace(input.Comment)
	if !validUUID(id) || input.Comment == "" || textlength.Count(input.Comment) > 5000 {
		return Finding{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Finding{}, fmt.Errorf("begin review finding risk: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return Finding{}, err
	}
	var personalLinked bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id WHERE t.finding_id=$1::uuid AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled'))`, id).Scan(&personalLinked); err != nil {
		return Finding{}, err
	}
	if personalLinked {
		return Finding{}, ErrConflict
	}

	var organizationID, previousStatus string
	var currentUntil *time.Time
	var currentVersion time.Time
	if err := tx.QueryRow(ctx, `
		SELECT assignee_organization_id::text, status, risk_accepted_until, updated_at
		FROM security_findings WHERE id::text = $1 FOR UPDATE`, id).Scan(&organizationID, &previousStatus, &currentUntil, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	} else if err != nil {
		return Finding{}, fmt.Errorf("load finding risk review: %w", err)
	}
	if err := checkFindingVersion(id, input.ExpectedUpdatedAt, currentVersion); err != nil {
		return Finding{}, err
	}
	if previousStatus != "risk_accepted" {
		return Finding{}, ErrState
	}
	effectiveUntil := currentUntil
	if input.AcceptedUntil != nil {
		if !input.AcceptedUntil.After(time.Now()) {
			return Finding{}, ErrInvalid
		}
		effectiveUntil = input.AcceptedUntil
	}
	if effectiveUntil == nil || !effectiveUntil.After(time.Now()) {
		return Finding{}, ErrInvalid
	}
	if _, err := tx.Exec(ctx, `
		UPDATE security_findings SET risk_accepted_until = $2, risk_reviewed_at = now(),
		    risk_reviewed_by = $3::uuid, risk_review_comment = $4,
		    updated_by = $3::uuid, updated_at = now()
		WHERE id::text = $1`, id, effectiveUntil, principal.UserID, input.Comment); err != nil {
		return Finding{}, fmt.Errorf("review finding risk: %w", err)
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "security_finding.review_risk", "security_finding", id,
		"复核安全发现风险接受", map[string]any{
			"finding_id": id, "previous_status": previousStatus, "new_status": previousStatus,
			"review_comment": input.Comment, "risk_accepted_until": effectiveUntil,
		}, requestID, ipAddress); err != nil {
		return Finding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Finding{}, fmt.Errorf("commit review finding risk: %w", err)
	}
	return s.getFinding(ctx, id)
}

func (s *Service) ListWorkOrders(ctx context.Context, principal auth.Principal, organizationID, status string) ([]WorkOrder, error) {
	// Kept for internal compatibility. HTTP collection endpoints must use SearchWorkOrders.
	result, err := s.SearchWorkOrders(ctx, principal, organizationID, status, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

// SearchWorkOrders bounds both the main query and related finding hydration to a page.
func (s *Service) SearchWorkOrders(ctx context.Context, principal auth.Principal, organizationID, status string, page pagination.Query) (pagination.Result[WorkOrder], error) {
	return s.SearchWorkOrdersWithFilter(ctx, principal, WorkOrderSearchFilter{
		OrganizationID: organizationID,
		Status:         status,
		Page:           page,
	})
}

// SearchWorkOrdersWithFilter applies all filters and pagination in the database before related findings are hydrated.
func (s *Service) SearchWorkOrdersWithFilter(ctx context.Context, principal auth.Principal, filter WorkOrderSearchFilter) (pagination.Result[WorkOrder], error) {
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.OrganizationID != "" {
		if !validUUID(filter.OrganizationID) {
			return pagination.Result[WorkOrder]{}, ErrInvalid
		}
		allowed, err := s.canViewWorkOrder(ctx, principal, filter.OrganizationID)
		if err != nil {
			return pagination.Result[WorkOrder]{}, err
		}
		if !allowed {
			return pagination.Result[WorkOrder]{}, ErrForbidden
		}
	}
	if filter.Status != "" && !validWorkOrderStatus(filter.Status) {
		return pagination.Result[WorkOrder]{}, ErrInvalid
	}
	selectWithTotal := strings.Replace(workOrderSelect, "SELECT ", "SELECT count(*) OVER (), ", 1)
	rows, err := s.pool.Query(ctx, accessibleCTE+selectWithTotal+`
		WHERE (
		        $2::boolean
		        OR (wo.assignee_organization_id IN (SELECT id FROM work_order_accessible) AND wo.status <> 'draft')
		      )
		  AND ($3::text = '' OR wo.assignee_organization_id::text = $3)
		  AND ($4::text = '' OR wo.status = $4)
		  AND (NOT $5::boolean OR (wo.due_at < now() AND wo.status NOT IN ('closed', 'cancelled')))
		ORDER BY CASE WHEN wo.status IN ('closed', 'cancelled') THEN 1 ELSE 0 END,
		         wo.due_at, wo.created_at DESC
		LIMIT $6 OFFSET $7`, principal.UserID, principal.CanManageSecurityWorkOrders(), filter.OrganizationID, filter.Status, filter.OverdueOnly, filter.Page.PageSize, filter.Page.Offset())
	if err != nil {
		return pagination.Result[WorkOrder]{}, fmt.Errorf("search security work orders: %w", err)
	}
	defer rows.Close()
	items := make([]WorkOrder, 0, filter.Page.PageSize)
	total := 0
	for rows.Next() {
		var item WorkOrder
		if err := scanWorkOrderWithTotal(rows, &item, &total); err != nil {
			return pagination.Result[WorkOrder]{}, fmt.Errorf("scan security work order: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[WorkOrder]{}, fmt.Errorf("iterate security work orders: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), filter.Page) {
		if err := s.pool.QueryRow(ctx, accessibleCTE+`
            SELECT count(*) FROM security_work_orders wo
            WHERE (
                    $2::boolean
                    OR (wo.assignee_organization_id IN (SELECT id FROM work_order_accessible) AND wo.status <> 'draft')
                  )
              AND ($3::text = '' OR wo.assignee_organization_id::text = $3)
              AND ($4::text = '' OR wo.status = $4)
              AND (NOT $5::boolean OR (wo.due_at < now() AND wo.status NOT IN ('closed', 'cancelled')))
        `, principal.UserID, principal.CanManageSecurityWorkOrders(), filter.OrganizationID, filter.Status, filter.OverdueOnly).Scan(&total); err != nil {
			return pagination.Result[WorkOrder]{}, fmt.Errorf("count security work orders: %w", err)
		}
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

func (s *Service) GetWorkOrder(ctx context.Context, principal auth.Principal, id string) (WorkOrder, error) {
	if !validUUID(id) {
		return WorkOrder{}, ErrInvalid
	}
	item, err := loadWorkOrder(ctx, s.pool, id)
	if err != nil {
		return WorkOrder{}, err
	}
	allowed, err := s.canViewWorkOrder(ctx, principal, item.AssigneeOrganizationID)
	if err != nil {
		return WorkOrder{}, err
	}
	if !allowed || (!principal.CanManageSecurityWorkOrders() && item.Status == "draft") {
		return WorkOrder{}, ErrForbidden
	}
	return item, nil
}

// SearchWorkOrderFindings loads linked findings only when a detail view asks
// for them, avoiding one unbounded child query per row in the main work-order list.
func (s *Service) SearchWorkOrderFindings(ctx context.Context, principal auth.Principal, workOrderID string, page pagination.Query) (pagination.Result[FindingSummary], error) {
	if _, err := s.GetWorkOrder(ctx, principal, workOrderID); err != nil {
		return pagination.Result[FindingSummary]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_work_order_findings WHERE work_order_id::text = $1`, workOrderID).Scan(&total); err != nil {
		return pagination.Result[FindingSummary]{}, fmt.Errorf("count work order findings: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT f.id::text, f.finding_code, f.title, f.severity, f.status,
		       COALESCE(a.id::text, ''), COALESCE(a.asset_value, ''), COALESCE(a.asset_name, ''),
		       COALESCE(f.pending_asset_draft_id::text, ''), (a.id IS NOT NULL AND a.deleted_at IS NOT NULL),
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_name, '')
		FROM security_work_order_findings link
		JOIN security_findings f ON f.id = link.finding_id
		LEFT JOIN assets a ON a.id = f.asset_id
		LEFT JOIN business_systems bs ON bs.id = f.business_system_id
		WHERE link.work_order_id::text = $1
		ORDER BY CASE f.severity WHEN 'critical' THEN 1 WHEN 'high' THEN 2 WHEN 'medium' THEN 3 WHEN 'low' THEN 4 ELSE 5 END,
		         f.finding_code
		LIMIT $2 OFFSET $3`, workOrderID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[FindingSummary]{}, fmt.Errorf("list work order findings: %w", err)
	}
	defer rows.Close()
	items := make([]FindingSummary, 0, page.PageSize)
	for rows.Next() {
		var item FindingSummary
		if err := rows.Scan(&item.ID, &item.Code, &item.Title, &item.Severity, &item.Status,
			&item.AssetID, &item.AssetValue, &item.AssetName, &item.PendingAssetDraftID, &item.AssetDeleted, &item.BusinessSystemID, &item.BusinessSystemName); err != nil {
			return pagination.Result[FindingSummary]{}, fmt.Errorf("scan work order finding: %w", err)
		}
		item.AssetLinkState = findingAssetLinkState(item.AssetID, item.PendingAssetDraftID, item.AssetDeleted)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[FindingSummary]{}, fmt.Errorf("iterate work order findings: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

// SearchWorkOrderEvents returns the newest workflow events first.
func (s *Service) SearchWorkOrderEvents(ctx context.Context, principal auth.Principal, workOrderID string, page pagination.Query) (pagination.Result[Event], error) {
	if _, err := s.GetWorkOrder(ctx, principal, workOrderID); err != nil {
		return pagination.Result[Event]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_work_order_events WHERE work_order_id::text = $1`, workOrderID).Scan(&total); err != nil {
		return pagination.Result[Event]{}, fmt.Errorf("count work order events: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.action, COALESCE(e.from_status, ''), COALESCE(e.to_status, ''), COALESCE(e.comment, ''), e.details,
		       COALESCE(e.actor_user_id::text, ''), COALESCE(u.display_name, ''),
		       COALESCE(e.actor_organization_id::text, ''), COALESCE(o.organization_name, ''), e.created_at
		FROM security_work_order_events e
		LEFT JOIN users u ON u.id = e.actor_user_id
		LEFT JOIN organizations o ON o.id = e.actor_organization_id
		WHERE e.work_order_id::text = $1
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $2 OFFSET $3`, workOrderID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Event]{}, fmt.Errorf("list work order events: %w", err)
	}
	defer rows.Close()
	items := make([]Event, 0, page.PageSize)
	for rows.Next() {
		var item Event
		if err := rows.Scan(&item.ID, &item.Action, &item.FromStatus, &item.ToStatus, &item.Comment,
			&item.Details, &item.ActorUserID, &item.ActorName, &item.ActorOrganizationID,
			&item.ActorOrganizationName, &item.CreatedAt); err != nil {
			return pagination.Result[Event]{}, fmt.Errorf("scan work order event: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Event]{}, fmt.Errorf("iterate work order events: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) CreateWorkOrder(ctx context.Context, principal auth.Principal, input WorkOrderInput, requestID, ipAddress string) (WorkOrder, error) {
	if !principal.CanManageSecurityWorkOrders() {
		return WorkOrder{}, ErrForbidden
	}
	normalizeWorkOrderInput(&input)
	if input.ExecutionMode == "" {
		input.ExecutionMode = "organization"
	}
	if input.ExecutionMode != "organization" && input.ExecutionMode != "individual" {
		return WorkOrder{}, ErrInvalid
	}
	if !validWorkOrderInput(input) {
		return WorkOrder{}, ErrInvalid
	}
	var organizationStatus, organizationType string
	if err := s.pool.QueryRow(ctx, `SELECT status, organization_type FROM organizations WHERE id::text = $1 AND deleted_at IS NULL`, input.AssigneeOrganizationID).Scan(&organizationStatus, &organizationType); errors.Is(err, pgx.ErrNoRows) {
		return WorkOrder{}, ErrNotFound
	} else if err != nil {
		return WorkOrder{}, fmt.Errorf("load work order organization: %w", err)
	}
	if organizationStatus != "enabled" || organizationType == "group" {
		return WorkOrder{}, ErrInvalid
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkOrder{}, fmt.Errorf("begin create work order: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	severity, err := validateFindingsForWorkOrder(ctx, tx, input.AssigneeOrganizationID, input.FindingIDs)
	if err != nil {
		return WorkOrder{}, err
	}
	number, err := newCode("WO")
	if err != nil {
		return WorkOrder{}, fmt.Errorf("generate work order number: %w", err)
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO security_work_orders (work_order_no, title, assignee_organization_id, severity, description,
		                                  remediation_requirement, due_at, created_by, updated_by, execution_mode)
		VALUES ($1, $2, $3::uuid, $4, NULLIF($5, ''), $6, $7, $8::uuid, $8::uuid, $9)
		RETURNING id::text`, number, input.Title, input.AssigneeOrganizationID, severity, input.Description,
		input.RemediationRequirement, input.DueAt, principal.UserID, input.ExecutionMode).Scan(&id); err != nil {
		return WorkOrder{}, fmt.Errorf("insert security work order: %w", err)
	}
	for _, findingID := range input.FindingIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO security_work_order_findings (work_order_id, finding_id) VALUES ($1::uuid, $2::uuid)`, id, findingID); err != nil {
			return WorkOrder{}, fmt.Errorf("link work order finding: %w", err)
		}
	}
	if err := writeEvent(ctx, tx, principal, id, "create", "", "draft", "创建安全整改工单", map[string]any{"finding_count": len(input.FindingIDs)}); err != nil {
		return WorkOrder{}, err
	}
	if err := writeAudit(ctx, tx, principal, input.AssigneeOrganizationID, "security_work_order.create", "security_work_order", id,
		"创建安全整改工单 "+number, map[string]any{"status": "draft", "finding_count": len(input.FindingIDs), "due_at": input.DueAt}, requestID, ipAddress); err != nil {
		return WorkOrder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkOrder{}, fmt.Errorf("commit create work order: %w", err)
	}
	return s.GetWorkOrder(ctx, principal, id)
}

func (s *Service) Issue(ctx context.Context, principal auth.Principal, id string, input CommentInput, requestID, ipAddress string) (WorkOrder, error) {
	if !principal.CanManageSecurityWorkOrders() {
		return WorkOrder{}, ErrForbidden
	}
	input.Comment = strings.TrimSpace(input.Comment)
	if !validUUID(id) || textlength.Count(input.Comment) > 2000 {
		return WorkOrder{}, ErrInvalid
	}
	return s.transition(ctx, principal, id, []string{"draft"}, "issued", "issue", input.Comment, requestID, ipAddress, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE security_work_orders SET issued_at = now() WHERE id::text = $1`, id); err != nil {
			return fmt.Errorf("set work order issued time: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE security_work_order_findings l SET finding_status_before_issue=f.status
			FROM security_findings f
			WHERE l.work_order_id=$1::uuid AND l.finding_id=f.id AND f.status IN ('pending', 'confirmed', 'in_progress')`, id); err != nil {
			return fmt.Errorf("save finding status before issue: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE security_findings SET status='in_progress', updated_at=now(), updated_by=$2::uuid
			WHERE id IN (SELECT finding_id FROM security_work_order_findings WHERE work_order_id=$1::uuid)
			  AND status IN ('pending','confirmed')`, id, principal.UserID); err != nil {
			return fmt.Errorf("assign security findings: %w", err)
		}
		return nil
	})
}

func (s *Service) Accept(ctx context.Context, principal auth.Principal, id string, input CommentInput, requestID, ipAddress string) (WorkOrder, error) {
	input.Comment = strings.TrimSpace(input.Comment)
	if !validUUID(id) || textlength.Count(input.Comment) > 2000 {
		return WorkOrder{}, ErrInvalid
	}
	return s.transition(ctx, principal, id, []string{"issued"}, "in_progress", "accept", input.Comment, requestID, ipAddress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE security_work_orders SET accepted_at = COALESCE(accepted_at, now()) WHERE id::text = $1`, id)
		return err
	})
}

func (s *Service) Progress(ctx context.Context, principal auth.Principal, id string, input ProgressInput, requestID, ipAddress string) (WorkOrder, error) {
	input.Progress = strings.TrimSpace(input.Progress)
	input.Comment = strings.TrimSpace(input.Comment)
	if !validUUID(id) || input.Progress == "" || textlength.Count(input.Progress) > 5000 || textlength.Count(input.Comment) > 2000 {
		return WorkOrder{}, ErrInvalid
	}
	return s.transition(ctx, principal, id, []string{"in_progress", "returned"}, "in_progress", "progress", input.Comment, requestID, ipAddress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE security_work_orders SET remediation_progress = $2, return_reason = CASE WHEN status = 'returned' THEN NULL ELSE return_reason END WHERE id::text = $1`, id, input.Progress)
		return err
	})
}

func (s *Service) Submit(ctx context.Context, principal auth.Principal, id string, input SubmitInput, requestID, ipAddress string) (WorkOrder, error) {
	input.RemediationResult = strings.TrimSpace(input.RemediationResult)
	input.Comment = strings.TrimSpace(input.Comment)
	if !validUUID(id) || input.RemediationResult == "" || textlength.Count(input.RemediationResult) > 10000 || textlength.Count(input.Comment) > 2000 {
		return WorkOrder{}, ErrInvalid
	}
	return s.transition(ctx, principal, id, []string{"in_progress", "returned"}, "pending_verification", "submit", input.Comment, requestID, ipAddress, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE security_work_orders SET remediation_result = $2, submitted_at = now(), return_reason = NULL WHERE id::text = $1`, id, input.RemediationResult)
		return err
	})
}

func (s *Service) Review(ctx context.Context, principal auth.Principal, id string, input ReviewInput, requestID, ipAddress string) (WorkOrder, error) {
	if !principal.CanManageSecurityWorkOrders() {
		return WorkOrder{}, ErrForbidden
	}
	input.Decision = strings.TrimSpace(input.Decision)
	input.VerificationResult = strings.TrimSpace(input.VerificationResult)
	input.ReturnReason = strings.TrimSpace(input.ReturnReason)
	if !validUUID(id) || (input.Decision != "returned" && input.Decision != "closed") || textlength.Count(input.VerificationResult) > 10000 || textlength.Count(input.ReturnReason) > 2000 ||
		(input.Decision == "returned" && input.ReturnReason == "") || (input.Decision == "closed" && input.VerificationResult == "") {
		return WorkOrder{}, ErrInvalid
	}
	action := "return"
	comment := input.ReturnReason
	if input.Decision == "closed" {
		action = "close"
		comment = input.VerificationResult
	}
	return s.transition(ctx, principal, id, []string{"pending_verification"}, input.Decision, action, comment, requestID, ipAddress, func(ctx context.Context, tx pgx.Tx) error {
		if input.Decision == "returned" {
			_, err := tx.Exec(ctx, `UPDATE security_work_orders SET verification_result = NULLIF($2, ''), return_reason = $3, verified_at = now() WHERE id::text = $1`, id, input.VerificationResult, input.ReturnReason)
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE security_work_orders SET verification_result = $2, return_reason = NULL, verified_at = now(), closed_at = now() WHERE id::text = $1`, id, input.VerificationResult); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			UPDATE security_findings SET status = 'fixed', updated_at = now(), updated_by = $2::uuid
			WHERE id IN (SELECT finding_id FROM security_work_order_findings WHERE work_order_id::text = $1)
			  AND status = 'in_progress'`, id, principal.UserID)
		return err
	})
}

func (s *Service) Cancel(ctx context.Context, principal auth.Principal, id string, input CommentInput, requestID, ipAddress string) (WorkOrder, error) {
	if !principal.CanManageSecurityWorkOrders() {
		return WorkOrder{}, ErrForbidden
	}
	input.Comment = strings.TrimSpace(input.Comment)
	if !validUUID(id) || input.Comment == "" || textlength.Count(input.Comment) > 2000 {
		return WorkOrder{}, ErrInvalid
	}
	return s.transition(ctx, principal, id, []string{"draft", "issued", "in_progress", "pending_verification", "returned"}, "cancelled", "cancel", input.Comment, requestID, ipAddress, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE security_work_orders SET cancelled_at = now() WHERE id::text = $1`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			-- Legacy links have no recoverable pre-issue state. 'confirmed' is the
			-- conservative fallback: this finding is known to be real and remains
			-- actionable, unlike resetting it to an untriaged 'pending' state.
			UPDATE security_findings f SET status=COALESCE(l.finding_status_before_issue,'confirmed'), updated_at=now(), updated_by=$2::uuid
			FROM security_work_order_findings l
			WHERE l.work_order_id=$1::uuid AND l.finding_id=f.id AND f.status='in_progress'
			  -- Cancelling an unissued draft must not change finding state.
			  AND EXISTS (SELECT 1 FROM security_work_orders w WHERE w.id=l.work_order_id AND w.issued_at IS NOT NULL)
			  AND NOT EXISTS (SELECT 1 FROM security_work_order_findings other_link JOIN security_work_orders other ON other.id=other_link.work_order_id WHERE other_link.finding_id=f.id AND other.id<>$1::uuid AND other.status NOT IN ('closed','cancelled'))`, id, principal.UserID)
		return err
	})
}

// canViewWorkOrder separates organization visibility from the explicit
// security-work-order read permission. Group security roles retain global read
// access; organization roles must be scoped to the assignee organization.
func (s *Service) canViewWorkOrder(ctx context.Context, principal auth.Principal, organizationID string) (bool, error) {
	if principal.CanManageSecurityWorkOrders() {
		return true, nil
	}
	return access.CanOperateOrganization(ctx, s.pool, principal, organizationID, auth.PermissionSecurityWorkOrderView)
}

func (s *Service) authorizeWorkOrderAction(ctx context.Context, principal auth.Principal, organizationID, executionMode, action string) error {
	if principal.CanManageSecurityWorkOrders() {
		return nil
	}
	permission := ""
	switch action {
	case "accept", "progress", "submit":
		if executionMode == "individual" {
			// Accept is only an organization-side orchestration step for an
			// individual work order. The actual task remains separately scoped.
			permission = "remediation.assign"
		} else {
			permission = auth.PermissionSecurityWorkOrderExecute
		}
	case "issue":
		permission = auth.PermissionSecurityWorkOrderAssign
	case "return":
		permission = auth.PermissionSecurityWorkOrderReview
	case "close", "cancel":
		permission = auth.PermissionSecurityWorkOrderClose
	default:
		return ErrForbidden
	}
	allowed, err := access.CanOperateOrganization(ctx, s.pool, principal, organizationID, permission)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

type transitionExtra func(context.Context, pgx.Tx) error

// individualWorkOrderActionForbidden identifies legacy organization-level
// transitions that must never mutate an individual work-order container.
// Individual remediation is driven exclusively by remediation_tasks.
func individualWorkOrderActionForbidden(action string) bool {
	switch action {
	case "accept", "progress", "submit", "return", "close":
		return true
	default:
		return false
	}
}

func (s *Service) transition(ctx context.Context, principal auth.Principal, id string, fromStatuses []string, toStatus, action, comment, requestID, ipAddress string, extra transitionExtra) (WorkOrder, error) {
	if !validUUID(id) {
		return WorkOrder{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return WorkOrder{}, fmt.Errorf("begin work order transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('netassets-remediation'))`); err != nil {
		return WorkOrder{}, err
	}
	var fromStatus, organizationID, number, executionMode string
	if err := tx.QueryRow(ctx, `SELECT status, assignee_organization_id::text, work_order_no, execution_mode FROM security_work_orders WHERE id::text = $1 FOR UPDATE`, id).Scan(&fromStatus, &organizationID, &number, &executionMode); errors.Is(err, pgx.ErrNoRows) {
		return WorkOrder{}, ErrNotFound
	} else if err != nil {
		return WorkOrder{}, fmt.Errorf("lock security work order: %w", err)
	}
	// Authorize before applying any orchestration side effects. In particular,
	// cancelling an individual work order also cancels its remediation tasks;
	// an unauthorized caller must never reach those updates.
	if err := s.authorizeWorkOrderAction(ctx, principal, organizationID, executionMode, action); err != nil {
		return WorkOrder{}, err
	}
	if executionMode == "individual" {
		// Individual work orders are orchestration containers. Concrete
		// remediation must go through remediation_tasks, never the legacy
		// organization-level accept/progress/submit fields. Keep accept out of
		// this path too: otherwise the old /accept endpoint can move the
		// container forward without a task-level assignment or acceptance.
		if individualWorkOrderActionForbidden(action) {
			return WorkOrder{}, fmt.Errorf("%w: %w", ErrConflict, ErrIndividualWorkOrderAction)
		}
		if action == "cancel" {
			if _, err := tx.Exec(ctx, `UPDATE remediation_submissions SET lifecycle_status='cancelled' WHERE lifecycle_status='current' AND task_id IN (SELECT id FROM remediation_tasks WHERE work_order_id=$1::uuid)`, id); err != nil {
				return WorkOrder{}, err
			}
			if _, err := tx.Exec(ctx, `UPDATE remediation_tasks SET status='cancelled',version=version+1,updated_at=now() WHERE work_order_id=$1::uuid AND status NOT IN ('completed','cancelled')`, id); err != nil {
				return WorkOrder{}, err
			}
		}
	}
	if !contains(fromStatuses, fromStatus) {
		return WorkOrder{}, ErrState
	}
	if extra != nil {
		if err := extra(ctx, tx); err != nil {
			return WorkOrder{}, fmt.Errorf("apply work order transition details: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE security_work_orders SET status = $2, updated_by = $3::uuid, updated_at = now() WHERE id::text = $1`, id, toStatus, principal.UserID); err != nil {
		return WorkOrder{}, fmt.Errorf("update work order status: %w", err)
	}
	if err := writeEvent(ctx, tx, principal, id, action, fromStatus, toStatus, comment, nil); err != nil {
		return WorkOrder{}, err
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "security_work_order."+action, "security_work_order", id,
		workOrderActionSummary(action)+" "+number, map[string]any{"from_status": fromStatus, "to_status": toStatus, "comment": comment}, requestID, ipAddress); err != nil {
		return WorkOrder{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return WorkOrder{}, fmt.Errorf("commit work order transition: %w", err)
	}
	return s.GetWorkOrder(ctx, principal, id)
}

func (s *Service) getFinding(ctx context.Context, id string) (Finding, error) {
	var item Finding
	err := scanFinding(s.pool.QueryRow(ctx, `
		SELECT f.id::text, f.finding_code, f.title, f.severity, f.status, f.asset_link_status, f.remediation_status,
		       o.id::text, o.organization_code, o.organization_name,
		       COALESCE(a.id::text, ''), COALESCE(a.asset_value, ''), COALESCE(a.asset_name, ''),
		       COALESCE(f.pending_asset_draft_id::text, ''), (a.id IS NOT NULL AND a.deleted_at IS NOT NULL),
		       COALESCE(bs.id::text, ''), COALESCE(bs.system_code, ''), COALESCE(bs.system_name, ''),
		       COALESCE(tb.id::text, ''), COALESCE(tb.batch_code, ''), COALESCE(tb.batch_name, ''),
		       COALESCE(f.vulnerability_type, ''), f.description, COALESCE(f.evidence, ''),
		       COALESCE(f.remediation_advice, ''), f.source, f.discovered_at,
		       COALESCE(u.display_name, ''), f.created_at, f.updated_at,
		       f.risk_accepted_at, f.risk_accepted_until, COALESCE(f.risk_acceptance_reason, ''),
		       COALESCE(risk_acceptor.display_name, ''), COALESCE(f.risk_accepted_from_status, ''),
		       f.risk_reviewed_at, COALESCE(risk_reviewer.display_name, ''), COALESCE(f.risk_review_comment, ''),
		       (f.status = 'risk_accepted' AND f.risk_accepted_until IS NOT NULL AND f.risk_accepted_until < now()),
		       COALESCE(f.import_status_raw,''), COALESCE(f.import_status_normalized,''), COALESCE(f.import_status_mapping_reason,''),
		       COALESCE(f.duplicate_of_finding_id::text,'')
		FROM security_findings f
		JOIN organizations o ON o.id = f.assignee_organization_id
		LEFT JOIN assets a ON a.id = f.asset_id
		LEFT JOIN business_systems bs ON bs.id = f.business_system_id
		LEFT JOIN security_test_batches tb ON tb.id = f.test_batch_id
		LEFT JOIN users u ON u.id = f.created_by
		LEFT JOIN users risk_acceptor ON risk_acceptor.id = f.risk_accepted_by
		LEFT JOIN users risk_reviewer ON risk_reviewer.id = f.risk_reviewed_by
		WHERE f.id::text = $1`, id), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		return Finding{}, ErrNotFound
	}
	if err != nil {
		return Finding{}, fmt.Errorf("load security finding: %w", err)
	}
	return item, nil
}

func (s *Service) validateReferences(ctx context.Context, organizationID, assetID, systemID string) error {
	return validateFindingReferences(ctx, s.pool, organizationID, assetID, systemID)
}

// Use the caller's transaction when a finding row is locked. Acquiring another
// pool connection here could exhaust the pool while other edits wait on locks.
func validateFindingReferences(ctx context.Context, db queryer, organizationID, assetID, systemID string) error {
	var valid bool
	if err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM organizations WHERE id::text = $1 AND status = 'enabled' AND deleted_at IS NULL AND organization_type <> 'group')`, organizationID).Scan(&valid); err != nil {
		return fmt.Errorf("validate finding organization: %w", err)
	}
	if !valid {
		return ErrNotFound
	}
	if assetID != "" {
		if err := db.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1 FROM assets a JOIN asset_organizations ao ON ao.asset_id = a.id
				WHERE a.id::text = $1 AND ao.organization_id::text = $2 AND a.deleted_at IS NULL
			)`, assetID, organizationID).Scan(&valid); err != nil {
			return fmt.Errorf("validate finding asset: %w", err)
		}
		if !valid {
			return ErrNotFound
		}
	}
	if systemID != "" {
		if err := db.QueryRow(ctx, `
			SELECT EXISTS (SELECT 1 FROM business_systems WHERE id::text = $1 AND owner_organization_id::text = $2 AND deleted_at IS NULL)`, systemID, organizationID).Scan(&valid); err != nil {
			return fmt.Errorf("validate finding business system: %w", err)
		}
		if !valid {
			return ErrNotFound
		}
	}
	return nil
}

func (s *Service) validateFindingBatchScope(ctx context.Context, batchID, organizationID, assetID string) error {
	return validateFindingBatchScope(ctx, s.pool, batchID, organizationID, assetID)
}

func validateFindingBatchScope(ctx context.Context, db queryer, batchID, organizationID, assetID string) error {
	if assetID == "" {
		return ErrInvalid
	}
	var valid bool
	if err := db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM security_test_batches b
			JOIN security_test_scope_assets scope ON scope.batch_id = b.id
			WHERE b.id::text = $1 AND b.status IN ('authorized', 'in_progress', 'completed')
			  AND scope.organization_id::text = $2 AND scope.asset_id::text = $3
		)`, batchID, organizationID, assetID).Scan(&valid); err != nil {
		return fmt.Errorf("validate finding test batch scope: %w", err)
	}
	if !valid {
		return ErrNotFound
	}
	return nil
}

func validateFindingsForWorkOrder(ctx context.Context, tx pgx.Tx, organizationID string, findingIDs []string) (string, error) {
	// Lock requested findings in a stable order before checking active links.
	// Concurrent creators for overlapping findings serialize here; the waiter
	// then observes the first transaction's committed work-order link.
	rows, err := tx.Query(ctx, `
		SELECT id::text, severity
		FROM security_findings
		WHERE id = ANY($1::uuid[]) AND assignee_organization_id::text = $2
		  AND status IN ('pending', 'confirmed', 'in_progress')
		ORDER BY id
		FOR UPDATE`, findingIDs, organizationID)
	if err != nil {
		return "", fmt.Errorf("load work order findings: %w", err)
	}
	found := make(map[string]string, len(findingIDs))
	for rows.Next() {
		var id, severity string
		if err := rows.Scan(&id, &severity); err != nil {
			rows.Close()
			return "", fmt.Errorf("scan work order finding: %w", err)
		}
		found[id] = severity
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return "", fmt.Errorf("iterate work order findings: %w", err)
	}
	rows.Close()
	if len(found) != len(findingIDs) {
		return "", ErrNotFound
	}
	var activeLinked bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM security_work_order_findings link
			JOIN security_work_orders wo ON wo.id = link.work_order_id
			WHERE link.finding_id = ANY($1::uuid[]) AND wo.status NOT IN ('closed', 'cancelled')
		)`, findingIDs).Scan(&activeLinked); err != nil {
		return "", fmt.Errorf("check active work order findings: %w", err)
	}
	if activeLinked {
		return "", ErrConflict
	}
	severity := "info"
	for _, itemSeverity := range found {
		if severityRank(itemSeverity) > severityRank(severity) {
			severity = itemSeverity
		}
	}
	return severity, nil
}

const accessibleCTE = `
	WITH RECURSIVE descendant_roots(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
	), descendants(id) AS (
		SELECT id FROM descendant_roots
		UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
	WHERE child.status='enabled' AND child.deleted_at IS NULL
	), accessible(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
		  AND ur.data_scope IN ('organization', 'organization_and_descendants')
		UNION SELECT id FROM descendants
	), work_order_permission_descendant_roots(id) AS (
		SELECT ur.organization_id
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		JOIN organizations work_order_root ON work_order_root.id=ur.organization_id AND work_order_root.status='enabled' AND work_order_root.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid
		  AND ur.organization_id IS NOT NULL
		  AND ur.data_scope = 'organization_and_descendants'
		  AND rp.permission_key IN ('security_workorder.view', 'security_workorder.execute')
	), work_order_permission_descendants(id) AS (
		SELECT id FROM work_order_permission_descendant_roots
		UNION
		SELECT child.id
		FROM organizations child
		JOIN work_order_permission_descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
	), work_order_accessible(id) AS (
		SELECT ur.organization_id
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		JOIN organizations work_order_direct ON work_order_direct.id=ur.organization_id AND work_order_direct.status='enabled' AND work_order_direct.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid
		  AND ur.organization_id IS NOT NULL
		  AND ur.data_scope IN ('organization', 'organization_and_descendants')
		  AND rp.permission_key IN ('security_workorder.view', 'security_workorder.execute')
		UNION SELECT id FROM work_order_permission_descendants
	), finding_permission_descendant_roots(permission_key, id) AS (
		SELECT rp.permission_key, ur.organization_id
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		JOIN organizations finding_root ON finding_root.id=ur.organization_id AND finding_root.status='enabled' AND finding_root.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid
		  AND ur.organization_id IS NOT NULL
		  AND ur.data_scope = 'organization_and_descendants'
		  AND rp.permission_key IN ('asset.manage', 'system.manage')
	), finding_permission_descendants(permission_key, id) AS (
		SELECT permission_key, id FROM finding_permission_descendant_roots
		UNION
		SELECT parent.permission_key, child.id
		FROM organizations child
		JOIN finding_permission_descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
	), finding_permission_accessible(permission_key, id) AS (
		SELECT rp.permission_key, ur.organization_id
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status = 'enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		JOIN organizations finding_direct ON finding_direct.id=ur.organization_id AND finding_direct.status='enabled' AND finding_direct.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid
		  AND ur.organization_id IS NOT NULL
		  AND ur.data_scope IN ('organization', 'organization_and_descendants')
		  AND rp.permission_key IN ('asset.manage', 'system.manage')
		UNION SELECT permission_key, id FROM finding_permission_descendants
	)
`

const workOrderSelect = `
	SELECT wo.id::text, wo.work_order_no, wo.title,
	       o.id::text, o.organization_code, o.organization_name,
	       wo.severity, wo.status, COALESCE(wo.description, ''), wo.remediation_requirement,
	       wo.due_at, (wo.due_at < now() AND wo.status NOT IN ('closed', 'cancelled')) AS overdue,
	       wo.issued_at, wo.accepted_at, wo.submitted_at, wo.verified_at, wo.closed_at, wo.cancelled_at,
	       COALESCE(wo.remediation_progress, ''), COALESCE(wo.remediation_result, ''),
	       COALESCE(wo.verification_result, ''), COALESCE(wo.return_reason, ''),
	       COALESCE(u.display_name, ''),
	       (SELECT count(*) FROM security_work_order_findings link WHERE link.work_order_id = wo.id),
	       wo.created_at, wo.updated_at, wo.execution_mode,CASE WHEN wo.execution_mode='individual' THEN remediation_order_counts(wo.id) ELSE '{}'::jsonb END
	FROM security_work_orders wo
	JOIN organizations o ON o.id = wo.assignee_organization_id
	LEFT JOIN users u ON u.id = wo.created_by
`

type rowScanner interface{ Scan(...any) error }

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func scanFindingWithTotal(row rowScanner, item *Finding, total *int) error {
	if err := row.Scan(&item.ID, &item.Code, &item.Title, &item.Severity, &item.Status, &item.AssetAssociationStatus, &item.RemediationStatus,
		&item.AssigneeOrganizationID, &item.AssigneeOrganizationCode, &item.AssigneeOrganizationName,
		&item.AssetID, &item.AssetValue, &item.AssetName, &item.PendingAssetDraftID, &item.AssetDeleted,
		&item.BusinessSystemID, &item.BusinessSystemCode,
		&item.BusinessSystemName, &item.TestBatchID, &item.TestBatchCode, &item.TestBatchName, &item.VulnerabilityType, &item.Description, &item.Evidence,
		&item.RemediationAdvice, &item.Source, &item.DiscoveredAt, &item.CreatedByName, &item.CreatedAt, &item.UpdatedAt,
		&item.RiskAcceptedAt, &item.RiskAcceptedUntil, &item.RiskAcceptanceReason, &item.RiskAcceptedByName,
		&item.RiskAcceptedFromStatus, &item.RiskReviewedAt, &item.RiskReviewedByName, &item.RiskReviewComment, &item.RiskAcceptanceExpired,
		&item.ImportStatusRaw, &item.ImportStatusNormalized, &item.ImportStatusMappingReason, &item.DuplicateOfFindingID, total); err != nil {
		return err
	}
	// Derive the single word the UI branches on so every listing and detail view
	// reports the same asset-link state without repeating the CASE expression.
	item.AssetLinkState = findingAssetLinkState(item.AssetID, item.PendingAssetDraftID, item.AssetDeleted)
	if item.AssetAssociationStatus == "" {
		item.AssetAssociationStatus = item.AssetLinkState
	}
	return nil
}

func scanWorkOrderWithTotal(row rowScanner, item *WorkOrder, total *int) error {
	return row.Scan(total, &item.ID, &item.Number, &item.Title, &item.AssigneeOrganizationID,
		&item.AssigneeOrganizationCode, &item.AssigneeOrganizationName, &item.Severity, &item.Status,
		&item.Description, &item.RemediationRequirement, &item.DueAt, &item.Overdue,
		&item.IssuedAt, &item.AcceptedAt, &item.SubmittedAt, &item.VerifiedAt, &item.ClosedAt,
		&item.CancelledAt, &item.RemediationProgress, &item.RemediationResult, &item.VerificationResult,
		&item.ReturnReason, &item.CreatedByName, &item.FindingCount, &item.CreatedAt, &item.UpdatedAt, &item.ExecutionMode, &item.RemediationCounts)
}

func scanFinding(row rowScanner, item *Finding) error {
	err := row.Scan(&item.ID, &item.Code, &item.Title, &item.Severity, &item.Status, &item.AssetAssociationStatus, &item.RemediationStatus,
		&item.AssigneeOrganizationID, &item.AssigneeOrganizationCode, &item.AssigneeOrganizationName,
		&item.AssetID, &item.AssetValue, &item.AssetName, &item.PendingAssetDraftID, &item.AssetDeleted,
		&item.BusinessSystemID, &item.BusinessSystemCode,
		&item.BusinessSystemName, &item.TestBatchID, &item.TestBatchCode, &item.TestBatchName, &item.VulnerabilityType, &item.Description, &item.Evidence,
		&item.RemediationAdvice, &item.Source, &item.DiscoveredAt, &item.CreatedByName, &item.CreatedAt, &item.UpdatedAt,
		&item.RiskAcceptedAt, &item.RiskAcceptedUntil, &item.RiskAcceptanceReason, &item.RiskAcceptedByName,
		&item.RiskAcceptedFromStatus, &item.RiskReviewedAt, &item.RiskReviewedByName, &item.RiskReviewComment, &item.RiskAcceptanceExpired,
		&item.ImportStatusRaw, &item.ImportStatusNormalized, &item.ImportStatusMappingReason, &item.DuplicateOfFindingID)
	if err != nil {
		return err
	}
	item.AssetLinkState = findingAssetLinkState(item.AssetID, item.PendingAssetDraftID, item.AssetDeleted)
	if item.AssetAssociationStatus == "" {
		item.AssetAssociationStatus = item.AssetLinkState
	}
	return nil
}

func scanWorkOrder(row rowScanner, item *WorkOrder) error {
	return row.Scan(&item.ID, &item.Number, &item.Title, &item.AssigneeOrganizationID,
		&item.AssigneeOrganizationCode, &item.AssigneeOrganizationName, &item.Severity, &item.Status,
		&item.Description, &item.RemediationRequirement, &item.DueAt, &item.Overdue,
		&item.IssuedAt, &item.AcceptedAt, &item.SubmittedAt, &item.VerifiedAt, &item.ClosedAt,
		&item.CancelledAt, &item.RemediationProgress, &item.RemediationResult, &item.VerificationResult,
		&item.ReturnReason, &item.CreatedByName, &item.FindingCount, &item.CreatedAt, &item.UpdatedAt, &item.ExecutionMode, &item.RemediationCounts)
}

func loadWorkOrder(ctx context.Context, q queryer, id string) (WorkOrder, error) {
	var item WorkOrder
	if err := scanWorkOrder(q.QueryRow(ctx, workOrderSelect+` WHERE wo.id::text = $1`, id), &item); errors.Is(err, pgx.ErrNoRows) {
		return WorkOrder{}, ErrNotFound
	} else if err != nil {
		return WorkOrder{}, fmt.Errorf("load security work order: %w", err)
	}
	return item, nil
}

func writeEvent(ctx context.Context, tx pgx.Tx, principal auth.Principal, workOrderID, action, fromStatus, toStatus, comment string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode work order event: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO security_work_order_events (work_order_id, actor_user_id, actor_organization_id, action, from_status, to_status, comment, details)
		VALUES ($1::uuid, $2::uuid, NULLIF($3, '')::uuid, $4, NULLIF($5, ''), NULLIF($6, ''), NULLIF($7, ''), $8::jsonb)`,
		workOrderID, principal.UserID, principalOrganizationID(principal), action, fromStatus, toStatus, comment, payload); err != nil {
		return fmt.Errorf("write work order event: %w", err)
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, organizationID, action, resourceType, resourceID, summary string, details map[string]any, requestID, ipAddress string) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode work order audit details: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4, $5, $6, $7::jsonb, NULLIF($8, '')::inet, NULLIF($9, ''))`,
		principal.UserID, organizationID, action, resourceType, resourceID, summary, payload, ipAddress, requestID); err != nil {
		return fmt.Errorf("write security work order audit: %w", err)
	}
	return nil
}

// expireDueRiskAcceptances materializes expiry as a state transition instead
// of leaving the database row in risk_accepted forever. It is invoked lazily by
// finding reads and summaries because this service has no scheduler. The
// operation is idempotent and writes a system audit record for every finding.
func (s *Service) expireDueRiskAcceptances(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin expire risk acceptances: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	type expiredRiskAcceptance struct {
		id             string
		organizationID string
		previousStatus string
		newStatus      string
		acceptedUntil  *time.Time
		expiredAt      time.Time
	}
	rows, err := tx.Query(ctx, `
		UPDATE security_findings
		SET status = CASE
				WHEN risk_accepted_from_status IN ('pending','in_progress','confirmed','resolved','fixed') THEN risk_accepted_from_status
				ELSE 'confirmed'
			END,
			risk_acceptance_expired_at = COALESCE(risk_acceptance_expired_at, now()),
			updated_at = now()
		WHERE status='risk_accepted'
		  AND risk_accepted_until IS NOT NULL
		  AND risk_accepted_until <= now()
		  AND risk_acceptance_expired_at IS NULL
		RETURNING id::text, assignee_organization_id::text, COALESCE(risk_accepted_from_status, ''),
		          risk_accepted_until, status, risk_acceptance_expired_at`)
	if err != nil {
		return fmt.Errorf("expire risk acceptances: %w", err)
	}
	expired := make([]expiredRiskAcceptance, 0)
	for rows.Next() {
		var item expiredRiskAcceptance
		if err := rows.Scan(&item.id, &item.organizationID, &item.previousStatus, &item.acceptedUntil, &item.newStatus, &item.expiredAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan expired risk acceptance: %w", err)
		}
		expired = append(expired, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate expired risk acceptances: %w", err)
	}
	// pgx keeps the connection busy while RETURNING rows are open. Read all
	// affected rows and close the result before using the same transaction for
	// audit inserts; otherwise expiry can fail intermittently under load.
	rows.Close()
	for _, item := range expired {
		payload, err := json.Marshal(map[string]any{
			"previous_status":            findingstatus.RiskAccepted,
			"restored_from_status":       restoredRiskAcceptanceStatus(item.previousStatus),
			"new_status":                 item.newStatus,
			"risk_accepted_from_status":  item.previousStatus,
			"risk_accepted_until":        item.acceptedUntil,
			"risk_acceptance_expired_at": item.expiredAt,
		})
		if err != nil {
			return fmt.Errorf("encode expired risk audit: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO audit_logs (organization_id, action, resource_type, resource_id, summary, details)
			VALUES (NULLIF($1,'')::uuid, 'security_finding.risk_acceptance_expired', 'security_finding', $2,
			        '风险接受已到期，恢复重新评估', $3::jsonb)`, item.organizationID, item.id, string(payload)); err != nil {
			return fmt.Errorf("write expired risk audit: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit expired risk acceptances: %w", err)
	}
	return nil
}

func normalizeFindingInput(input *FindingInput) {
	input.Title = strings.TrimSpace(input.Title)
	input.Severity = strings.TrimSpace(input.Severity)
	input.Status = strings.TrimSpace(input.Status)
	input.AssigneeOrganizationID = strings.TrimSpace(input.AssigneeOrganizationID)
	input.AssetID = strings.TrimSpace(input.AssetID)
	input.BusinessSystemID = strings.TrimSpace(input.BusinessSystemID)
	input.PendingAssetDraftID = strings.TrimSpace(input.PendingAssetDraftID)
	input.TestBatchID = strings.TrimSpace(input.TestBatchID)
	input.VulnerabilityType = strings.TrimSpace(input.VulnerabilityType)
	input.Description = strings.TrimSpace(input.Description)
	input.Evidence = strings.TrimSpace(input.Evidence)
	input.RemediationAdvice = strings.TrimSpace(input.RemediationAdvice)
	input.Source = strings.TrimSpace(input.Source)
	if input.Status == "" {
		input.Status = "pending"
	}
	if input.Source == "" {
		input.Source = "manual"
	}
	if input.DiscoveredAt.IsZero() {
		input.DiscoveredAt = time.Now()
	}
}

func canLinkDuplicateFromStatus(status string) bool {
	switch status {
	case findingstatus.Pending, findingstatus.InProgress, findingstatus.Confirmed:
		return true
	default:
		return false
	}
}

func restoredRiskAcceptanceStatus(status string) string {
	switch status {
	case findingstatus.Pending, findingstatus.InProgress, findingstatus.Confirmed, findingstatus.Resolved, findingstatus.Fixed:
		return status
	default:
		return findingstatus.Confirmed
	}
}

// checkFindingVersion enforces the optimistic lock on a status action once the
// row is already held with FOR UPDATE. Every state transition outside the import
// pipeline decides from data an operator read on screen, so it must prove that
// data is still current before it writes. A missing token is a client bug; a
// stale token is a conflict the caller resolves by refreshing and confirming
// again. The lock must be read as timestamptz so the comparison is exact.
func checkFindingVersion(id string, expected *time.Time, current time.Time) error {
	if expected == nil {
		return ErrVersionRequired
	}
	if !current.Equal(*expected) {
		return ErrEditConflict
	}
	return nil
}

func validRiskAcceptanceInput(input RiskAcceptanceInput) bool {
	return input.Reason != "" && textlength.Count(input.Reason) <= 5000 && input.AcceptedUntil.After(time.Now())
}

func validFindingInput(input FindingInput) bool {
	return input.Title != "" && textlength.Count(input.Title) <= 255 && validSeverity(input.Severity) && validFindingStatus(input.Status) &&
		validUUID(input.AssigneeOrganizationID) && (input.AssetID != "" || input.BusinessSystemID != "") &&
		(input.AssetID == "" || validUUID(input.AssetID)) && (input.BusinessSystemID == "" || validUUID(input.BusinessSystemID)) &&
		(input.TestBatchID == "" || validUUID(input.TestBatchID)) &&
		textlength.Count(input.VulnerabilityType) <= 128 && input.Description != "" && textlength.Count(input.Description) <= 20000 &&
		textlength.Count(input.Evidence) <= 30000 && textlength.Count(input.RemediationAdvice) <= 20000 &&
		(input.Source == "manual" || input.Source == "import" || input.Source == "api" || input.Source == "plugin")
}

func normalizeWorkOrderInput(input *WorkOrderInput) {
	input.Title = strings.TrimSpace(input.Title)
	input.AssigneeOrganizationID = strings.TrimSpace(input.AssigneeOrganizationID)
	input.Description = strings.TrimSpace(input.Description)
	input.RemediationRequirement = strings.TrimSpace(input.RemediationRequirement)
	input.FindingIDs = uniqueNonEmpty(input.FindingIDs)
}

func validWorkOrderInput(input WorkOrderInput) bool {
	if input.Title == "" || textlength.Count(input.Title) > 255 || !validUUID(input.AssigneeOrganizationID) ||
		textlength.Count(input.Description) > 10000 || input.RemediationRequirement == "" || textlength.Count(input.RemediationRequirement) > 20000 ||
		input.DueAt.IsZero() || !input.DueAt.After(time.Now().Add(-time.Minute)) || len(input.FindingIDs) == 0 || len(input.FindingIDs) > 100 {
		return false
	}
	for _, id := range input.FindingIDs {
		if !validUUID(id) {
			return false
		}
	}
	return true
}

func validSeverity(value string) bool {
	return value == "critical" || value == "high" || value == "medium" || value == "low" || value == "info"
}

func validFindingStatus(value string) bool {
	return findingstatus.Valid(value)
}

func validWorkOrderStatus(value string) bool {
	return value == "draft" || value == "issued" || value == "in_progress" || value == "pending_verification" || value == "returned" || value == "closed" || value == "cancelled"
}

func validUUID(value string) bool { return uuidPattern.MatchString(value) }

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

func newCode(prefix string) (string, error) {
	buffer := make([]byte, 5)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s-%s", prefix, time.Now().Format("20060102"), strings.ToUpper(hex.EncodeToString(buffer))), nil
}

func severityRank(value string) int {
	return map[string]int{"info": 1, "low": 2, "medium": 3, "high": 4, "critical": 5}[value]
}

func contains(values []string, value string) bool {
	for _, current := range values {
		if current == value {
			return true
		}
	}
	return false
}

func principalOrganizationID(principal auth.Principal) string {
	if principal.Organization == nil {
		return ""
	}
	return principal.Organization.ID
}

func workOrderActionSummary(action string) string {
	labels := map[string]string{
		"issue": "下发安全工单", "accept": "接收安全工单", "progress": "更新整改进度",
		"submit": "提交复测", "return": "退回安全工单", "close": "关闭安全工单", "cancel": "取消安全工单",
	}
	if label, ok := labels[action]; ok {
		return label
	}
	return "更新安全工单"
}

func (s *Service) guardPersonalFinding(ctx context.Context, id string) error {
	if !validUUID(id) {
		return ErrInvalid
	}
	var linked bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id WHERE t.finding_id=$1::uuid AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled'))`, id).Scan(&linked); err != nil {
		return err
	}
	if linked {
		return ErrConflict
	}
	return nil
}
