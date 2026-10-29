package dashboard_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/dashboard"
	"netassets/internal/pagination"
	"netassets/internal/securityworkorder"
)

func TestWorkOrderOverdueMatchesDashboardAcrossStatusesAndPages(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("NETASSETS_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("requires a dedicated migrated test database")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	stamp := time.Now().UTC().Format("20060102150405.000000000")
	var orgID, userID string
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM security_work_orders WHERE assignee_organization_id=$1::uuid`, orgID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id=$1::uuid`, userID)
		_, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1::uuid`, orgID)
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO organizations(organization_code,organization_name,organization_type) VALUES($1,'逾期口径回归','subsidiary') RETURNING id::text`, "OVERDUE_"+stamp).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO users(username,password_hash,display_name) VALUES($1,'unused','逾期回归管理员') RETURNING id::text`, "overdue_"+stamp).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	p := auth.Principal{UserID: userID, Roles: []string{auth.RoleGroupAdmin}, Permissions: []string{auth.PermissionSecurityWorkOrder}}
	dashboardService := dashboard.NewService(pool)
	orders := securityworkorder.NewService(pool, t.TempDir())
	before, err := dashboardService.Get(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"draft", "issued", "in_progress", "pending_verification", "returned", "closed", "cancelled"} {
		for _, past := range []bool{true, false} {
			due := time.Now().Add(time.Hour)
			if past {
				due = time.Now().Add(-time.Hour)
			}
			var id string
			if err := pool.QueryRow(ctx, `INSERT INTO security_work_orders(work_order_no,title,assignee_organization_id,severity,status,remediation_requirement,due_at)
				VALUES($1,$2,$3::uuid,'high',$2,'隔离回归',$4) RETURNING id::text`, stamp+"_"+status+due.Format("150405"), status, orgID, due).Scan(&id); err != nil {
				t.Fatal(err)
			}
			item, err := orders.GetWorkOrder(ctx, p, id)
			if err != nil {
				t.Fatal(err)
			}
			want := past && status != "draft" && status != "closed" && status != "cancelled"
			if item.Overdue != want {
				t.Errorf("status=%s, past=%v: overdue=%v, want %v", status, past, item.Overdue, want)
			}
		}
	}
	for _, page := range []int{1, 2, 100} {
		result, err := orders.SearchWorkOrdersWithFilter(ctx, p, securityworkorder.WorkOrderSearchFilter{
			OrganizationID: orgID, OverdueOnly: true, Page: pagination.Query{Page: page, PageSize: 1},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Total != 4 {
			t.Errorf("page %d total=%d, want 4", page, result.Total)
		}
		for _, item := range result.Items {
			if !item.Overdue || item.Status == "draft" || item.Status == "closed" || item.Status == "cancelled" {
				t.Errorf("unexpected overdue order: %+v", item)
			}
		}
	}
	after, err := dashboardService.Get(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if got := after.OverdueWorkOrderCount - before.OverdueWorkOrderCount; got != 4 {
		t.Errorf("dashboard overdue delta=%d, want 4", got)
	}
}
