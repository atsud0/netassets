package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/config"
)

// Exercise the real assembled router, not a standalone middleware-only router.
// A local fake pg_dump holds the actual backup gate, without dumping any data.
func TestAssembledAPIRoutesRespectBackupWriteGate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	stamp := fmt.Sprint(time.Now().UnixNano())
	var user, role string
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name,must_change_password) VALUES($1,'unused','备份路由门禁回归',false) RETURNING id::text`, "backup_guard_"+stamp).Scan(&user); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DELETE FROM audit_logs WHERE user_id=$1::uuid`, user); err != nil {
			t.Errorf("cleanup own audits: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, user); err != nil {
			t.Errorf("cleanup own user: %v", err)
		}
	})
	tag, err := pool.Exec(ctx, `INSERT INTO roles(role_key,role_name,role_scope,is_system) VALUES('group_admin','集团管理员','group',true) ON CONFLICT DO NOTHING`)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM roles WHERE role_key='group_admin' AND status='enabled'`).Scan(&role); err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 1 {
		t.Cleanup(func() {
			if _, err := pool.Exec(ctx, `DELETE FROM roles WHERE id=$1::uuid`, role); err != nil {
				t.Errorf("cleanup own role: %v", err)
			}
		})
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id,data_scope) VALUES($1::uuid,$2::uuid,'all')`, user, role); err != nil {
		t.Fatal(err)
	}
	token := "local-backup-guard-" + stamp
	hash := sha256.Sum256([]byte(token))
	if _, err := pool.Exec(ctx, `INSERT INTO sessions(user_id,token_hash,expires_at) VALUES($1::uuid,$2,now()+interval '1 hour')`, user, hex.EncodeToString(hash[:])); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "dump-entered")
	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte("#!/bin/sh\n: > \"$NETASSETS_BACKUP_GUARD_MARKER\"\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NETASSETS_BACKUP_GUARD_MARKER", marker)
	// Disable application background writes for this routing test only.
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	cancelWorkers()
	cfg := config.Config{Environment: "test", DatabaseURL: url, DataDir: filepath.Join(dir, "data"), SessionCookieName: "backup_guard_session", SessionTTL: time.Hour, DataQualityAutoScanTimezone: "UTC"}
	handler := NewWithContext(workerCtx, cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	request := func(method, path string, authenticated bool) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		if authenticated {
			req.AddCookie(&http.Cookie{Name: cfg.SessionCookieName, Value: token})
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	if got := request(http.MethodGet, "/api/v1/auth/me", true); got.Code != 200 {
		t.Fatalf("fixture authentication status=%d", got.Code)
	}
	backupCtx, cancelBackup := context.WithCancel(ctx)
	done := make(chan struct{})
	backupResponse := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/backups", nil).WithContext(backupCtx)
	req.AddCookie(&http.Cookie{Name: cfg.SessionCookieName, Value: token})
	go func() { defer close(done); handler.ServeHTTP(backupResponse, req) }()
	defer func() { cancelBackup(); <-done }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-done:
			t.Fatalf("backup did not enter controlled dump, status=%d", backupResponse.Code)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("backup did not enter controlled dump")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodPost, "/api/v1/assets"},
		{http.MethodPut, "/api/v1/auth/profile"},
		{http.MethodPatch, "/api/v1/security-findings/11111111-1111-4111-8111-111111111111"},
		{http.MethodDelete, "/api/v1/backups/not-a-backup"},
		{http.MethodGet, "/api/v1/data-quality-rules/scan"},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			out := request(tc.method, tc.path, false)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if out.Code != 503 || body.Error.Code != "backup_in_progress" {
				t.Errorf("assembled API during backup status=%d code=%s, want 503 backup_in_progress", out.Code, body.Error.Code)
			}
		})
	}
	if got := request(http.MethodPost, "/api/v1/assets", true); got.Code != 503 {
		t.Errorf("authenticated write during backup status=%d, want 503", got.Code)
	}
	if got := request(http.MethodGet, "/api/v1/backups", true); got.Code != 200 {
		t.Errorf("read-only API status=%d, want 200", got.Code)
	}
	if got := request(http.MethodGet, "/health/live", false); got.Code != 200 {
		t.Errorf("health status=%d, want 200", got.Code)
	}
	if got := request(http.MethodPost, "/api/v1/backups", true); got.Code != 409 {
		t.Errorf("second backup status=%d, want busy 409", got.Code)
	}
	cancelBackup()
	<-done
	if backupResponse.Code != 500 {
		t.Errorf("canceled fake dump status=%d, want 500", backupResponse.Code)
	}
	if got := request(http.MethodPost, "/api/v1/auth/login", false); got.Code != 400 {
		t.Errorf("gate not released after canceled backup: status=%d, want validation 400", got.Code)
	}
}
