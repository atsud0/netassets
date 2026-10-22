package customfield

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/access"
	"netassets/internal/auth"
	"netassets/internal/pagination"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid")
	ErrConflict  = errors.New("conflict")
	// ErrVersionRequired and ErrVersionConflict protect formal business-system
	// custom values, which are a second write endpoint for the same resource.
	ErrVersionRequired = errors.New("expected_version is required")
	ErrVersionConflict = errors.New("resource version conflict")
)

type Definition struct {
	ID           string    `json:"id"`
	ResourceType string    `json:"resource_type"`
	FieldKey     string    `json:"field_key"`
	FieldName    string    `json:"field_name"`
	Description  string    `json:"description,omitempty"`
	FieldType    string    `json:"field_type"`
	Options      []string  `json:"options"`
	IsRequired   bool      `json:"is_required"`
	IsEnabled    bool      `json:"is_enabled"`
	SortOrder    int       `json:"sort_order"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
type DefinitionInput struct {
	ResourceType string   `json:"resource_type"`
	FieldKey     string   `json:"field_key"`
	FieldName    string   `json:"field_name"`
	Description  string   `json:"description"`
	FieldType    string   `json:"field_type"`
	Options      []string `json:"options"`
	IsRequired   bool     `json:"is_required"`
	IsEnabled    bool     `json:"is_enabled"`
	SortOrder    int      `json:"sort_order"`
}
type Value struct {
	FieldKey string `json:"field_key"`
	Value    any    `json:"value"`
}
type Service struct{ pool *pgxpool.Pool }

func NewService(p *pgxpool.Pool) *Service { return &Service{p} }
func admin(p auth.Principal) error {
	if !p.HasRole("group_admin") {
		return ErrForbidden
	}
	return nil
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)

const definitionFields = `id::text,resource_type,field_key,field_name,COALESCE(description,''),field_type,options,is_required,is_enabled,sort_order,created_at,updated_at`

func normalize(in *DefinitionInput) {
	in.ResourceType = strings.TrimSpace(in.ResourceType)
	in.FieldKey = strings.ToLower(strings.TrimSpace(in.FieldKey))
	in.FieldName = strings.TrimSpace(in.FieldName)
	in.Description = strings.TrimSpace(in.Description)
	in.FieldType = strings.TrimSpace(in.FieldType)
	for i := range in.Options {
		in.Options[i] = strings.TrimSpace(in.Options[i])
	}
}
func valid(in DefinitionInput) bool {
	if (in.ResourceType != "asset" && in.ResourceType != "business_system") || !keyPattern.MatchString(in.FieldKey) || in.FieldName == "" || len(in.FieldName) > 128 || in.SortOrder < 0 || in.SortOrder > 100000 {
		return false
	}
	switch in.FieldType {
	case "text", "textarea", "number", "date", "boolean":
		return len(in.Options) == 0
	case "select", "multiselect":
		return len(in.Options) > 0 && len(in.Options) <= 100
	}
	return false
}
func (s *Service) List(ctx context.Context, p auth.Principal, typ string) ([]Definition, error) {
	result, err := s.Search(ctx, p, typ, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) Search(ctx context.Context, p auth.Principal, typ string, page pagination.Query) (pagination.Result[Definition], error) {
	if err := admin(p); err != nil {
		return pagination.Result[Definition]{}, err
	}
	if typ != "asset" && typ != "business_system" {
		return pagination.Result[Definition]{}, ErrInvalid
	}
	return s.searchDefinitions(ctx, `resource_type=$1`, []any{typ}, page)
}

func (s *Service) searchDefinitions(ctx context.Context, where string, args []any, page pagination.Query) (pagination.Result[Definition], error) {
	countArgs := append([]any(nil), args...)
	query := `SELECT ` + definitionFields + `,count(*) OVER () FROM custom_field_definitions WHERE ` + where + ` ORDER BY sort_order,created_at LIMIT $` + strconv.Itoa(len(args)+1) + ` OFFSET $` + strconv.Itoa(len(args)+2)
	args = append(args, page.PageSize, page.Offset())
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return pagination.Result[Definition]{}, err
	}
	defer rows.Close()
	items := make([]Definition, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var d Definition
		var raw []byte
		if err = rows.Scan(&d.ID, &d.ResourceType, &d.FieldKey, &d.FieldName, &d.Description, &d.FieldType, &raw, &d.IsRequired, &d.IsEnabled, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt, &total); err != nil {
			return pagination.Result[Definition]{}, err
		}
		_ = json.Unmarshal(raw, &d.Options)
		items = append(items, d)
	}
	if err = rows.Err(); err != nil {
		return pagination.Result[Definition]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM custom_field_definitions WHERE `+where, countArgs...).Scan(&total); err != nil {
			return pagination.Result[Definition]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in DefinitionInput, rid, ip string) (Definition, error) {
	if err := admin(p); err != nil {
		return Definition{}, err
	}
	normalize(&in)
	if !valid(in) {
		return Definition{}, ErrInvalid
	}
	raw, _ := json.Marshal(in.Options)
	var d Definition
	var opts []byte
	err := s.pool.QueryRow(ctx, `INSERT INTO custom_field_definitions(resource_type,field_key,field_name,description,field_type,options,is_required,is_enabled,sort_order,created_by)VALUES($1,$2,$3,NULLIF($4,''),$5,$6::jsonb,$7,$8,$9,$10::uuid) RETURNING id::text,resource_type,field_key,field_name,COALESCE(description,''),field_type,options,is_required,is_enabled,sort_order,created_at,updated_at`, in.ResourceType, in.FieldKey, in.FieldName, in.Description, in.FieldType, raw, in.IsRequired, in.IsEnabled, in.SortOrder, p.UserID).Scan(&d.ID, &d.ResourceType, &d.FieldKey, &d.FieldName, &d.Description, &d.FieldType, &opts, &d.IsRequired, &d.IsEnabled, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return Definition{}, ErrConflict
		}
		return Definition{}, err
	}
	_ = json.Unmarshal(opts, &d.Options)
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_logs(user_id,action,resource_type,resource_id,summary,request_ip,request_id)VALUES($1::uuid,'custom_field.create','custom_field',$2::uuid,$3,NULLIF($4,'')::inet,NULLIF($5,''))`, p.UserID, d.ID, "新增集团自定义字段："+d.FieldName, ip, rid)
	return d, err
}

// Update changes the display configuration of an existing definition. The object type,
// field code and value type are immutable once created so stored values stay compatible.
func (s *Service) Update(ctx context.Context, p auth.Principal, id string, in DefinitionInput, rid, ip string) (Definition, error) {
	if err := admin(p); err != nil {
		return Definition{}, err
	}
	var current Definition
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT id::text,resource_type,field_key,field_name,COALESCE(description,''),field_type,options,is_required,is_enabled,sort_order,created_at,updated_at FROM custom_field_definitions WHERE id=$1::uuid`, id).Scan(
		&current.ID, &current.ResourceType, &current.FieldKey, &current.FieldName, &current.Description, &current.FieldType, &raw, &current.IsRequired, &current.IsEnabled, &current.SortOrder, &current.CreatedAt, &current.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Definition{}, ErrNotFound
	}
	if err != nil {
		return Definition{}, err
	}
	_ = json.Unmarshal(raw, &current.Options)

	normalize(&in)
	if in.ResourceType == "" {
		in.ResourceType = current.ResourceType
	}
	if in.FieldKey == "" {
		in.FieldKey = current.FieldKey
	}
	if in.FieldType == "" {
		in.FieldType = current.FieldType
	}
	if in.ResourceType != current.ResourceType || in.FieldKey != current.FieldKey || in.FieldType != current.FieldType || !valid(in) {
		return Definition{}, ErrInvalid
	}
	raw, _ = json.Marshal(in.Options)
	var updated Definition
	var opts []byte
	err = s.pool.QueryRow(ctx, `UPDATE custom_field_definitions SET field_name=$2,description=NULLIF($3,''),options=$4::jsonb,is_required=$5,is_enabled=$6,sort_order=$7,updated_at=now() WHERE id=$1::uuid RETURNING id::text,resource_type,field_key,field_name,COALESCE(description,''),field_type,options,is_required,is_enabled,sort_order,created_at,updated_at`, id, in.FieldName, in.Description, raw, in.IsRequired, in.IsEnabled, in.SortOrder).Scan(
		&updated.ID, &updated.ResourceType, &updated.FieldKey, &updated.FieldName, &updated.Description, &updated.FieldType, &opts, &updated.IsRequired, &updated.IsEnabled, &updated.SortOrder, &updated.CreatedAt, &updated.UpdatedAt,
	)
	if err != nil {
		return Definition{}, err
	}
	_ = json.Unmarshal(opts, &updated.Options)
	action, summary := "custom_field.update", "更新集团自定义字段："+updated.FieldName
	if current.IsEnabled != updated.IsEnabled {
		action = "custom_field.status_change"
		if updated.IsEnabled {
			summary = "启用集团自定义字段：" + updated.FieldName
		} else {
			summary = "停用集团自定义字段：" + updated.FieldName
		}
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_logs(user_id,action,resource_type,resource_id,summary,request_ip,request_id)VALUES($1::uuid,$2,'custom_field',$3::uuid,$4,NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, action, updated.ID, summary, ip, rid)
	return updated, err
}

