-- +goose Up
ALTER TABLE data_quality_rules
    ADD COLUMN conditions jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD CONSTRAINT data_quality_rules_conditions_array_check
        CHECK (jsonb_typeof(conditions) = 'array');

DROP INDEX IF EXISTS data_quality_rules_unique_target_idx;
CREATE UNIQUE INDEX data_quality_rules_unique_target_conditions_idx
    ON data_quality_rules(resource_type, rule_type, target_field, conditions);
CREATE INDEX data_quality_rules_resource_target_idx
    ON data_quality_rules(resource_type, rule_type, target_field);

-- +goose Down
-- Conditional variants cannot be represented by the previous schema. Keep the
-- unconditional variant, if present, and remove conditional-only variants
-- before restoring the original uniqueness contract.
DELETE FROM data_quality_rules WHERE conditions <> '[]'::jsonb;

DROP INDEX IF EXISTS data_quality_rules_resource_target_idx;
DROP INDEX IF EXISTS data_quality_rules_unique_target_conditions_idx;
ALTER TABLE data_quality_rules
    DROP CONSTRAINT IF EXISTS data_quality_rules_conditions_array_check,
    DROP COLUMN IF EXISTS conditions;
CREATE UNIQUE INDEX data_quality_rules_unique_target_idx
    ON data_quality_rules(resource_type, rule_type, target_field);
