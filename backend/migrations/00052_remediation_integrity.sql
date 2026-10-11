-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION remediation_task_current(task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_findings f ON f.id=t.finding_id
 WHERE t.id=task_key AND f.assignee_organization_id=t.organization_id
 AND COALESCE(f.business_system_id,(SELECT sa.business_system_id FROM system_assets sa WHERE sa.asset_id=f.asset_id AND sa.is_primary ORDER BY sa.business_system_id LIMIT 1))=t.business_system_id
 AND (f.asset_id IS NULL OR EXISTS(SELECT 1 FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
 WHERE a.id=f.asset_id AND a.deleted_at IS NULL AND a.status<>'archived' AND ao.organization_id=t.organization_id)))
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_can_read(actor uuid, task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND (
 remediation_is_security(actor) OR remediation_can_manage(actor,t.organization_id,'remediation.assign')
 OR remediation_can_manage(actor,t.organization_id,'remediation.review')
 OR (t.assignee_user_id=actor AND remediation_grant_valid(t.grant_id,actor,t.organization_id,t.business_system_id) AND remediation_task_current(t.id))))
$$;
-- +goose StatementEnd

-- Legacy finding APIs must not race past the task workflow. The repair service
-- sets this transaction-local marker only after its own authorization checks.
-- +goose StatementBegin
CREATE FUNCTION guard_remediation_finding_write() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 PERFORM pg_advisory_xact_lock(hashtext('netassets-remediation'));
 IF current_setting('netassets.remediation_write',true) IS DISTINCT FROM 'on'
 AND EXISTS(SELECT 1 FROM remediation_tasks t JOIN security_work_orders w ON w.id=t.work_order_id
 WHERE t.finding_id=OLD.id AND t.status<>'cancelled' AND w.status NOT IN ('closed','cancelled')) THEN
  RAISE EXCEPTION 'finding is managed by a personal remediation task' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER guard_remediation_finding_write BEFORE UPDATE ON security_findings FOR EACH ROW EXECUTE FUNCTION guard_remediation_finding_write();

-- +goose Down
DROP TRIGGER guard_remediation_finding_write ON security_findings;
DROP FUNCTION guard_remediation_finding_write();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_can_read(actor uuid, task_key uuid) RETURNS boolean LANGUAGE sql STABLE AS $$
 SELECT EXISTS(SELECT 1 FROM remediation_tasks t WHERE t.id=task_key AND (
 remediation_is_security(actor) OR remediation_can_manage(actor,t.organization_id,'remediation.assign')
 OR remediation_can_manage(actor,t.organization_id,'remediation.review')
 OR (t.assignee_user_id=actor AND remediation_grant_valid(t.grant_id,actor,t.organization_id,t.business_system_id))))
$$;
-- +goose StatementEnd
DROP FUNCTION remediation_task_current(uuid);
