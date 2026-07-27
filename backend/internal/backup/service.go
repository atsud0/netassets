package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrForbidden = errors.New("backup forbidden")
	ErrNotFound  = errors.New("backup not found")
	ErrBusy      = errors.New("backup is busy")
)

// WriteGate coordinates application writes with a Web-created backup. All regular
// mutating HTTP requests hold a shared lock; backup creation takes the exclusive
// lock so the PostgreSQL dump and attachment archive describe the same application state.
type WriteGate struct{ mu sync.RWMutex }

func (g *WriteGate) TryEnterWrite() bool  { return g.mu.TryRLock() }
func (g *WriteGate) LeaveWrite()          { g.mu.RUnlock() }
func (g *WriteGate) TryBeginBackup() bool { return g.mu.TryLock() }
func (g *WriteGate) EndBackup()           { g.mu.Unlock() }

type Item struct {
	ID           string    `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	DatabaseSize int64     `json:"database_size"`
	DataSize     int64     `json:"data_size"`
	Verified     bool      `json:"verified"`
}

type Service struct {
	pool        *pgxpool.Pool
	databaseURL string
	dataDir     string
	rootDir     string
	gate        *WriteGate
}

func NewService(pool *pgxpool.Pool, databaseURL, dataDir string) *Service {
	return &Service{pool: pool, databaseURL: databaseURL, dataDir: dataDir, rootDir: filepath.Join(dataDir, "backups"), gate: &WriteGate{}}
}

func (s *Service) WriteGate() *WriteGate { return s.gate }

func (s *Service) List(ctx context.Context, principal auth.Principal) ([]Item, error) {
	result, err := s.Search(ctx, principal, pagination.Query{Page: 1, PageSize: pagination.MaxPageSize})
	return result.Items, err
}

// Search bounds the response after directory metadata has been sorted. Backup
// manifests live on disk, so full directory-name enumeration is unavoidable,
// while only the requested page is serialized to the client.
func (s *Service) Search(ctx context.Context, principal auth.Principal, page pagination.Query) (pagination.Result[Item], error) {
	if !principal.HasRole("group_admin") {
		return pagination.Result[Item]{}, ErrForbidden
	}
	entries, err := os.ReadDir(s.rootDir)
	if errors.Is(err, os.ErrNotExist) {
		return pagination.NewResult([]Item{}, 0, page), nil
	}
	if err != nil {
		return pagination.Result[Item]{}, fmt.Errorf("read backup directory: %w", err)
	}
	items := make([]Item, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || !validID(entry.Name()) {
			continue
		}
		item, err := s.inspect(entry.Name(), false)
		if err == nil {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	total := len(items)
	start := page.Offset()
	if start >= total {
		return pagination.NewResult([]Item{}, total, page), nil
	}
	end := start + page.PageSize
	if end > total {
		end = total
	}
	return pagination.NewResult(items[start:end], total, page), nil
}

func (s *Service) Create(ctx context.Context, principal auth.Principal, requestID, ipAddress string) (Item, error) {
	if !principal.HasRole("group_admin") {
		return Item{}, ErrForbidden
	}
	if !s.gate.TryBeginBackup() {
		return Item{}, ErrBusy
	}
	defer s.gate.EndBackup()
	if err := os.MkdirAll(s.rootDir, 0o700); err != nil {
		return Item{}, fmt.Errorf("create backup root: %w", err)
	}
	id, err := newID()
	if err != nil {
		return Item{}, err
	}
	temporaryDir, err := os.MkdirTemp(s.rootDir, ".creating-")
	if err != nil {
		return Item{}, fmt.Errorf("create backup workspace: %w", err)
	}
	defer os.RemoveAll(temporaryDir)
	databaseFile := filepath.Join(temporaryDir, "database.dump")
	dataFile := filepath.Join(temporaryDir, "app-data.tar.gz")
	if err := dumpDatabase(ctx, s.databaseURL, databaseFile); err != nil {
		return Item{}, err
	}
	if err := writeDataArchive(s.dataDir, dataFile); err != nil {
		return Item{}, err
	}
	if err := validateArchive(ctx, databaseFile, dataFile); err != nil {
		return Item{}, err
	}
	databaseSHA, err := checksum(databaseFile)
	if err != nil {
		return Item{}, err
	}
	dataSHA, err := checksum(dataFile)
	if err != nil {
		return Item{}, err
	}
	createdAt := time.Now().UTC()
	manifest := fmt.Sprintf("format_version=1\ncreated_at_utc=%s\ndatabase_file=database.dump\ndatabase_sha256=%s\napp_data_file=app-data.tar.gz\napp_data_sha256=%s\n", createdAt.Format(time.RFC3339), databaseSHA, dataSHA)
	if err := os.WriteFile(filepath.Join(temporaryDir, "manifest.txt"), []byte(manifest), 0o600); err != nil {
		return Item{}, fmt.Errorf("write backup manifest: %w", err)
	}
	if err := os.Chmod(databaseFile, 0o600); err != nil {
		return Item{}, err
	}
	if err := os.Chmod(dataFile, 0o600); err != nil {
		return Item{}, err
	}
	finalDir := filepath.Join(s.rootDir, id)
	if err := os.Rename(temporaryDir, finalDir); err != nil {
		return Item{}, fmt.Errorf("finalize backup: %w", err)
	}
	item, err := s.inspect(id, true)
	if err != nil {
		return Item{}, err
	}
	if err := s.writeAudit(ctx, principal, "backup.create", id, "集团管理员创建系统备份", requestID, ipAddress); err != nil {
		return Item{}, err
	}
	return item, nil
}

func (s *Service) Verify(ctx context.Context, principal auth.Principal, id string) (Item, error) {
	if !principal.HasRole("group_admin") {
		return Item{}, ErrForbidden
	}
	item, err := s.inspect(id, true)
	if err != nil {
		return Item{}, err
	}
	if err := validateArchive(ctx, filepath.Join(s.rootDir, id, "database.dump"), filepath.Join(s.rootDir, id, "app-data.tar.gz")); err != nil {
		return Item{}, err
	}
	item.Verified = true
	return item, nil
}

func (s *Service) Delete(ctx context.Context, principal auth.Principal, id, requestID, ipAddress string) error {
	if !principal.HasRole("group_admin") {
		return ErrForbidden
	}
	if _, err := s.inspect(id, false); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.rootDir, id)); err != nil {
		return fmt.Errorf("delete backup: %w", err)
	}
	return s.writeAudit(ctx, principal, "backup.delete", id, "集团管理员删除系统备份", requestID, ipAddress)
}

func (s *Service) FilePath(ctx context.Context, principal auth.Principal, id, file string) (string, string, error) {
	if !principal.HasRole("group_admin") {
		return "", "", ErrForbidden
	}
	if _, err := s.inspect(id, false); err != nil {
		return "", "", err
	}
	files := map[string]struct{ name, contentType string }{
		"database":    {"database.dump", "application/octet-stream"},
		"attachments": {"app-data.tar.gz", "application/gzip"},
		"manifest":    {"manifest.txt", "text/plain; charset=utf-8"},
	}
	selected, exists := files[file]
	if !exists {
		return "", "", ErrNotFound
	}
	return filepath.Join(s.rootDir, id, selected.name), selected.contentType, nil
}

func (s *Service) inspect(id string, verify bool) (Item, error) {
	if !validID(id) {
		return Item{}, ErrNotFound
	}
	dir := filepath.Join(s.rootDir, id)
	manifest, err := parseManifest(filepath.Join(dir, "manifest.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	createdAt, err := time.Parse(time.RFC3339, manifest["created_at_utc"])
	if err != nil || manifest["format_version"] != "1" {
		return Item{}, fmt.Errorf("invalid backup manifest")
	}
	databaseFile := filepath.Join(dir, "database.dump")
	dataFile := filepath.Join(dir, "app-data.tar.gz")
	databaseInfo, err := os.Stat(databaseFile)
	if err != nil {
		return Item{}, ErrNotFound
	}
	dataInfo, err := os.Stat(dataFile)
	if err != nil {
		return Item{}, ErrNotFound
	}
	item := Item{ID: id, CreatedAt: createdAt, DatabaseSize: databaseInfo.Size(), DataSize: dataInfo.Size()}
	if verify {
		databaseSHA, databaseErr := checksum(databaseFile)
		dataSHA, dataErr := checksum(dataFile)
		item.Verified = databaseErr == nil && dataErr == nil && databaseSHA == manifest["database_sha256"] && dataSHA == manifest["app_data_sha256"]
		if !item.Verified {
			return Item{}, fmt.Errorf("backup checksum validation failed")
		}
	}
	return item, nil
}

func (s *Service) writeAudit(ctx context.Context, principal auth.Principal, action, resourceID, summary, requestID, ipAddress string) error {
	if s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO audit_logs (user_id, organization_id, action, resource_type, resource_id, summary, request_ip, request_id) VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, 'backup', $4, $5, NULLIF($6, '')::inet, NULLIF($7, ''))`, principal.UserID, organizationID(principal), action, resourceID, summary, ipAddress, requestID)
	if err != nil {
		return fmt.Errorf("write backup audit: %w", err)
	}
	return nil
}

