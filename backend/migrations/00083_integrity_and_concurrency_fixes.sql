-- +goose Up
-- Complete the optimistic-lock and workflow invariants that were introduced
-- incrementally.  These are additive so already-published migrations remain
-- immutable.
ALTER TABLE assets ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;
ALTER TABLE security_work_order_findings ADD COLUMN IF NOT EXISTS finding_status_before_issue varchar(32);
ALTER TABLE business_systems ADD COLUMN IF NOT EXISTS status_before_archive varchar(24);

-- Retain the most recent open request per task; older simultaneous requests
-- are settled as rejected before enforcing mutual exclusion.
WITH ranked AS (
  SELECT id, row_number() OVER (PARTITION BY task_id ORDER BY created_at DESC, id DESC) AS position
  FROM remediation_requests WHERE status='pending'
)
UPDATE remediation_requests r SET status='rejected', decision='rejected', decided_at=now()
FROM ranked WHERE ranked.id=r.id AND ranked.position>1;
DROP INDEX IF EXISTS remediation_pending_request_idx;
CREATE UNIQUE INDEX remediation_one_pending_request_idx ON remediation_requests(task_id) WHERE status='pending';

-- A finding can be issued from either pending or confirmed.  Keep that state on
-- the work-order link so cancelling an order never silently changes confirmed
-- findings into pending findings.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_restore_finding_status_after_cancel(order_key uuid, actor uuid) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
  link record;
BEGIN
  FOR link IN
    SELECT l.finding_id, l.finding_status_before_issue
    FROM security_work_order_findings l
    WHERE l.work_order_id=order_key
      AND l.finding_status_before_issue IS NOT NULL
  LOOP
    IF NOT EXISTS (
      SELECT 1 FROM security_work_order_findings other_link
      JOIN security_work_orders other ON other.id=other_link.work_order_id
      WHERE other_link.finding_id=link.finding_id
        AND other.id<>order_key
        AND other.status NOT IN ('closed','cancelled')
    ) THEN
      UPDATE security_findings
      SET status=link.finding_status_before_issue, updated_at=now(), updated_by=actor
      WHERE id=link.finding_id AND status='in_progress';
    END IF;
  END LOOP;
END
$$;
-- +goose StatementEnd

-- Rebuild the derived finding state from the newest active task, not from any
-- historical completed task.  A second remediation round must be visible.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_security_finding_remediation_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    finding_key uuid;
    next_status varchar(32);
    prior_write text;
BEGIN
    finding_key := CASE WHEN TG_OP = 'DELETE' THEN OLD.finding_id ELSE NEW.finding_id END;
    prior_write := current_setting('netassets.remediation_write', true);
    PERFORM set_config('netassets.remediation_write', 'on', true);
    SELECT CASE t.status
        WHEN 'completed' THEN 'completed'
        WHEN 'pending_security_review' THEN 'pending_security_review'
        WHEN 'pending_org_review' THEN 'pending_org_review'
        WHEN 'in_progress' THEN 'in_progress'
        WHEN 'pending_acceptance' THEN 'pending_acceptance'
        ELSE 'not_started'
    END INTO next_status
    FROM remediation_tasks t
    WHERE t.finding_id=finding_key AND t.status<>'cancelled'
    ORDER BY t.created_at DESC, t.id DESC
    LIMIT 1;
    UPDATE security_findings SET remediation_status=COALESCE(next_status,'not_started') WHERE id=finding_key;
    PERFORM set_config('netassets.remediation_write', COALESCE(prior_write,''), true);
    RETURN CASE WHEN TG_OP='DELETE' THEN OLD ELSE NEW END;
END
$$;
-- +goose StatementEnd

