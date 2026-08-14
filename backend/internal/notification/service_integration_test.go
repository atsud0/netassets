package notification

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestSecurityOperatorNotificationQueries(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE users CASCADE`); err != nil {
		t.Fatalf("reset test users: %v", err)
	}

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ('notification_security_operator', 'not-used', '安全运营通知测试')
		RETURNING id::text
	`).Scan(&userID); err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	principal := auth.Principal{
		UserID: userID,
		Roles:  []string{auth.RoleSecurityOperator},
		Permissions: []string{
			auth.PermissionSecurityFinding,
			auth.PermissionSecurityWorkOrder,
		},
	}
	service := NewService(pool)

	if _, err := service.Summary(ctx, principal); err != nil {
		t.Fatalf("Summary(security operator) error = %v", err)
	}
	page, _ := pagination.Normalize(1, 20)
	if _, _, err := service.Search(ctx, principal, SearchInput{Page: page}); err != nil {
		t.Fatalf("Search(security operator) error = %v", err)
	}
	if err := service.MarkAllRead(ctx, principal); err != nil {
		t.Fatalf("MarkAllRead(security operator) error = %v", err)
	}
	if err := service.MarkRead(ctx, principal, "missing-notification"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkRead(missing notification) error = %v, want ErrNotFound", err)
	}
}

