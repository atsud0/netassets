package user_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"netassets/internal/auth"
	"netassets/internal/user"
)

type permissionFixture struct {
	pool              *pgxpool.Pool
	service           *user.Service
	authService       *auth.Service
	groupID           string
	organizationAID   string
	organizationBID   string
	branchAID         string
	organizationRole  string
	groupRole         string
	groupAdmin        auth.Principal
	organizationAdmin auth.Principal
	descendantAdmin   auth.Principal
	plainUser         auth.Principal
	targetAID         string
	targetBID         string
	targetBranchID    string
	crossTargetID     string
	groupTargetID     string
	profileUserID     string
	profileSessionID  string
	otherSessionID    string
}

func TestUserManagementPermissionBoundaries(t *testing.T) {
	fixture := newPermissionFixture(t)
	ctx := context.Background()

	t.Run("permission is required", func(t *testing.T) {
		if _, err := fixture.service.List(ctx, fixture.plainUser); !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("List() error = %v, want ErrForbidden", err)
		}
		_, err := fixture.service.Create(ctx, fixture.plainUser, validCreateInput(fixture.organizationAID, fixture.organizationRole, "permission_denied"), "", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Create() error = %v, want ErrForbidden", err)
		}
	})

	t.Run("group administrator can manage all organizations", func(t *testing.T) {
		items, err := fixture.service.List(ctx, fixture.groupAdmin)
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if !containsUser(items, fixture.targetAID) || !containsUser(items, fixture.targetBID) || !containsUser(items, fixture.targetBranchID) {
			t.Fatalf("group administrator list did not include all organization users")
		}
		_, err = fixture.service.Update(ctx, fixture.groupAdmin, fixture.targetBID, validUpdateInput(fixture.organizationBID, fixture.organizationRole, "集团管理员已更新"), "test-group-update", "")
		if err != nil {
			t.Fatalf("Update() error = %v", err)
		}
	})

	t.Run("organization scope manages only the exact organization", func(t *testing.T) {
		_, err := fixture.service.Update(ctx, fixture.organizationAdmin, fixture.targetAID, validUpdateInput(fixture.organizationAID, fixture.organizationRole, "子公司管理员已更新"), "test-org-update", "")
		if err != nil {
			t.Fatalf("Update(exact organization) error = %v", err)
		}
		_, err = fixture.service.Update(ctx, fixture.organizationAdmin, fixture.targetBranchID, validUpdateInput(fixture.branchAID, fixture.organizationRole, "不应成功"), "test-org-child-denied", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Update(child organization) error = %v, want ErrForbidden", err)
		}
	})

	t.Run("organization and descendants scope manages child organizations", func(t *testing.T) {
		_, err := fixture.service.Update(ctx, fixture.descendantAdmin, fixture.targetBranchID, validUpdateInput(fixture.branchAID, fixture.organizationRole, "办事处用户已更新"), "test-descendant-update", "")
		if err != nil {
			t.Fatalf("Update(descendant organization) error = %v", err)
		}
	})

	t.Run("subsidiary administrator cannot manage another subsidiary", func(t *testing.T) {
		_, err := fixture.service.Update(ctx, fixture.organizationAdmin, fixture.targetBID, validUpdateInput(fixture.organizationBID, fixture.organizationRole, "不应成功"), "test-other-org-denied", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Update(other organization) error = %v, want ErrForbidden", err)
		}
	})

	t.Run("cross organization appointment outside scope blocks management", func(t *testing.T) {
		_, err := fixture.service.Update(ctx, fixture.descendantAdmin, fixture.crossTargetID, user.UpdateInput{
			DisplayName: "不应成功",
			Appointments: []user.AppointmentInput{
				{OrganizationID: fixture.organizationAID, IsPrimary: true, Assignments: []user.AssignmentInput{{RoleID: fixture.organizationRole, DataScope: "organization"}}},
				{OrganizationID: fixture.organizationBID, Assignments: []user.AssignmentInput{{RoleID: fixture.organizationRole, DataScope: "organization"}}},
			},
		}, "test-cross-org-denied", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Update(cross organization user) error = %v, want ErrForbidden", err)
		}
	})

	t.Run("organization administrator cannot assign global roles", func(t *testing.T) {
		input := validUpdateInput(fixture.organizationAID, fixture.organizationRole, "不应成功")
		input.GlobalRoleIDs = []string{fixture.groupRole}
		_, err := fixture.service.Update(ctx, fixture.organizationAdmin, fixture.targetAID, input, "test-global-role-denied", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Update(global role) error = %v, want ErrForbidden", err)
		}
	})

	t.Run("organization administrator cannot manage a group role user", func(t *testing.T) {
		err := fixture.service.UpdateStatus(ctx, fixture.descendantAdmin, fixture.groupTargetID, user.StatusInput{Status: "disabled"}, "test-group-user-denied", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("UpdateStatus(group role user) error = %v, want ErrForbidden", err)
		}
	})

	t.Run("group administrator can create a global-role-only account", func(t *testing.T) {
		created, err := fixture.service.Create(ctx, fixture.groupAdmin, user.CreateInput{
			Username:      "global_only_test",
			Password:      "InitialPassword123!",
			DisplayName:   "仅集团角色用户",
			GlobalRoleIDs: []string{fixture.groupRole},
		}, "test-global-only-create", "")
		if err != nil {
			t.Fatalf("Create(global-role-only account) error = %v", err)
		}
		if len(created.Appointments) != 0 {
			t.Fatalf("global-role-only account appointments = %+v, want none", created.Appointments)
		}
		if len(created.GlobalRoles) != 1 || created.GlobalRoles[0].RoleID != fixture.groupRole {
			t.Fatalf("global-role-only account roles = %+v, want assigned group role", created.GlobalRoles)
		}
		login, err := fixture.authService.Login(ctx, auth.LoginInput{
			Username:  "global_only_test",
			Password:  "InitialPassword123!",
			RequestID: "test-global-only-login",
		})
		if err != nil {
			t.Fatalf("Login(global-role-only account) error = %v", err)
		}
		if login.User.Organization != nil || len(login.User.Organizations) != 0 {
			t.Fatalf("global-role-only login organizations = current:%+v all:%+v, want none", login.User.Organization, login.User.Organizations)
		}
		if !login.User.HasRole("group_admin") {
			t.Fatalf("global-role-only login roles = %+v, want group_admin", login.User.Roles)
		}
	})

	t.Run("created and reset passwords are temporary", func(t *testing.T) {
		created, err := fixture.service.Create(ctx, fixture.groupAdmin, validCreateInput(fixture.organizationAID, fixture.organizationRole, "temporary_password_test"), "test-temporary-create", "")
		if err != nil {
			t.Fatalf("Create(temporary password user) error = %v", err)
		}
		if !created.MustChangePassword {
			t.Fatal("new managed user was not marked for a mandatory password change")
		}

		if err := fixture.service.ResetPassword(ctx, fixture.groupAdmin, fixture.targetAID, user.ResetPasswordInput{Password: "TemporaryPassword123!"}, "test-temporary-reset", ""); err != nil {
			t.Fatalf("ResetPassword() error = %v", err)
		}
		var mustChange bool
		var passwordChangedAt *time.Time
		if err := fixture.pool.QueryRow(ctx, `SELECT must_change_password,password_changed_at FROM users WHERE id=$1::uuid`, fixture.targetAID).Scan(&mustChange, &passwordChangedAt); err != nil {
			t.Fatal(err)
		}
		if !mustChange || passwordChangedAt != nil {
			t.Fatalf("reset password state must_change=%v password_changed_at=%v", mustChange, passwordChangedAt)
		}
	})

	t.Run("search filters and pagination do not leak out-of-scope users", func(t *testing.T) {
		result, err := fixture.service.Search(ctx, fixture.groupAdmin, user.ListQuery{Keyword: "target_a_test", Page: 1, PageSize: 20})
		if err != nil {
			t.Fatalf("Search(keyword) error = %v", err)
		}
		if result.Total != 1 || len(result.Items) != 1 || result.Items[0].ID != fixture.targetAID {
			t.Fatalf("Search(keyword) result = %+v, want target A only", result)
		}

		result, err = fixture.service.Search(ctx, fixture.groupAdmin, user.ListQuery{OrganizationID: fixture.organizationBID, RoleID: fixture.organizationRole, Status: "enabled", Page: 1, PageSize: 1})
		if err != nil {
			t.Fatalf("Search(filters) error = %v", err)
		}
		if result.Total < 2 || len(result.Items) != 1 {
			t.Fatalf("Search(filters) pagination result = %+v, want total >= 2 and one item", result)
		}

		result, err = fixture.service.Search(ctx, fixture.organizationAdmin, user.ListQuery{Page: 1, PageSize: 100})
		if err != nil {
			t.Fatalf("Search(scoped) error = %v", err)
		}
		if containsUser(result.Items, fixture.targetBID) || containsUser(result.Items, fixture.crossTargetID) || containsUser(result.Items, fixture.targetBranchID) {
			t.Fatalf("scoped search leaked an out-of-scope user: %+v", result.Items)
		}
	})

	t.Run("user detail follows the same management boundary", func(t *testing.T) {
		item, err := fixture.service.Get(ctx, fixture.organizationAdmin, fixture.targetAID)
		if err != nil || item.ID != fixture.targetAID {
			t.Fatalf("Get(in-scope user) item=%+v error=%v", item, err)
		}
		if _, err := fixture.service.Get(ctx, fixture.organizationAdmin, fixture.targetBID); !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Get(out-of-scope user) error=%v, want ErrForbidden", err)
		}
	})

	t.Run("administrator cannot mutate self through user management", func(t *testing.T) {
		_, err := fixture.service.Update(ctx, fixture.organizationAdmin, fixture.organizationAdmin.UserID, validUpdateInput(fixture.organizationAID, fixture.organizationRole, "不应成功"), "test-self-update", "")
		if !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("Update(self) error = %v, want ErrForbidden", err)
		}
		if err := fixture.service.UpdateStatus(ctx, fixture.organizationAdmin, fixture.organizationAdmin.UserID, user.StatusInput{Status: "disabled"}, "test-self-status", ""); !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("UpdateStatus(self) error = %v, want ErrForbidden", err)
		}
		if err := fixture.service.ResetPassword(ctx, fixture.organizationAdmin, fixture.organizationAdmin.UserID, user.ResetPasswordInput{Password: "NewPassword123!"}, "test-self-password", ""); !errors.Is(err, user.ErrForbidden) {
			t.Fatalf("ResetPassword(self) error = %v, want ErrForbidden", err)
		}
	})
}

