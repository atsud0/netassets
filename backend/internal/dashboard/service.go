package dashboard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/findingstatus"
)

// findingStatusValues, findingStatusLabels and findingStatusOrders back the
// status breakdown, so the order and the Chinese labels come from the shared
// finding status vocabulary instead of a hand-maintained VALUES list.
var (
	findingStatusValues = func() []string {
		definitions := findingstatus.Definitions()
		values := make([]string, 0, len(definitions))
		for _, definition := range definitions {
			values = append(values, definition.Value)
		}
		return values
	}()
	findingStatusLabels = func() []string {
		definitions := findingstatus.Definitions()
		labels := make([]string, 0, len(definitions))
		for _, definition := range definitions {
			labels = append(labels, definition.Label)
		}
		return labels
	}()
	findingStatusOrders = func() []int32 {
		definitions := findingstatus.Definitions()
		orders := make([]int32, 0, len(definitions))
		for _, definition := range definitions {
			orders = append(orders, int32(definition.SortOrder))
		}
		return orders
	}()
)

type TaskProgress struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Status         string    `json:"status"`
	DeadlineAt     time.Time `json:"deadline_at"`
	TargetCount    int64     `json:"target_count"`
	SubmittedCount int64     `json:"submitted_count"`
	ApprovedCount  int64     `json:"approved_count"`
	CompletionRate int64     `json:"completion_rate"`
}

type Summary struct {
	OrganizationCount          int64         `json:"organization_count"`
	SystemCount                int64         `json:"system_count"`
	AssetCount                 int64         `json:"asset_count"`
	PublicAssetCount           int64         `json:"public_asset_count"`
	UnlinkedAssetCount         int64         `json:"unlinked_asset_count"`
	SensitivePublicSystemCount int64         `json:"sensitive_public_system_count"`
	ActiveTaskCount            int64         `json:"active_task_count"`
	PendingReviewCount         int64         `json:"pending_review_count"`
	PendingTaskCount           int64         `json:"pending_task_count"`
	ReturnedTaskCount          int64         `json:"returned_task_count"`
	OverdueCount               int64         `json:"overdue_count"`
	ActiveWorkOrderCount       int64         `json:"active_work_order_count"`
	PendingVerificationCount   int64         `json:"pending_verification_work_order_count"`
	OverdueWorkOrderCount      int64         `json:"overdue_work_order_count"`
	DueSoonWorkOrderCount      int64         `json:"due_soon_work_order_count"`
	LatestTask                 *TaskProgress `json:"latest_task,omitempty"`
}

type SecurityBreakdown struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int64  `json:"count"`
}

type SecurityTrendPoint struct {
	Date               string `json:"date"`
	NewFindingCount    int64  `json:"new_finding_count"`
	ClosedFindingCount int64  `json:"closed_finding_count"`
}

type OrganizationSecurityPerformance struct {
	OrganizationID        string `json:"organization_id"`
	OrganizationCode      string `json:"organization_code"`
	OrganizationName      string `json:"organization_name"`
	ActiveWorkOrderCount  int64  `json:"active_work_order_count"`
	OverdueWorkOrderCount int64  `json:"overdue_work_order_count"`
	ClosedWorkOrderCount  int64  `json:"closed_work_order_count"`
	ClosedOnTimeCount     int64  `json:"closed_on_time_count"`
	TimelyRate            int64  `json:"timely_rate"`
}

type TestCoverage struct {
	PublicAssetCount        int64 `json:"public_asset_count"`
	TestedPublicAssetCount  int64 `json:"tested_public_asset_count"`
	PublicAssetCoverageRate int64 `json:"public_asset_coverage_rate"`
	ActiveBatchCount        int64 `json:"active_batch_count"`
	CompletedBatchCount     int64 `json:"completed_batch_count"`
	TestFindingCount        int64 `json:"test_finding_count"`
	ClosedTestFindingCount  int64 `json:"closed_test_finding_count"`
	TestFindingClosureRate  int64 `json:"test_finding_closure_rate"`
}

