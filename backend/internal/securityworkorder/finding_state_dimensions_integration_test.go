package securityworkorder

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// 00069 split the finding lifecycle into three independent dimensions while
// keeping the legacy status column intact. The triggers are the only thing
// keeping asset_link_status and remediation_status truthful, so this test pins
// their transitions against a migrated database rather than trusting the
// migration text.
func TestFindingStateDimensionsTrackAssetLinkAndRemediation(t *testing.T) {
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
		t.Fatalf("reset finding dimension fixtures: %v", err)
	}

	var groupID, organizationID, userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('FINDING_DIM_GROUP','漏洞状态维度测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid,'FINDING_DIM_SUB','漏洞状态维度测试子公司','subsidiary') RETURNING id::text`, groupID).Scan(&organizationID); err != nil {
		t.Fatalf("insert organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('finding_dim_operator','not-used','漏洞状态维度测试人员') RETURNING id::text`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	var businessSystemID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO business_systems (system_code,system_name,owner_organization_id,system_status,system_level,internet_exposure,created_by,updated_by)
		VALUES ('FINDING_DIM_SYS','状态维度测试系统',$1::uuid,'running','normal','unknown',$2::uuid,$2::uuid)
		RETURNING id::text`, organizationID, userID).Scan(&businessSystemID); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO business_system_data_security_profiles (business_system_id) VALUES ($1::uuid)`, businessSystemID); err != nil {
		t.Fatalf("insert data security profile: %v", err)
	}
	var assetID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO assets (asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by)
		VALUES ('ip','192.0.2.99','192.0.2.99','状态维度测试资产','production','private','active','normal','manual',$1::uuid,$1::uuid)
		RETURNING id::text`, userID).Scan(&assetID); err != nil {
		t.Fatalf("insert asset: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary)
		VALUES ($1::uuid,$2::uuid,'owner',true)`, assetID, organizationID); err != nil {
		t.Fatalf("assign asset: %v", err)
	}
	var draftID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO inventory_asset_drafts (
			organization_id,operation,asset_type,asset_value,normalized_value,asset_name,
			environment,network_scope,status,importance,source,draft_status,created_by,updated_by,version,assigned_to,last_edited_by
		) VALUES (
			$1::uuid,'create','ip','192.0.2.100','192.0.2.100','待发布工作版本',
			'production','private','active','normal','finding_import','editing',$2::uuid,$2::uuid,1,$2::uuid,$2::uuid
		) RETURNING id::text`, organizationID, userID).Scan(&draftID); err != nil {
		t.Fatalf("insert working draft: %v", err)
	}

	insertFinding := func(code string) string {
		t.Helper()
		var findingID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO security_findings (
				finding_code,title,severity,status,assignee_organization_id,description,source,created_by,updated_by
			) VALUES ($1::text,'状态维度测试漏洞-'||$1::text,'high','pending',$2::uuid,'用于验证三维状态语义','import',$3::uuid,$3::uuid)
			RETURNING id::text`, code, organizationID, userID).Scan(&findingID); err != nil {
			t.Fatalf("insert finding %s: %v", code, err)
		}
		return findingID
	}
	linkStatus := func(findingID string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT asset_link_status FROM security_findings WHERE id=$1::uuid`, findingID).Scan(&value); err != nil {
			t.Fatalf("read asset_link_status: %v", err)
		}
		return value
	}
	remediationStatus := func(findingID string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT remediation_status FROM security_findings WHERE id=$1::uuid`, findingID).Scan(&value); err != nil {
			t.Fatalf("read remediation_status: %v", err)
		}
		return value
	}

	t.Run("new finding defaults to unlinked and not started", func(t *testing.T) {
		findingID := insertFinding("FINDING-DIM-DEFAULT")
		if got := linkStatus(findingID); got != "unlinked" {
			t.Fatalf("asset_link_status = %q, want unlinked", got)
		}
		if got := remediationStatus(findingID); got != "not_started" {
			t.Fatalf("remediation_status = %q, want not_started", got)
		}
	})

	t.Run("attaching a live asset moves the link dimension to live", func(t *testing.T) {
		findingID := insertFinding("FINDING-DIM-LIVE")
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET asset_id=$2::uuid WHERE id=$1::uuid`, findingID, assetID); err != nil {
			t.Fatalf("attach asset: %v", err)
		}
		if got := linkStatus(findingID); got != "live" {
			t.Fatalf("asset_link_status = %q, want live", got)
		}
	})

	t.Run("soft deleting the asset moves the link dimension to retired", func(t *testing.T) {
		findingID := insertFinding("FINDING-DIM-RETIRED")
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET asset_id=$2::uuid WHERE id=$1::uuid`, findingID, assetID); err != nil {
			t.Fatalf("attach asset: %v", err)
		}
		secondAssetID := insertRetiredAsset(t, pool, userID, organizationID)
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET asset_id=$2::uuid WHERE id=$1::uuid`, findingID, secondAssetID); err != nil {
			t.Fatalf("attach retired asset: %v", err)
		}
		if got := linkStatus(findingID); got != "retired" {
			t.Fatalf("asset_link_status = %q, want retired", got)
		}
	})

	t.Run("a working version moves the link dimension to pending", func(t *testing.T) {
		findingID := insertFinding("FINDING-DIM-PENDING")
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET pending_asset_draft_id=$2::uuid WHERE id=$1::uuid`, findingID, draftID); err != nil {
			t.Fatalf("attach working version: %v", err)
		}
		if got := linkStatus(findingID); got != "pending" {
			t.Fatalf("asset_link_status = %q, want pending", got)
		}
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET pending_asset_draft_id=NULL WHERE id=$1::uuid`, findingID); err != nil {
			t.Fatalf("detach working version: %v", err)
		}
		if got := linkStatus(findingID); got != "unlinked" {
			t.Fatalf("asset_link_status after detach = %q, want unlinked", got)
		}
	})

	t.Run("remediation tasks drive the remediation dimension only", func(t *testing.T) {
		findingID := insertFinding("FINDING-DIM-REMEDIATION")
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET asset_id=$2::uuid WHERE id=$1::uuid`, findingID, assetID); err != nil {
			t.Fatalf("attach asset: %v", err)
		}
		workOrderID := insertWorkOrder(t, pool, organizationID, userID)
		grantID := insertGrant(t, pool, userID, organizationID, businessSystemID, userID)
		taskID := insertRemediationTask(t, pool, workOrderID, findingID, organizationID, businessSystemID, userID, grantID)

		steps := []struct {
			status string
			want   string
		}{
			{"pending_acceptance", "pending_acceptance"},
			{"in_progress", "in_progress"},
			{"pending_org_review", "pending_org_review"},
			{"pending_security_review", "pending_security_review"},
			{"completed", "completed"},
		}
		for _, step := range steps {
			if _, err := pool.Exec(ctx, `UPDATE remediation_tasks SET status=$2 WHERE id=$1::uuid`, taskID, step.status); err != nil {
				t.Fatalf("advance remediation task to %s: %v", step.status, err)
			}
			if got := remediationStatus(findingID); got != step.want {
				t.Fatalf("remediation_status after %s = %q, want %q", step.status, got, step.want)
			}
		}

		// Cancelling the only task must fall back to not_started: the dimension
		// summarises live remediation work, not historical attempts.
		if _, err := pool.Exec(ctx, `UPDATE remediation_tasks SET status='cancelled' WHERE id=$1::uuid`, taskID); err != nil {
			t.Fatalf("cancel remediation task: %v", err)
		}
		if got := remediationStatus(findingID); got != "not_started" {
			t.Fatalf("remediation_status after cancel = %q, want not_started", got)
		}

		// The legacy vulnerability status must be untouched by the remediation
		// dimension; the two must not be conflated.
		var legacyStatus string
		if err := pool.QueryRow(ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, findingID).Scan(&legacyStatus); err != nil {
			t.Fatalf("read legacy status: %v", err)
		}
		if legacyStatus != "pending" {
			t.Fatalf("legacy status = %q, want pending: the remediation dimension must not rewrite the vulnerability status", legacyStatus)
		}
		if got := linkStatus(findingID); got != "live" {
			t.Fatalf("asset_link_status = %q, want live: the remediation dimension must not touch asset association", got)
		}
	})

	t.Run("invalid dimension values are rejected by the database", func(t *testing.T) {
		findingID := insertFinding("FINDING-DIM-VALIDATION")
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET asset_link_status='not-a-status' WHERE id=$1::uuid`, findingID); err == nil {
			t.Fatal("asset_link_status accepted a value outside the documented set")
		}
		if _, err := pool.Exec(ctx, `UPDATE security_findings SET remediation_status='not-a-status' WHERE id=$1::uuid`, findingID); err == nil {
			t.Fatal("remediation_status accepted a value outside the documented set")
		}
	})
}

func insertRetiredAsset(t *testing.T, pool *pgxpool.Pool, userID, organizationID string) string {
	t.Helper()
	var assetID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO assets (asset_type,asset_value,normalized_value,asset_name,environment,network_scope,status,importance,source,created_by,updated_by,deleted_at)
		VALUES ('ip','192.0.2.101','192.0.2.101','已下线测试资产','production','private','archived','normal','manual',$1::uuid,$1::uuid,now())
		RETURNING id::text`, userID).Scan(&assetID); err != nil {
		t.Fatalf("insert retired asset: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary)
		VALUES ($1::uuid,$2::uuid,'owner',true)`, assetID, organizationID); err != nil {
		t.Fatalf("assign retired asset: %v", err)
	}
	return assetID
}

func insertWorkOrder(t *testing.T, pool *pgxpool.Pool, organizationID, userID string) string {
	t.Helper()
	var workOrderID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO security_work_orders (
			work_order_no,title,assignee_organization_id,severity,status,description,
			remediation_requirement,due_at,created_by,updated_by
		) VALUES ('WO-FINDING-DIM','状态维度测试工单',$1::uuid,'high','issued','用于验证整改状态维度','完成整改',now()+interval '7 days',$2::uuid,$2::uuid)
		RETURNING id::text`, organizationID, userID).Scan(&workOrderID); err != nil {
		t.Fatalf("insert work order: %v", err)
	}
	return workOrderID
}

func insertGrant(t *testing.T, pool *pgxpool.Pool, userID, organizationID, businessSystemID, sponsorID string) string {
	t.Helper()
	var grantID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO remediation_grants (
			user_id,organization_id,business_system_id,participant_type,
			status,valid_from,valid_until,sponsor_user_id
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'internal','enabled',now(),now()+interval '30 days',$4::uuid)
		RETURNING id::text`, userID, organizationID, businessSystemID, sponsorID).Scan(&grantID); err != nil {
		t.Fatalf("insert grant: %v", err)
	}
	return grantID
}

func insertRemediationTask(t *testing.T, pool *pgxpool.Pool, workOrderID, findingID, organizationID, businessSystemID, userID, grantID string) string {
	t.Helper()
	var taskID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO remediation_tasks (
			task_no,work_order_id,finding_id,organization_id,business_system_id,
			assignee_user_id,grant_id,status,due_at,original_due_at,target_environment,created_by
		) VALUES ('RT-FINDING-DIM',$1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'pending_acceptance',
		          now()+interval '7 days',now()+interval '7 days','production',$5::uuid)
		RETURNING id::text`, workOrderID, findingID, organizationID, businessSystemID, userID, grantID).Scan(&taskID); err != nil {
		t.Fatalf("insert remediation task: %v", err)
	}
	return taskID
}
