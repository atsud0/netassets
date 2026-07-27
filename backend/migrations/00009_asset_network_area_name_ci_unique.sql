-- +goose Up
ALTER TABLE asset_network_areas
    DROP CONSTRAINT IF EXISTS asset_network_areas_organization_id_area_name_key;

CREATE UNIQUE INDEX asset_network_areas_org_name_lower_unique_idx
    ON asset_network_areas (organization_id, lower(area_name));

-- +goose Down
DROP INDEX IF EXISTS asset_network_areas_org_name_lower_unique_idx;

ALTER TABLE asset_network_areas
    ADD CONSTRAINT asset_network_areas_organization_id_area_name_key
    UNIQUE (organization_id, area_name);