type SecurityOverview struct {
	// PendingAssetFindingCount is 待关联资产: findings that belong to an asset
	// working version which has not been published yet, so they are tracked
	// before the asset register catches up.
	PendingAssetFindingCount int64 `json:"pending_asset_finding_count"`
	// RetiredAssetFindingCount is findings whose asset was soft deleted. The
	// finding survives on purpose because the vulnerability was real, but the
	// asset is gone, so the row needs its own state instead of an empty asset cell.
	RetiredAssetFindingCount int64                             `json:"retired_asset_finding_count"`
	FindingSeverity          []SecurityBreakdown               `json:"finding_severity"`
	FindingStatus            []SecurityBreakdown               `json:"finding_status"`
	FindingTrend             []SecurityTrendPoint              `json:"finding_trend"`
	OrganizationPerformance  []OrganizationSecurityPerformance `json:"organization_performance"`
	TestCoverage             TestCoverage                      `json:"test_coverage"`
}

type Service struct{ pool *pgxpool.Pool }

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

const scopeCTE = `
	WITH RECURSIVE descendant_roots(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope = 'organization_and_descendants'
	), descendants(id) AS (
		SELECT id FROM descendant_roots
		UNION SELECT child.id FROM organizations child JOIN descendants parent ON child.parent_id = parent.id
	WHERE child.status='enabled' AND child.deleted_at IS NULL
	), accessible(id) AS (
		SELECT ur.organization_id FROM user_roles ur
		JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
		WHERE ur.user_id = $1::uuid AND ur.organization_id IS NOT NULL
		  AND ur.data_scope IN ('organization', 'organization_and_descendants')
		UNION SELECT id FROM descendants
	), scoped_organizations(id) AS (
		SELECT o.id FROM organizations o
		WHERE o.status = 'enabled' AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
	)`

