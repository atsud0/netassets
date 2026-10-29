package remediation_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"netassets/internal/auth"
	"netassets/internal/notification"
	"netassets/internal/pagination"
	"netassets/internal/remediation"
	"netassets/internal/securityworkorder"
)

func version(t *testing.T, f *fixture, id string) int {
	t.Helper()
	d, err := f.s.Detail(f.ctx, f.group, id, false)
	must(t, err)
	return int(d["version"].(float64))
}
func TestHistoricalIdentityAndGlobalVendorMembership(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "snapshot-test-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	f.action(t, f.worker, id, "comment", remediation.ActionInput{Visibility: "participants", Reason: "交付前记录"}, true)
	f.submit(t, f.worker, id)
	var catalog, name string
	must(t, f.pool.QueryRow(f.ctx, `SELECT c.id::text,c.name FROM remediation_grants g JOIN vendors v ON v.id=g.vendor_id JOIN vendor_catalog c ON c.id=v.catalog_id WHERE g.id=$1::uuid`, f.grant).Scan(&catalog, &name))
	_, err := f.pool.Exec(f.ctx, `UPDATE users SET display_name='改名后的执行人员' WHERE id=$1::uuid`, f.worker.UserID)
	must(t, err)
	_, err = f.s.SaveCatalog(f.ctx, f.manager, catalog, remediation.CatalogInput{Name: name + "新版", Status: "enabled", Reason: "不允许组织改集团身份"})
	if !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("organization mutated global vendor: %v", err)
	}
	_, err = f.s.SaveCatalog(f.ctx, f.group, catalog, remediation.CatalogInput{Name: name + "新版", Status: "enabled", Reason: "集团更新名称"})
	must(t, err)
	d, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	if d["assignee_name"] != "执行人员" || d["vendor_name"] != name {
		t.Fatalf("assignment identity rewritten: %v", d)
	}
	page, _ := pagination.Normalize(1, 100)
	for _, kind := range []string{"comments", "events", "submissions"} {
		rows, e := f.s.Children(f.ctx, f.worker, id, kind, true, page)
		must(t, e)
		if strings.Contains(fmtJSON(rows.Items), "改名后的执行人员") || strings.Contains(fmtJSON(rows.Items), name+"新版") {
			t.Fatalf("history changed: %s", kind)
		}
	}
	must(t, f.s.UpdateMembership(f.ctx, f.group, catalog, f.worker.UserID, "disabled", "结束厂商成员关系", time.Now().Add(24*time.Hour)))
	if _, err = f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("disabled member retained access: %v", err)
	}
}

func TestExecutionPlanValidationAndScopedStatistics(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "plan-test-key", f.grant)
	f.assign(t, 1, "plan-other-key", f.otherGrant)
	_, err := f.s.Act(f.ctx, f.worker, id, "accept", "", remediation.ActionInput{ExpectedVersion: version(t, f, id)}, true)
	if !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("accepted without ETA: %v", err)
	}
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	eta := time.Now().Add(2 * time.Hour)
	_, err = f.s.Act(f.ctx, f.worker, id, "progress", "", remediation.ActionInput{ExpectedVersion: version(t, f, id), Progress: "等待发布", BlockedReason: "发布窗口关闭", ExpectedCompletion: &eta}, true)
	if !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("missing blocker dependency accepted: %v", err)
	}
	f.action(t, f.worker, id, "progress", remediation.ActionInput{Progress: "等待发布", BlockedReason: "窗口限制", BlockedDependency: "运维发布组", BlockedUntil: &eta, ExpectedCompletion: &eta}, true)
	page, _ := pagination.Normalize(1, 20)
	rows, err := f.s.List(f.ctx, f.worker, remediation.Filter{Mine: true, Blocked: true, Page: page})
	must(t, err)
	if rows.Total != 1 {
		t.Fatal("blocked filter")
	}
	st, err := f.s.Statistics(f.ctx, f.worker, remediation.Filter{Mine: true, Status: "all"})
	must(t, err)
	if st["total"] != float64(1) {
		t.Fatalf("statistics exposed another worker: %v", st)
	}
	sub := f.submit(t, f.worker, id)
	f.action(t, f.manager, id, "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "已部署"}, false)
	_, err = f.s.Act(f.ctx, f.group, id, "security-review", "", remediation.ActionInput{ExpectedVersion: version(t, f, id), SubmissionID: sub, Decision: "pass", Reason: "无实际环境时间"}, false)
	if !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("review without evidence context: %v", err)
	}
	f.action(t, f.group, id, "security-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "复测通过"}, false)
	rows, err = f.s.List(f.ctx, f.worker, remediation.Filter{Mine: true, Page: page})
	must(t, err)
	if rows.Total != 0 {
		t.Fatal("completed task appeared in default active list")
	}
	st, err = f.s.Statistics(f.ctx, f.worker, remediation.Filter{Mine: true, Status: "all"})
	must(t, err)
	if st["verified_fixed"] != float64(1) || st["risk_accepted"] != float64(0) || st["execution_seconds"].(float64) <= 0 {
		t.Fatalf("wrong statistics: %v", st)
	}
}

