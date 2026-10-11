-- +goose Up
-- Align security finding statuses with the user-facing workflow. Keep this as a
-- new migration so existing installations receive the data conversion safely.
ALTER TABLE security_findings
    DROP CONSTRAINT IF EXISTS security_findings_status_check;

UPDATE security_findings
SET status = CASE status
    WHEN 'open' THEN 'pending'
    WHEN 'assigned' THEN 'in_progress'
    WHEN 'accepted_risk' THEN 'risk_accepted'
    WHEN 'resolved' THEN 'fixed'
    ELSE status
END;

ALTER TABLE security_findings
    ALTER COLUMN status SET DEFAULT 'pending',
    ADD CONSTRAINT security_findings_status_check CHECK (status IN (
        'pending', 'in_progress', 'confirmed', 'resolved', 'fixed',
        'false_positive', 'ignored', 'duplicate', 'risk_accepted'
    ));

DROP INDEX IF EXISTS security_findings_risk_acceptance_idx;
CREATE INDEX security_findings_risk_acceptance_idx
    ON security_findings(status, risk_accepted_until)
    WHERE status = 'risk_accepted';

-- The function was introduced before the status vocabulary was aligned.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION remediation_order_counts(order_key uuid) RETURNS jsonb LANGUAGE sql STABLE AS $$
 SELECT jsonb_build_object('unassigned',count(*) FILTER(WHERE t.id IS NULL),'pending_acceptance',count(*) FILTER(WHERE t.status='pending_acceptance'),
 'in_progress',count(*) FILTER(WHERE t.status='in_progress'),'pending_org_review',count(*) FILTER(WHERE t.status='pending_org_review'),
 'pending_security_review',count(*) FILTER(WHERE t.status='pending_security_review'),'verified_fixed',count(*) FILTER(WHERE t.completion_reason='verified_fixed'),
 'risk_accepted',count(*) FILTER(WHERE t.completion_reason='risk_accepted' AND f.status='risk_accepted' AND f.risk_accepted_until>now()),
 'false_positive',count(*) FILTER(WHERE t.completion_reason='false_positive'),
 'risk_expired',count(*) FILTER(WHERE t.completion_reason='risk_accepted' AND (f.status<>'risk_accepted' OR f.risk_accepted_until<=now())))
 FROM security_work_order_findings l JOIN security_findings f ON f.id=l.finding_id
 LEFT JOIN remediation_tasks t ON t.work_order_id=l.work_order_id AND t.finding_id=l.finding_id AND t.status<>'cancelled' WHERE l.work_order_id=order_key
$$;
-- +goose StatementEnd

-- +goose Down
DROP INDEX IF EXISTS security_findings_risk_acceptance_idx;
ALTER TABLE security_findings
    DROP CONSTRAINT IF EXISTS security_findings_status_check;
UPDATE security_findings
SET status = CASE status
    WHEN 'pending' THEN 'open'
    WHEN 'in_progress' THEN 'assigned'
    WHEN 'confirmed' THEN 'open'
    WHEN 'risk_accepted' THEN 'accepted_risk'
    WHEN 'fixed' THEN 'resolved'
    WHEN 'ignored' THEN 'false_positive'
    WHEN 'duplicate' THEN 'false_positive'
    ELSE status
END;
ALTER TABLE security_findings
    ALTER COLUMN status SET DEFAULT 'open',
    ADD CONSTRAINT security_findings_status_check CHECK (status IN ('open', 'assigned', 'resolved', 'accepted_risk', 'false_positive'));
CREATE INDEX security_findings_risk_acceptance_idx
    ON security_findings(status, risk_accepted_until)
    WHERE status = 'accepted_risk';
