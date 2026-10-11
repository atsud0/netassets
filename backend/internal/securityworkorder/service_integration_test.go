package securityworkorder

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

func TestOrganizationAssetManagersCanSeePreWorkOrderFindings(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset security finding fixtures: %v", err)
	}

	var organizationID, assetManagerID, systemManagerID, assetRoleID, systemRoleID, assetID, findingID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('FINDING_SUB', '安全发现测试子公司', 'subsidiary') RETURNING id::text
	`).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, display_name) VALUES ('finding_asset_manager', 'not-used', '资产管理员') RETURNING id::text`).Scan(&assetManagerID); err != nil {
		t.Fatalf("insert asset manager: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users (username, password_hash, display_name) VALUES ('finding_system_manager', 'not-used', '系统管理员') RETURNING id::text`).Scan(&systemManagerID); err != nil {
		t.Fatalf("insert system manager: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key, permission_name) VALUES
			('asset.manage', '管理网络资产'), ('system.manage', '管理业务系统')
		ON CONFLICT (permission_key) DO NOTHING
	`); err != nil {
		t.Fatalf("insert ledger permissions: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
		VALUES ('finding_asset_manager_role', '资产管理员测试角色', 'organization', '', false, 'enabled')
		ON CONFLICT (role_key) DO UPDATE SET role_name = EXCLUDED.role_name, status = EXCLUDED.status
		RETURNING id::text
	`).Scan(&assetRoleID); err != nil {
		t.Fatalf("insert asset manager role: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
		VALUES ('finding_system_manager_role', '系统管理员测试角色', 'organization', '', false, 'enabled')
		ON CONFLICT (role_key) DO UPDATE SET role_name = EXCLUDED.role_name, status = EXCLUDED.status
		RETURNING id::text
	`).Scan(&systemRoleID); err != nil {
		t.Fatalf("insert system manager role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key) VALUES
			($1::uuid, 'asset.manage'), ($2::uuid, 'system.manage')
		ON CONFLICT DO NOTHING
	`, assetRoleID, systemRoleID); err != nil {
		t.Fatalf("grant finding visibility permissions: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope) VALUES
			($3::uuid, $1::uuid, $5::uuid, 'organization'),
			($4::uuid, $2::uuid, $5::uuid, 'organization')
	`, assetRoleID, systemRoleID, assetManagerID, systemManagerID, organizationID); err != nil {
		t.Fatalf("assign finding visibility roles: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO assets (asset_type, asset_value, normalized_value, asset_name, environment, network_scope, status, importance, source)
		VALUES ('domain', 'finding.example.com', 'finding.example.com', '安全发现测试资产', 'production', 'public', 'active', 'normal', 'manual')
		RETURNING id::text
	`).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary)
		VALUES ($1::uuid, $2::uuid, 'owner', true)
	`, assetID, organizationID); err != nil {
		t.Fatalf("assign asset organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_findings (
			finding_code, title, severity, status, assignee_organization_id, asset_id,
			description, source, created_by, updated_by
		) VALUES ('F-PRE-WORK-ORDER', '工单下发前安全发现', 'high', 'pending', $1::uuid, $2::uuid,
		          '用于验证子公司资产管理员可在工单下发前查看。', 'manual', $3::uuid, $3::uuid)
		RETURNING id::text
	`, organizationID, assetID, assetManagerID).Scan(&findingID); err != nil {
		t.Fatalf("insert security finding: %v", err)
	}

	service := NewService(pool, t.TempDir())
	page, _ := pagination.Normalize(1, 20)
	assetManager := auth.Principal{UserID: assetManagerID, Permissions: []string{"asset.manage"}}
	result, err := service.SearchFindings(ctx, assetManager, organizationID, "", page)
	if err != nil {
		t.Fatalf("SearchFindings(asset manager) error = %v", err)
	}
	if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != findingID {
		t.Fatalf("asset manager findings = total %d items %+v, want pre-work-order finding", result.Total, result.Items)
	}
	if _, err := service.GetFinding(ctx, assetManager, findingID); err != nil {
		t.Fatalf("GetFinding(asset manager) error = %v", err)
	}

	systemManager := auth.Principal{UserID: systemManagerID, Permissions: []string{"system.manage"}}
	systemResult, err := service.SearchFindings(ctx, systemManager, organizationID, "", page)
	if err != nil {
		t.Fatalf("SearchFindings(system-only manager) error = %v", err)
	}
	if systemResult.Total != 0 || len(systemResult.Items) != 0 {
		t.Fatalf("system-only manager findings = total %d items %d, want asset finding hidden", systemResult.Total, len(systemResult.Items))
	}
	if _, err := service.GetFinding(ctx, systemManager, findingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetFinding(system-only manager) error = %v, want ErrNotFound", err)
	}
}