func organizationID(principal auth.Principal) string {
	if principal.Organization == nil {
		return ""
	}
	return principal.Organization.ID
}

func dumpDatabase(ctx context.Context, databaseURL, output string) error {
	if strings.TrimSpace(databaseURL) == "" {
		return fmt.Errorf("database backup is not configured")
	}
	command := exec.CommandContext(ctx, "pg_dump", "--dbname="+databaseURL, "--format=custom", "--compress=6", "--no-owner", "--no-privileges", "--file="+output)
	if err := command.Run(); err != nil {
		return fmt.Errorf("create database dump: %w", err)
	}
	return nil
}

func validateArchive(ctx context.Context, databaseFile, dataFile string) error {
	if err := exec.CommandContext(ctx, "pg_restore", "--list", databaseFile).Run(); err != nil {
		return fmt.Errorf("validate database dump: %w", err)
	}
	file, err := os.Open(dataFile)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("validate attachment archive: %w", err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	for {
		_, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("validate attachment archive: %w", err)
		}
	}
}

func writeDataArchive(dataDir, destination string) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	gzipWriter := gzip.NewWriter(output)
	defer gzipWriter.Close()
	writer := tar.NewWriter(gzipWriter)
	defer writer.Close()
	return filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		if relativePath == "backups" {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(relativePath, "backups"+string(filepath.Separator)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("attachment data cannot include symbolic links: %s", relativePath)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relativePath)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, input)
		closeErr := input.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func checksum(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func parseManifest(path string) (map[string]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(contents), "\n") {
		key, value, found := strings.Cut(line, "=")
		if found && key != "" {
			values[key] = value
		}
	}
	return values, nil
}

func validID(id string) bool {
	if !strings.HasPrefix(id, "netassets-") || len(id) > 80 {
		return false
	}
	for _, character := range id {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' && character != 'T' && character != 'Z' {
			return false
		}
	}
	return true
}

func newID() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("create backup identifier: %w", err)
	}
	return "netassets-" + time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(bytes), nil
}
