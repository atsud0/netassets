package remediation_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/bootstrap"
	"netassets/internal/config"
	"netassets/internal/pagination"
	"netassets/internal/remediation"
	"netassets/internal/securityworkorder"
)

// roleMatrixFixture carries the five audited identities plus the data they all
// compete for: one organization-level and one individual work order.
type roleMatrixFixture struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	orders  *securityworkorder.Service
	repairs *remediation.Service

	groupAdmin, securityAdmin, orgAdmin, orgEditor, vendor auth.Principal

	orgA, orgB        string
	orderID           string
	individualOrderID string
	findingID         string
	riskFindingID     string
	draftFindingID    string
	remediationTaskID string
}

func setupRoleMatrix(t *testing.T) *roleMatrixFixture {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	suffix := strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", "")

	cfg := config.Config{
		BootstrapAdminUsername:    "role_matrix_root_" + suffix,
		BootstrapAdminPassword:    "Role-Matrix-Password-123",
		BootstrapAdminDisplayName: "集团安全管理员",
		BootstrapGroupCode:        "ROLE_MATRIX_GROUP_" + suffix,
		BootstrapGroupName:        "角色矩阵测试集团",
	}
	if err := bootstrap.Ensure(ctx, pool, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	login, err := auth.NewService(pool, time.Hour).Login(ctx, auth.LoginInput{
		Username: cfg.BootstrapAdminUsername, Password: cfg.BootstrapAdminPassword,
	})
	if err != nil {
		t.Fatalf("login bootstrap admin: %v", err)
	}

	f := &roleMatrixFixture{ctx: ctx, pool: pool, orders: securityworkorder.NewService(pool, t.TempDir()), repairs: remediation.NewService(pool, t.TempDir())}
	f.securityAdmin = login.User

	// A second group-level identity: group_admin carries every permission through
	// its role, so its principal must carry the role rather than a permission list.
	groupAdmin := createRoleMatrixUser(t, pool, "rm_group_admin_"+suffix)
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, data_scope)
		SELECT $1::uuid, id, 'all' FROM roles WHERE role_key='group_admin'
	`, groupAdmin); err != nil {
		t.Fatalf("assign group admin role: %v", err)
	}
	groupAdminPermissions, err := roleMatrixPermissions(ctx, pool, groupAdmin)
	if err != nil {
		t.Fatalf("load group admin permissions: %v", err)
	}
	f.groupAdmin = auth.Principal{UserID: groupAdmin, Roles: []string{"group_admin"}, Permissions: groupAdminPermissions}

	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ($1,'角色矩阵子公司 A','subsidiary') RETURNING id::text`, "RM_A_"+suffix).Scan(&f.orgA); err != nil {
		t.Fatalf("insert organization A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ($1,'角色矩阵子公司 B','subsidiary') RETURNING id::text`, "RM_B_"+suffix).Scan(&f.orgB); err != nil {
		t.Fatalf("insert organization B: %v", err)
	}
	systemA := createRoleMatrixSystem(t, pool, f.orgA, suffix)
	systemB := createRoleMatrixSystem(t, pool, f.orgB, suffix)

	f.orgAdmin = createRoleMatrixScopedPrincipal(t, pool, "rm_org_admin_"+suffix, "organization_admin", f.orgA)
	f.orgEditor = createRoleMatrixScopedPrincipal(t, pool, "rm_org_editor_"+suffix, "organization_editor", f.orgA)

	finding, err := f.orders.CreateFinding(ctx, f.securityAdmin, securityworkorder.FindingInput{
		Title: "角色矩阵漏洞", Severity: "high", Status: "confirmed",
		AssigneeOrganizationID: f.orgA, BusinessSystemID: systemA,
		Description: "用于验证五类角色的接口边界", Evidence: "脱敏证据", Source: "manual",
		DiscoveredAt: time.Now(),
	}, "", "")
	if err != nil {
		t.Fatalf("create finding: %v", err)
	}
	f.findingID = finding.ID

	// Risk acceptance and the draft-order case operate on findings that are not
	// already attached to an open work order: the 00052 guard rejects any
	// personal-remediation write before authorization is considered.
	unassignedFinding := func(title string) string {
		t.Helper()
		created, err := f.orders.CreateFinding(ctx, f.securityAdmin, securityworkorder.FindingInput{
			Title: title, Severity: "medium", Status: "confirmed",
			AssigneeOrganizationID: f.orgA, BusinessSystemID: systemA,
			Description: "未挂接工单的独立漏洞", Source: "manual", DiscoveredAt: time.Now(),
		}, "", "")
		if err != nil {
			t.Fatalf("create unassigned finding %s: %v", title, err)
		}
		return created.ID
	}
	f.riskFindingID = unassignedFinding("风险接受专用漏洞")
	f.draftFindingID = unassignedFinding("未下发工单专用漏洞")

	order, err := f.orders.CreateWorkOrder(ctx, f.securityAdmin, securityworkorder.WorkOrderInput{
		ExecutionMode: "organization", Title: "角色矩阵组织级工单", AssigneeOrganizationID: f.orgA,
		RemediationRequirement: "限时整改", DueAt: time.Now().Add(48 * time.Hour),
		FindingIDs: []string{finding.ID},
	}, "", "")
	if err != nil {
		t.Fatalf("create organization work order: %v", err)
	}
	f.orderID = order.ID

	findingB, err := f.orders.CreateFinding(ctx, f.securityAdmin, securityworkorder.FindingInput{
		Title: "个人任务漏洞", Severity: "high", Status: "confirmed",
		AssigneeOrganizationID: f.orgB, BusinessSystemID: systemB,
		Description: "用于验证 individual 工单隔离", Source: "manual", DiscoveredAt: time.Now(),
	}, "", "")
	if err != nil {
		t.Fatalf("create second finding: %v", err)
	}
	individual, err := f.orders.CreateWorkOrder(ctx, f.securityAdmin, securityworkorder.WorkOrderInput{
		ExecutionMode: "individual", Title: "角色矩阵个人工单", AssigneeOrganizationID: f.orgB,
		RemediationRequirement: "个人任务整改", DueAt: time.Now().Add(48 * time.Hour),
		FindingIDs: []string{findingB.ID},
	}, "", "")
	if err != nil {
		t.Fatalf("create individual work order: %v", err)
	}
	f.individualOrderID = individual.ID
	if _, err := f.orders.Issue(ctx, f.securityAdmin, f.individualOrderID, securityworkorder.CommentInput{}, "", ""); err != nil {
		t.Fatalf("issue individual work order: %v", err)
	}

	orgAdminB := createRoleMatrixScopedPrincipal(t, pool, "rm_org_admin_b_"+suffix, "organization_admin", f.orgB)
	vendor, err := f.repairs.SaveVendor(ctx, f.securityAdmin, "", f.orgB, "角色矩阵厂商_"+suffix, "厂商联系人", "enabled")
	if err != nil {
		t.Fatalf("save vendor: %v", err)
	}
	person, err := f.repairs.CreatePerson(ctx, orgAdminB, remediation.PersonInput{
		OrganizationID: f.orgB, SystemID: systemB, ParticipantType: "external",
		VendorID: vendor["id"].(string), Username: "rm_vendor_" + suffix,
		DisplayName: "外部厂商执行人", Password: "Vendor-Worker-Password-123",
		ValidUntil: time.Now().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("create external vendor person: %v", err)
	}
	f.vendor = auth.Principal{UserID: person["user_id"].(string)}

	task, err := f.repairs.Assign(ctx, orgAdminB, f.individualOrderID, "findings/"+findingB.ID, remediation.AssignInput{
		FindingID: findingB.ID, GrantID: person["id"].(string), DueAt: time.Now().Add(24 * time.Hour),
		TargetEnvironment: "生产", DeploymentRequired: true,
		Material: remediation.Material{Title: "对外漏洞材料", Severity: "high", Description: "已脱敏描述", Evidence: "可公开复现步骤", Requirement: "部署生产并验证"},
	})
	if err != nil {
		t.Fatalf("assign remediation task: %v", err)
	}
	f.remediationTaskID = task["id"].(string)
	return f
}

func createRoleMatrixUser(t *testing.T, pool *pgxpool.Pool, username string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, password_hash, display_name) VALUES ($1,'not-used',$1) RETURNING id::text`,
		username).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func createRoleMatrixSystem(t *testing.T, pool *pgxpool.Pool, organizationID, suffix string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO business_systems (system_code, system_name, owner_organization_id, system_status)
		VALUES ($1,'角色矩阵系统',$2::uuid,'running') RETURNING id::text`,
		"RM_SYS_"+suffix+"_"+organizationID[:8], organizationID).Scan(&id); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	return id
}

