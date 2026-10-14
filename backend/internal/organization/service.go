package organization

import (
	"context"
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
	"netassets/internal/textlength"
)

var (
	ErrForbidden            = errors.New("forbidden")
	ErrNotFound             = errors.New("not found")
	ErrConflict             = errors.New("conflict")
	ErrInvalid              = errors.New("invalid input")
	ErrBlocked              = errors.New("operation blocked")
	ErrConfirmationRequired = errors.New("confirmation required")
	codePattern             = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$`)
	uuidPattern             = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type Organization struct {
	ID               string         `json:"id"`
	ParentID         *string        `json:"parent_id"`
	ParentName       string         `json:"parent_name,omitempty"`
	Code             string         `json:"code"`
	Name             string         `json:"name"`
	ShortName        string         `json:"short_name,omitempty"`
	Type             string         `json:"type"`
	Manager          string         `json:"manager,omitempty"`
	Contact          string         `json:"contact,omitempty"`
	Status           string         `json:"status"`
	Notes            string         `json:"notes,omitempty"`
	ChildCount       int64          `json:"child_count"`
	BusinessSysCount int64          `json:"business_system_count"`
	AssetCount       int64          `json:"asset_count"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	Children         []Organization `json:"children,omitempty"`
	CanUpdate        bool           `json:"can_update"`
	CanCreateChild   bool           `json:"can_create_child"`
}

type Impact struct {
	OrganizationID               string   `json:"organization_id"`
	OrganizationName             string   `json:"organization_name"`
	OrganizationType             string   `json:"organization_type"`
	Status                       string   `json:"status"`
	UserCount                    int64    `json:"user_count"`
	RoleAssignmentCount          int64    `json:"role_assignment_count"`
	AssetCount                   int64    `json:"asset_count"`
	BusinessSystemCount          int64    `json:"business_system_count"`
	ChildCount                   int64    `json:"child_count"`
	EnabledChildCount            int64    `json:"enabled_child_count"`
	UnfinishedInventoryTaskCount int64    `json:"unfinished_inventory_task_count"`
	OpenWorkOrderCount           int64    `json:"open_work_order_count"`
	OpenQualityIssueCount        int64    `json:"open_quality_issue_count"`
	NetworkAreaCount             int64    `json:"network_area_count"`
	ActiveAPITokenCount          int64    `json:"active_api_token_count"`
	RequiresResourceConfirmation bool     `json:"requires_resource_confirmation"`
	CanDisable                   bool     `json:"can_disable"`
	DisableBlockers              []string `json:"disable_blockers"`
	CanDelete                    bool     `json:"can_delete"`
	DeleteBlockers               []string `json:"delete_blockers"`
}

type UpdateInput struct {
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Manager   string `json:"manager"`
	Contact   string `json:"contact"`
	Notes     string `json:"notes"`
}

