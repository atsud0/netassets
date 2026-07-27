-- +goose Up
CREATE TABLE security_test_batches (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_code varchar(64) NOT NULL UNIQUE,
    batch_name varchar(255) NOT NULL,
    test_type varchar(24) NOT NULL CHECK (test_type IN ('penetration', 'manual', 'scan', 'retest', 'other')),
    status varchar(24) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'authorized', 'in_progress', 'completed', 'cancelled')),
    provider varchar(255) NOT NULL,
    authorization_note text NOT NULL,
    authorized_by uuid REFERENCES users(id) ON DELETE SET NULL,
    authorized_at timestamptz,
    start_at timestamptz NOT NULL,
    end_at timestamptz NOT NULL,
    started_at timestamptz,
    completed_at timestamptz,
    cancelled_at timestamptz,
    notes text,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (end_at > start_at)
);
CREATE INDEX security_test_batches_status_time_idx ON security_test_batches(status, start_at DESC);

CREATE TABLE security_test_scope_assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id uuid NOT NULL REFERENCES security_test_batches(id) ON DELETE CASCADE,
    asset_id uuid REFERENCES assets(id) ON DELETE SET NULL,
    asset_type_snapshot varchar(24) NOT NULL,
    asset_value_snapshot text NOT NULL,
    asset_name_snapshot varchar(255),
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    organization_code_snapshot varchar(64) NOT NULL,
    organization_name_snapshot varchar(255) NOT NULL,
    business_system_id uuid REFERENCES business_systems(id) ON DELETE SET NULL,
    business_system_code_snapshot varchar(64),
    business_system_name_snapshot varchar(255),
    network_scope_snapshot varchar(16) NOT NULL,
    network_area_name_snapshot varchar(128),
    environment_snapshot varchar(24) NOT NULL,
    importance_snapshot varchar(16) NOT NULL,
    snapshot_at timestamptz NOT NULL DEFAULT now(),
    included_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (batch_id, asset_id)
);
CREATE INDEX security_test_scope_assets_batch_idx ON security_test_scope_assets(batch_id, organization_id, asset_type_snapshot);
CREATE INDEX security_test_scope_assets_asset_idx ON security_test_scope_assets(asset_id) WHERE asset_id IS NOT NULL;

CREATE TABLE security_test_batch_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    batch_id uuid NOT NULL REFERENCES security_test_batches(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action varchar(64) NOT NULL,
    from_status varchar(24),
    to_status varchar(24),
    comment text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_test_batch_events_batch_created_idx ON security_test_batch_events(batch_id, created_at, id);

ALTER TABLE security_findings
    ADD COLUMN test_batch_id uuid REFERENCES security_test_batches(id) ON DELETE SET NULL;
CREATE INDEX security_findings_test_batch_idx ON security_findings(test_batch_id) WHERE test_batch_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS security_findings_test_batch_idx;
ALTER TABLE security_findings DROP COLUMN IF EXISTS test_batch_id;
DROP TABLE IF EXISTS security_test_batch_events;
DROP TABLE IF EXISTS security_test_scope_assets;
DROP TABLE IF EXISTS security_test_batches;
