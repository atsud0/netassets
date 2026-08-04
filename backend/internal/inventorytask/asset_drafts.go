package inventorytask

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/pagination"
)

type AssetDraft struct {
	asset.Asset
	TaskID        string `json:"task_id"`
	SourceAssetID string `json:"source_asset_id,omitempty"`
	DraftStatus   string `json:"draft_status"`
	Operation     string `json:"operation"`
	IsDraft       bool   `json:"is_draft"`
}

type draftAssignment struct {
	TaskName          string
	OrganizationName  string
	ProgressStatus    string
	AssetNetworkScope string
}

func (s *Service) SearchAssetDrafts(ctx context.Context, principal auth.Principal, taskID, organizationID string, page pagination.Query) (pagination.Result[AssetDraft], error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("asset.manage") {
		return pagination.Result[AssetDraft]{}, ErrForbidden
	}
	if _, err := s.loadDraftAssignment(ctx, s.pool, principal, taskID, organizationID, false, "network_assets"); err != nil {
		return pagination.Result[AssetDraft]{}, err
	}
	visibility := ""
	if principal.HasRole("group_admin") {
		visibility = " AND draft.draft_status IN ('submitted', 'published')"
	} else {
		visibility = " AND draft.draft_status <> 'discarded'"
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM inventory_asset_drafts draft WHERE draft.task_id::text=$1 AND draft.organization_id::text=$2`+visibility, taskID, organizationID).Scan(&total); err != nil {
		return pagination.Result[AssetDraft]{}, fmt.Errorf("count inventory asset drafts: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT draft.id::text, draft.task_id::text, COALESCE(draft.source_asset_id::text,''), draft.operation, draft.draft_status,
		       draft.asset_type, draft.asset_value, draft.normalized_value, COALESCE(draft.asset_name, ''),
		       organization.id::text, organization.organization_code, organization.organization_name,
		       COALESCE(system.id::text, ''), COALESCE(system.system_code, ''), COALESCE(system.system_name, ''),
		       draft.environment, draft.network_scope, COALESCE(area.id::text, ''), COALESCE(area.area_name, ''),
		       draft.status, draft.importance, 'manual',
		       COALESCE(draft.owner_user_id::text, ''), COALESCE(owner.username, ''), COALESCE(owner.display_name, ''),
		       COALESCE(draft.owner_name, ''), COALESCE(draft.notes, ''), draft.expires_at, draft.created_at, draft.updated_at
		FROM inventory_asset_drafts draft
		JOIN organizations organization ON organization.id=draft.organization_id
		LEFT JOIN business_systems system ON system.id=draft.business_system_id AND system.deleted_at IS NULL
		LEFT JOIN asset_network_areas area ON area.id=draft.network_area_id
		LEFT JOIN users owner ON owner.id=draft.owner_user_id
		WHERE draft.task_id::text=$1 AND draft.organization_id::text=$2`+visibility+`
		ORDER BY draft.updated_at DESC, draft.id DESC LIMIT $3 OFFSET $4
	`, taskID, organizationID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[AssetDraft]{}, fmt.Errorf("list inventory asset drafts: %w", err)
	}
	defer rows.Close()
	items := make([]AssetDraft, 0, page.PageSize)
	for rows.Next() {
		item, err := scanAssetDraft(rows)
		if err != nil {
			return pagination.Result[AssetDraft]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[AssetDraft]{}, fmt.Errorf("iterate inventory asset drafts: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) CreateAssetDraft(ctx context.Context, principal auth.Principal, taskID, organizationID string, input asset.CreateInput, requestID, ipAddress string) (AssetDraft, error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("asset.manage") || principal.HasRole("group_admin") {
		return AssetDraft{}, ErrForbidden
	}
	input.OrganizationID = organizationID
	normalizedValue, err := asset.PrepareCreateInput(&input)
	if err != nil {
		return AssetDraft{}, mapAssetError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AssetDraft{}, fmt.Errorf("begin inventory asset draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	assignment, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "network_assets")
	if err != nil {
		return AssetDraft{}, err
	}
	if assignment.AssetNetworkScope != "all" && input.NetworkScope != assignment.AssetNetworkScope {
		return AssetDraft{}, ErrInvalid
	}
	if err := validateDraftReferences(ctx, tx, organizationID, input); err != nil {
		return AssetDraft{}, err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND deleted_at IS NULL)`, input.Type, normalizedValue).Scan(&duplicate); err != nil {
		return AssetDraft{}, fmt.Errorf("check formal asset duplicate: %w", err)
	}
	if duplicate {
		return AssetDraft{}, ErrConflict
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_asset_drafts (
			task_id, organization_id, asset_type, asset_value, normalized_value, asset_name,
			environment, network_scope, network_area_id, business_system_id, status, importance,
			owner_user_id, owner_name, notes, expires_at, created_by, updated_by
		) VALUES ($1::uuid,$2::uuid,$3,$4,$5,NULLIF($6,''),$7,$8,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,NULLIF($13,'')::uuid,NULLIF($14,''),NULLIF($15,''),$16,$17::uuid,$17::uuid)
		RETURNING id::text
	`, taskID, organizationID, input.Type, input.Value, normalizedValue, input.Name, input.Environment, input.NetworkScope,
		input.NetworkAreaID, input.BusinessSystemID, input.Status, input.Importance, input.OwnerUserID, input.OwnerName,
		input.Notes, input.ExpiresAt, principal.UserID).Scan(&id)
	if err != nil {
		return AssetDraft{}, mapDraftDatabaseError(err)
	}
	if err := writeDraftAudit(ctx, tx, principal, organizationID, taskID, id, "inventory_asset_draft.create", "新增填报资产草稿", input.Value, requestID, ipAddress); err != nil {
		return AssetDraft{}, err
	}
	item, err := loadAssetDraft(ctx, tx, id)
	if err != nil {
		return AssetDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDraft{}, fmt.Errorf("commit inventory asset draft: %w", err)
	}
	return item, nil
}

func (s *Service) CreateAssetUpdateDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, sourceAssetID string, input asset.CreateInput, requestID, ipAddress string) (AssetDraft, error) {
	return s.createAssetChangeDraft(ctx, principal, taskID, organizationID, sourceAssetID, "update", input, requestID, ipAddress)
}

func (s *Service) CreateAssetDeleteDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, sourceAssetID, requestID, ipAddress string) (AssetDraft, error) {
	return s.createAssetChangeDraft(ctx, principal, taskID, organizationID, sourceAssetID, "delete", asset.CreateInput{}, requestID, ipAddress)
}

func (s *Service) createAssetChangeDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, sourceAssetID, operation string, input asset.CreateInput, requestID, ipAddress string) (AssetDraft, error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("asset.manage") || principal.HasRole("group_admin") {
		return AssetDraft{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AssetDraft{}, fmt.Errorf("begin inventory asset change draft: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	assignment, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "network_assets")
	if err != nil {
		return AssetDraft{}, err
	}
	var current asset.CreateInput
	var normalizedValue string
	err = tx.QueryRow(ctx, `
		SELECT formal.asset_type,formal.asset_value,formal.normalized_value,COALESCE(formal.asset_name,''),owner.organization_id::text,
		       COALESCE(link.business_system_id::text,''),formal.environment,formal.network_scope,COALESCE(formal.network_area_id::text,''),
		       formal.status,formal.importance,COALESCE(owner.owner_user_id::text,''),COALESCE(owner.owner_name,''),COALESCE(formal.notes,''),formal.expires_at
		FROM assets formal JOIN asset_organizations owner ON owner.asset_id=formal.id AND owner.is_primary
		LEFT JOIN system_assets link ON link.asset_id=formal.id AND link.is_primary
		WHERE formal.id::text=$1 AND owner.organization_id::text=$2 AND formal.deleted_at IS NULL
	`, sourceAssetID, organizationID).Scan(&current.Type, &current.Value, &normalizedValue, &current.Name, &current.OrganizationID, &current.BusinessSystemID, &current.Environment, &current.NetworkScope, &current.NetworkAreaID, &current.Status, &current.Importance, &current.OwnerUserID, &current.OwnerName, &current.Notes, &current.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssetDraft{}, ErrNotFound
	}
	if err != nil {
		return AssetDraft{}, fmt.Errorf("load formal asset for draft: %w", err)
	}
	if assignment.AssetNetworkScope != "all" && current.NetworkScope != assignment.AssetNetworkScope {
		return AssetDraft{}, ErrInvalid
	}
	if operation == "update" {
		input.OrganizationID = organizationID
		normalizedValue, err = asset.PrepareCreateInput(&input)
		if err != nil {
			return AssetDraft{}, mapAssetError(err)
		}
		if assignment.AssetNetworkScope != "all" && input.NetworkScope != assignment.AssetNetworkScope {
			return AssetDraft{}, ErrInvalid
		}
		if err := validateDraftReferences(ctx, tx, organizationID, input); err != nil {
			return AssetDraft{}, err
		}
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND id::text<>$3 AND deleted_at IS NULL)`, input.Type, normalizedValue, sourceAssetID).Scan(&duplicate); err != nil {
			return AssetDraft{}, fmt.Errorf("check formal asset duplicate: %w", err)
		}
		if duplicate {
			return AssetDraft{}, ErrConflict
		}
		current = input
	}
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_asset_drafts(task_id,organization_id,operation,source_asset_id,asset_type,asset_value,normalized_value,asset_name,environment,network_scope,network_area_id,business_system_id,status,importance,owner_user_id,owner_name,notes,expires_at,created_by,updated_by)
		VALUES($1::uuid,$2::uuid,$3,$4::uuid,$5,$6,$7,NULLIF($8,''),$9,$10,NULLIF($11,'')::uuid,NULLIF($12,'')::uuid,$13,$14,NULLIF($15,'')::uuid,NULLIF($16,''),NULLIF($17,''),$18,$19::uuid,$19::uuid)
		ON CONFLICT (task_id,organization_id,source_asset_id) WHERE source_asset_id IS NOT NULL DO UPDATE SET
		operation=EXCLUDED.operation,asset_type=EXCLUDED.asset_type,asset_value=EXCLUDED.asset_value,normalized_value=EXCLUDED.normalized_value,asset_name=EXCLUDED.asset_name,
		environment=EXCLUDED.environment,network_scope=EXCLUDED.network_scope,network_area_id=EXCLUDED.network_area_id,business_system_id=EXCLUDED.business_system_id,
		status=EXCLUDED.status,importance=EXCLUDED.importance,owner_user_id=EXCLUDED.owner_user_id,owner_name=EXCLUDED.owner_name,notes=EXCLUDED.notes,expires_at=EXCLUDED.expires_at,
		draft_status='editing',updated_by=EXCLUDED.updated_by,updated_at=now()
		WHERE inventory_asset_drafts.draft_status IN ('editing','returned') RETURNING id::text
	`, taskID, organizationID, operation, sourceAssetID, current.Type, current.Value, normalizedValue, current.Name, current.Environment, current.NetworkScope, current.NetworkAreaID, current.BusinessSystemID, current.Status, current.Importance, current.OwnerUserID, current.OwnerName, current.Notes, current.ExpiresAt, principal.UserID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssetDraft{}, ErrState
	}
	if err != nil {
		return AssetDraft{}, mapDraftDatabaseError(err)
	}
	summary := "创建已有资产修改草稿"
	if operation == "delete" {
		summary = "创建已有资产删除草稿"
	}
	if err := writeDraftAudit(ctx, tx, principal, organizationID, taskID, id, "inventory_asset_draft."+operation, summary, current.Value, requestID, ipAddress); err != nil {
		return AssetDraft{}, err
	}
	item, err := loadAssetDraft(ctx, tx, id)
	if err != nil {
		return AssetDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDraft{}, fmt.Errorf("commit inventory asset change draft: %w", err)
	}
	return item, nil
}

func (s *Service) UpdateAssetDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID string, input asset.CreateInput, requestID, ipAddress string) (AssetDraft, error) {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("asset.manage") || principal.HasRole("group_admin") {
		return AssetDraft{}, ErrForbidden
	}
	input.OrganizationID = organizationID
	normalizedValue, err := asset.PrepareCreateInput(&input)
	if err != nil {
		return AssetDraft{}, mapAssetError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AssetDraft{}, fmt.Errorf("begin inventory asset draft update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	assignment, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "network_assets")
	if err != nil {
		return AssetDraft{}, err
	}
	if assignment.AssetNetworkScope != "all" && input.NetworkScope != assignment.AssetNetworkScope {
		return AssetDraft{}, ErrInvalid
	}
	if err := validateDraftReferences(ctx, tx, organizationID, input); err != nil {
		return AssetDraft{}, err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND id<>COALESCE((SELECT source_asset_id FROM inventory_asset_drafts WHERE id::text=$3),gen_random_uuid()) AND deleted_at IS NULL)`, input.Type, normalizedValue, draftID).Scan(&duplicate); err != nil {
		return AssetDraft{}, fmt.Errorf("check formal asset duplicate: %w", err)
	}
	if duplicate {
		return AssetDraft{}, ErrConflict
	}
	result, err := tx.Exec(ctx, `
		UPDATE inventory_asset_drafts SET asset_type=$5,asset_value=$6,normalized_value=$7,asset_name=NULLIF($8,''),
		       environment=$9,network_scope=$10,network_area_id=NULLIF($11,'')::uuid,business_system_id=NULLIF($12,'')::uuid,
		       status=$13,importance=$14,owner_user_id=NULLIF($15,'')::uuid,owner_name=NULLIF($16,''),notes=NULLIF($17,''),
		       expires_at=$18,draft_status='editing',updated_by=$19::uuid,updated_at=now()
		WHERE id::text=$4 AND task_id::text=$1 AND organization_id::text=$2 AND operation IN ('create','update') AND draft_status IN ('editing','returned')
	`, taskID, organizationID, principal.UserID, draftID, input.Type, input.Value, normalizedValue, input.Name,
		input.Environment, input.NetworkScope, input.NetworkAreaID, input.BusinessSystemID, input.Status, input.Importance,
		input.OwnerUserID, input.OwnerName, input.Notes, input.ExpiresAt, principal.UserID)
	if err != nil {
		return AssetDraft{}, mapDraftDatabaseError(err)
	}
	if result.RowsAffected() == 0 {
		return AssetDraft{}, ErrState
	}
	if err := writeDraftAudit(ctx, tx, principal, organizationID, taskID, draftID, "inventory_asset_draft.update", "更新填报资产草稿", input.Value, requestID, ipAddress); err != nil {
		return AssetDraft{}, err
	}
	item, err := loadAssetDraft(ctx, tx, draftID)
	if err != nil {
		return AssetDraft{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AssetDraft{}, fmt.Errorf("commit inventory asset draft update: %w", err)
	}
	return item, nil
}

func (s *Service) DeleteAssetDraft(ctx context.Context, principal auth.Principal, taskID, organizationID, draftID, requestID, ipAddress string) error {
	if !principal.HasPermission("inventory.manage") || !principal.HasPermission("asset.manage") || principal.HasRole("group_admin") {
		return ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin inventory asset draft delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := s.loadDraftAssignment(ctx, tx, principal, taskID, organizationID, true, "network_assets"); err != nil {
		return err
	}
	result, err := tx.Exec(ctx, `DELETE FROM inventory_asset_drafts WHERE id::text=$1 AND task_id::text=$2 AND organization_id::text=$3 AND draft_status IN ('editing','returned')`, draftID, taskID, organizationID)
	if err != nil {
		return fmt.Errorf("delete inventory asset draft: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrState
	}
	if err := writeDraftAudit(ctx, tx, principal, organizationID, taskID, draftID, "inventory_asset_draft.delete", "删除填报资产草稿", "", requestID, ipAddress); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) loadDraftAssignment(ctx context.Context, q queryRower, principal auth.Principal, taskID, organizationID string, editing bool, resource string) (draftAssignment, error) {
	if principal.HasRole("group_admin") {
		if editing {
			return draftAssignment{}, ErrForbidden
		}
		var item draftAssignment
		err := q.QueryRow(ctx, `SELECT task.task_name,organization.organization_name,progress.status,task.asset_network_scope FROM inventory_tasks task JOIN inventory_task_organizations progress ON progress.task_id=task.id JOIN organizations organization ON organization.id=progress.organization_id WHERE task.id::text=$1 AND progress.organization_id::text=$2`, taskID, organizationID).Scan(&item.TaskName, &item.OrganizationName, &item.ProgressStatus, &item.AssetNetworkScope)
		if errors.Is(err, pgx.ErrNoRows) {
			return draftAssignment{}, ErrNotFound
		}
		if err != nil {
			return draftAssignment{}, fmt.Errorf("load inventory draft assignment: %w", err)
		}
		return item, nil
	}
	query := contributorUpdatePrefix + `
		SELECT task.task_name, organization.organization_name, progress.status, task.asset_network_scope
		FROM inventory_tasks task
		JOIN inventory_task_organizations progress ON progress.task_id=task.id
		JOIN organizations organization ON organization.id=progress.organization_id
		WHERE task.id::text=$2 AND progress.organization_id::text=$3
		  AND progress.organization_id IN (SELECT id FROM accessible)`
	if editing {
		query += ` AND task.status='active' AND task.start_at<=now() AND (task.deadline_at>=now() OR task.allow_late_submission)
		           AND progress.status IN ('not_started','in_progress','returned','overdue') AND (CASE WHEN $4='business_systems' THEN (task.required_fields ? 'business_systems' OR task.required_fields ? 'data_security') ELSE task.required_fields ? $4 END)`
	}
	args := []any{principal.UserID, taskID, organizationID}
	if editing {
		args = append(args, resource)
	}
	var item draftAssignment
	err := q.QueryRow(ctx, query, args...).Scan(&item.TaskName, &item.OrganizationName, &item.ProgressStatus, &item.AssetNetworkScope)
	if errors.Is(err, pgx.ErrNoRows) {
		if editing {
			return draftAssignment{}, ErrState
		}
		return draftAssignment{}, ErrNotFound
	}
	if err != nil {
		return draftAssignment{}, fmt.Errorf("load inventory draft assignment: %w", err)
	}
	return item, nil
}

func validateDraftReferences(ctx context.Context, q queryRower, organizationID string, input asset.CreateInput) error {
	var organizationStatus string
	if err := q.QueryRow(ctx, `SELECT status FROM organizations WHERE id::text=$1 AND deleted_at IS NULL`, organizationID).Scan(&organizationStatus); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("load draft organization: %w", err)
	}
	if organizationStatus != "enabled" {
		return ErrInvalid
	}
	if input.NetworkAreaID != "" {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM asset_network_areas WHERE id::text=$1 AND organization_id::text=$2 AND status='enabled')`, input.NetworkAreaID, organizationID).Scan(&ok); err != nil {
			return fmt.Errorf("validate draft network area: %w", err)
		}
		if !ok {
			return ErrInvalid
		}
	}
	if input.BusinessSystemID != "" {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_systems WHERE id::text=$1 AND owner_organization_id::text=$2 AND deleted_at IS NULL)`, input.BusinessSystemID, organizationID).Scan(&ok); err != nil {
			return fmt.Errorf("validate draft business system: %w", err)
		}
		if !ok {
			return ErrInvalid
		}
	}
	if input.OwnerUserID != "" {
		var ok bool
		if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users user_account JOIN user_organizations membership ON membership.user_id=user_account.id WHERE user_account.id::text=$1 AND membership.organization_id::text=$2 AND user_account.status='enabled' AND user_account.deleted_at IS NULL)`, input.OwnerUserID, organizationID).Scan(&ok); err != nil {
			return fmt.Errorf("validate draft owner: %w", err)
		}
		if !ok {
			return ErrInvalid
		}
	}
	return nil
}

