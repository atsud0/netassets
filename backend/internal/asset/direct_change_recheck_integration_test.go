package asset

// A group administrator can write the formal asset ledger directly. That door is
// necessary for headquarters-led registration, but it is the only route into the
// ledger that nobody reviews, and the ledger is what both asset ownership and
// vulnerability accounting are built on. These tests pin the contract that every
// direct write states a reason, raises a recheck item for the owning
// organization, and can only be closed by that organization.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

func TestDirectLedgerWritesRequireReasonAndRaiseRecheckItem(t *testing.T) {
	fixture := newDirectChangeFixture(t)
	ctx := context.Background()
	service := NewService(fixture.pool)
	group := fixture.principal(t, "dc_group_admin", nil)

	input := validWorkingAssetInput(fixture.organizationA)
	input.Value = "198.51.100.10"
	// Without a reason the write is rejected: a recheck item nobody can act on is
	// the same as no recheck item, so the reason is mandatory rather than
	// best-effort.
	if _, err := service.Create(ctx, group, input, "manual", "req-no-reason", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("direct create without a reason = %v, want ErrInvalid", err)
	}
	var createdWithoutReason int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM assets WHERE asset_value='198.51.100.10'`).Scan(&createdWithoutReason); err != nil {
		t.Fatalf("count rejected assets: %v", err)
	}
	if createdWithoutReason != 0 {
		t.Fatal("a rejected direct create still wrote the asset; the reason check runs after the write")
	}

	input.DirectChangeReason = "集团资产清查中确认的存量服务器"
	created, err := service.Create(ctx, group, input, "manual", "req-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("direct create with a reason: %v", err)
	}
	recheck, err := service.GetDirectChangeRecheckTask(ctx, group, fixture.onlyRecheckID(t, created.ID))
	if err != nil {
		t.Fatalf("load recheck item: %v", err)
	}
	if recheck.Action != DirectChangeCreate || recheck.Status != DirectChangeStatusOpen {
		t.Fatalf("recheck item = action %q status %q, want create/open", recheck.Action, recheck.Status)
	}
	if recheck.Reason != input.DirectChangeReason {
		t.Fatalf("recheck reason = %q, want %q", recheck.Reason, input.DirectChangeReason)
	}
	var snapshot struct {
		After Asset `json:"after"`
	}
	if err := json.Unmarshal(recheck.Snapshot, &snapshot); err != nil {
		t.Fatalf("decode create snapshot: %v", err)
	}
	if snapshot.After.ID != created.ID || snapshot.After.Value != created.Value {
		t.Fatalf("create snapshot = %+v, want the created asset %s/%s", snapshot.After, created.ID, created.Value)
	}

	// Update: the item carries both the before state and the field-level diff, so
	// the organization can judge the change without trusting the writer.
	update := createInputFromAsset(created)
	update.Notes = "集团确认为核心服务器"
	update.DirectChangeReason = "核心资产复核"
	if _, err := service.Update(ctx, group, created.ID, update, "req-update", "127.0.0.1"); err != nil {
		t.Fatalf("direct update: %v", err)
	}
	recheck, err = service.GetDirectChangeRecheckTask(ctx, group, fixture.onlyRecheckID(t, created.ID))
	if err != nil {
		t.Fatalf("reload recheck item: %v", err)
	}
	if recheck.Action != DirectChangeUpdate {
		t.Fatalf("recheck action after update = %q, want update", recheck.Action)
	}
	var changed []struct {
		Field  string `json:"field"`
		Before string `json:"before"`
		After  string `json:"after"`
	}
	if err := json.Unmarshal(recheck.ChangedFields, &changed); err != nil {
		t.Fatalf("decode changed fields: %v", err)
	}
	foundNotes := false
	for _, c := range changed {
		if c.Field == "notes" && c.After == update.Notes {
			foundNotes = true
		}
	}
	if !foundNotes {
		t.Fatalf("changed fields = %+v, want the notes edit", changed)
	}
	snap := struct {
		Before Asset `json:"before"`
		After  Asset `json:"after"`
	}{}
	if err := json.Unmarshal(recheck.Snapshot, &snap); err != nil {
		t.Fatalf("decode update snapshot: %v", err)
	}
	if snap.Before.ID != created.ID || snap.After.ID != created.ID {
		t.Fatalf("update snapshot must record both states of %s, got %s -> %s", created.ID, snap.Before.ID, snap.After.ID)
	}
	if snap.Before.Notes == snap.After.Notes {
		t.Fatal("update snapshot recorded the same notes before and after; the after-state was read outside the write transaction")
	}

	// Delete: the item survives the soft delete because the asset row is only
	// flagged, and the before state is what the organization needs to notice the
	// asset is gone.
	if err := service.Delete(ctx, group, created.ID, nil, "集团确认该资产已下线", "req-delete", "127.0.0.1"); err != nil {
		t.Fatalf("direct delete: %v", err)
	}
	if err := service.Delete(ctx, group, created.ID, nil, "集团确认该资产已下线", "req-delete-again", "127.0.0.1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting an already deleted asset = %v, want ErrNotFound", err)
	}
	var deleteRechecks int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM asset_direct_change_recheck_tasks
		WHERE asset_id=$1::uuid AND action='delete' AND status='open'`, created.ID).Scan(&deleteRechecks); err != nil {
		t.Fatalf("count delete recheck items: %v", err)
	}
	if deleteRechecks != 1 {
		t.Fatalf("delete raised %d recheck items, want 1", deleteRechecks)
	}
}