func TestSelfServiceProfileAndPasswordBoundaries(t *testing.T) {
	fixture := newPermissionFixture(t)
	ctx := context.Background()
	principal := auth.Principal{
		UserID:       fixture.profileUserID,
		Username:     "profile_user",
		SessionID:    fixture.profileSessionID,
		Organization: &auth.Organization{ID: fixture.organizationAID},
	}

	if _, err := fixture.pool.Exec(ctx, `UPDATE users SET must_change_password=true,password_changed_at=NULL WHERE id=$1::uuid`, fixture.profileUserID); err != nil {
		t.Fatal(err)
	}

	var rolesBefore int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id=$1::uuid`, fixture.profileUserID).Scan(&rolesBefore); err != nil {
		t.Fatal(err)
	}
	updated, err := fixture.authService.UpdateProfile(ctx, principal, auth.ProfileInput{
		DisplayName: "个人资料已更新",
		Email:       "profile@example.com",
		MobilePhone: "13800000000",
		OfficePhone: "010-12345678",
	}, "test-profile-update", "")
	if err != nil {
		t.Fatalf("UpdateProfile() error = %v", err)
	}
	if updated.DisplayName != "个人资料已更新" || updated.Email != "profile@example.com" || updated.MobilePhone != "13800000000" || updated.OfficePhone != "010-12345678" {
		t.Fatalf("UpdateProfile() returned unexpected profile: %+v", updated)
	}
	var rolesAfter int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM user_roles WHERE user_id=$1::uuid`, fixture.profileUserID).Scan(&rolesAfter); err != nil {
		t.Fatal(err)
	}
	if rolesAfter != rolesBefore {
		t.Fatalf("profile update changed role assignments: before=%d after=%d", rolesBefore, rolesAfter)
	}

	if err := fixture.authService.ChangePassword(ctx, principal, auth.ChangePasswordInput{CurrentPassword: "wrong-password", NewPassword: "ChangedPassword123!"}, "test-password-wrong", ""); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatalf("ChangePassword(wrong current password) error = %v, want ErrUnauthorized", err)
	}
	if err := fixture.authService.ChangePassword(ctx, principal, auth.ChangePasswordInput{CurrentPassword: "InitialPassword123!", NewPassword: "ChangedPassword123!"}, "test-password-change", ""); err != nil {
		t.Fatalf("ChangePassword() error = %v", err)
	}

	var currentRevokedAt, otherRevokedAt *time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id=$1::uuid`, fixture.profileSessionID).Scan(&currentRevokedAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id=$1::uuid`, fixture.otherSessionID).Scan(&otherRevokedAt); err != nil {
		t.Fatal(err)
	}
	if currentRevokedAt != nil {
		t.Fatal("current session was revoked after self-service password change")
	}
	if otherRevokedAt == nil {
		t.Fatal("other session was not revoked after self-service password change")
	}
	var mustChange bool
	if err := fixture.pool.QueryRow(ctx, `SELECT must_change_password FROM users WHERE id=$1::uuid`, fixture.profileUserID).Scan(&mustChange); err != nil {
		t.Fatal(err)
	}
	if mustChange {
		t.Fatal("successful self-service password change did not clear the mandatory password-change flag")
	}
}