func TestSourceReviewFrozenMaterialAttachmentsAndAudit(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "material-test-key", f.grant)
	f.ctx = remediation.WithRequest(f.ctx, remediation.RequestInfo{UserID: f.manager.UserID, RequestID: "material-audit-test", IP: "127.0.0.1", Method: "POST", Route: "/remediation-tasks/:id/actions/:action"})
	file, err := f.s.Upload(f.ctx, f.manager, id, remediation.AttachmentInput{Name: "shared.txt", Visibility: "participants", Version: version(t, f, id), Reader: strings.NewReader("已批准的公开复现证据")}, false)
	must(t, err)
	material := remediation.Material{Title: "公开标题", Severity: "high", Description: "公开描述", Requirement: "生产验证", AttachmentIDs: []string{file["id"].(string)}}
	f.action(t, f.manager, id, "materials", remediation.ActionInput{Material: material, Reason: "批准共享附件"}, false)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	sub := f.submit(t, f.worker, id)
	f.action(t, f.manager, id, "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "提交完整"}, false)
	source := remediation.Material{Title: "原始更新标题", Severity: "high", Description: "内部新漏洞描述", Evidence: "不得泄露的内部源证据", Advice: "修复新的输入路径"}
	_, err = f.s.UpdateSource(f.ctx, f.manager, id, remediation.ActionInput{ExpectedVersion: version(t, f, id), Material: source, Reason: "越权源修改"}, false)
	if !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("organization altered group source: %v", err)
	}
	_, err = f.s.UpdateSource(f.ctx, f.group, id, remediation.ActionInput{ExpectedVersion: version(t, f, id), Material: source, Reason: "补充原始复现信息"}, false)
	must(t, err)
	d, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	if d["material_needs_review"] != true || d["status"] != "in_progress" || strings.Contains(fmtJSON(d), source.Evidence) {
		t.Fatalf("unsafe source propagation: %v", d)
	}
	if _, err = f.s.Act(f.ctx, f.worker, id, "submit", "pending-material-submit", remediation.ActionInput{ExpectedVersion: version(t, f, id)}, true); !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("submitted stale material: %v", err)
	}
	page, _ := pagination.Normalize(1, 20)
	old, err := f.s.Children(f.ctx, f.worker, id, "submissions", true, page)
	must(t, err)
	if old.Items[0]["lifecycle_status"] != "returned" || strings.Contains(fmtJSON(old.Items), source.Evidence) {
		t.Fatal("old submission evidence was changed or leaked")
	}
	material.Description = "已复核的新版公开描述"
	material.AttachmentIDs = nil
	f.action(t, f.manager, id, "materials", remediation.ActionInput{Material: material, Reason: "重新核准公开材料"}, false)
	history, err := f.s.Children(f.ctx, f.worker, id, "materials", true, page)
	must(t, err)
	if len(history.Items) != 3 || !strings.Contains(fmtJSON(history.Items[1]), "shared.txt") || strings.Contains(fmtJSON(history.Items[0]), "shared.txt") {
		t.Fatal("material attachment versions not frozen")
	}
	f.submit(t, f.worker, id)
	var audits int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_logs WHERE resource_id=$1 AND request_id='material-audit-test' AND details ? 'before' AND details ? 'after'`, id).Scan(&audits))
	if audits == 0 {
		t.Fatal("request id or before/after audit missing")
	}
	must(t, f.s.RecordDenial(f.ctx, "forbidden"))
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM audit_logs WHERE request_id='material-audit-test' AND result='failure'`).Scan(&audits))
	if audits == 0 {
		t.Fatal("denial missing")
	}
}

