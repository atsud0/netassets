-- +goose Up
ALTER TABLE assets
    ADD COLUMN expires_at timestamptz;

CREATE INDEX assets_expires_at_idx
    ON assets(expires_at)
    WHERE deleted_at IS NULL AND expires_at IS NOT NULL;

ALTER TABLE data_quality_rules
    DROP CONSTRAINT data_quality_rules_stale_after_days_check,
    DROP CONSTRAINT data_quality_rules_rule_type_check;

ALTER TABLE data_quality_rules
    ADD COLUMN expiry_warning_days integer,
    ADD CONSTRAINT data_quality_rules_rule_type_check
        CHECK (rule_type IN ('required', 'stale', 'expiry')),
    ADD CONSTRAINT data_quality_rules_threshold_check
        CHECK (
            (rule_type = 'required' AND stale_after_days IS NULL AND expiry_warning_days IS NULL)
            OR
            (rule_type = 'stale' AND target_field = 'updated_at' AND stale_after_days BETWEEN 1 AND 3650 AND expiry_warning_days IS NULL)
            OR
            (rule_type = 'expiry' AND resource_type = 'asset' AND target_field = 'expires_at' AND stale_after_days IS NULL AND expiry_warning_days BETWEEN 0 AND 3650)
        );

-- +goose Down
DELETE FROM data_quality_rules WHERE rule_type = 'expiry';

ALTER TABLE data_quality_rules
    DROP CONSTRAINT data_quality_rules_threshold_check,
    DROP CONSTRAINT data_quality_rules_rule_type_check,
    DROP COLUMN expiry_warning_days;

ALTER TABLE data_quality_rules
    ADD CONSTRAINT data_quality_rules_rule_type_check
        CHECK (rule_type IN ('required', 'stale')),
    ADD CONSTRAINT data_quality_rules_stale_after_days_check
        CHECK (
            (rule_type = 'required' AND stale_after_days IS NULL)
            OR
            (rule_type = 'stale' AND target_field = 'updated_at' AND stale_after_days BETWEEN 1 AND 3650)
        );

DROP INDEX IF EXISTS assets_expires_at_idx;
ALTER TABLE assets DROP COLUMN expires_at;
