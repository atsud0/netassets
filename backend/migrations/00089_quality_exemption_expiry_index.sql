-- +goose Up
-- The minute-level expiry worker must not scan all historical quality issues.
CREATE INDEX data_quality_issues_exemption_expiry_idx
    ON data_quality_issues(exemption_expires_at,id)
    WHERE status='exempted' AND exemption_expires_at IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS data_quality_issues_exemption_expiry_idx;