type gatedReader struct {
	started chan struct{}
	resume  chan struct{}
	read    bool
}

func (r *gatedReader) Read(b []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	close(r.started)
	<-r.resume
	return copy(b, "upload after revoke"), nil
}
func TestRevocationDuringUploadAndReassignmentNotice(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "upload-revoke-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	r := &gatedReader{started: make(chan struct{}), resume: make(chan struct{})}
	done := make(chan error, 1)
	v := version(t, f, id)
	go func() {
		_, err := f.s.Upload(f.ctx, f.worker, id, remediation.AttachmentInput{Name: "late.txt", Visibility: "participants", Version: v, Reader: r}, true)
		done <- err
	}()
	<-r.started
	err := f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "上传过程中结束合作")
	close(r.resume)
	must(t, err)
	if err = <-done; !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("upload survived revocation: %v", err)
	}
	var count int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM remediation_attachments WHERE task_id=$1::uuid`, id).Scan(&count))
	if count != 0 {
		t.Fatal("revoked upload metadata retained")
	}
	f.action(t, f.manager, id, "reassign", remediation.ActionInput{GrantID: f.otherGrant, Reason: "由其他人接手"}, false)
	page, _ := pagination.Normalize(1, 100)
	notices, _, err := notification.NewService(f.pool).Search(f.ctx, f.worker, notification.SearchInput{Page: page})
	must(t, err)
	found := false
	for _, n := range notices.Items {
		if n.ResourceType == "remediation_change" {
			found = true
			if strings.Contains(fmtJSON(n), id) || strings.Contains(fmtJSON(n), "对外漏洞材料") {
				t.Fatal("change notice exposed former task")
			}
		}
	}
	if !found {
		t.Fatal("missing safe reassignment notice")
	}
}

func TestUnassignedFindingAndConcurrentCancellation(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "cancel-race-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	sub := f.submit(t, f.worker, id)
	f.action(t, f.manager, id, "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "组织确认"}, false)
	version := version(t, f, id)
	now := time.Now()
	done := make(chan error, 2)
	go func() {
		_, err := f.s.Act(f.ctx, f.group, id, "security-review", "", remediation.ActionInput{ExpectedVersion: version, SubmissionID: sub, Decision: "pass", Reason: "复测通过", VerifiedAt: &now, VerifiedEnvironment: "生产"}, false)
		done <- err
	}()
	go func() {
		_, err := f.orders.Cancel(f.ctx, f.group, f.order, securityworkorder.CommentInput{Comment: "停止整改工单"}, "", "")
		done <- err
	}()
	for i := 0; i < 2; i++ {
		err := <-done
		if err != nil && !errors.Is(err, remediation.ErrConflict) {
			t.Fatal(err)
		}
	}
	var state, finding string
	must(t, f.pool.QueryRow(f.ctx, `SELECT t.status,f.status FROM remediation_tasks t JOIN security_findings f ON f.id=t.finding_id WHERE t.id=$1::uuid`, id).Scan(&state, &finding))
	if state != "completed" && state != "cancelled" {
		t.Fatalf("task still active after cancellation: %s", state)
	}
	if state == "completed" && finding != "fixed" {
		t.Fatalf("verified result overwritten by cancellation: finding status=%s", finding)
	}
}

func TestEightFindingsInternalAndTwoVendorsPartialReview(t *testing.T) {
	f := setup(t)
	suffix := time.Now().Format("150405000000000")
	internal, err := f.s.CreatePerson(f.ctx, f.manager, remediation.PersonInput{OrganizationID: f.org, SystemID: f.system, ParticipantType: "internal", Username: "internal_" + suffix, DisplayName: "内部开发", Password: "Internal-Repair-2026", ValidUntil: f.due.Add(24 * time.Hour)})
	must(t, err)
	vendor, err := f.s.SaveVendor(f.ctx, f.group, "", f.org, "第二厂商_"+suffix, "13800000018", "enabled")
	must(t, err)
	external, err := f.s.CreatePerson(f.ctx, f.manager, remediation.PersonInput{OrganizationID: f.org, SystemID: f.system, ParticipantType: "external", VendorID: vendor["id"].(string), Username: "vendor2_" + suffix, DisplayName: "第二厂商开发", Password: "Vendor-Repair-2026", ValidUntil: f.due.Add(24 * time.Hour)})
	must(t, err)
	people := []auth.Principal{f.worker, {UserID: internal["user_id"].(string)}, {UserID: external["user_id"].(string)}}
	grants := []string{f.grant, internal["id"].(string), external["id"].(string)}
	findings := []string{}
	for i := 0; i < 8; i++ {
		item, e := f.orders.CreateFinding(f.ctx, f.group, securityworkorder.FindingInput{Title: "八项闭环漏洞", Severity: "high", Status: "pending", AssigneeOrganizationID: f.org, BusinessSystemID: f.system, Description: "完整报告", Source: "manual", DiscoveredAt: time.Now()}, "", "")
		must(t, e)
		findings = append(findings, item.ID)
	}
	order, err := f.orders.CreateWorkOrder(f.ctx, f.group, securityworkorder.WorkOrderInput{ExecutionMode: "individual", Title: "八项多方修复", AssigneeOrganizationID: f.org, RemediationRequirement: "生产修复", DueAt: f.due, FindingIDs: findings}, "", "")
	must(t, err)
	_, err = f.orders.Issue(f.ctx, f.group, order.ID, securityworkorder.CommentInput{}, "", "")
	must(t, err)
	f.order = order.ID
	f.findings = findings
	ids := []string{}
	for i := 0; i < 8; i++ {
		id := f.assign(t, i, "eight-assign-"+findings[i], grants[i%3])
		ids = append(ids, id)
		f.action(t, people[i%3], id, "accept", remediation.ActionInput{}, true)
		sub := f.submit(t, people[i%3], id)
		f.action(t, f.manager, id, "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "组织确认已发布"}, false)
		decision := "pass"
		if i >= 6 {
			decision = "return"
		}
		f.action(t, f.group, id, "security-review", remediation.ActionInput{SubmissionID: sub, Decision: decision, Reason: "逐项生产验证结果"}, false)
	}
	page, _ := pagination.Normalize(1, 100)
	for i, p := range people {
		rows, e := f.s.List(f.ctx, p, remediation.Filter{Mine: true, Status: "all", OrderID: f.order, Page: page})
		must(t, e)
		expected := 3
		if i == 2 {
			expected = 2
		}
		if rows.Total != expected {
			t.Fatalf("cross vendor leak or missing tasks: %d", rows.Total)
		}
	}
	stats, err := f.s.Statistics(f.ctx, f.manager, remediation.Filter{OrderID: f.order, Status: "all"})
	must(t, err)
	if stats["verified_fixed"] != float64(6) {
		t.Fatalf("partial review total: %v", stats)
	}
	if err = f.s.CloseOrder(f.ctx, f.group, f.order, "不应关闭"); !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("closed with returned tasks: %v", err)
	}
	for i := 6; i < 8; i++ {
		sub := f.submit(t, people[i%3], ids[i])
		f.action(t, f.manager, ids[i], "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "补充整改"}, false)
		f.action(t, f.group, ids[i], "security-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "生产复测通过"}, false)
	}
	must(t, f.s.CloseOrder(f.ctx, f.group, f.order, "八项全部复测通过"))
}

func TestDeploymentAndForeignAttachmentValidation(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "proof-validate-1", f.grant)
	other := f.assign(t, 1, "proof-validate-2", f.otherGrant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	file, err := f.s.Upload(f.ctx, f.other, other, remediation.AttachmentInput{Name: "other.txt", Visibility: "participants", Version: version(t, f, other), Reader: strings.NewReader("另一人员任务证明")}, true)
	must(t, err)
	now := time.Now()
	in := remediation.ActionInput{ExpectedVersion: version(t, f, id), Summary: "修复代码", DeploymentStatus: "code_only", SelfTestResult: "本地验证"}
	if _, err = f.s.Act(f.ctx, f.worker, id, "submit", "code-only-test", in, true); !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("code-only accepted: %v", err)
	}
	in.DeploymentStatus = "target"
	in.ChangeReference = "release-1"
	in.DeployedAt = &now
	in.AttachmentIDs = []string{file["id"].(string)}
	if _, err = f.s.Act(f.ctx, f.worker, id, "submit", "foreign-attachment", in, true); !errors.Is(err, remediation.ErrInvalid) {
		t.Fatalf("foreign proof attached: %v", err)
	}
	in.AttachmentIDs = nil
	_, err = f.s.Act(f.ctx, f.worker, id, "submit", "same-key-submit", in, true)
	must(t, err)
	_, err = f.s.Act(f.ctx, f.worker, id, "submit", "same-key-submit", in, true)
	must(t, err)
	in.Summary = "不同请求不能重用幂等键"
	if _, err = f.s.Act(f.ctx, f.worker, id, "submit", "same-key-submit", in, true); !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("idempotency content changed: %v", err)
	}
	var count int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM remediation_submissions WHERE task_id=$1::uuid`, id).Scan(&count))
	if count != 1 {
		t.Fatal("duplicate submission")
	}
}

