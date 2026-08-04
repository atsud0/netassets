package inventorytask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/dataquality"
	"netassets/internal/exportfile"
	"netassets/internal/pagination"
)

var (
	ErrForbidden      = errors.New("forbidden")
	ErrNotFound       = errors.New("not found")
	ErrConflict       = errors.New("conflict")
	ErrInvalid        = errors.New("invalid input")
	ErrExportTooLarge = errors.New("synchronous xlsx export too large")
	ErrState          = errors.New("invalid state")
	ErrQuality        = errors.New("quality check failed")
	ErrResourceInUse  = errors.New("resource in use")
)

type OrganizationProgress struct {
	OrganizationID       string     `json:"organization_id"`
	OrganizationCode     string     `json:"organization_code"`
	OrganizationName     string     `json:"organization_name"`
	Status               string     `json:"status"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	SubmittedAt          *time.Time `json:"submitted_at,omitempty"`
	ReviewedAt           *time.Time `json:"reviewed_at,omitempty"`
	SubmissionNote       string     `json:"submission_note,omitempty"`
	SubmittedSystemCount int64      `json:"submitted_system_count"`
	SubmittedAssetCount  int64      `json:"submitted_asset_count"`
	SubmittedByName      string     `json:"submitted_by_name,omitempty"`
	ReviewedByName       string     `json:"reviewed_by_name,omitempty"`
	ReturnReason         string     `json:"return_reason,omitempty"`
	SnapshotAvailable    bool       `json:"snapshot_available"`
	SubmissionVersion    int        `json:"submission_version"`
}

type Task struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description,omitempty"`
	StartAt             time.Time              `json:"start_at"`
	DeadlineAt          time.Time              `json:"deadline_at"`
	Status              string                 `json:"status"`
	AssetNetworkScope   string                 `json:"asset_network_scope"`
	RequiredFields      []string               `json:"required_fields"`
	QualityRules        []string               `json:"quality_rules"`
	RequireReview       bool                   `json:"require_review"`
	AllowLateSubmission bool                   `json:"allow_late_submission"`
	TargetCount         int64                  `json:"target_count"`
	SubmittedCount      int64                  `json:"submitted_count"`
	ApprovedCount       int64                  `json:"approved_count"`
	CreatedByName       string                 `json:"created_by_name,omitempty"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
	Organizations       []OrganizationProgress `json:"organizations,omitempty"`
}

type CreateInput struct {
	Name                string    `json:"name"`
	Description         string    `json:"description"`
	StartAt             time.Time `json:"start_at"`
	DeadlineAt          time.Time `json:"deadline_at"`
	Status              string    `json:"status"`
	AssetNetworkScope   string    `json:"asset_network_scope"`
	RequiredFields      []string  `json:"required_fields"`
	QualityRules        []string  `json:"quality_rules"`
	RequireReview       bool      `json:"require_review"`
	AllowLateSubmission bool      `json:"allow_late_submission"`
	OrganizationIDs     []string  `json:"organization_ids"`
}

type QualityIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Count    int64  `json:"count"`
}

type QualityCheck struct {
	Passed       bool           `json:"passed"`
	SystemCount  int64          `json:"system_count"`
	AssetCount   int64          `json:"asset_count"`
	ErrorCount   int            `json:"error_count"`
	WarningCount int            `json:"warning_count"`
	Issues       []QualityIssue `json:"issues"`
}

type QualityCheckError struct {
	Result QualityCheck
}

func (e *QualityCheckError) Error() string { return ErrQuality.Error() }
func (e *QualityCheckError) Unwrap() error { return ErrQuality }

type SubmissionInput struct {
	Note string `json:"note"`
}

type ReviewInput struct {
	Decision     string               `json:"decision"`
	ReturnReason string               `json:"return_reason"`
	Comments     []ReviewCommentInput `json:"comments"`
}

type StatusInput struct {
	Status string `json:"status"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT t.id::text, t.task_name, COALESCE(t.description, ''), t.start_at, t.deadline_at, t.status, t.asset_network_scope,
		       t.required_fields, t.quality_rules, t.require_review, t.allow_late_submission,
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id),
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status IN ('submitted', 'approved')),
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status = 'approved'),
		       COALESCE(u.display_name, ''), t.created_at, t.updated_at
		FROM inventory_tasks t
		LEFT JOIN users u ON u.id = t.created_by
		WHERE $2::boolean OR (
			t.status <> 'draft' AND EXISTS (
				SELECT 1 FROM inventory_task_organizations ito
				WHERE ito.task_id = t.id AND ito.organization_id IN (SELECT id FROM accessible)
			)
		)
		ORDER BY t.created_at DESC
	`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list inventory tasks: %w", err)
	}
	defer rows.Close()

	items := make([]Task, 0)
	for rows.Next() {
		var item Task
		var requiredFieldsJSON, qualityRulesJSON []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.StartAt, &item.DeadlineAt, &item.Status, &item.AssetNetworkScope,
			&requiredFieldsJSON, &qualityRulesJSON, &item.RequireReview, &item.AllowLateSubmission,
			&item.TargetCount, &item.SubmittedCount, &item.ApprovedCount, &item.CreatedByName,
			&item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan inventory task: %w", err)
		}
		if err := json.Unmarshal(requiredFieldsJSON, &item.RequiredFields); err != nil {
			return nil, fmt.Errorf("decode required fields: %w", err)
		}
		if err := json.Unmarshal(qualityRulesJSON, &item.QualityRules); err != nil {
			return nil, fmt.Errorf("decode quality rules: %w", err)
		}
		organizations, err := s.listOrganizations(ctx, principal, item.ID)
		if err != nil {
			return nil, err
		}
		item.Organizations = organizations
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory tasks: %w", err)
	}
	return items, nil
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, taskID string, query pagination.Query) (pagination.Result[Task], error) {
	taskID = strings.TrimSpace(taskID)
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
			WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT t.id::text, t.task_name, COALESCE(t.description, ''), t.start_at, t.deadline_at, t.status, t.asset_network_scope,
		       t.required_fields, t.quality_rules, t.require_review, t.allow_late_submission,
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id),
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status IN ('submitted', 'approved')),
		       (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status = 'approved'),
		       COALESCE(u.display_name, ''), t.created_at, t.updated_at, count(*) OVER ()
		FROM inventory_tasks t LEFT JOIN users u ON u.id = t.created_by
		WHERE ($2::boolean OR (t.status <> 'draft' AND EXISTS (SELECT 1 FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.organization_id IN (SELECT id FROM accessible))))
		  AND ($5 = '' OR t.id::text = $5)
		ORDER BY t.created_at DESC LIMIT $3 OFFSET $4
	`, principal.UserID, principal.HasRole("group_admin"), query.PageSize, query.Offset(), taskID)
	if err != nil {
		return pagination.Result[Task]{}, fmt.Errorf("search inventory tasks: %w", err)
	}
	defer rows.Close()
	items := make([]Task, 0, query.PageSize)
	total := 0
	for rows.Next() {
		var item Task
		var requiredFieldsJSON, qualityRulesJSON []byte
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.StartAt, &item.DeadlineAt, &item.Status, &item.AssetNetworkScope, &requiredFieldsJSON, &qualityRulesJSON, &item.RequireReview, &item.AllowLateSubmission, &item.TargetCount, &item.SubmittedCount, &item.ApprovedCount, &item.CreatedByName, &item.CreatedAt, &item.UpdatedAt, &total); err != nil {
			return pagination.Result[Task]{}, fmt.Errorf("scan inventory task page: %w", err)
		}
		if err := json.Unmarshal(requiredFieldsJSON, &item.RequiredFields); err != nil {
			return pagination.Result[Task]{}, fmt.Errorf("decode required fields: %w", err)
		}
		if err := json.Unmarshal(qualityRulesJSON, &item.QualityRules); err != nil {
			return pagination.Result[Task]{}, fmt.Errorf("decode quality rules: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Task]{}, err
	}
	if pagination.NeedsTotalFallback(len(items), query) {
		if err := s.pool.QueryRow(ctx, `
            WITH RECURSIVE descendant_roots(id) AS (
                SELECT ur.organization_id FROM user_roles ur
                JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
            ), descendants(id) AS (
                SELECT id FROM descendant_roots
                UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
                WHERE child.status='enabled' AND child.deleted_at IS NULL
            ), accessible(id) AS (
                SELECT ur.organization_id FROM user_roles ur
                JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
                WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
                  AND ur.data_scope IN ('organization', 'organization_and_descendants')
                UNION SELECT id FROM descendants
            )
            SELECT count(*) FROM inventory_tasks t
            WHERE ($2::boolean OR (t.status <> 'draft' AND EXISTS (SELECT 1 FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.organization_id IN (SELECT id FROM accessible))))
              AND ($3 = '' OR t.id::text = $3)
        `, principal.UserID, principal.HasRole("group_admin"), taskID).Scan(&total); err != nil {
			return pagination.Result[Task]{}, fmt.Errorf("count inventory tasks: %w", err)
		}
	}
	return pagination.NewResult(items, total, query), nil
}

