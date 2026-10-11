package remediation

import (
	"context"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"netassets/internal/auth"
)

const MaxAttachmentSize int64 = 20 << 20

type AttachmentInput struct {
	Name, Visibility string
	Version          int
	Reader           io.Reader
}

func (s *Service) Upload(ctx context.Context, p auth.Principal, id string, in AttachmentInput, executor bool) (Object, error) {
	if !validID(id) || in.Reader == nil || in.Version < 1 || !contains([]string{"internal", "participants"}, in.Visibility) {
		return nil, ErrInvalid
	}
	name := filepath.Base(strings.ReplaceAll(in.Name, "\\", "/"))
	ext := strings.ToLower(filepath.Ext(name))
	if !textOK(name, 255, true) || !contains([]string{".png", ".jpg", ".jpeg", ".pdf", ".txt", ".log", ".csv", ".zip", ".xlsx", ".docx"}, ext) {
		return nil, ErrInvalid
	}
	t, err := loadTask(ctx, s.pool, id)
	if err != nil {
		return nil, err
	}
	if err = authorizeTask(ctx, s.pool, p, t, executor); err != nil {
		return nil, err
	}
	dir := filepath.Join(s.storageDir, "remediation", id)
	if err = os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, ".upload-")
	if err != nil {
		return nil, err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	size, err := io.Copy(file, io.LimitReader(in.Reader, MaxAttachmentSize+1))
	if err != nil {
		return nil, err
	}
	if size <= 0 || size > MaxAttachmentSize {
		return nil, ErrInvalid
	}
	if _, err = file.Seek(0, 0); err != nil {
		return nil, err
	}
	var head [512]byte
	n, _ := file.Read(head[:])
	kind := http.DetectContentType(head[:n])
	if (ext == ".png" && kind != "image/png") || ((ext == ".jpg" || ext == ".jpeg") && kind != "image/jpeg") || (ext == ".pdf" && kind != "application/pdf") {
		return nil, ErrInvalid
	}
	// HTML/script is never accepted as a text proof or rendered inline.
	if strings.Contains(kind, "html") || strings.Contains(kind, "javascript") {
		return nil, ErrInvalid
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	t, err = lockTask(ctx, tx, p, id, executor)
	if err != nil {
		return nil, err
	}
	canUpload := contains([]string{"pending_acceptance", "in_progress"}, t.Status) || (!executor && internal(ctx, tx, p, t) && contains([]string{"pending_org_review", "pending_security_review"}, t.Status))
	if t.Version != in.Version || !canUpload {
		return nil, ErrConflict
	}
	if in.Visibility == "internal" && (executor || !internal(ctx, tx, p, t)) {
		return nil, ErrForbidden
	}
	key := filepath.Join("remediation", id, token()+ext)
	target := filepath.Join(s.storageDir, key)
	if err = file.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(file.Name(), target); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(target)
		}
	}()
	var attachmentID string
	err = tx.QueryRow(ctx, `INSERT INTO remediation_attachments(task_id,visibility,storage_key,original_name,content_type,size_bytes,uploaded_by) VALUES($1::uuid,$2,$3,$4,$5,$6,$7::uuid) RETURNING id::text`, id, in.Visibility, key, name, kind, size, p.UserID).Scan(&attachmentID)
	if err != nil {
		return nil, err
	}
	if err = event(ctx, tx, p, t, "attachment.upload", in.Visibility, Object{"attachment_id": attachmentID, "name": name}); err != nil {
		return nil, err
	}
	err = tx.Commit(ctx)
	if err != nil {
		return nil, err
	}
	committed = true
	return Object{"id": attachmentID, "original_name": name}, nil
}

type Download struct {
	File        *os.File
	Disposition string
	Size        int64
}

func (s *Service) Download(ctx context.Context, p auth.Principal, id, attachmentID string, executor bool) (Download, error) {
	if !validID(id) || !validID(attachmentID) {
		return Download{}, ErrNotFound
	}
	tx, err := s.begin(ctx)
	if err != nil {
		return Download{}, err
	}
	defer tx.Rollback(ctx)
	t, err := loadTask(ctx, tx, id)
	if err != nil {
		return Download{}, err
	}
	if err = authorizeTask(ctx, tx, p, t, executor); err != nil {
		return Download{}, err
	}
	var name, key string
	var size int64
	err = tx.QueryRow(ctx, `SELECT original_name,storage_key,size_bytes FROM remediation_attachments WHERE id=$1::uuid AND task_id=$2::uuid AND ($3 OR visibility='participants')`, attachmentID, id, !executor && internal(ctx, tx, p, t)).Scan(&name, &key, &size)
	if err != nil {
		return Download{}, dbError(err)
	}
	if err = audit(ctx, tx, p, t.OrganizationID, attachmentID, "attachment.download.authorized", Object{"task_id": id}); err != nil {
		return Download{}, err
	}
	file, err := os.Open(filepath.Join(s.storageDir, key))
	if err != nil {
		return Download{}, ErrNotFound
	}
	if err = tx.Commit(ctx); err != nil {
		file.Close()
		return Download{}, err
	}
	return Download{File: file, Disposition: mime.FormatMediaType("attachment", map[string]string{"filename": name}), Size: size}, nil
}

func (s *Service) RecordDownloadResult(ctx context.Context, p auth.Principal, id, file string, completed bool) error {
	t, err := loadTask(ctx, s.pool, id)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = audit(ctx, tx, p, t.OrganizationID, file, "attachment.download", Object{"task_id": id, "completed": completed}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
