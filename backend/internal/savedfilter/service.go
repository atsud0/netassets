// Package savedfilter manages bounded, user-private saved filter views.
package savedfilter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

const MaxViewsPerPage = 20

var (
	ErrInvalid = errors.New("invalid input")
	// ErrFilterField carries the offending filter key and why it was refused, so the
	// caller can name the field instead of asking the user to re-check everything. A
	// single long search keyword and an unknown filter key are both "invalid", but only
	// one of them is something the user can see and fix.
	ErrFilterField = errors.New("invalid filter field")
	ErrNotFound    = errors.New("not found")
	ErrConflict    = errors.New("conflict")
	ErrLimit       = errors.New("view limit reached")
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

var pageDefinitions = map[string]map[string]fieldDefinition{
	"assets": {
		"q":               {maxLength: 200},
		"type":            {allowedValues: valueSet("domain", "ip", "url", "service", "other")},
		"status":          {allowedValues: valueSet("active", "pending", "disabled", "archived")},
		"organization_id": {uuid: true},
	},
	"security_work_orders": {
		"organization_id": {uuid: true},
		"status":          {allowedValues: valueSet("draft", "issued", "in_progress", "pending_verification", "returned", "closed", "cancelled")},
		"overdue":         {allowedValues: valueSet("true", "false")},
	},
	"data_quality_issues": {
		"organization_id": {uuid: true},
		"status":          {allowedValues: valueSet("pending", "in_progress", "pending_review", "closed", "exempted")},
		"severity":        {allowedValues: valueSet("error", "warning")},
		"resource_type":   {allowedValues: valueSet("asset", "business_system")},
		"assignee":        {uuidOrMe: true},
	},
}

type fieldDefinition struct {
	allowedValues map[string]struct{}
	maxLength     int
	uuid          bool
	uuidOrMe      bool
}

type View struct {
	ID        string            `json:"id"`
	PageKey   string            `json:"page_key"`
	Name      string            `json:"name"`
	Filters   map[string]string `json:"filters"`
	IsDefault bool              `json:"is_default"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type Input struct {
	PageKey   string            `json:"page_key"`
	Name      string            `json:"name"`
	Filters   map[string]string `json:"filters"`
	IsDefault bool              `json:"is_default"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// FieldError names the filter key that was refused and the reason, so the API can tell
// the user which of their inputs to change instead of listing every possible cause.
type FieldError struct {
	Field  string
	Reason string
}

func (e *FieldError) Error() string { return e.Reason }
func (e *FieldError) Unwrap() error { return ErrFilterField }

func filterError(field, reason string) error { return &FieldError{Field: field, Reason: reason} }

// FilterFieldLabel maps a saved-view filter key to the label the page shows for it, so
// an error can say "搜索关键词" instead of "q".
var FilterFieldLabel = map[string]map[string]string{
	"assets": {
		"q": "搜索关键词", "type": "资产类型", "status": "资产状态", "organization_id": "所属组织",
	},
	"security_work_orders": {
		"organization_id": "责任组织", "status": "工单状态", "overdue": "是否逾期",
	},
	"data_quality_issues": {
		"organization_id": "责任组织", "status": "问题状态", "severity": "问题级别",
		"resource_type": "资源类型", "assignee": "负责人",
	},
}

func FieldLabel(pageKey, field string) string {
	if labels := FilterFieldLabel[pageKey]; labels != nil {
		if label, ok := labels[field]; ok {
			return label
		}
	}
	return field
}

func NormalizeInput(input Input) (Input, error) {
	input.PageKey = strings.TrimSpace(input.PageKey)
	input.Name = strings.TrimSpace(input.Name)
	definition, ok := pageDefinitions[input.PageKey]
	switch {
	case !ok:
		return Input{}, invalidInput("请选择支持保存筛选视图的页面")
	case input.Name == "":
		return Input{}, invalidInput("视图名称不能为空")
	case utf8.RuneCountInString(input.Name) > 100:
		return Input{}, invalidInput("视图名称最多 100 字")
	}
	if input.Filters == nil {
		input.Filters = map[string]string{}
	}
	normalized := make(map[string]string, len(input.Filters))
	// Iterate in a fixed order so that a request with several bad filters always
	// reports the same one. Map order would otherwise make the message a lottery.
	for _, key := range sortedFilterKeys(input.Filters) {
		raw := input.Filters[key]
		field, exists := definition[key]
		if !exists {
			return Input{}, filterError(key, fmt.Sprintf("筛选条件“%s”不被当前页面支持", key))
		}
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		maxLength := field.maxLength
		if maxLength == 0 {
			maxLength = 128
		}
		if utf8.RuneCountInString(value) > maxLength {
			return Input{}, filterError(key, fmt.Sprintf("筛选条件“%s”最多 %d 个字，当前 %d 个字", FieldLabel(input.PageKey, key), maxLength, utf8.RuneCountInString(value)))
		}
		if len(field.allowedValues) > 0 {
			if _, exists := field.allowedValues[value]; !exists {
				return Input{}, filterError(key, fmt.Sprintf("筛选条件“%s”的取值不被支持", FieldLabel(input.PageKey, key)))
			}
		}
		if field.uuid && !uuidPattern.MatchString(value) {
			return Input{}, filterError(key, fmt.Sprintf("筛选条件“%s”的取值不正确", FieldLabel(input.PageKey, key)))
		}
		if field.uuidOrMe && value != "me" && !uuidPattern.MatchString(value) {
			return Input{}, filterError(key, fmt.Sprintf("筛选条件“%s”的取值不正确", FieldLabel(input.PageKey, key)))
		}
		normalized[key] = value
	}
	input.Filters = normalized
	return input, nil
}

func sortedFilterKeys(filters map[string]string) []string {
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func ValidPageKey(pageKey string) bool {
	_, ok := pageDefinitions[strings.TrimSpace(pageKey)]
	return ok
}

func (s *Service) Search(ctx context.Context, p auth.Principal, pageKey string, page pagination.Query) (pagination.Result[View], error) {
	pageKey = strings.TrimSpace(pageKey)
	if p.UserID == "" || !ValidPageKey(pageKey) {
		return pagination.Result[View]{}, ErrInvalid
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text,page_key,name,filters,is_default,
		       created_at,updated_at,count(*) OVER()
		FROM saved_filter_views
		WHERE user_id=$1::uuid AND page_key=$2
		ORDER BY is_default DESC,updated_at DESC,id
		LIMIT $3 OFFSET $4`, p.UserID, pageKey, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[View]{}, fmt.Errorf("list saved filter views: %w", err)
	}
	defer rows.Close()
	items := make([]View, 0)
	total := 0
	for rows.Next() {
		var item View
		var raw []byte
		if err := rows.Scan(&item.ID, &item.PageKey, &item.Name, &raw, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[View]{}, fmt.Errorf("scan saved filter view: %w", err)
		}
		if err := json.Unmarshal(raw, &item.Filters); err != nil {
			return pagination.Result[View]{}, fmt.Errorf("decode saved filter view: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[View]{}, fmt.Errorf("iterate saved filter views: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM saved_filter_views WHERE user_id=$1::uuid AND page_key=$2`, p.UserID, pageKey).Scan(&total); err != nil {
			return pagination.Result[View]{}, fmt.Errorf("count saved filter views: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) Create(ctx context.Context, p auth.Principal, input Input, requestID, ipAddress string) (View, error) {
	// The validation error is returned as-is. Swallowing it here and re-reporting a
	// generic ErrInvalid is what made every distinct refusal - an over-long keyword,
	// an unsupported filter key - reach the user as the same unusable message.
	input, err := NormalizeInput(input)
	if err != nil {
		return View{}, err
	}
	if p.UserID == "" {
		return View{}, ErrInvalid
	}
	filters, err := json.Marshal(input.Filters)
	if err != nil {
		return View{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin saved filter view create: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, p.UserID+":"+input.PageKey); err != nil {
		return View{}, fmt.Errorf("lock saved filter views: %w", err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_filter_views WHERE user_id=$1::uuid AND page_key=$2`, p.UserID, input.PageKey).Scan(&count); err != nil {
		return View{}, fmt.Errorf("count saved filter views: %w", err)
	}
	if count >= MaxViewsPerPage {
		return View{}, ErrLimit
	}
	if input.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE saved_filter_views SET is_default=false,updated_at=now() WHERE user_id=$1::uuid AND page_key=$2 AND is_default`, p.UserID, input.PageKey); err != nil {
			return View{}, fmt.Errorf("clear saved filter default: %w", err)
		}
	}
	item, err := insertView(ctx, tx, p.UserID, input, filters)
	if err != nil {
		return View{}, mapDatabaseError(err)
	}
	if err := writeAudit(ctx, tx, p.UserID, "saved_filter_view.create", item.ID, "保存个人筛选视图："+item.Name, requestID, ipAddress); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit saved filter view create: %w", err)
	}
	return item, nil
}

func (s *Service) Update(ctx context.Context, p auth.Principal, id string, input Input, requestID, ipAddress string) (View, error) {
	input, err := NormalizeInput(input)
	if err != nil {
		return View{}, err
	}
	if p.UserID == "" || !uuidPattern.MatchString(strings.TrimSpace(id)) {
		return View{}, invalidInput("保存视图标识不正确")
	}
	filters, _ := json.Marshal(input.Filters)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return View{}, fmt.Errorf("begin saved filter view update: %w", err)
	}
	defer tx.Rollback(ctx)
	var existingPage string
	if err := tx.QueryRow(ctx, `SELECT page_key FROM saved_filter_views WHERE id=$1::uuid AND user_id=$2::uuid FOR UPDATE`, id, p.UserID).Scan(&existingPage); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return View{}, ErrNotFound
		}
		return View{}, fmt.Errorf("find saved filter view: %w", err)
	}
	if existingPage != input.PageKey {
		return View{}, invalidInput("保存视图所属页面不能修改")
	}
	if input.IsDefault {
		if _, err := tx.Exec(ctx, `UPDATE saved_filter_views SET is_default=false,updated_at=now() WHERE user_id=$1::uuid AND page_key=$2 AND id<>$3::uuid AND is_default`, p.UserID, input.PageKey, id); err != nil {
			return View{}, fmt.Errorf("clear saved filter default: %w", err)
		}
	}
	item, err := updateView(ctx, tx, p.UserID, id, input, filters)
	if err != nil {
		return View{}, mapDatabaseError(err)
	}
	if err := writeAudit(ctx, tx, p.UserID, "saved_filter_view.update", item.ID, "更新个人筛选视图："+item.Name, requestID, ipAddress); err != nil {
		return View{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return View{}, fmt.Errorf("commit saved filter view update: %w", err)
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, p auth.Principal, id, requestID, ipAddress string) error {
	if p.UserID == "" || !uuidPattern.MatchString(strings.TrimSpace(id)) {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin saved filter view delete: %w", err)
	}
	defer tx.Rollback(ctx)
	var name string
	if err := tx.QueryRow(ctx, `DELETE FROM saved_filter_views WHERE id=$1::uuid AND user_id=$2::uuid RETURNING name`, id, p.UserID).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("delete saved filter view: %w", err)
	}
	if err := writeAudit(ctx, tx, p.UserID, "saved_filter_view.delete", id, "删除个人筛选视图："+name, requestID, ipAddress); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit saved filter view delete: %w", err)
	}
	return nil
}

func insertView(ctx context.Context, tx pgx.Tx, userID string, input Input, filters []byte) (View, error) {
	var item View
	var raw []byte
	err := tx.QueryRow(ctx, `
		INSERT INTO saved_filter_views(user_id,page_key,name,filters,is_default)
		VALUES($1::uuid,$2,$3,$4::jsonb,$5)
		RETURNING id::text,page_key,name,filters,is_default,created_at,updated_at`,
		userID, input.PageKey, input.Name, filters, input.IsDefault,
	).Scan(&item.ID, &item.PageKey, &item.Name, &raw, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &item.Filters)
	}
	return item, err
}

func updateView(ctx context.Context, tx pgx.Tx, userID, id string, input Input, filters []byte) (View, error) {
	var item View
	var raw []byte
	err := tx.QueryRow(ctx, `
		UPDATE saved_filter_views SET name=$3,filters=$4::jsonb,is_default=$5,updated_at=now()
		WHERE id=$1::uuid AND user_id=$2::uuid
		RETURNING id::text,page_key,name,filters,is_default,created_at,updated_at`,
		id, userID, input.Name, filters, input.IsDefault,
	).Scan(&item.ID, &item.PageKey, &item.Name, &raw, &item.IsDefault, &item.CreatedAt, &item.UpdatedAt)
	if err == nil {
		err = json.Unmarshal(raw, &item.Filters)
	}
	return item, err
}

func writeAudit(ctx context.Context, tx pgx.Tx, userID, action, resourceID, summary, requestID, ipAddress string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO audit_logs(user_id,action,resource_type,resource_id,summary,request_ip,request_id)
		VALUES($1::uuid,$2,'saved_filter_view',$3,$4,NULLIF($5,'')::inet,NULLIF($6,''))`,
		userID, action, resourceID, summary, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write saved filter view audit: %w", err)
	}
	return nil
}

func mapDatabaseError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return fmt.Errorf("save filter view: %w", err)
}

func valueSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
