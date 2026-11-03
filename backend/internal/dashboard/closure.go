package dashboard

import (
	"context"
	"fmt"
	"time"

	"netassets/internal/auth"
)

type SecurityClosureFunnel struct {
	DiscoveredFindingCount            int64 `json:"discovered_finding_count"`
	AssetLinkedFindingCount           int64 `json:"asset_linked_finding_count"`
	DispatchedFindingCount            int64 `json:"dispatched_finding_count"`
	RemediatingFindingCount           int64 `json:"remediating_finding_count"`
	PendingVerificationFindingCount   int64 `json:"pending_verification_finding_count"`
	ClosedFindingCount                int64 `json:"closed_finding_count"`
	DispatchedWorkOrderCount          int64 `json:"dispatched_work_order_count"`
	RemediatingWorkOrderCount         int64 `json:"remediating_work_order_count"`
	PendingVerificationWorkOrderCount int64 `json:"pending_verification_work_order_count"`
	ClosedWorkOrderCount              int64 `json:"closed_work_order_count"`
}

type UndispatchedFindingSummary struct {
	FindingCount        int64      `json:"finding_count"`
	CriticalCount       int64      `json:"critical_count"`
	HighCount           int64      `json:"high_count"`
	OrganizationCount   int64      `json:"organization_count"`
	BusinessSystemCount int64      `json:"business_system_count"`
	OldestDiscoveredAt  *time.Time `json:"oldest_discovered_at,omitempty"`
}

type WorkOrderFindingSummary struct {
	ActiveWorkOrderCount              int64 `json:"active_work_order_count"`
	ActiveFindingCount                int64 `json:"active_finding_count"`
	PendingVerificationWorkOrderCount int64 `json:"pending_verification_work_order_count"`
	PendingVerificationFindingCount   int64 `json:"pending_verification_finding_count"`
	OverdueWorkOrderCount             int64 `json:"overdue_work_order_count"`
	OverdueFindingCount               int64 `json:"overdue_finding_count"`
	ClosedWorkOrderCount              int64 `json:"closed_work_order_count"`
	ClosedFindingCount                int64 `json:"closed_finding_count"`
}

const closureScopeCTE = scopeCTE + `,
	visible_orders AS (
		SELECT wo.* FROM security_work_orders wo
		WHERE wo.issued_at IS NOT NULL AND wo.status NOT IN ('draft','cancelled')
		AND ($2::boolean OR wo.assignee_organization_id IN (SELECT id FROM scoped_organizations))
	), visible_findings AS (
		SELECT f.* FROM security_findings f WHERE $2::boolean OR (
			f.assignee_organization_id IN (SELECT id FROM scoped_organizations)
			AND EXISTS (SELECT 1 FROM security_work_order_findings link JOIN security_work_orders wo ON wo.id=link.work_order_id
				WHERE link.finding_id=f.id AND wo.issued_at IS NOT NULL AND wo.assignee_organization_id IN (SELECT id FROM scoped_organizations)))
	), effective_findings AS (
		SELECT f.*, current_order.id AS work_order_id, current_order.status AS order_status,
			current_order.assignee_organization_id AS order_organization_id, current_order.due_at, current_order.closed_at
		FROM visible_findings f
		LEFT JOIN LATERAL (
			SELECT wo.* FROM visible_orders wo JOIN security_work_order_findings link ON link.work_order_id=wo.id
			WHERE link.finding_id=f.id
			ORDER BY (wo.status <> 'closed') DESC, wo.issued_at DESC, wo.created_at DESC, wo.id DESC LIMIT 1
		) current_order ON true
	)`

func (s *Service) loadClosure(ctx context.Context, principal auth.Principal, result *SecurityOverview) error {
	funnel := &result.ClosureFunnel
	if err := s.pool.QueryRow(ctx, closureScopeCTE+`
		SELECT count(*), count(*) FILTER (WHERE asset_id IS NOT NULL), count(work_order_id),
			count(*) FILTER (WHERE order_status IN ('issued','in_progress','returned')),
			count(*) FILTER (WHERE order_status='pending_verification'),
			count(*) FILTER (WHERE order_status='closed' AND status IN ('fixed','false_positive','ignored','duplicate','risk_accepted')),
			(SELECT count(*) FROM visible_orders),
			(SELECT count(*) FROM visible_orders WHERE status IN ('issued','in_progress','returned')),
			(SELECT count(*) FROM visible_orders WHERE status='pending_verification'),
			(SELECT count(*) FROM visible_orders WHERE status='closed')
		FROM effective_findings`, principal.UserID, principal.CanViewGroupSecurityDashboard()).Scan(
		&funnel.DiscoveredFindingCount, &funnel.AssetLinkedFindingCount, &funnel.DispatchedFindingCount,
		&funnel.RemediatingFindingCount, &funnel.PendingVerificationFindingCount, &funnel.ClosedFindingCount,
		&funnel.DispatchedWorkOrderCount, &funnel.RemediatingWorkOrderCount, &funnel.PendingVerificationWorkOrderCount, &funnel.ClosedWorkOrderCount); err != nil {
		return fmt.Errorf("load security closure funnel: %w", err)
	}
	if result.CanViewUndispatched {
		gap := &result.UndispatchedFindings
		if err := s.pool.QueryRow(ctx, closureScopeCTE+`
			SELECT count(*), count(*) FILTER (WHERE severity='critical'), count(*) FILTER (WHERE severity='high'),
				count(DISTINCT assignee_organization_id), count(DISTINCT business_system_id), min(discovered_at)
			FROM effective_findings WHERE work_order_id IS NULL`, principal.UserID, true).Scan(
			&gap.FindingCount, &gap.CriticalCount, &gap.HighCount, &gap.OrganizationCount, &gap.BusinessSystemCount, &gap.OldestDiscoveredAt); err != nil {
			return fmt.Errorf("load undispatched findings: %w", err)
		}
	}
	orders := &result.WorkOrderFindings
	if err := s.pool.QueryRow(ctx, closureScopeCTE+`
		SELECT
			(SELECT count(*) FROM visible_orders WHERE status <> 'closed'), count(*) FILTER (WHERE order_status <> 'closed'),
			(SELECT count(*) FROM visible_orders WHERE status='pending_verification'), count(*) FILTER (WHERE order_status='pending_verification'),
			(SELECT count(*) FROM visible_orders WHERE status <> 'closed' AND due_at < now()), count(*) FILTER (WHERE order_status <> 'closed' AND due_at < now()),
			(SELECT count(*) FROM visible_orders WHERE status='closed'), count(*) FILTER (WHERE order_status='closed' AND status IN ('fixed','false_positive','ignored','duplicate','risk_accepted'))
		FROM effective_findings`, principal.UserID, principal.CanViewGroupSecurityDashboard()).Scan(
		&orders.ActiveWorkOrderCount, &orders.ActiveFindingCount, &orders.PendingVerificationWorkOrderCount, &orders.PendingVerificationFindingCount,
		&orders.OverdueWorkOrderCount, &orders.OverdueFindingCount, &orders.ClosedWorkOrderCount, &orders.ClosedFindingCount); err != nil {
		return fmt.Errorf("load work order finding coverage: %w", err)
	}
	return nil
}
