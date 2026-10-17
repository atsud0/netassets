-- +goose Up
-- 00044 attempted to remove the original task-scoped UNIQUE constraints using
-- their pre-truncation names. PostgreSQL truncates identifiers to 63 bytes, so
-- the constraints survived on databases that had already applied 00044. They
-- incorrectly make a discarded draft block a later draft with the same task
-- and resource value, even though working-version uniqueness is now scoped to
-- active drafts only.
ALTER TABLE inventory_asset_drafts
    DROP CONSTRAINT IF EXISTS inventory_asset_drafts_task_id_organization_id_asset_type_n_key;
ALTER TABLE inventory_business_system_drafts
    DROP CONSTRAINT IF EXISTS inventory_business_system_dra_task_id_organization_id_syste_key;

-- +goose Down
ALTER TABLE inventory_asset_drafts
    ADD CONSTRAINT inventory_asset_drafts_task_id_organization_id_asset_type_n_key
    UNIQUE (task_id, organization_id, asset_type, normalized_value);
ALTER TABLE inventory_business_system_drafts
    ADD CONSTRAINT inventory_business_system_dra_task_id_organization_id_syste_key
    UNIQUE (task_id, organization_id, system_code);