func loadAssetDraft(ctx context.Context, q queryRower, draftID string) (AssetDraft, error) {
	row := q.QueryRow(ctx, `
		SELECT draft.id::text, draft.task_id::text, COALESCE(draft.source_asset_id::text,''), draft.operation, draft.draft_status,
		       draft.asset_type, draft.asset_value, draft.normalized_value, COALESCE(draft.asset_name, ''),
		       organization.id::text, organization.organization_code, organization.organization_name,
		       COALESCE(system.id::text, ''), COALESCE(system.system_code, ''), COALESCE(system.system_name, ''),
		       draft.environment, draft.network_scope, COALESCE(area.id::text, ''), COALESCE(area.area_name, ''),
		       draft.status, draft.importance, 'manual', COALESCE(draft.owner_user_id::text, ''),
		       COALESCE(owner.username, ''), COALESCE(owner.display_name, ''), COALESCE(draft.owner_name, ''),
		       COALESCE(draft.notes, ''), draft.expires_at, draft.created_at, draft.updated_at
		FROM inventory_asset_drafts draft JOIN organizations organization ON organization.id=draft.organization_id
		LEFT JOIN business_systems system ON system.id=draft.business_system_id AND system.deleted_at IS NULL
		LEFT JOIN asset_network_areas area ON area.id=draft.network_area_id LEFT JOIN users owner ON owner.id=draft.owner_user_id
		WHERE draft.id::text=$1
	`, draftID)
	return scanAssetDraft(row)
}