// createRoleMatrixScopedPrincipal grants a built-in system role inside exactly one
// organization and returns a principal carrying the permissions that role holds.
func createRoleMatrixScopedPrincipal(t *testing.T, pool *pgxpool.Pool, username, roleKey, organizationID string) auth.Principal {
	t.Helper()
	userID := createRoleMatrixUser(t, pool, username)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope)
		SELECT $1::uuid, id, $3::uuid, 'organization' FROM roles WHERE role_key = $2`,
		userID, roleKey, organizationID); err != nil {
		t.Fatalf("assign %s: %v", roleKey, err)
	}
	permissions, err := roleMatrixPermissions(context.Background(), pool, userID)
	if err != nil {
		t.Fatalf("load %s permissions: %v", roleKey, err)
	}
	return auth.Principal{UserID: userID, Permissions: permissions}
}

func roleMatrixPermissions(ctx context.Context, pool *pgxpool.Pool, userID string) ([]string, error) {
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status='enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		WHERE ur.user_id = $1::uuid`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var permissions []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		permissions = append(permissions, key)
	}
	return permissions, rows.Err()
}

// assertRoleDenied is the shared "must not reach the data" assertion. A denied
// call must be a stable authorization error, never a partial success and never a
// leak disguised as an empty result.
func assertRoleDenied(t *testing.T, operation string, err error) {
	t.Helper()
	if !errors.Is(err, securityworkorder.ErrForbidden) && !errors.Is(err, securityworkorder.ErrNotFound) {
		t.Fatalf("%s error = %v, want a forbidden/not-found denial", operation, err)
	}
}

