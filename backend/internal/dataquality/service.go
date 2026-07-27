package dataquality

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden         = errors.New("forbidden")
	ErrNotFound          = errors.New("not found")
	ErrInvalid           = errors.New("invalid")
	ErrConflict          = errors.New("conflict")
	ErrInvalidTransition = errors.New("invalid transition")
)

type Rule struct {
	ID             string    `json:"id"`
	RuleName       string    `json:"rule_name"`
	Description    string    `json:"description,omitempty"`
	ResourceType   string    `json:"resource_type"`
	RuleType       string    `json:"rule_type"`
	TargetField    string    `json:"target_field"`
	Severity       string    `json:"severity"`
	StaleAfterDays int       `json:"stale_after_days,omitempty"`
	IsEnabled      bool      `json:"is_enabled"`
	SortOrder      int       `json:"sort_order"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type Input struct {
	RuleName       string `json:"rule_name"`
	Description    string `json:"description"`
	ResourceType   string `json:"resource_type"`
	RuleType       string `json:"rule_type"`
	TargetField    string `json:"target_field"`
	Severity       string `json:"severity"`
	StaleAfterDays int    `json:"stale_after_days"`
	IsEnabled      bool   `json:"is_enabled"`
	SortOrder      int    `json:"sort_order"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func isAdmin(p auth.Principal) error {
	if !p.HasRole("group_admin") {
		return ErrForbidden
	}
	return nil
}

func builtinTarget(typ, target string) bool {
	allowed := map[string]map[string]bool{
		"asset":           {"value": true, "name": true, "network_scope": true, "network_area_id": true, "owner_name": true, "business_system_id": true, "importance": true},
		"business_system": {"code": true, "name": true, "owner_name": true, "owner_contact": true, "system_level": true, "internet_exposure": true, "contains_personal_information": true, "contains_sensitive_personal_information": true, "data_volume_over_100k": true, "contains_important_data": true, "contains_core_data": true},
	}
	return allowed[typ][target]
}
func normalize(in *Input) {
	in.RuleName = strings.TrimSpace(in.RuleName)
	in.Description = strings.TrimSpace(in.Description)
	in.ResourceType = strings.TrimSpace(in.ResourceType)
	in.RuleType = strings.TrimSpace(in.RuleType)
	in.TargetField = strings.ToLower(strings.TrimSpace(in.TargetField))
	in.Severity = strings.TrimSpace(in.Severity)
}
func (s *Service) valid(ctx context.Context, in Input) error {
	if in.RuleName == "" || len(in.RuleName) > 128 || (in.ResourceType != "asset" && in.ResourceType != "business_system") || (in.RuleType != "required" && in.RuleType != "stale") || (in.Severity != "error" && in.Severity != "warning") || in.SortOrder < 0 || in.SortOrder > 100000 {
		return ErrInvalid
	}
	if in.RuleType == "stale" {
		if in.TargetField != "updated_at" || in.StaleAfterDays < 1 || in.StaleAfterDays > 3650 {
			return ErrInvalid
		}
		return nil
	}
	if in.StaleAfterDays != 0 {
		return ErrInvalid
	}
	if builtinTarget(in.ResourceType, in.TargetField) {
		return nil
	}
	if !strings.HasPrefix(in.TargetField, "custom:") || len(strings.TrimPrefix(in.TargetField, "custom:")) == 0 {
		return ErrInvalid
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM custom_field_definitions WHERE resource_type=$1 AND field_key=$2)`, in.ResourceType, strings.TrimPrefix(in.TargetField, "custom:")).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return ErrInvalid
	}
	return nil
}
func scanRule(row pgx.Row) (Rule, error) {
	var item Rule
	err := row.Scan(&item.ID, &item.RuleName, &item.Description, &item.ResourceType, &item.RuleType, &item.TargetField, &item.Severity, &item.StaleAfterDays, &item.IsEnabled, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

const fields = `id::text,rule_name,COALESCE(description,''),resource_type,rule_type,target_field,severity,COALESCE(stale_after_days,0),is_enabled,sort_order,created_at,updated_at`

func (s *Service) List(ctx context.Context, p auth.Principal) ([]Rule, error) {
	result, err := s.Search(ctx, p, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

func (s *Service) Search(ctx context.Context, p auth.Principal, page pagination.Query) (pagination.Result[Rule], error) {
	if err := isAdmin(p); err != nil {
		return pagination.Result[Rule]{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+fields+`,count(*) OVER () FROM data_quality_rules ORDER BY resource_type,sort_order,created_at LIMIT $1 OFFSET $2`, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Rule]{}, err
	}
	defer rows.Close()
	items := make([]Rule, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Rule
		if err := rows.Scan(&item.ID, &item.RuleName, &item.Description, &item.ResourceType, &item.RuleType, &item.TargetField, &item.Severity, &item.StaleAfterDays, &item.IsEnabled, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Rule]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Rule]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_rules`).Scan(&total); err != nil {
			return pagination.Result[Rule]{}, err
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) Create(ctx context.Context, p auth.Principal, in Input, rid, ip string) (Rule, error) {
	if err := isAdmin(p); err != nil {
		return Rule{}, err
	}
	normalize(&in)
	if err := s.valid(ctx, in); err != nil {
		return Rule{}, err
	}
	item, err := scanRule(s.pool.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,description,resource_type,rule_type,target_field,severity,stale_after_days,is_enabled,sort_order,created_by) VALUES($1,NULLIF($2,''),$3,$4,$5,$6,NULLIF($7,0),$8,$9,$10::uuid) RETURNING `+fields, in.RuleName, in.Description, in.ResourceType, in.RuleType, in.TargetField, in.Severity, in.StaleAfterDays, in.IsEnabled, in.SortOrder, p.UserID))
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return Rule{}, ErrConflict
		}
		return Rule{}, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_logs(user_id,action,resource_type,resource_id,summary,request_ip,request_id) VALUES($1::uuid,'data_quality_rule.create','data_quality_rule',$2::uuid,$3,NULLIF($4,'')::inet,NULLIF($5,''))`, p.UserID, item.ID, "新增数据质量规则："+item.RuleName, ip, rid)
	return item, err
}
func (s *Service) Update(ctx context.Context, p auth.Principal, id string, in Input, rid, ip string) (Rule, error) {
	if err := isAdmin(p); err != nil {
		return Rule{}, err
	}
	normalize(&in)
	if err := s.valid(ctx, in); err != nil {
		return Rule{}, err
	}
	item, err := scanRule(s.pool.QueryRow(ctx, `UPDATE data_quality_rules SET rule_name=$2,description=NULLIF($3,''),resource_type=$4,rule_type=$5,target_field=$6,severity=$7,stale_after_days=NULLIF($8,0),is_enabled=$9,sort_order=$10,updated_at=now() WHERE id=$1::uuid RETURNING `+fields, id, in.RuleName, in.Description, in.ResourceType, in.RuleType, in.TargetField, in.Severity, in.StaleAfterDays, in.IsEnabled, in.SortOrder))
	if errors.Is(err, pgx.ErrNoRows) {
		return Rule{}, ErrNotFound
	}
	if err != nil {
		if strings.Contains(err.Error(), "duplicate key") {
			return Rule{}, ErrConflict
		}
		return Rule{}, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO audit_logs(user_id,action,resource_type,resource_id,summary,request_ip,request_id) VALUES($1::uuid,'data_quality_rule.update','data_quality_rule',$2::uuid,$3,NULLIF($4,'')::inet,NULLIF($5,''))`, p.UserID, item.ID, "更新数据质量规则："+item.RuleName, ip, rid)
	return item, err
}
