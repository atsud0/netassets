package notification

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"netassets/internal/auth"
	"netassets/internal/pagination"
)

var (
	ErrInvalid  = errors.New("invalid notification request")
	ErrNotFound = errors.New("notification not found")
)

type Item struct {
	Key          string     `json:"key"`
	Category     string     `json:"category"`
	Title        string     `json:"title"`
	Message      string     `json:"message"`
	Level        string     `json:"level"`
	ResourceType string     `json:"resource_type"`
	ResourceID   string     `json:"resource_id"`
	ActionPath   string     `json:"action_path"`
	OccurredAt   time.Time  `json:"occurred_at"`
	DueAt        *time.Time `json:"due_at,omitempty"`
	Read         bool       `json:"read"`
}

type ListInput struct {
	UnreadOnly bool
	Category   string
	Limit      int
}

type SearchInput struct {
	UnreadOnly bool
	Category   string
	Page       pagination.Query
}

type Summary struct {
	Total       int64 `json:"total"`
	UnreadCount int64 `json:"unread_count"`
}

type Service struct {
	pool *pgxpool.Pool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

const notificationCandidatesCTE = `
WITH RECURSIVE descendant_roots(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	WHERE ur.user_id = $1::uuid
	  AND ur.organization_id IS NOT NULL
	  AND ur.data_scope = 'organization_and_descendants'
), descendants(id) AS (
	SELECT id FROM descendant_roots
	UNION
	SELECT child.id
	FROM organizations child
	JOIN descendants parent ON child.parent_id = parent.id
), accessible(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	WHERE ur.user_id = $1::uuid
	  AND ur.organization_id IS NOT NULL
	  AND ur.data_scope IN ('organization', 'organization_and_descendants')
	UNION
	SELECT id FROM descendants
), scoped_organizations(id) AS (
	SELECT o.id
	FROM organizations o
	WHERE o.status = 'enabled'
	  AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
), quality_descendant_roots(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='data_quality.manage'
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), quality_descendants(id) AS (
	SELECT id FROM quality_descendant_roots
	UNION
	SELECT child.id FROM organizations child JOIN quality_descendants parent ON child.parent_id=parent.id
), quality_accessible(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='data_quality.manage'
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
	UNION SELECT id FROM quality_descendants
), quality_scoped_organizations(id) AS (
	SELECT o.id FROM organizations o WHERE o.status='enabled' AND ($2::boolean OR o.id IN (SELECT id FROM quality_accessible))
), notification_candidates AS (
	SELECT
		'work_order:pending_verification:' || wo.id::text || ':' || extract(epoch FROM COALESCE(wo.submitted_at, wo.updated_at))::bigint::text AS notification_key,
		'work_order_review'::text AS category,
		'安全工单待复测'::text AS title,
		wo.work_order_no || '《' || wo.title || '》已提交整改，等待集团复测。'::text AS message,
		'warning'::text AS level,
		'security_work_order'::text AS resource_type,
		wo.id::text AS resource_id,
		'/security-work-orders?work_order_id=' || wo.id::text AS action_path,
		COALESCE(wo.submitted_at, wo.updated_at) AS occurred_at,
		wo.due_at AS due_at
	FROM security_work_orders wo
	WHERE $2::boolean
	  AND wo.status = 'pending_verification'

	UNION ALL

	SELECT
		'work_order:issued:' || wo.id::text || ':' || extract(epoch FROM COALESCE(wo.issued_at, wo.updated_at))::bigint::text,
		'work_order_received',
		'收到新的安全工单',
		wo.work_order_no || '《' || wo.title || '》已下发，请及时接收并处理。',
		'info',
		'security_work_order',
		wo.id::text,
		'/security-work-orders?work_order_id=' || wo.id::text,
		COALESCE(wo.issued_at, wo.updated_at),
		wo.due_at
	FROM security_work_orders wo
	WHERE NOT $2::boolean
	  AND wo.status = 'issued'
	  AND wo.issued_at IS NOT NULL
	  AND wo.assignee_organization_id IN (SELECT id FROM scoped_organizations)

	UNION ALL

	SELECT
		'work_order:returned:' || wo.id::text || ':' || extract(epoch FROM wo.updated_at)::bigint::text,
		'work_order_returned',
		'安全工单已退回整改',
		wo.work_order_no || '《' || wo.title || '》复测未通过，请根据退回意见继续整改。',
		'warning',
		'security_work_order',
		wo.id::text,
		'/security-work-orders?work_order_id=' || wo.id::text,
		wo.updated_at,
		wo.due_at
	FROM security_work_orders wo
	WHERE NOT $2::boolean
	  AND wo.status = 'returned'
	  AND wo.assignee_organization_id IN (SELECT id FROM scoped_organizations)

	UNION ALL

	SELECT
		'work_order:overdue:' || wo.id::text || ':' || extract(epoch FROM wo.due_at)::bigint::text,
		'work_order_overdue',
		'安全工单已逾期',
		wo.work_order_no || '《' || wo.title || '》已超过整改期限，请立即处理。',
		'danger',
		'security_work_order',
		wo.id::text,
		'/security-work-orders?work_order_id=' || wo.id::text,
		wo.due_at,
		wo.due_at
	FROM security_work_orders wo
	WHERE wo.status NOT IN ('draft', 'closed', 'cancelled')
	  AND wo.due_at < now()
	  AND ($2::boolean OR wo.assignee_organization_id IN (SELECT id FROM scoped_organizations))
	  AND ($2::boolean OR wo.issued_at IS NOT NULL)

	UNION ALL

	SELECT
		'work_order:due_soon:' || wo.id::text || ':' || extract(epoch FROM wo.due_at)::bigint::text,
		'work_order_due_soon',
		'安全工单即将到期',
		wo.work_order_no || '《' || wo.title || '》将在 3 天内到期，请安排整改或复测。',
		'warning',
		'security_work_order',
		wo.id::text,
		'/security-work-orders?work_order_id=' || wo.id::text,
		now(),
		wo.due_at
	FROM security_work_orders wo
	WHERE wo.status NOT IN ('draft', 'closed', 'cancelled')
	  AND wo.due_at >= now()
	  AND wo.due_at < now() + interval '3 days'
	  AND ($2::boolean OR wo.assignee_organization_id IN (SELECT id FROM scoped_organizations))
	  AND ($2::boolean OR wo.issued_at IS NOT NULL)

	UNION ALL

	SELECT
		'finding:risk_expired:' || sf.id::text || ':' || extract(epoch FROM sf.risk_accepted_until)::bigint::text,
		'risk_review',
		'风险接受已到期',
		sf.finding_code || '《' || sf.title || '》的风险接受期限已到期，请完成复核。',
		'danger',
		'security_finding',
		sf.id::text,
		'/security-work-orders?finding_id=' || sf.id::text,
		sf.risk_accepted_until,
		sf.risk_accepted_until
	FROM security_findings sf
	WHERE $2::boolean
	  AND sf.status = 'accepted_risk'
	  AND sf.risk_accepted_until IS NOT NULL
	  AND sf.risk_accepted_until < now()

	UNION ALL

	SELECT
		'finding:risk_due_soon:' || sf.id::text || ':' || extract(epoch FROM sf.risk_accepted_until)::bigint::text,
		'risk_review',
		'风险接受即将到期',
		sf.finding_code || '《' || sf.title || '》的风险接受将在 3 天内到期，请提前安排复核。',
		'warning',
		'security_finding',
		sf.id::text,
		'/security-work-orders?finding_id=' || sf.id::text,
		now(),
		sf.risk_accepted_until
	FROM security_findings sf
	WHERE $2::boolean
	  AND sf.status = 'accepted_risk'
	  AND sf.risk_accepted_until IS NOT NULL
	  AND sf.risk_accepted_until >= now()
	  AND sf.risk_accepted_until < now() + interval '3 days'

	UNION ALL

	SELECT
		'quality:pending_review:' || i.id::text || ':' || extract(epoch FROM COALESCE(i.submitted_at,i.updated_at))::bigint::text,
		'quality_review',
		'数据质量问题待复核',
		i.organization_name || '的“' || i.resource_name || '”已提交整改，等待集团复核。',
		CASE i.severity WHEN 'error' THEN 'danger' ELSE 'warning' END,
		'data_quality_issue',
		i.id::text,
		'/data-quality-issues?issue_id=' || i.id::text,
		COALESCE(i.submitted_at,i.updated_at),
		NULL::timestamptz
	FROM data_quality_issues i
	WHERE $2::boolean AND i.status='pending_review'

	UNION ALL

	SELECT
		'quality:detected:' || i.id::text || ':' || extract(epoch FROM i.first_detected_at)::bigint::text,
		'quality_received',
		'发现新的数据质量问题',
		'“' || i.resource_name || '”未满足规则“' || i.rule_name || '”，请安排整改。',
		CASE i.severity WHEN 'error' THEN 'danger' ELSE 'warning' END,
		'data_quality_issue',
		i.id::text,
		'/data-quality-issues?issue_id=' || i.id::text,
		i.first_detected_at,
		NULL::timestamptz
	FROM data_quality_issues i
	WHERE NOT $2::boolean AND i.status='pending' AND i.organization_id IN (SELECT id FROM quality_scoped_organizations)

	UNION ALL

	SELECT
		'quality:returned:' || i.id::text || ':' || extract(epoch FROM e.created_at)::bigint::text,
		'quality_returned',
		'数据质量整改已退回',
		'“' || i.resource_name || '”的整改复核未通过，请根据意见继续处理。',
		'warning',
		'data_quality_issue',
		i.id::text,
		'/data-quality-issues?issue_id=' || i.id::text,
		e.created_at,
		NULL::timestamptz
	FROM data_quality_issues i
	JOIN LATERAL (
		SELECT event_type,created_at FROM data_quality_issue_events WHERE issue_id=i.id ORDER BY created_at DESC,id DESC LIMIT 1
	) e ON e.event_type='returned'
	WHERE NOT $2::boolean AND i.status='in_progress' AND i.organization_id IN (SELECT id FROM quality_scoped_organizations)

	UNION ALL

	SELECT
		'quality:exemption_due:' || i.id::text || ':' || extract(epoch FROM i.exemption_expires_at)::bigint::text,
		'quality_exemption',
		'数据质量豁免即将到期',
		i.organization_name || '的“' || i.resource_name || '”豁免将在 3 天内到期，请安排复核。',
		'warning',
		'data_quality_issue',
		i.id::text,
		'/data-quality-issues?issue_id=' || i.id::text,
		now(),
		i.exemption_expires_at
	FROM data_quality_issues i
	WHERE $2::boolean AND i.status='exempted' AND i.exemption_expires_at>=now() AND i.exemption_expires_at<now()+interval '3 days'
)
`

func (s *Service) List(ctx context.Context, principal auth.Principal, input ListInput) ([]Item, Summary, error) {
	// Compatibility helper for internal callers. HTTP endpoints must use Search.
	page, _ := pagination.Normalize(1, input.Limit)
	result, summary, err := s.Search(ctx, principal, SearchInput{UnreadOnly: input.UnreadOnly, Category: input.Category, Page: page})
	return result.Items, summary, err
}

// Search executes the notifications CTE once for a bounded page and returns a
// filtered total from count(*) OVER (), rather than a length of page items.
func (s *Service) Search(ctx context.Context, principal auth.Principal, input SearchInput) (pagination.Result[Item], Summary, error) {
	input.Category = strings.TrimSpace(input.Category)
	isGroupAdmin := principal.HasRole("group_admin")
	query := notificationCandidatesCTE + `
SELECT nc.notification_key, nc.category, nc.title, nc.message, nc.level,
       nc.resource_type, nc.resource_id, nc.action_path, nc.occurred_at, nc.due_at,
       nr.read_at IS NOT NULL AS is_read, count(*) OVER ()
FROM notification_candidates nc
LEFT JOIN notification_reads nr
  ON nr.user_id = $1::uuid AND nr.notification_key = nc.notification_key
WHERE ($3::boolean = false OR nr.read_at IS NULL)
  AND ($4 = '' OR nc.category = $4)
ORDER BY (nr.read_at IS NULL) DESC,
         CASE nc.level WHEN 'danger' THEN 3 WHEN 'warning' THEN 2 ELSE 1 END DESC,
         nc.occurred_at DESC
LIMIT $5 OFFSET $6`
	rows, err := s.pool.Query(ctx, query, principal.UserID, isGroupAdmin, input.UnreadOnly, input.Category, input.Page.PageSize, input.Page.Offset())
	if err != nil {
		return pagination.Result[Item]{}, Summary{}, fmt.Errorf("search notifications: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0, input.Page.PageSize)
	total := 0
	for rows.Next() {
		var item Item
		if err := rows.Scan(&item.Key, &item.Category, &item.Title, &item.Message, &item.Level,
			&item.ResourceType, &item.ResourceID, &item.ActionPath, &item.OccurredAt, &item.DueAt, &item.Read, &total); err != nil {
			return pagination.Result[Item]{}, Summary{}, fmt.Errorf("scan notification: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return pagination.Result[Item]{}, Summary{}, fmt.Errorf("iterate notifications: %w", err)
	}
	if pagination.NeedsTotalFallback(len(items), input.Page) {
		countQuery := notificationCandidatesCTE + `
SELECT count(*)
FROM notification_candidates nc
LEFT JOIN notification_reads nr
  ON nr.user_id = $1::uuid AND nr.notification_key = nc.notification_key
WHERE ($3::boolean = false OR nr.read_at IS NULL)
  AND ($4 = '' OR nc.category = $4)`
		if err := s.pool.QueryRow(ctx, countQuery, principal.UserID, isGroupAdmin, input.UnreadOnly, input.Category).Scan(&total); err != nil {
			return pagination.Result[Item]{}, Summary{}, fmt.Errorf("count notifications: %w", err)
		}
	}
	summary, err := s.summaryWithCandidates(ctx, principal, isGroupAdmin)
	if err != nil {
		return pagination.Result[Item]{}, Summary{}, err
	}
	return pagination.NewResult(items, total, input.Page), summary, nil
}

func (s *Service) Summary(ctx context.Context, principal auth.Principal) (Summary, error) {
	return s.summaryWithCandidates(ctx, principal, principal.HasRole("group_admin"))
}

func (s *Service) summaryWithCandidates(ctx context.Context, principal auth.Principal, isGroupAdmin bool) (Summary, error) {
	var result Summary
	err := s.pool.QueryRow(ctx, notificationCandidatesCTE+`
SELECT count(*)::bigint, count(*) FILTER (WHERE nr.read_at IS NULL)::bigint
FROM notification_candidates nc
LEFT JOIN notification_reads nr
  ON nr.user_id = $1::uuid AND nr.notification_key = nc.notification_key`, principal.UserID, isGroupAdmin).Scan(&result.Total, &result.UnreadCount)
	if err != nil {
		return Summary{}, fmt.Errorf("load notification summary: %w", err)
	}
	return result, nil
}

func (s *Service) MarkRead(ctx context.Context, principal auth.Principal, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 255 {
		return ErrInvalid
	}
	visible, err := s.isVisible(ctx, principal, key)
	if err != nil {
		return err
	}
	if !visible {
		return ErrNotFound
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO notification_reads (user_id, notification_key, read_at)
		VALUES ($1::uuid, $2, now())
		ON CONFLICT (user_id, notification_key) DO UPDATE SET read_at = EXCLUDED.read_at`, principal.UserID, key)
	if err != nil {
		return fmt.Errorf("mark notification read: %w", err)
	}
	return nil
}

func (s *Service) MarkAllRead(ctx context.Context, principal auth.Principal) error {
	_, err := s.pool.Exec(ctx, notificationCandidatesCTE+`
INSERT INTO notification_reads (user_id, notification_key, read_at)
SELECT $1::uuid, notification_key, now()
FROM notification_candidates
ON CONFLICT (user_id, notification_key) DO UPDATE SET read_at = EXCLUDED.read_at`, principal.UserID, principal.HasRole("group_admin"))
	if err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}

func (s *Service) isVisible(ctx context.Context, principal auth.Principal, key string) (bool, error) {
	var visible bool
	err := s.pool.QueryRow(ctx, notificationCandidatesCTE+`
SELECT EXISTS (SELECT 1 FROM notification_candidates WHERE notification_key = $3)`, principal.UserID, principal.HasRole("group_admin"), key).Scan(&visible)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check notification visibility: %w", err)
	}
	return visible, nil
}

func ValidateCategory(category string) bool {
	if strings.TrimSpace(category) == "" {
		return true
	}
	switch strings.TrimSpace(category) {
	case "work_order_review", "work_order_received", "work_order_returned", "work_order_overdue", "work_order_due_soon", "risk_review", "quality_review", "quality_received", "quality_returned", "quality_exemption":
		return true
	default:
		return false
	}
}
