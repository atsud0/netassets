package notification_test

import (
	"context"
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
	"netassets/internal/notification"
	"netassets/internal/pagination"
)

// Notifications are the quietest read surface in the product: no button is
// hidden, no endpoint is guarded by an obvious permission, and a cross-organization
// message still renders a title and a jump link. These tests therefore assert on
// what a role must NOT see rather than on HTTP status codes.

// notificationScopeFixture creates two organizations, one organization-level work
// order each, and one assigned remediation task per organization.
type notificationScopeFixture struct {
	ctx   context.Context
	pool  *pgxpool.Pool
	notes *notification.Service

	groupAdmin, securityAdmin, orgAdminA, orgAdminB, vendorA auth.Principal
	orgA, orgB                                               string
	suffix                                                   string
	taskA, taskB                                             string
}

func setupNotificationScope(t *testing.T) *notificationScopeFixture {
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
		BootstrapAdminUsername:    "note_scope_root_" + suffix,
		BootstrapAdminPassword:    "Note-Scope-Password-123",
		BootstrapAdminDisplayName: "集团安全管理员",
		BootstrapGroupCode:        "NOTE_SCOPE_GROUP_" + suffix,
		BootstrapGroupName:        "通知范围测试集团",
	}
	if err := bootstrap.Ensure(ctx, pool, cfg, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	login, err := auth.NewService(pool, time.Hour).Login(ctx, auth.LoginInput{
		Username: cfg.BootstrapAdminUsername, Password: cfg.BootstrapAdminPassword,
	})
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	f := &notificationScopeFixture{ctx: ctx, pool: pool, notes: notification.NewService(pool), suffix: suffix}
	f.securityAdmin = login.User

	groupAdmin := createNotificationUser(t, pool, "note_group_admin_"+suffix)
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, data_scope)
		SELECT $1::uuid, id, 'all' FROM roles WHERE role_key='group_admin'`, groupAdmin); err != nil {
		t.Fatalf("assign group admin: %v", err)
	}
	f.groupAdmin = notificationPrincipalWithPermissions(t, pool, groupAdmin, []string{"group_admin"})

	for _, spec := range []struct {
		code, name string
		target     *string
	}{
		{"NOTE_A_" + suffix, "通知子公司 A", &f.orgA},
		{"NOTE_B_" + suffix, "通知子公司 B", &f.orgB},
	} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO organizations (organization_code, organization_name, organization_type)
			VALUES ($1,$2,'subsidiary') RETURNING id::text`, spec.code, spec.name).Scan(spec.target); err != nil {
			t.Fatalf("insert organization: %v", err)
		}
	}
	f.orgAdminA = createNotificationScopedPrincipal(t, pool, "note_org_admin_a_"+suffix, "organization_admin", f.orgA)
	f.orgAdminB = createNotificationScopedPrincipal(t, pool, "note_org_admin_b_"+suffix, "organization_admin", f.orgB)

	f.taskA = createNotificationTask(t, pool, f.securityAdmin, f.orgA, "A 组织漏洞", suffix+"_A")
	f.taskB = createNotificationTask(t, pool, f.securityAdmin, f.orgB, "B 组织漏洞", suffix+"_B")

	// An external participant in organization B, with no legacy permission and no
	// assignment to organization A's task.
	f.vendorA = auth.Principal{UserID: createNotificationUser(t, pool, "note_vendor_"+suffix)}
	return f
}

func createNotificationUser(t *testing.T, pool *pgxpool.Pool, username string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (username, password_hash, display_name) VALUES ($1,'not-used',$1) RETURNING id::text`,
		username).Scan(&id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

func createNotificationScopedPrincipal(t *testing.T, pool *pgxpool.Pool, username, roleKey, organizationID string) auth.Principal {
	t.Helper()
	userID := createNotificationUser(t, pool, username)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope)
		SELECT $1::uuid, id, $3::uuid, 'organization' FROM roles WHERE role_key = $2`,
		userID, roleKey, organizationID); err != nil {
		t.Fatalf("assign role: %v", err)
	}
	return notificationPrincipalWithPermissions(t, pool, userID, nil)
}

