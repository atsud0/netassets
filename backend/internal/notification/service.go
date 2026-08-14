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

type candidateScope struct {
	groupAdmin                  bool
	canManageSecurityWorkOrders bool
	canManageSecurityFindings   bool
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func scopeFor(principal auth.Principal) candidateScope {
	return candidateScope{
		groupAdmin:                  principal.HasRole(auth.RoleGroupAdmin),
		canManageSecurityWorkOrders: principal.CanManageSecurityWorkOrders(),
		canManageSecurityFindings:   principal.CanManageSecurityFindings(),
	}
}

const notificationCandidatesCTE = `
WITH RECURSIVE descendant_roots(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	JOIN organizations root ON root.id=ur.organization_id AND root.status='enabled' AND root.deleted_at IS NULL
	WHERE ur.user_id = $1::uuid
	  AND ur.organization_id IS NOT NULL
	  AND ur.data_scope = 'organization_and_descendants'
), descendants(id) AS (
	SELECT id FROM descendant_roots
	UNION
	SELECT child.id
	FROM organizations child
	JOIN descendants parent ON child.parent_id = parent.id
WHERE child.status='enabled' AND child.deleted_at IS NULL
), accessible(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	JOIN organizations direct ON direct.id=ur.organization_id AND direct.status='enabled' AND direct.deleted_at IS NULL
	WHERE ur.user_id = $1::uuid
	  AND ur.organization_id IS NOT NULL
	  AND ur.data_scope IN ('organization', 'organization_and_descendants')
	UNION
	SELECT id FROM descendants
), scoped_organizations(id) AS (
	SELECT o.id
	FROM organizations o
	WHERE o.status = 'enabled'
	  AND o.deleted_at IS NULL
	  AND ($2::boolean OR o.id IN (SELECT id FROM accessible))
), quality_descendant_roots(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='data_quality.manage'
	JOIN organizations quality_root ON quality_root.id=ur.organization_id AND quality_root.status='enabled' AND quality_root.deleted_at IS NULL
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), quality_descendants(id) AS (
	SELECT id FROM quality_descendant_roots
	UNION
	SELECT child.id FROM organizations child JOIN quality_descendants parent ON child.parent_id=parent.id
	WHERE child.status='enabled' AND child.deleted_at IS NULL
), quality_accessible(id) AS (
	SELECT ur.organization_id
	FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id AND rp.permission_key='data_quality.manage'
	JOIN organizations quality_direct ON quality_direct.id=ur.organization_id AND quality_direct.status='enabled' AND quality_direct.deleted_at IS NULL
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
	UNION SELECT id FROM quality_descendants
), quality_scoped_organizations(id) AS (
	SELECT o.id FROM organizations o WHERE o.status='enabled' AND o.deleted_at IS NULL AND ($2::boolean OR o.id IN (SELECT id FROM quality_accessible))
), event_permission_descendant_roots(permission_key,id) AS (
	SELECT rp.permission_key, ur.organization_id
	FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id
	JOIN organizations event_root ON event_root.id=ur.organization_id AND event_root.status='enabled' AND event_root.deleted_at IS NULL
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope='organization_and_descendants'
), event_permission_descendants(permission_key,id) AS (
	SELECT permission_key,id FROM event_permission_descendant_roots
	UNION
	SELECT parent.permission_key,child.id
	FROM organizations child
	JOIN event_permission_descendants parent ON child.parent_id=parent.id
	WHERE child.status='enabled' AND child.deleted_at IS NULL
), event_permission_accessible(permission_key,id) AS (
	SELECT rp.permission_key,ur.organization_id
	FROM user_roles ur
	JOIN roles r ON r.id=ur.role_id AND r.status='enabled'
	JOIN role_permissions rp ON rp.role_id=r.id
	JOIN organizations event_direct ON event_direct.id=ur.organization_id AND event_direct.status='enabled' AND event_direct.deleted_at IS NULL
	WHERE ur.user_id=$1::uuid AND ur.organization_id IS NOT NULL AND ur.data_scope IN ('organization','organization_and_descendants')
	UNION SELECT permission_key,id FROM event_permission_descendants
), stale_working_version_items AS (
	SELECT organization_id, 'asset'::text AS resource_kind, updated_at
	FROM inventory_asset_drafts
	WHERE NOT $2::boolean AND task_id IS NULL AND draft_status='editing' AND updated_at < now()-interval '7 days'
	UNION ALL
	SELECT organization_id, 'business_system'::text AS resource_kind, updated_at
	FROM inventory_business_system_drafts
	WHERE NOT $2::boolean AND task_id IS NULL AND draft_status='editing' AND updated_at < now()-interval '7 days'
), stale_working_versions AS (
	SELECT organization_id, resource_kind, count(*)::bigint AS item_count, min(updated_at) AS oldest_updated_at
	FROM stale_working_version_items
	GROUP BY organization_id, resource_kind
), notification_candidates AS (
	SELECT
		'ledger_event:' || ne.id::text AS notification_key,
		ne.category::text AS category,
		CASE ne.event_type
			WHEN 'asset.status_changed' THEN CASE WHEN ne.item_count>1 THEN '网络资产状态已批量调整' ELSE '网络资产状态已调整' END
			WHEN 'asset.network_area_changed' THEN CASE WHEN ne.item_count>1 THEN '网络区域已批量调整' ELSE '网络区域已调整' END
			WHEN 'asset.archived' THEN CASE WHEN ne.item_count>1 THEN '网络资产已批量归档' ELSE '网络资产已归档' END
			WHEN 'asset.deleted' THEN CASE WHEN ne.item_count>1 THEN '网络资产已批量删除' ELSE '网络资产已删除' END
			WHEN 'asset.restored' THEN CASE WHEN ne.item_count>1 THEN '网络资产已批量恢复' ELSE '网络资产已恢复' END
			WHEN 'business_system.archived' THEN CASE WHEN ne.item_count>1 THEN '业务系统已批量归档' ELSE '业务系统已归档' END
			WHEN 'business_system.restored' THEN CASE WHEN ne.item_count>1 THEN '业务系统已批量恢复' ELSE '业务系统已恢复' END
			WHEN 'business_system.deleted' THEN CASE WHEN ne.item_count>1 THEN '业务系统已批量删除' ELSE '业务系统已删除' END
			WHEN 'security_finding.created_on_asset' THEN CASE WHEN ne.item_count>1 THEN '网络资产新增多项安全发现' ELSE '网络资产新增安全发现' END
			WHEN 'security_finding.created_on_system' THEN CASE WHEN ne.item_count>1 THEN '业务系统新增多项安全发现' ELSE '业务系统新增安全发现' END
			WHEN 'working_version.asset_created' THEN CASE WHEN ne.item_count>1 THEN '新增多项网络资产工作版本' ELSE '新增网络资产工作版本' END
			WHEN 'working_version.business_system_created' THEN CASE WHEN ne.item_count>1 THEN '新增多个业务系统工作版本' ELSE '新增业务系统工作版本' END
			WHEN 'working_version.asset_restored' THEN '网络资产历史版本已恢复'
			WHEN 'working_version.business_system_restored' THEN '业务系统历史版本已恢复'
			WHEN 'working_version.asset_unselected' THEN '网络资产已取消纳入填报'
			WHEN 'working_version.business_system_unselected' THEN '业务系统已取消纳入填报'
			WHEN 'working_version.published' THEN '工作版本已发布'
			ELSE '台账发生变更'
		END::text AS title,
		CASE ne.event_type
			WHEN 'asset.status_changed' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已批量调整本组织 %s 项网络资产的状态，请及时核对台账。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已调整网络资产“%s”的状态，请及时核对。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'asset.network_area_changed' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已批量调整本组织 %s 项网络资产的网络区域，请及时核对台账。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已调整网络资产“%s”的网络区域，请及时核对。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'asset.archived' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已批量归档本组织 %s 项网络资产。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已归档网络资产“%s”。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'asset.deleted' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已将本组织 %s 项网络资产批量移入回收站。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已将网络资产“%s”移入回收站。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'asset.restored' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已从回收站批量恢复本组织 %s 项网络资产。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已从回收站恢复网络资产“%s”。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'business_system.archived' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已批量归档本组织 %s 个业务系统。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已归档业务系统“%s”。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'business_system.restored' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已批量恢复本组织 %s 个业务系统，请确认当前运行状态。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已恢复业务系统“%s”，请确认当前运行状态。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'business_system.deleted' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已将本组织 %s 个业务系统批量移入回收站。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.item_count)
				ELSE format('%s 已将业务系统“%s”移入回收站。', COALESCE(NULLIF(actor.display_name,''),actor.username,'管理员'),ne.resource_label) END
			WHEN 'security_finding.created_on_asset' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已在本组织网络资产上登记 %s 项安全发现，请及时查看和跟进。', COALESCE(NULLIF(actor.display_name,''),actor.username,'安全运营人员'),ne.item_count)
				ELSE format('%s 已在本组织网络资产上登记安全发现 %s，请及时查看和跟进。', COALESCE(NULLIF(actor.display_name,''),actor.username,'安全运营人员'),ne.resource_label) END
			WHEN 'security_finding.created_on_system' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已在本组织业务系统上登记 %s 项安全发现，请及时查看和跟进。', COALESCE(NULLIF(actor.display_name,''),actor.username,'安全运营人员'),ne.item_count)
				ELSE format('%s 已在本组织业务系统上登记安全发现 %s，请及时查看和跟进。', COALESCE(NULLIF(actor.display_name,''),actor.username,'安全运营人员'),ne.resource_label) END
			WHEN 'working_version.asset_created' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已新增本组织 %s 项网络资产工作版本，当前尚未上报集团。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.item_count)
				ELSE format('%s 已新增网络资产工作版本“%s”，当前尚未上报集团。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.resource_label) END
			WHEN 'working_version.business_system_created' THEN CASE WHEN ne.item_count>1
				THEN format('%s 已新增本组织 %s 个业务系统工作版本，当前尚未上报集团。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.item_count)
				ELSE format('%s 已新增业务系统工作版本“%s”，当前尚未上报集团。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.resource_label) END
			WHEN 'working_version.asset_restored' THEN format('%s 已将网络资产“%s”的历史版本恢复为未上报工作版本。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.resource_label)
			WHEN 'working_version.business_system_restored' THEN format('%s 已将业务系统“%s”的历史版本恢复为未上报工作版本。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.resource_label)
			WHEN 'working_version.asset_unselected' THEN format('%s 已将网络资产“%s”从当前填报任务取消纳入，资源已回到未上报工作区。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.resource_label)
			WHEN 'working_version.business_system_unselected' THEN format('%s 已将业务系统“%s”从当前填报任务取消纳入，资源已回到未上报工作区。', COALESCE(NULLIF(actor.display_name,''),actor.username,'组织用户'),ne.resource_label)
			WHEN 'working_version.published' THEN format('填报任务《%s》中 %s 个工作版本已通过审核并发布到集团正式台账。',ne.resource_label,COALESCE(NULLIF(ne.details->>'item_count',''),'0'))
			ELSE '本组织台账发生变更，请及时查看。'
		END::text AS message,
		ne.level::text AS level,
		ne.resource_type::text AS resource_type,
		COALESCE(ne.resource_id,'')::text AS resource_id,
		CASE
			WHEN ne.event_type='asset.deleted' THEN '/recycle-bin?resource_type=asset&organization_id=' || ne.target_organization_id::text
			WHEN ne.event_type LIKE 'asset.%' THEN '/assets?organization_id=' || ne.target_organization_id::text || CASE WHEN ne.resource_id IS NOT NULL THEN '&asset_id=' || ne.resource_id ELSE '' END
			WHEN ne.event_type='business_system.deleted' THEN '/recycle-bin?resource_type=business_system&organization_id=' || ne.target_organization_id::text
			WHEN ne.event_type LIKE 'business_system.%' THEN '/business-systems?organization_id=' || ne.target_organization_id::text || CASE WHEN ne.resource_id IS NOT NULL THEN '&system_id=' || ne.resource_id ELSE '' END
			WHEN ne.event_type LIKE 'security_finding.%' THEN '/security-work-orders?tab=findings&organization_id=' || ne.target_organization_id::text || CASE WHEN ne.resource_id IS NOT NULL THEN '&finding_id=' || ne.resource_id ELSE '' END
			WHEN ne.event_type IN ('working_version.asset_created','working_version.asset_restored','working_version.asset_unselected') THEN '/assets?organization_id=' || ne.target_organization_id::text || CASE WHEN ne.resource_id IS NOT NULL THEN '&asset_id=' || ne.resource_id ELSE '' END
			WHEN ne.event_type IN ('working_version.business_system_created','working_version.business_system_restored','working_version.business_system_unselected') THEN '/business-systems?organization_id=' || ne.target_organization_id::text || CASE WHEN ne.resource_id IS NOT NULL THEN '&system_id=' || ne.resource_id ELSE '' END
			WHEN ne.event_type='working_version.published' THEN '/inventory-tasks?task_id=' || COALESCE(ne.resource_id,'') || '&organization_id=' || ne.target_organization_id::text
			ELSE ''
		END::text AS action_path,
		ne.updated_at AS occurred_at,
		NULL::timestamptz AS due_at
		FROM notification_events ne
		LEFT JOIN users actor ON actor.id=ne.actor_user_id
		WHERE ne.actor_user_id IS DISTINCT FROM $1::uuid
		  AND (ne.category <> 'working_version' OR NOT $2::boolean)
		  AND (
			$2::boolean
			OR ne.target_user_id=$1::uuid
			OR (
				ne.target_user_id IS NULL
				AND EXISTS (
					SELECT 1 FROM event_permission_accessible visible
					WHERE visible.permission_key=ne.required_permission AND visible.id=ne.target_organization_id
				)
			)
		  )


	UNION ALL

	SELECT
		'working_version:stale:' || stale.resource_kind || ':' || stale.organization_id::text || ':' || to_char(date_trunc('week',now()),'IYYY-IW') || ':' || stale.item_count::text AS notification_key,
		'working_version'::text AS category,
		CASE stale.resource_kind WHEN 'asset' THEN '网络资产工作版本长期未上报' ELSE '业务系统工作版本长期未上报' END::text AS title,
		format(
			CASE stale.resource_kind
				WHEN 'asset' THEN '本组织有 %s 项网络资产工作版本已超过 7 天未上报，最早已停留 %s 天，请及时纳入填报任务或清理无效版本。'
				ELSE '本组织有 %s 个业务系统工作版本已超过 7 天未上报，最早已停留 %s 天，请及时纳入填报任务或清理无效版本。'
			END,
			stale.item_count,
			floor(extract(epoch FROM (now()-stale.oldest_updated_at))/86400)::bigint
		)::text AS message,
		'warning'::text AS level,
		'working_version'::text AS resource_type,
		stale.organization_id::text AS resource_id,
		CASE stale.resource_kind
			WHEN 'asset' THEN '/assets?organization_id=' || stale.organization_id::text
			ELSE '/business-systems?organization_id=' || stale.organization_id::text
		END::text AS action_path,
		date_trunc('week',now()) AS occurred_at,
		NULL::timestamptz AS due_at
	FROM stale_working_versions stale
	WHERE NOT $2::boolean
	  AND EXISTS (
		SELECT 1 FROM event_permission_accessible visible
		WHERE visible.id=stale.organization_id
		  AND visible.permission_key IN (
			'inventory.manage',
			CASE stale.resource_kind WHEN 'asset' THEN 'asset.manage' ELSE 'system.manage' END
		  )
	  )

	UNION ALL

	SELECT
		'inventory_task:pending_review:' || t.id::text || ':' || ito.organization_id::text || ':' || extract(epoch FROM COALESCE(ito.submitted_at,ito.updated_at))::bigint::text AS notification_key,
		'inventory_task_review'::text AS category,
		'填报任务待审核'::text AS title,
		o.organization_name || '已提交填报任务《' || t.task_name || '》，等待集团审核。'::text AS message,
		'warning'::text AS level,
		'inventory_task'::text AS resource_type,
		t.id::text AS resource_id,
		'/inventory-tasks?task_id=' || t.id::text || '&organization_id=' || ito.organization_id::text AS action_path,
		COALESCE(ito.submitted_at,ito.updated_at) AS occurred_at,
		t.deadline_at AS due_at
	FROM inventory_task_organizations ito
	JOIN inventory_tasks t ON t.id=ito.task_id
	JOIN organizations o ON o.id=ito.organization_id
	WHERE $2::boolean AND t.status='active' AND t.require_review AND ito.status='submitted'

	UNION ALL

	SELECT
		'inventory_task:received:' || t.id::text || ':' || ito.organization_id::text || ':' || extract(epoch FROM t.updated_at)::bigint::text,
		'inventory_task_received',
		'收到新的填报任务',
		'集团已下发填报任务《' || t.task_name || '》，请在截止时间前完成核查和提交。',
		'info',
		'inventory_task',
		t.id::text,
		'/inventory-tasks?task_id=' || t.id::text || '&organization_id=' || ito.organization_id::text,
		t.updated_at,
		t.deadline_at
	FROM inventory_task_organizations ito
	JOIN inventory_tasks t ON t.id=ito.task_id
	WHERE NOT $2::boolean AND t.status='active' AND ito.status='not_started'
	  AND EXISTS (
		SELECT 1 FROM event_permission_accessible visible
		WHERE visible.permission_key='inventory.manage' AND visible.id=ito.organization_id
	  )

	UNION ALL

	SELECT
		'inventory_task:returned:' || t.id::text || ':' || ito.organization_id::text || ':' || extract(epoch FROM ito.updated_at)::bigint::text,
		'inventory_task_returned',
		'填报任务已退回',
		'填报任务《' || t.task_name || '》审核未通过，请根据退回原因和逐条意见完成整改后重新提交。',
		'warning',
		'inventory_task',
		t.id::text,
		'/inventory-tasks?task_id=' || t.id::text || '&organization_id=' || ito.organization_id::text,
		ito.updated_at,
		t.deadline_at
	FROM inventory_task_organizations ito
	JOIN inventory_tasks t ON t.id=ito.task_id
	WHERE NOT $2::boolean AND t.status='active' AND ito.status='returned'
	  AND EXISTS (
		SELECT 1 FROM event_permission_accessible visible
		WHERE visible.permission_key='inventory.manage' AND visible.id=ito.organization_id
	  )

	UNION ALL

	SELECT
		'inventory_task:overdue:' || t.id::text || ':' || ito.organization_id::text || ':' || extract(epoch FROM t.deadline_at)::bigint::text,
		'inventory_task_overdue',
		'填报任务已逾期',
		'填报任务《' || t.task_name || '》已超过截止时间，请尽快处理并与集团确认。',
		'danger',
		'inventory_task',
		t.id::text,
		'/inventory-tasks?task_id=' || t.id::text || '&organization_id=' || ito.organization_id::text,
		t.deadline_at,
		t.deadline_at
	FROM inventory_task_organizations ito
	JOIN inventory_tasks t ON t.id=ito.task_id
	WHERE NOT $2::boolean AND t.status='active' AND ito.status IN ('not_started','in_progress','returned','overdue')
	  AND t.deadline_at < now()
	  AND EXISTS (
		SELECT 1 FROM event_permission_accessible visible
		WHERE visible.permission_key='inventory.manage' AND visible.id=ito.organization_id
	  )

	UNION ALL

	SELECT
		'inventory_task:due_soon:' || t.id::text || ':' || ito.organization_id::text || ':' || extract(epoch FROM t.deadline_at)::bigint::text,
		'inventory_task_due_soon',
		'填报任务即将截止',
		'填报任务《' || t.task_name || '》将在 3 天内截止，请及时完成核查和提交。',
		'warning',
		'inventory_task',
		t.id::text,
		'/inventory-tasks?task_id=' || t.id::text || '&organization_id=' || ito.organization_id::text,
		now(),
		t.deadline_at
	FROM inventory_task_organizations ito
	JOIN inventory_tasks t ON t.id=ito.task_id
	WHERE NOT $2::boolean AND t.status='active' AND ito.status IN ('not_started','in_progress','returned','overdue')
	  AND t.deadline_at >= now() AND t.deadline_at < now()+interval '3 days'
	  AND EXISTS (
		SELECT 1 FROM event_permission_accessible visible
		WHERE visible.permission_key='inventory.manage' AND visible.id=ito.organization_id
	  )

	UNION ALL

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
	WHERE $3::boolean
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
	WHERE NOT $3::boolean
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
	WHERE NOT $3::boolean
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
	  AND ($3::boolean OR wo.assignee_organization_id IN (SELECT id FROM scoped_organizations))
	  AND ($3::boolean OR wo.issued_at IS NOT NULL)

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
	  AND ($3::boolean OR wo.assignee_organization_id IN (SELECT id FROM scoped_organizations))
	  AND ($3::boolean OR wo.issued_at IS NOT NULL)

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
	WHERE $4::boolean
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
	WHERE $4::boolean
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
		LEFT JOIN asset_organizations quality_asset_owner
		  ON i.resource_type='asset' AND quality_asset_owner.asset_id=i.resource_id AND quality_asset_owner.is_primary
		WHERE NOT $2::boolean AND i.status='pending'
		  AND (
			i.assignee_user_id=$1::uuid
			OR (i.assignee_user_id IS NULL AND i.resource_type='asset' AND quality_asset_owner.owner_user_id=$1::uuid)
			OR (
				i.assignee_user_id IS NULL
				AND (i.resource_type<>'asset' OR quality_asset_owner.owner_user_id IS NULL)
				AND i.organization_id IN (SELECT id FROM quality_scoped_organizations)
			)
		  )

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
		LEFT JOIN asset_organizations quality_asset_owner
		  ON i.resource_type='asset' AND quality_asset_owner.asset_id=i.resource_id AND quality_asset_owner.is_primary
		JOIN LATERAL (
			SELECT event_type,created_at FROM data_quality_issue_events WHERE issue_id=i.id ORDER BY created_at DESC,id DESC LIMIT 1
		) e ON e.event_type='returned'
		WHERE NOT $2::boolean AND i.status='in_progress'
		  AND (
			i.assignee_user_id=$1::uuid
			OR (i.assignee_user_id IS NULL AND i.resource_type='asset' AND quality_asset_owner.owner_user_id=$1::uuid)
			OR (
				i.assignee_user_id IS NULL
				AND (i.resource_type<>'asset' OR quality_asset_owner.owner_user_id IS NULL)
				AND i.organization_id IN (SELECT id FROM quality_scoped_organizations)
			)
		  )

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

	UNION ALL

	SELECT
		'export_job:' || ej.id::text || ':' || ej.status || ':' || extract(epoch FROM ej.updated_at)::bigint::text,
		'export_job',
		CASE ej.status WHEN 'completed' THEN '后台导出文件已生成' ELSE '后台导出任务生成失败' END,
		CASE ej.status
			WHEN 'completed' THEN format('%s 已生成，共 %s 条数据，请在到期前下载。', ej.file_name, ej.processed_rows)
			ELSE format('%s后台导出任务执行失败，请打开导出任务查看并重试。', CASE ej.resource_type WHEN 'asset' THEN '网络资产' WHEN 'business_system' THEN '业务系统' ELSE '填报任务' END)
		END,
		CASE ej.status WHEN 'completed' THEN 'info' ELSE 'warning' END,
		'export_job',
		ej.id::text,
		'/export-jobs?job_id=' || ej.id::text,
		COALESCE(ej.completed_at, ej.updated_at),
		CASE WHEN ej.status='completed' THEN ej.expires_at ELSE NULL::timestamptz END
	FROM export_jobs ej
	WHERE ej.requested_by=$1::uuid AND ej.status IN ('completed','failed')
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
	scope := scopeFor(principal)
	query := notificationCandidatesCTE + `
SELECT nc.notification_key, nc.category, nc.title, nc.message, nc.level,
       nc.resource_type, nc.resource_id, nc.action_path, nc.occurred_at, nc.due_at,
       nr.read_at IS NOT NULL AS is_read, count(*) OVER ()
FROM notification_candidates nc
LEFT JOIN notification_reads nr
  ON nr.user_id = $1::uuid AND nr.notification_key = nc.notification_key
WHERE ($5::boolean = false OR nr.read_at IS NULL)
  AND ($6 = '' OR nc.category = $6)
ORDER BY (nr.read_at IS NULL) DESC,
         CASE nc.level WHEN 'danger' THEN 3 WHEN 'warning' THEN 2 ELSE 1 END DESC,
         nc.occurred_at DESC
LIMIT $7 OFFSET $8`
	rows, err := s.pool.Query(ctx, query, principal.UserID, scope.groupAdmin, scope.canManageSecurityWorkOrders, scope.canManageSecurityFindings, input.UnreadOnly, input.Category, input.Page.PageSize, input.Page.Offset())
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
WHERE ($5::boolean = false OR nr.read_at IS NULL)
  AND ($6 = '' OR nc.category = $6)`
		if err := s.pool.QueryRow(ctx, countQuery, principal.UserID, scope.groupAdmin, scope.canManageSecurityWorkOrders, scope.canManageSecurityFindings, input.UnreadOnly, input.Category).Scan(&total); err != nil {
			return pagination.Result[Item]{}, Summary{}, fmt.Errorf("count notifications: %w", err)
		}
	}
	summary, err := s.summaryWithCandidates(ctx, principal, scope)
	if err != nil {
		return pagination.Result[Item]{}, Summary{}, err
	}
	return pagination.NewResult(items, total, input.Page), summary, nil
}