func (s *Service) loadTaskForExport(ctx context.Context, principal auth.Principal, taskID string) (*Task, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, ErrInvalid
	}
	var item Task
	var requiredFieldsJSON, qualityRulesJSON []byte
	err := s.pool.QueryRow(ctx, `
        WITH RECURSIVE descendant_roots(id) AS (
            SELECT ur.organization_id FROM user_roles ur
            JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
        ), descendants(id) AS (
            SELECT id FROM descendant_roots
            UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
        WHERE child.status='enabled' AND child.deleted_at IS NULL
        ), accessible(id) AS (
            SELECT ur.organization_id FROM user_roles ur
            JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
            WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
              AND ur.data_scope IN ('organization', 'organization_and_descendants')
            UNION SELECT id FROM descendants
        )
	        SELECT t.id::text, t.task_name, COALESCE(t.description, ''), t.start_at, t.deadline_at, t.status, t.asset_network_scope,
               t.required_fields, t.quality_rules, t.require_review, t.allow_late_submission,
               (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id),
               (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status IN ('submitted', 'approved')),
               (SELECT count(*) FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.status = 'approved'),
               COALESCE(u.display_name, ''), t.created_at, t.updated_at
        FROM inventory_tasks t LEFT JOIN users u ON u.id = t.created_by
        WHERE t.id::text = $3
          AND ($2::boolean OR (t.status <> 'draft' AND EXISTS (
              SELECT 1 FROM inventory_task_organizations ito
              WHERE ito.task_id = t.id AND ito.organization_id IN (SELECT id FROM accessible)
          )))
    `, principal.UserID, principal.HasRole("group_admin"), taskID).Scan(
		&item.ID, &item.Name, &item.Description, &item.StartAt, &item.DeadlineAt, &item.Status, &item.AssetNetworkScope,
		&requiredFieldsJSON, &qualityRulesJSON, &item.RequireReview, &item.AllowLateSubmission,
		&item.TargetCount, &item.SubmittedCount, &item.ApprovedCount, &item.CreatedByName,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load inventory task export: %w", err)
	}
	if err := json.Unmarshal(requiredFieldsJSON, &item.RequiredFields); err != nil {
		return nil, fmt.Errorf("decode required fields: %w", err)
	}
	if err := json.Unmarshal(qualityRulesJSON, &item.QualityRules); err != nil {
		return nil, fmt.Errorf("decode quality rules: %w", err)
	}
	item.Organizations, err = s.listOrganizations(ctx, principal, item.ID)
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Service) Export(ctx context.Context, principal auth.Principal, taskID, format, requestID, ipAddress string) ([]byte, string, string, int, error) {
	selected, err := s.loadTaskForExport(ctx, principal, taskID)
	if err != nil {
		return nil, "", "", 0, err
	}
	if err := exportfile.ValidateSynchronousExport(format, len(selected.Organizations)); err != nil {
		if errors.Is(err, exportfile.ErrSynchronousXLSXTooLarge) {
			return nil, "", "", 0, ErrExportTooLarge
		}
		return nil, "", "", 0, ErrInvalid
	}
	data, contentType, fileName, err := buildExport(*selected, format)
	if err != nil {
		return nil, "", "", 0, err
	}
	organizationID := principalOrganizationID(principal)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'inventory_task.export', 'inventory_task', $3, '生成填报任务导出文件',
		        jsonb_build_object('format', $4::text, 'count', $5::int, 'task_name', $6::text, 'result', 'generated', 'delivery_state', 'unconfirmed'),
		        NULLIF($7, '')::inet, NULLIF($8, ''))
	`, principal.UserID, organizationID, selected.ID, strings.ToLower(strings.TrimPrefix(format, ".")), len(selected.Organizations), selected.Name, ipAddress, requestID); err != nil {
		return nil, "", "", 0, fmt.Errorf("write inventory task export audit: %w", err)
	}
	return data, contentType, fileName, len(selected.Organizations), nil
}

// StreamCSVExport writes task progress rows directly to the response. Task
// progress is already loaded as one authorization-scoped aggregate, but CSV
// serialization no longer creates an additional full byte buffer.
func (s *Service) StreamCSVExport(ctx context.Context, principal auth.Principal, taskID string, output io.Writer, ready exportfile.ReadyFunc, requestID, ipAddress string) (int, error) {
	selected, err := s.loadTaskForExport(ctx, principal, taskID)
	if err != nil {
		return 0, err
	}
	fileName := "填报任务结果-" + sanitizeExportName(selected.Name) + ".csv"
	if ready != nil {
		ready(exportfile.Metadata{ContentType: exportfile.CSVContentType, FileName: fileName, Count: len(selected.Organizations)})
	}
	stream, err := exportfile.NewCSVStream(output, inventoryTaskExportHeaders())
	if err != nil {
		return 0, fmt.Errorf("start inventory task csv export: %w", err)
	}
	written := 0
	for _, progress := range selected.Organizations {
		if err := stream.Write(inventoryTaskExportRow(*selected, progress)); err != nil {
			return written, fmt.Errorf("write inventory task csv row: %w", err)
		}
		written++
	}
	if err := stream.Flush(); err != nil {
		return written, fmt.Errorf("flush inventory task csv export: %w", err)
	}

	organizationID := principalOrganizationID(principal)
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, 'inventory_task.export', 'inventory_task', $3, '生成填报任务导出文件',
		        jsonb_build_object('format', 'csv', 'count', $4::int, 'task_name', $5::text, 'result', 'generated', 'delivery_state', 'response_written_unconfirmed'),
		        NULLIF($6, '')::inet, NULLIF($7, ''))
	`, principal.UserID, organizationID, selected.ID, written, selected.Name, ipAddress, requestID); err != nil {
		return written, fmt.Errorf("write inventory task export audit: %w", err)
	}
	return written, nil
}

