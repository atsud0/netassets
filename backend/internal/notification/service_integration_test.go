package notification

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestSecurityOperatorNotificationQueries(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("reset test users: %v", err)
	}

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ('notification_security_operator', 'not-used', '安全运营通知测试')
		RETURNING id::text
	`).Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	principal := auth.Principal{
		UserID: userID,
		Roles:  []string{auth.RoleSecurityOperator},
		Permissions: []string{
			auth.PermissionSecurityFinding,
			auth.PermissionSecurityWorkOrder,
		},
	}
	service := NewService(pool)

	if _, err := service.Summary(ctx, principal); err != nil {
		t.Fatalf("Summary(security operator) error = %v", err)
	}
	page, _ := pagination.Normalize(1, 20)
	if _, _, err := service.Search(ctx, principal, SearchInput{Page: page}); err != nil {
		t.Fatalf("Search(security operator) error = %v", err)
	}
	if err := service.MarkAllRead(ctx, principal); err != nil {
		t.Fatalf("MarkAllRead(security operator) error = %v", err)
	}
	if err := service.MarkRead(ctx, principal, "missing-notification"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkRead(missing notification) error = %v, want ErrNotFound", err)
	}
}
