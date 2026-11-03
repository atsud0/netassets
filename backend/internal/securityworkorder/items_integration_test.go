package securityworkorder

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestOrganizationOrderItemGuardsAndConcurrency(test *testing.T) {
	pool, group, organization, findingID := reviewFixture(test)
	ctx := context.Background()
	service := NewService(pool, test.TempDir())
	order, err := service.CreateWorkOrder(ctx, group, reviewOrder(organization, findingID), "guard-create", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	_, err = service.Issue(ctx, group, order.ID, CommentInput{}, "guard-issue", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	_, err = service.Accept(ctx, group, order.ID, CommentInput{}, "guard-accept", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	page, _ := pagination.Normalize(1, 20)
	current := func() WorkOrderItemVersion {
		test.Helper()
		rows, loadErr := service.SearchWorkOrderFindings(ctx, group, order.ID, page)
		if loadErr != nil {
			test.Fatal(loadErr)
		}
		row := rows.Items[0]
		return WorkOrderItemVersion{ID: row.ID, Version: row.Version, FindingUpdatedAt: row.FindingUpdatedAt}
	}
	process := func(input WorkOrderItemInput) (BatchResult, error) {
		return service.ProcessWorkOrderItems(ctx, group, order.ID, input, "guard-process", "127.0.0.1")
	}
	original := current()
	if _, err = service.Submit(ctx, group, order.ID, SubmitInput{RemediationResult: "旧接口不得绕过逐项提交"}, "guard-legacy", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		test.Fatalf("legacy bypass: %v", err)
	}
	invalid := WorkOrderItemInput{Action: "submit", Note: "材料必须属于本工单", Items: []WorkOrderItemVersion{original}, EvidenceIDs: []string{"00000000-0000-0000-0000-000000000001"}}
	result, err := process(invalid)
	if err != nil || result.Items[0].Code != "invalid_input" {
		test.Fatalf("invalid evidence: %+v %v", result, err)
	}
	if got := current(); got.Version != original.Version {
		test.Fatalf("failed transaction changed version: %+v", got)
	}
	missing := original
	missing.Version = 0
	result, err = process(WorkOrderItemInput{Action: "progress", Note: "缺少版本", Items: []WorkOrderItemVersion{missing}})
	if err != nil || result.Items[0].Code != "version_required" {
		test.Fatalf("missing version: %+v %v", result, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE security_findings SET updated_at=clock_timestamp() WHERE id=$1::uuid`, findingID); err != nil {
		test.Fatal(err)
	}
	result, err = process(WorkOrderItemInput{Action: "progress", Note: "发现已变更", Items: []WorkOrderItemVersion{original}})
	if err != nil || result.Items[0].Code != "edit_conflict" {
		test.Fatalf("stale finding: %+v %v", result, err)
	}
	concurrent := WorkOrderItemInput{Action: "progress", Note: "并发更新只生效一次", Items: []WorkOrderItemVersion{current()}}
	var waiting sync.WaitGroup
	results := make(chan BatchResult, 2)
	failures := make(chan error, 2)
	for attempt := 0; attempt < 2; attempt++ {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			response, callErr := process(concurrent)
			results <- response
			failures <- callErr
		}()
	}
	waiting.Wait()
	close(results)
	close(failures)
	succeeded, failed := 0, 0
	for callErr := range failures {
		if callErr != nil {
			test.Fatal(callErr)
		}
	}
	for response := range results {
		succeeded += response.Succeeded
		failed += response.Failed
	}
	if succeeded != 1 || failed != 1 {
		test.Fatalf("concurrency: %d success %d failed", succeeded, failed)
	}
	if _, err = pool.Exec(ctx, `UPDATE security_work_orders SET execution_mode='individual' WHERE id=$1::uuid`, order.ID); err != nil {
		test.Fatal(err)
	}
	result, err = process(WorkOrderItemInput{Action: "progress", Note: "不得绕过个人任务", Items: []WorkOrderItemVersion{current()}})
	if err != nil || result.Items[0].Code != "conflict" {
		test.Fatalf("individual guard: %+v %v", result, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE security_work_orders SET execution_mode='organization' WHERE id=$1::uuid`, order.ID); err != nil {
		test.Fatal(err)
	}
	_, err = service.Cancel(ctx, group, order.ID, CommentInput{Comment: "验证取消隔离"}, "guard-cancel", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	result, err = process(WorkOrderItemInput{Action: "progress", Note: "取消工单不能修改", Items: []WorkOrderItemVersion{current()}})
	if err != nil || result.Items[0].Code != "invalid_state" {
		test.Fatalf("cancelled guard: %+v %v", result, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM security_work_order_item_history WHERE work_order_id=$1::uuid`, order.ID).Scan(&count); err != nil || count != 1 {
		test.Fatalf("unexpected duplicate/failed history: %d %v", count, err)
	}
}

func TestOrganizationOrderItemsLifecycle(test *testing.T) {
	pool, group, organization, first := reviewFixture(test)
	ctx := context.Background()
	service := NewService(pool, test.TempDir())
	user := p03InsertUser(test, pool, "item_org_admin", "项目组织管理员")
	p03AssignRole(test, pool, user, "organization_admin", organization, "organization")
	executor := p03Principal(test, pool, user, []string{"organization_admin"})
	otherOrg := p03InsertOrg(test, pool, "", "ITEM_OTHER", "Other org", "subsidiary")
	otherUser := p03InsertUser(test, pool, "item_other_admin", "外组织管理员")
	p03AssignRole(test, pool, otherUser, "organization_admin", otherOrg, "organization")
	outsider := p03Principal(test, pool, otherUser, []string{"organization_admin"})
	ids := []string{first}
	for _, title := range []string{"Item two", "Item three"} {
		finding, err := service.GetFinding(ctx, group, first)
		if err != nil {
			test.Fatal(err)
		}
		created, err := service.CreateFinding(ctx, group, FindingInput{Title: title, Severity: "high", Status: "confirmed", AssigneeOrganizationID: organization, BusinessSystemID: finding.BusinessSystemID, Description: "逐项整改测试", Source: "manual"}, "items-create", "127.0.0.1")
		if err != nil {
			test.Fatal(err)
		}
		ids = append(ids, created.ID)
	}
	input := reviewOrder(organization, first)
	input.FindingIDs = ids
	order, err := service.CreateWorkOrder(ctx, group, input, "items-order", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	order, err = service.Issue(ctx, group, order.ID, CommentInput{}, "items-issue", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	order, err = service.Accept(ctx, executor, order.ID, CommentInput{}, "items-accept", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	attachment, err := service.AddAttachment(ctx, executor, order.ID, AttachmentInput{Category: "remediation", FileName: "proof.txt", ContentType: "text/plain", SizeBytes: 5, Reader: strings.NewReader("proof")}, "items-proof", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	page, _ := pagination.Normalize(1, 100)
	versionFor := func(id string) WorkOrderItemVersion {
		test.Helper()
		result, loadErr := service.SearchWorkOrderFindings(ctx, group, order.ID, page)
		if loadErr != nil {
			test.Fatal(loadErr)
		}
		for _, row := range result.Items {
			if row.ID == id {
				return WorkOrderItemVersion{ID: id, Version: row.Version, FindingUpdatedAt: row.FindingUpdatedAt}
			}
		}
		test.Fatal("missing item")
		return WorkOrderItemVersion{}
	}
	request := func(actor auth.Principal, action string, items ...WorkOrderItemVersion) (BatchResult, error) {
		evidence := []string{}
		if action == "submit" {
			evidence = []string{attachment.ID}
		}
		return service.ProcessWorkOrderItems(ctx, actor, order.ID, WorkOrderItemInput{Items: items, Action: action, Note: "逐项处置说明", EvidenceIDs: evidence}, "items-"+action, "127.0.0.1")
	}
	assertStatus := func(expected string) {
		test.Helper()
		actual, loadErr := service.GetWorkOrder(ctx, group, order.ID)
		if loadErr != nil || actual.Status != expected {
			test.Fatalf("order status want %s got %s: %v", expected, actual.Status, loadErr)
		}
	}
	stale := versionFor(first)
	if _, err = request(outsider, "submit", stale); !errors.Is(err, ErrForbidden) {
		test.Fatalf("cross-org: %v", err)
	}
	result, err := request(executor, "submit", stale, WorkOrderItemVersion{ID: "00000000-0000-0000-0000-000000000001", Version: 1})
	if err != nil || result.Succeeded != 1 || result.Failed != 1 {
		test.Fatalf("partial: %+v %v", result, err)
	}
	assertStatus("in_progress")
	result, err = request(executor, "submit", stale)
	if err != nil || result.Items[0].Code != "edit_conflict" {
		test.Fatalf("retry: %+v %v", result, err)
	}
	result, err = request(executor, "pass", versionFor(first))
	if err != nil || result.Items[0].Code != "forbidden" {
		test.Fatalf("org verification: %+v %v", result, err)
	}
	result, err = request(group, "pass", versionFor(first))
	if err != nil || result.Succeeded != 1 {
		test.Fatalf("partial verification: %+v %v", result, err)
	}
	assertStatus("in_progress")
	result, err = request(executor, "submit", versionFor(ids[1]), versionFor(ids[2]))
	if err != nil || result.Succeeded != 2 {
		test.Fatalf("remaining submit: %+v %v", result, err)
	}
	assertStatus("pending_verification")
	result, err = request(group, "pass", versionFor(ids[1]))
	if err != nil || result.Succeeded != 1 {
		test.Fatalf("pass: %+v %v", result, err)
	}
	result, err = request(group, "return", versionFor(ids[2]))
	if err != nil || result.Succeeded != 1 {
		test.Fatalf("return: %+v %v", result, err)
	}
	assertStatus("returned")
	result, err = request(executor, "submit", versionFor(ids[2]))
	if err != nil || result.Succeeded != 1 {
		test.Fatalf("resubmit: %+v %v", result, err)
	}
	assertStatus("pending_verification")
	result, err = request(group, "pass", versionFor(ids[2]))
	if err != nil || result.Succeeded != 1 {
		test.Fatalf("final pass: %+v %v", result, err)
	}
	assertStatus("closed")
	result, err = request(group, "return", versionFor(first))
	if err != nil || result.Items[0].Code != "invalid_state" {
		test.Fatalf("closed immutable: %+v %v", result, err)
	}
	if err = service.DeleteAttachment(ctx, group, order.ID, attachment.ID, "items-delete-proof", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		test.Fatalf("evidence delete: %v", err)
	}
	var histories int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM security_work_order_item_history WHERE work_order_id=$1::uuid`, order.ID).Scan(&histories); err != nil || histories != 8 {
		test.Fatalf("immutable history: %d %v", histories, err)
	}
	for _, id := range ids {
		finding, loadErr := service.GetFinding(ctx, group, id)
		if loadErr != nil || finding.Status != "fixed" {
			test.Fatalf("finding closure: %+v %v", finding, loadErr)
		}
	}
}

func TestOrganizationItemDispositionPreservesHistoricalOrders(test *testing.T) {
	pool, manager, organization, findingID := reviewFixture(test)
	ctx := context.Background()
	service := NewService(pool, test.TempDir())
	create := func() WorkOrder {
		test.Helper()
		order, err := service.CreateWorkOrder(ctx, manager, reviewOrder(organization, findingID), "history-create", "127.0.0.1")
		if err != nil {
			test.Fatal(err)
		}
		if _, err = service.Issue(ctx, manager, order.ID, CommentInput{}, "history-issue", "127.0.0.1"); err != nil {
			test.Fatal(err)
		}
		if _, err = service.Accept(ctx, manager, order.ID, CommentInput{}, "history-accept", "127.0.0.1"); err != nil {
			test.Fatal(err)
		}
		return order
	}
	previous := create()
	if _, err := service.CreateWorkOrder(ctx, manager, reviewOrder(organization, findingID), "history-overlap", "127.0.0.1"); !errors.Is(err, ErrConflict) {
		test.Fatalf("parallel active reference: %v", err)
	}
	if _, err := service.Cancel(ctx, manager, previous.ID, CommentInput{Comment: "重新派发"}, "history-cancel", "127.0.0.1"); err != nil {
		test.Fatal(err)
	}
	current := create()
	finding, err := service.GetFinding(ctx, manager, findingID)
	if err != nil {
		test.Fatal(err)
	}
	master, err := service.CreateFinding(ctx, manager, FindingInput{Title: "统一主漏洞", Severity: "high", Status: "confirmed", AssigneeOrganizationID: organization, BusinessSystemID: finding.BusinessSystemID, Description: "专项重复处置", Source: "manual"}, "history-master", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	_, err = service.LinkDuplicateFinding(ctx, manager, findingID, DuplicateFindingInput{MasterFindingID: master.ID, Reason: "确认同一根因", ExpectedUpdatedAt: &finding.UpdatedAt}, "history-disposition", "127.0.0.1")
	if err != nil {
		test.Fatal(err)
	}
	for _, expected := range []struct{ id, status, stage string }{{previous.ID, "cancelled", "not_started"}, {current.ID, "closed", "disposition"}} {
		order, loadErr := service.GetWorkOrder(ctx, manager, expected.id)
		if loadErr != nil || order.Status != expected.status {
			test.Fatalf("historical order status: %+v %v", order, loadErr)
		}
		rows, loadErr := service.SearchWorkOrderFindings(ctx, manager, expected.id, pagination.Query{Page: 1, PageSize: 20})
		if loadErr != nil || len(rows.Items) != 1 || rows.Items[0].Stage != expected.stage {
			test.Fatalf("historical item stage: %+v %v", rows, loadErr)
		}
	}
	var previousHistory, currentHistory int
	if err = pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE work_order_id=$1::uuid),count(*) FILTER(WHERE work_order_id=$2::uuid) FROM security_work_order_item_history`, previous.ID, current.ID).Scan(&previousHistory, &currentHistory); err != nil || previousHistory != 0 || currentHistory != 1 {
		test.Fatalf("history mutation: %d/%d %v", previousHistory, currentHistory, err)
	}
}