func (s *Service) ExportRowCount(ctx context.Context, principal auth.Principal, taskID string) (int, error) {
	selected, err := s.loadTaskForExport(ctx, principal, taskID)
	if err != nil {
		return 0, err
	}
	return len(selected.Organizations), nil
}

func (s *Service) WriteXLSXExport(ctx context.Context, principal auth.Principal, taskID, outputPath string, progress exportfile.ProgressFunc) (exportfile.Metadata, error) {
	selected, err := s.loadTaskForExport(ctx, principal, taskID)
	if err != nil {
		return exportfile.Metadata{}, err
	}
	stream, err := exportfile.NewXLSXStream(outputPath, "填报结果", inventoryTaskExportHeaders())
	if err != nil {
		return exportfile.Metadata{}, fmt.Errorf("start inventory task xlsx export: %w", err)
	}
	defer stream.Abort()
	total := len(selected.Organizations)
	if progress != nil {
		if err := progress(total, 0); err != nil {
			return exportfile.Metadata{}, err
		}
	}
	for index, item := range selected.Organizations {
		if err := stream.Write(inventoryTaskExportRow(*selected, item)); err != nil {
			return exportfile.Metadata{}, fmt.Errorf("write inventory task xlsx row: %w", err)
		}
		if progress != nil && ((index+1)%100 == 0 || index+1 == total) {
			if err := progress(total, index+1); err != nil {
				return exportfile.Metadata{}, err
			}
		}
	}
	if err := stream.Close(); err != nil {
		return exportfile.Metadata{}, fmt.Errorf("finish inventory task xlsx export: %w", err)
	}
	return exportfile.Metadata{
		ContentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		FileName:    "填报任务结果-" + sanitizeExportName(selected.Name) + ".xlsx",
		Count:       total,
	}, nil
}

func buildExport(task Task, format string) ([]byte, string, string, error) {
	rows := make([][]string, 0, len(task.Organizations))
	for _, progress := range task.Organizations {
		rows = append(rows, inventoryTaskExportRow(task, progress))
	}
	data, contentType, fileName, err := exportfile.Build(format, "填报任务结果-"+sanitizeExportName(task.Name), inventoryTaskExportHeaders(), rows)
	if errors.Is(err, exportfile.ErrInvalidFormat) {
		return nil, "", "", ErrInvalid
	}
	if errors.Is(err, exportfile.ErrSynchronousXLSXTooLarge) {
		return nil, "", "", ErrExportTooLarge
	}
	return data, contentType, fileName, err
}

func inventoryTaskExportHeaders() []string {
	return []string{
		"任务名称", "任务状态", "任务说明", "网络资产核查范围", "开始时间", "截止时间", "需要集团审核", "允许逾期提交",
		"目标组织数", "已提交组织数", "已通过组织数", "组织编码", "组织名称", "填报状态", "开始填报时间",
		"提交时间", "提交人", "业务系统数量", "网络资产数量", "提交说明", "审核时间", "审核人", "退回原因",
		"任务创建人", "任务创建时间",
	}
}

func inventoryTaskExportRow(task Task, progress OrganizationProgress) []string {
	return []string{
		task.Name, inventoryTaskExportLabel("task_status", task.Status), task.Description, inventoryTaskExportLabel("asset_network_scope", task.AssetNetworkScope), formatExportTime(task.StartAt), formatExportTime(task.DeadlineAt),
		inventoryTaskExportLabel("bool", strconv.FormatBool(task.RequireReview)), inventoryTaskExportLabel("bool", strconv.FormatBool(task.AllowLateSubmission)),
		strconv.FormatInt(task.TargetCount, 10), strconv.FormatInt(task.SubmittedCount, 10), strconv.FormatInt(task.ApprovedCount, 10),
		progress.OrganizationCode, progress.OrganizationName, inventoryTaskExportLabel("progress_status", progress.Status), formatExportTimePointer(progress.StartedAt),
		formatExportTimePointer(progress.SubmittedAt), progress.SubmittedByName, strconv.FormatInt(progress.SubmittedSystemCount, 10), strconv.FormatInt(progress.SubmittedAssetCount, 10),
		progress.SubmissionNote, formatExportTimePointer(progress.ReviewedAt), progress.ReviewedByName, progress.ReturnReason,
		task.CreatedByName, formatExportTime(task.CreatedAt),
	}
}

func inventoryTaskExportLabel(kind, value string) string {
	labels := map[string]map[string]string{
		"task_status":         {"draft": "草稿", "active": "进行中", "closed": "已关闭", "completed": "已完成", "archived": "已归档"},
		"progress_status":     {"not_started": "未开始", "in_progress": "填报中", "submitted": "待审核", "returned": "已退回", "approved": "已通过", "overdue": "已逾期"},
		"asset_network_scope": {"all": "全部网络资产", "public": "仅公网资产", "private": "仅内网资产"},
		"bool":                {"true": "是", "false": "否"},
	}
	if label, ok := labels[kind][value]; ok {
		return label
	}
	return value
}

func formatExportTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format("2006-01-02 15:04:05Z07:00")
}

func formatExportTimePointer(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatExportTime(*value)
}