func (s *Service) Values(ctx context.Context, p auth.Principal, typ, id string) ([]Value, error) {
	org, err := resourceOrg(ctx, s.pool, typ, id)
	if err != nil {
		return nil, err
	}
	ok, err := access.CanAccessOrganization(ctx, s.pool, p, org)
	if err != nil || !ok {
		if err != nil {
			return nil, err
		}
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT d.field_key,v.value FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type=$1 AND v.resource_id=$2::uuid AND d.is_enabled`, typ, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Value{}
	for rows.Next() {
		var v Value
		var raw []byte
		if err = rows.Scan(&v.FieldKey, &raw); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &v.Value)
		out = append(out, v)
	}
	return out, rows.Err()
}
func resourceOrg(ctx context.Context, p *pgxpool.Pool, typ, id string) (string, error) {
	var org string
	var q string
	if typ == "asset" {
		q = `SELECT ao.organization_id::text FROM asset_organizations ao WHERE ao.asset_id=$1::uuid AND ao.is_primary LIMIT 1`
	} else if typ == "business_system" {
		q = `SELECT owner_organization_id::text FROM business_systems WHERE id=$1::uuid AND deleted_at IS NULL`
	} else {
		return "", ErrInvalid
	}
	err := p.QueryRow(ctx, q, id).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return org, err
}

// ActiveDefinitions loads enabled fields for an existing resource.
func (s *Service) ActiveDefinitions(ctx context.Context, p auth.Principal, typ, resourceID string) ([]Definition, error) {
	org, err := resourceOrg(ctx, s.pool, typ, resourceID)
	if err != nil {
		return nil, err
	}
	allowed, err := access.CanAccessOrganization(ctx, s.pool, p, org)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,resource_type,field_key,field_name,COALESCE(description,''),field_type,options,is_required,is_enabled,sort_order,created_at,updated_at FROM custom_field_definitions WHERE resource_type=$1 AND is_enabled ORDER BY sort_order,created_at`, typ)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var d Definition
		var raw []byte
		if err := rows.Scan(&d.ID, &d.ResourceType, &d.FieldKey, &d.FieldName, &d.Description, &d.FieldType, &raw, &d.IsRequired, &d.IsEnabled, &d.SortOrder, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &d.Options)
		out = append(out, d)
	}
	return out, rows.Err()
}

