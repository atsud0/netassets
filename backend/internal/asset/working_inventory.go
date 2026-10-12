package asset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/customfield"
	"netassets/internal/inventorydraft"
	"netassets/internal/notification"
	"netassets/internal/pagination"
)

func usesWorkingInventory(p auth.Principal) bool { return !p.HasRole("group_admin") }

const assetWorkingAccessibleCTE = `
WITH RECURSIVE roots(id) AS (
 SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
 JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
 JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
 WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), descendants(id) AS (
 SELECT id FROM roots UNION SELECT o.id FROM organizations o JOIN descendants p ON o.parent_id=p.id WHERE o.status='enabled' AND o.deleted_at IS NULL
), accessible(id) AS (
 SELECT ur.organization_id FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
 JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='asset.manage'
 JOIN organizations o ON o.id=ur.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
 WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
 UNION SELECT id FROM descendants
)`

// searchWorkingCreates returns the organization working ledger. Active drafts
// replace their published source only for organization-side users; headquarters
// continues to query the published assets table through SearchWithFilter.
func (s *Service) searchWorkingCreates(ctx context.Context, p auth.Principal, f SearchFilter) (pagination.Result[Asset], error) {
	if !p.HasPermission("asset.manage") {
		return pagination.Result[Asset]{}, ErrForbidden
	}
	page := f.Page
	if page.Page == 0 {
		page.Page = pagination.DefaultPage
	}
	if page.PageSize == 0 {
		page.PageSize = pagination.DefaultPageSize
	}
	rows, err := s.pool.Query(ctx, assetWorkingAccessibleCTE+`, ledger AS (
 SELECT a.id::text id,a.asset_type,a.asset_value,a.normalized_value,COALESCE(a.asset_name,'') asset_name,o.id::text organization_id,o.organization_code,o.organization_name,
 COALESCE(bs.id::text,'') business_system_id,COALESCE(bs.system_code,'') business_system_code,COALESCE(bs.system_name,'') business_system_name,
 a.environment,a.network_scope,COALESCE(na.id::text,'') network_area_id,COALESCE(na.area_name,'') network_area_name,a.status,a.importance,a.source,
 COALESCE(ao.owner_user_id::text,'') owner_user_id,COALESCE(u.username,'') owner_username,COALESCE(u.display_name,'') owner_display_name,COALESCE(ao.owner_name,'') owner_name,
 COALESCE(a.notes,'') notes,a.expires_at,a.created_at,a.updated_at,'reported'::text reporting_status,false is_working_copy,''::text published_id,''::text task_id,''::text draft_status,''::text operation,'[]'::jsonb custom_values
 FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary JOIN organizations o ON o.id=ao.organization_id
 LEFT JOIN users u ON u.id=ao.owner_user_id LEFT JOIN asset_network_areas na ON na.id=a.network_area_id
 LEFT JOIN LATERAL (SELECT b.id,b.system_code,b.system_name FROM system_assets sa JOIN business_systems b ON b.id=sa.business_system_id WHERE sa.asset_id=a.id AND b.deleted_at IS NULL ORDER BY sa.is_primary DESC,sa.created_at LIMIT 1) bs ON true
 WHERE a.deleted_at IS NULL AND o.id IN (SELECT id FROM accessible)
   AND NOT EXISTS (SELECT 1 FROM inventory_asset_drafts d WHERE d.source_asset_id=a.id AND d.organization_id=o.id AND d.draft_status IN `+inventorydraft.ActiveSQL+`)
 UNION ALL
 SELECT d.id::text,d.asset_type,d.asset_value,d.normalized_value,COALESCE(d.asset_name,''),o.id::text,o.organization_code,o.organization_name,
 COALESCE(wb.id::text,b.id::text,''),COALESCE(wb.system_code,b.system_code,''),COALESCE(wb.system_name,b.system_name,''),d.environment,d.network_scope,
 COALESCE(na.id::text,''),COALESCE(na.area_name,''),d.status,d.importance,d.source,COALESCE(d.owner_user_id::text,''),COALESCE(u.username,''),COALESCE(u.display_name,''),
 COALESCE(d.owner_name,''),COALESCE(d.notes,''),d.expires_at,d.created_at,d.updated_at,
 CASE WHEN d.draft_status='submitted' THEN 'pending_review' WHEN d.draft_status='returned' THEN 'returned' WHEN d.operation='delete' THEN 'pending_offline' WHEN d.operation='update' THEN 'changed' ELSE 'unreported' END,
 true,COALESCE(d.source_asset_id::text,''),COALESCE(d.task_id::text,''),d.draft_status,d.operation,d.custom_values
 FROM inventory_asset_drafts d JOIN organizations o ON o.id=d.organization_id LEFT JOIN users u ON u.id=d.owner_user_id
 LEFT JOIN asset_network_areas na ON na.id=d.network_area_id LEFT JOIN business_systems b ON b.id=d.business_system_id AND b.deleted_at IS NULL
 LEFT JOIN inventory_business_system_drafts wb ON wb.id=d.business_system_draft_id AND wb.draft_status IN `+inventorydraft.ActiveSQL+`
 WHERE d.organization_id IN (SELECT id FROM accessible) AND d.draft_status IN `+inventorydraft.ActiveSQL+`
)
SELECT id,asset_type,asset_value,normalized_value,asset_name,organization_id,organization_code,organization_name,business_system_id,business_system_code,business_system_name,
 environment,network_scope,network_area_id,network_area_name,status,importance,source,owner_user_id,owner_username,owner_display_name,owner_name,notes,expires_at,created_at,updated_at,
 reporting_status,is_working_copy,published_id,task_id,draft_status,operation,custom_values,count(*) OVER()
FROM ledger WHERE ($2='' OR asset_type=$2) AND ($3='' OR status=$3) AND ($4='' OR organization_id=$4)
 AND ($5='' OR concat_ws(' ',asset_value,asset_name,organization_name,business_system_name,business_system_code,network_area_name,owner_name,owner_username,owner_display_name) ILIKE '%'||$5||'%')
 AND (COALESCE(cardinality($6::text[]),0)=0 OR id=ANY($6::text[]))
ORDER BY organization_code,asset_type,normalized_value LIMIT $7 OFFSET $8`, p.UserID, strings.TrimSpace(f.Type), strings.TrimSpace(f.Status), strings.TrimSpace(f.OrganizationID), strings.TrimSpace(f.Keyword), f.SelectedIDs, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Asset]{}, fmt.Errorf("search organization working assets: %w", err)
	}
	defer rows.Close()
	items := make([]Asset, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var a Asset
		var raw []byte
		if err = rows.Scan(&a.ID, &a.Type, &a.Value, &a.NormalizedValue, &a.Name, &a.OrganizationID, &a.OrganizationCode, &a.OrganizationName, &a.BusinessSystemID, &a.BusinessSystemCode, &a.BusinessSystemName, &a.Environment, &a.NetworkScope, &a.NetworkAreaID, &a.NetworkAreaName, &a.Status, &a.Importance, &a.Source, &a.OwnerUserID, &a.OwnerUsername, &a.OwnerUserDisplayName, &a.OwnerName, &a.Notes, &a.ExpiresAt, &a.CreatedAt, &a.UpdatedAt, &a.ReportingStatus, &a.IsWorkingCopy, &a.PublishedID, &a.TaskID, &a.DraftStatus, &a.Operation, &raw, &total); err != nil {
			return pagination.Result[Asset]{}, fmt.Errorf("scan organization working asset: %w", err)
		}
		if err = json.Unmarshal(raw, &a.CustomValues); err != nil {
			return pagination.Result[Asset]{}, err
		}
		items = append(items, a)
	}
	if err = rows.Err(); err != nil {
		return pagination.Result[Asset]{}, err
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) getWorking(ctx context.Context, p auth.Principal, id string) (Asset, error) {
	return s.getWorkingWithPredicate(ctx, p, "d.id::text=$2", id)
}

