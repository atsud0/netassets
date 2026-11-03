package securityworkorder

import (
	"context"
	"strings"
	"testing"

	"netassets/internal/pagination"
)

func TestSecurityBatchTransactionsVersionsAndAudit(test *testing.T) {
	pool, principal, organization, findingID := reviewFixture(test)
	ctx := context.Background()
	service := NewService(pool, test.TempDir())
	if _, err := pool.Exec(ctx, `UPDATE security_findings SET status='pending' WHERE id=$1::uuid`, findingID); err != nil {
		test.Fatal(err)
	}
	finding, err := service.GetFinding(ctx, principal, findingID)
	if err != nil {
		test.Fatal(err)
	}
	items := []BatchItem{{ID: findingID, ExpectedUpdatedAt: &finding.UpdatedAt}, {ID: "00000000-0000-0000-0000-000000000001"}, {ID: "bad-id"}}
	result, err := service.BatchUpdateFindingStatus(ctx, principal, BatchInput{Items: items, Status: "confirmed"}, "batch-findings", "127.0.0.1")
	if err != nil || result.Succeeded != 1 || result.Failed != 2 || result.Items[1].Code != "not_found" {
		test.Fatalf("findings: %+v, %v", result, err)
	}
	result, err = service.BatchUpdateFindingStatus(ctx, principal, BatchInput{Items: []BatchItem{{ID: findingID}}, Status: "pending"}, "batch-missing-version", "127.0.0.1")
	if err != nil || result.Items[0].Code != "version_required" {
		test.Fatalf("missing version: %+v, %v", result, err)
	}
	result, err = service.BatchUpdateFindingStatus(ctx, principal, BatchInput{Items: items[:1], Status: "pending"}, "batch-stale", "127.0.0.1")
	if err != nil || result.Items[0].Code != "edit_conflict" {
		test.Fatalf("stale version: %+v, %v", result, err)
	}
	order, err := service.CreateWorkOrder(ctx, principal, reviewOrder(organization, findingID), "batch-create", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	result, err = service.BatchCancel(ctx, principal, BatchInput{Items: []BatchItem{{ID: order.ID}, items[1]}, Reason: "批量取消保留证据"}, "batch-cancel", "127.0.0.1")
	if err != nil || result.Succeeded != 1 || result.Failed != 1 {
		test.Fatalf("cancel: %+v, %v", result, err)
	}
	result, err = service.BatchCancel(ctx, principal, BatchInput{Items: []BatchItem{{ID: order.ID}}, Reason: "重试"}, "batch-repeat", "127.0.0.1")
	if err != nil || result.Items[0].Code != "invalid_state" {
		test.Fatalf("repeat cancellation: %+v, %v", result, err)
	}
	imported, err := service.ImportFindings(ctx, principal, "", "batch.csv", strings.NewReader("资产类型,资产值,漏洞标题,严重等级,漏洞描述\nip,198.51.100.42,批量待关联,高危,导入测试"), "batch-import", "127.0.0.1")
	if err != nil || imported.PendingCount != 1 {
		test.Fatalf("import: %+v, %v", imported, err)
	}
	page, _ := pagination.Normalize(1, 20)
	candidates, err := service.SearchFindingImportCandidates(ctx, principal, FindingImportCandidateFilter{Status: "pending", Page: page})
	if err != nil || len(candidates.Items) != 1 {
		test.Fatalf("candidates: %+v, %v", candidates, err)
	}
	input := BatchInput{Items: []BatchItem{{ID: candidates.Items[0].ID}}, Reason: "统一忽略原因"}
	result, err = service.BatchIgnoreCandidates(ctx, principal, input, "batch-ignore", "127.0.0.1")
	if err != nil || result.Succeeded != 1 {
		test.Fatalf("ignore: %+v, %v", result, err)
	}
	candidate, err := service.GetFindingImportCandidate(ctx, principal, input.Items[0].ID)
	if err != nil || candidate.Status != "ignored" || candidate.ReviewNote != input.Reason || candidate.ReviewedAt == nil {
		test.Fatalf("ignored detail: %+v, %v", candidate, err)
	}
	result, err = service.BatchIgnoreCandidates(ctx, principal, input, "batch-ignore-repeat", "127.0.0.1")
	if err != nil || result.Items[0].Code != "invalid_state" {
		test.Fatalf("repeat ignore: %+v, %v", result, err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE request_id IN ('batch-findings','batch-cancel','batch-ignore')`).Scan(&count); err != nil {
		test.Fatal(err)
	}
	if count < 3 {
		test.Fatalf("missing batch item audit logs: %d", count)
	}
}
