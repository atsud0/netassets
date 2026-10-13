-- +goose Up
-- Remediation executability was decided by three different expressions:
-- remediation_task_current(), the authorizeTask executor SQL, and the
-- needs_reassignment filter. They drifted (the system-retirement marker was
-- added to two of them and missed in the displayed column), and none of them can
-- tell a manager *why* a task stopped being executable. This function is the one
-- place that answers both questions, so the list, the detail panel and the action
-- guard cannot disagree again.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_task_execution_state(task_key uuid)
RETURNS TABLE(can_execute boolean, reason text) LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN x.reason IS NULL THEN true ELSE false END, x.reason
 FROM (
  SELECT CASE
    WHEN t.id IS NULL THEN 'task_not_found'
    WHEN t.status IN ('completed','cancelled') THEN 'task_closed'
    -- Retired markers come first: they are the explicit, restorable record and
    -- they explain the more common operational case.
    WHEN t.asset_retired_at IS NOT NULL THEN 'asset_retired'
    WHEN t.system_retired_at IS NOT NULL THEN 'system_retired'
    -- Then the live-data checks remediation_task_current() folds together.
    WHEN NOT EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id) THEN 'finding_missing'
    WHEN NOT EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id AND f.assignee_organization_id=t.organization_id) THEN 'organization_mismatch'
    WHEN NOT EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id
      AND COALESCE(f.business_system_id,(SELECT sa.business_system_id FROM system_assets sa WHERE sa.asset_id=f.asset_id AND sa.is_primary ORDER BY sa.business_system_id LIMIT 1))=t.business_system_id) THEN 'system_mismatch'
    WHEN EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id AND f.asset_id IS NOT NULL
      AND NOT EXISTS(SELECT 1 FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
        WHERE a.id=f.asset_id AND a.deleted_at IS NULL AND a.status<>'archived' AND ao.organization_id=t.organization_id))
      THEN 'asset_unavailable'
    WHEN NOT EXISTS(SELECT 1 FROM business_systems b WHERE b.id=t.business_system_id AND b.deleted_at IS NULL AND b.system_status<>'archived') THEN 'system_unavailable'
    WHEN NOT EXISTS(SELECT 1 FROM remediation_grants g
      JOIN users u ON u.id=g.user_id AND u.status='enabled' AND u.deleted_at IS NULL
      JOIN organizations o ON o.id=g.organization_id AND o.status='enabled' AND o.deleted_at IS NULL
      JOIN business_systems gb ON gb.id=g.business_system_id AND gb.owner_organization_id=g.organization_id
        AND gb.deleted_at IS NULL AND gb.system_status<>'archived'
      LEFT JOIN vendors v ON v.id=g.vendor_id
      WHERE g.id=t.grant_id AND g.user_id=t.assignee_user_id AND g.organization_id=t.organization_id AND g.business_system_id=t.business_system_id
        AND g.status='enabled' AND g.valid_from<=now() AND g.valid_until>now()
        AND (g.vendor_id IS NULL OR (v.status='enabled' AND v.organization_id=g.organization_id)))
      THEN 'authorization_expired'
    ELSE NULL END AS reason
  FROM remediation_tasks t WHERE t.id=task_key) x
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_task_execution_state(actor uuid, task_key uuid)
RETURNS TABLE(can_execute boolean, reason text) LANGUAGE sql STABLE AS $$
 SELECT s.can_execute,
   CASE WHEN s.can_execute THEN NULL
        WHEN s.reason IS NOT NULL THEN s.reason
        WHEN NOT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND t.assignee_user_id=actor) THEN 'not_assignee'
        ELSE 'not_authorized' END
 FROM remediation_task_execution_state(task_key) s
$$;
-- +goose StatementEnd
-- The list filter must be the exact negation of the displayed column, so bind
-- the filter to the function instead of repeating the expression.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_task_needs_reassignment(task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT COALESCE((SELECT reason IS NOT NULL FROM remediation_task_execution_state(task_key)), false)
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS remediation_task_needs_reassignment(uuid);
DROP FUNCTION IF EXISTS remediation_task_execution_state(uuid,uuid);
DROP FUNCTION IF EXISTS remediation_task_execution_state(uuid);
