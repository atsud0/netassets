package dataquality

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/assetobservation"
	"netassets/internal/auth"
	"netassets/internal/pagination"
	"netassets/internal/textlength"
)

var (
	ErrForbidden         = errors.New("forbidden")
	ErrNotFound          = errors.New("not found")
	ErrInvalid           = errors.New("invalid")
	ErrConflict          = errors.New("conflict")
	ErrInvalidTransition = errors.New("invalid transition")
)

// RuleValidationError exposes only an actionable, safe rule-input message.
// It preserves ErrInvalid for existing callers and error-code mappings.
type RuleValidationError struct {
	Message string
}

func (e *RuleValidationError) Error() string { return e.Message }
func (e *RuleValidationError) Unwrap() error { return ErrInvalid }

func invalidRule(message string) error { return &RuleValidationError{Message: message} }

const (
	maxRuleConditions     = 10
	maxConditionValues    = 50
	maxConditionValueSize = 256
)

var conditionUUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type RuleCondition struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

func (condition *RuleCondition) UnmarshalJSON(data []byte) error {
	var wire struct {
		Field    string   `json:"field"`
		Operator string   `json:"operator"`
		Values   []string `json:"values"`
		Value    string   `json:"value"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	condition.Field = wire.Field
	condition.Operator = wire.Operator
	condition.Values = wire.Values
	if len(condition.Values) == 0 && strings.TrimSpace(wire.Value) != "" {
		condition.Values = []string{wire.Value}
	}
	switch strings.ToLower(strings.TrimSpace(condition.Operator)) {
	case "equals", "equal":
		condition.Operator = "eq"
	case "not_equals", "not_equal":
		condition.Operator = "ne"
	}
	return nil
}

type Rule struct {
	ID                string          `json:"id"`
	RuleName          string          `json:"rule_name"`
	Description       string          `json:"description,omitempty"`
	ResourceType      string          `json:"resource_type"`
	RuleType          string          `json:"rule_type"`
	TargetField       string          `json:"target_field"`
	Severity          string          `json:"severity"`
	StaleAfterDays    int             `json:"stale_after_days,omitempty"`
	ExpiryWarningDays int             `json:"expiry_warning_days,omitempty"`
	Conditions        []RuleCondition `json:"conditions"`
	IsEnabled         bool            `json:"is_enabled"`
	SortOrder         int             `json:"sort_order"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type Input struct {
	RuleName          string          `json:"rule_name"`
	Description       string          `json:"description"`
	ResourceType      string          `json:"resource_type"`
	RuleType          string          `json:"rule_type"`
	TargetField       string          `json:"target_field"`
	Severity          string          `json:"severity"`
	StaleAfterDays    int             `json:"stale_after_days"`
	ExpiryWarningDays int             `json:"expiry_warning_days"`
	Conditions        []RuleCondition `json:"conditions"`
	IsEnabled         bool            `json:"is_enabled"`
	SortOrder         int             `json:"sort_order"`
}

type conditionFieldSpec struct {
	kind          string
	allowedValues map[string]bool
}

type Service struct {
	pool                 *pgxpool.Pool
	observationRefresher externalObservationRefresher
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, observationRefresher: assetobservation.NewService(pool)}
}

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

func stringSet(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

func builtinConditionField(resourceType, field string) (conditionFieldSpec, bool) {
	asset := map[string]conditionFieldSpec{
		"asset_type":          {kind: "enum", allowedValues: stringSet("domain", "ip", "url", "service", "other")},
		"status":              {kind: "enum", allowedValues: stringSet("active", "pending", "disabled", "archived")},
		"environment":         {kind: "enum", allowedValues: stringSet("production", "staging", "testing", "development", "office", "other")},
		"importance":          {kind: "enum", allowedValues: stringSet("normal", "important", "core")},
		"network_scope":       {kind: "enum", allowedValues: stringSet("public", "private", "unknown")},
		"organization_id":     {kind: "uuid"},
		"network_area_id":     {kind: "uuid"},
		"business_system_id":  {kind: "uuid"},
		"has_business_system": {kind: "presence"},
	}
	businessSystem := map[string]conditionFieldSpec{
		"status":                        {kind: "enum", allowedValues: stringSet("planning", "running", "suspended", "offline", "archived")},
		"system_level":                  {kind: "enum", allowedValues: stringSet("normal", "important", "core")},
		"deployment_type":               {kind: "text"},
		"internet_exposure":             {kind: "enum", allowedValues: stringSet("yes", "no", "partial", "unknown")},
		"contains_personal_information": {kind: "enum", allowedValues: stringSet("yes", "no", "unknown")},
		"contains_sensitive_personal_information": {kind: "enum", allowedValues: stringSet("yes", "no", "unknown")},
		"data_volume_over_100k":                   {kind: "enum", allowedValues: stringSet("yes", "no", "unknown")},
		"contains_important_data":                 {kind: "enum", allowedValues: stringSet("yes", "no", "unknown", "pending")},
		"contains_core_data":                      {kind: "enum", allowedValues: stringSet("yes", "no", "unknown", "pending")},
		"organization_id":                         {kind: "uuid"},
	}
	if resourceType == "asset" {
		spec, ok := asset[field]
		return spec, ok
	}
	if resourceType == "business_system" {
		spec, ok := businessSystem[field]
		return spec, ok
	}
	return conditionFieldSpec{}, false
}

func normalize(in *Input) {
	in.RuleName = strings.TrimSpace(in.RuleName)
	in.Description = strings.TrimSpace(in.Description)
	in.ResourceType = strings.TrimSpace(in.ResourceType)
	in.RuleType = strings.TrimSpace(in.RuleType)
	in.TargetField = strings.ToLower(strings.TrimSpace(in.TargetField))
	in.Severity = strings.TrimSpace(in.Severity)
	if in.Conditions == nil {
		in.Conditions = []RuleCondition{}
	}
	for index := range in.Conditions {
		condition := &in.Conditions[index]
		condition.Field = strings.ToLower(strings.TrimSpace(condition.Field))
		condition.Operator = strings.ToLower(strings.TrimSpace(condition.Operator))
		seen := map[string]bool{}
		values := make([]string, 0, len(condition.Values))
		for _, raw := range condition.Values {
			value := strings.TrimSpace(raw)
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
		sort.Strings(values)
		condition.Values = values
	}
	sort.Slice(in.Conditions, func(i, j int) bool {
		left := in.Conditions[i]
		right := in.Conditions[j]
		if left.Field != right.Field {
			return left.Field < right.Field
		}
		if left.Operator != right.Operator {
			return left.Operator < right.Operator
		}
		return strings.Join(left.Values, "\x00") < strings.Join(right.Values, "\x00")
	})
}

func normalizeLoadedConditions(resourceType string, conditions []RuleCondition) []RuleCondition {
	for index := range conditions {
		condition := &conditions[index]
		condition.Field = strings.ToLower(strings.TrimSpace(condition.Field))
		condition.Operator = strings.ToLower(strings.TrimSpace(condition.Operator))
		if resourceType == "business_system" && condition.Field == "system_status" {
			condition.Field = "status"
		}
		if resourceType == "asset" && condition.Field == "asset_status" {
			condition.Field = "status"
		}
	}
	return conditions
}

func (s *Service) customConditionField(ctx context.Context, resourceType, field string) (conditionFieldSpec, error) {
	if !strings.HasPrefix(field, "custom:") {
		return conditionFieldSpec{}, ErrInvalid
	}
	key := strings.TrimPrefix(field, "custom:")
	if key == "" {
		return conditionFieldSpec{}, ErrInvalid
	}
	var fieldType string
	var rawOptions []byte
	if err := s.pool.QueryRow(ctx, `SELECT field_type,options FROM custom_field_definitions WHERE resource_type=$1 AND field_key=$2`, resourceType, key).Scan(&fieldType, &rawOptions); errors.Is(err, pgx.ErrNoRows) {
		return conditionFieldSpec{}, ErrInvalid
	} else if err != nil {
		return conditionFieldSpec{}, err
	}
	switch fieldType {
	case "boolean":
		return conditionFieldSpec{kind: "boolean"}, nil
	case "select", "multiselect":
		var options []string
		if err := json.Unmarshal(rawOptions, &options); err != nil || len(options) == 0 {
			return conditionFieldSpec{}, ErrInvalid
		}
		kind := "enum"
		if fieldType == "multiselect" {
			kind = "multiselect"
		}
		return conditionFieldSpec{kind: kind, allowedValues: stringSet(options...)}, nil
	case "text", "textarea", "number", "date":
		return conditionFieldSpec{kind: "text"}, nil
	default:
		return conditionFieldSpec{}, ErrInvalid
	}
}

func validConditionValues(condition RuleCondition, spec conditionFieldSpec) error {
	valueCount := len(condition.Values)
	for _, value := range condition.Values {
		if len(value) > maxConditionValueSize {
			return invalidRule("条件值不能超过 256 字节")
		}
		if spec.kind == "uuid" && !conditionUUIDPattern.MatchString(value) {
			return invalidRule("条件值中的组织或网络区域标识格式不正确")
		}
		if spec.allowedValues != nil && !spec.allowedValues[value] {
			return invalidRule("条件值必须选择当前字段支持的选项")
		}
	}

	switch spec.kind {
	case "presence":
		if (condition.Operator != "is_true" && condition.Operator != "is_false") || valueCount != 0 {
			return invalidRule("关联存在性条件仅支持为真或为假，且无需填写条件值")
		}
		return nil
	case "boolean":
		if condition.Operator != "is_true" && condition.Operator != "is_false" && condition.Operator != "empty" && condition.Operator != "not_empty" {
			return invalidRule("布尔条件仅支持为真、为假、为空或不为空")
		}
		if valueCount != 0 {
			return invalidRule("当前条件运算符无需填写条件值")
		}
		return nil
	case "multiselect":
		if condition.Operator == "empty" || condition.Operator == "not_empty" {
			if valueCount != 0 {
				return invalidRule("为空或不为空条件无需填写条件值")
			}
			return nil
		}
		if condition.Operator != "in" && condition.Operator != "not_in" {
			return invalidRule("多选字段条件仅支持包含、排除、为空或不为空")
		}
	default:
		if condition.Operator == "empty" || condition.Operator == "not_empty" {
			if valueCount != 0 {
				return invalidRule("为空或不为空条件无需填写条件值")
			}
			return nil
		}
		if condition.Operator != "eq" && condition.Operator != "ne" && condition.Operator != "in" && condition.Operator != "not_in" {
			return invalidRule("请选择当前字段支持的条件运算符")
		}
	}

	if condition.Operator == "eq" || condition.Operator == "ne" {
		if valueCount != 1 {
			return invalidRule("等于或不等于条件必须填写一个条件值")
		}
		return nil
	}
	if valueCount < 1 || valueCount > maxConditionValues {
		return invalidRule("包含或排除条件须填写 1 至 50 个条件值")
	}
	return nil
}

func (s *Service) validateConditions(ctx context.Context, resourceType string, conditions []RuleCondition) error {
	if len(conditions) > maxRuleConditions {
		return invalidRule("生效条件最多 10 条")
	}
	seen := map[string]bool{}
	for index, condition := range conditions {
		if condition.Field == "" || len(condition.Field) > 128 || condition.Operator == "" {
			return invalidRule(fmt.Sprintf("第 %d 条生效条件的字段和运算符不能为空，字段最多 128 字节", index+1))
		}
		spec, ok := builtinConditionField(resourceType, condition.Field)
		var err error
		if !ok {
			spec, err = s.customConditionField(ctx, resourceType, condition.Field)
			if errors.Is(err, ErrInvalid) {
				return invalidRule(fmt.Sprintf("第 %d 条生效条件请选择有效字段", index+1))
			}
			if err != nil {
				return err
			}
		}
		if err := validConditionValues(condition, spec); err != nil {
			var validationError *RuleValidationError
			if errors.As(err, &validationError) {
				return invalidRule(fmt.Sprintf("第 %d 条生效条件：%s", index+1, validationError.Message))
			}
			return err
		}
		raw, _ := json.Marshal(condition)
		key := string(raw)
		if seen[key] {
			return invalidRule(fmt.Sprintf("第 %d 条生效条件与前面的条件重复", index+1))
		}
		seen[key] = true
	}
	return nil
}

func (s *Service) valid(ctx context.Context, in Input) error {
	if in.RuleName == "" {
		return invalidRule("规则名称不能为空")
	}
	if textlength.Count(in.RuleName) > 128 {
		return invalidRule("规则名称最多 128 字")
	}
	if in.ResourceType != "asset" && in.ResourceType != "business_system" {
		return invalidRule("请选择有效的检查对象")
	}
	if in.RuleType != "required" && in.RuleType != "stale" && in.RuleType != "expiry" {
		return invalidRule("请选择有效的规则类型")
	}
	if in.Severity != "error" && in.Severity != "warning" {
		return invalidRule("请选择有效的问题等级")
	}
	if in.SortOrder < 0 || in.SortOrder > 100000 {
		return invalidRule("排序须为 0 至 100000 的整数")
	}
	if in.RuleType == "stale" {
		if in.TargetField != "updated_at" {
			return invalidRule("长期未维护规则的检查项须为最后更新时间")
		}
		if in.StaleAfterDays < 1 || in.StaleAfterDays > 3650 {
			return invalidRule("未维护天数须为 1 至 3650 的整数")
		}
		if in.ExpiryWarningDays != 0 {
			return invalidRule("长期未维护规则不支持提前提醒天数")
		}
	} else if in.RuleType == "expiry" {
		if in.ResourceType != "asset" {
			return invalidRule("到期提醒规则的检查对象须为网络资产")
		}
		if in.TargetField != "expires_at" && !isExternalExpiryTarget(in.TargetField) {
			return invalidRule("请选择有效的到期提醒检查项")
		}
		if in.StaleAfterDays != 0 {
			return invalidRule("到期提醒规则不支持未维护天数")
		}
		if in.ExpiryWarningDays < 0 || in.ExpiryWarningDays > 3650 {
			return invalidRule("提前提醒天数须为 0 至 3650 的整数")
		}
	} else {
		if in.StaleAfterDays != 0 {
			return invalidRule("必填检查规则不支持未维护天数")
		}
		if in.ExpiryWarningDays != 0 {
			return invalidRule("必填检查规则不支持提前提醒天数")
		}
		if !builtinTarget(in.ResourceType, in.TargetField) {
			if !strings.HasPrefix(in.TargetField, "custom:") || len(strings.TrimPrefix(in.TargetField, "custom:")) == 0 {
				return invalidRule("请选择有效的必填检查项")
			}
			var exists bool
			err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM custom_field_definitions WHERE resource_type=$1 AND field_key=$2)`, in.ResourceType, strings.TrimPrefix(in.TargetField, "custom:")).Scan(&exists)
			if err != nil {
				return err
			}
			if !exists {
				return invalidRule("请选择有效的必填检查项")
			}
		}
	}
	return s.validateConditions(ctx, in.ResourceType, in.Conditions)
}

func scanRule(row pgx.Row) (Rule, error) {
	var item Rule
	var rawConditions []byte
	err := row.Scan(&item.ID, &item.RuleName, &item.Description, &item.ResourceType, &item.RuleType, &item.TargetField, &item.Severity, &item.StaleAfterDays, &item.ExpiryWarningDays, &rawConditions, &item.IsEnabled, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return item, err
	}
	if err := json.Unmarshal(rawConditions, &item.Conditions); err != nil {
		return item, err
	}
	if item.Conditions == nil {
		item.Conditions = []RuleCondition{}
	}
	item.Conditions = normalizeLoadedConditions(item.ResourceType, item.Conditions)
	return item, nil
}

const fields = `id::text,rule_name,COALESCE(description,''),resource_type,rule_type,target_field,severity,COALESCE(stale_after_days,0),COALESCE(expiry_warning_days,0),conditions,is_enabled,sort_order,created_at,updated_at`

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
		var rawConditions []byte
		if err := rows.Scan(&item.ID, &item.RuleName, &item.Description, &item.ResourceType, &item.RuleType, &item.TargetField, &item.Severity, &item.StaleAfterDays, &item.ExpiryWarningDays, &rawConditions, &item.IsEnabled, &item.SortOrder, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Rule]{}, err
		}
		if err := json.Unmarshal(rawConditions, &item.Conditions); err != nil {
			return pagination.Result[Rule]{}, err
		}
		if item.Conditions == nil {
			item.Conditions = []RuleCondition{}
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
	rawConditions, _ := json.Marshal(in.Conditions)
	item, err := scanRule(s.pool.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,description,resource_type,rule_type,target_field,severity,stale_after_days,expiry_warning_days,conditions,is_enabled,sort_order,created_by) VALUES($1,NULLIF($2,''),$3,$4::varchar,$5,$6,NULLIF($7,0),CASE WHEN $4::varchar='expiry' THEN $8::integer ELSE NULL::integer END,$9::jsonb,$10,$11,$12::uuid) RETURNING `+fields, in.RuleName, in.Description, in.ResourceType, in.RuleType, in.TargetField, in.Severity, in.StaleAfterDays, in.ExpiryWarningDays, rawConditions, in.IsEnabled, in.SortOrder, p.UserID))
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
	rawConditions, _ := json.Marshal(in.Conditions)
	item, err := scanRule(s.pool.QueryRow(ctx, `UPDATE data_quality_rules SET rule_name=$2,description=NULLIF($3,''),resource_type=$4,rule_type=$5::varchar,target_field=$6,severity=$7,stale_after_days=NULLIF($8,0),expiry_warning_days=CASE WHEN $5::varchar='expiry' THEN $9::integer ELSE NULL::integer END,conditions=$10::jsonb,is_enabled=$11,sort_order=$12,updated_at=now() WHERE id=$1::uuid RETURNING `+fields, id, in.RuleName, in.Description, in.ResourceType, in.RuleType, in.TargetField, in.Severity, in.StaleAfterDays, in.ExpiryWarningDays, rawConditions, in.IsEnabled, in.SortOrder))
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
