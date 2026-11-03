package dashboard_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/dashboard"
)

func TestSecurityClosureScopeDeduplicationAndCoverage(t *testing.T) {
	url := os.Getenv("NETASSETS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var fixtureTime time.Time
	var fixtureDate string
	if err := pool.QueryRow(ctx, `SELECT (current_date - interval '12 hours')::timestamptz, to_char(current_date - 1, 'YYYY-MM-DD')`).Scan(&fixtureTime, &fixtureDate); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	id := func(query string, args ...any) string {
		var value string
		if err := pool.QueryRow(ctx, query, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	exec := func(query string, args ...any) {
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	org := id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'闭环组织','subsidiary') RETURNING id::text`, "CLOSURE_"+stamp)
	other := id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'其他组织','subsidiary') RETURNING id::text`, "CLOSURE_OTHER_"+stamp)
	user := id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','闭环回归用户') RETURNING id::text`, "closure_"+stamp)
	role := id(`INSERT INTO roles(role_key,role_name,role_scope) VALUES($1,'闭环测试角色','organization') RETURNING id::text`, "closure_"+stamp)
	asset := id(`INSERT INTO assets(asset_type,asset_value,normalized_value) VALUES('domain',$1,$1) RETURNING id::text`, "closure-"+stamp+".invalid")
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM security_work_orders WHERE assignee_organization_id IN ($1::uuid,$2::uuid)`, org, other)
		_, _ = pool.Exec(ctx, `DELETE FROM security_findings WHERE assignee_organization_id IN ($1::uuid,$2::uuid)`, org, other)
		_, _ = pool.Exec(ctx, `DELETE FROM business_systems WHERE owner_organization_id IN ($1::uuid,$2::uuid)`, org, other)
		_, _ = pool.Exec(ctx, `DELETE FROM assets WHERE id=$1::uuid`, asset)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1::uuid`, user)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE id=$1::uuid`, role)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, user)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id IN ($1::uuid,$2::uuid)`, org, other)
	}()
	exec(`INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,'organization')`, user, role, org)
	systemA := id(`INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status) VALUES($1,'系统A',$2::uuid,'running') RETURNING id::text`, "CA_"+stamp, org)
	systemB := id(`INSERT INTO business_systems(system_code,system_name,owner_organization_id,system_status) VALUES($1,'系统B',$2::uuid,'running') RETURNING id::text`, "CB_"+stamp, org)
	group := auth.Principal{UserID: user, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{auth.PermissionSecurityWorkOrder}}
	sub := auth.Principal{UserID: user, Permissions: []string{auth.PermissionSecurityWorkOrderExecute}}
	service := dashboard.NewService(pool)
	before, err := service.GetSecurityOverview(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	findings := make([]string, 8)
	for index := range findings {
		owner, system, severity, status := org, systemA, "medium", "in_progress"
		if index == 0 {
			severity = "critical"
		}
		if index == 1 {
			severity, system = "high", systemB
		}
		if index == 5 {
			status = "fixed"
		}
		if index == 6 {
			status = "resolved"
		}
		if index == 7 {
			owner = other
		}
		findings[index] = id(`INSERT INTO security_findings(finding_code,title,severity,status,assignee_organization_id,business_system_id,asset_id,description,discovered_at) VALUES($1,$1,$2,$3,$4::uuid,$5::uuid,$6::uuid,'回归',$7) RETURNING id::text`, stamp+"_"+string(rune('a'+index)), severity, status, owner, system, asset, fixtureTime)
	}
	order := func(name, status, owner string, issued bool, findingIndexes ...int) {
		var issuedAt, closedAt any
		if issued {
			issuedAt = fixtureTime.Add(time.Hour)
		}
		if status == "closed" {
			closedAt = fixtureTime.Add(2 * time.Hour)
		}
		due := time.Now().Add(time.Hour)
		if name == "active" {
			due = time.Now().Add(-time.Minute)
		}
		orderID := id(`INSERT INTO security_work_orders(work_order_no,title,severity,status,assignee_organization_id,remediation_requirement,due_at,issued_at,closed_at) VALUES($1,'闭环回归','high',$2,$3::uuid,'回归',$4,$5,$6) RETURNING id::text`, stamp+"_"+name, status, owner, due, issuedAt, closedAt)
		for _, index := range findingIndexes {
			exec(`INSERT INTO security_work_order_findings(work_order_id,finding_id) VALUES($1::uuid,$2::uuid)`, orderID, findings[index])
		}
	}
	order("draft", "draft", org, false, 1)
	order("cancelled", "cancelled", org, true, 2)
	order("history", "closed", org, true, 3)
	order("duplicate_active", "in_progress", org, true, 3)
	order("active", "in_progress", org, true, 3)
	order("verification", "pending_verification", org, true, 4)
	order("closed", "closed", org, true, 5, 6)
	order("outside", "issued", other, true, 7)
	after, err := service.GetSecurityOverview(ctx, group)
	if err != nil {
		t.Fatal(err)
	}
	funnel := after.ClosureFunnel
	if funnel.DiscoveredFindingCount-before.ClosureFunnel.DiscoveredFindingCount != 8 || funnel.DispatchedFindingCount-before.ClosureFunnel.DispatchedFindingCount != 5 || funnel.RemediatingFindingCount-before.ClosureFunnel.RemediatingFindingCount != 2 || funnel.PendingVerificationFindingCount-before.ClosureFunnel.PendingVerificationFindingCount != 1 || funnel.ClosedFindingCount-before.ClosureFunnel.ClosedFindingCount != 1 {
		t.Fatalf("unexpected funnel: before=%+v after=%+v", before.ClosureFunnel, funnel)
	}
	gap := after.UndispatchedFindings
	if gap.FindingCount-before.UndispatchedFindings.FindingCount != 3 || gap.CriticalCount-before.UndispatchedFindings.CriticalCount != 1 || gap.HighCount-before.UndispatchedFindings.HighCount != 1 || gap.OldestDiscoveredAt == nil {
		t.Fatalf("unexpected gap: %+v", gap)
	}
	coverage := after.WorkOrderFindings
	if coverage.ActiveWorkOrderCount-before.WorkOrderFindings.ActiveWorkOrderCount != 4 || coverage.ActiveFindingCount-before.WorkOrderFindings.ActiveFindingCount != 3 || coverage.OverdueWorkOrderCount-before.WorkOrderFindings.OverdueWorkOrderCount != 1 || coverage.OverdueFindingCount-before.WorkOrderFindings.OverdueFindingCount != 1 {
		t.Fatalf("unexpected coverage: %+v", coverage)
	}
	visible, err := service.GetSecurityOverview(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	if visible.CanViewUndispatched || visible.UndispatchedFindings.FindingCount != 0 || visible.ClosureFunnel.DiscoveredFindingCount != 5 || visible.ClosureFunnel.DispatchedFindingCount != 4 {
		t.Fatalf("subsidiary scope leak or omission: %+v", visible)
	}
	if len(visible.OrganizationPerformance) != 1 || visible.OrganizationPerformance[0].ActiveFindingCount != 2 || visible.OrganizationPerformance[0].PendingVerificationFindingCount != 1 {
		t.Fatalf("unexpected organization coverage: %+v", visible.OrganizationPerformance)
	}
	for _, trend := range visible.FindingTrend {
		if trend.Date != fixtureDate {
			continue
		}
		if trend.NewFindingCount != 5 || trend.DispatchedFindingCount != 4 || trend.ClosedFindingCount != 1 {
			t.Fatalf("unexpected trend: %+v", trend)
		}
		return
	}
	t.Fatalf("missing trend for fixture date %s", fixtureDate)
}