func TestDirectChangeRecheckIsScopedAndClosedByTheOwningOrganization(t *testing.T) {
	fixture := newDirectChangeFixture(t)
	ctx := context.Background()
	service := NewService(fixture.pool)
	group := fixture.principal(t, "dc_group_admin", nil)
	orgA := fixture.principal(t, "dc_org_admin_a", nil)
	orgB := fixture.principal(t, "dc_org_admin_b", nil)

	input := validWorkingAssetInput(fixture.organizationA)
	input.Value = "198.51.100.20"
	input.DirectChangeReason = "集团资产清查补录"
	created, err := service.Create(ctx, group, input, "manual", "req-scope-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("direct create: %v", err)
	}
	recheckID := fixture.onlyRecheckID(t, created.ID)

	page, _ := pagination.Normalize(1, 20)
	orgAItems, err := service.SearchDirectChangeRecheckTasks(ctx, orgA, DirectChangeRecheckFilter{Page: page})
	if err != nil {
		t.Fatalf("organization A list: %v", err)
	}
	if orgAItems.Total != 1 || orgAItems.Items[0].ID != recheckID {
		t.Fatalf("organization A sees %+v, want exactly its own recheck item %s", orgAItems, recheckID)
	}
	orgBItems, err := service.SearchDirectChangeRecheckTasks(ctx, orgB, DirectChangeRecheckFilter{Page: page})
	if err != nil {
		t.Fatalf("organization B list: %v", err)
	}
	if orgBItems.Total != 0 {
		t.Fatalf("organization B sees %d recheck items; the list leaks across organizations", orgBItems.Total)
	}
	// Reading the item directly by id is scoped the same way as the list, so a
	// guessed id is not a way around the organization boundary.
	if _, err := service.GetDirectChangeRecheckTask(ctx, orgB, recheckID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("organization B reading another organization's recheck item = %v, want ErrNotFound", err)
	}

	// The writer may not sign off on its own write: a group administrator has no
	// organization appointment on the owning organization, so the recheck record
	// is only worth anything if a second party closes it.
	if _, err := service.ConfirmDirectChangeRecheckTask(ctx, group, recheckID, DirectChangeConfirmationInput{
		Status: DirectChangeStatusConfirmed, Note: "自行确认",
	}, "req-group-confirm", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("group administrator closing its own recheck item = %v, want ErrForbidden", err)
	}
	if _, err := service.ConfirmDirectChangeRecheckTask(ctx, orgB, recheckID, DirectChangeConfirmationInput{
		Status: DirectChangeStatusConfirmed, Note: "认可变更",
	}, "req-b-confirm", "127.0.0.1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("another organization closing the recheck item = %v, want ErrForbidden", err)
	}
	if _, err := service.ConfirmDirectChangeRecheckTask(ctx, orgA, recheckID, DirectChangeConfirmationInput{
		Status: "", Note: "认可变更",
	}, "req-a-empty", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("confirming without a decision = %v, want ErrInvalid", err)
	}
	if _, err := service.ConfirmDirectChangeRecheckTask(ctx, orgA, recheckID, DirectChangeConfirmationInput{
		Status: DirectChangeStatusConfirmed, Note: "  ",
	}, "req-a-blank-note", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("confirming without a note = %v, want ErrInvalid", err)
	}

	closed, err := service.ConfirmDirectChangeRecheckTask(ctx, orgA, recheckID, DirectChangeConfirmationInput{
		Status: DirectChangeStatusConfirmed, Note: "确认归属无误",
	}, "req-a-confirm", "127.0.0.1")
	if err != nil {
		t.Fatalf("owning organization closing the recheck item: %v", err)
	}
	if closed.Status != DirectChangeStatusConfirmed || closed.ConfirmedBy != orgA.UserID || closed.ConfirmedAt == nil {
		t.Fatalf("closed recheck item = %+v, want confirmed by %s with a timestamp", closed, orgA.UserID)
	}
	// A closed item is a finished decision, not something to overwrite.
	if _, err := service.ConfirmDirectChangeRecheckTask(ctx, orgA, recheckID, DirectChangeConfirmationInput{
		Status: DirectChangeStatusDisputed, Note: "改主意了",
	}, "req-a-again", "127.0.0.1"); !errors.Is(err, ErrConflict) {
		t.Fatalf("re-closing a closed recheck item = %v, want ErrConflict", err)
	}

	// The rejected repeat must not touch the stored decision and must not add a
	// second audit line: the first conclusion stays the only auditable one.
	reloaded, err := service.GetDirectChangeRecheckTask(ctx, orgA, recheckID)
	if err != nil {
		t.Fatalf("reload closed recheck item: %v", err)
	}
	if reloaded.Status != DirectChangeStatusConfirmed || reloaded.ConfirmationNote != "确认归属无误" {
		t.Fatalf("recheck item after rejected repeat = status %q note %q, want confirmed/确认归属无误", reloaded.Status, reloaded.ConfirmationNote)
	}
	var confirmAudits int
	if err := fixture.pool.QueryRow(ctx, `
		SELECT count(*) FROM audit_logs
		WHERE action='asset_direct_change_recheck.confirm' AND resource_id=$1::uuid`, recheckID).Scan(&confirmAudits); err != nil {
		t.Fatalf("count confirmation audits: %v", err)
	}
	if confirmAudits != 1 {
		t.Fatalf("confirmation audit rows = %d, want exactly 1", confirmAudits)
	}
}