func TestRoleMatrixOrganizationWorkOrderReadBoundary(t *testing.T) {
	f := setupRoleMatrix(t)
	page, _ := pagination.Normalize(1, 50)

	// Group roles read across organizations.
	for name, principal := range map[string]auth.Principal{"group_admin": f.groupAdmin, "security_operator": f.securityAdmin} {
		if _, err := f.orders.GetWorkOrder(f.ctx, principal, f.orderID); err != nil {
			t.Fatalf("%s GetWorkOrder error = %v", name, err)
		}
		result, err := f.orders.SearchWorkOrdersWithFilter(f.ctx, principal, securityworkorder.WorkOrderSearchFilter{Page: page})
		if err != nil {
			t.Fatalf("%s SearchWorkOrdersWithFilter error = %v", name, err)
		}
		if result.Total == 0 {
			t.Fatalf("%s work order list is empty; group roles must see all organizations", name)
		}
	}

	// A freshly created order is a draft. Even an organization role holding
	// security_workorder.view must not observe it before the group dispatches it.
	if _, err := f.orders.GetWorkOrder(f.ctx, f.orgAdmin, f.orderID); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("org admin GetWorkOrder(draft) error = %v, want securityworkorder.ErrForbidden", err)
	}
	drafts, err := f.orders.SearchWorkOrdersWithFilter(f.ctx, f.orgAdmin, securityworkorder.WorkOrderSearchFilter{Page: page})
	if err != nil {
		t.Fatalf("org admin SearchWorkOrdersWithFilter(draft) error = %v", err)
	}
	if drafts.Total != 0 || len(drafts.Items) != 0 {
		t.Fatalf("org admin draft list returned total %d items %d, want no rows before dispatch", drafts.Total, len(drafts.Items))
	}
	if _, err := f.orders.Issue(f.ctx, f.securityAdmin, f.orderID, securityworkorder.CommentInput{Comment: "集团下发"}, "", ""); err != nil {
		t.Fatalf("security admin Issue error = %v", err)
	}

	// Once dispatched, the organization administrator may read its own order.
	if _, err := f.orders.GetWorkOrder(f.ctx, f.orgAdmin, f.orderID); err != nil {
		t.Fatalf("org admin GetWorkOrder error = %v", err)
	}
	scoped, err := f.orders.SearchWorkOrdersWithFilter(f.ctx, f.orgAdmin, securityworkorder.WorkOrderSearchFilter{Page: page})
	if err != nil {
		t.Fatalf("org admin SearchWorkOrdersWithFilter error = %v", err)
	}
	if scoped.Total == 0 {
		t.Fatal("org admin list is empty after dispatch; the organization must see its own issued order")
	}
	for _, item := range scoped.Items {
		if item.AssigneeOrganizationID != f.orgA {
			t.Fatalf("org admin list leaked order %s of organization %s", item.ID, item.AssigneeOrganizationID)
		}
	}

	// Cross-organization reads must fail: the same endpoint, a different org.
	_, err = f.orders.SearchWorkOrdersWithFilter(f.ctx, f.orgAdmin, securityworkorder.WorkOrderSearchFilter{OrganizationID: f.orgB, Page: page})
	assertRoleDenied(t, "org admin list filtered by organization B", err)
	_, err = f.orders.GetWorkOrder(f.ctx, f.orgAdmin, f.individualOrderID)
	assertRoleDenied(t, "org admin GetWorkOrder(individual order of org B)", err)

	// The filler holds no security_workorder.* permission at all.
	if _, err := f.orders.GetWorkOrder(f.ctx, f.orgEditor, f.orderID); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("filler GetWorkOrder error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.SearchWorkOrderEvents(f.ctx, f.orgEditor, f.orderID, page); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("filler SearchWorkOrderEvents error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.SearchWorkOrderFindings(f.ctx, f.orgEditor, f.orderID, page); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("filler SearchWorkOrderFindings error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.SearchWorkOrderAttachments(f.ctx, f.orgEditor, f.orderID, page); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("filler SearchWorkOrderAttachments error = %v, want securityworkorder.ErrForbidden", err)
	}
}