// getWorkingBySource resolves the active organization work version for a
// published asset. The list API normally returns the draft ID, but accepting a
// published ID here makes batch operations idempotent when the client has a
// stale row or submits a selection captured before the working ledger replaced
// the published row.
func (s *Service) getWorkingBySource(ctx context.Context, p auth.Principal, sourceAssetID string) (Asset, error) {
	return s.getWorkingWithPredicate(ctx, p, "d.source_asset_id::text=$2", sourceAssetID)
}

// workingAssetInTx loads a working version inside an open transaction. The
// pool-backed getWorking refuses group administrators because organization users
// are the audience for the working ledger, but headquarters needs to read the
// row it just created while settling a finding-import candidate in the same
// transaction.
func workingAssetInTx(ctx context.Context, tx pgx.Tx, p auth.Principal, id string) (Asset, error) {
	row := tx.QueryRow(ctx, `
SELECT d.id::text,d.asset_type,d.asset_value,d.normalized_value,COALESCE(d.asset_name,''),o.id::text,o.organization_code,o.organization_name,
 COALESCE(d.business_system_id::text,''),COALESCE(d.business_system_id::text,''),COALESCE(d.business_system_id::text,''),d.environment,d.network_scope,
 COALESCE(d.network_area_id::text,''),COALESCE(na.area_name,''),d.status,d.importance,d.source,COALESCE(d.owner_user_id::text,''),COALESCE(u.username,''),COALESCE(u.display_name,''),
 COALESCE(d.owner_name,''),COALESCE(d.notes,''),d.expires_at,d.created_at,d.updated_at,'unreported',true,COALESCE(d.source_asset_id::text,''),COALESCE(d.task_id::text,''),d.draft_status,d.operation,d.custom_values
FROM inventory_asset_drafts d JOIN organizations o ON o.id=d.organization_id LEFT JOIN users u ON u.id=d.owner_user_id
LEFT JOIN asset_network_areas na ON na.id=d.network_area_id
WHERE d.id::text=$1 AND d.draft_status IN `+inventorydraft.ActiveSQL, id)
	var a Asset
	var raw []byte
	if err := row.Scan(&a.ID, &a.Type, &a.Value, &a.NormalizedValue, &a.Name, &a.OrganizationID, &a.OrganizationCode, &a.OrganizationName, &a.BusinessSystemID, &a.BusinessSystemCode, &a.BusinessSystemName, &a.Environment, &a.NetworkScope, &a.NetworkAreaID, &a.NetworkAreaName, &a.Status, &a.Importance, &a.Source, &a.OwnerUserID, &a.OwnerUsername, &a.OwnerUserDisplayName, &a.OwnerName, &a.Notes, &a.ExpiresAt, &a.CreatedAt, &a.UpdatedAt, &a.ReportingStatus, &a.IsWorkingCopy, &a.PublishedID, &a.TaskID, &a.DraftStatus, &a.Operation, &raw); errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, pgx.ErrNoRows
	} else if err != nil {
		return Asset{}, err
	}
	if err := json.Unmarshal(raw, &a.CustomValues); err != nil {
		return Asset{}, err
	}
	return a, nil
}

