-- +goose Up
CREATE TABLE data_quality_rules (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    rule_name varchar(128) NOT NULL,
    description text,
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('asset', 'business_system')),
    rule_type varchar(32) NOT NULL DEFAULT 'required' CHECK (rule_type IN ('required')),
    target_field varchar(128) NOT NULL,
    severity varchar(16) NOT NULL CHECK (severity IN ('error', 'warning')),
    is_enabled boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX data_quality_rules_unique_target_idx ON data_quality_rules(resource_type, rule_type, target_field);
CREATE INDEX data_quality_rules_enabled_idx ON data_quality_rules(is_enabled, resource_type, sort_order);
-- +goose Down
DROP TABLE IF EXISTS data_quality_rules;