func sanitizeExportName(value string) string {
	value = strings.TrimSpace(value)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", "*", "-", "?", "-", "\"", "-", "<", "-", ">", "-", "|", "-")
	value = strings.Trim(replacer.Replace(value), ". ")
	if value == "" {
		return "未命名任务"
	}
	return value
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (Task, error) {
	if !principal.HasRole("group_admin") {
		return Task{}, ErrForbidden
	}
	if err := normalizeCreateInput(&input); err != nil {
		return Task{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, fmt.Errorf("begin inventory task: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	organizationRows, err := tx.Query(ctx, `
		SELECT id::text, organization_code, organization_name
		FROM organizations
		WHERE id::text = ANY($1) AND status = 'enabled' AND deleted_at IS NULL AND organization_type <> 'group'
		ORDER BY organization_code
	`, input.OrganizationIDs)
	if err != nil {
		return Task{}, fmt.Errorf("validate inventory task organizations: %w", err)
	}
	organizations := make([]OrganizationProgress, 0, len(input.OrganizationIDs))
	for organizationRows.Next() {
		var organization OrganizationProgress
		if err := organizationRows.Scan(&organization.OrganizationID, &organization.OrganizationCode, &organization.OrganizationName); err != nil {
			organizationRows.Close()
			return Task{}, fmt.Errorf("scan inventory task organization: %w", err)
		}
		organization.Status = "not_started"
		organizations = append(organizations, organization)
	}
	if err := organizationRows.Err(); err != nil {
		organizationRows.Close()
		return Task{}, fmt.Errorf("iterate inventory task organizations: %w", err)
	}
	organizationRows.Close()
	if len(organizations) != len(input.OrganizationIDs) {
		return Task{}, ErrNotFound
	}

	requiredFieldsJSON, _ := json.Marshal(input.RequiredFields)
	qualityRulesJSON, _ := json.Marshal(input.QualityRules)
	var item Task
	err = tx.QueryRow(ctx, `
		INSERT INTO inventory_tasks (
			task_name, description, start_at, deadline_at, status, asset_network_scope, required_fields, quality_rules,
			require_review, allow_late_submission, created_by
		) VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6, $7::jsonb, $8::jsonb, $9, $10, $11::uuid)
		RETURNING id::text, task_name, COALESCE(description, ''), start_at, deadline_at, status, asset_network_scope,
		          required_fields, quality_rules, require_review, allow_late_submission, created_at, updated_at
	`, input.Name, input.Description, input.StartAt, input.DeadlineAt, input.Status, input.AssetNetworkScope, requiredFieldsJSON, qualityRulesJSON,
		input.RequireReview, input.AllowLateSubmission, principal.UserID).Scan(
		&item.ID, &item.Name, &item.Description, &item.StartAt, &item.DeadlineAt, &item.Status, &item.AssetNetworkScope,
		&requiredFieldsJSON, &qualityRulesJSON, &item.RequireReview, &item.AllowLateSubmission,
		&item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("insert inventory task: %w", err)
	}
	for _, organization := range organizations {
		if _, err := tx.Exec(ctx, `
			INSERT INTO inventory_task_organizations (task_id, organization_id)
			VALUES ($1::uuid, $2::uuid)
		`, item.ID, organization.OrganizationID); err != nil {
			return Task{}, fmt.Errorf("attach inventory task organization: %w", err)
		}
	}
	if err := writeAudit(ctx, tx, principal, principalOrganizationID(principal), "inventory_task.create", item.ID,
		"创建填报任务", input.Name, input.Status, "", len(organizations), input.AssetNetworkScope, requestID, ipAddress); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, fmt.Errorf("commit inventory task: %w", err)
	}
	item.RequiredFields = input.RequiredFields
	item.QualityRules = input.QualityRules
	item.TargetCount = int64(len(organizations))
	item.CreatedByName = principal.DisplayName
	item.Organizations = organizations
	return item, nil
}

func (s *Service) Start(ctx context.Context, principal auth.Principal, taskID, organizationID, requestID, ipAddress string) (OrganizationProgress, error) {
	if principal.HasRole("group_admin") {
		return OrganizationProgress{}, ErrForbidden
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("begin inventory task start: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	result, err := tx.Exec(ctx, contributorUpdatePrefix+`
		UPDATE inventory_task_organizations ito
		SET status = 'in_progress', started_at = COALESCE(ito.started_at, now()), updated_at = now()
		FROM inventory_tasks t
		WHERE t.id = ito.task_id AND t.id::text = $2 AND ito.organization_id::text = $3
		  AND ito.organization_id IN (SELECT id FROM accessible)
		  AND t.status = 'active' AND t.start_at <= now()
		  AND (t.deadline_at >= now() OR t.allow_late_submission)
		  AND ito.status IN ('not_started', 'returned', 'overdue')
	`, principal.UserID, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("start inventory task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return OrganizationProgress{}, ErrState
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "inventory_task.start", taskID,
		"开始填报任务", "", "in_progress", "", 0, "", requestID, ipAddress); err != nil {
		return OrganizationProgress{}, err
	}
	item, err := loadOrganizationProgress(ctx, tx, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProgress{}, fmt.Errorf("commit inventory task start: %w", err)
	}
	return item, nil
}

func (s *Service) QualityCheck(ctx context.Context, principal auth.Principal, taskID, organizationID string) (QualityCheck, error) {
	return loadQualityCheck(ctx, s.pool, principal, taskID, organizationID)
}

type qualityCheckQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func loadQualityCheck(ctx context.Context, q qualityCheckQuerier, principal auth.Principal, taskID, organizationID string) (QualityCheck, error) {
	var requiredFieldsJSON []byte
	var assetNetworkScope string
	var counts qualityCounts
	err := q.QueryRow(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		), assignment AS (
			SELECT t.required_fields, t.asset_network_scope
			FROM inventory_tasks t
			JOIN inventory_task_organizations ito ON ito.task_id = t.id
			WHERE t.id::text = $2 AND ito.organization_id::text = $3
			  AND ($4::boolean OR ito.organization_id IN (SELECT id FROM accessible))
		), org_systems AS (
			SELECT bs.id, bs.owner_name, bs.owner_contact, bs.system_level,
			       p.contains_personal_information, p.contains_sensitive_personal_information,
			       p.data_volume_over_100k, p.contains_important_data, p.contains_core_data
			FROM business_systems bs
			CROSS JOIN assignment task_scope
			LEFT JOIN business_system_data_security_profiles p ON p.business_system_id = bs.id
			WHERE bs.owner_organization_id::text = $3 AND bs.deleted_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM inventory_business_system_drafts change_draft WHERE change_draft.task_id::text=$2 AND change_draft.organization_id::text=$3 AND change_draft.source_system_id=bs.id AND change_draft.draft_status IN ('editing','returned'))
			  AND (task_scope.required_fields ? 'business_systems' OR task_scope.required_fields ? 'data_security')
			UNION ALL
			SELECT draft.id, draft.owner_name, draft.owner_contact, draft.system_level,
			       draft.contains_personal_information, draft.contains_sensitive_personal_information,
			       draft.data_volume_over_100k, draft.contains_important_data, draft.contains_core_data
			FROM inventory_business_system_drafts draft
			CROSS JOIN assignment task_scope
			WHERE draft.task_id::text = $2 AND draft.organization_id::text = $3
			  AND draft.draft_status IN ('editing', 'returned')
			  AND draft.operation <> 'delete'
			  AND (task_scope.required_fields ? 'business_systems' OR task_scope.required_fields ? 'data_security')
		), org_assets AS (
			SELECT a.id, a.network_scope, a.importance, ao.owner_name,
			       EXISTS (SELECT 1 FROM system_assets sa WHERE sa.asset_id = a.id) AS has_system
			FROM assets a
			JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
			CROSS JOIN assignment task_scope
			WHERE ao.organization_id::text = $3 AND a.deleted_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM inventory_asset_drafts change_draft WHERE change_draft.task_id::text=$2 AND change_draft.organization_id::text=$3 AND change_draft.source_asset_id=a.id AND change_draft.draft_status IN ('editing','returned'))
			  AND task_scope.required_fields ? 'network_assets'
			  AND (task_scope.asset_network_scope = 'all' OR a.network_scope = task_scope.asset_network_scope)
			UNION ALL
			SELECT draft.id, draft.network_scope, draft.importance, draft.owner_name,
			       draft.business_system_id IS NOT NULL AS has_system
			FROM inventory_asset_drafts draft
			CROSS JOIN assignment task_scope
			WHERE draft.task_id::text = $2 AND draft.organization_id::text = $3
			  AND draft.draft_status IN ('editing', 'returned')
			  AND draft.operation <> 'delete'
			  AND task_scope.required_fields ? 'network_assets'
			  AND (task_scope.asset_network_scope = 'all' OR draft.network_scope = task_scope.asset_network_scope)
		)
		SELECT a.required_fields, a.asset_network_scope,
		       (SELECT count(*) FROM org_systems),
		       (SELECT count(*) FROM org_assets),
		       (SELECT count(*) FROM org_systems WHERE btrim(COALESCE(owner_name, '')) = '' OR btrim(COALESCE(owner_contact, '')) = ''),
		       (SELECT count(*) FROM org_assets WHERE btrim(COALESCE(owner_name, '')) = ''),
		       (SELECT count(*) FROM org_systems WHERE contains_personal_information IS NULL
		          OR contains_personal_information = 'unknown'
		          OR contains_sensitive_personal_information = 'unknown'
		          OR data_volume_over_100k = 'unknown'
		          OR contains_important_data = 'unknown'
		          OR contains_core_data = 'unknown'),
		       (SELECT count(*) FROM org_assets oa WHERE oa.network_scope = 'public' AND NOT oa.has_system),
		       (SELECT count(*) FROM org_systems WHERE system_level IN ('important', 'core')
		          AND (btrim(COALESCE(owner_name, '')) = '' OR btrim(COALESCE(owner_contact, '')) = '')),
		       (SELECT count(*) FROM org_assets WHERE importance IN ('important', 'core') AND btrim(COALESCE(owner_name, '')) = '')
		FROM assignment a
	`, principal.UserID, taskID, organizationID, principal.HasRole("group_admin")).Scan(
		&requiredFieldsJSON, &assetNetworkScope, &counts.SystemCount, &counts.AssetCount, &counts.MissingSystemOwner,
		&counts.MissingAssetOwner, &counts.UnknownDataSecurity, &counts.UnlinkedPublicAssets,
		&counts.MissingPrioritySystemOwner, &counts.MissingPriorityAssetOwner,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return QualityCheck{}, ErrNotFound
	}
	if err != nil {
		return QualityCheck{}, fmt.Errorf("load inventory quality check: %w", err)
	}
	var requiredFields []string
	if err := json.Unmarshal(requiredFieldsJSON, &requiredFields); err != nil {
		return QualityCheck{}, fmt.Errorf("decode inventory quality fields: %w", err)
	}
	result := buildQualityCheck(requiredFields, counts)
	if err := appendConfiguredQualityRules(ctx, q, taskID, organizationID, requiredFields, assetNetworkScope, &result); err != nil {
		return QualityCheck{}, err
	}
	result.Passed = result.ErrorCount == 0
	return result, nil
}

type qualityCounts struct {
	SystemCount                int64
	AssetCount                 int64
	MissingSystemOwner         int64
	MissingAssetOwner          int64
	UnknownDataSecurity        int64
	UnlinkedPublicAssets       int64
	MissingPrioritySystemOwner int64
	MissingPriorityAssetOwner  int64
}

func buildQualityCheck(requiredFields []string, counts qualityCounts) QualityCheck {
	result := QualityCheck{SystemCount: counts.SystemCount, AssetCount: counts.AssetCount, Issues: make([]QualityIssue, 0)}
	required := make(map[string]bool, len(requiredFields))
	for _, field := range requiredFields {
		required[field] = true
	}
	add := func(code, severity, message string, count int64) {
		if count <= 0 {
			return
		}
		result.Issues = append(result.Issues, QualityIssue{Code: code, Severity: severity, Message: message, Count: count})
		if severity == "error" {
			result.ErrorCount++
		} else {
			result.WarningCount++
		}
	}
	needsSystems, needsAssets := selectedResourceTypes(requiredFields)
	if needsSystems && counts.SystemCount == 0 {
		add("missing_business_system", "error", "尚未录入业务系统", 1)
	}
	if needsAssets && counts.AssetCount == 0 {
		add("missing_network_asset", "error", "尚未录入网络资产", 1)
	}
	if required["owner_contacts"] {
		if needsSystems {
			add("missing_system_owner", "error", "业务系统负责人或联系方式未填写完整", counts.MissingSystemOwner)
		}
		if needsAssets {
			add("missing_asset_owner", "error", "网络资产负责人未填写", counts.MissingAssetOwner)
		}
	} else {
		if needsSystems {
			add("missing_priority_system_owner", "warning", "核心或重要业务系统负责人信息未填写完整", counts.MissingPrioritySystemOwner)
		}
		if needsAssets {
			add("missing_priority_asset_owner", "warning", "核心或重要网络资产负责人未填写", counts.MissingPriorityAssetOwner)
		}
	}
	if required["data_security"] {
		add("unknown_data_security", "error", "业务系统的数据安全属性仍有未确认项", counts.UnknownDataSecurity)
	}
	if required["network_assets"] {
		add("unlinked_public_asset", "warning", "公网资产尚未关联业务系统", counts.UnlinkedPublicAssets)
	}
	result.Passed = result.ErrorCount == 0
	return result
}

func appendConfiguredQualityRules(ctx context.Context, q qualityCheckQuerier, taskID, organizationID string, requiredFields []string, assetNetworkScope string, result *QualityCheck) error {
	includeSystems, includeAssets := selectedResourceTypes(requiredFields)
	rows, err := q.Query(ctx, `SELECT id::text,rule_name,resource_type,target_field,severity,conditions FROM data_quality_rules WHERE is_enabled AND rule_type='required' ORDER BY sort_order,created_at`)
	if err != nil {
		return fmt.Errorf("load configured quality rules: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var rule dataquality.Rule
		var rawConditions []byte
		if err := rows.Scan(&rule.ID, &rule.RuleName, &rule.ResourceType, &rule.TargetField, &rule.Severity, &rawConditions); err != nil {
			return err
		}
		rule.RuleType = "required"
		if (rule.ResourceType == "asset" && !includeAssets) || (rule.ResourceType == "business_system" && !includeSystems) {
			continue
		}
		if err := json.Unmarshal(rawConditions, &rule.Conditions); err != nil {
			return fmt.Errorf("decode configured quality rule conditions: %w", err)
		}
		missing, err := configuredMissingCount(ctx, q, rule, taskID, organizationID, assetNetworkScope)
		if err != nil {
			return err
		}
		if missing == 0 {
			continue
		}
		code := "configured_required_" + rule.ResourceType + "_" + strings.ReplaceAll(strings.TrimPrefix(rule.TargetField, "custom:"), ":", "_")
		result.Issues = append(result.Issues, QualityIssue{Code: code, Severity: rule.Severity, Message: "集团规则“" + rule.RuleName + "”要求填写，当前存在缺失项", Count: missing})
		if rule.Severity == "error" {
			result.ErrorCount++
		} else {
			result.WarningCount++
		}
	}
	return rows.Err()
}

func configuredMissingCount(ctx context.Context, q qualityCheckQuerier, rule dataquality.Rule, taskID, organizationID, assetNetworkScope string) (int64, error) {
	query, args, err := dataquality.BuildRuleIssueCountQuery(rule, organizationID)
	if err != nil {
		return 0, fmt.Errorf("build configured quality rule %s: %w", rule.ID, err)
	}
	if rule.ResourceType == "asset" && assetNetworkScope != "all" {
		args = append(args, assetNetworkScope)
		query += fmt.Sprintf(" AND a.network_scope=$%d", len(args))
	}
	var count int64
	if err := q.QueryRow(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count configured quality rule %s: %w", rule.ID, err)
	}
	if rule.ResourceType == "business_system" {
		draftCount, err := configuredBusinessSystemDraftMissingCount(ctx, q, rule, taskID, organizationID)
		if err != nil {
			return 0, err
		}
		count += draftCount
	}
	return count, nil
}

func (s *Service) Submit(ctx context.Context, principal auth.Principal, taskID, organizationID string, input SubmissionInput, requestID, ipAddress string) (OrganizationProgress, error) {
	if principal.HasRole("group_admin") {
		return OrganizationProgress{}, ErrForbidden
	}
	input.Note = strings.TrimSpace(input.Note)
	if len(input.Note) > 5000 {
		return OrganizationProgress{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("begin inventory task submission: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	quality, err := loadQualityCheck(ctx, tx, principal, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if !quality.Passed {
		return OrganizationProgress{}, &QualityCheckError{Result: quality}
	}

	result, err := tx.Exec(ctx, contributorUpdatePrefix+`
		UPDATE inventory_task_organizations ito
		SET status = CASE WHEN t.require_review THEN 'submitted' ELSE 'approved' END,
		    started_at = COALESCE(ito.started_at, now()), submitted_by = $1::uuid, submitted_at = now(),
			    submission_note = NULLIF($4, ''),
			    submitted_system_count = (
					SELECT (
						SELECT count(*) FROM business_systems bs
						WHERE bs.owner_organization_id = ito.organization_id AND bs.deleted_at IS NULL
						  AND (t.required_fields ? 'business_systems' OR t.required_fields ? 'data_security')
					) + (
						SELECT count(*) FROM inventory_business_system_drafts draft
						WHERE draft.task_id=t.id AND draft.organization_id=ito.organization_id
						  AND draft.draft_status IN ('editing','returned')
						  AND (t.required_fields ? 'business_systems' OR t.required_fields ? 'data_security')
					)
				),
			    submitted_asset_count = (
					SELECT (
						SELECT count(*) FROM assets a
						JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
						WHERE ao.organization_id = ito.organization_id AND a.deleted_at IS NULL
						  AND t.required_fields ? 'network_assets'
						  AND (t.asset_network_scope = 'all' OR a.network_scope = t.asset_network_scope)
					) + (
						SELECT count(*) FROM inventory_asset_drafts draft
						WHERE draft.task_id = t.id AND draft.organization_id = ito.organization_id
						  AND draft.draft_status IN ('editing', 'returned')
						  AND t.required_fields ? 'network_assets'
						  AND (t.asset_network_scope = 'all' OR draft.network_scope = t.asset_network_scope)
					)
				),
		    reviewed_by = NULL,
		    reviewed_at = CASE WHEN t.require_review THEN NULL ELSE now() END,
		    return_reason = NULL,
		    updated_at = now()
		FROM inventory_tasks t
		WHERE t.id = ito.task_id AND t.id::text = $2 AND ito.organization_id::text = $3
		  AND ito.organization_id IN (SELECT id FROM accessible)
		  AND t.status = 'active' AND t.start_at <= now()
		  AND (t.deadline_at >= now() OR t.allow_late_submission)
		  AND ito.status IN ('not_started', 'in_progress', 'returned', 'overdue')
	`, principal.UserID, taskID, organizationID, input.Note)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("submit inventory task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return OrganizationProgress{}, ErrState
	}
	snapshot, err := createSubmissionSnapshot(ctx, tx, principal, taskID, organizationID, input.Note)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_business_system_drafts
		SET draft_status='submitted', submitted_at=now(), updated_by=$3::uuid, updated_at=now()
		WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status IN ('editing','returned')
	`, taskID, organizationID, principal.UserID); err != nil {
		return OrganizationProgress{}, fmt.Errorf("submit inventory business system drafts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_asset_drafts
		SET draft_status='submitted', submitted_at=now(), updated_by=$3::uuid, updated_at=now()
		WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status IN ('editing','returned')
	`, taskID, organizationID, principal.UserID); err != nil {
		return OrganizationProgress{}, fmt.Errorf("submit inventory asset drafts: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_task_organizations
		SET latest_submission_id = $3::uuid,
		    submitted_system_count = $4,
		    submitted_asset_count = $5,
		    updated_at = now()
		WHERE task_id::text = $1 AND organization_id::text = $2
	`, taskID, organizationID, snapshot.ID, snapshot.SystemCount, snapshot.AssetCount); err != nil {
		return OrganizationProgress{}, fmt.Errorf("attach inventory submission snapshot: %w", err)
	}
	item, err := loadOrganizationProgress(ctx, tx, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if item.Status == "approved" {
		if err := publishBusinessSystemDrafts(ctx, tx, principal, taskID, organizationID, requestID, ipAddress); err != nil {
			return OrganizationProgress{}, err
		}
		if err := publishAssetDrafts(ctx, tx, principal, taskID, organizationID, requestID, ipAddress); err != nil {
			return OrganizationProgress{}, err
		}
	}
	if err := writeSubmissionAudit(ctx, tx, principal, organizationID, taskID, input.Note, item.Status, snapshot, requestID, ipAddress); err != nil {
		return OrganizationProgress{}, err
	}
	if err := completeIfApproved(ctx, tx, taskID); err != nil {
		return OrganizationProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProgress{}, fmt.Errorf("commit inventory task submission: %w", err)
	}
	return item, nil
}

func (s *Service) Review(ctx context.Context, principal auth.Principal, taskID, organizationID string, input ReviewInput, requestID, ipAddress string) (OrganizationProgress, error) {
	if !principal.HasRole("group_admin") {
		return OrganizationProgress{}, ErrForbidden
	}
	if err := normalizeReviewInput(&input); err != nil {
		return OrganizationProgress{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("begin inventory task review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertReviewComments(ctx, tx, principal, taskID, organizationID, input.Comments); err != nil {
		return OrganizationProgress{}, err
	}
	if input.Decision == "approved" {
		if err := publishBusinessSystemDrafts(ctx, tx, principal, taskID, organizationID, requestID, ipAddress); err != nil {
			return OrganizationProgress{}, err
		}
		if err := publishAssetDrafts(ctx, tx, principal, taskID, organizationID, requestID, ipAddress); err != nil {
			return OrganizationProgress{}, err
		}
	}

	result, err := tx.Exec(ctx, `
		UPDATE inventory_task_organizations ito
		SET status = $3::varchar,
		    reviewed_by = $4::uuid,
		    reviewed_at = now(),
		    return_reason = CASE WHEN $3::varchar = 'returned'::varchar THEN $5 ELSE NULL END,
		    updated_at = now()
		FROM inventory_tasks t
		WHERE t.id = ito.task_id AND t.id::text = $1 AND ito.organization_id::text = $2
		  AND t.status = 'active' AND t.require_review AND ito.status = 'submitted'
	`, taskID, organizationID, input.Decision, principal.UserID, input.ReturnReason)
	if err != nil {
		return OrganizationProgress{}, fmt.Errorf("review inventory task: %w", err)
	}
	if result.RowsAffected() == 0 {
		return OrganizationProgress{}, ErrState
	}
	if input.Decision == "returned" {
		if _, err := tx.Exec(ctx, `UPDATE inventory_business_system_drafts SET draft_status='returned',updated_by=$3::uuid,updated_at=now() WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status='submitted'`, taskID, organizationID, principal.UserID); err != nil {
			return OrganizationProgress{}, fmt.Errorf("return inventory business system drafts: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE inventory_asset_drafts SET draft_status='returned',updated_by=$3::uuid,updated_at=now() WHERE task_id::text=$1 AND organization_id::text=$2 AND draft_status='submitted'`, taskID, organizationID, principal.UserID); err != nil {
			return OrganizationProgress{}, fmt.Errorf("return inventory asset drafts: %w", err)
		}
	}
	summary := "审核通过填报任务"
	if input.Decision == "returned" {
		summary = "退回填报任务"
		if len(input.Comments) > 0 {
			summary = fmt.Sprintf("退回填报任务（逐条意见 %d 条）", len(input.Comments))
		}
	}
	if err := writeAudit(ctx, tx, principal, organizationID, "inventory_task.review", taskID,
		summary, "", input.Decision, input.ReturnReason, 0, "", requestID, ipAddress); err != nil {
		return OrganizationProgress{}, err
	}
	if input.Decision == "approved" {
		if err := completeIfApproved(ctx, tx, taskID); err != nil {
			return OrganizationProgress{}, err
		}
	}
	item, err := loadOrganizationProgress(ctx, tx, taskID, organizationID)
	if err != nil {
		return OrganizationProgress{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationProgress{}, fmt.Errorf("commit inventory task review: %w", err)
	}
	return item, nil
}

func (s *Service) UpdateStatus(ctx context.Context, principal auth.Principal, taskID string, input StatusInput, requestID, ipAddress string) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	input.Status = strings.TrimSpace(input.Status)
	if input.Status != "active" && input.Status != "closed" && input.Status != "archived" {
		return ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin inventory task status update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result, err := tx.Exec(ctx, `
		UPDATE inventory_tasks
		SET status = $2::varchar, updated_at = now()
		WHERE id::text = $1 AND (
			(status = 'draft' AND $2::varchar = 'active'::varchar) OR
			(status = 'active' AND $2::varchar = 'closed'::varchar) OR
			(status IN ('draft', 'closed', 'completed') AND $2::varchar = 'archived'::varchar)
		)
	`, taskID, input.Status)
	if err != nil {
		return fmt.Errorf("update inventory task status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrState
	}
	if err := writeAudit(ctx, tx, principal, principalOrganizationID(principal), "inventory_task.status_update", taskID,
		"更新填报任务状态", "", input.Status, "", 0, "", requestID, ipAddress); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inventory task status update: %w", err)
	}
	return nil
}

// SearchOrganizations returns one bounded page of organization progress for a
// task. Non-group users only receive progress rows within their organization
// data scope; draft tasks remain visible only to group administrators.
func (s *Service) SearchOrganizations(ctx context.Context, principal auth.Principal, taskID, organizationID string, page pagination.Query) (pagination.Result[OrganizationProgress], error) {
	taskID = strings.TrimSpace(taskID)
	organizationID = strings.TrimSpace(organizationID)
	if taskID == "" {
		return pagination.Result[OrganizationProgress]{}, ErrInvalid
	}
	visibilityCTE := `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
	`
	where := `
		WHERE ito.task_id::text = $2
		  AND ($3::boolean OR (t.status <> 'draft' AND o.id IN (SELECT id FROM accessible)))
		  AND ($4 = '' OR ito.organization_id::text = $4)
	`
	var visible bool
	var total int
	if err := s.pool.QueryRow(ctx, visibilityCTE+`
		SELECT
			EXISTS (
				SELECT 1 FROM inventory_tasks visible_task
				WHERE visible_task.id::text = $2
				  AND ($3::boolean OR (
					visible_task.status <> 'draft' AND EXISTS (
						SELECT 1 FROM inventory_task_organizations visible_ito
						WHERE visible_ito.task_id = visible_task.id
						  AND visible_ito.organization_id IN (SELECT id FROM accessible)
					)
				  ))
			),
			(
				SELECT count(*)
				FROM inventory_task_organizations ito
				JOIN inventory_tasks t ON t.id = ito.task_id
				JOIN organizations o ON o.id = ito.organization_id
		`+where+`
			)
	`, principal.UserID, taskID, principal.HasRole("group_admin"), organizationID).Scan(&visible, &total); err != nil {
		return pagination.Result[OrganizationProgress]{}, fmt.Errorf("count inventory task organizations: %w", err)
	}
	if !visible {
		return pagination.Result[OrganizationProgress]{}, ErrNotFound
	}

	rows, err := s.pool.Query(ctx, visibilityCTE+`
		SELECT o.id::text, o.organization_code, o.organization_name, ito.status,
		       ito.started_at, ito.submitted_at, ito.reviewed_at, COALESCE(ito.submission_note, ''),
		       ito.submitted_system_count, ito.submitted_asset_count,
		       COALESCE(submitter.display_name, ''), COALESCE(reviewer.display_name, ''), COALESCE(ito.return_reason, ''),
		       (ito.latest_submission_id IS NOT NULL), COALESCE(submission.submission_version, 0)
		FROM inventory_task_organizations ito
		JOIN inventory_tasks t ON t.id = ito.task_id
		JOIN organizations o ON o.id = ito.organization_id
		LEFT JOIN users submitter ON submitter.id = ito.submitted_by
		LEFT JOIN users reviewer ON reviewer.id = ito.reviewed_by
		LEFT JOIN inventory_submissions submission ON submission.id = ito.latest_submission_id
	`+where+`
		ORDER BY o.organization_code, o.id
		LIMIT $5 OFFSET $6
	`, principal.UserID, taskID, principal.HasRole("group_admin"), organizationID, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[OrganizationProgress]{}, fmt.Errorf("search inventory task organizations: %w", err)
	}
	defer rows.Close()
	items := make([]OrganizationProgress, 0, page.PageSize)
	for rows.Next() {
		var item OrganizationProgress
		if err := scanOrganizationProgress(rows, &item); err != nil {
			return pagination.Result[OrganizationProgress]{}, fmt.Errorf("scan inventory task organization progress page: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[OrganizationProgress]{}, fmt.Errorf("iterate inventory task organization progress page: %w", err)
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) listOrganizations(ctx context.Context, principal auth.Principal, taskID string) ([]OrganizationProgress, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE descendant_roots(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
		), descendants(id) AS (
			SELECT id FROM descendant_roots
			UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
		WHERE child.status='enabled' AND child.deleted_at IS NULL
		), accessible(id) AS (
			SELECT ur.organization_id FROM user_roles ur
			JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
			WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
			  AND ur.data_scope IN ('organization', 'organization_and_descendants')
			UNION SELECT id FROM descendants
		)
		SELECT o.id::text, o.organization_code, o.organization_name, ito.status,
		       ito.started_at, ito.submitted_at, ito.reviewed_at, COALESCE(ito.submission_note, ''),
		       ito.submitted_system_count, ito.submitted_asset_count,
		       COALESCE(submitter.display_name, ''), COALESCE(reviewer.display_name, ''), COALESCE(ito.return_reason, ''),
		       (ito.latest_submission_id IS NOT NULL), COALESCE(submission.submission_version, 0)
		FROM inventory_task_organizations ito
		JOIN organizations o ON o.id = ito.organization_id
		LEFT JOIN users submitter ON submitter.id = ito.submitted_by
		LEFT JOIN users reviewer ON reviewer.id = ito.reviewed_by
		LEFT JOIN inventory_submissions submission ON submission.id = ito.latest_submission_id
		WHERE ito.task_id::text = $2 AND ($3::boolean OR o.id IN (SELECT id FROM accessible))
		ORDER BY o.organization_code
	`, principal.UserID, taskID, principal.HasRole("group_admin"))
	if err != nil {
		return nil, fmt.Errorf("list inventory task organizations: %w", err)
	}
	defer rows.Close()
	items := make([]OrganizationProgress, 0)
	for rows.Next() {
		var item OrganizationProgress
		if err := scanOrganizationProgress(rows, &item); err != nil {
			return nil, fmt.Errorf("scan inventory task organization progress: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate inventory task organization progress: %w", err)
	}
	return items, nil
}

const contributorUpdatePrefix = `
	WITH RECURSIVE descendant_roots(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
	), descendants(id) AS (
		SELECT id FROM descendant_roots
		UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
	WHERE child.status='enabled' AND child.deleted_at IS NULL
	), accessible(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
		  AND ur.data_scope IN ('organization', 'organization_and_descendants')
		UNION SELECT id FROM descendants
	)
`

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func loadOrganizationProgress(ctx context.Context, q queryRower, taskID, organizationID string) (OrganizationProgress, error) {
	var item OrganizationProgress
	row := q.QueryRow(ctx, `
		SELECT o.id::text, o.organization_code, o.organization_name, ito.status,
		       ito.started_at, ito.submitted_at, ito.reviewed_at, COALESCE(ito.submission_note, ''),
		       ito.submitted_system_count, ito.submitted_asset_count,
		       COALESCE(submitter.display_name, ''), COALESCE(reviewer.display_name, ''), COALESCE(ito.return_reason, ''),
		       (ito.latest_submission_id IS NOT NULL), COALESCE(submission.submission_version, 0)
		FROM inventory_task_organizations ito
		JOIN organizations o ON o.id = ito.organization_id
		LEFT JOIN users submitter ON submitter.id = ito.submitted_by
		LEFT JOIN users reviewer ON reviewer.id = ito.reviewed_by
		LEFT JOIN inventory_submissions submission ON submission.id = ito.latest_submission_id
		WHERE ito.task_id::text = $1 AND ito.organization_id::text = $2
	`, taskID, organizationID)
	if err := scanOrganizationProgress(row, &item); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrganizationProgress{}, ErrNotFound
		}
		return OrganizationProgress{}, fmt.Errorf("load inventory task organization progress: %w", err)
	}
	return item, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanOrganizationProgress(row rowScanner, item *OrganizationProgress) error {
	return row.Scan(&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName, &item.Status,
		&item.StartedAt, &item.SubmittedAt, &item.ReviewedAt, &item.SubmissionNote,
		&item.SubmittedSystemCount, &item.SubmittedAssetCount, &item.SubmittedByName,
		&item.ReviewedByName, &item.ReturnReason, &item.SnapshotAvailable, &item.SubmissionVersion)
}

func completeIfApproved(ctx context.Context, tx pgx.Tx, taskID string) error {
	if _, err := tx.Exec(ctx, `
		UPDATE inventory_tasks t
		SET status = 'completed', updated_at = now()
		WHERE t.id::text = $1 AND t.status = 'active'
		  AND NOT EXISTS (
			SELECT 1 FROM inventory_task_organizations ito
			WHERE ito.task_id = t.id AND ito.status <> 'approved'
		  )
	`, taskID); err != nil {
		return fmt.Errorf("complete inventory task: %w", err)
	}
	return nil
}

func writeAudit(ctx context.Context, tx pgx.Tx, principal auth.Principal, organizationID, action, resourceID, summary, note, status, reason string, targetCount int, assetNetworkScope, requestID, ipAddress string) error {
	if _, err := tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, details, request_ip, request_id)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, 'inventory_task', $4, $5,
		        jsonb_build_object('note', $6::text, 'status', $7::text, 'return_reason', $8::text, 'target_count', $9::int,
		                           'asset_network_scope', NULLIF($10::text, '')),
		        NULLIF($11, '')::inet, NULLIF($12, ''))
	`, principal.UserID, organizationID, action, resourceID, summary, note, status, reason, targetCount, assetNetworkScope, ipAddress, requestID); err != nil {
		return fmt.Errorf("write inventory task audit: %w", err)
	}
	return nil
}

func normalizeCreateInput(input *CreateInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.Status = strings.TrimSpace(input.Status)
	input.AssetNetworkScope = strings.TrimSpace(input.AssetNetworkScope)
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.AssetNetworkScope == "" {
		input.AssetNetworkScope = "all"
	}
	if input.Name == "" || len(input.Name) > 255 || len(input.Description) > 5000 || input.StartAt.IsZero() ||
		input.DeadlineAt.IsZero() || !input.DeadlineAt.After(input.StartAt) ||
		(input.Status != "draft" && input.Status != "active") || !validAssetNetworkScope(input.AssetNetworkScope) {
		return ErrInvalid
	}
	input.OrganizationIDs = uniqueNonEmpty(input.OrganizationIDs)
	input.RequiredFields = uniqueNonEmpty(input.RequiredFields)
	input.QualityRules = uniqueNonEmpty(input.QualityRules)
	if len(input.OrganizationIDs) == 0 || len(input.OrganizationIDs) > 1000 || len(input.RequiredFields) > 100 || len(input.QualityRules) > 100 || !validRequiredFields(input.RequiredFields) {
		return ErrInvalid
	}
	return nil
}

func validAssetNetworkScope(value string) bool {
	return value == "all" || value == "public" || value == "private"
}

func validRequiredFields(fields []string) bool {
	allowed := map[string]bool{
		"business_systems": true,
		"data_security":    true,
		"network_assets":   true,
		"owner_contacts":   true,
	}
	for _, field := range fields {
		if !allowed[field] {
			return false
		}
	}
	includeSystems, includeAssets := selectedResourceTypes(fields)
	return includeSystems || includeAssets
}

func selectedResourceTypes(requiredFields []string) (includeSystems, includeAssets bool) {
	for _, field := range requiredFields {
		switch field {
		case "business_systems", "data_security":
			includeSystems = true
		case "network_assets":
			includeAssets = true
		}
	}
	return includeSystems, includeAssets
}

func normalizeReviewInput(input *ReviewInput) error {
	input.Decision = strings.TrimSpace(input.Decision)
	input.ReturnReason = strings.TrimSpace(input.ReturnReason)
	if input.Decision != "approved" && input.Decision != "returned" {
		return ErrInvalid
	}
	if input.Decision == "returned" && (input.ReturnReason == "" || len(input.ReturnReason) > 2000) {
		return ErrInvalid
	}
	if input.Decision == "approved" {
		input.ReturnReason = ""
		input.Comments = nil
		return nil
	}
	comments, err := normalizeReviewComments(input.Comments)
	if err != nil {
		return err
	}
	input.Comments = comments
	return nil
}

func principalOrganizationID(principal auth.Principal) string {
	if principal.Organization == nil {
		return ""
	}
	return principal.Organization.ID
}

func uniqueNonEmpty(values []string) []string {
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
