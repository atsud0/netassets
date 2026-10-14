package securityworkorder

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// "待处理" on the summary cards used to be the only backlog number, which made
// one figure stand for five different queues. These tests pin the declared
// 口径: pending counts the vulnerability lifecycle only, each waiting dimension
// is counted separately, and severity counters cover the whole visible scope
// rather than the pending subset.
func TestFindingSummarySeparatesLifecycleFromWaitingDimensions(t *testing.T) {
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
		t.Fatalf("reset summary fixtures: %v", err)
	}

	var groupID, organizationID, userID, assetID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code,organization_name,organization_type)
		VALUES ('SUM_GROUP','汇总口径测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id,organization_code,organization_name,organization_type)
		VALUES ($1::uuid,'SUM_SUB','汇总口径测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('finding_summary_manager','not-used','汇总口径测试人员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key,permission_name) VALUES
			('asset.manage','管理网络资产'), ('system.manage','管理业务系统'), ('security.finding.manage','管理安全发现')
		ON CONFLICT (permission_key) DO NOTHING`); err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	var roleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key,role_name,role_scope,description,is_system,status)
		VALUES ('finding_summary_role','汇总口径角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text`).Scan(&roleID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id,permission_key)
		SELECT $1::uuid, permission_key FROM permissions
		WHERE permission_key IN ('asset.manage','system.manage','security.finding.manage') ON CONFLICT DO NOTHING`, roleID); err != nil {
		t.Fatalf("grant permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'organization')`, userID, roleID, organizationID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO assets (asset_type, asset_value, normalized_value, environment, network_scope, status, importance, source)
		VALUES ('domain','summary.example.com','summary.example.com','production','public','active','normal','manual')
		RETURNING id::text`).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary)
		VALUES ($1::uuid,$2::uuid,'owner',true)`, assetID, organizationID); err != nil {
		t.Fatalf("assign asset organization: %v", err)
	}

	businessSystemID := insertSummaryBusinessSystem(t, pool, organizationID, userID)
	workOrderID := insertSummaryWorkOrder(t, pool, organizationID, userID)
	grantID := insertSummaryGrant(t, pool, userID, organizationID, businessSystemID)

	insert := func(t *testing.T, code, severity, status, remediationStatus string, withAsset bool, riskUntil any) {
		t.Helper()
		assetValue := any(nil)
		systemValue := any(nil)
		if withAsset {
			assetValue = assetID
		} else {
			// A finding without an asset is only visible to somebody who manages
			// business systems; that is exactly how 待关联漏洞候选 are reached.
			systemValue = businessSystemID
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO security_findings (
				finding_code, title, severity, status, assignee_organization_id, asset_id, business_system_id,
				risk_accepted_until, description, source, created_by, updated_by
			) VALUES ($1,$2,$3,$4,$5::uuid,$6::uuid,$7::uuid,$8::timestamptz,'汇总口径测试发现','manual',$9::uuid,$9::uuid)
			ON CONFLICT (finding_code) DO NOTHING`,
			code, code, severity, status, organizationID, assetValue, systemValue, riskUntil, userID); err != nil {
			t.Fatalf("insert finding %s: %v", code, err)
		}
		// The 00069/00071 triggers own asset_link_status and remediation_status;
		// they are written the way production writes them, through remediation
		// tasks, so this test measures real bookkeeping rather than hand-set values.
		if remediationStatus != "not_started" {
			if _, err := pool.Exec(ctx, `
				INSERT INTO remediation_tasks (
					task_no,work_order_id,finding_id,organization_id,business_system_id,
					assignee_user_id,grant_id,status,due_at,original_due_at,target_environment,created_by
				) VALUES ($1,$2::uuid,(SELECT id FROM security_findings WHERE finding_code=$3),$4::uuid,$5::uuid,$6::uuid,$7::uuid,$8,
				          now()+interval '7 days',now()+interval '7 days','production',$6::uuid)`,
				"SUM-TASK-"+code, workOrderID, code, organizationID, businessSystemID, userID, grantID, remediationStatus); err != nil {
				t.Fatalf("insert remediation task for %s: %v", code, err)
			}
		}
	}
	insert(t, "SUM-PENDING-CRITICAL", "critical", "pending", "not_started", true, nil)
	insert(t, "SUM-PENDING-HIGH", "high", "pending", "not_started", true, nil)
	insert(t, "SUM-CONFIRMED-MEDIUM", "medium", "confirmed", "not_started", true, nil)
	insert(t, "SUM-UNLINKED", "medium", "pending", "not_started", false, nil)
	insert(t, "SUM-ACCEPTANCE", "low", "in_progress", "pending_acceptance", true, nil)
	insert(t, "SUM-ORG-REVIEW", "low", "in_progress", "pending_org_review", true, nil)
	insert(t, "SUM-SEC-REVIEW", "low", "in_progress", "pending_security_review", true, nil)
	insert(t, "SUM-RISK-OK", "low", "risk_accepted", "not_started", true, "2099-01-01T00:00:00Z")
	insert(t, "SUM-RISK-EXPIRED", "low", "risk_accepted", "not_started", true, "2000-01-01T00:00:00Z")
	// Record where the expired acceptance came from, so expiry restores a known
	// lifecycle state instead of the 'confirmed' fallback.
	if _, err := pool.Exec(ctx, `
		UPDATE security_findings SET risk_accepted_from_status='pending'
		WHERE finding_code='SUM-RISK-EXPIRED'`); err != nil {
		t.Fatalf("record previous acceptance status: %v", err)
	}

	service := NewService(pool, t.TempDir())
	manager := auth.Principal{UserID: userID, Permissions: []string{"asset.manage", "system.manage", "security.finding.manage"}}
	stats, err := service.SummarizeFindings(ctx, manager, "")
	if err != nil {
		t.Fatalf("SummarizeFindings error = %v", err)
	}
	if stats.Total != 9 {
		t.Fatalf("total = %d, want 9; stats=%+v", stats.Total, stats)
	}
	// The expired acceptance is restored to its prior lifecycle state (pending)
	// before the summary is read, so 待处理 legitimately grows by one and the
	// item is still reported under the expired-acceptance dimension.
	if stats.Pending != 4 {
		t.Fatalf("pending = %d, want 4 (3 lifecycle pending + 1 restored from an expired acceptance); stats=%+v", stats.Pending, stats)
	}
	if stats.Critical != 1 || stats.High != 1 || stats.Medium != 2 || stats.Low != 5 {
		t.Fatalf("severity counters = critical %d high %d medium %d low %d, want 1/1/2/5", stats.Critical, stats.High, stats.Medium, stats.Low)
	}
	if stats.PendingAssetLink != 1 {
		t.Fatalf("pending_asset_link = %d, want 1; stats=%+v", stats.PendingAssetLink, stats)
	}
	if stats.PendingAcceptance != 1 || stats.PendingOrganizationReview != 1 || stats.PendingSecurityReview != 1 {
		t.Fatalf("remediation dimensions = %d/%d/%d, want 1/1/1; stats=%+v", stats.PendingAcceptance, stats.PendingOrganizationReview, stats.PendingSecurityReview, stats)
	}
	if stats.RiskAccepted != 1 || stats.RiskAcceptedExpired != 1 {
		t.Fatalf("risk counters = accepted %d expired %d, want 1/1; stats=%+v", stats.RiskAccepted, stats.RiskAcceptedExpired, stats)
	}
	// Severity totals must add up to the visible scope, otherwise the cards read
	// as a breakdown of 待处理.
	if stats.Critical+stats.High+stats.Medium+stats.Low != stats.Total {
		t.Fatalf("severity total %d != summary total %d", stats.Critical+stats.High+stats.Medium+stats.Low, stats.Total)
	}
	// 待处理 must never absorb the other queues: it equals the lifecycle counter
	// only, and every other dimension is reported separately.
	if stats.Pending == stats.Total {
		t.Fatal("pending equals total; the lifecycle counter is swallowing other dimensions")
	}
}

func insertSummaryBusinessSystem(t *testing.T, pool *pgxpool.Pool, organizationID, userID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO business_systems (system_code,system_name,owner_organization_id,system_status,system_level,internet_exposure,created_by,updated_by)
		VALUES ('SUM_SYS','汇总口径测试系统',$1::uuid,'running','normal','unknown',$2::uuid,$2::uuid)
		RETURNING id::text`, organizationID, userID).Scan(&id); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO business_system_data_security_profiles (business_system_id) VALUES ($1::uuid)`, id); err != nil {
		t.Fatalf("insert data security profile: %v", err)
	}
	return id
}

func insertSummaryWorkOrder(t *testing.T, pool *pgxpool.Pool, organizationID, userID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO security_work_orders (
			work_order_no,title,assignee_organization_id,severity,status,description,
			remediation_requirement,due_at,created_by,updated_by
		) VALUES ('WO-SUM-SEMANTICS','汇总口径测试工单',$1::uuid,'high','issued','汇总口径测试','完成整改',now()+interval '7 days',$2::uuid,$2::uuid)
		RETURNING id::text`, organizationID, userID).Scan(&id); err != nil {
		t.Fatalf("insert work order: %v", err)
	}
	return id
}

func insertSummaryGrant(t *testing.T, pool *pgxpool.Pool, userID, organizationID, businessSystemID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO remediation_grants (
			user_id,organization_id,business_system_id,participant_type,
			status,valid_from,valid_until,sponsor_user_id
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'internal','enabled',now(),now()+interval '30 days',$1::uuid)
		RETURNING id::text`, userID, organizationID, businessSystemID).Scan(&id); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	return id
}
