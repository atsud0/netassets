-- +goose Up
-- A business system soft delete invalidates remediation work exactly like an
-- asset soft delete does: remediation_task_current() requires the system to be
-- live, so every open task on the system silently flipped to needs-reassignment
-- with no recorded reason and no restorable marker. The asset path already has
-- an explicit retirement record; this adds the same explicit record for the
-- system dimension so the reviewer can explain and later restore the work.
ALTER TABLE remediation_tasks
    ADD COLUMN IF NOT EXISTS system_retired_at timestamptz;
CREATE INDEX IF NOT EXISTS remediation_tasks_system_retired_idx
    ON remediation_tasks(business_system_id) WHERE system_retired_at IS NOT NULL;

-- The retirement block on the assignee side is identical to the asset one, so
-- fold both markers into the single read predicate.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_can_read(actor uuid, task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND (
 remediation_is_security(actor) OR remediation_can_manage(actor,t.organization_id,'remediation.assign')
 OR remediation_can_manage(actor,t.organization_id,'remediation.review')
 OR (t.assignee_user_id=actor AND (
      (remediation_grant_valid(t.grant_id,actor,t.organization_id,t.business_system_id) AND remediation_task_current(t.id))
      OR t.asset_retired_at IS NOT NULL OR t.system_retired_at IS NOT NULL))))
 $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_can_read(actor uuid, task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND (
 remediation_is_security(actor) OR remediation_can_manage(actor,t.organization_id,'remediation.assign')
 OR remediation_can_manage(actor,t.organization_id,'remediation.review')
 OR (t.assignee_user_id=actor AND (
      (remediation_grant_valid(t.grant_id,actor,t.organization_id,t.business_system_id) AND remediation_task_current(t.id))
      OR t.asset_retired_at IS NOT NULL))))
 $$;
-- +goose StatementEnd
DROP INDEX IF EXISTS remediation_tasks_system_retired_idx;
ALTER TABLE remediation_tasks DROP COLUMN IF EXISTS system_retired_at;
