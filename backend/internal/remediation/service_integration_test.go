package remediation_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/asset"
	"netassets/internal/auth"
	"netassets/internal/bootstrap"
	"netassets/internal/businesssystem"
	"netassets/internal/config"
	"netassets/internal/notification"
	"netassets/internal/pagination"
	"netassets/internal/recyclebin"
	"netassets/internal/remediation"
	"netassets/internal/securityworkorder"
)

type fixture struct {
	ctx                                               context.Context
	pool                                              *pgxpool.Pool
	s                                                 *remediation.Service
	orders                                            *securityworkorder.Service
	group, manager, worker, other                     auth.Principal
	org, system, order, grant, otherGrant, thirdGrant string
	findings                                          []string
	due                                               time.Time
}

func setup(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("NETASSETS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires a dedicated migrated Podman PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	must(t, err)
	t.Cleanup(pool.Close)
	// Unique fixtures avoid destructive resets and can coexist with other suites.
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")
	cfg := config.Config{BootstrapAdminUsername: "repair_root_" + suffix, BootstrapAdminPassword: "Repair-Test-Password-123", BootstrapAdminDisplayName: "集团安全管理员", BootstrapGroupCode: "REPAIR_GROUP_" + suffix, BootstrapGroupName: "修复测试集团"}
	must(t, bootstrap.Ensure(ctx, pool, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))))
	authService := auth.NewService(pool, time.Hour)
	login, err := authService.Login(ctx, auth.LoginInput{Username: cfg.BootstrapAdminUsername, Password: cfg.BootstrapAdminPassword})
	must(t, err)
	f := &fixture{ctx: ctx, pool: pool, s: remediation.NewService(pool, t.TempDir()), orders: securityworkorder.NewService(pool), group: login.User, due: time.Now().Add(24 * time.Hour)}
	must(t, pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'修复子公司','subsidiary') RETURNING id::text`, "RORG_"+suffix).Scan(&f.org))
	must(t, pool.QueryRow(ctx, `INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status) VALUES($1,'修复测试系统',$2::uuid,'running') RETURNING id::text`, "RSYS_"+suffix, f.org).Scan(&f.system))
	var managerID string
	must(t, pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'not-used','组织管理员') RETURNING id::text`, "manager_"+suffix).Scan(&managerID))
	_, err = pool.Exec(ctx, `INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) SELECT $1::uuid,id,$2::uuid,'organization' FROM roles WHERE role_key='organization_admin'`, managerID, f.org)
	must(t, err)
	f.manager = auth.Principal{UserID: managerID, Permissions: []string{"remediation.assign", "remediation.review", "remediation.people.manage", "vendor.manage", auth.PermissionSecurityWorkOrderView, auth.PermissionSecurityWorkOrderExecute}}
	vendor, err := f.s.SaveVendor(ctx, f.group, "", f.org, "测试厂商_"+suffix, "项目联系人", "enabled")
	must(t, err)
	for i := 0; i < 3; i++ {
		person, err := f.s.CreatePerson(ctx, f.manager, remediation.PersonInput{OrganizationID: f.org, SystemID: f.system, ParticipantType: "external", VendorID: vendor["id"].(string), Username: []string{"worker_", "other_", "third_"}[i] + suffix, DisplayName: "执行人员", Password: "Repair-Worker-Password-123", ValidUntil: time.Now().Add(7 * 24 * time.Hour)})
		must(t, err)
		p := auth.Principal{UserID: person["user_id"].(string)}
		if i == 0 {
			f.worker = p
			f.grant = person["id"].(string)
		} else if i == 1 {
			f.other = p
			f.otherGrant = person["id"].(string)
		} else {
			f.thirdGrant = person["id"].(string)
		}
	}
	for i := 0; i < 2; i++ {
		finding, err := f.orders.CreateFinding(ctx, f.group, securityworkorder.FindingInput{Title: "测试漏洞", Severity: "high", Status: "pending", AssigneeOrganizationID: f.org, BusinessSystemID: f.system, Description: "内部完整材料", Evidence: "内部敏感证据", Source: "manual", DiscoveredAt: time.Now()}, "", "")
		must(t, err)
		f.findings = append(f.findings, finding.ID)
	}
	order, err := f.orders.CreateWorkOrder(ctx, f.group, securityworkorder.WorkOrderInput{ExecutionMode: "individual", Title: "个人修复验收", AssigneeOrganizationID: f.org, RemediationRequirement: "部署验证", DueAt: f.due, FindingIDs: f.findings}, "", "")
	must(t, err)
	f.order = order.ID
	_, err = f.orders.Issue(ctx, f.group, f.order, securityworkorder.CommentInput{}, "", "")
	must(t, err)
	return f
}
// findingVersion returns the current optimistic-lock token of a finding. Tests
// that drive a status action have to send it, because the service now refuses
// to write a finding whose row moved after the caller read it.
func findingVersion(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) *time.Time {
	t.Helper()
	var version time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM security_findings WHERE id::text = $1`, id).Scan(&version); err != nil {
		t.Fatalf("load finding version: %v", err)
	}
	return &version
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func TestIndividualWorkOrderLegacyAcceptIsRejected(t *testing.T) {
	f := setup(t)
	_, err := f.orders.Accept(f.ctx, f.manager, f.order, securityworkorder.CommentInput{}, "", "")
	if !errors.Is(err, securityworkorder.ErrIndividualWorkOrderAction) {
		t.Fatalf("legacy individual accept error = %v, want ErrIndividualWorkOrderAction", err)
	}
}

func (f *fixture) assign(t *testing.T, index int, key, grant string) string {
	t.Helper()
	out, err := f.s.Assign(f.ctx, f.manager, f.order, key, remediation.AssignInput{FindingID: f.findings[index], GrantID: grant, DueAt: f.due.Add(-time.Hour), TargetEnvironment: "生产", DeploymentRequired: true, Material: remediation.Material{Title: "对外漏洞材料", Severity: "high", Description: "已脱敏描述", Evidence: "可公开复现步骤", Requirement: "部署生产并验证"}})
	must(t, err)
	return out["id"].(string)
}
func (f *fixture) action(t *testing.T, p auth.Principal, id, action string, in remediation.ActionInput, executor bool) remediation.Object {
	t.Helper()
	d, err := f.s.Detail(f.ctx, p, id, executor)
	must(t, err)
	in.ExpectedVersion = int(d["version"].(float64))
	if action == "accept" && in.ExpectedCompletion == nil {
		target := time.Now().Add(time.Hour)
		in.ExpectedCompletion = &target
	}
	if action == "security-review" && in.VerifiedAt == nil {
		at := time.Now()
		in.VerifiedAt = &at
		in.VerifiedEnvironment = "生产"
	}
	out, err := f.s.Act(f.ctx, p, id, action, "submit-key-"+time.Now().Format("150405.000000000"), in, executor)
	must(t, err)
	return out
}
func (f *fixture) submit(t *testing.T, p auth.Principal, id string) string {
	now := time.Now()
	f.action(t, p, id, "submit", remediation.ActionInput{Summary: "修复参数检查", DeploymentStatus: "target", ChangeReference: "release-1", DeployedAt: &now, SelfTestResult: "生产环境复现不再出现"}, true)
	d, err := f.s.Detail(f.ctx, p, id, true)
	must(t, err)
	return d["current_submission_id"].(string)
}
func TestRepairLifecycleAndIsolation(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "assign-key-1", f.grant)
	id2 := f.assign(t, 1, "assign-key-2", f.otherGrant)
	if id != f.assign(t, 0, "assign-key-1", f.grant) {
		t.Fatal("idempotent assignment changed id")
	}
	if _, err := f.s.Detail(f.ctx, f.other, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("other worker read task: %v", err)
	}
	page, _ := pagination.Normalize(1, 20)
	list, err := f.s.List(f.ctx, f.worker, remediation.Filter{Mine: true, Page: page})
	must(t, err)
	if list.Total != 1 {
		t.Fatalf("worker sees %d tasks", list.Total)
	}
	d, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	if d["description"] != "已脱敏描述" || d["evidence"] != "可公开复现步骤" {
		t.Fatal("unfiltered finding leaked")
	}
	f.action(t, f.manager, id, "comment", remediation.ActionInput{Reason: "内部不得公开", Visibility: "internal"}, false)
	children, err := f.s.Children(f.ctx, f.worker, id, "comments", true, page)
	must(t, err)
	if children.Total != 0 {
		t.Fatal("internal comment leaked")
	}
	children, err = f.s.Children(f.ctx, f.worker, id, "events", true, page)
	must(t, err)
	for _, e := range children.Items {
		if e["action"] == "comment" {
			t.Fatal("internal event leaked")
		}
	}
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	f.action(t, f.other, id2, "accept", remediation.ActionInput{}, true)
	sub := f.submit(t, f.worker, id)
	sub2 := f.submit(t, f.other, id2)
	workerDetail, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	_, err = f.s.Act(f.ctx, f.worker, id, "organization-review", "self-review-key", remediation.ActionInput{
		ExpectedVersion: int(workerDetail["version"].(float64)), SubmissionID: sub,
		Decision: "pass", Reason: "提交人不能自我复核",
	}, false)
	if !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("submitter organization review error = %v, want ErrForbidden", err)
	}
	if err = f.s.CloseOrder(f.ctx, f.group, f.order, "提前关闭"); !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("closed incomplete: %v", err)
	}
	if _, err = f.orders.Submit(f.ctx, f.manager, f.order, securityworkorder.SubmitInput{RemediationResult: "绕过"}, "", ""); !errors.Is(err, securityworkorder.ErrConflict) {
		t.Fatalf("legacy submit bypass: %v", err)
	}
	f.action(t, f.manager, id, "organization-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "确认已发布", InternalNote: "内部联系信息"}, false)
	f.action(t, f.manager, id2, "organization-review", remediation.ActionInput{SubmissionID: sub2, Decision: "pass", Reason: "确认已发布"}, false)
	f.action(t, f.group, id, "security-review", remediation.ActionInput{SubmissionID: sub, Decision: "pass", Reason: "生产版本 release-1 复测通过"}, false)
	f.action(t, f.group, id2, "security-review", remediation.ActionInput{SubmissionID: sub2, Decision: "return", Reason: "另一路径仍有漏洞"}, false)
	var first, second string
	must(t, f.pool.QueryRow(f.ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, f.findings[0]).Scan(&first))
	must(t, f.pool.QueryRow(f.ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, f.findings[1]).Scan(&second))
	if first != "fixed" || second != "in_progress" {
		t.Fatalf("partial review: %s %s", first, second)
	}
	children, err = f.s.Children(f.ctx, f.worker, id, "submissions", true, page)
	must(t, err)
	if strings.Contains(fmtJSON(children.Items), "内部联系信息") {
		t.Fatal("internal review leaked")
	}
	sub3 := f.submit(t, f.other, id2)
	if sub2 == sub3 {
		t.Fatal("submission overwritten")
	}
	f.action(t, f.manager, id2, "organization-review", remediation.ActionInput{SubmissionID: sub3, Decision: "pass", Reason: "材料齐全"}, false)
	f.action(t, f.group, id2, "security-review", remediation.ActionInput{SubmissionID: sub3, Decision: "pass", Reason: "生产复测通过"}, false)
	must(t, f.s.CloseOrder(f.ctx, f.group, f.order, "全部逐项通过"))
}
func TestReassignmentRevocationAndProofAttachments(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "assign-revoke-1", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	d, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	attachment, err := f.s.Upload(f.ctx, f.worker, id, remediation.AttachmentInput{Name: "proof.txt", Visibility: "participants", Version: int(d["version"].(float64)), Reader: strings.NewReader("修复验证证据")}, true)
	must(t, err)
	download, err := f.s.Download(f.ctx, f.worker, id, attachment["id"].(string), true)
	must(t, err)
	download.File.Close()
	f.action(t, f.manager, id, "reassign", remediation.ActionInput{GrantID: f.otherGrant, Reason: "交接给新负责人"}, false)
	if _, err = f.s.Download(f.ctx, f.worker, id, attachment["id"].(string), true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("old worker download: %v", err)
	}
	f.action(t, f.other, id, "accept", remediation.ActionInput{}, true)
	must(t, f.s.UpdateGrant(f.ctx, f.manager, f.otherGrant, "revoked", time.Time{}, "合作结束"))
	if _, err = f.s.Detail(f.ctx, f.other, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("revoked task access: %v", err)
	}
	n := notification.NewService(f.pool)
	page, _ := pagination.Normalize(1, 100)
	messages, _, err := n.Search(f.ctx, f.other, notification.SearchInput{Page: page})
	must(t, err)
	for _, item := range messages.Items {
		if item.ResourceID == id {
			t.Fatal("revoked notification leaked")
		}
	}
	// Another grant remains independent of the revoked relationship.
	people, err := f.s.People(f.ctx, f.manager, f.org, f.system, page)
	must(t, err)
	if people.Total != 3 {
		t.Fatalf("unexpected grant count %d", people.Total)
	}
}
func TestConcurrentAssignmentAndStaleReview(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, key := range []string{"race-key-a", "race-key-b"} {
		wg.Add(1)
		go func(key string) {
			defer wg.Done()
			_, err := f.s.Assign(f.ctx, f.manager, f.order, key, remediation.AssignInput{FindingID: f.findings[0], GrantID: f.grant, DueAt: f.due.Add(-time.Hour), TargetEnvironment: "生产", Material: remediation.Material{Title: "测试", Severity: "high", Description: "描述", Requirement: "要求"}})
			errs <- err
		}(key)
	}
	wg.Wait()
	close(errs)
	success, conflict := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, remediation.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent assigns: success %d conflict %d", success, conflict)
	}
	page, _ := pagination.Normalize(1, 20)
	list, err := f.s.List(f.ctx, f.worker, remediation.Filter{Mine: true, Page: page})
	must(t, err)
	id := list.Items[0]["id"].(string)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	sub := f.submit(t, f.worker, id)
	d, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	oldVersion := int(d["version"].(float64))
	f.action(t, f.worker, id, "withdraw", remediation.ActionInput{SubmissionID: sub, Reason: "补充材料"}, true)
	_, err = f.s.Act(f.ctx, f.manager, id, "organization-review", "unused-key", remediation.ActionInput{ExpectedVersion: oldVersion, SubmissionID: sub, Decision: "pass", Reason: "过时审核"}, false)
	if !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("stale review: %v", err)
	}
}

func fmtJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestInvitationIdentityAndSingleUse(t *testing.T) {
	f := setup(t)
	input := remediation.PersonInput{OrganizationID: f.org, SystemID: f.system, ParticipantType: "internal", Username: "invite_" + time.Now().Format("150405000000"), DisplayName: "受邀开发", ValidUntil: time.Now().Add(7 * 24 * time.Hour)}
	invite, err := f.s.Invite(f.ctx, f.manager, input)
	must(t, err)
	result, err := f.s.AcceptInvitation(f.ctx, nil, remediation.AcceptInvitationInput{Token: invite["token"].(string), Password: "Invite-Test-Password-123"})
	must(t, err)
	if _, err = f.s.AcceptInvitation(f.ctx, nil, remediation.AcceptInvitationInput{Token: invite["token"].(string), Password: "Invite-Test-Password-123"}); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("invitation replay: %v", err)
	}
	authService := auth.NewService(f.pool, time.Hour)
	login, err := authService.Login(f.ctx, auth.LoginInput{Username: input.Username, Password: "Invite-Test-Password-123"})
	must(t, err)
	if !login.User.ExecutionOnly || !login.User.RepairEnabled || len(login.User.Roles) != 0 || len(login.User.Permissions) != 0 {
		t.Fatalf("executor got management scope: %+v", login.User)
	}
	must(t, f.s.UpdateGrant(f.ctx, f.manager, result["id"].(string), "revoked", time.Time{}, "结束本轮授权"))
	invite, err = f.s.Invite(f.ctx, f.manager, input)
	must(t, err)
	if _, err = f.s.AcceptInvitation(f.ctx, nil, remediation.AcceptInvitationInput{Token: invite["token"].(string), Password: "attacker-password-123"}); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("existing account linked without owner: %v", err)
	}
	if _, err = f.s.AcceptInvitation(f.ctx, &f.other, remediation.AcceptInvitationInput{Token: invite["token"].(string)}); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("wrong account linked: %v", err)
	}
	_, err = f.s.AcceptInvitation(f.ctx, &login.User, remediation.AcceptInvitationInput{Token: invite["token"].(string)})
	must(t, err)
	input.Username += "_revoked"
	invite, err = f.s.Invite(f.ctx, f.manager, input)
	must(t, err)
	must(t, f.s.RevokeInvitation(f.ctx, f.manager, invite["id"].(string), "错误邀请"))
	if _, err = f.s.AcceptInvitation(f.ctx, nil, remediation.AcceptInvitationInput{Token: invite["token"].(string), Password: "Invite-Test-Password-123"}); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("revoked invitation accepted: %v", err)
	}
}

func TestExtensionRiskDispositionAndExpiredGrant(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "request-task-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	detail, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	version := int(detail["version"].(float64))
	proposed := f.due.Add(time.Hour)
	request, err := f.s.Request(f.ctx, f.worker, id, remediation.RequestInput{Type: "extension", Mitigation: "临时限制入口", Reason: "发布窗口调整；已限制受影响入口", ProposedDueAt: &proposed, ExpectedVersion: version})
	must(t, err)
	decision := remediation.DecideInput{Decision: "approved", Reason: "允许调整发布窗口", ExpectedVersion: version}
	if err = f.s.Decide(f.ctx, f.manager, request["id"].(string), decision); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("organization extended group deadline: %v", err)
	}
	must(t, f.s.Decide(f.ctx, f.group, request["id"].(string), decision))
	var due time.Time
	must(t, f.pool.QueryRow(f.ctx, `SELECT due_at FROM security_work_orders WHERE id=$1::uuid`, f.order).Scan(&due))
	if !due.Equal(proposed.Truncate(time.Microsecond)) {
		t.Fatalf("deadline not updated: %v", due)
	}
	detail, err = f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	version = int(detail["version"].(float64))
	request, err = f.s.Request(f.ctx, f.worker, id, remediation.RequestInput{Type: "risk_acceptance", Reason: "等待第三方补丁，已有访问限制", ExpectedVersion: version})
	must(t, err)
	until := time.Now().Add(24 * time.Hour)
	decision = remediation.DecideInput{Decision: "approved", Reason: "限期接受并跟踪补丁", ExpectedVersion: version, RiskUntil: &until}
	if err = f.s.Decide(f.ctx, f.manager, request["id"].(string), decision); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("organization accepted risk: %v", err)
	}
	must(t, f.s.Decide(f.ctx, f.group, request["id"].(string), decision))
	detail, err = f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	if detail["completion_reason"] != "risk_accepted" {
		t.Fatalf("wrong completion: %v", detail["completion_reason"])
	}
	if _, err = f.s.Act(f.ctx, f.manager, id, "reopen-risk", "", remediation.ActionInput{Reason: "恢复整改", ExpectedVersion: int(detail["version"].(float64))}, false); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("organization revoked group risk decision: %v", err)
	}
	f.action(t, f.group, id, "reopen-risk", remediation.ActionInput{Reason: "补丁已可用，恢复整改"}, false)
	var findingStatus string
	must(t, f.pool.QueryRow(f.ctx, `SELECT status FROM security_findings WHERE id=$1::uuid`, f.findings[0]).Scan(&findingStatus))
	if findingStatus != "in_progress" {
		t.Fatalf("risk reopen did not restore finding: %s", findingStatus)
	}
	// Request-time checks apply even without a scheduler.
	_, err = f.pool.Exec(f.ctx, `UPDATE remediation_grants SET valid_from=now()-interval '2 days',valid_until=now()-interval '1 day' WHERE id=$1::uuid`, f.grant)
	must(t, err)
	if _, err = f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("expired grant read: %v", err)
	}
}

func TestScopeChangesAndLegacyFindingWriteGuard(t *testing.T) {
	f := setup(t)
	id := f.assign(t, 0, "scope-change-key", f.grant)
	_, err := f.pool.Exec(f.ctx, `UPDATE security_findings SET status='fixed' WHERE id=$1::uuid`, f.findings[0])
	if err == nil {
		t.Fatal("legacy raw finding write bypassed task workflow")
	}
	var otherOrg string
	must(t, f.pool.QueryRow(f.ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'其他组织','subsidiary') RETURNING id::text`, "OTHER_"+time.Now().Format("150405.000000000")).Scan(&otherOrg))
	if _, err = f.s.SaveVendor(f.ctx, f.manager, "", otherOrg, "越权厂商", "", "enabled"); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("cross-org vendor access: %v", err)
	}
	_, err = f.pool.Exec(f.ctx, `UPDATE business_systems SET owner_organization_id=$2::uuid WHERE id=$1::uuid`, f.system, otherOrg)
	must(t, err)
	if _, err = f.s.Detail(f.ctx, f.worker, id, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("system transfer retained executor access: %v", err)
	}
	detail, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	if detail["needs_reassignment"] != true {
		t.Fatal("administrator not warned of scope change")
	}
}

