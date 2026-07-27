-- +goose Up
CREATE TABLE asset_network_areas (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    area_name varchar(128) NOT NULL,
    area_code varchar(64),
    status varchar(16) NOT NULL DEFAULT 'enabled' CHECK (status IN ('enabled', 'disabled')),
    sort_order integer NOT NULL DEFAULT 0,
    notes text,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, area_name)
);
CREATE INDEX asset_network_areas_org_status_idx
    ON asset_network_areas(organization_id, status, sort_order, area_name);

ALTER TABLE assets
    ADD COLUMN network_area_id uuid REFERENCES asset_network_areas(id) ON DELETE SET NULL;
CREATE INDEX assets_network_area_idx ON assets(network_area_id) WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS assets_network_area_idx;
ALTER TABLE assets DROP COLUMN IF EXISTS network_area_id;
DROP TABLE IF EXISTS asset_network_areas;