func TestSessionManagementBoundaries(t *testing.T) {
	fixture := newPermissionFixture(t)
	ctx := context.Background()
	principal := auth.Principal{
		UserID:       fixture.profileUserID,
		Username:     "profile_user",
		SessionID:    fixture.profileSessionID,
		Organization: &auth.Organization{ID: fixture.organizationAID},
	}
	foreignSessionID := insertSession(t, fixture.pool, fixture.targetAID, fixture.organizationAID, strings.Repeat("3", 64))

	sessions, err := fixture.authService.ListSessions(ctx, principal)
	if err != nil {
		t.Fatalf("ListSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("ListSessions() returned %d sessions, want 2", len(sessions))
	}
	if !sessions[0].Current || sessions[0].ID != fixture.profileSessionID {
		t.Fatalf("first session = %+v, want current profile session", sessions[0])
	}
	if sessions[0].Device == "" || sessions[0].Browser == "" || sessions[0].OperatingSystem == "" {
		t.Fatalf("session labels were not populated: %+v", sessions[0])
	}

	if err := fixture.authService.RevokeSession(ctx, principal, fixture.profileSessionID, "test-current-session-revoke", ""); !errors.Is(err, auth.ErrCurrentSession) {
		t.Fatalf("RevokeSession(current) error = %v, want ErrCurrentSession", err)
	}
	if err := fixture.authService.RevokeSession(ctx, principal, foreignSessionID, "test-foreign-session-revoke", ""); !errors.Is(err, auth.ErrSessionNotFound) {
		t.Fatalf("RevokeSession(foreign) error = %v, want ErrSessionNotFound", err)
	}
	if err := fixture.authService.RevokeSession(ctx, principal, fixture.otherSessionID, "test-other-session-revoke", ""); err != nil {
		t.Fatalf("RevokeSession(other) error = %v", err)
	}
	var revokedAt *time.Time
	if err := fixture.pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id=$1::uuid`, fixture.otherSessionID).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil {
		t.Fatal("selected other session was not revoked")
	}

	additionalSessionID := insertSession(t, fixture.pool, fixture.profileUserID, fixture.organizationAID, strings.Repeat("4", 64))
	count, err := fixture.authService.RevokeOtherSessions(ctx, principal, "test-revoke-other-sessions", "")
	if err != nil {
		t.Fatalf("RevokeOtherSessions() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("RevokeOtherSessions() count = %d, want 1", count)
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id=$1::uuid`, additionalSessionID).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil {
		t.Fatal("other sessions revoke did not revoke the remaining session")
	}
	if err := fixture.pool.QueryRow(ctx, `SELECT revoked_at FROM sessions WHERE id=$1::uuid`, fixture.profileSessionID).Scan(&revokedAt); err != nil {
		t.Fatal(err)
	}
	if revokedAt != nil {
		t.Fatal("other sessions revoke unexpectedly revoked current session")
	}
	var auditCount int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM audit_logs WHERE user_id=$1::uuid AND action IN ('auth.session_revoke','auth.sessions_revoke_others')`, fixture.profileUserID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("session revoke audit count = %d, want 2", auditCount)
	}
}

func newPermissionFixture(t *testing.T) *permissionFixture {
	t.Helper()
	databaseURL := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("NETASSETS_TEST_DATABASE_URL is not set; integration test requires a dedicated migrated PostgreSQL database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping test database: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE audit_logs, sessions, user_roles, role_permissions, permissions, roles, user_organizations, users, organizations RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("reset test database: %v", err)
	}

	fixture := &permissionFixture{pool: pool, service: user.NewService(pool), authService: auth.NewService(pool, 12*time.Hour)}
	fixture.groupID = insertOrganization(t, pool, "TEST-GROUP", "测试集团", "group", "")
	fixture.organizationAID = insertOrganization(t, pool, "TEST-A", "子公司 A", "subsidiary", fixture.groupID)
	fixture.organizationBID = insertOrganization(t, pool, "TEST-B", "子公司 B", "subsidiary", fixture.groupID)
	fixture.branchAID = insertOrganization(t, pool, "TEST-A-BRANCH", "A 公司办事处", "other", fixture.organizationAID)

	if _, err := pool.Exec(ctx, `INSERT INTO permissions(permission_key, permission_name) VALUES ('user.manage','用户管理')`); err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	fixture.groupRole = insertRole(t, pool, "group_admin", "集团管理员", "group", true)
	fixture.organizationRole = insertRole(t, pool, "organization_admin", "子公司管理员", "organization", true)
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions(role_id, permission_key) VALUES ($1::uuid,'user.manage'),($2::uuid,'user.manage')`, fixture.groupRole, fixture.organizationRole); err != nil {
		t.Fatalf("assign role permissions: %v", err)
	}

	hash, err := bcrypt.GenerateFromPassword([]byte("InitialPassword123!"), 4)
	if err != nil {
		t.Fatal(err)
	}
	groupAdminID := insertUser(t, pool, "group_admin_test", "集团管理员", string(hash))
	organizationAdminID := insertUser(t, pool, "org_admin_test", "子公司管理员", string(hash))
	descendantAdminID := insertUser(t, pool, "desc_admin_test", "下级组织管理员", string(hash))
	plainUserID := insertUser(t, pool, "plain_user_test", "普通用户", string(hash))
	fixture.targetAID = insertUser(t, pool, "target_a_test", "A 公司用户", string(hash))
	fixture.targetBID = insertUser(t, pool, "target_b_test", "B 公司用户", string(hash))
	fixture.targetBranchID = insertUser(t, pool, "target_branch_test", "A 办事处用户", string(hash))
	fixture.crossTargetID = insertUser(t, pool, "target_cross_test", "跨组织用户", string(hash))
	fixture.groupTargetID = insertUser(t, pool, "target_group_test", "集团角色用户", string(hash))
	fixture.profileUserID = insertUser(t, pool, "profile_user", "个人资料用户", string(hash))

	attachUser(t, pool, groupAdminID, fixture.groupID, true)
	assignRole(t, pool, groupAdminID, fixture.groupRole, "", "all")
	attachUser(t, pool, organizationAdminID, fixture.organizationAID, true)
	assignRole(t, pool, organizationAdminID, fixture.organizationRole, fixture.organizationAID, "organization")
	attachUser(t, pool, descendantAdminID, fixture.organizationAID, true)
	assignRole(t, pool, descendantAdminID, fixture.organizationRole, fixture.organizationAID, "organization_and_descendants")
	attachUser(t, pool, plainUserID, fixture.organizationAID, true)
	attachAndAssign(t, pool, fixture.targetAID, fixture.organizationAID, fixture.organizationRole, true)
	attachAndAssign(t, pool, fixture.targetBID, fixture.organizationBID, fixture.organizationRole, true)
	attachAndAssign(t, pool, fixture.targetBranchID, fixture.branchAID, fixture.organizationRole, true)
	attachAndAssign(t, pool, fixture.crossTargetID, fixture.organizationAID, fixture.organizationRole, true)
	attachAndAssign(t, pool, fixture.crossTargetID, fixture.organizationBID, fixture.organizationRole, false)
	attachUser(t, pool, fixture.groupTargetID, fixture.groupID, true)
	assignRole(t, pool, fixture.groupTargetID, fixture.groupRole, "", "all")
	attachAndAssign(t, pool, fixture.profileUserID, fixture.organizationAID, fixture.organizationRole, true)

	fixture.profileSessionID = insertSession(t, pool, fixture.profileUserID, fixture.organizationAID, strings.Repeat("1", 64))
	fixture.otherSessionID = insertSession(t, pool, fixture.profileUserID, fixture.organizationAID, strings.Repeat("2", 64))

	fixture.groupAdmin = auth.Principal{UserID: groupAdminID, Username: "group_admin_test", Roles: []string{"group_admin"}, Permissions: []string{"user.manage"}}
	fixture.organizationAdmin = auth.Principal{UserID: organizationAdminID, Username: "org_admin_test", Roles: []string{"organization_admin"}, Permissions: []string{"user.manage"}}
	fixture.descendantAdmin = auth.Principal{UserID: descendantAdminID, Username: "desc_admin_test", Roles: []string{"organization_admin"}, Permissions: []string{"user.manage"}}
	fixture.plainUser = auth.Principal{UserID: plainUserID, Username: "plain_user_test"}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `TRUNCATE audit_logs, sessions, user_roles, role_permissions, permissions, roles, user_organizations, users, organizations RESTART IDENTITY CASCADE`)
	})
	return fixture
}