type directChangeFixture struct {
	pool          *pgxpool.Pool
	organizationA string
	organizationB string
	userIDs       []string
}

func newDirectChangeFixture(t *testing.T) *directChangeFixture {
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
	if _, err := pool.Exec(ctx, `TRUNCATE organizations, users CASCADE`); err != nil {
		t.Fatalf("reset direct change fixtures: %v", err)
	}
	var groupID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (organization_code,organization_name,organization_type)
		VALUES ('DC_GROUP','直接变更测试集团','group') RETURNING id::text`).Scan(&groupID); err != nil {
		t.Fatalf("insert group: %v", err)
	}
	var organizationA, organizationB string
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id,organization_code,organization_name,organization_type)
		VALUES ($1::uuid,'DC_A','直接变更测试子公司甲','subsidiary') RETURNING id::text`, groupID).Scan(&organizationA); err != nil {
		t.Fatalf("insert organization A: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO organizations (parent_id,organization_code,organization_name,organization_type)
		VALUES ($1::uuid,'DC_B','直接变更测试子公司乙','subsidiary') RETURNING id::text`, groupID).Scan(&organizationB); err != nil {
		t.Fatalf("insert organization B: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO permissions (permission_key,permission_name)
		VALUES ('asset.manage','管理网络资产') ON CONFLICT (permission_key) DO NOTHING`); err != nil {
		t.Fatalf("insert permission: %v", err)
	}
	var roleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key,role_name,role_scope,description,is_system,status)
		VALUES ('dc_org_role','直接变更组织角色','organization','',false,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text`).Scan(&roleID); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO role_permissions (role_id,permission_key) VALUES ($1::uuid,'asset.manage') ON CONFLICT DO NOTHING`, roleID); err != nil {
		t.Fatalf("grant permission: %v", err)
	}
	fixture := &directChangeFixture{pool: pool, organizationA: organizationA, organizationB: organizationB}
	insertUser := func(username, displayName, organizationID, scope string) string {
		t.Helper()
		var userID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (username,password_hash,display_name)
			VALUES ($1,'not-used',$2) RETURNING id::text`, username, displayName).Scan(&userID); err != nil {
			t.Fatalf("insert user %s: %v", username, err)
		}
		if organizationID != "" {
			if _, err := pool.Exec(ctx, `
				INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
				VALUES ($1::uuid,$2::uuid,$3::uuid,$4)`, userID, roleID, organizationID, scope); err != nil {
				t.Fatalf("assign role to %s: %v", username, err)
			}
		}
		fixture.userIDs = append(fixture.userIDs, userID)
		return userID
	}
	// The group administrator is appointed without an organization, exactly as
	// production does, so the scope checks under test are the real ones.
	var groupRoleID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO roles (role_key,role_name,role_scope,description,is_system,status)
		VALUES ('dc_group_role','直接变更集团角色','group','',true,'enabled')
		ON CONFLICT (role_key) DO UPDATE SET status='enabled' RETURNING id::text`).Scan(&groupRoleID); err != nil {
		t.Fatalf("insert group role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id,permission_key)
		SELECT $1::uuid, permission_key FROM permissions ON CONFLICT DO NOTHING`, groupRoleID); err != nil {
		t.Fatalf("grant group permissions: %v", err)
	}
	var groupAdminID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (username,password_hash,display_name)
		VALUES ('dc_group_admin','not-used','直接变更集团管理员') RETURNING id::text`).Scan(&groupAdminID); err != nil {
		t.Fatalf("insert group administrator: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id,role_id,organization_id,data_scope)
		VALUES ($1::uuid,$2::uuid,NULL,'all')`, groupAdminID, groupRoleID); err != nil {
		t.Fatalf("assign group role: %v", err)
	}
	fixture.userIDs = append(fixture.userIDs, groupAdminID)
	insertUser("dc_org_admin_a", "直接变更甲公司管理员", organizationA, "organization")
	insertUser("dc_org_admin_b", "直接变更乙公司管理员", organizationB, "organization")
	return fixture
}

// principal reads the role and permission grants back from the database, so the
// tests authorize exactly like production instead of trusting hand-written role
// strings.
func (f *directChangeFixture) principal(t *testing.T, username string, organizationID *string) auth.Principal {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := f.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE username=$1`, username).Scan(&userID); err != nil {
		t.Fatalf("load user %s: %v", username, err)
	}
	principal := auth.Principal{UserID: userID, Username: username, Roles: []string{}, Permissions: []string{}}
	if username == "dc_group_admin" {
		principal.Roles = []string{"group_admin"}
	} else {
		principal.Roles = []string{"organization_admin"}
	}
	rows, err := f.pool.Query(ctx, `
		SELECT DISTINCT r.role_key
		FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
		WHERE ur.user_id=$1::uuid`, userID)
	if err != nil {
		t.Fatalf("load roles for %s: %v", username, err)
	}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			rows.Close()
			t.Fatalf("scan role for %s: %v", username, err)
		}
		principal.Roles = append(principal.Roles, role)
	}
	rows.Close()
	permissionRows, err := f.pool.Query(ctx, `
		SELECT DISTINCT rp.permission_key
		FROM user_roles ur JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
		JOIN role_permissions rp ON rp.role_id=r.id
		WHERE ur.user_id=$1::uuid`, userID)
	if err != nil {
		t.Fatalf("load permissions for %s: %v", username, err)
	}
	for permissionRows.Next() {
		var permission string
		if err := permissionRows.Scan(&permission); err != nil {
			permissionRows.Close()
			t.Fatalf("scan permission for %s: %v", username, err)
		}
		principal.Permissions = append(principal.Permissions, permission)
	}
	permissionRows.Close()
	_ = organizationID
	return principal
}

