-- +goose Up
CREATE TABLE custom_field_definitions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('asset', 'business_system')),
    field_key varchar(64) NOT NULL,
    field_name varchar(128) NOT NULL,
    description text,
    field_type varchar(24) NOT NULL CHECK (field_type IN ('text', 'textarea', 'number', 'date', 'select', 'multiselect', 'boolean')),
    options jsonb NOT NULL DEFAULT '[]'::jsonb,
    is_required boolean NOT NULL DEFAULT false,
    is_enabled boolean NOT NULL DEFAULT true,
    sort_order integer NOT NULL DEFAULT 0,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(resource_type, field_key)
);
CREATE INDEX custom_field_definitions_resource_idx ON custom_field_definitions(resource_type, is_enabled, sort_order);
CREATE TABLE custom_field_values (
    resource_type varchar(32) NOT NULL CHECK (resource_type IN ('asset', 'business_system')),
    resource_id uuid NOT NULL,
    field_definition_id uuid NOT NULL REFERENCES custom_field_definitions(id) ON DELETE CASCADE,
    value jsonb NOT NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(resource_id, field_definition_id)
);
CREATE INDEX custom_field_values_resource_idx ON custom_field_values(resource_type, resource_id);
-- +goose Down
DROP TABLE IF EXISTS custom_field_values;
DROP TABLE IF EXISTS custom_field_definitions;