-- archived is not executable/live, even though it is not a soft-deleted row.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_security_finding_asset_link_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_status varchar(16); prior_write text;
BEGIN
    SELECT CASE
      WHEN NEW.pending_asset_draft_id IS NOT NULL THEN 'pending'
      WHEN NEW.asset_id IS NULL THEN 'unlinked'
      WHEN a.deleted_at IS NULL AND a.status<>'archived' THEN 'live'
      ELSE 'retired' END
      INTO next_status FROM assets a WHERE a.id=NEW.asset_id;
    IF NOT FOUND THEN next_status:=CASE WHEN NEW.pending_asset_draft_id IS NOT NULL THEN 'pending' ELSE 'unlinked' END; END IF;
    IF NEW.asset_link_status IS DISTINCT FROM next_status THEN
      prior_write:=current_setting('netassets.remediation_write',true);
      PERFORM set_config('netassets.remediation_write','on',true);
      UPDATE security_findings SET asset_link_status=next_status WHERE id=NEW.id;
      PERFORM set_config('netassets.remediation_write',COALESCE(prior_write,''),true);
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_security_finding_asset_link_from_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE prior_write text;
BEGIN
  prior_write:=current_setting('netassets.remediation_write',true);
  PERFORM set_config('netassets.remediation_write','on',true);
  UPDATE security_findings SET asset_link_status=CASE
    WHEN pending_asset_draft_id IS NOT NULL THEN 'pending'
    WHEN NEW.deleted_at IS NULL AND NEW.status<>'archived' THEN 'live'
    ELSE 'retired' END WHERE asset_id=NEW.id;
  PERFORM set_config('netassets.remediation_write',COALESCE(prior_write,''),true);
  RETURN NEW;
END
$$;
-- +goose StatementEnd

DROP TRIGGER IF EXISTS assets_finding_link_status_trigger ON assets;
CREATE TRIGGER assets_finding_link_status_trigger AFTER UPDATE OF deleted_at, status ON assets FOR EACH ROW EXECUTE FUNCTION sync_security_finding_asset_link_from_asset();

-- Restore the system mismatch guard lost when 00080 replaced the function.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_task_execution_state(task_key uuid)
RETURNS TABLE(can_execute boolean, reason text) LANGUAGE sql STABLE AS $$
 SELECT CASE WHEN x.reason IS NULL THEN true ELSE false END, x.reason
 FROM (SELECT CASE
  WHEN t.id IS NULL THEN 'task_not_found'
  WHEN t.status IN ('completed','cancelled') THEN 'task_closed'
  WHEN t.asset_retired_at IS NOT NULL THEN 'asset_retired'
  WHEN t.system_retired_at IS NOT NULL THEN 'system_retired'
  WHEN NOT EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id) THEN 'finding_missing'
  WHEN NOT EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id AND f.assignee_organization_id=t.organization_id) THEN 'organization_mismatch'
  WHEN NOT EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id AND COALESCE(f.business_system_id,(SELECT sa.business_system_id FROM system_assets sa WHERE sa.asset_id=f.asset_id AND sa.is_primary ORDER BY sa.business_system_id LIMIT 1))=t.business_system_id) THEN 'system_mismatch'
  WHEN EXISTS(SELECT 1 FROM security_findings f WHERE f.id=t.finding_id AND f.asset_id IS NOT NULL AND NOT EXISTS(SELECT 1 FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary WHERE a.id=f.asset_id AND a.deleted_at IS NULL AND a.status<>'archived' AND ao.organization_id=t.organization_id)) THEN 'asset_unavailable'
  WHEN NOT EXISTS(SELECT 1 FROM business_systems b WHERE b.id=t.business_system_id AND b.deleted_at IS NULL AND b.system_status<>'archived') THEN 'system_unavailable'
  WHEN NOT remediation_grant_valid(t.grant_id,t.assignee_user_id,t.organization_id,t.business_system_id) THEN 'authorization_expired'
  ELSE NULL END reason FROM remediation_tasks t WHERE t.id=task_key) x
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS remediation_restore_finding_status_after_cancel(uuid,uuid);
-- Do not remove additive columns on rollback; deployed data must remain readable.