func TestRoleMatrixOrganizationWorkOrderActions(t *testing.T) {
	f := setupRoleMatrix(t)
	due := time.Now().Add(24 * time.Hour)

	// issue is a group-level action: the organization administrator must not be
	// able to dispatch work into the organization from the legacy endpoint.
	_, err := f.orders.Issue(f.ctx, f.orgAdmin, f.orderID, securityworkorder.CommentInput{Comment: "越权下发"}, "", "")
	assertRoleDenied(t, "org admin Issue", err)
	_, err = f.orders.Issue(f.ctx, f.orgEditor, f.orderID, securityworkorder.CommentInput{Comment: "越权下发"}, "", "")
	assertRoleDenied(t, "filler Issue", err)

	if _, err := f.orders.Issue(f.ctx, f.securityAdmin, f.orderID, securityworkorder.CommentInput{Comment: "集团下发"}, "", ""); err != nil {
		t.Fatalf("security admin Issue error = %v", err)
	}

	// accept/progress/submit stay organization-scoped. The filler is refused even
	// though the organization is the one it belongs to.
	_, err = f.orders.Accept(f.ctx, f.orgEditor, f.orderID, securityworkorder.CommentInput{Comment: "填报员接收"}, "", "")
	assertRoleDenied(t, "filler Accept", err)
	if _, err := f.orders.Accept(f.ctx, f.orgAdmin, f.orderID, securityworkorder.CommentInput{Comment: "组织接收"}, "", ""); err != nil {
		t.Fatalf("org admin Accept error = %v", err)
	}

	_, err = f.orders.Progress(f.ctx, f.orgEditor, f.orderID, securityworkorder.ProgressInput{Progress: "填报员推进"}, "", "")
	assertRoleDenied(t, "filler Progress", err)
	if _, err := f.orders.Progress(f.ctx, f.orgAdmin, f.orderID, securityworkorder.ProgressInput{Progress: "组织推进"}, "", ""); err != nil {
		t.Fatalf("org admin Progress error = %v", err)
	}

	_, err = f.orders.Submit(f.ctx, f.orgEditor, f.orderID, securityworkorder.SubmitInput{RemediationResult: "填报员提交"}, "", "")
	assertRoleDenied(t, "filler Submit", err)
	if _, err := f.orders.Submit(f.ctx, f.orgAdmin, f.orderID, securityworkorder.SubmitInput{RemediationResult: "组织提交"}, "", ""); err != nil {
		t.Fatalf("org admin Submit error = %v", err)
	}

	// review/close are group-level. Even the organization administrator must not
	// be able to close its own remediation.
	if _, err := f.orders.Review(f.ctx, f.orgAdmin, f.orderID, securityworkorder.ReviewInput{Decision: "returned", ReturnReason: "材料不足"}, "", ""); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("org admin Review(return) error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.Review(f.ctx, f.orgAdmin, f.orderID, securityworkorder.ReviewInput{Decision: "closed", VerificationResult: "已复测通过"}, "", ""); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("org admin Review(closed) error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.Review(f.ctx, f.orgEditor, f.orderID, securityworkorder.ReviewInput{Decision: "closed", VerificationResult: "已复测通过"}, "", ""); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("filler Review error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.Review(f.ctx, f.securityAdmin, f.orderID, securityworkorder.ReviewInput{Decision: "returned", ReturnReason: "补充复测记录"}, "", ""); err != nil {
		t.Fatalf("security admin Review error = %v", err)
	}

	// cancel: group-only, regardless of who currently sits in the organization.
	_, err = f.orders.Cancel(f.ctx, f.orgAdmin, f.orderID, securityworkorder.CommentInput{Comment: "组织自行取消"}, "", "")
	assertRoleDenied(t, "org admin Cancel", err)

	// creation is a group-level action.
	_, err = f.orders.CreateWorkOrder(f.ctx, f.orgAdmin, securityworkorder.WorkOrderInput{
		ExecutionMode: "organization", Title: "越权创建", AssigneeOrganizationID: f.orgA,
		RemediationRequirement: "整改", DueAt: due, FindingIDs: []string{f.findingID},
	}, "", "")
	assertRoleDenied(t, "org admin CreateWorkOrder", err)
}

func TestRoleMatrixVendorCannotUseLegacyEndpoints(t *testing.T) {
	f := setupRoleMatrix(t)
	page, _ := pagination.Normalize(1, 50)

	// An external vendor participant holds no legacy work-order permission.
	if _, err := f.orders.GetWorkOrder(f.ctx, f.vendor, f.individualOrderID); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("vendor GetWorkOrder error = %v, want securityworkorder.ErrForbidden", err)
	}
	result, err := f.orders.SearchWorkOrdersWithFilter(f.ctx, f.vendor, securityworkorder.WorkOrderSearchFilter{Page: page})
	if err != nil {
		t.Fatalf("vendor SearchWorkOrdersWithFilter error = %v", err)
	}
	if result.Total != 0 || len(result.Items) != 0 {
		t.Fatalf("vendor work order list returned total %d items %d, want no rows", result.Total, len(result.Items))
	}
	// Naming an organization escalates from an empty scope to an explicit denial.
	_, err = f.orders.SearchWorkOrdersWithFilter(f.ctx, f.vendor, securityworkorder.WorkOrderSearchFilter{OrganizationID: f.orgB, Page: page})
	if !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("vendor SearchWorkOrdersWithFilter(org filter) error = %v, want securityworkorder.ErrForbidden", err)
	}
	for name, call := range map[string]func() error{
		"Accept": func() error {
			_, err := f.orders.Accept(f.ctx, f.vendor, f.individualOrderID, securityworkorder.CommentInput{}, "", "")
			return err
		},
		"Progress": func() error {
			_, err := f.orders.Progress(f.ctx, f.vendor, f.individualOrderID, securityworkorder.ProgressInput{Progress: "x"}, "", "")
			return err
		},
		"Submit": func() error {
			_, err := f.orders.Submit(f.ctx, f.vendor, f.individualOrderID, securityworkorder.SubmitInput{RemediationResult: "x"}, "", "")
			return err
		},
		"Review": func() error {
			_, err := f.orders.Review(f.ctx, f.vendor, f.individualOrderID, securityworkorder.ReviewInput{Decision: "closed", VerificationResult: "x"}, "", "")
			return err
		},
		"Cancel": func() error {
			_, err := f.orders.Cancel(f.ctx, f.vendor, f.individualOrderID, securityworkorder.CommentInput{Comment: "x"}, "", "")
			return err
		},
		"Issue": func() error {
			_, err := f.orders.Issue(f.ctx, f.vendor, f.individualOrderID, securityworkorder.CommentInput{}, "", "")
			return err
		},
	} {
		assertRoleDenied(t, "vendor "+name, call())
	}
}

