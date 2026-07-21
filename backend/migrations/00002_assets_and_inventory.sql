-- +goose Up
CREATE TABLE business_systems (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    system_code varchar(64) NOT NULL UNIQUE,
    system_name varchar(255) NOT NULL,
    short_name varchar(128),
    owner_organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    department_organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    owner_name varchar(128),
    owner_contact varchar(255),
    system_status varchar(24) NOT NULL CHECK (system_status IN ('planning', 'running', 'suspended', 'offline', 'archived')),
    system_level varchar(16) NOT NULL DEFAULT 'normal' CHECK (system_level IN ('normal', 'important', 'core')),
    deployment_type varchar(32),
    internet_exposure varchar(16) NOT NULL DEFAULT 'unknown' CHECK (internet_exposure IN ('yes', 'no', 'partial', 'unknown')),
    vendor varchar(255),
    description text,
    online_at date,
    offline_at date,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);
CREATE INDEX business_systems_org_idx ON business_systems(owner_organization_id, system_status) WHERE deleted_at IS NULL;
CREATE INDEX business_systems_owner_idx ON business_systems(owner_user_id) WHERE deleted_at IS NULL;

CREATE TABLE business_system_data_security_profiles (
    business_system_id uuid PRIMARY KEY REFERENCES business_systems(id) ON DELETE CASCADE,
    contains_personal_information varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_personal_information IN ('yes', 'no', 'unknown')),
    contains_sensitive_personal_information varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_sensitive_personal_information IN ('yes', 'no', 'unknown')),
    personal_information_types jsonb NOT NULL DEFAULT '[]'::jsonb,
    contains_minor_information varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_minor_information IN ('yes', 'no', 'unknown')),
    personal_information_subject_count bigint CHECK (personal_information_subject_count >= 0),
    personal_information_record_count bigint CHECK (personal_information_record_count >= 0),
    personal_information_volume_range varchar(32),
    data_statistical_scope text,
    data_baseline_date date,
    data_is_estimated boolean NOT NULL DEFAULT false,
    contains_important_data varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_important_data IN ('yes', 'no', 'unknown', 'pending')),
    contains_core_data varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_core_data IN ('yes', 'no', 'unknown', 'pending')),
    data_sensitivity_level varchar(32),
    contains_trade_secrets varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_trade_secrets IN ('yes', 'no', 'unknown')),
    contains_customer_data varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_customer_data IN ('yes', 'no', 'unknown')),
    contains_employee_data varchar(16) NOT NULL DEFAULT 'unknown' CHECK (contains_employee_data IN ('yes', 'no', 'unknown')),
    shares_data_with_third_party varchar(16) NOT NULL DEFAULT 'unknown' CHECK (shares_data_with_third_party IN ('yes', 'no', 'unknown')),
    cross_border_transfer varchar(16) NOT NULL DEFAULT 'unknown' CHECK (cross_border_transfer IN ('yes', 'no', 'unknown')),
    data_storage_location text,
    data_retention_period varchar(128),
    classification_status varchar(24) NOT NULL DEFAULT 'not_started' CHECK (classification_status IN ('completed', 'in_progress', 'not_started')),
    reported_by uuid REFERENCES users(id) ON DELETE SET NULL,
    confirmed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    confirmed_at timestamptz,
    notes text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX system_data_personal_idx ON business_system_data_security_profiles(contains_personal_information, contains_sensitive_personal_information);
CREATE INDEX system_data_record_count_idx ON business_system_data_security_profiles(personal_information_record_count);

CREATE TABLE assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_type varchar(24) NOT NULL CHECK (asset_type IN ('domain', 'ip', 'url', 'service', 'other')),
    asset_value text NOT NULL,
    normalized_value text NOT NULL,
    asset_name varchar(255),
    environment varchar(24) NOT NULL DEFAULT 'production' CHECK (environment IN ('production', 'staging', 'testing', 'development', 'office', 'other')),
    network_scope varchar(16) NOT NULL DEFAULT 'unknown' CHECK (network_scope IN ('public', 'private', 'unknown')),
    status varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'disabled', 'archived')),
    importance varchar(16) NOT NULL DEFAULT 'normal' CHECK (importance IN ('normal', 'important', 'core')),
    source varchar(16) NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'import', 'api', 'plugin')),
    notes text,
    extra_data jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    first_seen_at timestamptz,
    last_seen_at timestamptz,
    deleted_at timestamptz
);
CREATE UNIQUE INDEX assets_active_unique_idx ON assets(asset_type, normalized_value) WHERE deleted_at IS NULL;
CREATE INDEX assets_type_status_idx ON assets(asset_type, status) WHERE deleted_at IS NULL;
CREATE INDEX assets_value_search_idx ON assets USING gin (to_tsvector('simple', normalized_value));

