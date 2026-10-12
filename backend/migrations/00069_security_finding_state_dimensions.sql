-- +goose Up
-- Keep the legacy security_findings.status column as the vulnerability/risk
-- state for API compatibility. Persist the two independent dimensions that
-- were previously inferred or mixed into that value: asset association and
-- remediation execution.
ALTER TABLE security_findings
    ADD COLUMN asset_link_status varchar(16) NOT NULL DEFAULT 'unlinked'
        CHECK (asset_link_status IN ('live', 'pending', 'retired', 'unlinked')),
    ADD COLUMN remediation_status varchar(32) NOT NULL DEFAULT 'not_started'
        CHECK (remediation_status IN ('not_started', 'pending_acceptance', 'in_progress', 'pending_org_review', 'pending_security_review', 'completed'));

UPDATE security_findings f
SET asset_link_status = CASE
    WHEN f.pending_asset_draft_id IS NOT NULL THEN 'pending'
    WHEN f.asset_id IS NULL THEN 'unlinked'
    WHEN a.deleted_at IS NULL THEN 'live'
    ELSE 'retired'
END
FROM (SELECT id, deleted_at FROM assets) a
WHERE a.id = f.asset_id;
UPDATE security_findings
SET asset_link_status = CASE
    WHEN pending_asset_draft_id IS NOT NULL THEN 'pending'
    ELSE 'unlinked'
END
WHERE asset_id IS NULL;

UPDATE security_findings f
SET remediation_status = COALESCE((
    SELECT CASE
        WHEN bool_or(t.status = 'completed') THEN 'completed'
        WHEN bool_or(t.status = 'pending_security_review') THEN 'pending_security_review'
        WHEN bool_or(t.status = 'pending_org_review') THEN 'pending_org_review'
        WHEN bool_or(t.status = 'in_progress') THEN 'in_progress'
        WHEN bool_or(t.status = 'pending_acceptance') THEN 'pending_acceptance'
        ELSE 'not_started'
    END
    FROM remediation_tasks t
    WHERE t.finding_id = f.id AND t.status <> 'cancelled'
), 'not_started');

CREATE INDEX security_findings_asset_link_status_idx
    ON security_findings(asset_link_status, updated_at DESC);
CREATE INDEX security_findings_remediation_status_idx
    ON security_findings(remediation_status, updated_at DESC);

-- Keep the persisted dimensions correct for all existing write paths. The
-- legacy status remains authoritative for vulnerability state; these triggers
-- intentionally do not rewrite it.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION sync_security_finding_asset_link_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    next_status varchar(16);
    prior_write text;
BEGIN
    SELECT CASE
        WHEN NEW.pending_asset_draft_id IS NOT NULL THEN 'pending'
        WHEN NEW.asset_id IS NULL THEN 'unlinked'
        WHEN a.deleted_at IS NULL THEN 'live'
        ELSE 'retired'
    END INTO next_status
    FROM assets a
    WHERE a.id = NEW.asset_id;
    IF NOT FOUND THEN
        next_status := CASE WHEN NEW.pending_asset_draft_id IS NOT NULL THEN 'pending' ELSE 'unlinked' END;
    END IF;
    IF NEW.asset_link_status IS DISTINCT FROM next_status THEN
        -- Derived maintenance write, not an operator edit: satisfy the 00052
        -- remediation guard only for this statement and restore the marker.
        prior_write := current_setting('netassets.remediation_write', true);
        PERFORM set_config('netassets.remediation_write', 'on', true);
        UPDATE security_findings SET asset_link_status = next_status WHERE id = NEW.id;
        PERFORM set_config('netassets.remediation_write', COALESCE(prior_write, ''), true);
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION sync_security_finding_asset_link_from_asset() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    prior_write text;
BEGIN
    prior_write := current_setting('netassets.remediation_write', true);
    PERFORM set_config('netassets.remediation_write', 'on', true);
    UPDATE security_findings
    SET asset_link_status = CASE
        WHEN pending_asset_draft_id IS NOT NULL THEN 'pending'
        WHEN NEW.deleted_at IS NULL THEN 'live'
        ELSE 'retired'
    END
    WHERE asset_id = NEW.id;
    PERFORM set_config('netassets.remediation_write', COALESCE(prior_write, ''), true);
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION sync_security_finding_remediation_status() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    finding_key uuid;
    next_status varchar(32);
    prior_write text;
BEGIN
    finding_key := CASE WHEN TG_OP = 'DELETE' THEN OLD.finding_id ELSE NEW.finding_id END;
    -- The derived write below is system maintenance, not an operator edit of
    -- the vulnerability record, so it must satisfy the 00052 remediation guard.
    prior_write := current_setting('netassets.remediation_write', true);
    PERFORM set_config('netassets.remediation_write', 'on', true);
    SELECT CASE
        WHEN bool_or(t.status = 'completed') THEN 'completed'
        WHEN bool_or(t.status = 'pending_security_review') THEN 'pending_security_review'
        WHEN bool_or(t.status = 'pending_org_review') THEN 'pending_org_review'
        WHEN bool_or(t.status = 'in_progress') THEN 'in_progress'
        WHEN bool_or(t.status = 'pending_acceptance') THEN 'pending_acceptance'
        ELSE 'not_started'
    END INTO next_status
    FROM remediation_tasks t
    WHERE t.finding_id = finding_key AND t.status <> 'cancelled';
    UPDATE security_findings SET remediation_status = COALESCE(next_status, 'not_started') WHERE id = finding_key;
    PERFORM set_config('netassets.remediation_write', COALESCE(prior_write, ''), true);
    RETURN CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER security_findings_asset_link_status_trigger
AFTER INSERT OR UPDATE OF asset_id, pending_asset_draft_id ON security_findings
FOR EACH ROW EXECUTE FUNCTION sync_security_finding_asset_link_status();
CREATE TRIGGER assets_finding_link_status_trigger
AFTER UPDATE OF deleted_at ON assets
FOR EACH ROW EXECUTE FUNCTION sync_security_finding_asset_link_from_asset();
CREATE TRIGGER remediation_tasks_finding_status_trigger
AFTER INSERT OR UPDATE OF finding_id, status OR DELETE ON remediation_tasks
FOR EACH ROW EXECUTE FUNCTION sync_security_finding_remediation_status();

COMMENT ON COLUMN security_findings.status IS '兼容字段：漏洞/风险状态（vulnerability status）；不表示资产关联或整改进度。';
COMMENT ON COLUMN security_findings.asset_link_status IS '资产关联状态：live、pending、retired、unlinked。';
COMMENT ON COLUMN security_findings.remediation_status IS '整改执行状态：由有效整改任务状态汇总，不表示漏洞是否真实或已修复。';

-- +goose Down
DROP TRIGGER IF EXISTS remediation_tasks_finding_status_trigger ON remediation_tasks;
DROP TRIGGER IF EXISTS assets_finding_link_status_trigger ON assets;
DROP TRIGGER IF EXISTS security_findings_asset_link_status_trigger ON security_findings;
DROP FUNCTION IF EXISTS sync_security_finding_remediation_status();
DROP FUNCTION IF EXISTS sync_security_finding_asset_link_from_asset();
DROP FUNCTION IF EXISTS sync_security_finding_asset_link_status();
DROP INDEX IF EXISTS security_findings_remediation_status_idx;
DROP INDEX IF EXISTS security_findings_asset_link_status_idx;
ALTER TABLE security_findings
    DROP COLUMN IF EXISTS remediation_status,
    DROP COLUMN IF EXISTS asset_link_status;
