package asset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
	"netassets/internal/notification"
	"netassets/internal/pagination"
	"netassets/internal/textlength"
)

// The formal asset ledger has two doors. Subsidiaries can only reach it through
// an inventory task: working version, submit, group review, publish. A group
// administrator writes it directly, which is necessary for headquarters-led
// registration but leaves nobody reviewing what was written. These helpers make
// the second door leave the same kind of trace the first one does, without
// changing who is allowed to write.
const (
	DirectChangeCreate = "create"
	DirectChangeUpdate = "update"
	DirectChangeDelete = "delete"

	DirectChangeStatusOpen      = "open"
	DirectChangeStatusConfirmed = "confirmed"
	DirectChangeStatusDisputed  = "disputed"
)

// DirectChangeReasonMax bounds the mandatory reason a group administrator has to
// give when writing the ledger directly. The reason is what makes the recheck
// item reviewable later, so it may not be optional.
const DirectChangeReasonMax = 1000

type DirectChangeReason struct {
	Reason string `json:"reason"`
}

type DirectChangeRecheckTask struct {
	ID               string          `json:"id"`
	AssetID          string          `json:"asset_id"`
	AssetType        string          `json:"asset_type"`
	AssetValue       string          `json:"asset_value"`
	AssetName        string          `json:"asset_name"`
	OrganizationID   string          `json:"organization_id"`
	OrganizationName string          `json:"organization_name"`
	Action           string          `json:"action"`
	Reason           string          `json:"reason"`
	Snapshot         json.RawMessage `json:"snapshot"`
	ChangedFields    json.RawMessage `json:"changed_fields"`
	Status           string          `json:"status"`
	CreatedBy        string          `json:"created_by"`
	CreatedByName    string          `json:"created_by_name"`
	CreatedAt        time.Time       `json:"created_at"`
	ConfirmedBy      string          `json:"confirmed_by"`
	ConfirmedByName  string          `json:"confirmed_by_name"`
	ConfirmedAt      *time.Time      `json:"confirmed_at"`
	ConfirmationNote string          `json:"confirmation_note"`
	RequestID        string          `json:"request_id"`
	RequestIP        string          `json:"request_ip"`
}

type DirectChangeConfirmationInput struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

type DirectChangeRecheckFilter struct {
	Status         string
	OrganizationID string
	AssetID        string
	Page           pagination.Query
}

const directChangeRecheckFields = `
	t.id::text, COALESCE(t.asset_id::text,''), COALESCE(a.asset_type,''), COALESCE(a.asset_value,''), COALESCE(a.asset_name,''),
	o.id::text, o.organization_name, t.action, t.reason, t.snapshot::text, t.changed_fields::text, t.status,
	COALESCE(t.created_by::text,''), COALESCE(cu.display_name, cu.username, ''), t.created_at,
	COALESCE(t.confirmed_by::text,''), COALESCE(fu.display_name, fu.username, ''), t.confirmed_at,
	COALESCE(t.confirmation_note,''), COALESCE(t.request_id,''), COALESCE(t.request_ip::text,'')`

const directChangeRecheckJoin = `
	FROM asset_direct_change_recheck_tasks t
	JOIN organizations o ON o.id = t.organization_id
	LEFT JOIN assets a ON a.id = t.asset_id
	LEFT JOIN users cu ON cu.id = t.created_by
	LEFT JOIN users fu ON fu.id = t.confirmed_by`

// The organization that owns the asset is the party asked to confirm the change,
// so it must hold asset.manage there to see and close the item. Headquarters is
// the writer of these rows, so the query is scoped the same way the asset list
// scopes writes: a group administrator sees everything, an organization user only
// the assets it may actually manage.
// The reachable-organization set depends on which placeholder carries the user
// id, so the two call sites get explicit variants instead of a fragile
// string replacement at build time.
const directChangeAccessibleCTEAt1 = `
RECURSIVE roots(id) AS (
    SELECT ur.organization_id FROM user_roles ur
    JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
    JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
    JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
    WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), descendants(id) AS (
    SELECT id FROM roots UNION
    SELECT o.id FROM organizations o JOIN descendants p ON o.parent_id=p.id
    WHERE o.status='enabled' AND o.deleted_at IS NULL
), accessible(id) AS (
    SELECT ur.organization_id FROM user_roles ur
    JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
    JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
    JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
    WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
    UNION SELECT id FROM descendants
)
`