func validCreateInput(organizationID, roleID, username string) user.CreateInput {
	return user.CreateInput{
		Username:     username,
		Password:     "InitialPassword123!",
		DisplayName:  "新用户",
		Appointments: []user.AppointmentInput{{OrganizationID: organizationID, IsPrimary: true, Assignments: []user.AssignmentInput{{RoleID: roleID, DataScope: "organization"}}}},
	}
}

func validUpdateInput(organizationID, roleID, displayName string) user.UpdateInput {
	return user.UpdateInput{
		DisplayName:  displayName,
		Appointments: []user.AppointmentInput{{OrganizationID: organizationID, IsPrimary: true, Assignments: []user.AssignmentInput{{RoleID: roleID, DataScope: "organization"}}}},
	}
}

func containsUser(items []user.User, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func insertOrganization(t *testing.T, pool *pgxpool.Pool, code, name, kind, parentID string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO organizations(parent_id, organization_code, organization_name, organization_type) VALUES (NULLIF($1,'')::uuid,$2,$3,$4) RETURNING id::text`, parentID, code, name, kind).Scan(&id); err != nil {
		t.Fatalf("insert organization %s: %v", code, err)
	}
	return id
}

func insertRole(t *testing.T, pool *pgxpool.Pool, key, name, scope string, system bool) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO roles(role_key, role_name, role_scope, is_system, status) VALUES ($1,$2,$3,$4,'enabled') RETURNING id::text`, key, name, scope, system).Scan(&id); err != nil {
		t.Fatalf("insert role %s: %v", key, err)
	}
	return id
}