// An asset soft delete must not silently kill open remediation work, and must not
// leave the assignee working a target that no longer exists either. The task is
// retired (kept for audit, blocked for the executor, surfaced to the reviewer),
// and a restore brings it back without resurrecting work that was settled while
// the asset was gone.
func TestAssetRetirementRetiresThenRestoresRemediationTask(t *testing.T) {
	f := setup(t)

	// Give the finding a formal asset before assigning remediation. This keeps
	// the normal finding/task integrity trigger happy and makes the test exercise
	// the real asset delete path rather than a fixture-only UPDATE.
	var assetID string
	must(t, f.pool.QueryRow(f.ctx, `
		INSERT INTO assets(asset_type,asset_value,normalized_value,status,created_by)
		VALUES('ip', $1, $1, 'active', $2::uuid) RETURNING id::text`, "10.20.30.40", f.group.UserID).Scan(&assetID))
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary)
		VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, f.org)
	must(t, err)
	_, err = f.pool.Exec(f.ctx, `
		UPDATE security_findings SET asset_id=$2::uuid WHERE id=$1::uuid`, f.findings[0], assetID)
	must(t, err)

	id := f.assign(t, 0, "asset-retire-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	before, err := f.s.Detail(f.ctx, f.worker, id, true)
	must(t, err)
	if before["asset_retired_at"] != nil {
		t.Fatal("a live asset produced a retirement mark")
	}

	// Soft delete through the real asset service so the retirement runs inside
	// the same transaction as the ledger delete.
	assets := asset.NewService(f.pool)
	must(t, assets.Delete(f.ctx, f.group, assetID, nil, "资产下线，核查关联漏洞", "asset-retire-delete", "127.0.0.1"))

	var deletedAt any
	must(t, f.pool.QueryRow(f.ctx, `SELECT deleted_at FROM assets WHERE id=$1::uuid`, assetID).Scan(&deletedAt))
	if deletedAt == nil {
		t.Fatal("asset was not soft deleted")
	}
	var taskStatus string
	var retirement any
	must(t, f.pool.QueryRow(f.ctx, `SELECT status, asset_retired_at FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&taskStatus, &retirement))
	if taskStatus != "in_progress" {
		t.Fatalf("task status after asset delete = %q; the delivery history must survive, not be cancelled", taskStatus)
	}
	if retirement == nil {
		t.Fatal("open task was not marked as retired after its asset was deleted")
	}
	var retirementEvents int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM remediation_task_events WHERE task_id=$1::uuid AND action='asset_retired'`, id).Scan(&retirementEvents))
	if retirementEvents != 1 {
		t.Fatalf("asset_retired events = %d, want 1", retirementEvents)
	}
	managed, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	if managed["needs_reassignment"] != true {
		t.Fatal("reviewer was not told the task needs reassignment after the asset was retired")
	}
	page, _ := pagination.Normalize(1, 50)
	pendingList, err := f.s.List(f.ctx, f.manager, remediation.Filter{NeedsReassignment: true, Page: page})
	must(t, err)
	found := false
	for _, row := range pendingList.Items {
		if row["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatal("the retired task is missing from the needs-reassignment queue")
	}

	// The assignee must not be able to keep delivering against a deleted asset.
	detail, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	target := time.Now().Add(time.Hour)
	_, err = f.s.Act(f.ctx, f.worker, id, "submit", "retired-submit-key", remediation.ActionInput{
		ExpectedVersion: int(detail["version"].(float64)), Summary: "继续修复", DeploymentStatus: "target",
		ChangeReference: "release-x", DeployedAt: &target, SelfTestResult: "自测通过",
	}, true)
	if !errors.Is(err, remediation.ErrConflict) {
		t.Fatalf("executor submit on a retired task = %v, want ErrConflict", err)
	}

	// Restoring the asset brings the task back without rewriting its history.
	must(t, recyclebin.NewService(f.pool).RestoreAsset(f.ctx, f.group, assetID, "asset-retire-restore", "127.0.0.1"))
	var revived any
	must(t, f.pool.QueryRow(f.ctx, `SELECT asset_retired_at FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&revived))
	if revived != nil {
		t.Fatal("restoring the asset did not clear the retirement mark")
	}
	restored, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	if restored["asset_retired_at"] != nil {
		t.Fatal("restored task still reports a retirement")
	}
	// Delivery can resume after restoration.
	now := time.Now()
	f.action(t, f.worker, id, "submit", remediation.ActionInput{Summary: "恢复后提交", DeploymentStatus: "target", ChangeReference: "release-y", DeployedAt: &now, SelfTestResult: "自测通过"}, true)
}

// A business system soft delete must retire its open remediation work exactly
// like an asset soft delete does, and restoring it must bring the work back
// without resurrecting anything that was already cancelled.
func TestBusinessSystemRetirementRetiresAndRestoresTasks(t *testing.T) {
	f := setup(t)
	systems := businesssystem.NewService(f.pool)
	// Give the fixture's first finding a real asset so the system retirement test
	// covers the same shape production has: a task targeting a published system
	// whose assets are already being taken out of service.
	var assetID string
	// queryBusinessSystem inner-joins the data-security profile, so a system built
	// by raw SQL needs its row or the delete path reports "not found".
	_, err := f.pool.Exec(f.ctx, `INSERT INTO business_system_data_security_profiles(business_system_id) VALUES($1::uuid) ON CONFLICT DO NOTHING`, f.system)
	must(t, err)
	must(t, f.pool.QueryRow(f.ctx, `INSERT INTO assets(asset_type,asset_value,normalized_value,status,created_by)
		VALUES('ip',$1,$1,'active',$2::uuid) RETURNING id::text`, fmt.Sprintf("10.20.%d.%d", os.Getpid()%250, time.Now().UnixNano()%250), f.group.UserID).Scan(&assetID))
	_, err = f.pool.Exec(f.ctx, `INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary) VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, f.org)
	must(t, err)
	_, err = f.pool.Exec(f.ctx, `INSERT INTO system_assets(asset_id,business_system_id,is_primary) VALUES($1::uuid,$2::uuid,true)`, assetID, f.system)
	must(t, err)
	// The throwaway asset belongs to the system only so the delete has a retired
	// member; the task under test stays asset-less, so its only retirement reason
	// can be the system one.
	id := f.assign(t, 0, "system-retire-key", f.grant)
	f.action(t, f.worker, id, "accept", remediation.ActionInput{}, true)
	cancelled := f.assign(t, 1, "system-retire-cancelled", f.otherGrant)
	f.action(t, f.manager, cancelled, "cancel", remediation.ActionInput{Reason: "重复立项"}, false)

	// The bootstrap admin is the only principal that deletes a published system
	// outright; organization roles go through the working inventory instead.
	admin := f.group
	admin.Roles = []string{auth.RoleGroupAdmin}
	admin.Permissions = append(admin.Permissions, "system.manage")
	if err := systems.Delete(f.ctx, admin, f.system, nil, "system-retire-delete", "127.0.0.1"); !errors.Is(err, businesssystem.ErrInUse) {
		t.Fatalf("system delete with a live linked asset = %v, want ErrInUse; a system with work in flight must be deleted from a system whose assets are already retired", err)
	}
	// Retire the linked asset first: the fixture's finding belongs to it, so the
	// asset goes through the same soft delete production would use.
	assets := asset.NewService(f.pool)
	err = assets.Delete(f.ctx, f.group, assetID, nil, "系统下线前先退役资产", "system-retire-asset", "127.0.0.1")
	must(t, err)
	if err := systems.Delete(f.ctx, admin, f.system, nil, "system-retire-delete", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}

	var retirement any
	var taskStatus string
	must(t, f.pool.QueryRow(f.ctx, `SELECT status, system_retired_at FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&taskStatus, &retirement))
	if taskStatus != "in_progress" || retirement == nil {
		t.Fatalf("open task after system delete = status %q retired %v; history must survive and be marked retired", taskStatus, retirement)
	}
	var retiredEvents int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM remediation_task_events WHERE task_id=$1::uuid AND action='system_retired'`, id).Scan(&retiredEvents))
	if retiredEvents != 1 {
		t.Fatalf("system_retired events = %d, want 1", retiredEvents)
	}
	managed, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	if managed["needs_reassignment"] != true {
		t.Fatal("reviewer was not warned the task needs reassignment after its system was deleted")
	}
	if managed["can_execute"] != false || managed["execution_blocked_reason"] != "system_retired" {
		t.Fatalf("can_execute=%v reason=%v, want false/system_retired", managed["can_execute"], managed["execution_blocked_reason"])
	}
	page, _ := pagination.Normalize(1, 50)
	queue, err := f.s.List(f.ctx, f.manager, remediation.Filter{NeedsReassignment: true, Page: page})
	must(t, err)
	found := false
	for _, row := range queue.Items {
		if row["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatal("the system-retired task is missing from the needs-reassignment queue")
	}

	// Every delivery action must be refused with a reason, not just the one the
	// asset-retirement test happens to cover.
	detail, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	version := int(detail["version"].(float64))
	target := time.Now().Add(time.Hour)
	for _, tc := range []struct {
		action string
		in     remediation.ActionInput
	}{
		{"accept", remediation.ActionInput{ExpectedVersion: version, ExpectedCompletion: &target}},
		{"progress", remediation.ActionInput{ExpectedVersion: version, ExpectedCompletion: &target}},
		{"submit", remediation.ActionInput{ExpectedVersion: version, Summary: "继续修复", DeploymentStatus: "target", ChangeReference: "release-z", DeployedAt: &target, SelfTestResult: "自测通过"}},
		{"request.create", remediation.ActionInput{ExpectedVersion: version, Reason: "申请延期", BlockedDependency: "等待窗口", BlockedUntil: &target}},
	} {
		if _, err = f.s.Act(f.ctx, f.worker, id, tc.action, "system-retire-"+tc.action, tc.in, true); !errors.Is(err, remediation.ErrConflict) {
			t.Fatalf("%s on a system-retired task = %v, want ErrConflict", tc.action, err)
		}
	}

	must(t, recyclebin.NewService(f.pool).RestoreBusinessSystem(f.ctx, f.group, f.system, "system-retire-restore", "127.0.0.1"))
	var restoredEvents int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM remediation_task_events WHERE task_id=$1::uuid AND action='system_restored'`, id).Scan(&restoredEvents))
	if restoredEvents != 1 {
		t.Fatalf("system_restored events = %d, want 1", restoredEvents)
	}
	var stillRetired any
	must(t, f.pool.QueryRow(f.ctx, `SELECT system_retired_at FROM remediation_tasks WHERE id=$1::uuid`, id).Scan(&stillRetired))
	if stillRetired != nil {
		t.Fatal("restoring the system did not clear the retirement mark")
	}
	// A restored system comes back archived, so the task stays unexecutable until
	// the group reactivates it. Retiring is not the same as resuming.
	restored, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	if restored["can_execute"] != false || restored["execution_blocked_reason"] != "system_unavailable" {
		t.Fatalf("archived system leaves can_execute=%v reason=%v, want false/system_unavailable", restored["can_execute"], restored["execution_blocked_reason"])
	}
	_, err = f.pool.Exec(f.ctx, `UPDATE business_systems SET system_status='running' WHERE id=$1::uuid`, f.system)
	must(t, err)
	live, err := f.s.Detail(f.ctx, f.manager, id, false)
	must(t, err)
	if live["can_execute"] != true {
		t.Fatalf("reactivated system leaves can_execute=%v (reason %v), want true", live["can_execute"], live["execution_blocked_reason"])
	}
	now := time.Now()
	f.action(t, f.worker, id, "submit", remediation.ActionInput{Summary: "恢复后提交", DeploymentStatus: "target", ChangeReference: "release-a", DeployedAt: &now, SelfTestResult: "自测通过"}, true)

	// A cancelled task is history, not interrupted work: restoring the system
	// must never bring it back into the active queue.
	var cancelledStatus string
	var cancelledRetired any
	must(t, f.pool.QueryRow(f.ctx, `SELECT status, system_retired_at FROM remediation_tasks WHERE id=$1::uuid`, cancelled).Scan(&cancelledStatus, &cancelledRetired))
	if cancelledStatus != "cancelled" || cancelledRetired != nil {
		t.Fatalf("cancelled task after system restore = status %q retired %v; it must stay cancelled", cancelledStatus, cancelledRetired)
	}
}

// An assignable list must only offer findings the assign action will accept.
// Assign refuses a finding whose asset is soft deleted, so a row that lingers in
// the picker is a guaranteed failure at the moment of selection.
func TestAssignableFindingsHideSoftDeletedAssets(t *testing.T) {
	f := setup(t)
	page, _ := pagination.Normalize(1, 50)
	var assetID string
	must(t, f.pool.QueryRow(f.ctx, `
		INSERT INTO assets(asset_type,asset_value,normalized_value,status,created_by)
		VALUES('ip',$1,$1,'active',$2::uuid) RETURNING id::text`,
		fmt.Sprintf("10.30.%d.%d", os.Getpid()%250, time.Now().UnixNano()%250), f.group.UserID).Scan(&assetID))
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO asset_organizations(asset_id,organization_id,relationship_type,is_primary)
		VALUES($1::uuid,$2::uuid,'owner',true)`, assetID, f.org)
	must(t, err)
	_, err = f.pool.Exec(f.ctx, `UPDATE security_findings SET asset_id=$2::uuid WHERE id=$1::uuid`, f.findings[1], assetID)
	must(t, err)

	before, err := f.s.AssignableFindings(f.ctx, f.manager, f.order, page)
	must(t, err)
	if !assignableHas(before.Items, f.findings[1]) {
		t.Fatal("a live asset's finding is missing from the assignable list")
	}
	must(t, asset.NewService(f.pool).Delete(f.ctx, f.group, assetID, nil, "资产下线", "assignable-hide", "127.0.0.1"))
	after, err := f.s.AssignableFindings(f.ctx, f.manager, f.order, page)
	must(t, err)
	if assignableHas(after.Items, f.findings[1]) {
		t.Fatal("the assignable list still offers a finding whose asset is deleted")
	}
}

func assignableHas(items []remediation.Object, id string) bool {
	for _, item := range items {
		if item["id"] == id {
			return true
		}
	}
	return false
}

// Migration 00075 replaced a mis-specified index: the retired-task lookup was
// created on asset_id while every query filters on finding_id, so it was never
// used and the intended lookup stayed a sequential scan.
func TestRetiredTaskLookupIndexIsKeyedByFinding(t *testing.T) {
	f := setup(t)
	var columns string
	must(t, f.pool.QueryRow(f.ctx, `
		SELECT string_agg(a.attname, ',' ORDER BY a.attname)
		FROM pg_class idx
		JOIN pg_index i ON i.indexrelid = idx.oid
		JOIN pg_class tbl ON tbl.oid = i.indrelid
		JOIN pg_attribute a ON a.attrelid = tbl.oid AND a.attnum = ANY(i.indkey)
		WHERE idx.relname = 'remediation_tasks_finding_retired_idx'`).Scan(&columns))
	if columns != "finding_id" {
		t.Fatalf("remediation_tasks_finding_retired_idx covers %q, want finding_id", columns)
	}
	var stale int
	must(t, f.pool.QueryRow(f.ctx, `SELECT count(*) FROM pg_class WHERE relname = 'remediation_tasks_asset_retired_idx'`).Scan(&stale))
	if stale != 0 {
		t.Fatal("the mis-specified remediation_tasks_asset_retired_idx is still present")
	}
}