func TestRoleMatrixIndividualOrderRejectsLegacyRemediation(t *testing.T) {
	f := setupRoleMatrix(t)
	orgAdminB := createRoleMatrixScopedPrincipal(t, f.pool, "rm_org_admin_b2_"+time.Now().Format("150405.000000"), "organization_admin", f.orgB)

	// Even with full organization execute permission, individual containers
	// refuse the legacy remediation endpoints.
	for name, call := range map[string]func() error{
		"Accept": func() error {
			_, err := f.orders.Accept(f.ctx, orgAdminB, f.individualOrderID, securityworkorder.CommentInput{}, "", "")
			return err
		},
		"Progress": func() error {
			_, err := f.orders.Progress(f.ctx, orgAdminB, f.individualOrderID, securityworkorder.ProgressInput{Progress: "x"}, "", "")
			return err
		},
		"Submit": func() error {
			_, err := f.orders.Submit(f.ctx, orgAdminB, f.individualOrderID, securityworkorder.SubmitInput{RemediationResult: "x"}, "", "")
			return err
		},
	} {
		if err := call(); !errors.Is(err, securityworkorder.ErrIndividualWorkOrderAction) {
			t.Fatalf("individual order %s error = %v, want securityworkorder.ErrIndividualWorkOrderAction", name, err)
		}
	}

	// review/close and cancel stay group-only, so an organization role is refused
	// on the permission check before the individual-container guard runs.
	for name, call := range map[string]func() error{
		"Review": func() error {
			_, err := f.orders.Review(f.ctx, orgAdminB, f.individualOrderID, securityworkorder.ReviewInput{Decision: "closed", VerificationResult: "x"}, "", "")
			return err
		},
		"Cancel": func() error {
			_, err := f.orders.Cancel(f.ctx, orgAdminB, f.individualOrderID, securityworkorder.CommentInput{Comment: "x"}, "", "")
			return err
		},
	} {
		if err := call(); !errors.Is(err, securityworkorder.ErrForbidden) {
			t.Fatalf("individual order %s error = %v, want securityworkorder.ErrForbidden", name, err)
		}
	}
	// A group security role does reach the guard, which rejects the legacy
	// container transition with an explicit state conflict.
	if _, err := f.orders.Review(f.ctx, f.securityAdmin, f.individualOrderID, securityworkorder.ReviewInput{Decision: "closed", VerificationResult: "x"}, "", ""); !errors.Is(err, securityworkorder.ErrIndividualWorkOrderAction) {
		t.Fatalf("individual order Review(group security) error = %v, want securityworkorder.ErrIndividualWorkOrderAction", err)
	}

	// The vendor reaches its own task through remediation_tasks, never through the
	// legacy container endpoints.
	if _, err := f.repairs.Detail(f.ctx, f.vendor, f.remediationTaskID, true); err != nil {
		t.Fatalf("vendor remediation task detail error = %v", err)
	}
	if _, err := f.repairs.Detail(f.ctx, f.vendor, f.orderID, true); err == nil {
		t.Fatal("vendor must not reach an organization-level work order through the repair workspace")
	}
}

