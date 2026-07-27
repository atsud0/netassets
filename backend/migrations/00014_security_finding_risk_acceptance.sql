-- +goose Up
ALTER TABLE security_findings
    ADD COLUMN risk_accepted_at timestamptz,
    ADD COLUMN risk_accepted_until timestamptz,
    ADD COLUMN risk_acceptance_reason text,
    ADD COLUMN risk_accepted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN risk_accepted_from_status varchar(24),
    ADD COLUMN risk_reviewed_at timestamptz,
    ADD COLUMN risk_reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN risk_review_comment text;

CREATE INDEX security_findings_risk_acceptance_idx
    ON security_findings(status, risk_accepted_until)
    WHERE status = 'accepted_risk';

-- +goose Down
DROP INDEX IF EXISTS security_findings_risk_acceptance_idx;
ALTER TABLE security_findings
    DROP COLUMN IF EXISTS risk_review_comment,
    DROP COLUMN IF EXISTS risk_reviewed_by,
    DROP COLUMN IF EXISTS risk_reviewed_at,
    DROP COLUMN IF EXISTS risk_accepted_from_status,
    DROP COLUMN IF EXISTS risk_accepted_by,
    DROP COLUMN IF EXISTS risk_acceptance_reason,
    DROP COLUMN IF EXISTS risk_accepted_until,
    DROP COLUMN IF EXISTS risk_accepted_at;