const directChangeAccessibleCTEAt2 = `
RECURSIVE roots(id) AS (
    SELECT ur.organization_id FROM user_roles ur
    JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
    JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
    JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
    WHERE ur.user_id=$2::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), descendants(id) AS (
    SELECT id FROM roots UNION
    SELECT o.id FROM organizations o JOIN descendants p ON o.parent_id=p.id
    WHERE o.status='enabled' AND o.deleted_at IS NULL
), accessible(id) AS (
    SELECT ur.organization_id FROM user_roles ur
    JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
    JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
    JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
    WHERE ur.user_id=$2::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
    UNION SELECT id FROM descendants
)
`

// writeDirectChangeRecheck is called inside the same transaction as the direct
// write. If the recheck record cannot be written the whole write rolls back:
// an untraceable change to the formal ledger is worse than a failed one.
func (s *Service) writeDirectChangeRecheck(ctx context.Context, tx pgx.Tx, principal auth.Principal, action, assetID, organizationID, reason string, snapshot any, changedFields any, requestID, ipAddress string) error {
	action = strings.TrimSpace(action)
	reason = strings.TrimSpace(reason)
	switch action {
	case DirectChangeCreate, DirectChangeUpdate, DirectChangeDelete:
	default:
		return ErrInvalid
	}
	if reason == "" || len(reason) > DirectChangeReasonMax || !assetUUIDPattern.MatchString(assetID) {
		return ErrInvalid
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode direct change snapshot: %w", err)
	}
	fieldsJSON, err := json.Marshal(changedFields)
	if err != nil {
		return fmt.Errorf("encode direct change fields: %w", err)
	}
	var id string
	if err := tx.QueryRow(ctx, `
		INSERT INTO asset_direct_change_recheck_tasks (
			asset_id, organization_id, action, reason, snapshot, changed_fields, created_by, request_id, request_ip
		) VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6::jsonb, $7::uuid, NULLIF($8,''), NULLIF($9,'')::inet)
		RETURNING id::text`, assetID, organizationID, action, reason, snapshotJSON, fieldsJSON, principal.UserID, requestID, ipAddress).Scan(&id); err != nil {
		return fmt.Errorf("write direct change recheck task: %w", err)
	}
	label := directChangeActionLabel(action)
	return notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetDirectChanged, Category: notification.CategoryAssetChange,
		TargetOrganizationID: organizationID, ActorUserID: principal.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: assetID, ResourceLabel: action + "|" + assetID, BatchKey: "asset_direct_change:" + id,
		Level: "warning",
		Details: map[string]any{
			"recheck_task_id": id, "action": action, "action_label": label,
			"reason": reason, "request_id": requestID,
		},
	})
}

// validateDirectChangeReason keeps the reason mandatory and bounded. A direct
// write without a stated reason would raise a recheck item nobody can act on,
// which is the same as raising none.
func validateDirectChangeReason(reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len(reason) > DirectChangeReasonMax {
		return ErrInvalid
	}
	return nil
}

func directChangeActionLabel(action string) string {
	switch action {
	case DirectChangeCreate:
		return "集团直接新增"
	case DirectChangeUpdate:
		return "集团直接修改"
	case DirectChangeDelete:
		return "集团直接删除"
	}
	return action
}

// scanDirectChangeRecheckRow reads one recheck row. The list query appends a
// trailing window-count column; the scanner hands it back so the caller can use
// the real total instead of counting the page it happened to receive.
func scanDirectChangeRecheckRow(row pgx.Row, withCount bool) (DirectChangeRecheckTask, int64, error) {
	var item DirectChangeRecheckTask
	var snapshot, changedFields string
	var rowTotal int64
	destinations := []any{&item.ID, &item.AssetID, &item.AssetType, &item.AssetValue, &item.AssetName,
		&item.OrganizationID, &item.OrganizationName, &item.Action, &item.Reason, &snapshot, &changedFields, &item.Status,
		&item.CreatedBy, &item.CreatedByName, &item.CreatedAt, &item.ConfirmedBy, &item.ConfirmedByName, &item.ConfirmedAt,
		&item.ConfirmationNote, &item.RequestID, &item.RequestIP}
	if withCount {
		destinations = append(destinations, &rowTotal)
	}
	if err := row.Scan(destinations...); err != nil {
		return DirectChangeRecheckTask{}, 0, err
	}
	item.Snapshot = json.RawMessage(snapshot)
	item.ChangedFields = json.RawMessage(changedFields)
	return item, rowTotal, nil
}

