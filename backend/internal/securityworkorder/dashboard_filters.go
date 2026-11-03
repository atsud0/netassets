package securityworkorder

import (
	"strings"
	"time"
)

type DashboardFindingFilter struct {
	Scope          bool
	TestBatchOnly  bool
	DispatchState  string
	DiscoveredDate string
	DispatchedDate string
	ClosedDate     string
}

func dashboardFindingFilterSQL(filter DashboardFindingFilter) (string, error) {
	filter.DispatchState = strings.TrimSpace(filter.DispatchState)
	switch filter.DispatchState {
	case "", "undispatched", "dispatched", "remediating", "pending_verification", "closed":
	default:
		return "", invalidField("请选择有效的安全闭环阶段")
	}
	for _, date := range []string{filter.DiscoveredDate, filter.DispatchedDate, filter.ClosedDate} {
		if date == "" {
			continue
		}
		parsed, err := time.Parse("2006-01-02", date)
		if err != nil || parsed.Format("2006-01-02") != date {
			return "", invalidField("安全闭环日期必须为 YYYY-MM-DD")
		}
	}
	predicates := make([]string, 0, 5)
	if filter.TestBatchOnly {
		predicates = append(predicates, "f.test_batch_id IS NOT NULL")
	}
	if filter.Scope {
		predicates = append(predicates, `($2::boolean OR (f.assignee_organization_id IN (SELECT id FROM accessible)
			AND EXISTS (SELECT 1 FROM security_work_order_findings dashboard_link JOIN security_work_orders dashboard_order ON dashboard_order.id=dashboard_link.work_order_id
				WHERE dashboard_link.finding_id=f.id AND dashboard_order.issued_at IS NOT NULL
				AND dashboard_order.assignee_organization_id IN (SELECT id FROM accessible))))`)
	}
	currentOrder := `SELECT dashboard_order.* FROM security_work_orders dashboard_order
		JOIN security_work_order_findings dashboard_link ON dashboard_link.work_order_id=dashboard_order.id
		WHERE dashboard_link.finding_id=f.id AND dashboard_order.issued_at IS NOT NULL AND dashboard_order.status NOT IN ('draft','cancelled')
		AND ($2::boolean OR dashboard_order.assignee_organization_id IN (SELECT id FROM accessible))
		ORDER BY (dashboard_order.status <> 'closed') DESC, dashboard_order.issued_at DESC, dashboard_order.created_at DESC, dashboard_order.id DESC LIMIT 1`
	closedCondition := `dashboard_current.status='closed' AND f.status IN ('fixed','false_positive','ignored','duplicate','risk_accepted')`
	switch filter.DispatchState {
	case "undispatched":
		predicates = append(predicates, `NOT EXISTS (`+currentOrder+`)`)
	case "dispatched":
		predicates = append(predicates, `EXISTS (`+currentOrder+`)`)
	case "remediating":
		predicates = append(predicates, `EXISTS (SELECT 1 FROM (`+currentOrder+`) dashboard_current WHERE dashboard_current.status IN ('issued','in_progress','returned'))`)
	case "pending_verification":
		predicates = append(predicates, `EXISTS (SELECT 1 FROM (`+currentOrder+`) dashboard_current WHERE dashboard_current.status='pending_verification')`)
	case "closed":
		predicates = append(predicates, `EXISTS (SELECT 1 FROM (`+currentOrder+`) dashboard_current WHERE `+closedCondition+`)`)
	}
	if filter.DiscoveredDate != "" {
		predicates = append(predicates, dashboardDateSQL("f.discovered_at", filter.DiscoveredDate))
	}
	if filter.DispatchedDate != "" {
		predicates = append(predicates, `EXISTS (SELECT 1 FROM security_work_order_findings dashboard_link JOIN security_work_orders dashboard_order ON dashboard_order.id=dashboard_link.work_order_id
			WHERE dashboard_link.finding_id=f.id AND dashboard_order.issued_at IS NOT NULL AND dashboard_order.status NOT IN ('draft','cancelled')
			AND ($2::boolean OR dashboard_order.assignee_organization_id IN (SELECT id FROM accessible)) AND `+dashboardDateSQL("dashboard_order.issued_at", filter.DispatchedDate)+`)`)
	}
	if filter.ClosedDate != "" {
		predicates = append(predicates, `EXISTS (SELECT 1 FROM (`+currentOrder+`) dashboard_current WHERE `+closedCondition+` AND `+dashboardDateSQL("dashboard_current.closed_at", filter.ClosedDate)+`)`)
	}
	if len(predicates) == 0 {
		return "", nil
	}
	return " AND " + strings.Join(predicates, " AND "), nil
}

func dashboardDateSQL(column, date string) string {
	return "(" + column + " >= DATE '" + date + "' AND " + column + " < DATE '" + date + "' + interval '1 day')"
}

func dashboardWorkOrderStatus(status string) bool {
	return status == "active" || status == "remediating" || status == "dispatched"
}

func dashboardWorkOrderStatusSQL(placeholder string) string {
	return "(" + placeholder + "::text = '' OR wo.status = " + placeholder +
		" OR (" + placeholder + "::text='active' AND wo.issued_at IS NOT NULL AND wo.status NOT IN ('draft','closed','cancelled'))" +
		" OR (" + placeholder + "::text='remediating' AND wo.issued_at IS NOT NULL AND wo.status IN ('issued','in_progress','returned'))" +
		" OR (" + placeholder + "::text='dispatched' AND wo.issued_at IS NOT NULL AND wo.status NOT IN ('draft','cancelled')))"
}

func dashboardWorkOrderScopeSQL(scope bool) string {
	if !scope {
		return ""
	}
	return " AND wo.issued_at IS NOT NULL AND wo.status NOT IN ('draft','cancelled')"
}
