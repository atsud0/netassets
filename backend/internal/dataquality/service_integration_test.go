package dataquality

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

func TestCreateAndUpdateExpiryRule(t *testing.T) {
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

	stamp := time.Now().UTC().Format("20060102150405.000000000")
	username := "dataquality_expiry_" + strings.NewReplacer(".", "", "-", "").Replace(stamp)
	ruleName := "资产有效期集成测试_" + stamp
	requestID := "dataquality-expiry-integration-" + stamp

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users(username,password_hash,display_name)
		VALUES($1,'not-used','资产有效期规则集成测试')
		RETURNING id::text
	`, username).Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM audit_logs WHERE request_id=$1`, requestID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM data_quality_rules WHERE rule_name=$1`, ruleName)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1::uuid`, userID)
	})

	service := NewService(pool)
	principal := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}}
	input := Input{
		RuleName:          ruleName,
		Description:       "验证 expiry 规则写入时的 PostgreSQL 参数类型推断",
		ResourceType:      "asset",
		RuleType:          "expiry",
		TargetField:       "expires_at",
		Severity:          "warning",
		ExpiryWarningDays: 0,
		Conditions: []RuleCondition{
			{Field: "status", Operator: "eq", Values: []string{"active"}},
		},
		IsEnabled: true,
		SortOrder: 99999,
	}

	created, err := service.Create(ctx, principal, input, requestID, "127.0.0.1")
	if err != nil {
		t.Fatalf("Create(expiry rule) error = %v", err)
	}
	if created.RuleType != "expiry" || created.TargetField != "expires_at" || created.ExpiryWarningDays != 0 {
		t.Fatalf("created expiry rule = %+v", created)
	}

	input.ExpiryWarningDays = 30
	updated, err := service.Update(ctx, principal, created.ID, input, requestID, "127.0.0.1")
	if err != nil {
		t.Fatalf("Update(expiry rule) error = %v", err)
	}
	if updated.ExpiryWarningDays != 30 {
		t.Fatalf("updated expiry warning days = %d, want 30", updated.ExpiryWarningDays)
	}
}
