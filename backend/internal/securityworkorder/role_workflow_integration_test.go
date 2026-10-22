package securityworkorder

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/asset"
	"netassets/internal/businesssystem"
	"netassets/internal/inventorytask"
	"netassets/internal/pagination"
)

// TestRoleWorkflowMainline deliberately uses the production services in one
// transactionally independent scenario. The smaller package tests prove each
// state transition; this test proves the hand-off between the four people who
// own the real process: group administrator, group security administrator,
// organization administrator and organization reporter.
func TestRoleWorkflowMainline(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset fixtures: %v", err)
	}

	p03Bootstrap(t, pool, "ROLE_FLOW")
	groupID := p03InsertOrg(t, pool, "", "ROLE_FLOW_GROUP", "角色流程集团", "group")
	orgID := p03InsertOrg(t, pool, groupID, "ROLE_FLOW_SUB", "角色流程子公司", "subsidiary")
	groupAdminID := p03InsertUser(t, pool, "role_flow_group_admin", "集团管理员")
	securityID := p03InsertUser(t, pool, "role_flow_security", "集团安全管理员")
	orgAdminID := p03InsertUser(t, pool, "role_flow_org_admin", "组织管理员")
	editorID := p03InsertUser(t, pool, "role_flow_editor", "组织填报员")
	p03AssignRole(t, pool, groupAdminID, "group_admin", "", "all")
	p03AssignRole(t, pool, securityID, "security_operator", "", "all")
	p03AssignRole(t, pool, orgAdminID, "organization_admin", orgID, "organization")
	p03AssignRole(t, pool, editorID, "organization_editor", orgID, "organization")
	groupAdmin := p03Principal(t, pool, groupAdminID, []string{"group_admin"})
	securityAdmin := p03Principal(t, pool, securityID, []string{"security_operator"})
	orgAdmin := p03Principal(t, pool, orgAdminID, []string{"organization_admin"})
	editor := p03Principal(t, pool, editorID, []string{"organization_editor"})

	tasks := inventorytask.NewService(pool)
	findings := NewService(pool, t.TempDir())

	// 1. Group administration creates and launches the reporting task. Security
	// administration is intentionally not allowed to change reporting scope.
	task, err := tasks.Create(ctx, groupAdmin, inventorytask.CreateInput{
		Name: "角色流程资产填报", Description: "验证全角色交接", StartAt: time.Now().Add(-time.Hour),
		DeadlineAt: time.Now().Add(24 * time.Hour), Status: "active", AssetNetworkScope: "all",
		RequiredFields: []string{"business_systems", "network_assets"}, RequireReview: true, AllowLateSubmission: true, OrganizationIDs: []string{orgID},
	}, "role-flow-task", "192.168.8.6")
	if err != nil {
		t.Fatalf("group admin creates task: %v", err)
	}
	if _, err := tasks.Create(ctx, securityAdmin, inventorytask.CreateInput{Name: "越权任务", StartAt: time.Now().Add(-time.Hour), DeadlineAt: time.Now().Add(time.Hour), OrganizationIDs: []string{orgID}}, "role-flow-denied", "192.168.8.6"); !errors.Is(err, inventorytask.ErrForbidden) {
		t.Fatalf("security admin task creation error = %v, want forbidden", err)
	}
	if _, err := tasks.Start(ctx, orgAdmin, task.ID, orgID, "role-flow-start", "192.168.8.6"); err != nil {
		t.Fatalf("organization admin starts task: %v", err)
	}

	// 2. The reporter creates both kinds of inventory working versions.
	// Business-system publication must precede the asset ledger publication in
	// the same review transaction.
	if _, err := tasks.CreateBusinessSystemDraft(ctx, editor, task.ID, orgID, inventorytask.BusinessSystemDraftInput{
		CreateInput: businesssystem.CreateInput{
			Code: "ROLE_FLOW_SYS", Name: "角色流程业务系统", OrganizationID: orgID, Status: "running", Level: "important",
			InternetExposure: "yes", ContainsPersonalInformation: "no", ContainsSensitivePersonalInformation: "no",
			DataVolumeOver100K: "no", ContainsImportantData: "yes", ContainsCoreData: "no",
		},
	}, "role-flow-system", "192.168.8.6"); err != nil {
		t.Fatalf("organization editor creates business-system draft: %v", err)
	}
	draft, err := tasks.CreateAssetDraft(ctx, editor, task.ID, orgID, asset.CreateInput{
		Type: "ip", Value: "198.51.100.88", Name: "角色流程资产", OrganizationID: orgID,
		Environment: "production", NetworkScope: "public", Status: "active", Importance: "important", OwnerName: "角色流程子公司",
	}, "role-flow-asset", "192.168.8.6")
	if err != nil {
		t.Fatalf("organization editor creates asset draft: %v", err)
	}
	if _, err := tasks.CreateAssetDraft(ctx, orgAdmin, task.ID, orgID, asset.CreateInput{Type: "ip", Value: "198.51.100.89", Name: "管理员资产", OrganizationID: orgID, Environment: "production", NetworkScope: "public", Status: "active", Importance: "normal"}, "role-flow-admin-asset", "192.168.8.6"); err != nil {
		t.Fatalf("organization admin should be able to create a reporting draft: %v", err)
	}
	if _, err := findings.CreateFinding(ctx, editor, FindingInput{Title: "填报员不得录入漏洞", Severity: "high", Status: "confirmed", AssigneeOrganizationID: orgID, Source: "manual", DiscoveredAt: time.Now()}, "role-flow-finding-denied", "192.168.8.6"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("organization editor finding creation error = %v, want forbidden: %v", err, err)
	}
	if _, err := tasks.Submit(ctx, editor, task.ID, orgID, inventorytask.SubmissionInput{Note: "填报完成"}, "role-flow-submit", "192.168.8.6"); err != nil {
		t.Fatalf("organization editor submits task: %v", err)
	}
	progress, err := tasks.Review(ctx, groupAdmin, task.ID, orgID, inventorytask.ReviewInput{Decision: "approved"}, "role-flow-review", "192.168.8.6")
	if err != nil {
		t.Fatalf("group admin approves task: %v", err)
	}
	if progress.Status != "approved" {
		t.Fatalf("review status = %q, want approved", progress.Status)
	}

	var formalSystemID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM business_systems WHERE system_code='ROLE_FLOW_SYS' AND deleted_at IS NULL`).Scan(&formalSystemID); err != nil {
		t.Fatalf("published business system missing: %v", err)
	}
	var formalAssetID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM assets WHERE normalized_value='198.51.100.88' AND deleted_at IS NULL`).Scan(&formalAssetID); err != nil {
		t.Fatalf("published asset missing: %v", err)
	}
	if formalAssetID != draft.ID {
		t.Fatalf("published asset id = %q, want working-version identity %q", formalAssetID, draft.ID)
	}

	// 3. Group security administration records the finding against the newly
	// published asset, and issues a work order. Organization users can execute
	// only their own issued order; they cannot issue one themselves.
	finding, err := findings.CreateFinding(ctx, securityAdmin, FindingInput{Title: "角色流程高危漏洞", Severity: "high", Status: "confirmed", AssigneeOrganizationID: orgID, AssetID: formalAssetID, Description: "角色流程验证", Source: "manual", DiscoveredAt: time.Now()}, "role-flow-finding", "192.168.8.6")
	if err != nil {
		t.Fatalf("security admin creates finding: %v", err)
	}
	if _, err := findings.CreateWorkOrder(ctx, editor, WorkOrderInput{Title: "越权下发", AssigneeOrganizationID: orgID, DueAt: time.Now().Add(24 * time.Hour), FindingIDs: []string{finding.ID}}, "role-flow-wo-denied", "192.168.8.6"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("organization editor work order creation error = %v, want forbidden", err)
	}
	order, err := findings.CreateWorkOrder(ctx, securityAdmin, WorkOrderInput{Title: "角色流程安全工单", AssigneeOrganizationID: orgID, Description: "请修复漏洞", RemediationRequirement: "完成修复并提交证据", DueAt: time.Now().Add(24 * time.Hour), FindingIDs: []string{finding.ID}}, "role-flow-wo", "192.168.8.6")
	if err != nil {
		t.Fatalf("security admin creates work order: %v", err)
	}
	if _, err := findings.Issue(ctx, orgAdmin, order.ID, CommentInput{Comment: "组织管理员不得发放"}, "role-flow-issue-denied", "192.168.8.6"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("organization admin issue error = %v, want forbidden", err)
	}
	if _, err := findings.Issue(ctx, securityAdmin, order.ID, CommentInput{Comment: "集团安全管理员下发"}, "role-flow-issue", "192.168.8.6"); err != nil {
		t.Fatalf("security admin issues work order: %v", err)
	}
	visible, err := findings.SearchWorkOrdersWithFilter(ctx, orgAdmin, WorkOrderSearchFilter{OrganizationID: orgID, Page: pagination.Query{Page: 1, PageSize: 20}})
	if err != nil {
		t.Fatalf("organization admin lists issued orders: %v", err)
	}
	if visible.Total != 1 || visible.Items[0].ID != order.ID {
		t.Fatalf("organization order visibility = total %d items %#v", visible.Total, visible.Items)
	}

	if _, err := findings.Accept(ctx, orgAdmin, order.ID, CommentInput{Comment: "已接收"}, "role-flow-accept", "192.168.8.6"); err != nil {
		t.Fatalf("organization admin accepts order: %v", err)
	}
	// The reporter's responsibility ends at inventory submission. Security
	// remediation is an organization-admin responsibility; the reporter must
	// not be granted work-order execution merely because it can edit assets.
	if _, err := findings.Progress(ctx, editor, order.ID, ProgressInput{Progress: "越权执行", Comment: "填报员不得执行工单"}, "role-flow-progress-denied", "192.168.8.6"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("organization editor progress error = %v, want forbidden", err)
	}
	if _, err := findings.Progress(ctx, orgAdmin, order.ID, ProgressInput{Progress: "正在修复", Comment: "组织管理员执行"}, "role-flow-progress", "192.168.8.6"); err != nil {
		t.Fatalf("organization admin progresses order: %v", err)
	}
	if _, err := findings.Submit(ctx, orgAdmin, order.ID, SubmitInput{RemediationResult: "已修复，提交验证", Comment: "完成整改"}, "role-flow-submit-remediation", "192.168.8.6"); err != nil {
		t.Fatalf("organization admin submits remediation: %v", err)
	}
	if _, err := findings.Review(ctx, securityAdmin, order.ID, ReviewInput{Decision: "closed", VerificationResult: "验证通过"}, "role-flow-close", "192.168.8.6"); err != nil {
		t.Fatalf("security admin closes work order: %v", err)
	}
	closed, err := findings.GetWorkOrder(ctx, securityAdmin, order.ID)
	if err != nil {
		t.Fatalf("reload closed work order: %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("closed work order status = %q", closed.Status)
	}
	finalFinding, err := findings.getFinding(ctx, finding.ID)
	if err != nil {
		t.Fatalf("reload finding: %v", err)
	}
	if finalFinding.Status != "fixed" {
		t.Fatalf("finding status after verified remediation = %q, want fixed", finalFinding.Status)
	}
}