func TestClosedRiskExpiryAndNewRemediationRound(t *testing.T) {
	f := setup(t)
	first := f.assign(t, 0, "risk-history-first", f.grant)
	second := f.assign(t, 1, "risk-history-second", f.otherGrant)
	until := time.Now().Add(24 * time.Hour)
	for i, id := range []string{first, second} {
		person := f.worker
		kind := "risk_acceptance"
		if i == 1 {
			person = f.other
			kind = "false_positive"
		}
		r, err := f.s.Request(f.ctx, person, id, remediation.RequestInput{Type: kind, Reason: "集团评估其他处置", ExpectedVersion: version(t, f, id)})
		must(t, err)
		must(t, f.s.Decide(f.ctx, f.group, r["id"].(string), remediation.DecideInput{Decision: "approved", Reason: "按项评估", RiskUntil: &until, ExpectedVersion: version(t, f, id)}))
	}
	stats, err := f.s.Statistics(f.ctx, f.manager, remediation.Filter{OrderID: f.order, Status: "all"})
	must(t, err)
	if stats["verified_fixed"] != float64(0) || stats["risk_accepted"] != float64(1) || stats["false_positive"] != float64(1) {
		t.Fatalf("dispositions counted as fixes: %v", stats)
	}
	must(t, f.s.CloseOrder(f.ctx, f.group, f.order, "完成两项获批处置"))
	_, err = f.pool.Exec(f.ctx, `UPDATE security_findings SET risk_accepted_until=now()-interval '1 hour' WHERE id=$1::uuid`, f.findings[0])
	must(t, err)
	page, _ := pagination.Normalize(1, 100)
	notices, _, err := notification.NewService(f.pool).Search(f.ctx, f.group, notification.SearchInput{Category: "risk_review", Page: page})
	must(t, err)
	found := false
	for _, item := range notices.Items {
		if item.ResourceID == f.findings[0] {
			found = true
		}
	}
	if !found {
		t.Fatal("expired risk has no review notice")
	}
	stats, err = f.s.Statistics(f.ctx, f.manager, remediation.Filter{OrderID: f.order, Status: "all"})
	must(t, err)
	if stats["risk_accepted"] != float64(0) || stats["risk_expired"] != float64(1) {
		t.Fatalf("expired risk still valid: %v", stats)
	}
	_, err = f.orders.RevokeRisk(f.ctx, f.group, f.findings[0], securityworkorder.RiskRevokeInput{
		ExpectedUpdatedAt: findingVersion(t, f.ctx, f.pool, f.findings[0]),
	}, "", "")
	must(t, err)
	next, err := f.orders.CreateWorkOrder(f.ctx, f.group, securityworkorder.WorkOrderInput{ExecutionMode: "individual", Title: "风险到期再次整改", RemediationRequirement: "重新修复并部署", AssigneeOrganizationID: f.org, DueAt: f.due, FindingIDs: []string{f.findings[0]}}, "", "")
	must(t, err)
	if next.ID == f.order {
		t.Fatal("old work order overwritten")
	}
	d, err := f.s.Detail(f.ctx, f.group, first, false)
	must(t, err)
	if d["completion_reason"] != "risk_accepted" || d["work_order_status"] != "closed" {
		t.Fatal("closed history changed")
	}
}