// onlyRecheckID reads the recheck item the write just raised for this asset. A
// direct write raises exactly one, so a different count is a real defect rather
// than a test convenience problem.
func (f *directChangeFixture) onlyRecheckID(t *testing.T, assetID string) string {
	t.Helper()
	var id string
	if err := f.pool.QueryRow(context.Background(), `
		SELECT id::text FROM asset_direct_change_recheck_tasks WHERE asset_id=$1::uuid
		ORDER BY created_at DESC, id DESC LIMIT 1`, assetID).Scan(&id); err != nil {
		t.Fatalf("load recheck item for asset %s: %v", assetID, err)
	}
	return id
}

// Batch and import are the two bulk doors into the formal ledger. If they were
// left out of the reason contract, a group administrator could bulk-write the
// ledger and produce no recheck item at all, which is the same hole the
// single-row audit was built to close.
func TestDirectChangeBulkWritesAlsoLeaveRecheckItems(t *testing.T) {
	fixture := newDirectChangeFixture(t)
	ctx := context.Background()
	service := NewService(fixture.pool)
	group := fixture.principal(t, "dc_group_admin", nil)

	input := validWorkingAssetInput(fixture.organizationA)
	input.Value = "198.51.100.30"
	input.DirectChangeReason = "集团资产清查补录"
	first, err := service.Create(ctx, group, input, "manual", "req-bulk-create", "127.0.0.1")
	if err != nil {
		t.Fatalf("seed asset: %v", err)
	}
	input.Value = "198.51.100.31"
	second, err := service.Create(ctx, group, input, "manual", "req-bulk-create-2", "127.0.0.1")
	if err != nil {
		t.Fatalf("seed second asset: %v", err)
	}
	status := "disabled"
	batch, err := service.UpdateBatch(ctx, group, BatchUpdateInput{
		IDs:                []string{first.ID, second.ID},
		Status:             &status,
		DirectChangeReason: "集团统一停用",
	}, "req-bulk-batch", "127.0.0.1")
	if err != nil {
		t.Fatalf("direct batch update: %v", err)
	}
	if batch.SuccessCount != 2 || batch.FailureCount != 0 {
		t.Fatalf("batch result = %+v, want two successful items", batch)
	}
	for _, id := range []string{first.ID, second.ID} {
		recheck, err := service.GetDirectChangeRecheckTask(ctx, group, fixture.onlyRecheckID(t, id))
		if err != nil {
			t.Fatalf("load batch recheck item for %s: %v", id, err)
		}
		if recheck.Action != DirectChangeUpdate || recheck.Reason != "集团统一停用" {
			t.Fatalf("batch recheck item = %+v, want an update carrying the batch reason", recheck)
		}
	}

	// A bulk write without a reason is rejected the same way a single write is,
	// item by item, so the batch result carries the failure instead of writing
	// unaudited ledger rows.
	input.Value = "198.51.100.32"
	input.DirectChangeReason = ""
	if _, err := service.Create(ctx, group, input, "manual", "req-no-reason-bulk", "127.0.0.1"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("direct create without a reason = %v, want ErrInvalid", err)
	}
	rejected, err := service.UpdateBatch(ctx, group, BatchUpdateInput{
		IDs:    []string{first.ID},
		Status: &status,
	}, "req-bulk-batch-no-reason", "127.0.0.1")
	if err != nil {
		t.Fatalf("batch without a reason: %v", err)
	}
	if rejected.FailureCount != 1 || rejected.Items[0].Code != "invalid" {
		t.Fatalf("batch without a reason = %+v, want one invalid item", rejected.Items)
	}
}
