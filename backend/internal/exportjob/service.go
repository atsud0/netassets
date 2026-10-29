package exportjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/businesssystem"
	"netassets/internal/exportfile"
	"netassets/internal/inventorytask"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("export job forbidden")
	ErrInvalid   = errors.New("invalid export job")
	ErrNotFound  = errors.New("export job not found")
	ErrBusy      = errors.New("too many export jobs")
	ErrConflict  = errors.New("export job conflict")
	ErrExpired   = errors.New("export job expired")
)

const (
	ResourceAsset          = "asset"
	ResourceBusinessSystem = "business_system"
	ResourceInventoryTask  = "inventory_task"

	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusExpired   = "expired"

	jobFileName      = "export.xlsx"
	jobTemporaryName = ".creating.xlsx"
	maxActivePerUser = 3
	fileTTL          = 7 * 24 * time.Hour
)

type AssetFilter struct {
	OrganizationID string `json:"organization_id,omitempty"`
	Type           string `json:"type,omitempty"`
	Status         string `json:"status,omitempty"`
	Keyword        string `json:"q,omitempty"`
}

type BusinessSystemFilter struct {
	OrganizationID string `json:"organization_id,omitempty"`
	Status         string `json:"status,omitempty"`
	Keyword        string `json:"q,omitempty"`
}

type InventoryTaskFilter struct {
	TaskID string `json:"task_id"`
}

type CreateInput struct {
	ResourceType string          `json:"resource_type"`
	Format       string          `json:"format"`
	Filter       json.RawMessage `json:"filter"`
}