func TestRoleMatrixFindingAndAttachmentBoundaries(t *testing.T) {
	f := setupRoleMatrix(t)
	page, _ := pagination.Normalize(1, 50)

	// Group security roles manage findings and risk decisions.
	if _, err := f.orders.AcceptRisk(f.ctx, f.securityAdmin, f.riskFindingID, securityworkorder.RiskAcceptanceInput{
		Reason: "业务接受风险", AcceptedUntil: time.Now().Add(30 * 24 * time.Hour),
		ExpectedUpdatedAt: findingVersion(t, f.ctx, f.pool, f.riskFindingID),
	}, "", ""); err != nil {
		t.Fatalf("security admin AcceptRisk error = %v", err)
	}
	if _, err := f.orders.RevokeRisk(f.ctx, f.securityAdmin, f.riskFindingID, securityworkorder.RiskRevokeInput{
		ExpectedUpdatedAt: findingVersion(t, f.ctx, f.pool, f.riskFindingID),
	}, "", ""); err != nil {
		t.Fatalf("security admin RevokeRisk error = %v", err)
	}
	// Organization roles never decide risk acceptance.
	_, err := f.orders.AcceptRisk(f.ctx, f.orgAdmin, f.riskFindingID, securityworkorder.RiskAcceptanceInput{
		Reason: "越权接受", AcceptedUntil: time.Now().Add(24 * time.Hour)}, "", "")
	assertRoleDenied(t, "org admin AcceptRisk", err)
	_, err = f.orders.ReviewRisk(f.ctx, f.orgAdmin, f.riskFindingID, securityworkorder.RiskReviewInput{Comment: "越权复核"}, "", "")
	assertRoleDenied(t, "org admin ReviewRisk", err)

	// Attachment metadata and download share the work-order read boundary.
	if _, err := f.orders.GetAttachment(f.ctx, f.securityAdmin, f.orderID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, securityworkorder.ErrNotFound) {
		t.Fatalf("security admin GetAttachment(missing) error = %v, want securityworkorder.ErrNotFound", err)
	}
	if _, err := f.orders.GetAttachment(f.ctx, f.orgEditor, f.orderID, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("filler GetAttachment error = %v, want securityworkorder.ErrForbidden", err)
	}
	// Upload is an execution action, and the filler has none.
	_, err = f.orders.AddAttachment(f.ctx, f.orgEditor, f.orderID, securityworkorder.AttachmentInput{
		Category: "remediation", FileName: "evidence.txt", ContentType: "text/plain",
		SizeBytes: int64(len("hello")), Reader: bytes.NewReader([]byte("hello")),
	}, "", "")
	assertRoleDenied(t, "filler AddAttachment", err)
	// Reading the individual order's attachment list stays denied for the vendor.
	if _, err := f.orders.SearchWorkOrderAttachments(f.ctx, f.vendor, f.individualOrderID, page); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("vendor SearchWorkOrderAttachments error = %v, want securityworkorder.ErrForbidden", err)
	}
}

