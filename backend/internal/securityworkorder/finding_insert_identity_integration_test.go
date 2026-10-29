package securityworkorder

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
)

// Own fixtures live in one rolled-back transaction: never truncate shared test
// data, including evidence left by earlier failed capacity imports.
func TestFindingIdentityConflictPreservesAuditAndNotificationCounts(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stamp := time.Now().Format("20060102150405.000000000")
	queryID := func(sql string, args ...any) string {
		t.Helper()
		var id string
		if err := tx.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	groupID := queryID(`INSERT INTO organizations (organization_code,organization_name,organization_type) VALUES ($1,'索引回归集团','group') RETURNING id::text`, "IDX_G_"+stamp)
	orgID := queryID(`INSERT INTO organizations (organization_code,organization_name,organization_type,parent_id) VALUES ($1,'索引回归组织','subsidiary',$2::uuid) RETURNING id::text`, "IDX_O_"+stamp, groupID)
	otherOrgID := queryID(`INSERT INTO organizations (organization_code,organization_name,organization_type,parent_id) VALUES ($1,'其他索引回归组织','subsidiary',$2::uuid) RETURNING id::text`, "IDX_OTHER_"+stamp, groupID)
	systemID := queryID(`INSERT INTO business_systems (system_code,system_name,owner_organization_id,system_status) VALUES ($1,'索引回归系统',$2::uuid,'running') RETURNING id::text`, "IDX_SYS_"+stamp, orgID)
	userID := queryID(`INSERT INTO users (username,password_hash,display_name) VALUES ($1,'not-used','索引回归用户') RETURNING id::text`, "idx_"+stamp)
	assetID := queryID(`INSERT INTO assets (asset_type,asset_value,normalized_value) VALUES ('domain',$1,$1) RETURNING id::text`, "idx-"+stamp+".example.com")
	exec(`INSERT INTO asset_organizations (asset_id,organization_id,relationship_type,is_primary,owner_user_id) VALUES ($1::uuid,$2::uuid,'owner',true,$3::uuid)`, assetID, orgID, userID)
	batchID := queryID(`INSERT INTO security_test_batches (batch_code,batch_name,test_type,status,provider,authorization_note,start_at,end_at) VALUES ($1,'索引回归批次','scan','authorized','隔离验证','已授权',now(),now()+interval '1 day') RETURNING id::text`, "IDX_B_"+stamp)
	exec(`INSERT INTO security_test_scope_assets (batch_id,asset_id,asset_type_snapshot,asset_value_snapshot,organization_id,organization_code_snapshot,organization_name_snapshot,network_scope_snapshot,environment_snapshot,importance_snapshot)
 SELECT $1::uuid,a.id,a.asset_type,a.asset_value,o.id,o.organization_code,o.organization_name,a.network_scope,a.environment,a.importance FROM assets a CROSS JOIN organizations o WHERE a.id=$2::uuid AND o.id=$3::uuid`, batchID, assetID, orgID)
	if err := validateFindingReferences(ctx, tx, orgID, assetID, systemID); err != nil {
		t.Fatalf("valid owned asset: %v", err)
	}
	if err := validateFindingReferences(ctx, tx, otherOrgID, assetID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other organization cannot own this asset: %v", err)
	}
	if err := validateFindingReferences(ctx, tx, otherOrgID, "", systemID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other organization cannot own this system: %v", err)
	}
	if err := validateFindingReferences(ctx, tx, groupID, assetID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("group is not a responsible organization: %v", err)
	}
	if err := validateFindingBatchScope(ctx, tx, batchID, orgID, assetID); err != nil {
		t.Fatalf("authorized scope: %v", err)
	}
	if err := validateFindingBatchScope(ctx, tx, batchID, groupID, assetID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong organization scope: %v", err)
	}
	exec(`UPDATE security_test_batches SET status='draft' WHERE id=$1::uuid`, batchID)
	if err := validateFindingBatchScope(ctx, tx, batchID, orgID, assetID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unauthorized batch: %v", err)
	}
	exec(`UPDATE security_test_batches SET status='authorized' WHERE id=$1::uuid`, batchID)
	service := NewService(pool)
	principal := auth.Principal{UserID: userID}
	for _, batch := range []string{"", batchID} {
		input := FindingInput{Title: "Indexed Finding", Severity: "high", Status: "pending", AssigneeOrganizationID: orgID, AssetID: assetID, TestBatchID: batch, Description: "保持审计与通知", Source: "manual"}
		if _, _, err := service.createFindingInTx(ctx, tx, principal, input, "identity-"+stamp, "127.0.0.1"); err != nil {
			t.Fatal(err)
		}
		// Use a savepoint so a real 23505 rolls back only the attempted duplicate.
		child, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		input.Title = "  INDEXED finding  "
		_, _, insertErr := service.createFindingInTx(ctx, child, principal, input, "identity-"+stamp, "127.0.0.1")
		_ = child.Rollback(ctx)
		if !errors.Is(insertErr, ErrConflict) {
			t.Fatalf("case/trim duplicate must be a conflict: %v", insertErr)
		}
	}
	var findings, audits, notificationItems int
	for _, check := range []struct {
		sql  string
		dest *int
	}{
		{`SELECT count(*) FROM security_findings WHERE assignee_organization_id=$1::uuid`, &findings},
		{`SELECT count(*) FROM audit_logs WHERE organization_id=$1::uuid AND action='security_finding.create'`, &audits},
		{`SELECT COALESCE(sum(item_count),0)::int FROM notification_events WHERE target_organization_id=$1::uuid AND event_type='security_finding.created_on_asset' AND target_user_id IS NOT NULL`, &notificationItems},
	} {
		if err := tx.QueryRow(ctx, check.sql, orgID).Scan(check.dest); err != nil {
			t.Fatal(err)
		}
	}
	if findings != 2 || audits != 2 || notificationItems != 2 {
		t.Fatalf("two successes and two duplicates: findings=%d audits=%d notification items=%d", findings, audits, notificationItems)
	}
}