type Item struct {
	ID                 string          `json:"id"`
	ResourceType       string          `json:"resource_type"`
	Format             string          `json:"format"`
	Filter             json.RawMessage `json:"filter"`
	Status             string          `json:"status"`
	TotalRows          int             `json:"total_rows"`
	ProcessedRows      int             `json:"processed_rows"`
	FileName           string          `json:"file_name,omitempty"`
	ContentType        string          `json:"content_type,omitempty"`
	FileSize           int64           `json:"file_size"`
	ErrorMessage       string          `json:"error_message,omitempty"`
	StartedAt          *time.Time      `json:"started_at,omitempty"`
	CompletedAt        *time.Time      `json:"completed_at,omitempty"`
	ExpiresAt          *time.Time      `json:"expires_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	RequiredPermission string          `json:"-"`
	RequestedBy        string          `json:"-"`
	PrincipalSnapshot  json.RawMessage `json:"-"`
}

type PrincipalSnapshot struct {
	UserID        string              `json:"user_id"`
	Username      string              `json:"username"`
	DisplayName   string              `json:"display_name"`
	Organization  *auth.Organization  `json:"organization,omitempty"`
	Organizations []auth.Organization `json:"organizations"`
	Roles         []string            `json:"roles"`
	Permissions   []string            `json:"permissions"`
}

func snapshotPrincipal(principal auth.Principal) PrincipalSnapshot {
	return PrincipalSnapshot{
		UserID: principal.UserID, Username: principal.Username, DisplayName: principal.DisplayName,
		Organization: principal.Organization, Organizations: principal.Organizations,
		Roles: append([]string(nil), principal.Roles...), Permissions: append([]string(nil), principal.Permissions...),
	}
}

func (snapshot PrincipalSnapshot) Principal() auth.Principal {
	return auth.Principal{
		UserID: snapshot.UserID, Username: snapshot.Username, DisplayName: snapshot.DisplayName,
		Organization: snapshot.Organization, Organizations: snapshot.Organizations,
		Roles: snapshot.Roles, Permissions: snapshot.Permissions,
	}
}

type Service struct {
	pool            *pgxpool.Pool
	rootDir         string
	assets          *asset.Service
	businessSystems *businesssystem.Service
	inventoryTasks  *inventorytask.Service
	logger          *slog.Logger
	wake            chan struct{}
}

func NewService(pool *pgxpool.Pool, dataDir string, assets *asset.Service, businessSystems *businesssystem.Service, inventoryTasks *inventorytask.Service, logger *slog.Logger) *Service {
	return &Service{
		pool: pool, rootDir: filepath.Join(dataDir, "exports"), assets: assets,
		businessSystems: businessSystems, inventoryTasks: inventoryTasks, logger: logger,
		wake: make(chan struct{}, 1),
	}
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.pool == nil {
		return
	}
	go s.run(ctx)
}

func (s *Service) run(ctx context.Context) {
	if _, err := s.pool.Exec(ctx, `UPDATE export_jobs SET status='queued', started_at=NULL, updated_at=now() WHERE status='running'`); err != nil {
		s.logger.Error("recover export jobs", "error", err)
	}
	s.cleanup(ctx)
	poll := time.NewTicker(2 * time.Second)
	cleanup := time.NewTicker(time.Hour)
	defer poll.Stop()
	defer cleanup.Stop()
	for {
		for {
			job, err := s.claim(ctx)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					s.logger.Error("claim export job", "error", err)
				}
				break
			}
			if job == nil {
				break
			}
			s.process(ctx, *job)
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-poll.C:
		case <-cleanup.C:
			s.cleanup(ctx)
		}
	}
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput, requestID, ipAddress string) (Item, error) {
	resourceType := strings.TrimSpace(input.ResourceType)
	format := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(input.Format), "."))
	if format == "" {
		format = "xlsx"
	}
	if format != "xlsx" {
		return Item{}, invalidInput("导出格式仅支持 XLSX")
	}
	filter, permission, total, err := s.validateCreate(ctx, principal, resourceType, input.Filter)
	if err != nil {
		return Item{}, err
	}
	principalJSON, err := json.Marshal(snapshotPrincipal(principal))
	if err != nil {
		return Item{}, fmt.Errorf("encode export principal: %w", err)
	}
	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Item{}, fmt.Errorf("begin export job transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	// Serialize task creation per requester so concurrent requests cannot bypass
	// the active-job limit between COUNT and INSERT.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, principal.UserID); err != nil {
		return Item{}, fmt.Errorf("lock export job requester: %w", err)
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM export_jobs WHERE requested_by=$1::uuid AND status IN ('queued','running')`, principal.UserID).Scan(&active); err != nil {
		return Item{}, fmt.Errorf("count active export jobs: %w", err)
	}
	if active >= maxActivePerUser {
		return Item{}, ErrBusy
	}

	var item Item
	var filterJSON []byte
	err = tx.QueryRow(ctx, `
		INSERT INTO export_jobs (resource_type,format,filter_snapshot,principal_snapshot,requested_by,organization_id,required_permission,total_rows)
		VALUES ($1,'xlsx',$2::jsonb,$3::jsonb,$4::uuid,NULLIF($5,'')::uuid,$6,$7)
		RETURNING id::text,resource_type,format,filter_snapshot,status,total_rows,processed_rows,file_name,content_type,file_size,error_message,
		          started_at,completed_at,expires_at,created_at,updated_at,required_permission,requested_by::text
	`, resourceType, filter, principalJSON, principal.UserID, organizationID, permission, total).Scan(
		&item.ID, &item.ResourceType, &item.Format, &filterJSON, &item.Status, &item.TotalRows, &item.ProcessedRows,
		&item.FileName, &item.ContentType, &item.FileSize, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt,
		&item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.RequiredPermission, &item.RequestedBy,
	)
	if err != nil {
		return Item{}, fmt.Errorf("create export job: %w", err)
	}
	item.Filter = append(json.RawMessage(nil), filterJSON...)
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,NULLIF($2,'')::uuid,'export_job.create','export_job',$3,'创建后台导出任务',
		        jsonb_build_object('resource_type',$4::text,'format','xlsx','total_rows',$5::int),NULLIF($6,'')::inet,NULLIF($7,''))
	`, principal.UserID, organizationID, item.ID, resourceType, total, ipAddress, requestID)
	if err != nil {
		return Item{}, fmt.Errorf("write export job audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Item{}, fmt.Errorf("commit export job: %w", err)
	}
	s.signal()
	return item, nil
}

func (s *Service) validateCreate(ctx context.Context, principal auth.Principal, resourceType string, raw json.RawMessage) ([]byte, string, int, error) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage(`{}`)
	}
	switch resourceType {
	case ResourceAsset:
		if !principal.HasPermission("asset.manage") {
			return nil, "", 0, ErrForbidden
		}
		var filter AssetFilter
		if err := decodeStrict(raw, &filter); err != nil {
			return nil, "", 0, invalidInput("网络资产导出筛选条件格式不正确")
		}
		filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
		filter.Type = strings.TrimSpace(filter.Type)
		filter.Status = strings.TrimSpace(filter.Status)
		filter.Keyword = strings.TrimSpace(filter.Keyword)
		count, err := s.assets.ExportRowCount(ctx, principal, asset.ExportFilter{OrganizationID: filter.OrganizationID, Type: filter.Type, Status: filter.Status, Keyword: filter.Keyword})
		if err != nil {
			return nil, "", 0, mapResourceError(err)
		}
		encoded, _ := json.Marshal(filter)
		return encoded, "asset.manage", count, nil
	case ResourceBusinessSystem:
		if !principal.HasPermission("system.manage") {
			return nil, "", 0, ErrForbidden
		}
		var filter BusinessSystemFilter
		if err := decodeStrict(raw, &filter); err != nil {
			return nil, "", 0, invalidInput("业务系统导出筛选条件格式不正确")
		}
		filter.OrganizationID = strings.TrimSpace(filter.OrganizationID)
		filter.Status = strings.TrimSpace(filter.Status)
		filter.Keyword = strings.TrimSpace(filter.Keyword)
		count, err := s.businessSystems.ExportRowCount(ctx, principal, businesssystem.SearchFilter{
			OrganizationID: filter.OrganizationID, Status: filter.Status, Keyword: filter.Keyword,
		})
		if err != nil {
			return nil, "", 0, mapResourceError(err)
		}
		encoded, _ := json.Marshal(filter)
		return encoded, "system.manage", count, nil
	case ResourceInventoryTask:
		if !principal.HasPermission("inventory.manage") {
			return nil, "", 0, ErrForbidden
		}
		var filter InventoryTaskFilter
		if err := decodeStrict(raw, &filter); err != nil {
			return nil, "", 0, invalidInput("填报任务导出筛选条件格式不正确")
		}
		filter.TaskID = strings.TrimSpace(filter.TaskID)
		if filter.TaskID == "" {
			return nil, "", 0, invalidInput("请选择要导出的填报任务")
		}
		if !validID(filter.TaskID) {
			return nil, "", 0, invalidInput("填报任务标识不正确")
		}
		count, err := s.inventoryTasks.ExportRowCount(ctx, principal, filter.TaskID)
		if err != nil {
			return nil, "", 0, mapResourceError(err)
		}
		encoded, _ := json.Marshal(filter)
		return encoded, "inventory.manage", count, nil
	default:
		return nil, "", 0, invalidInput("请选择有效的导出资源类型")
	}
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrInvalid
		}
		return err
	}
	return nil
}

func mapResourceError(err error) error {
	switch {
	case errors.Is(err, asset.ErrForbidden), errors.Is(err, businesssystem.ErrForbidden), errors.Is(err, inventorytask.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, asset.ErrInvalid), errors.Is(err, businesssystem.ErrInvalid), errors.Is(err, inventorytask.ErrInvalid):
		var assetField *asset.InputValidationError
		if errors.As(err, &assetField) {
			return invalidInput(assetField.Message)
		}
		var systemField *businesssystem.InputValidationError
		if errors.As(err, &systemField) {
			return invalidInput(systemField.Message)
		}
		var taskField *inventorytask.InputValidationError
		if errors.As(err, &taskField) {
			return invalidInput(taskField.Message)
		}
		return ErrInvalid
	case errors.Is(err, asset.ErrNotFound), errors.Is(err, businesssystem.ErrNotFound), errors.Is(err, inventorytask.ErrNotFound):
		return ErrNotFound
	default:
		return err
	}
}

func (s *Service) Search(ctx context.Context, principal auth.Principal, page pagination.Query, status, resourceType string) (pagination.Result[Item], error) {
	status = strings.TrimSpace(status)
	resourceType = strings.TrimSpace(resourceType)
	if status != "" && status != StatusQueued && status != StatusRunning && status != StatusCompleted && status != StatusFailed && status != StatusExpired {
		return pagination.Result[Item]{}, invalidInput("请选择有效的导出任务状态")
	}
	if resourceType != "" && resourceType != ResourceAsset && resourceType != ResourceBusinessSystem && resourceType != ResourceInventoryTask {
		return pagination.Result[Item]{}, invalidInput("请选择有效的导出资源类型")
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text,resource_type,format,filter_snapshot,status,total_rows,processed_rows,file_name,content_type,file_size,error_message,
		       started_at,completed_at,expires_at,created_at,updated_at,required_permission,requested_by::text,count(*) OVER ()
		FROM export_jobs
		WHERE requested_by=$1::uuid AND ($2='' OR status=$2) AND ($3='' OR resource_type=$3)
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5
	`, principal.UserID, status, resourceType, page.PageSize, page.Offset())
	if err != nil {
		return pagination.Result[Item]{}, fmt.Errorf("search export jobs: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0, page.PageSize)
	total := 0
	for rows.Next() {
		var item Item
		var filter []byte
		if err := rows.Scan(&item.ID, &item.ResourceType, &item.Format, &filter, &item.Status, &item.TotalRows, &item.ProcessedRows,
			&item.FileName, &item.ContentType, &item.FileSize, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt,
			&item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.RequiredPermission, &item.RequestedBy, &total); err != nil {
			return pagination.Result[Item]{}, fmt.Errorf("scan export job: %w", err)
		}
		item.Filter = append(json.RawMessage(nil), filter...)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Item]{}, fmt.Errorf("iterate export jobs: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), page) {
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM export_jobs WHERE requested_by=$1::uuid AND ($2='' OR status=$2) AND ($3='' OR resource_type=$3)`, principal.UserID, status, resourceType).Scan(&total); err != nil {
			return pagination.Result[Item]{}, fmt.Errorf("count export jobs: %w", err)
		}
	}
	return pagination.NewResult(items, total, page), nil
}

func (s *Service) Get(ctx context.Context, principal auth.Principal, id string) (Item, error) {
	if !validID(id) {
		return Item{}, ErrNotFound
	}
	var item Item
	var filter []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id::text,resource_type,format,filter_snapshot,status,total_rows,processed_rows,file_name,content_type,file_size,error_message,
		       started_at,completed_at,expires_at,created_at,updated_at,required_permission,requested_by::text
		FROM export_jobs WHERE id::text=$2 AND requested_by=$1::uuid
	`, principal.UserID, id).Scan(&item.ID, &item.ResourceType, &item.Format, &filter, &item.Status, &item.TotalRows, &item.ProcessedRows,
		&item.FileName, &item.ContentType, &item.FileSize, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt,
		&item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.RequiredPermission, &item.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("get export job: %w", err)
	}
	item.Filter = append(json.RawMessage(nil), filter...)
	return item, nil
}

func (s *Service) Download(ctx context.Context, principal auth.Principal, id, requestID, ipAddress string) (string, Item, error) {
	item, err := s.Get(ctx, principal, id)
	if err != nil {
		return "", Item{}, err
	}
	if item.Status == StatusExpired || (item.ExpiresAt != nil && !item.ExpiresAt.After(time.Now())) {
		s.expire(ctx, item.ID)
		return "", Item{}, ErrExpired
	}
	if item.Status != StatusCompleted {
		return "", Item{}, ErrConflict
	}
	path := s.filePath(item.ID)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", Item{}, ErrNotFound
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", Item{}, fmt.Errorf("inspect export file: %w", err)
	}
	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,NULLIF($2,'')::uuid,'export_job.download','export_job',$3,'下载后台导出文件',jsonb_build_object('file_name',$4::text,'file_size',$5::bigint),NULLIF($6,'')::inet,NULLIF($7,''))
	`, principal.UserID, organizationID, item.ID, item.FileName, item.FileSize, ipAddress, requestID); err != nil {
		return "", Item{}, fmt.Errorf("write export download audit: %w", err)
	}
	return path, item, nil
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, id, requestID, ipAddress string) error {
	if !validID(id) {
		return ErrNotFound
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin export job deletion: %w", err)
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM export_jobs WHERE id::text=$2 AND requested_by=$1::uuid FOR UPDATE`, principal.UserID, id).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load export job for delete: %w", err)
	}
	if status == StatusRunning {
		return ErrConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM export_jobs WHERE id::text=$2 AND requested_by=$1::uuid`, principal.UserID, id); err != nil {
		return fmt.Errorf("delete export job: %w", err)
	}
	organizationID := ""
	if principal.Organization != nil {
		organizationID = principal.Organization.ID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details,request_ip,request_id)
		VALUES ($1::uuid,NULLIF($2,'')::uuid,'export_job.delete','export_job',$3,'删除后台导出任务',jsonb_build_object('previous_status',$4::text),NULLIF($5,'')::inet,NULLIF($6,''))
	`, principal.UserID, organizationID, id, status, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write export deletion audit: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit export job deletion: %w", err)
	}
	if err := os.RemoveAll(s.jobDir(id)); err != nil {
		s.logger.Error("delete export job file", "job_id", id, "error", err)
	}
	return nil
}