func TestUnassignedFindingPreventsClosure(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "unassigned-close-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	sub := f.submit(t, f.worker, id)
	f.action(t, f.manager, id, "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "材料齐全"}, false)
	f.action(t, f.group, id, "security-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "逐项修复通过"}, false)
	if err := f.s.CloseOrder(f.ctx, f.group, f.order, "忽略未分派项"); !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("unassigned finding ignored: %v", err)
	}
}

func TestLegacyOrganizationModeStillCompletesWithAttachments(t *testing.T) {
	f := setup(t)
	f.orders = securityworkorder.NewService(f.pool, t.TempDir())
	_, err := f.orders.Cancel(f.ctx, f.group, f.order, securityworkorder.CommentInput{Comment: "改用独立组织模式测试"}, "", "")
	must(t, err)
	order, err := f.orders.CreateWorkOrder(f.ctx, f.group, securityworkorder.WorkOrderInput{Title: "存量组织模式兼容", AssigneeOrganizationID: f.org, DueAt: f.due, FindingIDs: f.findings, RemediationRequirement: "统一整改"}, "", "")
	must(t, err)
	if order.ExecutionMode != "organization" {
		t.Fatal("legacy default changed")
	}
	_, err = f.orders.Issue(f.ctx, f.group, order.ID, securityworkorder.CommentInput{}, "", "")
	must(t, err)
	_, err = f.orders.Accept(f.ctx, f.manager, order.ID, securityworkorder.CommentInput{}, "", "")
	must(t, err)
	_, err = f.orders.Progress(f.ctx, f.manager, order.ID, securityworkorder.ProgressInput{Progress: "组织统一处理"}, "", "")
	must(t, err)
	attachment, err := f.orders.AddAttachment(f.ctx, f.manager, order.ID, securityworkorder.AttachmentInput{Category: "remediation", FileName: "legacy.txt", SizeBytes: 6, Reader: strings.NewReader("legacy")}, "", "")
	must(t, err)
	_, err = f.orders.GetAttachment(f.ctx, f.manager, order.ID, attachment.ID)
	must(t, err)
	_, err = f.orders.Submit(f.ctx, f.manager, order.ID, securityworkorder.SubmitInput{RemediationResult: "两个漏洞统一修复"}, "", "")
	must(t, err)
	result, err := f.orders.Review(f.ctx, f.group, order.ID, securityworkorder.ReviewInput{Decision: "closed", VerificationResult: "整体复测通过"}, "", "")
	must(t, err)
	if result.Status != "closed" {
		t.Fatal("legacy order not closed")
	}
	var count int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM security_findings WHERE id=ANY($1::uuid[]) AND status='fixed'`, f.findings).Scan(&count))
	if count != 2 {
		t.Fatal("legacy closure did not resolve findings")
	}
}

func TestConcurrentSubmitReassignAndWithdrawReview(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "submit-reassign-race", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	v := version(t, f, id)
	now := time.Now()
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		_, e := f.s.Act(f.ctx, f.worker, id, "submit", "race-submit-key", remediation.ActionInput{ExpectedVersion: v, Summary: "并发提交", DeploymentStatus: "target", ChangeReference: "release-race", DeployedAt: &now, SelfTestResult: "验证通过"}, true)
		results <- e
	}()
	go func() {
		<-start
		_, e := f.s.Act(f.ctx, f.manager, id, "reassign", "", remediation.ActionInput{ExpectedVersion: v, GrantID: f.otherGrant, Reason: "交接人员"}, false)
		results <- e
	}()
	close(start)
	wins := 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			wins++
		} else if !errors.Is(e, remediation.ErrConflict) && !errors.Is(e, remediation.ErrNotFound) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatalf("submit/reassign successes: %d", wins)
	}
	d, e := f.s.Detail(f.ctx, f.group, id, false)
	must(t, e)
	if d["assignee_user_id"] == f.other.UserID && d["status"] != "pending_acceptance" {
		t.Fatal("new assignee inherited old submission")
	}
	id = f.assign(t, 1, "withdraw-review-race", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	sub := f.submit(t, f.worker, id)
	v = version(t, f, id)
	start = make(chan struct{})
	go func() {
		<-start
		_, e := f.s.Act(f.ctx, f.worker, id, "withdraw", "", remediation.ActionInput{ExpectedVersion: v, SubmissionID: sub, Reason: "补充证明"}, true)
		results <- e
	}()
	go func() {
		<-start
		_, e := f.s.Act(f.ctx, f.manager, id, "organization-review", "", remediation.ActionInput{ExpectedVersion: v, SubmissionID: sub, Decision: "pass", Reason: "组织已确认"}, false)
		results <- e
	}()
	close(start)
	wins = 0
	for i := 0; i < 2; i++ {
		e := <-results
		if e == nil {
			wins++
		} else if !errors.Is(e, remediation.ErrConflict) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatalf("withdraw/review successes: %d", wins)
	}
}

func TestOverdueSummarySettingsAndStableNotificationRead(t *testing.T) {
	f := setup(t)
	for i := 0; i < 2; i++ {
		id := f.assign(t, i, "overdue-summary-"+string(rune('a'+i)), f.grant)
		_, err := f.pool.Exec(f.ctx, `UPDATE remediation_tasks SET due_at=now()-interval '1 day' WHERE id=$1::uuid`, id)
		must(t, err)
	}
	must(t, f.s.SetSettings(f.ctx, f.group, 3, "三天一轮汇总"))
	defer func() { must(t, f.s.SetSettings(f.ctx, f.group, 1, "恢复默认频率")) }()
	if e := f.s.SetSettings(f.ctx, f.manager, 2, "组织越权"); !errors.Is(e, remediation.ErrForbidden) {
		t.Fatal("organization changed global reminders", e)
	}
	page, _ := pagination.Normalize(1, 100)
	svc := notification.NewService(f.pool)
	notices, _, err := svc.Search(f.ctx, f.worker, notification.SearchInput{Page: page})
	must(t, err)
	key := ""
	count := 0
	for _, n := range notices.Items {
		if n.ResourceType == "remediation_summary" {
			count++
			key = n.Key
			if !strings.Contains(n.Message, "2 项") || n.ActionPath != "/repair/tasks?overdue=true" {
				t.Fatalf("incorrect summary: %+v", n)
			}
		}
	}
	if count != 1 {
		t.Fatalf("summary count %d", count)
	}
	must(t, svc.MarkRead(f.ctx, f.worker, key))
	must(t, svc.MarkRead(f.ctx, f.worker, key))
	notices, _, err = svc.Search(f.ctx, f.worker, notification.SearchInput{Page: page, UnreadOnly: true})
	must(t, err)
	for _, n := range notices.Items {
		if n.Key == key {
			t.Fatal("event replay recreated read notification")
		}
	}
}

func TestCancellationAndCrossOrganizationSharedIdentity(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "cancel-task-returns-unassigned", f.grant)
	f.action(t, f.manager, id, "cancel", remediation.ActionInput{Reason: "重新选择负责人"}, false)
	summary, err := f.s.OrderSummary(f.ctx, f.manager, f.order)
	must(t, err)
	if summary["unassigned"].(float64) != 2 {
		t.Fatal("cancelled task still counted as assigned")
	}
	var state string
	must(t, f.pool.QueryRow(f.ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, f.findings[0]).Scan(&state))
	if state == "resolved" {
		t.Fatal("task cancellation resolved vulnerability")
	}
	id = f.assign(t, 0, "assign-after-cancel", f.grant)
	second := setup(t)
	var username, vendorName string
	must(t, f.pool.QueryRow(f.ctx, `SELECT u.username,v.name FROM remediation_grants g JOIN users u ON u.id=g.user_id JOIN vendors v ON v.id=g.vendor_id WHERE g.id=$1::uuid`, f.grant).Scan(&username, &vendorName))
	vendor, err := second.s.SaveVendor(second.ctx, second.group, "", second.org, vendorName, "13800000018", "enabled")
	must(t, err)
	invitation, err := second.s.Invite(second.ctx, second.manager, remediation.PersonInput{OrganizationID: second.org, SystemID: second.system, VendorID: vendor["id"].(string), ParticipantType: "external", Username: username, DisplayName: "执行人员", ValidUntil: time.Now().Add(7 * 24 * time.Hour)})
	must(t, err)
	grant, err := second.s.AcceptInvitation(second.ctx, &f.worker, remediation.AcceptInvitationInput{Token: invitation["token"].(string)})
	must(t, err)
	if _, err = second.s.Detail(second.ctx, f.worker, second.assign(t, 1, "other-worker-cross-org", second.grant), true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatal("same vendor disclosed unassigned task", err)
	}
	otherID := second.assign(t, 0, "shared-user-second-org", grant["id"].(string))
	if _, err = f.s.Detail(f.ctx, f.other, otherID, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatal("same vendor peer saw other organization", err)
	}
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.grant, "revoked", time.Time{}, "结束本组织合作"))
	if _, err = f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatal("old scope remained accessible", err)
	}
	if _, err = second.s.Detail(second.ctx, f.worker, otherID, true); err != nil {
		t.Fatal("local revocation broke other organization", err)
	}
	if err = f.s.UpdateGrant(f.ctx, f.manager, grant["id"].(string), "revoked", time.Time{}, "越权撤销"); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatal("cross-organization revocation accepted", err)
	}
}
