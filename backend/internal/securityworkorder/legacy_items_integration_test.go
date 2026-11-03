package securityworkorder

import (
	"context"
	"errors"
	"strings"
	"testing"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestLegacyResolvedOrganizationItemRequiresExplicitResubmission(t *testing.T) {
	pool, group, organization, findingID := reviewFixture(t)
	ctx := context.Background()
	service := NewService(pool, t.TempDir())
	user := p03InsertUser(t, pool, "legacy_executor", "历史工单执行人")
	p03AssignRole(t, pool, user, "organization_admin", organization, "organization")
	executor := p03Principal(t, pool, user, []string{"organization_admin"})
	order, err := service.CreateWorkOrder(ctx, group, reviewOrder(organization, findingID), "legacy-create", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Issue(ctx, group, order.ID, CommentInput{}, "legacy-issue", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Accept(ctx, executor, order.ID, CommentInput{}, "legacy-accept", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE security_findings SET status='resolved' WHERE id=$1::uuid`, findingID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE security_work_orders SET status='pending_verification',remediation_result='历史整单文字' WHERE id=$1::uuid`, order.ID); err != nil {
		t.Fatal(err)
	}
	page, _ := pagination.Normalize(1, 20)
	current := func() WorkOrderItemVersion {
		t.Helper()
		rows, err := service.SearchWorkOrderFindings(ctx, group, order.ID, page)
		if err != nil || len(rows.Items) != 1 {
			t.Fatalf("read item: %+v %v", rows, err)
		}
		row := rows.Items[0]
		return WorkOrderItemVersion{ID: row.ID, Version: row.Version, FindingUpdatedAt: row.FindingUpdatedAt}
	}
	assertState := func(stage, status string, histories int) {
		t.Helper()
		var actualStage, actualStatus string
		var count int
		err := pool.QueryRow(ctx, `SELECT l.stage,f.status,(SELECT count(*) FROM security_work_order_item_history WHERE work_order_id=l.work_order_id) FROM security_work_order_findings l JOIN security_findings f ON f.id=l.finding_id WHERE l.work_order_id=$1::uuid`, order.ID).Scan(&actualStage, &actualStatus, &count)
		if err != nil || actualStage != stage || actualStatus != status || count != histories {
			t.Fatalf("state: %s/%s history=%d: %v", actualStage, actualStatus, count, err)
		}
	}
	assertState("not_started", "resolved", 0)
	attachment, err := service.AddAttachment(ctx, executor, order.ID, AttachmentInput{Category: "remediation", FileName: "new-proof.txt", ContentType: "text/plain", SizeBytes: 5, Reader: strings.NewReader("proof")}, "legacy-proof", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	original := current()
	request := func(actor auth.Principal, input WorkOrderItemInput) (BatchResult, error) {
		return service.ProcessWorkOrderItems(ctx, actor, order.ID, input, "legacy-process", "127.0.0.1")
	}
	valid := WorkOrderItemInput{Items: []WorkOrderItemVersion{original}, Action: "submit", Note: "重新确认整改结果并提交材料", EvidenceIDs: []string{attachment.ID}}
	for _, testCase := range []struct {
		name    string
		input   WorkOrderItemInput
		code    string
		invalid bool
	}{
		{"missing note", WorkOrderItemInput{Items: valid.Items, Action: "submit", EvidenceIDs: valid.EvidenceIDs}, "", true},
		{"missing evidence", WorkOrderItemInput{Items: valid.Items, Action: "submit", Note: valid.Note}, "", true},
		{"unknown evidence", WorkOrderItemInput{Items: valid.Items, Action: "submit", Note: valid.Note, EvidenceIDs: []string{"00000000-0000-0000-0000-000000000001"}}, "invalid_input", false},
		{"missing version", WorkOrderItemInput{Items: []WorkOrderItemVersion{{ID: findingID}}, Action: "submit", Note: valid.Note, EvidenceIDs: valid.EvidenceIDs}, "version_required", false},
		{"stale version", WorkOrderItemInput{Items: []WorkOrderItemVersion{{ID: findingID, Version: original.Version + 1, FindingUpdatedAt: original.FindingUpdatedAt}}, Action: "submit", Note: valid.Note, EvidenceIDs: valid.EvidenceIDs}, "edit_conflict", false},
		{"progress cannot reset resolved", WorkOrderItemInput{Items: valid.Items, Action: "progress", Note: valid.Note}, "invalid_state", false},
		{"organization cannot pass", WorkOrderItemInput{Items: valid.Items, Action: "pass", Note: valid.Note}, "forbidden", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result, err := request(executor, testCase.input)
			if testCase.invalid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("want invalid: %v", err)
				}
				return
			}
			if err != nil || result.Failed != 1 || result.Items[0].Code != testCase.code {
				t.Fatalf("guard: %+v %v", result, err)
			}
		})
	}
	assertState("not_started", "resolved", 0)
	result, err := request(group, WorkOrderItemInput{Items: valid.Items, Action: "pass", Note: "不能推断历史逐项提交"})
	if err != nil || result.Failed != 1 || result.Items[0].Code != "invalid_state" {
		t.Fatalf("premature review: %+v %v", result, err)
	}
	result, err = request(executor, valid)
	if err != nil || result.Succeeded != 1 {
		t.Fatalf("resubmit: %+v %v", result, err)
	}
	assertState("pending_verification", "resolved", 1)
	result, err = request(executor, valid)
	if err != nil || result.Failed != 1 || result.Items[0].Code != "edit_conflict" {
		t.Fatalf("replay: %+v %v", result, err)
	}
	review := WorkOrderItemInput{Items: []WorkOrderItemVersion{current()}, Action: "pass", Note: "集团重新复测通过"}
	result, err = request(executor, review)
	if err != nil || result.Failed != 1 || result.Items[0].Code != "forbidden" {
		t.Fatalf("self verification: %+v %v", result, err)
	}
	result, err = request(group, review)
	if err != nil || result.Succeeded != 1 {
		t.Fatalf("group review: %+v %v", result, err)
	}
	assertState("verified", "fixed", 2)
	updated, err := service.GetWorkOrder(ctx, group, order.ID)
	if err != nil || updated.Status != "closed" {
		t.Fatalf("aggregate closure: %+v %v", updated, err)
	}
	var action, note string
	var evidence []string
	if err = pool.QueryRow(ctx, `SELECT action,note,evidence_ids::text[] FROM security_work_order_item_history WHERE work_order_id=$1::uuid ORDER BY version LIMIT 1`, order.ID).Scan(&action, &note, &evidence); err != nil || action != "submit" || note != valid.Note || len(evidence) != 1 || evidence[0] != attachment.ID {
		t.Fatalf("explicit new history: %s %s %v: %v", action, note, evidence, err)
	}
}