func TestLedgerEventNotificationScopeAndAggregation(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset ledger notification fixtures: %v", err)
	}

	var rootID, organizationID, childID, otherOrganizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('LEDGER_ROOT', '通知测试集团', 'group') RETURNING id::text
	`).Scan(&rootID); err != nil {
		t.Fatalf("insert root organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'LEDGER_SUB', '通知测试子公司', 'subsidiary') RETURNING id::text
	`, rootID).Scan(&organizationID); err != nil {
		t.Fatalf("insert target organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'LEDGER_OFFICE', '通知测试办事处', 'department') RETURNING id::text
	`, organizationID).Scan(&childID); err != nil {
		t.Fatalf("insert child organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'LEDGER_OTHER', '其他子公司', 'subsidiary') RETURNING id::text
	`, rootID).Scan(&otherOrganizationID); err != nil {
		t.Fatalf("insert other organization: %v", err)
	}

	var actorID, recipientID, descendantRecipientID, otherRecipientID, groupAdminID string
	for _, fixture := range []struct {
		username string
		name     string
		dest     *string
	}{
		{username: "ledger_actor", name: "集团操作人", dest: &actorID},
		{username: "ledger_recipient", name: "子公司填报员", dest: &recipientID},
		{username: "ledger_descendant_recipient", name: "子公司管理员", dest: &descendantRecipientID},
		{username: "ledger_other_recipient", name: "其他公司填报员", dest: &otherRecipientID},
		{username: "ledger_group_admin", name: "另一集团管理员", dest: &groupAdminID},
	} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, display_name)
			VALUES ($1, 'not-used', $2) RETURNING id::text
		`, fixture.username, fixture.name).Scan(fixture.dest); err != nil {
			t.Fatalf("insert user %s: %v", fixture.username, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key, permission_name)
		VALUES ('asset.manage', '管理网络资产')
		ON CONFLICT (permission_key) DO NOTHING
	`); err != nil {
		t.Fatalf("insert asset permission: %v", err)
	}
	var roleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
		VALUES ('ledger_asset_editor', '通知测试资产管理员', 'organization', '', false, 'enabled')
		ON CONFLICT (role_key) DO UPDATE SET role_name = EXCLUDED.role_name, status = EXCLUDED.status
		RETURNING id::text
	`).Scan(&roleID); err != nil {
		t.Fatalf("insert asset role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_key) VALUES ($1::uuid, 'asset.manage')
		ON CONFLICT DO NOTHING
	`, roleID); err != nil {
		t.Fatalf("grant asset permission: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, organization_id, data_scope) VALUES
			($2::uuid, $1::uuid, $5::uuid, 'organization'),
			($3::uuid, $1::uuid, $5::uuid, 'organization'),
			($4::uuid, $1::uuid, $5::uuid, 'organization_and_descendants'),
			($6::uuid, $1::uuid, $7::uuid, 'organization')
	`, roleID, actorID, recipientID, descendantRecipientID, organizationID, otherRecipientID, otherOrganizationID); err != nil {
		t.Fatalf("assign asset notification roles: %v", err)
	}

	for _, resourceID := range []string{
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1",
		"aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa2",
	} {
		if err := WriteEvent(ctx, pool, EventInput{
			EventType: EventAssetArchived, Category: CategoryAssetChange,
			TargetOrganizationID: organizationID, ActorUserID: actorID, RequiredPermission: "asset.manage",
			ResourceType: "asset", ResourceID: resourceID, ResourceLabel: "批量资产", BatchKey: "ledger-batch-1", Level: "warning",
		}); err != nil {
			t.Fatalf("write aggregated asset event: %v", err)
		}
	}
	if err := WriteEvent(ctx, pool, EventInput{
		EventType: EventAssetWorkingVersionCreated, Category: CategoryWorkingVersion,
		TargetOrganizationID: organizationID, ActorUserID: actorID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: "dddddddd-dddd-dddd-dddd-dddddddddddd", ResourceLabel: "未上报资产", BatchKey: "working-version-private-1",
	}); err != nil {
		t.Fatalf("write private working-version event: %v", err)
	}
	if err := WriteEvent(ctx, pool, EventInput{
		EventType: EventAssetDeleted, Category: CategoryAssetChange,
		TargetOrganizationID: childID, ActorUserID: actorID, RequiredPermission: "asset.manage",
		ResourceType: "asset", ResourceID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", ResourceLabel: "办事处资产", BatchKey: "ledger-child-1", Level: "danger",
	}); err != nil {
		t.Fatalf("write child organization event: %v", err)
	}
	for _, resourceID := range []string{
		"cccccccc-cccc-cccc-cccc-ccccccccccc1",
		"cccccccc-cccc-cccc-cccc-ccccccccccc2",
	} {
		if err := WriteEvent(ctx, pool, EventInput{
			EventType: EventAssetNetworkAreaChanged, Category: CategoryAssetChange,
			TargetOrganizationID: organizationID, TargetUserID: recipientID, ActorUserID: actorID, RequiredPermission: "asset.manage",
			ResourceType: "asset", ResourceID: resourceID, ResourceLabel: "责任人资产", BatchKey: "ledger-targeted-1", Level: "info",
		}); err != nil {
			t.Fatalf("write targeted asset event: %v", err)
		}
	}

	page, _ := pagination.Normalize(1, 20)
	service := NewService(pool)
	recipient := auth.Principal{UserID: recipientID, Permissions: []string{"asset.manage"}}
	result, summary, err := service.Search(ctx, recipient, SearchInput{Category: CategoryAssetChange, Page: page})
	if err != nil {
		t.Fatalf("Search(recipient) error = %v", err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("recipient notifications = total %d items %d, want organization and targeted events", result.Total, len(result.Items))
	}
	for _, item := range result.Items {
		if !strings.Contains(item.Title, "批量") || !strings.Contains(item.Message, "2") {
			t.Fatalf("aggregated notification = title %q message %q, want batch count", item.Title, item.Message)
		}
	}
	if summary.UnreadCount != 3 {
		t.Fatalf("recipient unread count = %d, want 3 including the private working-version event", summary.UnreadCount)
	}
	if err := service.MarkRead(ctx, recipient, result.Items[0].Key); err != nil {
		t.Fatalf("MarkRead(recipient) error = %v", err)
	}
	readSummary, err := service.Summary(ctx, recipient)
	if err != nil {
		t.Fatalf("Summary(recipient) error = %v", err)
	}
	if readSummary.UnreadCount != 2 {
		t.Fatalf("recipient unread count after marking one message = %d, want 2", readSummary.UnreadCount)
	}

	actor := auth.Principal{UserID: actorID, Permissions: []string{"asset.manage"}}
	actorResult, _, err := service.Search(ctx, actor, SearchInput{Category: CategoryAssetChange, Page: page})
	if err != nil {
		t.Fatalf("Search(actor) error = %v", err)
	}
	if actorResult.Total != 0 {
		t.Fatalf("actor notifications = %d, want self-generated events excluded", actorResult.Total)
	}
	other := auth.Principal{UserID: otherRecipientID, Permissions: []string{"asset.manage"}}
	otherResult, _, err := service.Search(ctx, other, SearchInput{Category: CategoryAssetChange, Page: page})
	if err != nil {
		t.Fatalf("Search(other organization) error = %v", err)
	}
	if otherResult.Total != 0 {
		t.Fatalf("other organization notifications = %d, want 0", otherResult.Total)
	}
	descendantRecipient := auth.Principal{UserID: descendantRecipientID, Permissions: []string{"asset.manage"}}
	descendantResult, _, err := service.Search(ctx, descendantRecipient, SearchInput{Category: CategoryAssetChange, Page: page})
	if err != nil {
		t.Fatalf("Search(descendant recipient) error = %v", err)
	}
	if descendantResult.Total != 2 {
		t.Fatalf("descendant recipient notifications = %d, want parent and child events", descendantResult.Total)
	}
	groupAdmin := auth.Principal{UserID: groupAdminID, Roles: []string{auth.RoleGroupAdmin}}
	groupResult, _, err := service.Search(ctx, groupAdmin, SearchInput{Category: CategoryAssetChange, Page: page})
	if err != nil {
		t.Fatalf("Search(group admin) error = %v", err)
	}
	if groupResult.Total != 3 {
		t.Fatalf("group administrator notifications = %d, want organization and targeted events", groupResult.Total)
	}
	workingResult, _, err := service.Search(ctx, recipient, SearchInput{Category: CategoryWorkingVersion, Page: page})
	if err != nil {
		t.Fatalf("Search(recipient working versions) error = %v", err)
	}
	if workingResult.Total != 1 || len(workingResult.Items) != 1 {
		t.Fatalf("recipient working-version notifications = total %d items %d, want 1", workingResult.Total, len(workingResult.Items))
	}
	if !strings.Contains(workingResult.Items[0].Message, "尚未上报集团") {
		t.Fatalf("working-version message = %q, want unreported explanation", workingResult.Items[0].Message)
	}
	groupWorkingResult, _, err := service.Search(ctx, groupAdmin, SearchInput{Category: CategoryWorkingVersion, Page: page})
	if err != nil {
		t.Fatalf("Search(group admin working versions) error = %v", err)
	}
	if groupWorkingResult.Total != 0 {
		t.Fatalf("group administrator working-version notifications = %d, want private events hidden", groupWorkingResult.Total)
	}
}

func TestStaleWorkingVersionReminderScope(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset stale working-version fixtures: %v", err)
	}

	var rootID, organizationID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code, organization_name, organization_type)
		VALUES ('STALE_WORK_ROOT', '长期未上报提醒测试集团', 'group') RETURNING id::text
	`).Scan(&rootID); err != nil {
		t.Fatalf("insert stale reminder root organization: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id, organization_code, organization_name, organization_type)
		VALUES ($1::uuid, 'STALE_WORK_SUB', '长期未上报提醒测试子公司', 'subsidiary') RETURNING id::text
	`, rootID).Scan(&organizationID); err != nil {
		t.Fatalf("insert stale reminder organization: %v", err)
	}

	userIDs := map[string]string{}
	for _, username := range []string{"stale_asset_user", "stale_system_user", "stale_inventory_user", "stale_group_admin"} {
		var userID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username, password_hash, display_name)
			VALUES ($1, 'not-used', $1) RETURNING id::text
		`, username).Scan(&userID); err != nil {
			t.Fatalf("insert stale reminder user %s: %v", username, err)
		}
		userIDs[username] = userID
	}

	for _, fixture := range []struct {
		roleKey        string
		permissionKey  string
		permissionName string
		username       string
	}{
		{roleKey: "stale_asset_editor", permissionKey: "asset.manage", permissionName: "管理网络资产", username: "stale_asset_user"},
		{roleKey: "stale_system_editor", permissionKey: "system.manage", permissionName: "管理业务系统", username: "stale_system_user"},
		{roleKey: "stale_inventory_editor", permissionKey: "inventory.manage", permissionName: "管理填报任务", username: "stale_inventory_user"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO permissions (permission_key, permission_name) VALUES ($1, $2)
			ON CONFLICT (permission_key) DO NOTHING
		`, fixture.permissionKey, fixture.permissionName); err != nil {
			t.Fatalf("insert stale reminder permission %s: %v", fixture.permissionKey, err)
		}
		var roleID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO roles (role_key, role_name, role_scope, description, is_system, status)
			VALUES ($1, $1, 'organization', '', false, 'enabled')
			ON CONFLICT (role_key) DO UPDATE SET status='enabled'
			RETURNING id::text
		`, fixture.roleKey).Scan(&roleID); err != nil {
			t.Fatalf("insert stale reminder role %s: %v", fixture.roleKey, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO role_permissions (role_id, permission_key) VALUES ($1::uuid, $2)
			ON CONFLICT DO NOTHING
		`, roleID, fixture.permissionKey); err != nil {
			t.Fatalf("grant stale reminder permission to role %s: %v", fixture.roleKey, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_roles (user_id, role_id, organization_id, data_scope)
			VALUES ($1::uuid, $2::uuid, $3::uuid, 'organization')
		`, userIDs[fixture.username], roleID, organizationID); err != nil {
			t.Fatalf("assign stale reminder role %s: %v", fixture.roleKey, err)
		}
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO inventory_asset_drafts (
			organization_id, asset_type, asset_value, normalized_value, asset_name,
			created_by, updated_by, created_at, updated_at
		) VALUES
			($1::uuid, 'domain', 'stale-one.example.test', 'stale-one.example.test', '长期未上报资产一', $2::uuid, $2::uuid, now()-interval '10 days', now()-interval '10 days'),
			($1::uuid, 'domain', 'stale-two.example.test', 'stale-two.example.test', '长期未上报资产二', $2::uuid, $2::uuid, now()-interval '9 days', now()-interval '9 days'),
			($1::uuid, 'domain', 'fresh.example.test', 'fresh.example.test', '近期工作版本', $2::uuid, $2::uuid, now()-interval '2 days', now()-interval '2 days')
	`, organizationID, userIDs["stale_asset_user"]); err != nil {
		t.Fatalf("insert stale asset working versions: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO inventory_business_system_drafts (
			organization_id, system_code, system_name, created_by, updated_by, created_at, updated_at
		) VALUES ($1::uuid, 'STALE-SYSTEM-001', '长期未上报业务系统', $2::uuid, $2::uuid, now()-interval '8 days', now()-interval '8 days')
	`, organizationID, userIDs["stale_system_user"]); err != nil {
		t.Fatalf("insert stale business-system working version: %v", err)
	}

	page, _ := pagination.Normalize(1, 20)
	service := NewService(pool)
	assetResult, _, err := service.Search(ctx, auth.Principal{UserID: userIDs["stale_asset_user"]}, SearchInput{Category: CategoryWorkingVersion, Page: page})
	if err != nil {
		t.Fatalf("Search(asset stale reminder) error = %v", err)
	}
	if assetResult.Total != 1 || len(assetResult.Items) != 1 {
		t.Fatalf("asset stale reminders = total %d items %d, want 1", assetResult.Total, len(assetResult.Items))
	}
	if !strings.Contains(assetResult.Items[0].Title, "网络资产") || !strings.Contains(assetResult.Items[0].Message, "2 项") || strings.Contains(assetResult.Items[0].Message, "业务系统") {
		t.Fatalf("asset stale reminder = title %q message %q", assetResult.Items[0].Title, assetResult.Items[0].Message)
	}
	if !strings.HasPrefix(assetResult.Items[0].ActionPath, "/assets?") {
		t.Fatalf("asset stale reminder action path = %q", assetResult.Items[0].ActionPath)
	}

	systemResult, _, err := service.Search(ctx, auth.Principal{UserID: userIDs["stale_system_user"]}, SearchInput{Category: CategoryWorkingVersion, Page: page})
	if err != nil {
		t.Fatalf("Search(system stale reminder) error = %v", err)
	}
	if systemResult.Total != 1 || len(systemResult.Items) != 1 || !strings.Contains(systemResult.Items[0].Title, "业务系统") {
		t.Fatalf("system stale reminders = total %d items %d title %q, want 1 business-system reminder", systemResult.Total, len(systemResult.Items), systemResult.Items[0].Title)
	}

	inventoryResult, _, err := service.Search(ctx, auth.Principal{UserID: userIDs["stale_inventory_user"]}, SearchInput{Category: CategoryWorkingVersion, Page: page})
	if err != nil {
		t.Fatalf("Search(inventory stale reminders) error = %v", err)
	}
	if inventoryResult.Total != 2 || len(inventoryResult.Items) != 2 {
		t.Fatalf("inventory stale reminders = total %d items %d, want asset and system reminders", inventoryResult.Total, len(inventoryResult.Items))
	}

	groupResult, _, err := service.Search(ctx, auth.Principal{UserID: userIDs["stale_group_admin"], Roles: []string{auth.RoleGroupAdmin}}, SearchInput{Category: CategoryWorkingVersion, Page: page})
	if err != nil {
		t.Fatalf("Search(group administrator stale reminders) error = %v", err)
	}
	if groupResult.Total != 0 {
		t.Fatalf("group administrator stale reminders = %d, want private reminders hidden", groupResult.Total)
	}
}
