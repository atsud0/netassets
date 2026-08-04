-- +goose Up
CREATE TABLE inventory_asset_drafts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id uuid NOT NULL REFERENCES inventory_tasks(id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    operation varchar(16) NOT NULL DEFAULT 'create' CHECK (operation IN ('create', 'update', 'delete')),
    source_asset_id uuid REFERENCES assets(id) ON DELETE RESTRICT,
    asset_type varchar(24) NOT NULL CHECK (asset_type IN ('domain', 'ip', 'url', 'service', 'other')),
    asset_value text NOT NULL,
    normalized_value text NOT NULL,
    asset_name varchar(255),
    environment varchar(24) NOT NULL DEFAULT 'production' CHECK (environment IN ('production', 'staging', 'testing', 'development', 'office', 'other')),
    network_scope varchar(16) NOT NULL DEFAULT 'unknown' CHECK (network_scope IN ('public', 'private', 'unknown')),
    network_area_id uuid REFERENCES asset_network_areas(id) ON DELETE SET NULL,
    business_system_id uuid REFERENCES business_systems(id) ON DELETE SET NULL,
    status varchar(16) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'pending', 'disabled', 'archived')),
    importance varchar(16) NOT NULL DEFAULT 'normal' CHECK (importance IN ('normal', 'important', 'core')),
    owner_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    owner_name varchar(128),
    notes text,
    expires_at timestamptz,
    draft_status varchar(16) NOT NULL DEFAULT 'editing' CHECK (draft_status IN ('editing', 'submitted', 'returned', 'published', 'discarded')),
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    submitted_at timestamptz,
    published_at timestamptz,
    UNIQUE(task_id, organization_id, asset_type, normalized_value)
);
CREATE INDEX inventory_asset_drafts_assignment_idx
    ON inventory_asset_drafts(task_id, organization_id, draft_status, updated_at DESC);
CREATE INDEX inventory_asset_drafts_source_asset_idx
    ON inventory_asset_drafts(source_asset_id) WHERE source_asset_id IS NOT NULL;

-- +goose Down
DROP TABLE inventory_asset_drafts;
