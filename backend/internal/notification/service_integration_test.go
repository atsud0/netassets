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
	if summary.UnreadCount != 2 {
		t.Fatalf("recipient unread count = %d, want 2", summary.UnreadCount)
	}
	if err := service.MarkRead(ctx, recipient, result.Items[0].Key); err != nil {
		t.Fatalf("MarkRead(recipient) error = %v", err)
	}
	readSummary, err := service.Summary(ctx, recipient)
	if err != nil {
		t.Fatalf("Summary(recipient) error = %v", err)
	}
	if readSummary.UnreadCount != 1 {
		t.Fatalf("recipient unread count after marking one message = %d, want 1", readSummary.UnreadCount)
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
}
