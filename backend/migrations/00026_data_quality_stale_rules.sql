-- +goose Up
ALTER TABLE data_quality_rules
    DROP CONSTRAINT data_quality_rules_rule_type_check;

ALTER TABLE data_quality_rules
    ADD COLUMN stale_after_days integer,
    ADD CONSTRAINT data_quality_rules_rule_type_check
        CHECK (rule_type IN ('required', 'stale')),
    ADD CONSTRAINT data_quality_rules_stale_after_days_check
        CHECK (
            (rule_type = 'required' AND stale_after_days IS NULL)
            OR
            (rule_type = 'stale' AND target_field = 'updated_at' AND stale_after_days BETWEEN 1 AND 3650)
        );

-- +goose Down
DELETE FROM data_quality_rules WHERE rule_type = 'stale';

ALTER TABLE data_quality_rules
    DROP CONSTRAINT data_quality_rules_stale_after_days_check,
    DROP COLUMN stale_after_days,
    DROP CONSTRAINT data_quality_rules_rule_type_check;

ALTER TABLE data_quality_rules
    ADD CONSTRAINT data_quality_rules_rule_type_check
        CHECK (rule_type IN ('required'));