func notificationPrincipalWithPermissions(t *testing.T, pool *pgxpool.Pool, userID string, roles []string) auth.Principal {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT DISTINCT rp.permission_key
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id AND r.status='enabled'
		JOIN role_permissions rp ON rp.role_id = r.id
		WHERE ur.user_id = $1::uuid`, userID)
	if err != nil {
		t.Fatalf("load permissions: %v", err)
	}
	defer rows.Close()
	var permissions []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatalf("scan permission: %v", err)
		}
		permissions = append(permissions, key)
	}
	return auth.Principal{UserID: userID, Roles: roles, Permissions: permissions}
}

// createNotificationTask builds an overdue, assigned remediation task. Overdue
// tasks are exactly the entries that must not cross an organization boundary.
func createNotificationTask(t *testing.T, pool *pgxpool.Pool, principal auth.Principal, organizationID, title, suffix string) string {
	t.Helper()
	ctx := context.Background()
	var systemID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO business_systems (system_code, system_name, owner_organization_id, system_status)
		VALUES ($1,'通知范围系统',$2::uuid,'running') RETURNING id::text`,
		"NOTE_SYS_"+suffix, organizationID).Scan(&systemID); err != nil {
		t.Fatalf("insert business system: %v", err)
	}
	var workOrderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_work_orders (work_order_no, title, assignee_organization_id, severity,
		                                   description, remediation_requirement, status, execution_mode,
		                                   created_by, updated_by, issued_at, due_at)
		VALUES ($1,'通知范围工单',$2::uuid,'high','用于验证通知范围','必须在版本窗口内完成整改',
		        'issued','individual',$3::uuid,$3::uuid,now(),now()+interval '2 days')
		RETURNING id::text`, "NOTE_WO_"+suffix, organizationID, principal.UserID).Scan(&workOrderID); err != nil {
		t.Fatalf("insert work order: %v", err)
	}
	var findingID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO security_findings (finding_code, title, severity, status, assignee_organization_id,
		                               business_system_id, description, source, created_by, updated_by)
		VALUES ($1,$2,'high','confirmed',$3::uuid,$4::uuid,'用于验证通知范围','manual',$5::uuid,$5::uuid)
		RETURNING id::text`, "NOTE_F_"+suffix, title, organizationID, systemID, principal.UserID).Scan(&findingID); err != nil {
		t.Fatalf("insert finding: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO security_work_order_findings (work_order_id, finding_id) VALUES ($1::uuid,$2::uuid)`,
		workOrderID, findingID); err != nil {
		t.Fatalf("link finding: %v", err)
	}
	// The assignee must hold a valid grant: remediation_tasks.grant_id is mandatory
	// and remediation_can_read() consults the grant, not the organization alone.
	assignee := createNotificationUser(t, pool, "note_worker_"+suffix)
	var grantID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO remediation_grants (user_id, organization_id, business_system_id, participant_type,
		                             status, valid_from, valid_until, sponsor_user_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'internal','enabled',now()-interval '5 days',
		        now()+interval '30 days',$4::uuid)
		RETURNING id::text`, assignee, organizationID, systemID, principal.UserID).Scan(&grantID); err != nil {
		t.Fatalf("insert remediation grant: %v", err)
	}
	var taskID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO remediation_tasks (task_no, work_order_id, finding_id, organization_id, business_system_id,
		                               assignee_user_id, grant_id, status, due_at, original_due_at,
		                               target_environment, created_by)
		VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7::uuid,'in_progress',
		        now()-interval '2 days',now()-interval '3 days','production',$8::uuid)
		RETURNING id::text`, "NOTE_T_"+suffix, workOrderID, findingID, organizationID, systemID,
		assignee, grantID, principal.UserID).Scan(&taskID); err != nil {
		t.Fatalf("insert remediation task: %v", err)
	}
	return taskID
}

func notificationPage() pagination.Query {
	page, _ := pagination.Normalize(1, 100)
	return page
}

func TestNotificationScopeGroupRolesSeeAllOrganizations(t *testing.T) {
	f := setupNotificationScope(t)
	for name, principal := range map[string]auth.Principal{"group_admin": f.groupAdmin, "security_operator": f.securityAdmin} {
		result, _, err := f.notes.Search(f.ctx, principal, notification.SearchInput{Page: notificationPage()})
		if err != nil {
			t.Fatalf("%s Search error = %v", name, err)
		}
		if result.Total == 0 {
			t.Fatalf("%s notification list is empty; group roles must see the whole group", name)
		}
	}
}

