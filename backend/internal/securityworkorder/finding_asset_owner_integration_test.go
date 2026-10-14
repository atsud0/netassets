package securityworkorder

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// asset_organizations only guarantees uniqueness on
// (asset_id, organization_id, relationship_type); nothing forces every asset to
// carry a row with is_primary. Registering a finding read that row with
// QueryRow and treated a missing row as a hard error, so an asset without a
// primary organization row made every import candidate that targeted it fail
// with 处理漏洞导入待关联项失败 instead of producing a finding.
//
// A missing primary row means "no single accountable owner", not "the asset is
// broken": the finding must still be recorded and the notification still written,
// just without a targeted recipient.
func TestFindingRegistrationSurvivesAssetWithoutPrimaryOrganization(t *testing.T) {
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
		t.Fatalf("reset fixtures: %v", err)
	}

	var groupID, organizationID, operatorID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code,organization_name,organization_type)
		VALUES ('NO_PRIMARY_GROUP','无主归属测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id,organization_code,organization_name,organization_type)
		VALUES ($1::uuid,'NO_PRIMARY_SUB','无主归属测试子公司','subsidiary') RETURNING id::text`,
		groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('no_primary_operator','not-used','无主归属测试运营人员') RETURNING id::text`).Scan(&operatorID); err != nil {
		t.Fatalf("insert operator: %v", err)
	}
	var assetID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO assets (asset_type,asset_value,normalized_value,asset_name,environment,
		                     network_scope,status,importance,source,created_by,updated_by)
		VALUES ('ip','192.0.2.77','192.0.2.77','无主归属资产','production','public','active','normal','manual',$1::uuid,$1::uuid)
		RETURNING id::text`, operatorID).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	// A secondary, non-primary row only: the asset is owned by the organization,
	// but nothing marks which row is the accountable one.
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary)
		VALUES ($1::uuid,$2::uuid,'shared',false)`, assetID, organizationID); err != nil {
		t.Fatalf("insert secondary organization row: %v", err)
	}
	var primaryRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM asset_organizations WHERE asset_id=$1::uuid AND is_primary`, assetID).Scan(&primaryRows); err != nil {
		t.Fatalf("count primary rows: %v", err)
	}
	if primaryRows != 0 {
		t.Fatalf("fixture must have no primary organization row, found %d", primaryRows)
	}

	principal := auth.Principal{
		UserID: operatorID, Username: "no_primary_operator", DisplayName: "无主归属测试运营人员",
		Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityFinding},
	}
	service := NewService(pool, t.TempDir())
	finding, err := service.CreateFinding(ctx, principal, FindingInput{
		Title: "无主归属资产上的漏洞", Severity: "high", Status: "pending",
		AssigneeOrganizationID: organizationID, AssetID: assetID,
		Description: "用于验证无主归属资产仍可登记安全发现", Source: "manual",
	}, "req-no-primary", "127.0.0.1")
	if err != nil {
		t.Fatalf("registering a finding on an asset without a primary organization row must succeed, got: %v", err)
	}
	if finding.ID == "" || finding.Code == "" {
		t.Fatalf("finding was not persisted: %+v", finding)
	}

	// The notification must still exist; it simply has no targeted recipient.
	var events int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM notification_events
		WHERE resource_type='security_finding' AND resource_id::text=$1::text`, finding.ID).Scan(&events); err != nil {
		t.Fatalf("count notification events: %v", err)
	}
	if events == 0 {
		t.Fatal("no notification event was written for a finding on an asset without a primary organization row")
	}

	// A primary row that does exist must still route the notification to its owner,
	// so the fix did not turn targeted notifications off.
	var ownerID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary,owner_user_id)
		VALUES ($1::uuid,$2::uuid,'owner',true,$3::uuid)
		RETURNING owner_user_id::text`, assetID, organizationID, operatorID).Scan(&ownerID); err != nil {
		t.Fatalf("insert primary organization row: %v", err)
	}
	second, err := service.CreateFinding(ctx, principal, FindingInput{
		Title: "有主归属资产上的漏洞", Severity: "high", Status: "pending",
		AssigneeOrganizationID: organizationID, AssetID: assetID,
		Description: "用于验证有主归属资产仍定向通知负责人", Source: "manual",
	}, "req-with-primary", "127.0.0.1")
	if err != nil {
		t.Fatalf("registering a finding with a primary owner must succeed, got: %v", err)
	}
	var targeted *string
	if err := pool.QueryRow(ctx, `
		SELECT target_user_id::text FROM notification_events
		WHERE resource_type='security_finding' AND resource_id::text=$1::text`, second.ID).Scan(&targeted); err != nil {
		t.Fatalf("read target user: %v", err)
	}
	if targeted == nil || *targeted != operatorID {
		t.Fatalf("expected the notification to target the primary owner %s, got %v", operatorID, targeted)
	}
}