type ValuesInput struct {
	Values          []Value `json:"values"`
	ExpectedVersion *int    `json:"expected_version,omitempty"`
}

func (s *Service) SaveValues(ctx context.Context, p auth.Principal, typ, id string, in ValuesInput, rid, ip string) error {
	// This endpoint writes directly to formal custom_field_values. Non-group
	// administrators must submit custom fields through the asset/system working
	// version payload instead of bypassing inventory review.
	if (typ == "business_system" || typ == "asset") && !p.HasRole("group_admin") {
		return ErrForbidden
	}
	if (typ == "business_system" || typ == "asset") && (in.ExpectedVersion == nil || *in.ExpectedVersion <= 0) {
		return ErrVersionRequired
	}
	org, err := resourceOrg(ctx, s.pool, typ, id)
	if err != nil {
		return err
	}
	perm := "asset.manage"
	if typ == "business_system" {
		perm = "system.manage"
	}
	ok, err := access.CanOperateOrganization(ctx, s.pool, p, org, perm)
	if err != nil {
		return err
	}
	if !ok {
		return ErrForbidden
	}
	defs, err := s.ActiveDefinitions(ctx, p, typ, id)
	if err != nil {
		return err
	}
	byKey := make(map[string]Definition, len(defs))
	for _, d := range defs {
		byKey[d.FieldKey] = d
	}
	provided := make(map[string]any, len(in.Values))
	for _, value := range in.Values {
		if _, duplicate := provided[value.FieldKey]; duplicate {
			return ErrInvalid
		}
		d, found := byKey[value.FieldKey]
		if !found || !validFieldValue(d, value.Value) {
			return ErrInvalid
		}
		provided[value.FieldKey] = value.Value
	}
	// A partial update must not bypass a required field that is neither supplied nor stored.
	existing := map[string]any{}
	rows, err := s.pool.Query(ctx, `SELECT d.field_key,v.value FROM custom_field_values v JOIN custom_field_definitions d ON d.id=v.field_definition_id WHERE v.resource_type=$1 AND v.resource_id=$2::uuid AND d.is_enabled`, typ, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var key string
		var raw []byte
		var value any
		if err = rows.Scan(&key, &raw); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			rows.Close()
			return err
		}
		existing[key] = value
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, d := range defs {
		if !d.IsRequired {
			continue
		}
		value, present := provided[d.FieldKey]
		if !present {
			value, present = existing[d.FieldKey]
		}
		if !present || isBlank(value) {
			return ErrInvalid
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if typ == "business_system" || typ == "asset" {
		var nextVersion int
		table := "business_systems"
		if typ == "asset" {
			table = "assets"
		}
		query := `UPDATE ` + table + `
			SET version=version+1, updated_by=$1::uuid, updated_at=now()
			WHERE id=$2::uuid AND deleted_at IS NULL AND version=$3
			RETURNING version`
		err = tx.QueryRow(ctx, query, p.UserID, id, *in.ExpectedVersion).Scan(&nextVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrVersionConflict
		}
		if err != nil {
			return err
		}
	}
	for key, value := range provided {
		d := byKey[key]
		raw, _ := json.Marshal(value)
		if _, err = tx.Exec(ctx, `INSERT INTO custom_field_values(resource_type,resource_id,field_definition_id,value,updated_by)VALUES($1,$2::uuid,$3::uuid,$4::jsonb,$5::uuid) ON CONFLICT(resource_id,field_definition_id) DO UPDATE SET value=EXCLUDED.value,updated_by=EXCLUDED.updated_by,updated_at=now()`, typ, id, d.ID, raw, p.UserID); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_logs(user_id,organization_id,action,resource_type,resource_id,summary,request_ip,request_id)VALUES($1::uuid,$2::uuid,'custom_field.value_update',$3,$4::uuid,'更新自定义字段值',NULLIF($5,'')::inet,NULLIF($6,''))`, p.UserID, org, typ, id, ip, rid)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// FillableDefinitions exposes enabled definitions to users with resource maintenance permission.
func (s *Service) FillableDefinitions(ctx context.Context, p auth.Principal, typ string) ([]Definition, error) {
	result, err := s.SearchFillableDefinitions(ctx, p, typ, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) SearchFillableDefinitions(ctx context.Context, p auth.Principal, typ string, page pagination.Query) (pagination.Result[Definition], error) {
	if typ != "asset" && typ != "business_system" {
		return pagination.Result[Definition]{}, ErrInvalid
	}
	permission := "asset.manage"
	if typ == "business_system" {
		permission = "system.manage"
	}
	if !p.HasPermission(permission) {
		return pagination.Result[Definition]{}, ErrForbidden
	}
	return s.searchDefinitions(ctx, `resource_type=$1 AND is_enabled`, []any{typ}, page)
}

func isBlank(v any) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	if xs, ok := v.([]any); ok {
		return len(xs) == 0
	}
	return false
}
func validFieldValue(d Definition, v any) bool {
	if isBlank(v) {
		return !d.IsRequired
	}
	switch d.FieldType {
	case "text", "textarea", "date":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "select":
		x, ok := v.(string)
		if !ok {
			return false
		}
		for _, o := range d.Options {
			if o == x {
				return true
			}
		}
		return false
	case "multiselect":
		xs, ok := v.([]any)
		if !ok {
			return false
		}
		for _, x := range xs {
			str, ok := x.(string)
			if !ok {
				return false
			}
			found := false
			for _, o := range d.Options {
				if o == str {
					found = true
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	return false
}
