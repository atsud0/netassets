-- +goose Up
ALTER TABLE inventory_tasks
    ADD COLUMN asset_network_scope varchar(16) NOT NULL DEFAULT 'all'
    CHECK (asset_network_scope IN ('all', 'public', 'private'));

COMMENT ON COLUMN inventory_tasks.asset_network_scope IS
    'Network asset scope for this task: all, public, or private. Unknown assets are included only when scope is all.';

-- +goose Down
ALTER TABLE inventory_tasks DROP COLUMN IF EXISTS asset_network_scope;