func (s *Service) Get(ctx context.Context, principal auth.Principal) (Summary, error) {
	var result Summary
	if err := s.pool.QueryRow(ctx, scopeCTE+`
		SELECT
			(SELECT count(*) FROM organizations o WHERE o.id IN (SELECT id FROM scoped_organizations) AND o.organization_type <> 'group'),
			(SELECT count(*) FROM business_systems bs WHERE bs.owner_organization_id IN (SELECT id FROM scoped_organizations) AND bs.deleted_at IS NULL),
			(SELECT count(DISTINCT a.id) FROM assets a JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary WHERE ao.organization_id IN (SELECT id FROM scoped_organizations) AND a.deleted_at IS NULL),
			(SELECT count(DISTINCT a.id) FROM assets a JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary WHERE ao.organization_id IN (SELECT id FROM scoped_organizations) AND a.deleted_at IS NULL AND a.network_scope = 'public'),
			(SELECT count(DISTINCT a.id) FROM assets a JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary WHERE ao.organization_id IN (SELECT id FROM scoped_organizations) AND a.deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM system_assets sa WHERE sa.asset_id = a.id)),
			(SELECT count(DISTINCT bs.id)
			 FROM business_systems bs
			 JOIN business_system_data_security_profiles p ON p.business_system_id = bs.id
			 JOIN system_assets sa ON sa.business_system_id = bs.id
			 JOIN assets a ON a.id = sa.asset_id AND a.deleted_at IS NULL AND a.network_scope = 'public'
			 WHERE bs.owner_organization_id IN (SELECT id FROM scoped_organizations) AND bs.deleted_at IS NULL
			   AND p.contains_sensitive_personal_information = 'yes')
	`, principal.UserID, principal.HasRole("group_admin")).Scan(
		&result.OrganizationCount, &result.SystemCount, &result.AssetCount, &result.PublicAssetCount,
		&result.UnlinkedAssetCount, &result.SensitivePublicSystemCount,
	); err != nil {
		return Summary{}, fmt.Errorf("load dashboard asset summary: %w", err)
	}

	if err := s.pool.QueryRow(ctx, scopeCTE+`
		SELECT
			count(*) FILTER (WHERE wo.status NOT IN ('closed', 'cancelled')),
			count(*) FILTER (WHERE wo.status = 'pending_verification'),
			count(*) FILTER (WHERE wo.status NOT IN ('closed', 'cancelled', 'draft') AND wo.due_at < now()),
			count(*) FILTER (WHERE wo.status NOT IN ('closed', 'cancelled', 'draft') AND wo.due_at >= now() AND wo.due_at < now() + interval '3 days')
		FROM security_work_orders wo
		WHERE ($2::boolean OR (wo.assignee_organization_id IN (SELECT id FROM scoped_organizations) AND wo.status <> 'draft'))
	`, principal.UserID, principal.CanManageSecurityWorkOrders()).Scan(
		&result.ActiveWorkOrderCount, &result.PendingVerificationCount,
		&result.OverdueWorkOrderCount, &result.DueSoonWorkOrderCount,
	); err != nil {
		return Summary{}, fmt.Errorf("load dashboard work order summary: %w", err)
	}

	if err := s.pool.QueryRow(ctx, scopeCTE+`
		SELECT
			(SELECT count(*) FROM inventory_tasks t WHERE t.status = 'active'
			  AND ($2::boolean OR EXISTS (SELECT 1 FROM inventory_task_organizations ito WHERE ito.task_id = t.id AND ito.organization_id IN (SELECT id FROM scoped_organizations)))),
			(SELECT count(*) FROM inventory_task_organizations ito JOIN inventory_tasks t ON t.id = ito.task_id
			  WHERE ito.status = 'submitted' AND t.status = 'active' AND ito.organization_id IN (SELECT id FROM scoped_organizations)),
			(SELECT count(*) FROM inventory_task_organizations ito JOIN inventory_tasks t ON t.id = ito.task_id
			  WHERE t.status = 'active' AND ito.status IN ('not_started', 'in_progress', 'returned', 'overdue')
			    AND ito.organization_id IN (SELECT id FROM scoped_organizations)),
			(SELECT count(*) FROM inventory_task_organizations ito JOIN inventory_tasks t ON t.id = ito.task_id
			  WHERE t.status = 'active' AND ito.status = 'returned' AND ito.organization_id IN (SELECT id FROM scoped_organizations)),
			(SELECT count(*) FROM inventory_task_organizations ito JOIN inventory_tasks t ON t.id = ito.task_id
			  WHERE t.status = 'active' AND t.deadline_at < now() AND ito.status NOT IN ('submitted', 'approved')
			    AND ito.organization_id IN (SELECT id FROM scoped_organizations))
	`, principal.UserID, principal.HasRole("group_admin")).Scan(
		&result.ActiveTaskCount, &result.PendingReviewCount, &result.PendingTaskCount,
		&result.ReturnedTaskCount, &result.OverdueCount,
	); err != nil {
		return Summary{}, fmt.Errorf("load dashboard task summary: %w", err)
	}

	var task TaskProgress
	err := s.pool.QueryRow(ctx, scopeCTE+`
		SELECT t.id::text, t.task_name, t.status, t.deadline_at,
		       count(ito.organization_id),
		       count(ito.organization_id) FILTER (WHERE ito.status IN ('submitted', 'approved')),
		       count(ito.organization_id) FILTER (WHERE ito.status = 'approved')
		FROM inventory_tasks t
		JOIN inventory_task_organizations ito ON ito.task_id = t.id
		WHERE t.status IN ('active', 'completed')
		  AND ($2::boolean OR ito.organization_id IN (SELECT id FROM scoped_organizations))
		GROUP BY t.id
		ORDER BY CASE WHEN t.status = 'active' THEN 0 ELSE 1 END, t.created_at DESC
		LIMIT 1
	`, principal.UserID, principal.HasRole("group_admin")).Scan(
		&task.ID, &task.Name, &task.Status, &task.DeadlineAt, &task.TargetCount, &task.SubmittedCount, &task.ApprovedCount,
	)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Summary{}, fmt.Errorf("load latest dashboard task: %w", err)
	}
	if err == nil {
		if task.TargetCount > 0 {
			task.CompletionRate = task.ApprovedCount * 100 / task.TargetCount
		}
		result.LatestTask = &task
	}
	return result, nil
}