func (s *Service) claim(ctx context.Context) (*Item, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var item Item
	var filter, principal []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text,resource_type,format,filter_snapshot,principal_snapshot,status,total_rows,processed_rows,
		       file_name,content_type,file_size,error_message,started_at,completed_at,expires_at,created_at,updated_at,required_permission,requested_by::text
		FROM export_jobs WHERE status='queued' ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1
	`).Scan(&item.ID, &item.ResourceType, &item.Format, &filter, &principal, &item.Status, &item.TotalRows, &item.ProcessedRows,
		&item.FileName, &item.ContentType, &item.FileSize, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt,
		&item.ExpiresAt, &item.CreatedAt, &item.UpdatedAt, &item.RequiredPermission, &item.RequestedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE export_jobs SET status='running',started_at=now(),processed_rows=0,error_message='',updated_at=now() WHERE id=$1::uuid`, item.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	item.Status = StatusRunning
	item.Filter = append(json.RawMessage(nil), filter...)
	item.PrincipalSnapshot = append(json.RawMessage(nil), principal...)
	return &item, nil
}

func (s *Service) process(ctx context.Context, item Item) {
	var snapshot PrincipalSnapshot
	if err := json.Unmarshal(item.PrincipalSnapshot, &snapshot); err != nil {
		s.fail(ctx, item.ID, fmt.Errorf("decode principal snapshot: %w", err))
		return
	}
	principal := snapshot.Principal()
	if !principal.HasPermission(item.RequiredPermission) {
		s.fail(ctx, item.ID, ErrForbidden)
		return
	}
	if err := os.MkdirAll(s.jobDir(item.ID), 0o700); err != nil {
		s.fail(ctx, item.ID, err)
		return
	}
	temporaryPath := filepath.Join(s.jobDir(item.ID), jobTemporaryName)
	_ = os.Remove(temporaryPath)
	progress := func(total, processed int) error {
		_, err := s.pool.Exec(ctx, `UPDATE export_jobs SET total_rows=$2,processed_rows=$3,updated_at=now() WHERE id=$1::uuid AND status='running'`, item.ID, total, processed)
		return err
	}
	var meta exportfile.Metadata
	var err error
	switch item.ResourceType {
	case ResourceAsset:
		var filter AssetFilter
		err = json.Unmarshal(item.Filter, &filter)
		if err == nil {
			meta, err = s.assets.WriteXLSXExport(ctx, principal, asset.ExportFilter{OrganizationID: filter.OrganizationID, Type: filter.Type, Status: filter.Status, Keyword: filter.Keyword}, temporaryPath, progress)
		}
	case ResourceBusinessSystem:
		var filter BusinessSystemFilter
		err = json.Unmarshal(item.Filter, &filter)
		if err == nil {
			meta, err = s.businessSystems.WriteXLSXExport(ctx, principal, businesssystem.SearchFilter{
				OrganizationID: filter.OrganizationID, Status: filter.Status, Keyword: filter.Keyword,
			}, temporaryPath, progress)
		}
	case ResourceInventoryTask:
		var filter InventoryTaskFilter
		err = json.Unmarshal(item.Filter, &filter)
		if err == nil {
			meta, err = s.inventoryTasks.WriteXLSXExport(ctx, principal, filter.TaskID, temporaryPath, progress)
		}
	default:
		err = ErrInvalid
	}
	if err != nil {
		_ = os.Remove(temporaryPath)
		s.fail(ctx, item.ID, err)
		return
	}
	finalPath := s.filePath(item.ID)
	if err := os.Rename(temporaryPath, finalPath); err != nil {
		s.fail(ctx, item.ID, fmt.Errorf("publish export file: %w", err))
		return
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		s.fail(ctx, item.ID, fmt.Errorf("inspect generated export: %w", err))
		return
	}
	expiresAt := time.Now().Add(fileTTL)
	_, err = s.pool.Exec(ctx, `
		UPDATE export_jobs SET status='completed',processed_rows=$2,total_rows=$2,file_name=$3,content_type=$4,file_size=$5,
		       completed_at=now(),expires_at=$6,updated_at=now() WHERE id=$1::uuid AND status='running'
	`, item.ID, meta.Count, meta.FileName, meta.ContentType, info.Size(), expiresAt)
	if err != nil {
		s.logger.Error("complete export job", "job_id", item.ID, "error", err)
		return
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_logs (user_id,organization_id,action,resource_type,resource_id,summary,details)
		SELECT requested_by,organization_id,'export_job.complete','export_job',id::text,'后台导出任务生成完成',
		       jsonb_build_object('resource_type',resource_type,'count',processed_rows,'file_size',file_size)
		FROM export_jobs WHERE id=$1::uuid
	`, item.ID)
	if err != nil {
		s.logger.Error("write export completion audit", "job_id", item.ID, "error", err)
	}
}

func (s *Service) fail(ctx context.Context, id string, failure error) {
	s.logger.Error("generate export job", "job_id", id, "error", failure)
	message := "生成文件失败，请重试；如多次失败请联系集团管理员"
	switch {
	case errors.Is(failure, ErrForbidden):
		message = "导出任务的权限快照无效，请重新创建任务"
	case errors.Is(failure, ErrInvalid):
		message = "导出任务参数无效，请重新创建任务"
	}
	_, err := s.pool.Exec(ctx, `UPDATE export_jobs SET status='failed',error_message=$2,completed_at=now(),updated_at=now() WHERE id=$1::uuid AND status='running'`, id, message)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.Error("fail export job", "job_id", id, "error", err)
	}
	_ = os.RemoveAll(s.jobDir(id))
}

func (s *Service) cleanup(ctx context.Context) {
	rows, err := s.pool.Query(ctx, `UPDATE export_jobs SET status='expired',updated_at=now() WHERE status='completed' AND expires_at<=now() RETURNING id::text`)
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			s.logger.Error("expire export jobs", "error", err)
		}
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			_ = os.RemoveAll(s.jobDir(id))
		}
	}
}

func (s *Service) expire(ctx context.Context, id string) {
	_, _ = s.pool.Exec(ctx, `UPDATE export_jobs SET status='expired',updated_at=now() WHERE id=$1::uuid AND status='completed'`, id)
	_ = os.RemoveAll(s.jobDir(id))
}

func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) jobDir(id string) string   { return filepath.Join(s.rootDir, id) }
func (s *Service) filePath(id string) string { return filepath.Join(s.jobDir(id), jobFileName) }

func validID(value string) bool {
	if len(value) != 36 || filepath.Base(value) != value {
		return false
	}
	for index, r := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			if r != '-' {
				return false
			}
			continue
		}
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}
