package securityworkorder

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"netassets/internal/auth"
	"netassets/internal/dashboard"
	"netassets/internal/pagination"
)

func TestDashboardDrilldownMatchesOverviewAndEmptyPageCounts(t *testing.T) {
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
	stamp := fmt.Sprint(time.Now().UnixNano())
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
	org := id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'下钻组织','subsidiary') RETURNING id::text`, "DRILL_"+stamp)
	other := id(`INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'下钻其他组织','subsidiary') RETURNING id::text`, "DRILL_OTHER_"+stamp)
	groupID := id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','只读安全看板用户') RETURNING id::text`, "drill_group_"+stamp)
	subID := id(`INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','子组织资产管理员') RETURNING id::text`, "drill_sub_"+stamp)
	roleID := id(`INSERT INTO roles(role_key,role_name,role_scope) VALUES($1,'下钻作用域','organization') RETURNING id::text`, "drill_"+stamp)
	assetID := id(`INSERT INTO assets(asset_type,asset_value,normalized_value) VALUES('domain',$1,$1) RETURNING id::text`, "drill-"+stamp+".invalid")
	batchID := id(`INSERT INTO security_test_batches(batch_code,batch_name,test_type,status,provider,authorization_note,start_at,end_at) VALUES($1,'下钻测试批次','scan','completed','隔离回归','隔离回归',now()-interval '1 day',now()) RETURNING id::text`, "DRILL_BATCH_"+stamp)
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM security_work_orders WHERE assignee_organization_id IN ($1::uuid,$2::uuid)`, org, other)
		_, _ = pool.Exec(ctx, `DELETE FROM security_findings WHERE assignee_organization_id IN ($1::uuid,$2::uuid)`, org, other)
		_, _ = pool.Exec(ctx, `DELETE FROM security_test_batches WHERE id=$1::uuid`, batchID)
		_, _ = pool.Exec(ctx, `DELETE FROM assets WHERE id=$1::uuid`, assetID)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id=$1::uuid`, subID)
		_, _ = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role_id=$1::uuid`, roleID)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE id=$1::uuid`, roleID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1::uuid,$2::uuid)`, groupID, subID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id IN ($1::uuid,$2::uuid)`, org, other)
	}()
	exec(`INSERT INTO permissions(permission_key,permission_name) VALUES('asset.manage','资产管理'),('security_workorder.view','查看工单') ON CONFLICT DO NOTHING`)
	exec(`INSERT INTO role_permissions(role_id,permission_key) VALUES($1::uuid,'asset.manage'),($1::uuid,'security_workorder.view')`, roleID)
	exec(`INSERT INTO user_roles(user_id,role_id,organization_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,'organization')`, subID, roleID, org)
	group := auth.Principal{UserID: groupID, Roles: []string{auth.RoleSecurityOperator}, Permissions: []string{auth.PermissionSecurityWorkOrder}}
	sub := auth.Principal{UserID: subID, Permissions: []string{"asset.manage", auth.PermissionSecurityWorkOrderView}}
	findings := make([]string, 9)
	for index := range findings {
		owner, status := org, "in_progress"
		var asset any = assetID
		if index == 4 {
			asset = nil
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
		findings[index] = id(`INSERT INTO security_findings(finding_code,title,severity,status,assignee_organization_id,asset_id,description,discovered_at) VALUES($1,$1,'high',$2,$3::uuid,$4::uuid,'下钻回归','2021-05-06 12:00:00+00') RETURNING id::text`, fmt.Sprintf("DRILL_%s_%d", stamp, index), status, owner, asset)
	}
	for _, index := range []int{0, 3, 5, 7, 8} {
		exec(`UPDATE security_findings SET test_batch_id=$2::uuid WHERE id=$1::uuid`, findings[index], batchID)
	}
	order := func(name, status, owner string, issued bool, indexes ...int) {
		var issuedAt, closedAt any
		if issued {
			issuedAt = "2021-05-07 12:00:00+00"
		}
		if status == "closed" {
			closedAt = "2021-05-08 12:00:00+00"
		}
		due := time.Now().Add(time.Hour)
		if name == "active" {
			due = time.Now().Add(-time.Hour)
		}
		orderID := id(`INSERT INTO security_work_orders(work_order_no,title,severity,status,assignee_organization_id,remediation_requirement,due_at,issued_at,closed_at) VALUES($1,'下钻回归','high',$2,$3::uuid,'回归',$4,$5::timestamptz,$6::timestamptz) RETURNING id::text`, stamp+"_"+name, status, owner, due, issuedAt, closedAt)
		for _, index := range indexes {
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
	order("outside", "issued", other, true, 7, 8)
	service := NewService(pool, t.TempDir())
	for _, actor := range []struct {
		name      string
		principal auth.Principal
	}{{"group dashboard viewer", group}, {"subsidiary", sub}} {
		t.Run(actor.name, func(t *testing.T) {
			overview, err := dashboard.NewService(pool).GetSecurityOverview(ctx, actor.principal)
			if err != nil {
				t.Fatal(err)
			}
			expectedTestFindings := int64(5)
			if actor.principal.UserID == sub.UserID {
				expectedTestFindings = 2
			}
			if overview.TestCoverage.TestFindingCount != expectedTestFindings {
				t.Fatalf("test coverage count=%d expected=%d", overview.TestCoverage.TestFindingCount, expectedTestFindings)
			}
			cases := []struct {
				name       string
				options    FindingSearchOptions
				assetState string
				expected   int64
			}{
				{"all", FindingSearchOptions{DashboardScope: true}, "", overview.ClosureFunnel.DiscoveredFindingCount},
				{"linked", FindingSearchOptions{DashboardScope: true}, "linked", overview.ClosureFunnel.AssetLinkedFindingCount},
				{"dispatched", FindingSearchOptions{DashboardScope: true, DispatchState: "dispatched"}, "", overview.ClosureFunnel.DispatchedFindingCount},
				{"remediating", FindingSearchOptions{DashboardScope: true, DispatchState: "remediating"}, "", overview.ClosureFunnel.RemediatingFindingCount},
				{"verification", FindingSearchOptions{DashboardScope: true, DispatchState: "pending_verification"}, "", overview.ClosureFunnel.PendingVerificationFindingCount},
				{"closed", FindingSearchOptions{DashboardScope: true, DispatchState: "closed"}, "", overview.ClosureFunnel.ClosedFindingCount},
				{"test findings", FindingSearchOptions{DashboardScope: true, TestBatchOnly: true}, "", overview.TestCoverage.TestFindingCount},
			}
			if actor.principal.CanViewGroupSecurityDashboard() {
				cases = append(cases, struct {
					name       string
					options    FindingSearchOptions
					assetState string
					expected   int64
				}{"undispatched", FindingSearchOptions{DashboardScope: true, DispatchState: "undispatched"}, "", overview.UndispatchedFindings.FindingCount})
			}
			for _, test := range cases {
				for _, pageNumber := range []int{1, 10000} {
					page := pagination.Query{Page: pageNumber, PageSize: 1}
					list, err := service.SearchFindings(ctx, actor.principal, "", "", test.assetState, page, test.options)
					if err != nil || int64(list.Total) != test.expected {
						t.Errorf("%s page %d total=%d expected=%d: %v", test.name, pageNumber, list.Total, test.expected, err)
					}
				}
			}
			for _, test := range []struct {
				status   string
				overdue  bool
				expected int64
			}{{"active", false, overview.WorkOrderFindings.ActiveWorkOrderCount}, {"pending_verification", false, overview.WorkOrderFindings.PendingVerificationWorkOrderCount}, {"closed", false, overview.WorkOrderFindings.ClosedWorkOrderCount}, {"", true, overview.WorkOrderFindings.OverdueWorkOrderCount}} {
				for _, pageNumber := range []int{1, 10000} {
					list, err := service.SearchWorkOrdersWithFilter(ctx, actor.principal, WorkOrderSearchFilter{DashboardScope: true, Status: test.status, OverdueOnly: test.overdue, Page: pagination.Query{Page: pageNumber, PageSize: 1}})
					if err != nil || int64(list.Total) != test.expected {
						t.Errorf("orders status=%s overdue=%v page=%d total=%d expected=%d: %v", test.status, test.overdue, pageNumber, list.Total, test.expected, err)
					}
				}
			}
		})
	}
	for _, test := range []struct {
		options  FindingSearchOptions
		expected int
	}{{FindingSearchOptions{DashboardScope: true, DiscoveredDate: "2021-05-06"}, 9}, {FindingSearchOptions{DashboardScope: true, DispatchedDate: "2021-05-07"}, 6}, {FindingSearchOptions{DashboardScope: true, ClosedDate: "2021-05-08"}, 1}} {
		for _, pageNumber := range []int{1, 10000} {
			list, err := service.SearchFindings(ctx, group, "", "", "", pagination.Query{Page: pageNumber, PageSize: 1}, test.options)
			if err != nil || list.Total != test.expected {
				t.Errorf("date filter %+v page=%d total=%d expected=%d: %v", test.options, pageNumber, list.Total, test.expected, err)
			}
		}
	}
	for _, actor := range []auth.Principal{group, sub} {
		list, err := service.SearchFindings(ctx, actor, org, "", "", pagination.Query{Page: 1, PageSize: 100}, FindingSearchOptions{DashboardScope: true, Severity: "high"})
		expected := 8
		if actor.UserID == sub.UserID {
			expected = 5
		}
		if err != nil || list.Total != expected {
			t.Errorf("organization filtered dashboard total=%d expected=%d: %v", list.Total, expected, err)
		}
	}
}