// GetSecurityOverview returns operational security indicators for the principal's
// accessible organization scope. Subsidiary users only see findings that have
// already been disclosed through an issued work order.
func (s *Service) GetSecurityOverview(ctx context.Context, principal auth.Principal) (SecurityOverview, error) {
	result := SecurityOverview{
		FindingSeverity:         make([]SecurityBreakdown, 0, 5),
		FindingStatus:           make([]SecurityBreakdown, 0, 5),
		FindingTrend:            make([]SecurityTrendPoint, 0, 30),
		OrganizationPerformance: make([]OrganizationSecurityPerformance, 0),
	}
	findingVisible := func(alias string) string {
		return `
		($2::boolean OR (
			` + alias + `.assignee_organization_id IN (SELECT id FROM scoped_organizations)
			AND EXISTS (
				SELECT 1 FROM security_work_order_findings link
				JOIN security_work_orders wo ON wo.id = link.work_order_id
				WHERE link.finding_id = ` + alias + `.id AND wo.issued_at IS NOT NULL
			)
		))`
	}

	severityRows, err := s.pool.Query(ctx, scopeCTE+`
		SELECT definitions.key, definitions.label, count(f.id)
		FROM (VALUES
			('critical', '严重', 1), ('high', '高危', 2), ('medium', '中危', 3),
			('low', '低危', 4), ('info', '提示', 5)
		) AS definitions(key, label, sort_order)
		LEFT JOIN security_findings f ON f.severity = definitions.key AND `+findingVisible("f")+`
		GROUP BY definitions.key, definitions.label, definitions.sort_order
		ORDER BY definitions.sort_order`,
		principal.UserID, principal.CanViewGroupSecurityDashboard())
	if err != nil {
		return SecurityOverview{}, fmt.Errorf("load security severity overview: %w", err)
	}
	defer severityRows.Close()
	for severityRows.Next() {
		var item SecurityBreakdown
		if err := severityRows.Scan(&item.Key, &item.Label, &item.Count); err != nil {
			return SecurityOverview{}, fmt.Errorf("scan security severity overview: %w", err)
		}
		result.FindingSeverity = append(result.FindingSeverity, item)
	}
	if err := severityRows.Err(); err != nil {
		return SecurityOverview{}, fmt.Errorf("iterate security severity overview: %w", err)
	}

	statusRows, err := s.pool.Query(ctx, scopeCTE+`
		SELECT definitions.key, definitions.label, count(f.id)
		FROM unnest($3::text[], $4::text[], $5::int[]) AS definitions(key, label, sort_order)
		LEFT JOIN security_findings f ON f.status = definitions.key AND `+findingVisible("f")+`
		GROUP BY definitions.key, definitions.label, definitions.sort_order
		ORDER BY definitions.sort_order`,
		principal.UserID, principal.CanViewGroupSecurityDashboard(), findingStatusValues, findingStatusLabels, findingStatusOrders)
	if err != nil {
		return SecurityOverview{}, fmt.Errorf("load security finding status overview: %w", err)
	}
	defer statusRows.Close()
	for statusRows.Next() {
		var item SecurityBreakdown
		if err := statusRows.Scan(&item.Key, &item.Label, &item.Count); err != nil {
			return SecurityOverview{}, fmt.Errorf("scan security finding status overview: %w", err)
		}
		result.FindingStatus = append(result.FindingStatus, item)
	}
	if err := statusRows.Err(); err != nil {
		return SecurityOverview{}, fmt.Errorf("iterate security finding status overview: %w", err)
	}

	// 待关联资产 follows the same visibility rule as every other indicator: group
	// users see everything in scope, subsidiary users only see findings already
	// disclosed through an issued work order.
	if err := s.pool.QueryRow(ctx, scopeCTE+`
		SELECT count(*) FROM security_findings f
		WHERE `+pendingAssetFinding("f")+` AND `+findingVisible("f")+`
	`, principal.UserID, principal.CanViewGroupSecurityDashboard()).Scan(&result.PendingAssetFindingCount); err != nil {
		return SecurityOverview{}, fmt.Errorf("load security pending asset findings: %w", err)
	}

	if err := s.pool.QueryRow(ctx, scopeCTE+`
		SELECT count(*) FROM security_findings f
		JOIN assets retired_asset ON retired_asset.id = f.asset_id
		WHERE `+retiredAssetFinding("f", "retired_asset")+` AND `+findingVisible("f")+`
	`, principal.UserID, principal.CanViewGroupSecurityDashboard()).Scan(&result.RetiredAssetFindingCount); err != nil {
		return SecurityOverview{}, fmt.Errorf("load security retired asset findings: %w", err)
	}

	trendRows, err := s.pool.Query(ctx, scopeCTE+`
		SELECT to_char(days.day, 'YYYY-MM-DD'),
			count(DISTINCT new_finding.id),
			count(DISTINCT closed_link.finding_id)
		FROM generate_series(current_date - interval '29 days', current_date, interval '1 day') AS days(day)
		LEFT JOIN security_findings new_finding
			ON new_finding.discovered_at >= days.day
			AND new_finding.discovered_at < days.day + interval '1 day'
			AND `+findingVisible("new_finding")+`
		LEFT JOIN security_work_order_events close_event
			ON close_event.action = 'close'
			AND close_event.created_at >= days.day
			AND close_event.created_at < days.day + interval '1 day'
		LEFT JOIN security_work_orders closed_order
			ON closed_order.id = close_event.work_order_id
			AND ($2::boolean OR (closed_order.assignee_organization_id IN (SELECT id FROM scoped_organizations) AND closed_order.issued_at IS NOT NULL))
		LEFT JOIN security_work_order_findings closed_link ON closed_link.work_order_id = closed_order.id
		GROUP BY days.day
		ORDER BY days.day`, principal.UserID, principal.CanViewGroupSecurityDashboard())
	if err != nil {
		return SecurityOverview{}, fmt.Errorf("load security finding trend: %w", err)
	}
	defer trendRows.Close()
	for trendRows.Next() {
		var item SecurityTrendPoint
		if err := trendRows.Scan(&item.Date, &item.NewFindingCount, &item.ClosedFindingCount); err != nil {
			return SecurityOverview{}, fmt.Errorf("scan security finding trend: %w", err)
		}
		result.FindingTrend = append(result.FindingTrend, item)
	}
	if err := trendRows.Err(); err != nil {
		return SecurityOverview{}, fmt.Errorf("iterate security finding trend: %w", err)
	}

	performanceRows, err := s.pool.Query(ctx, scopeCTE+`
		SELECT o.id::text, o.organization_code, o.organization_name,
			count(wo.id) FILTER (WHERE wo.status NOT IN ('draft', 'closed', 'cancelled')),
			count(wo.id) FILTER (WHERE wo.status NOT IN ('draft', 'closed', 'cancelled') AND wo.due_at < now()),
			count(wo.id) FILTER (WHERE wo.status = 'closed'),
			count(wo.id) FILTER (WHERE wo.status = 'closed' AND wo.closed_at <= wo.due_at)
		FROM organizations o
		LEFT JOIN security_work_orders wo ON wo.assignee_organization_id = o.id
			AND ($2::boolean OR wo.issued_at IS NOT NULL)
		WHERE o.id IN (SELECT id FROM scoped_organizations) AND o.organization_type <> 'group'
		GROUP BY o.id, o.organization_code, o.organization_name
		ORDER BY count(wo.id) FILTER (WHERE wo.status NOT IN ('draft', 'closed', 'cancelled') AND wo.due_at < now()) DESC,
			count(wo.id) FILTER (WHERE wo.status NOT IN ('draft', 'closed', 'cancelled')) DESC, o.organization_name
		LIMIT 20`, principal.UserID, principal.CanViewGroupSecurityDashboard())
	if err != nil {
		return SecurityOverview{}, fmt.Errorf("load organization security performance: %w", err)
	}
	defer performanceRows.Close()
	for performanceRows.Next() {
		var item OrganizationSecurityPerformance
		if err := performanceRows.Scan(&item.OrganizationID, &item.OrganizationCode, &item.OrganizationName,
			&item.ActiveWorkOrderCount, &item.OverdueWorkOrderCount, &item.ClosedWorkOrderCount, &item.ClosedOnTimeCount); err != nil {
			return SecurityOverview{}, fmt.Errorf("scan organization security performance: %w", err)
		}
		item.TimelyRate = percentage(item.ClosedOnTimeCount, item.ClosedWorkOrderCount)
		result.OrganizationPerformance = append(result.OrganizationPerformance, item)
	}
	if err := performanceRows.Err(); err != nil {
		return SecurityOverview{}, fmt.Errorf("iterate organization security performance: %w", err)
	}

	coverageQuery := scopeCTE + `
		SELECT
			(SELECT count(DISTINCT a.id)
			 FROM assets a JOIN asset_organizations ao ON ao.asset_id = a.id AND ao.is_primary
			 WHERE ao.organization_id IN (SELECT id FROM scoped_organizations)
			   AND a.deleted_at IS NULL AND a.network_scope = 'public'),
			(SELECT count(DISTINCT scope.asset_id)
			 FROM security_test_scope_assets scope
			 JOIN security_test_batches batch ON batch.id = scope.batch_id
			 WHERE batch.status = 'completed' AND scope.network_scope_snapshot = 'public'
			   AND ($2::boolean OR scope.organization_id IN (SELECT id FROM scoped_organizations))),
			(SELECT count(DISTINCT batch.id)
			 FROM security_test_batches batch
			 WHERE batch.status IN ('authorized', 'in_progress')
			   AND ($2::boolean OR EXISTS (SELECT 1 FROM security_test_scope_assets scope WHERE scope.batch_id = batch.id AND scope.organization_id IN (SELECT id FROM scoped_organizations)))),
			(SELECT count(DISTINCT batch.id)
			 FROM security_test_batches batch
			 WHERE batch.status = 'completed'
			   AND ($2::boolean OR EXISTS (SELECT 1 FROM security_test_scope_assets scope WHERE scope.batch_id = batch.id AND scope.organization_id IN (SELECT id FROM scoped_organizations)))),
			(SELECT count(*) FROM security_findings f WHERE f.test_batch_id IS NOT NULL AND ` + findingVisible("f") + `),
			(SELECT count(*) FROM security_findings f WHERE f.test_batch_id IS NOT NULL
			   AND f.status IN (` + findingstatus.ClosedSQL() + `) AND ` + findingVisible("f") + `)
	`
	if err := s.pool.QueryRow(ctx, coverageQuery, principal.UserID, principal.CanViewGroupSecurityDashboard()).Scan(
		&result.TestCoverage.PublicAssetCount, &result.TestCoverage.TestedPublicAssetCount,
		&result.TestCoverage.ActiveBatchCount, &result.TestCoverage.CompletedBatchCount,
		&result.TestCoverage.TestFindingCount, &result.TestCoverage.ClosedTestFindingCount,
	); err != nil {
		return SecurityOverview{}, fmt.Errorf("load security test coverage: %w", err)
	}

	result.TestCoverage.PublicAssetCoverageRate = percentage(result.TestCoverage.TestedPublicAssetCount, result.TestCoverage.PublicAssetCount)
	result.TestCoverage.TestFindingClosureRate = percentage(result.TestCoverage.ClosedTestFindingCount, result.TestCoverage.TestFindingCount)
	return result, nil
}

// pendingAssetFinding matches findings that are tracked against an unpublished
// asset working version: no formal asset yet, but the owning organization already
// confirmed the draft that will register it.
func pendingAssetFinding(alias string) string {
	return alias + `.asset_id IS NULL AND ` + alias + `.pending_asset_draft_id IS NOT NULL`
}

// retiredAssetFinding matches findings whose asset was soft deleted. Such a
// finding keeps its asset_id, so it is neither unlinked nor pending: the asset
// existed, was classified, and was then retired on purpose. Counting it as
// "待关联资产" would push the organization to re-register an asset they already
// retired, so it gets its own indicator.
func retiredAssetFinding(findingAlias, assetAlias string) string {
	return findingAlias + `.asset_id IS NOT NULL AND ` + assetAlias + `.deleted_at IS NOT NULL`
}

func percentage(numerator, denominator int64) int64 {
	if denominator <= 0 {
		return 0
	}
	return numerator * 100 / denominator
}