func TestNotificationScopeDoesNotCrossOrganizationBoundary(t *testing.T) {
	f := setupNotificationScope(t)

	// Organization A's administrator may see A's entries but never B's task.
	result, _, err := f.notes.Search(f.ctx, f.orgAdminA, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("org admin A Search error = %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_task" && item.ResourceID == f.taskB {
			t.Fatalf("org admin A received a notification for organization B's task %s", f.taskB)
		}
	}
	if result.Total == 0 {
		t.Fatal("org admin A notification list is empty; it must still see its own organization's entries")
	}
	// Organization B's administrator is symmetric and independent.
	resultB, _, err := f.notes.Search(f.ctx, f.orgAdminB, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("org admin B Search error = %v", err)
	}
	for _, item := range resultB.Items {
		if item.ResourceType == "remediation_task" && item.ResourceID == f.taskA {
			t.Fatalf("org admin B received a notification for organization A's task %s", f.taskA)
		}
	}
}

func TestNotificationScopeUnassignedUserSeesNothing(t *testing.T) {
	f := setupNotificationScope(t)

	// A user with no appointment at all must not receive another organization's
	// remediation notifications, even though both tasks are overdue.
	for name, principal := range map[string]auth.Principal{"vendor": f.vendorA} {
		result, summary, err := f.notes.Search(f.ctx, principal, notification.SearchInput{Page: notificationPage()})
		if err != nil {
			t.Fatalf("%s Search error = %v", name, err)
		}
		for _, item := range result.Items {
			if item.ResourceType == "remediation_task" && (item.ResourceID == f.taskA || item.ResourceID == f.taskB) {
				t.Fatalf("%s received a remediation notification for an unassigned task %s", name, item.ResourceID)
			}
		}
		// The summary is computed from the same candidates, so it must not claim
		// unread items the list refuses to show.
		if summary.Total != int64(result.Total) {
			t.Fatalf("%s summary total = %d but list total = %d; the two use different scopes", name, summary.Total, result.Total)
		}
	}
}

func TestNotificationScopeOrgEditorHasNoRemediationAccess(t *testing.T) {
	f := setupNotificationScope(t)
	// organization_editor holds no remediation.* permission: it must not receive
	// remediation_task notifications, not even for its own organization.
	editor := createNotificationScopedPrincipal(t, f.pool, "note_org_editor_"+f.suffix, "organization_editor", f.orgA)
	result, _, err := f.notes.Search(f.ctx, editor, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_task" {
			t.Fatalf("unassigned user received remediation_task notification %s", item.ResourceID)
		}
	}
}

// Retirement is the one remediation event that happens without the assignee
// doing anything, so it is the one event most likely to be missing from the
// notification chain: the reviewer needs to know the work stopped, and the
// assignee needs to know it can continue again.
func TestNotificationChainIncludesRetirementAndRestorationEvents(t *testing.T) {
	f := setupNotificationScope(t)
	ctx := f.ctx
	for action := range map[string]string{
		"asset_retired":   "关联资产已删除，任务暂停",
		"system_retired":  "关联业务系统已删除，任务暂停",
		"asset_restored":  "关联资产已恢复，任务可继续",
		"system_restored": "业务系统已恢复，任务待确认",
	} {
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO remediation_task_events (task_id, actor_id, action, visibility, details)
			VALUES ($1::uuid,$2::uuid,$3,'participants','{}'::jsonb)`, f.taskA, f.securityAdmin.UserID, action); err != nil {
			t.Fatalf("insert %s event: %v", action, err)
		}
	}
	// The notification query labels each event with the task's current material.
	// The raw-SQL fixture has none, so the join would drop every event silently.
	var revisions int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM remediation_material_versions WHERE task_id=$1::uuid`, f.taskA).Scan(&revisions); err != nil {
		t.Fatalf("count material versions: %v", err)
	}
	if revisions == 0 {
		if _, err := f.pool.Exec(ctx, `
			INSERT INTO remediation_material_versions (task_id, revision, title, severity, description, requirement, approved_by)
			VALUES ($1::uuid,1,'通知范围材料','high','用于验证通知范围','完成整改',$2::uuid)`, f.taskA, f.securityAdmin.UserID); err != nil {
			t.Fatalf("insert material version: %v", err)
		}
	}
	result, _, err := f.notes.Search(ctx, f.orgAdminA, notification.SearchInput{Page: notificationPage()})
	if err != nil {
		t.Fatalf("Search error = %v", err)
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		if item.ResourceType == "remediation_task" && item.ResourceID == f.taskA {
			seen[item.Title] = true
		}
	}
	for _, want := range []string{
		"关联资产已删除，任务暂停",
		"关联业务系统已删除，任务暂停",
		"关联资产已恢复，任务可继续",
		"业务系统已恢复，任务待确认",
	} {
		if !seen[want] {
			t.Fatalf("notification list is missing %q; got %v", want, seen)
		}
	}
}