func insertUser(t *testing.T, pool *pgxpool.Pool, username, displayName, passwordHash string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO users(username,password_hash,display_name,status,password_changed_at) VALUES ($1,$2,$3,'enabled',now()) RETURNING id::text`, username, passwordHash, displayName).Scan(&id); err != nil {
		t.Fatalf("insert user %s: %v", username, err)
	}
	return id
}

func attachUser(t *testing.T, pool *pgxpool.Pool, userID, organizationID string, primary bool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO user_organizations(user_id,organization_id,is_primary) VALUES ($1::uuid,$2::uuid,$3)`, userID, organizationID, primary); err != nil {
		t.Fatalf("attach user to organization: %v", err)
	}
}

func assignRole(t *testing.T, pool *pgxpool.Pool, userID, roleID, organizationID, dataScope string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4)`, userID, roleID, organizationID, dataScope); err != nil {
		t.Fatalf("assign role: %v", err)
	}
}

func attachAndAssign(t *testing.T, pool *pgxpool.Pool, userID, organizationID, roleID string, primary bool) {
	t.Helper()
	attachUser(t, pool, userID, organizationID, primary)
	assignRole(t, pool, userID, roleID, organizationID, "organization")
}

func insertSession(t *testing.T, pool *pgxpool.Pool, userID, organizationID, tokenHash string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO sessions(user_id,token_hash,expires_at,current_organization_id,user_agent) VALUES ($1::uuid,$2,now()+interval '1 day',$3::uuid,'integration-test') RETURNING id::text`, userID, tokenHash, organizationID).Scan(&id); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return id
}