func (s *Service) getWorkingWithPredicate(ctx context.Context, p auth.Principal, predicate, id string) (Asset, error) {
	if p.HasRole("group_admin") || !p.HasPermission("asset.manage") {
		return Asset{}, ErrNotFound
	}
	row := s.pool.QueryRow(ctx, assetWorkingAccessibleCTE+`
SELECT d.id::text,d.asset_type,d.asset_value,d.normalized_value,COALESCE(d.asset_name,''),o.id::text,o.organization_code,o.organization_name,
 COALESCE(wb.id::text,b.id::text,''),COALESCE(wb.system_code,b.system_code,''),COALESCE(wb.system_name,b.system_name,''),d.environment,d.network_scope,
 COALESCE(na.id::text,''),COALESCE(na.area_name,''),d.status,d.importance,d.source,COALESCE(d.owner_user_id::text,''),COALESCE(u.username,''),COALESCE(u.display_name,''),
 COALESCE(d.owner_name,''),COALESCE(d.notes,''),d.expires_at,d.created_at,d.updated_at,
 CASE WHEN d.draft_status='submitted' THEN 'pending_review' WHEN d.draft_status='returned' THEN 'returned' WHEN d.operation='delete' THEN 'pending_offline' WHEN d.operation='update' THEN 'changed' ELSE 'unreported' END,
 true,COALESCE(d.source_asset_id::text,''),COALESCE(d.task_id::text,''),d.draft_status,d.operation,d.custom_values
FROM inventory_asset_drafts d JOIN organizations o ON o.id=d.organization_id LEFT JOIN users u ON u.id=d.owner_user_id
LEFT JOIN asset_network_areas na ON na.id=d.network_area_id LEFT JOIN business_systems b ON b.id=d.business_system_id AND b.deleted_at IS NULL
LEFT JOIN inventory_business_system_drafts wb ON wb.id=d.business_system_draft_id AND wb.draft_status IN `+inventorydraft.ActiveSQL+`
WHERE `+predicate+` AND d.organization_id IN (SELECT id FROM accessible) AND d.draft_status IN `+inventorydraft.ActiveSQL+``, p.UserID, id)
	var a Asset
	var raw []byte
	err := row.Scan(&a.ID, &a.Type, &a.Value, &a.NormalizedValue, &a.Name, &a.OrganizationID, &a.OrganizationCode, &a.OrganizationName, &a.BusinessSystemID, &a.BusinessSystemCode, &a.BusinessSystemName, &a.Environment, &a.NetworkScope, &a.NetworkAreaID, &a.NetworkAreaName, &a.Status, &a.Importance, &a.Source, &a.OwnerUserID, &a.OwnerUsername, &a.OwnerUserDisplayName, &a.OwnerName, &a.Notes, &a.ExpiresAt, &a.CreatedAt, &a.UpdatedAt, &a.ReportingStatus, &a.IsWorkingCopy, &a.PublishedID, &a.TaskID, &a.DraftStatus, &a.Operation, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return Asset{}, ErrNotFound
	}
	if err != nil {
		return Asset{}, err
	}
	if err = json.Unmarshal(raw, &a.CustomValues); err != nil {
		return Asset{}, err
	}
	return a, nil
}

