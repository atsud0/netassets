-- +goose Up
ALTER TABLE inventory_asset_drafts
    ADD COLUMN source varchar(16) NOT NULL DEFAULT 'manual'
    CHECK (source IN ('manual', 'import', 'api', 'plugin'));

COMMENT ON COLUMN inventory_asset_drafts.source IS '组织网络资产工作版本来源；导入生成的工作版本为 import';

-- +goose Down
ALTER TABLE inventory_asset_drafts DROP COLUMN IF EXISTS source;