CREATE TABLE asset_organizations (
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    relationship_type varchar(16) NOT NULL CHECK (relationship_type IN ('owner', 'user', 'operator', 'shared', 'unknown')),
    is_primary boolean NOT NULL DEFAULT false,
    owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    owner_name varchar(128),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, organization_id, relationship_type)
);
CREATE UNIQUE INDEX asset_primary_owner_idx ON asset_organizations(asset_id) WHERE is_primary;
CREATE INDEX asset_organizations_org_idx ON asset_organizations(organization_id, relationship_type);

CREATE TABLE system_assets (
    business_system_id uuid NOT NULL REFERENCES business_systems(id) ON DELETE CASCADE,
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    relationship_type varchar(24) NOT NULL DEFAULT 'uses',
    is_primary boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (business_system_id, asset_id, relationship_type)
);
CREATE INDEX system_assets_asset_idx ON system_assets(asset_id);

CREATE TABLE tags (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid REFERENCES organizations(id) ON DELETE CASCADE,
    tag_name varchar(64) NOT NULL,
    color varchar(16),
    description text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, tag_name)
);

CREATE TABLE asset_tags (
    asset_id uuid NOT NULL REFERENCES assets(id) ON DELETE CASCADE,
    tag_id uuid NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (asset_id, tag_id)
);

CREATE TABLE inventory_tasks (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_name varchar(255) NOT NULL,
    description text,
    start_at timestamptz NOT NULL,
    deadline_at timestamptz NOT NULL,
    status varchar(16) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'active', 'closed', 'completed', 'archived')),
    required_fields jsonb NOT NULL DEFAULT '[]'::jsonb,
    quality_rules jsonb NOT NULL DEFAULT '[]'::jsonb,
    require_review boolean NOT NULL DEFAULT true,
    allow_late_submission boolean NOT NULL DEFAULT false,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (deadline_at > start_at)
);

CREATE TABLE inventory_task_organizations (
    task_id uuid NOT NULL REFERENCES inventory_tasks(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    status varchar(24) NOT NULL DEFAULT 'not_started' CHECK (status IN ('not_started', 'in_progress', 'submitted', 'returned', 'approved', 'overdue')),
    submitted_by uuid REFERENCES users(id) ON DELETE SET NULL,
    submitted_at timestamptz,
    reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    return_reason text,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, organization_id)
);
CREATE INDEX inventory_task_org_status_idx ON inventory_task_organizations(organization_id, status);

CREATE TABLE inventory_review_comments (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES inventory_tasks(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    resource_type varchar(32) NOT NULL,
    resource_id uuid NOT NULL,
    comment text NOT NULL,
    status varchar(16) NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'resolved')),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    resolved_by uuid REFERENCES users(id) ON DELETE SET NULL,
    resolved_at timestamptz
);

CREATE TABLE asset_candidates (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_type varchar(24) NOT NULL,
    asset_value text NOT NULL,
    normalized_value text NOT NULL,
    suggested_data jsonb NOT NULL DEFAULT '{}'::jsonb,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    source varchar(32) NOT NULL,
    source_plugin varchar(128),
    source_reference varchar(255),
    status varchar(16) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'ignored', 'duplicate', 'invalid')),
    matched_asset_id uuid REFERENCES assets(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz
);
CREATE INDEX asset_candidates_org_status_idx ON asset_candidates(organization_id, status, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS asset_candidates;
DROP TABLE IF EXISTS inventory_review_comments;
DROP TABLE IF EXISTS inventory_task_organizations;
DROP TABLE IF EXISTS inventory_tasks;
DROP TABLE IF EXISTS asset_tags;
DROP TABLE IF EXISTS tags;
DROP TABLE IF EXISTS system_assets;
DROP TABLE IF EXISTS asset_organizations;
DROP TABLE IF EXISTS assets;
DROP TABLE IF EXISTS business_system_data_security_profiles;
DROP TABLE IF EXISTS business_systems;