func scanAssetDraft(row rowScanner) (AssetDraft, error) {
	var item AssetDraft
	err := row.Scan(&item.ID, &item.TaskID, &item.SourceAssetID, &item.Operation, &item.DraftStatus,
		&item.Type, &item.Value, &item.NormalizedValue, &item.Name,
		&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
		&item.BusinessSystemID, &item.BusinessSystemCode, &item.BusinessSystemName,
		&item.Environment, &item.NetworkScope, &item.NetworkAreaID, &item.NetworkAreaName,
		&item.Status, &item.Importance, &item.Source, &item.OwnerUserID, &item.OwnerUsername,
		&item.OwnerUserDisplayName, &item.OwnerName, &item.Notes, &item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AssetDraft{}, ErrNotFound
	}
	if err != nil {
		return AssetDraft{}, fmt.Errorf("scan inventory asset draft: %w", err)
	}
	item.IsDraft = true
	return item, nil
}

func writeDraftAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, organizationID, taskID, draftID, action, summary, value, requestID, ipAddress string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES ($1::uuid,$2::uuid,$3,'inventory_asset_draft',$4,$5,jsonb_build_object('task_id',$6::text,'asset_value',$7::text),NULLIF($8,'')::inet,NULLIF($9,''))`, principal.UserID, organizationID, action, draftID, summary, taskID, value, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write inventory asset draft audit: %w", err)
	}
	return nil
}

func mapAssetError(err error) error {
	switch {
	case errors.Is(err, asset.ErrInvalid):
		return ErrInvalid
	case errors.Is(err, asset.ErrConflict):
		return ErrConflict
	case errors.Is(err, asset.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}

func mapDraftDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("write inventory asset draft: %w", err)
}

func publishAssetDrafts(ctx context.Context, tx pgx.Tx, principal auth.Principal, taskID, organizationID, requestID, ipAddress string) error {
	rows, err := tx.Query(ctx, `SELECT id::text,operation,COALESCE(source_asset_id::text,''),asset_type,asset_value,normalized_value,COALESCE(asset_name,''),environment,network_scope,COALESCE(network_area_id::text,''),COALESCE(business_system_id::text,''),status,importance,COALESCE(owner_user_id::text,''),COALESCE(owner_name,''),COALESCE(notes,''),expires_at FROM inventory_asset_drafts WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status='submitted' ORDER BY created_at FOR UPDATE`, taskID, organizationID)
	if err != nil {
		return fmt.Errorf("load submitted asset drafts: %w", err)
	}
	type draft struct {
		id, operation, sourceID, typ, value, normalized, name, environment, scope, areaID, systemID, status, importance, ownerUserID, ownerName, notes string
		expiresAt                                                                                                                                      *time.Time
	}
	items := make([]draft, 0)
	for rows.Next() {
		var item draft
		if err := rows.Scan(&item.id, &item.operation, &item.sourceID, &item.typ, &item.value, &item.normalized, &item.name, &item.environment, &item.scope, &item.areaID, &item.systemID, &item.status, &item.importance, &item.ownerUserID, &item.ownerName, &item.notes, &item.expiresAt); err != nil {
			rows.Close()
			return fmt.Errorf("scan submitted asset draft: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate submitted asset drafts: %w", err)
	}
	rows.Close()
	for _, item := range items {
		resourceID := item.id
		action, summary := "asset.create", "审核发布填报网络资产"
		switch item.operation {
		case "create":
			if _, err := tx.Exec(ctx, `INSERT INTO assets (id,asset_type,asset_value,normalized_value,asset_name,environment,network_scope,network_area_id,status,importance,source,notes,expires_at,created_by,updated_by,first_seen_at,last_seen_at) VALUES ($1::uuid,$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,'')::uuid,$9,$10,'manual',NULLIF($11,''),$12,$13::uuid,$13::uuid,now(),now())`, item.id, item.typ, item.value, item.normalized, item.name, item.environment, item.scope, item.areaID, item.status, item.importance, item.notes, item.expiresAt, principal.UserID); err != nil {
				return mapDraftDatabaseError(err)
			}
		case "update":
			if item.sourceID == "" {
				return ErrInvalid
			}
			resourceID = item.sourceID
			action, summary = "asset.update", "审核发布填报网络资产变更"
			result, err := tx.Exec(ctx, `UPDATE assets SET asset_type=$2,asset_value=$3,normalized_value=$4,asset_name=NULLIF($5,''),environment=$6,network_scope=$7,network_area_id=NULLIF($8,'')::uuid,status=$9,importance=$10,notes=NULLIF($11,''),expires_at=$12,updated_by=$13::uuid,updated_at=now() WHERE id::text=$1 AND deleted_at IS NULL`, item.sourceID, item.typ, item.value, item.normalized, item.name, item.environment, item.scope, item.areaID, item.status, item.importance, item.notes, item.expiresAt, principal.UserID)
			if err != nil {
				return mapDraftDatabaseError(err)
			}
			if result.RowsAffected() == 0 {
				return ErrNotFound
			}
		case "delete":
			if item.sourceID == "" {
				return ErrInvalid
			}
			resourceID = item.sourceID
			action, summary = "asset.delete", "审核发布填报网络资产删除"
			result, err := tx.Exec(ctx, `UPDATE assets SET deleted_at=now(),updated_by=$2::uuid,updated_at=now() WHERE id::text=$1 AND deleted_at IS NULL`, item.sourceID, principal.UserID)
			if err != nil {
				return fmt.Errorf("delete published asset: %w", err)
			}
			if result.RowsAffected() == 0 {
				return ErrNotFound
			}
		default:
			return ErrInvalid
		}
		if item.operation != "delete" {
			if _, err := tx.Exec(ctx, `DELETE FROM asset_organizations WHERE asset_id::text=$1 AND is_primary`, resourceID); err != nil {
				return fmt.Errorf("clear published asset organization: %w", err)
			}
			if _, err := tx.Exec(ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary,owner_user_id,owner_name) VALUES($1::uuid,$2::uuid,'owner',true,NULLIF($3,'')::uuid,NULLIF($4,''))`, resourceID, organizationID, item.ownerUserID, item.ownerName); err != nil {
				return fmt.Errorf("attach published asset organization: %w", err)
			}
			if _, err := tx.Exec(ctx, `DELETE FROM system_assets WHERE asset_id::text=$1 AND is_primary`, resourceID); err != nil {
				return fmt.Errorf("clear published asset system: %w", err)
			}
			if item.systemID != "" {
				if _, err := tx.Exec(ctx, `INSERT INTO system_assets(business_system_id,asset_id,relationship_type,is_primary,created_by) VALUES($1::uuid,$2::uuid,'uses',true,$3::uuid)`, item.systemID, resourceID, principal.UserID); err != nil {
					return fmt.Errorf("attach published asset system: %w", err)
				}
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,$3,'asset',$4,$5,jsonb_build_object('task_id',$6::text,'type',$7::text,'value',$8::text,'source','inventory_task'),NULLIF($9,'')::inet,NULLIF($10,''))`, principal.UserID, organizationID, action, resourceID, summary, taskID, item.typ, item.value, ipAddress, requestID); err != nil {
			return fmt.Errorf("write published asset audit: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE inventory_asset_drafts SET draft_status='published',published_at=now(),updated_by=$3::uuid,updated_at=now() WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status='submitted'`, taskID, organizationID, principal.UserID); err != nil {
		return fmt.Errorf("mark asset drafts published: %w", err)
	}
	return nil
}
