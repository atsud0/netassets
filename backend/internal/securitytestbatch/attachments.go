package securitytestbatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

const MaxAttachmentSize int64 = 20 << 20

type Attachment struct {
	ID             string    `json:"id"`
	BatchID        string    `json:"batch_id"`
	Category       string    `json:"category"`
	FileName       string    `json:"file_name"`
	ContentType    string    `json:"content_type"`
	SizeBytes      int64     `json:"size_bytes"`
	Description    string    `json:"description,omitempty"`
	UploadedBy     string    `json:"uploaded_by,omitempty"`
	UploadedByName string    `json:"uploaded_by_name,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	StoragePath    string    `json:"-"`
}

type AttachmentInput struct {
	Category    string
	FileName    string
	ContentType string
	Description string
	SizeBytes   int64
	Reader      io.Reader
}

var allowedAttachmentExtensions = map[string]struct{}{
	".pdf": {}, ".png": {}, ".jpg": {}, ".jpeg": {}, ".txt": {}, ".csv": {},
	".xlsx": {}, ".docx": {}, ".zip": {},
}

func (s *Service) AddAttachment(ctx context.Context, principal auth.Principal, batchID string, input AttachmentInput, requestID, ipAddress string) (Attachment, error) {
	if !principal.CanManageSecurityTests() {
		return Attachment{}, ErrForbidden
	}
	batch, err := s.Get(ctx, principal, batchID)
	if err != nil {
		return Attachment{}, err
	}

	input.Category = strings.TrimSpace(input.Category)
	input.FileName = sanitizeOriginalFilename(input.FileName)
	input.Description = strings.TrimSpace(input.Description)
	ext := strings.ToLower(filepath.Ext(input.FileName))
	if input.Reader == nil || input.FileName == "" || input.SizeBytes <= 0 || input.SizeBytes > MaxAttachmentSize ||
		len(input.Description) > 500 || !validAttachmentCategory(input.Category) {
		return Attachment{}, ErrInvalid
	}
	if _, ok := allowedAttachmentExtensions[ext]; !ok {
		return Attachment{}, ErrInvalid
	}
	if batch.Status == "draft" || batch.Status == "cancelled" {
		return Attachment{}, ErrState
	}

	storedName, err := randomStoredName(ext)
	if err != nil {
		return Attachment{}, fmt.Errorf("generate security test batch attachment name: %w", err)
	}
	relativePath := filepath.Join("security-test-batches", batchID, storedName)
	absolutePath := filepath.Join(s.storageDir, relativePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o750); err != nil {
		return Attachment{}, fmt.Errorf("create security test batch attachment directory: %w", err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(absolutePath), ".upload-*")
	if err != nil {
		return Attachment{}, fmt.Errorf("create security test batch attachment temporary file: %w", err)
	}
	tempPath := tempFile.Name()
	cleanupTemp := true
	defer func() {
		_ = tempFile.Close()
		if cleanupTemp {
			_ = os.Remove(tempPath)
		}
	}()

	written, err := io.Copy(tempFile, io.LimitReader(input.Reader, MaxAttachmentSize+1))
	if err != nil {
		return Attachment{}, fmt.Errorf("store security test batch attachment: %w", err)
	}
	if written != input.SizeBytes || written > MaxAttachmentSize {
		return Attachment{}, ErrInvalid
	}
	if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
		return Attachment{}, fmt.Errorf("inspect security test batch attachment: %w", err)
	}
	header := make([]byte, 512)
	n, err := tempFile.Read(header)
	if err != nil && !errors.Is(err, io.EOF) {
		return Attachment{}, fmt.Errorf("inspect security test batch attachment content: %w", err)
	}
	detectedType := http.DetectContentType(header[:n])
	if normalized := normalizeContentType(input.ContentType); normalized != "" && normalized != "application/octet-stream" {
		detectedType = normalized
	}
	if err := tempFile.Close(); err != nil {
		return Attachment{}, fmt.Errorf("close security test batch attachment: %w", err)
	}
	if err := os.Rename(tempPath, absolutePath); err != nil {
		return Attachment{}, fmt.Errorf("finalize security test batch attachment: %w", err)
	}
	cleanupTemp = false
	cleanupStored := true
	defer func() {
		if cleanupStored {
			_ = os.Remove(absolutePath)
		}
	}()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Attachment{}, fmt.Errorf("begin security test batch attachment transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	var attachmentID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO security_test_batch_attachments
		(batch_id, category, file_name, stored_name, content_type, size_bytes, storage_path, description, uploaded_by)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), $9::uuid)
		RETURNING id::text`, batchID, input.Category, input.FileName, storedName, detectedType, written,
		relativePath, input.Description, principal.UserID).Scan(&attachmentID); err != nil {
		return Attachment{}, fmt.Errorf("insert security test batch attachment: %w", err)
	}
	if err := writeEvent(ctx, tx, principal.UserID, batchID, "attachment_upload", batch.Status, batch.Status,
		"上传附件："+input.FileName, map[string]any{"attachment_id": attachmentID, "category": input.Category, "size_bytes": written}); err != nil {
		return Attachment{}, err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_batch.attachment_upload", batchID,
		"上传安全测试批次附件 "+batch.Name,
		map[string]any{"attachment_id": attachmentID, "file_name": input.FileName, "category": input.Category}, requestID, ipAddress); err != nil {
		return Attachment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Attachment{}, fmt.Errorf("commit security test batch attachment transaction: %w", err)
	}
	cleanupStored = false
	return s.GetAttachment(ctx, principal, batchID, attachmentID)
}

