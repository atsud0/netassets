package dataquality

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestScanPersistenceChunksPreserveWorkflowAndRollback(t *testing.T) {
	databaseURL := os.Getenv("NETASSETS_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	stamp := time.Now().UnixNano()
	var organizationID, batchID string
	if err := tx.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'批量巡检事务测试','subsidiary') RETURNING id::text`, fmt.Sprintf("SCAN_BATCH_%d", stamp)).Scan(&organizationID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO data_quality_scan_batches(organization_id) VALUES($1::uuid) RETURNING id::text`, organizationID).Scan(&batchID); err != nil {
		t.Fatal(err)
	}
	rule := Rule{RuleName: "批量巡检", ResourceType: "asset", TargetField: "owner", Severity: "warning"}
	if err := tx.QueryRow(ctx, `INSERT INTO data_quality_rules(rule_name,resource_type,rule_type,target_field,severity,is_enabled) VALUES($1,'asset','required','owner','warning',false) RETURNING id::text`, fmt.Sprintf("批量巡检_%d", stamp)).Scan(&rule.ID); err != nil {
		t.Fatal(err)
	}
	issues := make([]ScanIssue, 1001)
	for index := range issues {
		issues[index] = ScanIssue{
			ResourceID:   fmt.Sprintf("00000000-0000-4000-8000-%012d", index+1),
			ResourceName: fmt.Sprintf("资源😀_%d", index), OrganizationID: organizationID,
			OrganizationCode: "批量编码", OrganizationName: "批量组织😀",
		}
	}
	if err := persistScanIssues(ctx, tx, rule, issues, batchID, nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM data_quality_scan_batch_issues WHERE batch_id=$1::uuid`, batchID).Scan(&count); err != nil || count != len(issues) {
		t.Fatalf("all chunks linked: count=%d err=%v", count, err)
	}
	statuses := []string{"closed", "in_progress", "pending_review", "exempted", "pending"}
	for index, status := range statuses {
		if _, err := tx.Exec(ctx, `UPDATE data_quality_issues SET status=$3,resolution_type='false_positive',closed_at=now() WHERE rule_id=$1::uuid AND resource_id=$2::uuid`, rule.ID, issues[index].ResourceID, status); err != nil {
			t.Fatal(err)
		}
	}
	var secondBatchID string
	if err := tx.QueryRow(ctx, `INSERT INTO data_quality_scan_batches(organization_id) VALUES($1::uuid) RETURNING id::text`, organizationID).Scan(&secondBatchID); err != nil {
		t.Fatal(err)
	}
	for index := range issues {
		issues[index].ResourceName += "更新"
	}
	if err := persistScanIssues(ctx, tx, rule, issues, secondBatchID, nil); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM data_quality_issues WHERE rule_id=$1::uuid AND occurrence_count=2 AND last_detected_batch_id=$2::uuid AND resource_name LIKE '%更新' AND organization_name='批量组织😀'`, rule.ID, secondBatchID).Scan(&count); err != nil || count != len(issues) {
		t.Fatalf("all issues updated exactly once: count=%d err=%v", count, err)
	}
	for index, originalStatus := range statuses {
		var status string
		var resolution, closedAt *string
		if err := tx.QueryRow(ctx, `SELECT status,resolution_type,closed_at::text FROM data_quality_issues WHERE rule_id=$1::uuid AND resource_id=$2::uuid`, rule.ID, issues[index].ResourceID).Scan(&status, &resolution, &closedAt); err != nil {
			t.Fatal(err)
		}
		if originalStatus == "closed" {
			if status != "pending" || resolution != nil || closedAt != nil {
				t.Fatalf("closed issue not reopened correctly: %s %v %v", status, resolution, closedAt)
			}
		} else if status != originalStatus || resolution == nil || *resolution != "false_positive" || closedAt == nil {
			t.Fatalf("workflow state overwritten for %s", originalStatus)
		}
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM data_quality_scan_batch_issues WHERE batch_id=$1::uuid`, secondBatchID).Scan(&count); err != nil || count != len(issues) {
		t.Fatalf("rescan links: count=%d err=%v", count, err)
	}
	issues[500].ResourceID = "invalid-uuid"
	if err := persistScanIssues(ctx, tx, rule, issues, secondBatchID, nil); err == nil {
		t.Fatal("invalid second chunk must fail")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_issues WHERE rule_id=$1::uuid`, rule.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed scan leaked issues: count=%d err=%v", count, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM data_quality_scan_batches WHERE id=ANY($1::uuid[])`, []string{batchID, secondBatchID}).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed scan leaked batches: count=%d err=%v", count, err)
	}
}