type CreateInput struct {
	ParentID  string `json:"parent_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	ShortName string `json:"short_name"`
	Type      string `json:"type"`
	Manager   string `json:"manager"`
	Contact   string `json:"contact"`
	Notes     string `json:"notes"`
}

type StructureInput struct {
	ParentID string `json:"parent_id"`
	Type     string `json:"type"`
}

type DisableInput struct {
	ConfirmResources bool `json:"confirm_resources"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, organizationListSQL(false), principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	defer rows.Close()
	items := make([]Organization, 0)
	for rows.Next() {
		item, err := scanOrganization(rows)
		if err != nil {
			return nil, fmt.Errorf("scan organization: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate organizations: %w", err)
	}
	if err := s.attachCapabilities(ctx, principal, items); err != nil {
		return nil, err
	}
	return items, nil
}

func (s *Service) Tree(ctx context.Context, principal auth.Principal) ([]Organization, error) {
	items, err := s.List(ctx, principal)
	if err != nil {
		return nil, err
	}
	return BuildTree(items), nil
}

func BuildTree(items []Organization) []Organization {
	byParent := make(map[string][]Organization)
	known := make(map[string]struct{}, len(items))
	for _, item := range items {
		known[item.ID] = struct{}{}
	}
	for _, item := range items {
		parentKey := ""
		if item.ParentID != nil {
			if _, ok := known[*item.ParentID]; ok {
				parentKey = *item.ParentID
			}
		}
		byParent[parentKey] = append(byParent[parentKey], item)
	}
	var build func(string) []Organization
	build = func(parentID string) []Organization {
		children := byParent[parentID]
		for index := range children {
			children[index].Children = build(children[index].ID)
		}
		return children
	}
	return build("")
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, query pagination.Query) (pagination.Result[Organization], error) {
	rows, err := s.pool.Query(ctx, organizationListSQL(true), principal.UserID, principal.HasRole("group_admin"), query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Organization]{}, fmt.Errorf("search organizations: %w", err)
	}
	defer rows.Close()
	items := make([]Organization, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item Organization
		if err := rows.Scan(&item.ID, &item.ParentID, &item.ParentName, &item.Code, &item.Name, &item.ShortName, &item.Type,
			&item.Manager, &item.Contact, &item.Status, &item.Notes, &item.ChildCount, &item.BusinessSysCount,
			&item.AssetCount, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Organization]{}, fmt.Errorf("scan organization page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Organization]{}, fmt.Errorf("iterate organization page: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, accessibleOrganizationCTE()+`
			SELECT count(*) FROM organizations o
			WHERE o.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
		`, principal.UserID, principal.HasRole("group_admin")).Scan(&total); err != nil {
			return pagination.Result[Organization]{}, fmt.Errorf("count organizations: %w", err)
		}
	}
	if err := s.attachCapabilities(ctx, principal, items); err != nil {
		return pagination.Result[Organization]{}, err
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (Organization, error) {
	input.Code = strings.TrimSpace(input.Code)
	input.Name = strings.TrimSpace(input.Name)
	input.ShortName = strings.TrimSpace(input.ShortName)
	input.ParentID = strings.TrimSpace(input.ParentID)
	input.Type = strings.TrimSpace(input.Type)
	input.Manager = strings.TrimSpace(input.Manager)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Notes = strings.TrimSpace(input.Notes)
	if !codePattern.MatchString(input.Code) || input.Name == "" || textlength.Count(input.Name) > 255 || !validUUID(input.ParentID) || !validNonGroupType(input.Type) ||
		textlength.Count(input.ShortName) > 128 || textlength.Count(input.Manager) > 128 || textlength.Count(input.Contact) > 255 || textlength.Count(input.Notes) > 5000 {
		return Organization{}, ErrInvalid
	}
	allowed, err := s.canOperateOrganization(ctx, principal, input.ParentID, "organization.create")
	if err != nil {
		return Organization{}, err
	}
	if !allowed {
		return Organization{}, ErrForbidden
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin create organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var parentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL`, input.ParentID).Scan(&parentStatus); errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	} else if err != nil {
		return Organization{}, fmt.Errorf("load parent organization: %w", err)
	}
	if parentStatus != "enabled" {
		return Organization{}, ErrInvalid
	}

	var item Organization
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (parent_id,organization_code,organization_name,short_name,organization_type,manager,contact,status,notes)
		VALUES ($1::uuid,$2,$3,NULLIF($4,''),$5,NULLIF($6,''),NULLIF($7,''),'enabled',NULLIF($8,''))
		RETURNING id::text,parent_id::text,organization_code,organization_name,COALESCE(short_name,''),organization_type,
		          COALESCE(manager,''),COALESCE(contact,''),status,COALESCE(notes,''),created_at,updated_at
	`, input.ParentID, input.Code, input.Name, input.ShortName, input.Type, input.Manager, input.Contact, input.Notes).Scan(
		&item.ID, &item.ParentID, &item.Code, &item.Name, &item.ShortName, &item.Type, &item.Manager, &item.Contact,
		&item.Status, &item.Notes, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Organization{}, ErrConflict
		}
		return Organization{}, fmt.Errorf("insert organization: %w", err)
	}
	if err := tx.QueryRow(ctx, `SELECT organization_name FROM organizations WHERE id=$1::uuid`, input.ParentID).Scan(&item.ParentName); err != nil {
		return Organization{}, fmt.Errorf("load parent organization name: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'organization.create','organization',$3,'新增组织机构',
		        jsonb_build_object('code',$4::text,'name',$5::text,'type',$6::text),NULLIF($7,'')::inet,NULLIF($8,''))
	`, principal.UserID, input.ParentID, item.ID, item.Code, item.Name, item.Type, ipAddress, requestID); err != nil {
		return Organization{}, fmt.Errorf("write organization audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization: %w", err)
	}
	return item, nil
}

func (s *Service) Update(ctx context.Context, principal auth.Principal, organizationID string, input UpdateInput, requestID, ipAddress string) (Organization, error) {
	organizationID = strings.TrimSpace(organizationID)
	input.Name = strings.TrimSpace(input.Name)
	input.ShortName = strings.TrimSpace(input.ShortName)
	input.Manager = strings.TrimSpace(input.Manager)
	input.Contact = strings.TrimSpace(input.Contact)
	input.Notes = strings.TrimSpace(input.Notes)
	if !validUUID(organizationID) || input.Name == "" || textlength.Count(input.Name) > 255 || textlength.Count(input.ShortName) > 128 ||
		textlength.Count(input.Manager) > 128 || textlength.Count(input.Contact) > 255 || textlength.Count(input.Notes) > 5000 {
		return Organization{}, ErrInvalid
	}
	allowed, err := s.canOperateOrganization(ctx, principal, organizationID, "organization.update")
	if err != nil {
		return Organization{}, err
	}
	if !allowed {
		return Organization{}, ErrForbidden
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin update organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var item Organization
	err = tx.QueryRow(ctx, `
		UPDATE organizations o SET organization_name=$2,short_name=NULLIF($3,''),manager=NULLIF($4,''),contact=NULLIF($5,''),notes=NULLIF($6,''),updated_at=now()
		WHERE o.id=$1::uuid AND o.deleted_at IS NULL
		RETURNING o.id::text,o.parent_id::text,o.organization_code,o.organization_name,COALESCE(o.short_name,''),o.organization_type,
		          COALESCE(o.manager,''),COALESCE(o.contact,''),o.status,COALESCE(o.notes,''),o.created_at,o.updated_at
	`, organizationID, input.Name, input.ShortName, input.Manager, input.Contact, input.Notes).Scan(
		&item.ID, &item.ParentID, &item.Code, &item.Name, &item.ShortName, &item.Type, &item.Manager, &item.Contact,
		&item.Status, &item.Notes, &item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("update organization: %w", err)
	}
	if err := loadOrganizationRelations(ctx, tx, &item); err != nil {
		return Organization{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'organization.update','organization',$2,'编辑组织机构',
		        jsonb_build_object('name',$3::text,'short_name',$4::text,'manager',$5::text,'contact',$6::text),NULLIF($7,'')::inet,NULLIF($8,''))
	`, principal.UserID, item.ID, item.Name, item.ShortName, item.Manager, item.Contact, ipAddress, requestID); err != nil {
		return Organization{}, fmt.Errorf("write organization update audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization update: %w", err)
	}
	return item, nil
}

func (s *Service) GetImpact(ctx context.Context, principal auth.Principal, organizationID string) (Impact, error) {
	if !principal.HasRole("group_admin") {
		return Impact{}, ErrForbidden
	}
	if !validUUID(strings.TrimSpace(organizationID)) {
		return Impact{}, ErrInvalid
	}
	return loadImpact(ctx, s.pool, organizationID)
}

func (s *Service) Disable(ctx context.Context, principal auth.Principal, organizationID string, input DisableInput, requestID, ipAddress string) (Impact, error) {
	if !principal.HasRole("group_admin") {
		return Impact{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	if !validUUID(organizationID) {
		return Impact{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Impact{}, fmt.Errorf("begin disable organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	impact, err := loadImpact(ctx, tx, organizationID)
	if err != nil {
		return Impact{}, err
	}
	if impact.Status == "disabled" {
		return impact, nil
	}
	if !impact.CanDisable {
		return impact, ErrBlocked
	}
	if impact.RequiresResourceConfirmation && !input.ConfirmResources {
		return impact, ErrConfirmationRequired
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET status='disabled',updated_at=now() WHERE id=$1::uuid AND deleted_at IS NULL`, organizationID); err != nil {
		return Impact{}, fmt.Errorf("disable organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET current_organization_id=NULL WHERE current_organization_id=$1::uuid AND revoked_at IS NULL`, organizationID); err != nil {
		return Impact{}, fmt.Errorf("clear disabled organization sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'organization.disable','organization',$2,'停用组织机构',
		        jsonb_build_object('user_count',$3::bigint,'asset_count',$4::bigint,'business_system_count',$5::bigint),NULLIF($6,'')::inet,NULLIF($7,''))
	`, principal.UserID, organizationID, impact.UserCount, impact.AssetCount, impact.BusinessSystemCount, ipAddress, requestID); err != nil {
		return Impact{}, fmt.Errorf("write organization disable audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Impact{}, fmt.Errorf("commit organization disable: %w", err)
	}
	impact.Status = "disabled"
	return impact, nil
}

func (s *Service) Enable(ctx context.Context, principal auth.Principal, organizationID, requestID, ipAddress string) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	if !validUUID(organizationID) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin enable organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var parentID *string
	var orgType, status string
	if err := tx.QueryRow(ctx, `SELECT parent_id::text,organization_type,status FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, organizationID).Scan(&parentID, &orgType, &status); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return fmt.Errorf("load organization for enable: %w", err)
	}
	if status == "enabled" {
		return nil
	}
	if orgType != "group" {
		if parentID == nil {
			return ErrBlocked
		}
		var parentEnabled bool
		if err := tx.QueryRow(ctx, `SELECT status='enabled' FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL`, *parentID).Scan(&parentEnabled); errors.Is(err, pgx.ErrNoRows) {
			return ErrBlocked
		} else if err != nil {
			return fmt.Errorf("load parent organization for enable: %w", err)
		}
		if !parentEnabled {
			return ErrBlocked
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET status='enabled',updated_at=now() WHERE id=$1::uuid`, organizationID); err != nil {
		return fmt.Errorf("enable organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'organization.enable','organization',$2,'恢复启用组织机构',NULLIF($3,'')::inet,NULLIF($4,''))
	`, principal.UserID, organizationID, ipAddress, requestID); err != nil {
		return fmt.Errorf("write organization enable audit: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Service) UpdateStructure(ctx context.Context, principal auth.Principal, organizationID string, input StructureInput, requestID, ipAddress string) (Organization, error) {
	if !principal.HasRole("group_admin") {
		return Organization{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	input.ParentID = strings.TrimSpace(input.ParentID)
	input.Type = strings.TrimSpace(input.Type)
	if !validUUID(organizationID) || !validUUID(input.ParentID) || !validNonGroupType(input.Type) || organizationID == input.ParentID {
		return Organization{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, fmt.Errorf("begin update organization structure: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var oldParentID *string
	var oldType string
	if err := tx.QueryRow(ctx, `SELECT parent_id::text,organization_type FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL FOR UPDATE`, organizationID).Scan(&oldParentID, &oldType); errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	} else if err != nil {
		return Organization{}, fmt.Errorf("load organization structure: %w", err)
	}
	if oldType == "group" {
		return Organization{}, ErrBlocked
	}
	var parentStatus string
	if err := tx.QueryRow(ctx, `SELECT status FROM organizations WHERE id=$1::uuid AND deleted_at IS NULL`, input.ParentID).Scan(&parentStatus); errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	} else if err != nil {
		return Organization{}, fmt.Errorf("load new parent organization: %w", err)
	}
	if parentStatus != "enabled" {
		return Organization{}, ErrBlocked
	}
	var createsCycle bool
	if err := tx.QueryRow(ctx, `
		WITH RECURSIVE descendants(id) AS (
			SELECT id FROM organizations WHERE parent_id=$1::uuid AND deleted_at IS NULL
			UNION ALL
			SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id WHERE child.deleted_at IS NULL
		)
		SELECT EXISTS(SELECT 1 FROM descendants WHERE id=$2::uuid)
	`, organizationID, input.ParentID).Scan(&createsCycle); err != nil {
		return Organization{}, fmt.Errorf("check organization hierarchy cycle: %w", err)
	}
	if createsCycle {
		return Organization{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET parent_id=$2::uuid,organization_type=$3,updated_at=now() WHERE id=$1::uuid`, organizationID, input.ParentID, input.Type); err != nil {
		return Organization{}, fmt.Errorf("update organization structure: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'organization.structure_update','organization',$2,'调整组织层级与类型',
		        jsonb_build_object('old_parent_id',$3::text,'new_parent_id',$4::text,'old_type',$5::text,'new_type',$6::text),NULLIF($7,'')::inet,NULLIF($8,''))
	`, principal.UserID, organizationID, nullableString(oldParentID), input.ParentID, oldType, input.Type, ipAddress, requestID); err != nil {
		return Organization{}, fmt.Errorf("write organization structure audit: %w", err)
	}
	item, err := loadOrganization(ctx, tx, organizationID)
	if err != nil {
		return Organization{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Organization{}, fmt.Errorf("commit organization structure: %w", err)
	}
	return item, nil
}

func (s *Service) SoftDelete(ctx context.Context, principal auth.Principal, organizationID, requestID, ipAddress string) (Impact, error) {
	if !principal.HasRole("group_admin") {
		return Impact{}, ErrForbidden
	}
	organizationID = strings.TrimSpace(organizationID)
	if !validUUID(organizationID) {
		return Impact{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Impact{}, fmt.Errorf("begin delete organization: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	impact, err := loadImpact(ctx, tx, organizationID)
	if err != nil {
		return Impact{}, err
	}
	if !impact.CanDelete {
		return impact, ErrBlocked
	}
	if _, err := tx.Exec(ctx, `UPDATE organizations SET deleted_at=now(),deleted_by=$2::uuid,status='disabled',updated_at=now() WHERE id=$1::uuid AND deleted_at IS NULL`, organizationID, principal.UserID); err != nil {
		return Impact{}, fmt.Errorf("soft delete organization: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE sessions SET current_organization_id=NULL WHERE current_organization_id=$1::uuid AND revoked_at IS NULL`, organizationID); err != nil {
		return Impact{}, fmt.Errorf("clear deleted organization sessions: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at=now() WHERE organization_id=$1::uuid AND revoked_at IS NULL`, organizationID); err != nil {
		return Impact{}, fmt.Errorf("revoke deleted organization api tokens: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,$2::uuid,'organization.delete','organization',$2,'组织移入回收站',jsonb_build_object('name',$3::text),NULLIF($4,'')::inet,NULLIF($5,''))
	`, principal.UserID, organizationID, impact.OrganizationName, ipAddress, requestID); err != nil {
		return Impact{}, fmt.Errorf("write organization delete audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Impact{}, fmt.Errorf("commit organization delete: %w", err)
	}
	return impact, nil
}

type rowScanner interface{ Scan(...any) error }
type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadImpact(ctx context.Context, db queryRower, organizationID string) (Impact, error) {
	var item Impact
	err := db.QueryRow(ctx, `
		SELECT o.id::text,o.organization_name,o.organization_type,o.status,
		       (SELECT count(DISTINCT uo.user_id) FROM user_organizations uo JOIN users u ON u.id=uo.user_id AND u.deleted_at IS NULL WHERE uo.organization_id=o.id),
		       (SELECT count(*) FROM user_roles ur WHERE ur.organization_id=o.id),
		       (SELECT count(DISTINCT ao.asset_id) FROM asset_organizations ao JOIN assets a ON a.id=ao.asset_id AND a.deleted_at IS NULL WHERE ao.organization_id=o.id),
		       (SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id=o.id AND bs.deleted_at IS NULL),
		       (SELECT count(*) FROM organizations child WHERE child.parent_id=o.id AND child.deleted_at IS NULL),
		       (SELECT count(*) FROM organizations child WHERE child.parent_id=o.id AND child.deleted_at IS NULL AND child.status='enabled'),
		       (SELECT count(*) FROM inventory_task_organizations ito JOIN inventory_tasks it ON it.id=ito.task_id WHERE ito.organization_id=o.id AND it.status NOT IN ('closed','completed','archived') AND ito.status <> 'approved'),
		       (SELECT count(*) FROM security_work_orders wo WHERE wo.assignee_organization_id=o.id AND wo.status NOT IN ('closed','cancelled')),
		       (SELECT count(*) FROM data_quality_issues dqi WHERE dqi.organization_id=o.id AND dqi.status NOT IN ('closed','exempted')),
		       (SELECT count(*) FROM asset_network_areas na WHERE na.organization_id=o.id),
		       (SELECT count(*) FROM api_tokens token WHERE token.organization_id=o.id AND token.revoked_at IS NULL AND token.expires_at > now())
		FROM organizations o WHERE o.id=$1::uuid AND o.deleted_at IS NULL
	`, organizationID).Scan(&item.OrganizationID, &item.OrganizationName, &item.OrganizationType, &item.Status,
		&item.UserCount, &item.RoleAssignmentCount, &item.AssetCount, &item.BusinessSystemCount, &item.ChildCount,
		&item.EnabledChildCount, &item.UnfinishedInventoryTaskCount, &item.OpenWorkOrderCount, &item.OpenQualityIssueCount,
		&item.NetworkAreaCount, &item.ActiveAPITokenCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return Impact{}, ErrNotFound
	}
	if err != nil {
		return Impact{}, fmt.Errorf("load organization impact: %w", err)
	}
	finalizeImpact(&item)
	return item, nil
}

func finalizeImpact(item *Impact) {
	item.DisableBlockers = make([]string, 0)
	if item.OrganizationType == "group" {
		item.DisableBlockers = append(item.DisableBlockers, "集团根组织不能停用")
	}
	if item.EnabledChildCount > 0 {
		item.DisableBlockers = append(item.DisableBlockers, "存在启用中的下级组织")
	}
	if item.UnfinishedInventoryTaskCount > 0 {
		item.DisableBlockers = append(item.DisableBlockers, "存在未完成的填报任务")
	}
	if item.OpenWorkOrderCount > 0 {
		item.DisableBlockers = append(item.DisableBlockers, "存在未关闭的安全工单")
	}
	if item.OpenQualityIssueCount > 0 {
		item.DisableBlockers = append(item.DisableBlockers, "存在未完成的质量整改")
	}
	item.CanDisable = len(item.DisableBlockers) == 0
	item.RequiresResourceConfirmation = item.UserCount > 0 || item.AssetCount > 0 || item.BusinessSystemCount > 0

	item.DeleteBlockers = make([]string, 0)
	if item.OrganizationType == "group" {
		item.DeleteBlockers = append(item.DeleteBlockers, "集团根组织不能删除")
	}
	if item.Status != "disabled" {
		item.DeleteBlockers = append(item.DeleteBlockers, "请先停用组织")
	}
	blockerCounts := []struct {
		count int64
		label string
	}{
		{item.ChildCount, "仍有下级组织"},
		{item.UserCount, "仍有用户任职"},
		{item.RoleAssignmentCount, "仍有角色授权"},
		{item.AssetCount, "仍有网络资产"},
		{item.BusinessSystemCount, "仍有业务系统"},
		{item.UnfinishedInventoryTaskCount, "仍有未完成填报任务"},
		{item.OpenWorkOrderCount, "仍有未关闭安全工单"},
		{item.OpenQualityIssueCount, "仍有未完成质量整改"},
		{item.NetworkAreaCount, "仍有网络区域配置"},
	}
	for _, blocker := range blockerCounts {
		if blocker.count > 0 {
			item.DeleteBlockers = append(item.DeleteBlockers, blocker.label)
		}
	}
	item.CanDelete = len(item.DeleteBlockers) == 0
}

func (s *Service) attachCapabilities(ctx context.Context, principal auth.Principal, items []Organization) error {
	if principal.HasRole("group_admin") {
		for index := range items {
			items[index].CanUpdate = true
			items[index].CanCreateChild = items[index].Status == "enabled"
		}
		return nil
	}
	updateIDs, err := s.operableOrganizationIDs(ctx, principal, "organization.update")
	if err != nil {
		return err
	}
	createIDs, err := s.operableOrganizationIDs(ctx, principal, "organization.create")
	if err != nil {
		return err
	}
	for index := range items {
		items[index].CanUpdate = updateIDs[items[index].ID]
		items[index].CanCreateChild = items[index].Status == "enabled" && createIDs[items[index].ID]
	}
	return nil
}

func (s *Service) canOperateOrganization(ctx context.Context, principal auth.Principal, organizationID, permission string) (bool, error) {
	if principal.HasRole("group_admin") {
		return true, nil
	}
	if !principal.HasPermission(permission) {
		return false, nil
	}
	var allowed bool
	if err := s.pool.QueryRow(ctx, operableOrganizationCTE()+`
		SELECT EXISTS(SELECT 1 FROM operable WHERE id=$3::uuid)
	`, principal.UserID, permission, organizationID).Scan(&allowed); err != nil {
		return false, fmt.Errorf("check %s organization scope: %w", permission, err)
	}
	return allowed, nil
}

func (s *Service) operableOrganizationIDs(ctx context.Context, principal auth.Principal, permission string) (map[string]bool, error) {
	result := make(map[string]bool)
	if !principal.HasPermission(permission) {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, operableOrganizationCTE()+`
		SELECT id::text FROM operable
	`, principal.UserID, permission)
	if err != nil {
		return nil, fmt.Errorf("list %s organization scope: %w", permission, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan %s organization scope: %w", permission, err)
		}
		result[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s organization scope: %w", permission, err)
	}
	return result, nil
}

func operableOrganizationCTE() string {
	return `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id
			FROM user_roles ur
			JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
			JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key=$2
			JOIN organizations root ON root.id=ur.organization_id
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL
			  AND (ur.data_scope='organization_and_descendants' OR r.role_key='organization_admin')
			  AND root.status='enabled' AND root.deleted_at IS NULL
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION
			SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
			WHERE child.status='enabled' AND child.deleted_at IS NULL
		), operable(id) AS (
			SELECT ur.organization_id
			FROM user_roles ur
			JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
			JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key=$2
			JOIN organizations direct ON direct.id=ur.organization_id
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL
			  AND (ur.data_scope IN ('organization','organization_and_descendants') OR r.role_key='organization_admin')
			  AND direct.status='enabled' AND direct.deleted_at IS NULL
			UNION SELECT id FROM descendants
		)
	`
}

func organizationListSQL(paged bool) string {
	suffix := ""
	if paged {
		suffix = ", count(*) OVER ()"
	}
	limit := ""
	if paged {
		limit = " LIMIT $3 OFFSET $4"
	}
	return accessibleOrganizationCTE() + `
		SELECT o.id::text,o.parent_id::text,COALESCE(parent.organization_name,''),o.organization_code,o.organization_name,
		       COALESCE(o.short_name,''),o.organization_type,COALESCE(o.manager,''),COALESCE(o.contact,''),o.status,COALESCE(o.notes,''),
		       (SELECT count(*) FROM organizations child WHERE child.parent_id=o.id AND child.deleted_at IS NULL),
		       (SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id=o.id AND bs.deleted_at IS NULL),
		       (SELECT count(DISTINCT ao.asset_id) FROM asset_organizations ao JOIN assets a ON a.id=ao.asset_id AND a.deleted_at IS NULL WHERE ao.organization_id=o.id),
		       o.created_at,o.updated_at` + suffix + `
		FROM organizations o LEFT JOIN organizations parent ON parent.id=o.parent_id AND parent.deleted_at IS NULL
		WHERE o.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
		ORDER BY CASE WHEN o.organization_type='group' THEN 0 ELSE 1 END,o.organization_code` + limit
}

func accessibleOrganizationCTE() string {
	return `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id
			FROM user_roles ur
			JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
			JOIN organizations root ON root.id=ur.organization_id
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL
			  AND (ur.data_scope='organization_and_descendants' OR r.role_key='organization_admin')
			  AND root.status='enabled' AND root.deleted_at IS NULL
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION
			SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id=parent.id
			WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id
			FROM user_roles ur
			JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
			JOIN organizations direct ON direct.id=ur.organization_id
			WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL
			  AND (ur.data_scope IN ('organization','organization_and_descendants') OR r.role_key='organization_admin')
			  AND direct.status='enabled' AND direct.deleted_at IS NULL
			UNION SELECT id FROM descendants
		)
	`
}

func scanOrganization(row rowScanner) (Organization, error) {
	var item Organization
	err := row.Scan(&item.ID, &item.ParentID, &item.ParentName, &item.Code, &item.Name, &item.ShortName, &item.Type,
		&item.Manager, &item.Contact, &item.Status, &item.Notes, &item.ChildCount, &item.BusinessSysCount,
		&item.AssetCount, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func loadOrganization(ctx context.Context, db queryRower, organizationID string) (Organization, error) {
	row := db.QueryRow(ctx, `
		SELECT o.id::text,o.parent_id::text,COALESCE(parent.organization_name,''),o.organization_code,o.organization_name,
		       COALESCE(o.short_name,''),o.organization_type,COALESCE(o.manager,''),COALESCE(o.contact,''),o.status,COALESCE(o.notes,''),
		       (SELECT count(*) FROM organizations child WHERE child.parent_id=o.id AND child.deleted_at IS NULL),
		       (SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id=o.id AND bs.deleted_at IS NULL),
		       (SELECT count(DISTINCT ao.asset_id) FROM asset_organizations ao JOIN assets a ON a.id=ao.asset_id AND a.deleted_at IS NULL WHERE ao.organization_id=o.id),
		       o.created_at,o.updated_at
		FROM organizations o LEFT JOIN organizations parent ON parent.id=o.parent_id
		WHERE o.id=$1::uuid AND o.deleted_at IS NULL
	`, organizationID)
	item, err := scanOrganization(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Organization{}, ErrNotFound
	}
	if err != nil {
		return Organization{}, fmt.Errorf("load organization: %w", err)
	}
	return item, nil
}

func loadOrganizationRelations(ctx context.Context, db queryRower, item *Organization) error {
	if item.ParentID != nil {
		if err := db.QueryRow(ctx, `SELECT organization_name FROM organizations WHERE id=$1::uuid`, *item.ParentID).Scan(&item.ParentName); err != nil {
			return fmt.Errorf("load parent organization name: %w", err)
		}
	}
	if err := db.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM organizations child WHERE child.parent_id=$1::uuid AND child.deleted_at IS NULL),
		       (SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id=$1::uuid AND bs.deleted_at IS NULL),
		       (SELECT count(DISTINCT ao.asset_id) FROM asset_organizations ao JOIN assets a ON a.id=ao.asset_id AND a.deleted_at IS NULL WHERE ao.organization_id=$1::uuid)
	`, item.ID).Scan(&item.ChildCount, &item.BusinessSysCount, &item.AssetCount); err != nil {
		return fmt.Errorf("load organization counts: %w", err)
	}
	return nil
}

func validUUID(value string) bool { return uuidPattern.MatchString(strings.TrimSpace(value)) }
func validNonGroupType(value string) bool {
	return value == "subsidiary" || value == "department" || value == "other"
}
func nullableString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