// SearchDirectChangeRecheckTasks lists the direct ledger writes. Headquarters
// sees the whole group; an organization user sees the writes against its own
// assets, which is exactly the population it is asked to confirm.
func (s *Service) SearchDirectChangeRecheckTasks(ctx context.Context, principal auth.Principal, filter DirectChangeRecheckFilter) (pagination.Result[DirectChangeRecheckTask], error) {
	if !principal.HasPermission("asset.manage") {
		return pagination.Result[DirectChangeRecheckTask]{}, ErrForbidden
	}
	filter.Status = strings.TrimSpace(filter.Status)
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.AssetID = strings.TrimSpace(filter.AssetID)
	if filter.Status != "" && !validDirectChangeStatus(filter.Status) {
		return pagination.Result[DirectChangeRecheckTask]{}, ErrInvalid
	}
	if filter.AssetID != "" && !assetUUIDPattern.MatchString(filter.AssetID) {
		return pagination.Result[DirectChangeRecheckTask]{}, ErrInvalid
	}
	args := []any{principal.UserID, principal.HasRole("group_admin"), filter.Status, filter.OrganizationID, filter.AssetID, filter.Page.PageSize, filter.Page.Offset()}
	rows, err := s.pool.Query(ctx, `
		WITH `+directChangeAccessibleCTEAt1+`
		SELECT `+directChangeRecheckFields+`, count(*) OVER () `+directChangeRecheckJoin+`
		WHERE ($2::boolean OR t.organization_id IN (SELECT id FROM accessible))
		  AND ($3::text = '' OR t.status = $3)
		  AND ($4::text = '' OR t.organization_id::text = $4)
		  AND ($5::text = '' OR t.asset_id::text = $5)
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $6 OFFSET $7`, args...)
	if err != nil {
		return pagination.Result[DirectChangeRecheckTask]{}, fmt.Errorf("search direct change recheck tasks: %w", err)
	}
	defer rows.Close()
	items := make([]DirectChangeRecheckTask, 0, filter.Page.PageSize)
	total := 0
	for rows.Next() {
		item, rowTotal, err := scanDirectChangeRecheckRow(rows, true)
		if err != nil {
			return pagination.Result[DirectChangeRecheckTask]{}, fmt.Errorf("scan direct change recheck task: %w", err)
		}
		if total == 0 {
			total = int(rowTotal)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[DirectChangeRecheckTask]{}, fmt.Errorf("iterate direct change recheck tasks: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), filter.Page) {
		if err := s.pool.QueryRow(ctx, `
			WITH `+directChangeAccessibleCTEAt1+`
			SELECT count(*) FROM asset_direct_change_recheck_tasks t
			WHERE ($2::boolean OR t.organization_id IN (SELECT id FROM accessible))
			  AND ($3::text = '' OR t.status = $3)
			  AND ($4::text = '' OR t.organization_id::text = $4)
			  AND ($5::text = '' OR t.asset_id::text = $5)`,
			principal.UserID, principal.HasRole("group_admin"), filter.Status, filter.OrganizationID, filter.AssetID).Scan(&total); err != nil {
			return pagination.Result[DirectChangeRecheckTask]{}, fmt.Errorf("count direct change recheck tasks: %w", err)
		}
	}
	return pagination.NewResult(items, total, filter.Page), nil
}

// GetDirectChangeRecheckTask loads one recheck item under the same scope rule as
// the list, so a subsidiary cannot read another organization's recheck record by
// guessing its id.
func (s *Service) GetDirectChangeRecheckTask(ctx context.Context, principal auth.Principal, id string) (DirectChangeRecheckTask, error) {
	if !principal.HasPermission("asset.manage") {
		return DirectChangeRecheckTask{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	if !assetUUIDPattern.MatchString(id) {
		return DirectChangeRecheckTask{}, ErrInvalid
	}
	item, _, err := scanDirectChangeRecheckRow(s.pool.QueryRow(ctx, `
		WITH `+directChangeAccessibleCTEAt2+`
		SELECT `+directChangeRecheckFields+directChangeRecheckJoin+`
		WHERE t.id=$1::uuid
		  AND (COALESCE($3, false) OR t.organization_id IN (SELECT id FROM accessible))`,
		id, principal.UserID, principal.HasRole("group_admin")), false)
	if errors.Is(err, pgx.ErrNoRows) {
		return DirectChangeRecheckTask{}, ErrNotFound
	}
	if err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("load direct change recheck task: %w", err)
	}
	return item, nil
}

// ConfirmDirectChangeRecheckTask closes a recheck item. Only the organization
// that owns the asset may close it, and headquarters may not close it on the
// organization's behalf: the whole point of the item is that a second party
// looks at what was written. A "disputed" decision records that the change is
// wrong without deleting anything.
func (s *Service) ConfirmDirectChangeRecheckTask(ctx context.Context, principal auth.Principal, id string, input DirectChangeConfirmationInput, requestID, ipAddress string) (DirectChangeRecheckTask, error) {
	if !principal.HasPermission("asset.manage") {
		return DirectChangeRecheckTask{}, ErrForbidden
	}
	id = strings.TrimSpace(id)
	input.Status = strings.TrimSpace(input.Status)
	input.Note = strings.TrimSpace(input.Note)
	if !assetUUIDPattern.MatchString(id) || !validDirectChangeStatus(input.Status) || input.Status == DirectChangeStatusOpen {
		return DirectChangeRecheckTask{}, ErrInvalid
	}
	if input.Note == "" || textlength.Count(input.Note) > 2000 {
		return DirectChangeRecheckTask{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("begin direct change recheck confirmation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var current, organizationID string
	if err := tx.QueryRow(ctx, `SELECT status, organization_id::text FROM asset_direct_change_recheck_tasks WHERE id=$1::uuid FOR UPDATE`, id).Scan(&current, &organizationID); errors.Is(err, pgx.ErrNoRows) {
		return DirectChangeRecheckTask{}, ErrNotFound
	} else if err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("lock direct change recheck task: %w", err)
	}
	if current != DirectChangeStatusOpen {
		return DirectChangeRecheckTask{}, ErrConflict
	}
	// Only the organization that owns the asset may close the item, and never the
	// writer of the change itself. access.CanOperateOrganization short-circuits to
	// true for a group administrator, which is exactly the shortcut that would let
	// headquarters sign off on its own write, so the ownership appointment is
	// checked explicitly here instead of through that helper.
	var actsForOwner bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM user_roles ur
			JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
			JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
			JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
			WHERE ur.user_id=$1::uuid
			  AND ur.organization_id=$2::uuid
			  AND ur.data_scope IN ('organization','organization_and_descendants'))
		`, principal.UserID, organizationID).Scan(&actsForOwner); err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("check recheck owner appointment: %w", err)
	}
	if !actsForOwner {
		return DirectChangeRecheckTask{}, ErrForbidden
	}
	if _, err := tx.Exec(ctx, `
		UPDATE asset_direct_change_recheck_tasks
		SET status=$2, confirmed_by=$3::uuid, confirmed_at=now(), confirmation_note=$4
		WHERE id=$1::uuid`, id, input.Status, principal.UserID, input.Note); err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("confirm direct change recheck task: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES($1::uuid,$2::uuid,'asset_direct_change_recheck.confirm','asset_direct_change_recheck',$3,$4,
		       jsonb_build_object('status',$5::text,'note',$6::text),NULLIF($7,'')::inet,NULLIF($8,''))`,
		principal.UserID, organizationID, id,
		"复核集团直接变更资产："+input.Status, input.Status, input.Note, ipAddress, requestID); err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("write direct change recheck audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return DirectChangeRecheckTask{}, fmt.Errorf("commit direct change recheck confirmation: %w", err)
	}
	return s.GetDirectChangeRecheckTask(ctx, principal, id)
}

func validDirectChangeStatus(status string) bool {
	switch status {
	case DirectChangeStatusOpen, DirectChangeStatusConfirmed, DirectChangeStatusDisputed:
		return true
	}
	return false
}
