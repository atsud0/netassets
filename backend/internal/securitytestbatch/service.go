package securitytestbatch

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

type Batch struct {
	ID                string       `json:"id"`
	Code              string       `json:"code"`
	Name              string       `json:"name"`
	TestType          string       `json:"test_type"`
	Status            string       `json:"status"`
	Provider          string       `json:"provider"`
	AuthorizationNote string       `json:"authorization_note"`
	AuthorizedBy      string       `json:"authorized_by,omitempty"`
	AuthorizedByName  string       `json:"authorized_by_name,omitempty"`
	AuthorizedAt      *time.Time   `json:"authorized_at,omitempty"`
	StartAt           time.Time    `json:"start_at"`
	EndAt             time.Time    `json:"end_at"`
	StartedAt         *time.Time   `json:"started_at,omitempty"`
	CompletedAt       *time.Time   `json:"completed_at,omitempty"`
	CancelledAt       *time.Time   `json:"cancelled_at,omitempty"`
	Notes             string       `json:"notes,omitempty"`
	AssetCount        int          `json:"asset_count"`
	OrganizationCount int          `json:"organization_count"`
	CreatedByName     string       `json:"created_by_name,omitempty"`
	CreatedAt         time.Time    `json:"created_at"`
	UpdatedAt         time.Time    `json:"updated_at"`
	ScopeAssets       []ScopeAsset `json:"scope_assets,omitempty"`
	Events            []BatchEvent `json:"events,omitempty"`
	Attachments       []Attachment `json:"attachments,omitempty"`
}

type ScopeAsset struct {
	ID                 string    `json:"id"`
	AssetID            string    `json:"asset_id,omitempty"`
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
	SnapshotAt         time.Time `json:"snapshot_at"`
}

type BatchEvent struct {
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
	Name              string    `json:"batch_name"`
	TestType          string    `json:"test_type"`
	Provider          string    `json:"provider"`
	AuthorizationNote string    `json:"authorization_note"`
	StartAt           time.Time `json:"start_at"`
	EndAt             time.Time `json:"end_at"`
	Notes             string    `json:"notes"`
	AssetIDs          []string  `json:"asset_ids"`
}

type TransitionInput struct {
	Comment string `json:"comment"`
}

type Service struct {
	pool       *pgxpool.Pool
	storageDir string
}

func NewService(pool *pgxpool.Pool, storageDirs ...string) *Service {
	storageDir := "data"
	if len(storageDirs) > 0 && strings.TrimSpace(storageDirs[0]) != "" {
		storageDir = storageDirs[0]
	}
	return &Service{pool: pool, storageDir: storageDir}
}

