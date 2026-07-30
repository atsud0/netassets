package auditlog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

var (
	ErrForbidden = errors.New("forbidden")
	ErrInvalid   = errors.New("invalid input")
	uuidPattern  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type Filter struct {
	Page           int
	PageSize       int
	Action         string
	ResourceType   string
	Result         string
	UserID         string
	OrganizationID string
	Keyword        string
	StartAt        *time.Time
	EndAt          *time.Time
}

type Item struct {
	ID                int64           `json:"id"`
	UserID            string          `json:"user_id,omitempty"`
	Username          string          `json:"username,omitempty"`
	DisplayName       string          `json:"display_name,omitempty"`
	OrganizationID    string          `json:"organization_id,omitempty"`
	OrganizationCode  string          `json:"organization_code,omitempty"`
	OrganizationName  string          `json:"organization_name,omitempty"`
	Action            string          `json:"action"`
	ActionLabel       string          `json:"action_label"`
	ResourceType      string          `json:"resource_type,omitempty"`
	ResourceTypeLabel string          `json:"resource_type_label,omitempty"`
	ResourceID        string          `json:"resource_id,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	Details           json.RawMessage `json:"details"`
	RequestIP         string          `json:"request_ip,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	Result            string          `json:"result"`
	ResultLabel       string          `json:"result_label"`
	CreatedAt         time.Time       `json:"created_at"`
}

type ListResult struct {
	Items    []Item `json:"items"`
	Total    int64  `json:"total"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type Options struct {
	Actions       []Option `json:"actions"`
	ResourceTypes []Option `json:"resource_types"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal, filter Filter) (ListResult, error) {
	if !principal.HasRole("group_admin") {
		return ListResult{}, ErrForbidden
	}
	filter, err := NormalizeFilter(filter)
	if err != nil {
		return ListResult{}, err
	}

	args := make([]any, 0, 12)
	conditions := []string{"1 = 1"}
	add := func(value any, condition string) {
		args = append(args, value)
		conditions = append(conditions, fmt.Sprintf(condition, len(args)))
	}
	if filter.Action != "" {
		add(filter.Action, "al.action = $%d")
	}
	if filter.ResourceType != "" {
		add(filter.ResourceType, "al.resource_type = $%d")
	}
	if filter.Result != "" {
		add(filter.Result, "al.result = $%d")
	}
	if filter.UserID != "" {
		add(filter.UserID, "al.user_id = $%d::uuid")
	}
	if filter.OrganizationID != "" {
		add(filter.OrganizationID, "al.organization_id = $%d::uuid")
	}
	if filter.Keyword != "" {
		keyword := "%" + filter.Keyword + "%"
		fields := []string{
			"al.action", "COALESCE(al.resource_type, '')", "COALESCE(al.resource_id, '')", "COALESCE(al.summary, '')", "al.details::text",
			"COALESCE(al.request_id, '')", "COALESCE(u.username, '')", "COALESCE(u.display_name, '')",
			"COALESCE(o.organization_code, '')", "COALESCE(o.organization_name, '')",
		}
		placeholders := make([]string, 0, len(fields))
		for _, field := range fields {
			args = append(args, keyword)
			placeholders = append(placeholders, fmt.Sprintf("%s ILIKE $%d", field, len(args)))
		}
		conditions = append(conditions, "("+strings.Join(placeholders, " OR ")+")")
	}
	if filter.StartAt != nil {
		add(*filter.StartAt, "al.created_at >= $%d")
	}
	if filter.EndAt != nil {
		add(*filter.EndAt, "al.created_at < $%d")
	}

	where := strings.Join(conditions, " AND ")
	countArgs := append([]any(nil), args...)
	var total int64
	if err := s.pool.QueryRow(ctx, fmt.Sprintf(`
		SELECT count(*)
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.user_id
		LEFT JOIN organizations o ON o.id = al.organization_id
		WHERE %s
	`, where), countArgs...).Scan(&total); err != nil {
		return ListResult{}, fmt.Errorf("count audit logs: %w", err)
	}

	limitArg := len(args) + 1
	offsetArg := len(args) + 2
	args = append(args, filter.PageSize, (filter.Page-1)*filter.PageSize)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT al.id, COALESCE(al.user_id::text, ''), COALESCE(u.username, ''), COALESCE(u.display_name, ''),
		       COALESCE(al.organization_id::text, ''), COALESCE(o.organization_code, ''), COALESCE(o.organization_name, ''),
		       al.action, COALESCE(al.resource_type, ''), COALESCE(al.resource_id, ''), COALESCE(al.summary, ''),
		       al.details, COALESCE(host(al.request_ip), ''), COALESCE(al.request_id, ''), al.result, al.created_at
		FROM audit_logs al
		LEFT JOIN users u ON u.id = al.user_id
		LEFT JOIN organizations o ON o.id = al.organization_id
		WHERE %s
		ORDER BY al.created_at DESC, al.id DESC
		LIMIT $%d OFFSET $%d
	`, where, limitArg, offsetArg), args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("list audit logs: %w", err)
	}
	defer rows.Close()

	items := make([]Item, 0)
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.ID, &item.UserID, &item.Username, &item.DisplayName,
			&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.Action, &item.ResourceType, &item.ResourceID, &item.Summary,
			&item.Details, &item.RequestIP, &item.RequestID, &item.Result, &item.CreatedAt); err != nil {
			return ListResult{}, fmt.Errorf("scan audit log: %w", err)
		}
		if len(item.Details) == 0 {
			item.Details = json.RawMessage(`{}`)
		}
		item.ActionLabel = ActionLabel(item.Action)
		item.ResourceTypeLabel = ResourceTypeLabel(item.ResourceType)
		item.ResultLabel = ResultLabel(item.Result)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("iterate audit logs: %w", err)
	}
	return ListResult{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (s *Service) Options(ctx context.Context, principal auth.Principal) (Options, error) {
	if !principal.HasRole("group_admin") {
		return Options{}, ErrForbidden
	}
	var result Options
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT action FROM audit_logs WHERE action <> '' ORDER BY action`)
	if err != nil {
		return Options{}, fmt.Errorf("list audit actions: %w", err)
	}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			rows.Close()
			return Options{}, fmt.Errorf("scan audit action: %w", err)
		}
		result.Actions = append(result.Actions, Option{Value: value, Label: ActionLabel(value)})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Options{}, fmt.Errorf("iterate audit actions: %w", err)
	}
	rows, err = s.pool.Query(ctx, `SELECT DISTINCT resource_type FROM audit_logs WHERE resource_type IS NOT NULL AND resource_type <> '' ORDER BY resource_type`)
	if err != nil {
		return Options{}, fmt.Errorf("list audit resource types: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return Options{}, fmt.Errorf("scan audit resource type: %w", err)
		}
		result.ResourceTypes = append(result.ResourceTypes, Option{Value: value, Label: ResourceTypeLabel(value)})
	}
	if err := rows.Err(); err != nil {
		return Options{}, fmt.Errorf("iterate audit resource types: %w", err)
	}
	return result, nil
}