func TestRoleMatrixDraftWorkOrderHiddenFromOrganizationRoles(t *testing.T) {
	f := setupRoleMatrix(t)

	// A draft has not been dispatched yet: only group roles may see it, even
	// when the organization already holds the read permission.
	draft, err := f.orders.CreateWorkOrder(f.ctx, f.securityAdmin, securityworkorder.WorkOrderInput{
		ExecutionMode: "organization", Title: "未下发工单", AssigneeOrganizationID: f.orgA,
		RemediationRequirement: "整改", DueAt: time.Now().Add(24 * time.Hour),
		FindingIDs: []string{f.draftFindingID},
	}, "", "")
	if err != nil {
		t.Fatalf("create draft work order: %v", err)
	}
	if _, err := f.orders.GetWorkOrder(f.ctx, f.orgAdmin, draft.ID); !errors.Is(err, securityworkorder.ErrForbidden) {
		t.Fatalf("org admin GetWorkOrder(draft) error = %v, want securityworkorder.ErrForbidden", err)
	}
	if _, err := f.orders.GetWorkOrder(f.ctx, f.groupAdmin, draft.ID); err != nil {
		t.Fatalf("group admin GetWorkOrder(draft) error = %v", err)
	}
}

func TestRoleMatrixVendorCompletesFullRemediationReview(t *testing.T) {
	f := setupRoleMatrix(t)
	orgAdminB := createRoleMatrixScopedPrincipal(t, f.pool, "rm_org_admin_review_"+strings.ReplaceAll(time.Now().Format("150405.000000000"), ".", ""), "organization_admin", f.orgB)

	readVersion := func(p auth.Principal, executor bool) int {
		t.Helper()
		detail, err := f.repairs.Detail(f.ctx, p, f.remediationTaskID, executor)
		if err != nil {
			t.Fatalf("read remediation task: %v", err)
		}
		return int(detail["version"].(float64))
	}
	eta := time.Now().Add(12 * time.Hour)
	if _, err := f.repairs.Act(f.ctx, f.vendor, f.remediationTaskID, "accept", "", remediation.ActionInput{
		ExpectedVersion: readVersion(f.vendor, true), ExpectedCompletion: &eta,
	}, true); err != nil {
		t.Fatalf("vendor accept remediation: %v", err)
	}
	if _, err := f.repairs.Act(f.ctx, f.vendor, f.remediationTaskID, "progress", "", remediation.ActionInput{
		ExpectedVersion: readVersion(f.vendor, true), Progress: "修复已部署并完成自测", ExpectedCompletion: &eta,
	}, true); err != nil {
		t.Fatalf("vendor progress remediation: %v", err)
	}
	deployedAt := time.Now().Add(-time.Minute)
	submitted, err := f.repairs.Act(f.ctx, f.vendor, f.remediationTaskID, "submit", "vendor-role-matrix-submit", remediation.ActionInput{
		ExpectedVersion: readVersion(f.vendor, true), Summary: "修复输入校验并部署至生产", DeploymentStatus: "target",
		ChangeReference: "release-role-matrix-1", DeployedAt: &deployedAt, SelfTestResult: "生产环境复测通过",
	}, true)
	if err != nil {
		t.Fatalf("vendor submit remediation: %v", err)
	}
	if submitted["status"] != "pending_org_review" {
		t.Fatalf("vendor submit status = %v, want pending_org_review", submitted["status"])
	}
	submittedDetail, err := f.repairs.Detail(f.ctx, f.vendor, f.remediationTaskID, true)
	if err != nil {
		t.Fatalf("read submitted remediation task: %v", err)
	}
	submissionID, ok := submittedDetail["current_submission_id"].(string)
	if !ok || submissionID == "" {
		t.Fatalf("submitted task detail missing current_submission_id: %#v", submittedDetail)
	}

	orgReviewVersion := readVersion(orgAdminB, false)
	if _, err := f.repairs.Act(f.ctx, orgAdminB, f.remediationTaskID, "organization-review", "", remediation.ActionInput{
		ExpectedVersion: orgReviewVersion, SubmissionID: submissionID, Decision: "pass", Reason: "组织确认已部署并留存证据",
	}, false); err != nil {
		t.Fatalf("organization review remediation: %v", err)
	}
	// Organization roles may perform only their review stage, not the group's
	// independent security verification stage.
	verifiedAt := time.Now()
	if _, err := f.repairs.Act(f.ctx, orgAdminB, f.remediationTaskID, "security-review", "", remediation.ActionInput{
		ExpectedVersion: readVersion(orgAdminB, false), SubmissionID: submissionID, Decision: "pass", Reason: "越权安全复核",
		VerifiedEnvironment: "生产", VerifiedAt: &verifiedAt,
	}, false); !errors.Is(err, remediation.ErrForbidden) {
		t.Fatalf("organization security-review error = %v, want remediation.ErrForbidden", err)
	}
	if _, err := f.repairs.Act(f.ctx, f.securityAdmin, f.remediationTaskID, "security-review", "", remediation.ActionInput{
		ExpectedVersion: readVersion(f.securityAdmin, false), SubmissionID: submissionID, Decision: "pass", Reason: "集团安全复测通过",
		VerifiedEnvironment: "生产", VerifiedAt: &verifiedAt,
	}, false); err != nil {
		t.Fatalf("group security review remediation: %v", err)
	}

	var taskStatus, findingStatus, remediationStatus string
	if err := f.pool.QueryRow(f.ctx, `
		SELECT t.status, f.status, f.remediation_status
		FROM remediation_tasks t JOIN security_findings f ON f.id=t.finding_id
		WHERE t.id=$1::uuid`, f.remediationTaskID).Scan(&taskStatus, &findingStatus, &remediationStatus); err != nil {
		t.Fatalf("read completed remediation state: %v", err)
	}
	if taskStatus != "completed" || findingStatus != "fixed" || remediationStatus != "completed" {
		t.Fatalf("completion state task/finding/remediation=%s/%s/%s, want completed/fixed/completed", taskStatus, findingStatus, remediationStatus)
	}
	if _, err := f.repairs.Detail(f.ctx, f.vendor, f.remediationTaskID, true); err != nil {
		t.Fatalf("vendor should retain own completed task history: %v", err)
	}
	page, _ := pagination.Normalize(1, 50)
	findings, err := f.orders.SearchFindings(f.ctx, f.vendor, "", "", "", page)
	if err != nil {
		t.Fatalf("vendor SearchFindings error = %v", err)
	}
	if findings.Total != 0 || len(findings.Items) != 0 {
		t.Fatalf("vendor sees %d group findings, want no rows", findings.Total)
	}
	if _, err := f.repairs.Detail(f.ctx, f.vendor, f.individualOrderID, true); !errors.Is(err, remediation.ErrNotFound) {
		t.Fatalf("vendor access to parent work order error = %v, want remediation.ErrNotFound", err)
	}
}