func (s *Service) validateWorkingValues(ctx context.Context, values []customfield.Value) ([]byte, error) {
	defs, err := customfield.NewService(s.pool).EnabledDefinitions(ctx, "asset")
	if err != nil {
		return nil, err
	}
	if _, _, err = customfield.ValidateValues(defs, values); err != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(values)
}

func (s *Service) createUnreported(ctx context.Context, p auth.Principal, in CreateInput, requestID, ip string) (Asset, error) {
	return s.createUnreportedWithSource(ctx, p, in, "manual", requestID, ip)
}

func (s *Service) createUnreportedWithSource(ctx context.Context, p auth.Principal, in CreateInput, source, requestID, ip string) (Asset, error) {
	if !p.HasPermission("asset.manage") {
		return Asset{}, ErrForbidden
	}
	if source != "manual" && source != "import" && source != "api" && source != "plugin" && source != "finding_import" {
		return Asset{}, ErrInvalid
	}
	normalized, e := PrepareCreateInput(&in)
	if e != nil {
		return Asset{}, e
	}
	ok, e := access.CanOperateOrganization(ctx, s.pool, p, in.OrganizationID, "asset.manage")
	if e != nil {
		return Asset{}, e
	}
	if !ok {
		return Asset{}, ErrForbidden
	}
	raw, e := s.validateWorkingValues(ctx, in.CustomValues)
	if e != nil {
		return Asset{}, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Asset{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bs, bsd, e := validateUnreportedReferences(ctx, tx, in)
	if e != nil {
		return Asset{}, e
	}
	var duplicate bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM inventory_asset_drafts WHERE asset_type=$1 AND normalized_value=$2 AND draft_status IN `+inventorydraft.ActiveSQL+`)`, in.Type, normalized).Scan(&duplicate)
	if e != nil {
		return Asset{}, e
	}
	if duplicate {
		return Asset{}, ErrConflict
	}
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO inventory_asset_drafts(task_id,organization_id,operation,asset_type,asset_value,normalized_value,asset_name,environment,network_scope,network_area_id,business_system_id,business_system_draft_id,status,importance,source,owner_user_id,owner_name,notes,expires_at,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'create',$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,$13,NULLIF($14,'')::uuid,NULLIF($15,''),NULLIF($16,''),$17,$18::jsonb,'editing',$19::uuid,$19::uuid) RETURNING id::text`, in.OrganizationID, in.Type, in.Value, normalized, in.Name, in.Environment, in.NetworkScope, in.NetworkAreaID, bs, bsd, in.Status, in.Importance, source, in.OwnerUserID, in.OwnerName, in.Notes, in.ExpiresAt, raw, p.UserID).Scan(&id)
	if e != nil {
		return Asset{}, mapUnreportedDB(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'asset.working_create','asset',$3,'新增未上报网络资产',jsonb_build_object('reporting_status','unreported','source',$6::text),NULLIF($4,'')::inet,NULLIF($5,''))`, p.UserID, in.OrganizationID, id, ip, requestID, source); e != nil {
		return Asset{}, e
	}
	if e = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetWorkingVersionCreated, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: in.OrganizationID, ActorUserID: p.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: id, ResourceLabel: workingAssetLabel(in), BatchKey: requestID,
		Details: map[string]any{"operation": "create", "source": source},
	}); e != nil {
		return Asset{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Asset{}, e
	}
	return s.getWorking(ctx, p, id)
}

// CreateWorkingVersion records an organization working version without going
// through a reporting task, and lets the caller settle related rows in the same
// transaction.
//
// Headquarters uses this when it confirms an asset that a finding import found
// but the asset register does not hold yet. Writing the formal assets table
// directly skipped the working version, and with it the snapshot, the quality
// checks and the publish/rollback path every other asset change follows. Going
// through the working version also leaves the organization that owns the address
// as the party that fills in the missing details instead of the reviewer.
func (s *Service) CreateWorkingVersion(ctx context.Context, p auth.Principal, in CreateInput, source, requestID, ip string, afterCreate func(pgx.Tx, Asset) error) (Asset, error) {
	if source != "manual" && source != "import" && source != "api" && source != "plugin" && source != "finding_import" {
		return Asset{}, ErrInvalid
	}
	if afterCreate == nil {
		return s.createUnreportedWithSource(ctx, p, in, source, requestID, ip)
	}
	normalized, err := PrepareCreateInput(&in)
	if err != nil {
		return Asset{}, err
	}
	raw, err := s.validateWorkingValues(ctx, in.CustomValues)
	if err != nil {
		return Asset{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Asset{}, fmt.Errorf("begin asset working version: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	systemID, systemDraftID, err := validateUnreportedReferences(ctx, tx, in)
	if err != nil {
		return Asset{}, err
	}
	var duplicate bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND deleted_at IS NULL) OR EXISTS(SELECT 1 FROM inventory_asset_drafts WHERE asset_type=$1 AND normalized_value=$2 AND draft_status IN `+inventorydraft.ActiveSQL+`)`, in.Type, normalized).Scan(&duplicate); err != nil {
		return Asset{}, fmt.Errorf("check asset working version duplicate: %w", err)
	}
	if duplicate {
		return Asset{}, ErrConflict
	}
	var id string
	if err := tx.QueryRow(ctx, `INSERT INTO inventory_asset_drafts(task_id,organization_id,operation,asset_type,asset_value,normalized_value,asset_name,environment,network_scope,network_area_id,business_system_id,business_system_draft_id,status,importance,source,owner_user_id,owner_name,notes,expires_at,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'create',$2,$3,$4,NULLIF($5,''),$6,$7,NULLIF($8,'')::uuid,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,$13,NULLIF($14,'')::uuid,NULLIF($15,''),NULLIF($16,''),$17,$18::jsonb,'editing',$19::uuid,$19::uuid) RETURNING id::text`, in.OrganizationID, in.Type, in.Value, normalized, in.Name, in.Environment, in.NetworkScope, in.NetworkAreaID, systemID, systemDraftID, in.Status, in.Importance, source, in.OwnerUserID, in.OwnerName, in.Notes, in.ExpiresAt, raw, p.UserID).Scan(&id); err != nil {
		return Asset{}, mapUnreportedDB(err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'asset.working_create','asset',$3,'新增未上报网络资产',jsonb_build_object('reporting_status','unreported','source',$6::text),NULLIF($4,'')::inet,NULLIF($5,''))`, p.UserID, in.OrganizationID, id, ip, requestID, source); err != nil {
		return Asset{}, fmt.Errorf("write asset working version audit: %w", err)
	}
	created, err := workingAssetInTx(ctx, tx, p, id)
	if err != nil {
		return Asset{}, err
	}
	if err := afterCreate(tx, created); err != nil {
		return Asset{}, err
	}
	if err := notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetWorkingVersionCreated, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: in.OrganizationID, ActorUserID: p.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: id, ResourceLabel: workingAssetLabel(in), BatchKey: requestID,
		Details: map[string]any{"operation": "create", "source": source},
	}); err != nil {
		return Asset{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Asset{}, fmt.Errorf("commit asset working version: %w", err)
	}
	return created, nil
}

func (s *Service) updateWorking(ctx context.Context, p auth.Principal, id string, in CreateInput, requestID, ip string) (Asset, error) {
	cur, e := s.getWorking(ctx, p, id)
	if e != nil {
		return Asset{}, e
	}
	if cur.TaskID != "" || cur.DraftStatus == "submitted" || cur.Operation == "delete" {
		return Asset{}, ErrInvalid
	}
	if cur.OrganizationID != in.OrganizationID {
		return Asset{}, ErrForbidden
	}
	normalized, e := PrepareCreateInput(&in)
	if e != nil {
		return Asset{}, e
	}
	raw, e := s.validateWorkingValues(ctx, in.CustomValues)
	if e != nil {
		return Asset{}, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Asset{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bs, bsd, e := validateUnreportedReferences(ctx, tx, in)
	if e != nil {
		return Asset{}, e
	}
	// Editing an existing working version can retype or revalue it, so the pair
	// must be re-reserved rather than assumed still held by this row.
	if e = s.ensureWorkingValueAvailable(ctx, tx, in.Type, normalized, id); e != nil {
		return Asset{}, e
	}
	r, e := tx.Exec(ctx, `UPDATE inventory_asset_drafts SET asset_type=$2,asset_value=$3,normalized_value=$4,asset_name=NULLIF($5,''),environment=$6,network_scope=$7,network_area_id=NULLIF($8,'')::uuid,business_system_id=NULLIF($9,'')::uuid,business_system_draft_id=NULLIF($10,'')::uuid,status=$11,importance=$12,owner_user_id=NULLIF($13,'')::uuid,owner_name=NULLIF($14,''),notes=NULLIF($15,''),expires_at=$16,custom_values=$17::jsonb,draft_status='editing',updated_by=$18::uuid,updated_at=now() WHERE id::text=$1 AND task_id IS NULL AND operation IN ('create','update') AND draft_status IN `+inventorydraft.EditableSQL+` AND ($19::timestamptz IS NULL OR updated_at=$19::timestamptz)`, id, in.Type, in.Value, normalized, in.Name, in.Environment, in.NetworkScope, in.NetworkAreaID, bs, bsd, in.Status, in.Importance, in.OwnerUserID, in.OwnerName, in.Notes, in.ExpiresAt, raw, p.UserID, in.ExpectedUpdatedAt)
	if e != nil {
		return Asset{}, mapUnreportedDB(e)
	}
	if r.RowsAffected() == 0 {
		if in.ExpectedUpdatedAt != nil {
			return Asset{}, ErrEditConflict
		}
		return Asset{}, ErrNotFound
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'asset.working_update','asset',$3,'编辑网络资产工作版本',jsonb_build_object('published_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, in.OrganizationID, id, cur.PublishedID, ip, requestID); e != nil {
		return Asset{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Asset{}, e
	}
	return s.getWorking(ctx, p, id)
}

func (s *Service) createWorkingUpdate(ctx context.Context, p auth.Principal, current Asset, in CreateInput, requestID, ip string) (Asset, error) {
	if current.OrganizationID != in.OrganizationID {
		return Asset{}, ErrForbidden
	}
	normalized, e := PrepareCreateInput(&in)
	if e != nil {
		return Asset{}, e
	}
	raw, e := s.validateWorkingValues(ctx, in.CustomValues)
	if e != nil {
		return Asset{}, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return Asset{}, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	bs, bsd, e := validateUnreportedReferences(ctx, tx, in)
	if e != nil {
		return Asset{}, e
	}
	if e = s.ensureWorkingValueAvailable(ctx, tx, in.Type, normalized, ""); e != nil {
		return Asset{}, e
	}
	var id string
	e = tx.QueryRow(ctx, `INSERT INTO inventory_asset_drafts(task_id,organization_id,operation,source_asset_id,asset_type,asset_value,normalized_value,asset_name,environment,network_scope,network_area_id,business_system_id,business_system_draft_id,status,importance,source,owner_user_id,owner_name,notes,expires_at,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'update',$2::uuid,$3,$4,$5,NULLIF($6,''),$7,$8,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,NULLIF($11,'')::uuid,$12,$13,$14,NULLIF($15,'')::uuid,NULLIF($16,''),NULLIF($17,''),$18,$19::jsonb,'editing',$20::uuid,$20::uuid) RETURNING id::text`, in.OrganizationID, current.ID, in.Type, in.Value, normalized, in.Name, in.Environment, in.NetworkScope, in.NetworkAreaID, bs, bsd, in.Status, in.Importance, current.Source, in.OwnerUserID, in.OwnerName, in.Notes, in.ExpiresAt, raw, p.UserID).Scan(&id)
	if e != nil {
		return Asset{}, mapUnreportedDB(e)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'asset.working_change','asset',$3,'创建网络资产待上报变更',jsonb_build_object('published_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, in.OrganizationID, id, current.ID, ip, requestID); e != nil {
		return Asset{}, e
	}
	if e = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetWorkingVersionCreated, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: in.OrganizationID, ActorUserID: p.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: id, ResourceLabel: workingAssetLabel(in), BatchKey: requestID,
		Details: map[string]any{"operation": "update", "published_id": current.ID},
	}); e != nil {
		return Asset{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return Asset{}, e
	}
	return s.getWorking(ctx, p, id)
}

func (s *Service) deleteWorking(ctx context.Context, p auth.Principal, id, requestID, ip string) error {
	cur, e := s.getWorking(ctx, p, id)
	if e != nil {
		return e
	}
	if cur.TaskID != "" || cur.DraftStatus == "submitted" {
		return ErrInvalid
	}
	ok, e := access.CanOperateOrganization(ctx, s.pool, p, cur.OrganizationID, "asset.manage")
	if e != nil {
		return e
	}
	if !ok {
		return ErrForbidden
	}
	if cur.Operation == "create" {
		return s.discardWorkingCreate(ctx, p, id)
	}
	r, e := s.pool.Exec(ctx, `UPDATE inventory_asset_drafts SET operation='delete',draft_status='editing',updated_by=$2::uuid,updated_at=now() WHERE id::text=$1 AND task_id IS NULL AND source_asset_id IS NOT NULL AND draft_status IN `+inventorydraft.EditableSQL+``, id, p.UserID)
	if e != nil {
		return e
	}
	if r.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// discardWorkingCreate drops an unpublished "create" working version.
//
// A working version confirmed from a finding-import asset candidate is not just
// a draft: security_findings rows and asset_candidates rows already point at it.
// Discarding it while leaving those pointers behind used to strand everything.
// asset_candidates.Accept refuses to run again once working_version_draft_id is
// set, so the candidate could never be re-confirmed, and the findings kept a
// pending_asset_draft_id pointing at a row that can never be published, leaving
// the "pending asset" dashboard count permanently inflated with no way out.
//
// Unwinding in one transaction keeps the three tables consistent: the candidate
// returns to a confirmable state, the findings drop back to unlinked so the
// "pending asset" queue shows them again, and the import rows return to the
// candidate so they can be re-linked.
func (s *Service) discardWorkingCreate(ctx context.Context, p auth.Principal, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin discard working asset create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE inventory_asset_drafts SET draft_status='discarded',updated_by=$2::uuid,updated_at=now()
		WHERE id::text=$1 AND task_id IS NULL AND operation='create' AND draft_status IN `+inventorydraft.EditableSQL+``, id, p.UserID)
	if err != nil {
		return fmt.Errorf("discard working asset create: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Release the candidate and reopen all of its import rows in one statement.
	// A candidate can be discarded after the operator has released its findings,
	// so those rows may already be marked imported/duplicate. Keeping them in
	// that state would make retry impossible even though the candidate is pending
	// again. The CTE scopes the reset to this draft; it must not touch unrelated
	// pending candidates.
	if _, err := tx.Exec(ctx, `
		WITH released AS (
			UPDATE asset_candidates
			SET working_version_draft_id=NULL, status='pending', reviewed_by=NULL, reviewed_at=NULL,
			    review_note=NULL, updated_at=now()
			WHERE working_version_draft_id=$1::uuid AND status='pending'
			RETURNING id
		)
		UPDATE security_finding_import_candidates
		SET status='pending', matched_asset_id=NULL, matched_scope_asset_id=NULL,
		    imported_finding_id=NULL, reviewed_by=NULL, reviewed_at=NULL,
		    review_note=NULL, updated_at=now()
		WHERE asset_candidate_id IN (SELECT id FROM released)
		  AND status IN ('pending','imported','duplicate')`, id); err != nil {
		return fmt.Errorf("release asset candidate after discarding working version: %w", err)
	}

	// A finding parked on the discarded draft has no asset and no draft left, so
	// it must become visible as unlinked again instead of waiting forever.
	if _, err := tx.Exec(ctx, `
		UPDATE security_findings
		SET pending_asset_draft_id=NULL, updated_by=$2::uuid, updated_at=now()
		WHERE pending_asset_draft_id=$1::uuid`, id, p.UserID); err != nil {
		return fmt.Errorf("release findings from discarded working version: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit discard working asset create: %w", err)
	}
	return nil
}

func (s *Service) createWorkingDelete(ctx context.Context, p auth.Principal, current Asset, requestID, ip string) error {
	ok, err := access.CanOperateOrganization(ctx, s.pool, p, current.OrganizationID, "asset.manage")
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	values, err := customfield.ValuesInTx(ctx, tx, "asset", current.ID)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	var id string
	err = tx.QueryRow(ctx, `INSERT INTO inventory_asset_drafts(task_id,organization_id,operation,source_asset_id,asset_type,asset_value,normalized_value,asset_name,environment,network_scope,network_area_id,business_system_id,status,importance,source,owner_user_id,owner_name,notes,expires_at,custom_values,draft_status,created_by,updated_by) VALUES(NULL,$1::uuid,'delete',$2::uuid,$3,$4,$5,NULLIF($6,''),$7,$8,NULLIF($9,'')::uuid,NULLIF($10,'')::uuid,$11,$12,$13,NULLIF($14,'')::uuid,NULLIF($15,''),NULLIF($16,''),$17,$18::jsonb,'editing',$19::uuid,$19::uuid) RETURNING id::text`, current.OrganizationID, current.ID, current.Type, current.Value, current.NormalizedValue, current.Name, current.Environment, current.NetworkScope, current.NetworkAreaID, current.BusinessSystemID, current.Status, current.Importance, current.Source, current.OwnerUserID, current.OwnerName, current.Notes, current.ExpiresAt, raw, p.UserID).Scan(&id)
	if err != nil {
		return mapUnreportedDB(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id) VALUES($1::uuid,$2::uuid,'asset.working_delete','asset',$3,'创建网络资产下线申请',jsonb_build_object('published_id',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, current.OrganizationID, id, current.ID, ip, requestID); err != nil {
		return err
	}
	if err = notification.WriteEvent(ctx, tx, notification.EventInput{
		EventType: notification.EventAssetWorkingVersionCreated, Category: notification.CategoryWorkingVersion,
		TargetOrganizationID: current.OrganizationID, ActorUserID: p.UserID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: id, ResourceLabel: workingAssetLabel(CreateInput{Name: current.Name, Value: current.Value}), BatchKey: requestID,
		Details: map[string]any{"operation": "delete", "published_id": current.ID},
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func workingAssetLabel(input CreateInput) string {
	if label := strings.TrimSpace(input.Name); label != "" {
		return label
	}
	return strings.TrimSpace(input.Value)
}

func validateUnreportedReferences(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, in CreateInput) (string, string, error) {
	var st string
	if e := q.QueryRow(ctx, `SELECT status FROM organizations WHERE id::text=$1 AND deleted_at IS NULL`, in.OrganizationID).Scan(&st); errors.Is(e, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	} else if e != nil {
		return "", "", e
	}
	if st != "enabled" {
		return "", "", ErrInvalid
	}
	if in.NetworkAreaID != "" {
		var ok bool
		if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM asset_network_areas WHERE id::text=$1 AND organization_id::text=$2 AND status='enabled')`, in.NetworkAreaID, in.OrganizationID).Scan(&ok); e != nil {
			return "", "", e
		} else if !ok {
			return "", "", ErrInvalid
		}
	}
	if in.OwnerUserID != "" {
		var ok bool
		if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_organizations uo ON uo.user_id=u.id WHERE u.id::text=$1 AND uo.organization_id::text=$2 AND u.status='enabled' AND u.deleted_at IS NULL)`, in.OwnerUserID, in.OrganizationID).Scan(&ok); e != nil {
			return "", "", e
		} else if !ok {
			return "", "", ErrInvalid
		}
	}
	bs, bsd := "", ""
	if in.BusinessSystemID != "" {
		var d bool
		if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM inventory_business_system_drafts WHERE id::text=$1 AND organization_id::text=$2 AND operation<>'delete' AND draft_status IN `+inventorydraft.ActiveSQL+`)`, in.BusinessSystemID, in.OrganizationID).Scan(&d); e != nil {
			return "", "", e
		}
		if d {
			bsd = in.BusinessSystemID
		} else {
			var ok bool
			if e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM business_systems WHERE id::text=$1 AND owner_organization_id::text=$2 AND deleted_at IS NULL)`, in.BusinessSystemID, in.OrganizationID).Scan(&ok); e != nil {
				return "", "", e
			} else if !ok {
				return "", "", ErrInvalid
			}
			bs = in.BusinessSystemID
		}
	}
	return bs, bsd, nil
}
func mapUnreportedDB(e error) error {
	var pe *pgconn.PgError
	if errors.As(e, &pe) && pe.Code == "23505" {
		return ErrConflict
	}
	return e
}

// ensureWorkingValueAvailable reserves the (asset_type, normalized_value) pair
// for a working version. Both the published ledger and every other active draft
// count as a reservation, because the organization ledger hides a published row
// as soon as it has an active draft — a draft that duplicates an unrelated
// published asset would therefore collide only later, at publish time, where the
// failure is far harder to attribute.
//
// excludeDraftID is the row being rewritten, so re-submitting an asset without
// changing its type or value does not report a conflict against itself.
func (s *Service) ensureWorkingValueAvailable(ctx context.Context, tx pgx.Tx, assetType, normalizedValue, excludeDraftID string) error {
	var duplicate bool
	e := tx.QueryRow(ctx, `SELECT EXISTS(
	 SELECT 1 FROM assets WHERE asset_type=$1 AND normalized_value=$2 AND deleted_at IS NULL
	) OR EXISTS(
	 SELECT 1 FROM inventory_asset_drafts
	 WHERE asset_type=$1 AND normalized_value=$2
	   AND draft_status IN `+inventorydraft.ActiveSQL+`
	   AND ($3='' OR id::text<>$3)
	)`, assetType, normalizedValue, excludeDraftID).Scan(&duplicate)
	if e != nil {
		return e
	}
	if duplicate {
		return ErrConflict
	}
	return nil
}