func (s *Service) List(ctx context.Context, principal auth.Principal, status string) ([]Batch, error) {
	if !principal.CanManageSecurityTests() {
		return nil, ErrForbidden
	}
	status = strings.TrimSpace(status)
	if status != "" && !validStatus(status) {
		return nil, &ValidationError{Message: "请选择有效的安全测试批次状态"}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id::text, b.batch_code, b.batch_name, b.test_type, b.status, b.provider,
		       b.authorization_note, COALESCE(b.authorized_by::text, ''), COALESCE(au.display_name, ''),
		       b.authorized_at, b.start_at, b.end_at, b.started_at, b.completed_at, b.cancelled_at,
		       COALESCE(b.notes, ''), COUNT(sa.id)::int, COUNT(DISTINCT sa.organization_id)::int,
		       COALESCE(cu.display_name, ''), b.created_at, b.updated_at
		FROM security_test_batches b
		LEFT JOIN security_test_scope_assets sa ON sa.batch_id = b.id
		LEFT JOIN users au ON au.id = b.authorized_by
		LEFT JOIN users cu ON cu.id = b.created_by
		WHERE ($1::text = '' OR b.status = $1)
		GROUP BY b.id, au.display_name, cu.display_name
		ORDER BY b.created_at DESC`, status)
	if err != nil {
		return nil, fmt.Errorf("list security test batches: %w", err)
	}
	defer rows.Close()
	items := make([]Batch, 0)
	for rows.Next() {
		var item Batch
		if err := scanBatch(rows, &item); err != nil {
			return nil, fmt.Errorf("scan security test batch: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, status string, query pagination.Query) (pagination.Result[Batch], error) {
	if !principal.CanManageSecurityTests() {
		return pagination.Result[Batch]{}, ErrForbidden
	}
	status = strings.TrimSpace(status)
	if status != "" && !validStatus(status) {
		return pagination.Result[Batch]{}, &ValidationError{Message: "请选择有效的安全测试批次状态"}
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id::text, b.batch_code, b.batch_name, b.test_type, b.status, b.provider,
		       b.authorization_note, COALESCE(b.authorized_by::text, ''), COALESCE(au.display_name, ''),
		       b.authorized_at, b.start_at, b.end_at, b.started_at, b.completed_at, b.cancelled_at,
		       COALESCE(b.notes, ''), COUNT(sa.id)::int, COUNT(DISTINCT sa.organization_id)::int,
		       COALESCE(cu.display_name, ''), b.created_at, b.updated_at, count(*) OVER ()
		FROM security_test_batches b
		LEFT JOIN security_test_scope_assets sa ON sa.batch_id = b.id
		LEFT JOIN users au ON au.id = b.authorized_by
		LEFT JOIN users cu ON cu.id = b.created_by
		WHERE ($1::text = '' OR b.status = $1)
		GROUP BY b.id, au.display_name, cu.display_name
		ORDER BY b.created_at DESC
		LIMIT $2 OFFSET $3`, status, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Batch]{}, fmt.Errorf("search security test batches: %w", err)
	}
	defer rows.Close()
	items := make([]Batch, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item Batch
		targets := append(batchScanTargets(&item), &total)
		if err := rows.Scan(targets...); err != nil {
			return pagination.Result[Batch]{}, fmt.Errorf("scan security test batch page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Batch]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_test_batches WHERE ($1::text = '' OR status = $1)`, status).Scan(&total); err != nil {
			return pagination.Result[Batch]{}, fmt.Errorf("count security test batches: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, id string) (Batch, error) {
	if !principal.CanManageSecurityTests() {
		return Batch{}, ErrForbidden
	}
	if !validUUID(id) {
		return Batch{}, &ValidationError{Message: "安全测试批次标识不正确"}
	}
	item, err := s.get(ctx, id)
	if err != nil {
		return Batch{}, err
	}
	item.ScopeAssets, err = s.loadScope(ctx, id)
	if err != nil {
		return Batch{}, err
	}
	return item, nil
}

// SearchEvents returns the newest batch workflow events first. Events are
// loaded separately so the batch detail response does not grow without bound.
func (s *Service) SearchEvents(ctx context.Context, principal auth.Principal, id string, query pagination.Query) (pagination.Result[BatchEvent], error) {
	if !principal.CanManageSecurityTests() {
		return pagination.Result[BatchEvent]{}, ErrForbidden
	}
	if !validUUID(id) {
		return pagination.Result[BatchEvent]{}, &ValidationError{Message: "安全测试批次标识不正确"}
	}
	if _, err := s.get(ctx, id); err != nil {
		return pagination.Result[BatchEvent]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_test_batch_events WHERE batch_id=$1::uuid`, id).Scan(&total); err != nil {
		return pagination.Result[BatchEvent]{}, fmt.Errorf("count security test batch events: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT e.id, e.action, COALESCE(e.from_status, ''), COALESCE(e.to_status, ''), COALESCE(e.comment, ''),
		       e.details, COALESCE(u.display_name, ''), e.created_at
		FROM security_test_batch_events e LEFT JOIN users u ON u.id=e.actor_user_id
		WHERE e.batch_id=$1::uuid
		ORDER BY e.created_at DESC, e.id DESC
		LIMIT $2 OFFSET $3`, id, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[BatchEvent]{}, fmt.Errorf("list security test batch events: %w", err)
	}
	defer rows.Close()
	items := make([]BatchEvent, 0, query.PageSize)
	for rows.Next() {
		var item BatchEvent
		if err := rows.Scan(&item.ID, &item.Action, &item.FromStatus, &item.ToStatus, &item.Comment, &item.Details, &item.ActorName, &item.CreatedAt); err != nil {
			return pagination.Result[BatchEvent]{}, fmt.Errorf("scan security test batch event: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[BatchEvent]{}, fmt.Errorf("iterate security test batch events: %w", err)
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input Input, requestID, ipAddress string) (Batch, error) {
	if !principal.CanManageSecurityTests() {
		return Batch{}, ErrForbidden
	}
	normalizeInput(&input)
	if err := validateInput(input); err != nil {
		return Batch{}, err
	}
	code, err := newCode()
	if err != nil {
		return Batch{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("begin create security test batch: %w", err)
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO security_test_batches
		(batch_code, batch_name, test_type, provider, authorization_note, start_at, end_at, notes, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9::uuid, $9::uuid)
		RETURNING id::text`, code, input.Name, input.TestType, input.Provider, input.AuthorizationNote,
		input.StartAt, input.EndAt, input.Notes, principal.UserID).Scan(&id)
	if err != nil {
		return Batch{}, fmt.Errorf("create security test batch: %w", err)
	}
	if err := replaceScope(ctx, tx, id, principal.UserID, input.AssetIDs); err != nil {
		return Batch{}, err
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "create", "", "draft", "", map[string]any{"asset_count": len(input.AssetIDs)}); err != nil {
		return Batch{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_batch.create", id, "创建安全测试批次 "+input.Name,
		map[string]any{"batch_code": code, "asset_count": len(input.AssetIDs)}, requestID, ipAddress); err != nil {
		return Batch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Batch{}, fmt.Errorf("commit security test batch: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, id string, input Input, requestID, ipAddress string) (Batch, error) {
	if !principal.CanManageSecurityTests() {
		return Batch{}, ErrForbidden
	}
	if !validUUID(id) {
		return Batch{}, &ValidationError{Message: "安全测试批次标识不正确"}
	}
	normalizeInput(&input)
	if err := validateInput(input); err != nil {
		return Batch{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("begin update security test batch: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `
		UPDATE security_test_batches SET batch_name=$2, test_type=$3, provider=$4, authorization_note=$5,
		start_at=$6, end_at=$7, notes=NULLIF($8, ''), updated_by=$9::uuid, updated_at=now()
		WHERE id=$1::uuid AND status='draft'`, id, input.Name, input.TestType, input.Provider,
		input.AuthorizationNote, input.StartAt, input.EndAt, input.Notes, principal.UserID)
	if err != nil {
		return Batch{}, fmt.Errorf("update security test batch: %w", err)
	}
	if command.RowsAffected() == 0 {
		return Batch{}, s.classifyMissingOrState(ctx, tx, id)
	}
	if err := replaceScope(ctx, tx, id, principal.UserID, input.AssetIDs); err != nil {
		return Batch{}, err
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, "update", "draft", "draft", "", map[string]any{"asset_count": len(input.AssetIDs)}); err != nil {
		return Batch{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_batch.update", id, "更新安全测试批次 "+input.Name,
		map[string]any{"asset_count": len(input.AssetIDs)}, requestID, ipAddress); err != nil {
		return Batch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Batch{}, fmt.Errorf("commit security test batch update: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) Authorize(ctx context.Context, principal auth.Principal, id string, input TransitionInput, requestID, ipAddress string) (Batch, error) {
	return s.transition(ctx, principal, id, []string{"draft"}, "authorized", "authorize", input.Comment, requestID, ipAddress,
		`authorized_by=$3::uuid, authorized_at=now()`)
}

func (s *Service) Start(ctx context.Context, principal auth.Principal, id string, input TransitionInput, requestID, ipAddress string) (Batch, error) {
	return s.transition(ctx, principal, id, []string{"authorized"}, "in_progress", "start", input.Comment, requestID, ipAddress,
		`started_at=now()`)
}

func (s *Service) Complete(ctx context.Context, principal auth.Principal, id string, input TransitionInput, requestID, ipAddress string) (Batch, error) {
	return s.transition(ctx, principal, id, []string{"in_progress"}, "completed", "complete", input.Comment, requestID, ipAddress,
		`completed_at=now()`)
}

func (s *Service) Cancel(ctx context.Context, principal auth.Principal, id string, input TransitionInput, requestID, ipAddress string) (Batch, error) {
	return s.transition(ctx, principal, id, []string{"draft", "authorized", "in_progress"}, "cancelled", "cancel", input.Comment, requestID, ipAddress,
		`cancelled_at=now()`)
}

func (s *Service) transition(ctx context.Context, principal auth.Principal, id string, fromStatuses []string, toStatus, action, comment, requestID, ipAddress, extraSet string) (Batch, error) {
	if !principal.CanManageSecurityTests() {
		return Batch{}, ErrForbidden
	}
	if !validUUID(id) {
		return Batch{}, &ValidationError{Message: "安全测试批次标识不正确"}
	}
	if err := validateTransitionComment(action, comment); err != nil {
		return Batch{}, err
	}
	comment = strings.TrimSpace(comment)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Batch{}, fmt.Errorf("begin batch transition: %w", err)
	}
	defer tx.Rollback(ctx)
	var fromStatus, name string
	err = tx.QueryRow(ctx, `SELECT status, batch_name FROM security_test_batches WHERE id=$1::uuid FOR UPDATE`, id).Scan(&fromStatus, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, fmt.Errorf("lock security test batch: %w", err)
	}
	if !contains(fromStatuses, fromStatus) {
		return Batch{}, ErrState
	}
	query := `UPDATE security_test_batches SET status=$2, updated_by=$3::uuid, updated_at=now(), ` + extraSet + ` WHERE id=$1::uuid`
	if _, err := tx.Exec(ctx, query, id, toStatus, principal.UserID); err != nil {
		return Batch{}, fmt.Errorf("transition security test batch: %w", err)
	}
	if err := writeEvent(ctx, tx, principal.UserID, id, action, fromStatus, toStatus, comment, nil); err != nil {
		return Batch{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_batch."+action, id, "安全测试批次状态变更："+name,
		map[string]any{"from_status": fromStatus, "to_status": toStatus, "comment": comment}, requestID, ipAddress); err != nil {
		return Batch{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Batch{}, fmt.Errorf("commit batch transition: %w", err)
	}
	return s.Get(ctx, principal, id)
}

func (s *Service) get(ctx context.Context, id string) (Batch, error) {
	var item Batch
	err := s.pool.QueryRow(ctx, `
		SELECT b.id::text, b.batch_code, b.batch_name, b.test_type, b.status, b.provider,
		       b.authorization_note, COALESCE(b.authorized_by::text, ''), COALESCE(au.display_name, ''),
		       b.authorized_at, b.start_at, b.end_at, b.started_at, b.completed_at, b.cancelled_at,
		       COALESCE(b.notes, ''), COUNT(sa.id)::int, COUNT(DISTINCT sa.organization_id)::int,
		       COALESCE(cu.display_name, ''), b.created_at, b.updated_at
		FROM security_test_batches b
		LEFT JOIN security_test_scope_assets sa ON sa.batch_id=b.id
		LEFT JOIN users au ON au.id=b.authorized_by
		LEFT JOIN users cu ON cu.id=b.created_by
		WHERE b.id=$1::uuid GROUP BY b.id, au.display_name, cu.display_name`, id).Scan(batchScanTargets(&item)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Batch{}, ErrNotFound
	}
	if err != nil {
		return Batch{}, fmt.Errorf("get security test batch: %w", err)
	}
	return item, nil
}

func (s *Service) loadScope(ctx context.Context, id string) ([]ScopeAsset, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(asset_id::text, ''), asset_type_snapshot, asset_value_snapshot,
		       COALESCE(asset_name_snapshot, ''), COALESCE(organization_id::text, ''), organization_code_snapshot,
		       organization_name_snapshot, COALESCE(business_system_id::text, ''), COALESCE(business_system_code_snapshot, ''),
		       COALESCE(business_system_name_snapshot, ''), network_scope_snapshot, COALESCE(network_area_name_snapshot, ''),
		       environment_snapshot, importance_snapshot, snapshot_at
		FROM security_test_scope_assets WHERE batch_id=$1::uuid
		ORDER BY organization_name_snapshot, asset_type_snapshot, asset_value_snapshot`, id)
	if err != nil {
		return nil, fmt.Errorf("load security test scope: %w", err)
	}
	defer rows.Close()
	items := make([]ScopeAsset, 0)
	for rows.Next() {
		var item ScopeAsset
		if err := rows.Scan(&item.ID, &item.AssetID, &item.AssetType, &item.AssetValue, &item.AssetName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.BusinessSystemID,
			&item.BusinessSystemCode, &item.BusinessSystemName, &item.NetworkScope, &item.NetworkAreaName,
			&item.Environment, &item.Importance, &item.SnapshotAt); err != nil {
			return nil, fmt.Errorf("scan security test scope: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func replaceScope(ctx context.Context, tx pgx.Tx, batchID, userID string, assetIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM security_test_scope_assets WHERE batch_id=$1::uuid`, batchID); err != nil {
		return fmt.Errorf("clear security test scope: %w", err)
	}
	command, err := tx.Exec(ctx, `
		INSERT INTO security_test_scope_assets
		(batch_id, asset_id, asset_type_snapshot, asset_value_snapshot, asset_name_snapshot,
		 organization_id, organization_code_snapshot, organization_name_snapshot,
		 business_system_id, business_system_code_snapshot, business_system_name_snapshot,
		 network_scope_snapshot, network_area_name_snapshot, environment_snapshot, importance_snapshot, included_by)
		SELECT $1::uuid, a.id, a.asset_type, a.asset_value, a.asset_name,
		       o.id, o.organization_code, o.organization_name,
		       bs.id, bs.system_code, bs.system_name, a.network_scope, na.area_name, a.environment, a.importance, $2::uuid
		FROM assets a
		JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
		JOIN organizations o ON o.id=ao.organization_id
		LEFT JOIN LATERAL (
			SELECT linked.id, linked.system_code, linked.system_name
			FROM system_assets sa JOIN business_systems linked ON linked.id=sa.business_system_id
			WHERE sa.asset_id=a.id AND linked.deleted_at IS NULL
			ORDER BY sa.is_primary DESC, linked.system_name LIMIT 1
		) bs ON true
		LEFT JOIN asset_network_areas na ON na.id=a.network_area_id
		WHERE a.id = ANY($3::uuid[]) AND a.deleted_at IS NULL AND a.status <> 'archived' AND a.network_scope='public'`,
		batchID, userID, assetIDs)
	if err != nil {
		return fmt.Errorf("snapshot security test scope: %w", err)
	}
	if command.RowsAffected() != int64(len(assetIDs)) {
		return ErrConflict
	}
	return nil
}

func (s *Service) classifyMissingOrState(ctx context.Context, tx pgx.Tx, id string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM security_test_batches WHERE id=$1::uuid)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("classify security test batch: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return ErrState
}

func writeEvent(ctx context.Context, tx pgx.Tx, userID, batchID, action, fromStatus, toStatus, comment string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode security test batch event: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO security_test_batch_events (batch_id, actor_user_id, action, from_status, to_status, comment, details)
		VALUES ($1::uuid, $2::uuid, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7::jsonb)`,
		batchID, userID, action, fromStatus, toStatus, comment, payload)
	if err != nil {
		return fmt.Errorf("write security test batch event: %w", err)
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, action, resourceID, summary string, details map[string]any, requestID, ipAddress string) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode security test batch audit: %w", err)
	}
	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, 'security_test_batch', $4, $5, $6::jsonb, NULLIF($7, '')::inet, NULLIF($8, ''))`,
		principal.UserID, organizationID, action, resourceID, summary, payload, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write security test batch audit: %w", err)
	}
	return nil
}

func batchScanTargets(item *Batch) []any {
	return []any{&item.ID, &item.Code, &item.Name, &item.TestType, &item.Status, &item.Provider,
		&item.AuthorizationNote, &item.AuthorizedBy, &item.AuthorizedByName, &item.AuthorizedAt,
		&item.StartAt, &item.EndAt, &item.StartedAt, &item.CompletedAt, &item.CancelledAt, &item.Notes,
		&item.AssetCount, &item.OrganizationCount, &item.CreatedByName, &item.CreatedAt, &item.UpdatedAt}
}

func scanBatch(row interface{ Scan(...any) error }, item *Batch) error {
	return row.Scan(batchScanTargets(item)...)
}

func normalizeInput(input *Input) {
	input.Name = strings.TrimSpace(input.Name)
	input.TestType = strings.TrimSpace(input.TestType)
	input.Provider = strings.TrimSpace(input.Provider)
	input.AuthorizationNote = strings.TrimSpace(input.AuthorizationNote)
	input.Notes = strings.TrimSpace(input.Notes)
	input.AssetIDs = uniqueIDs(input.AssetIDs)
}

func validInput(input Input) bool { return validateInput(input) == nil }

func validTestType(value string) bool {
	return contains([]string{"penetration", "manual", "scan", "retest", "other"}, value)
}
func validStatus(value string) bool {
	return contains([]string{"draft", "authorized", "in_progress", "completed", "cancelled"}, value)
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
		return "", fmt.Errorf("generate security test batch code: %w", err)
	}
	return "ST-" + time.Now().Format("20060102") + "-" + strings.ToUpper(hex.EncodeToString(buffer)), nil
}