func (s *Service) GetAttachment(ctx context.Context, principal auth.Principal, batchID, attachmentID string) (Attachment, error) {
	if !principal.CanManageSecurityTests() {
		return Attachment{}, ErrForbidden
	}
	if !validUUID(batchID) || !validUUID(attachmentID) {
		return Attachment{}, ErrInvalid
	}
	if _, err := s.get(ctx, batchID); err != nil {
		return Attachment{}, err
	}
	item, err := loadAttachment(ctx, s.pool, batchID, attachmentID)
	if err != nil {
		return Attachment{}, err
	}
	item.StoragePath = filepath.Join(s.storageDir, item.StoragePath)
	return item, nil
}

func (s *Service) DeleteAttachment(ctx context.Context, principal auth.Principal, batchID, attachmentID, requestID, ipAddress string) error {
	if !principal.CanManageSecurityTests() {
		return ErrForbidden
	}
	batch, err := s.Get(ctx, principal, batchID)
	if err != nil {
		return err
	}
	attachment, err := loadAttachment(ctx, s.pool, batchID, attachmentID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete security test batch attachment: %w", err)
	}
	defer tx.Rollback(ctx)
	command, err := tx.Exec(ctx, `DELETE FROM security_test_batch_attachments WHERE id::text = $1 AND batch_id::text = $2`, attachmentID, batchID)
	if err != nil {
		return fmt.Errorf("delete security test batch attachment: %w", err)
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	if err := writeEvent(ctx, tx, principal.UserID, batchID, "attachment_delete", batch.Status, batch.Status,
		"删除附件："+attachment.FileName, map[string]any{"attachment_id": attachmentID}); err != nil {
		return err
	}
	if err := writeAudit(ctx, tx, principal, "security_test_batch.attachment_delete", batchID,
		"删除安全测试批次附件 "+batch.Name,
		map[string]any{"attachment_id": attachmentID, "file_name": attachment.FileName}, requestID, ipAddress); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete security test batch attachment: %w", err)
	}
	if err := os.Remove(filepath.Join(s.storageDir, attachment.StoragePath)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete security test batch attachment file: %w", err)
	}
	return nil
}

// SearchAttachments returns attachment metadata without loading file contents.
// The dedicated download endpoint continues to enforce authorization per file.
func (s *Service) SearchAttachments(ctx context.Context, principal auth.Principal, batchID string, query pagination.Query) (pagination.Result[Attachment], error) {
	if !principal.CanManageSecurityTests() {
		return pagination.Result[Attachment]{}, ErrForbidden
	}
	if !validUUID(batchID) {
		return pagination.Result[Attachment]{}, ErrInvalid
	}
	if _, err := s.get(ctx, batchID); err != nil {
		return pagination.Result[Attachment]{}, err
	}
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM security_test_batch_attachments WHERE batch_id=$1::uuid`, batchID).Scan(&total); err != nil {
		return pagination.Result[Attachment]{}, fmt.Errorf("count security test batch attachments: %w", err)
	}
	rows, err := s.pool.Query(ctx, attachmentSelect+`
		WHERE a.batch_id::text = $1
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $2 OFFSET $3`, batchID, query.PageSize, query.Offset())
	if err != nil {
		return pagination.Result[Attachment]{}, fmt.Errorf("list security test batch attachments: %w", err)
	}
	defer rows.Close()
	items := make([]Attachment, 0, query.PageSize)
	for rows.Next() {
		var item Attachment
		if err := scanAttachment(rows, &item); err != nil {
			return pagination.Result[Attachment]{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Attachment]{}, fmt.Errorf("iterate security test batch attachments: %w", err)
	}
	return pagination.NewResult(items, total, query), nil
}

func loadAttachment(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, batchID, attachmentID string) (Attachment, error) {
	var item Attachment
	err := scanAttachment(q.QueryRow(ctx, attachmentSelect+` WHERE a.batch_id::text = $1 AND a.id::text = $2`, batchID, attachmentID), &item)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attachment{}, ErrNotFound
	}
	if err != nil {
		return Attachment{}, err
	}
	return item, nil
}

const attachmentSelect = `
	SELECT a.id::text, a.batch_id::text, a.category, a.file_name, a.content_type, a.size_bytes,
	       COALESCE(a.description, ''), COALESCE(a.uploaded_by::text, ''), COALESCE(u.display_name, ''),
	       a.created_at, a.storage_path
	FROM security_test_batch_attachments a
	LEFT JOIN users u ON u.id = a.uploaded_by`

func scanAttachment(row interface{ Scan(...any) error }, item *Attachment) error {
	if err := row.Scan(&item.ID, &item.BatchID, &item.Category, &item.FileName, &item.ContentType, &item.SizeBytes,
		&item.Description, &item.UploadedBy, &item.UploadedByName, &item.CreatedAt, &item.StoragePath); err != nil {
		return fmt.Errorf("scan security test batch attachment: %w", err)
	}
	return nil
}

func validAttachmentCategory(value string) bool {
	return value == "test_report" || value == "reference"
}

func sanitizeOriginalFilename(value string) string {
	value = filepath.Base(strings.TrimSpace(strings.ReplaceAll(value, "\\", "/")))
	value = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, value)
	if len(value) > 255 {
		ext := filepath.Ext(value)
		base := strings.TrimSuffix(value, ext)
		maxBase := 255 - len(ext)
		if maxBase < 1 {
			return ""
		}
		value = base[:maxBase] + ext
	}
	return value
}

func randomStoredName(ext string) (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer) + ext, nil
}

func normalizeContentType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return mediaType
}
