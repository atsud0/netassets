package securityworkorder

import (
	"errors"
	"strings"
	"testing"
)

func TestDashboardFindingFilterValidation(t *testing.T) {
	for _, filter := range []DashboardFindingFilter{
		{DispatchState: "invalid"}, {DiscoveredDate: "2026-02-30"}, {DispatchedDate: "2026-1-02"},
		{ClosedDate: "2026-01-02' OR true --"}, {ClosedDate: "2026-01-02T00:00:00Z"},
	} {
		if _, err := dashboardFindingFilterSQL(filter); !errors.Is(err, ErrInvalid) {
			t.Fatalf("filter %+v should be rejected: %v", filter, err)
		}
	}
	if query, err := dashboardFindingFilterSQL(DashboardFindingFilter{}); err != nil || query != "" {
		t.Fatalf("default filter altered existing search: %q, %v", query, err)
	}
	query, err := dashboardFindingFilterSQL(DashboardFindingFilter{Scope: true, DispatchState: "closed", ClosedDate: "2026-11-03"})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"dashboard_order.issued_at IS NOT NULL", "dashboard_order.status NOT IN ('draft','cancelled')", "(dashboard_order.status <> 'closed') DESC", "dashboard_order.assignee_organization_id IN (SELECT id FROM accessible)", "DATE '2026-11-03'", "f.status IN ('fixed','false_positive','ignored','duplicate','risk_accepted')"} {
		if !strings.Contains(query, fragment) {
			t.Errorf("query missing %q", fragment)
		}
	}
	if strings.Contains(query, "'resolved'") {
		t.Fatal("organization resolution must not count as verified closure")
	}
	testQuery, err := dashboardFindingFilterSQL(DashboardFindingFilter{TestBatchOnly: true})
	if err != nil || !strings.Contains(testQuery, "f.test_batch_id IS NOT NULL") {
		t.Fatalf("test coverage drilldown must exclude manual findings: %q, %v", testQuery, err)
	}
}

func TestDashboardWorkOrderAliases(t *testing.T) {
	for _, status := range []string{"active", "remediating", "dispatched"} {
		if !dashboardWorkOrderStatus(status) {
			t.Errorf("missing alias %s", status)
		}
	}
	for _, status := range []string{"closed", "invalid", "' OR true"} {
		if dashboardWorkOrderStatus(status) {
			t.Errorf("unexpected alias %s", status)
		}
	}
	if dashboardWorkOrderScopeSQL(false) != "" {
		t.Fatal("default scope must preserve normal listing")
	}
	if !strings.Contains(dashboardWorkOrderStatusSQL("$4"), "wo.issued_at IS NOT NULL") {
		t.Fatal("aliases must only count issued orders")
	}
}
