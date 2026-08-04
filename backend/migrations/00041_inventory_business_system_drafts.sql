-- +goose Up
CREATE TABLE inventory_business_system_drafts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES inventory_tasks(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    operation varchar(16) NOT NULL DEFAULT 'create' CHECK (operation IN ('create', 'update', 'delete')),
    source_system_id uuid REFERENCES business_systems(id) ON DELETE RESTRICT,
    system_code varchar(64) NOT NULL,
    system_name varchar(255) NOT NULL,
    short_name varchar(128),
    owner_name varchar(128),
    owner_contact varchar(255),
    system_status varchar(24) NOT NULL DEFAULT 'running' CHECK (system_status IN ('planning', 'running', 'suspended', 'offline', 'archived')),
    system_level varchar(24) NOT NULL DEFAULT 'normal' CHECK (system_level IN ('normal', 'important', 'core')),
    deployment_type varchar(64),
    internet_exposure varchar(16) NOT NULL DEFAULT 'unknown' CHECK (internet_exposure IN ('yes', 'no', 'partial', 'unknown')),
    description text,
    contains_personal_information varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_personal_information IN ('yes', 'no', 'unknown')),
    contains_sensitive_personal_information varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_sensitive_personal_information IN ('yes', 'no', 'unknown')),
    personal_information_record_count bigint CHECK (personal_information_record_count >= 0),
    data_volume_over_100k varchar(16) NOT NULL DEFAULT 'unknown' CHECK (data_volume_over_100k IN ('yes', 'no', 'unknown')),
    contains_important_data varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_important_data IN ('yes', 'no', 'unknown', 'pending')),
    contains_core_data varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_core_data IN ('yes', 'no', 'unknown', 'pending')),
    data_storage_location text,
    security_notes text,
    custom_values jsonb NOT NULL DEFAULT '[]'::jsonb,
    draft_status varchar(16) NOT NULL DEFAULT 'editing' CHECK (draft_status IN ('editing', 'submitted', 'returned', 'published', 'discarded')),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    submitted_at timestamptz,
    published_at timestamptz,
    UNIQUE(task_id, organization_id, system_code)
);
CREATE INDEX inventory_business_system_drafts_assignment_idx
    ON inventory_business_system_drafts(task_id, organization_id, draft_status, updated_at DESC);
CREATE INDEX inventory_business_system_drafts_source_idx
    ON inventory_business_system_drafts(source_system_id) WHERE source_system_id IS NOT NULL;

-- +goose Down
DROP TABLE inventory_business_system_drafts;