func (s *Service) Summary(ctx context.Context, principal auth.Principal) (Summary, error) {
	return s.summaryWithCandidates(ctx, principal, scopeFor(principal))
}

func (s *Service) summaryWithCandidates(ctx context.Context, principal auth.Principal, scope candidateScope) (Summary, error) {
	var result Summary
	err := s.pool.QueryRow(ctx, notificationCandidatesCTE+`
SELECT count(*)::bigint, count(*) FILTER (WHERE nr.read_at IS NULL)::bigint
FROM notification_candidates nc
LEFT JOIN notification_reads nr
  ON nr.user_id = $1::uuid AND nr.notification_key = nc.notification_key`, principal.UserID, scope.groupAdmin, scope.canManageSecurityWorkOrders, scope.canManageSecurityFindings).Scan(&result.Total, &result.UnreadCount)
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
	scope := scopeFor(principal)
	_, err := s.pool.Exec(ctx, notificationCandidatesCTE+`
INSERT INTO notification_reads (user_id, notification_key, read_at)
SELECT $1::uuid, notification_key, now()
FROM notification_candidates
ON CONFLICT (user_id, notification_key) DO UPDATE SET read_at = EXCLUDED.read_at`, principal.UserID, scope.groupAdmin, scope.canManageSecurityWorkOrders, scope.canManageSecurityFindings)
	if err != nil {
		return fmt.Errorf("mark all notifications read: %w", err)
	}
	return nil
}

func (s *Service) isVisible(ctx context.Context, principal auth.Principal, key string) (bool, error) {
	scope := scopeFor(principal)
	var visible bool
	err := s.pool.QueryRow(ctx, notificationCandidatesCTE+`
SELECT EXISTS (SELECT 1 FROM notification_candidates WHERE notification_key = $5)`, principal.UserID, scope.groupAdmin, scope.canManageSecurityWorkOrders, scope.canManageSecurityFindings, key).Scan(&visible)
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
	case "export_job", "work_order_review", "work_order_received", "work_order_returned", "work_order_overdue", "work_order_due_soon", "risk_review", "quality_review", "quality_received", "quality_returned", "quality_exemption", CategoryAssetChange, CategoryBusinessSystemChange, CategoryFindingReceived, CategoryInventoryTaskReview, CategoryInventoryTaskReceived, CategoryInventoryTaskReturned, CategoryInventoryTaskDueSoon, CategoryInventoryTaskOverdue, CategoryWorkingVersion:
		return true
	default:
		return false
	}
}