func NormalizeFilter(filter Filter) (Filter, error) {
	if filter.Page < 1 {
		filter.Page = DefaultPage
	}
	if filter.PageSize == 0 {
		filter.PageSize = DefaultPageSize
	}
	if filter.PageSize < 1 || filter.PageSize > MaxPageSize {
		return Filter{}, ErrInvalid
	}
	filter.Action = strings.TrimSpace(filter.Action)
	filter.ResourceType = strings.TrimSpace(filter.ResourceType)
	filter.Result = strings.TrimSpace(filter.Result)
	filter.UserID = strings.TrimSpace(filter.UserID)
	filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
	filter.Keyword = strings.TrimSpace(filter.Keyword)
	if filter.Result != "" && filter.Result != "success" && filter.Result != "failure" {
		return Filter{}, ErrInvalid
	}
	if filter.UserID != "" {
		if _, err := parseUUID(filter.UserID); err != nil {
			return Filter{}, ErrInvalid
		}
	}
	if filter.OrganizationID != "" {
		if _, err := parseUUID(filter.OrganizationID); err != nil {
			return Filter{}, ErrInvalid
		}
	}
	if filter.StartAt != nil && filter.EndAt != nil && !filter.EndAt.After(*filter.StartAt) {
		return Filter{}, ErrInvalid
	}
	return filter, nil
}

func parseUUID(value string) (string, error) {
	if !uuidPattern.MatchString(value) {
		return "", errors.New("invalid uuid")
	}
	return value, nil
}

func ParseTime(value string, endExclusive bool) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return &parsed, nil
	}
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("CST", 8*60*60)
	}
	parsed, err := time.ParseInLocation("2006-01-02", value, location)
	if err != nil {
		return nil, ErrInvalid
	}
	if endExclusive {
		parsed = parsed.AddDate(0, 0, 1)
	}
	return &parsed, nil
}

func ActionLabel(value string) string {
	labels := map[string]string{
		"asset.create": "新增网络资产", "asset.update": "更新网络资产", "asset.delete": "删除网络资产", "asset.restore": "恢复网络资产", "asset.import": "导入网络资产", "asset.export": "导出网络资产",
		"auth.login": "登录", "auth.logout": "退出登录", "business_system.create": "新增业务系统", "business_system.update": "更新业务系统", "business_system.delete": "删除业务系统", "business_system.archive": "归档业务系统", "business_system.restore": "恢复业务系统", "business_system.import": "导入业务系统", "business_system.export": "导出业务系统",
		"inventory_task.create": "创建填报任务", "inventory_task.start": "开始填报", "inventory_task.submit": "提交填报", "inventory_task.review": "审核填报", "inventory_task.status_update": "更新填报任务状态", "inventory_task.export": "导出填报结果",
		"data_quality.scan": "执行数据质量巡检", "data_quality.scan.scheduled": "系统自动数据质量巡检",
		"organization.create": "新增组织", "organization.update": "更新组织", "user.create": "新增用户",
		"security_finding.create": "登记安全发现", "security_finding.update": "更新安全发现", "security_finding.import": "导入安全发现", "security_finding.accept_risk": "接受安全发现风险", "security_finding.revoke_risk": "撤销风险接受", "security_finding.review_risk": "复核风险接受",
		"security_work_order.create": "创建安全工单", "security_work_order.issue": "下发安全工单", "security_work_order.accept": "接收安全工单", "security_work_order.progress": "更新整改进度", "security_work_order.submit": "提交复测", "security_work_order.return": "退回安全工单", "security_work_order.close": "关闭安全工单", "security_work_order.cancel": "取消安全工单",
	}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func ResourceTypeLabel(value string) string {
	labels := map[string]string{"asset": "网络资产", "asset_import": "网络资产导入", "business_system": "业务系统", "business_system_import": "业务系统导入", "inventory_task": "填报任务", "data_quality_scan_batch": "数据质量巡检批次", "organization": "组织机构", "user": "用户", "security_finding": "安全发现", "security_work_order": "安全工单"}
	if label, ok := labels[value]; ok {
		return label
	}
	return value
}

func ResultLabel(value string) string {
	if value == "failure" {
		return "失败"
	}
	return "成功"
}
