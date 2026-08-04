-- +goose Up
CREATE UNIQUE INDEX inventory_asset_drafts_task_source_unique
    ON inventory_asset_drafts(task_id, organization_id, source_asset_id)
    WHERE source_asset_id IS NOT NULL;

CREATE UNIQUE INDEX inventory_business_system_drafts_task_source_unique
    ON inventory_business_system_drafts(task_id, organization_id, source_system_id)
    WHERE source_system_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS inventory_business_system_drafts_task_source_unique;
DROP INDEX IF EXISTS inventory_asset_drafts_task_source_unique;
