-- +goose Up
-- An assignee may still read the audit trail and the explicit retirement reason
-- after the asset disappears. They cannot execute new work (the service checks
-- asset_retired_at), but hiding the task entirely would make the reassignment
-- hand-off opaque to the person who was doing it.
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

-- +goose Down
-- Recreate the pre-retirement visibility rule; migration 00052 owns the base
-- function, and this explicit body keeps rollback deterministic.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_can_read(actor uuid, task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND (
 remediation_is_security(actor) OR remediation_can_manage(actor,t.organization_id,'remediation.assign')
 OR remediation_can_manage(actor,t.organization_id,'remediation.review')
 OR (t.assignee_user_id=actor AND remediation_grant_valid(t.grant_id,actor,t.organization_id,t.business_system_id) AND remediation_task_current(t.id))))
 $$;
-- +goose StatementEnd
