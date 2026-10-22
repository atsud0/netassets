-- +goose Up
-- Bind asset change drafts to the exact formal version they were based on.
-- NULL remains valid for create drafts and historical drafts; publication rejects
-- historical update/delete drafts conservatively rather than overwriting silently.
ALTER TABLE inventory_asset_drafts
    ADD COLUMN IF NOT EXISTS source_asset_version integer;

COMMENT ON COLUMN inventory_asset_drafts.source_asset_version IS
    '正式资产变更工作版本的来源版本；发布时用于防止覆盖创建草稿后的正式资产修改';

-- +goose Down
ALTER TABLE inventory_asset_drafts DROP COLUMN IF EXISTS source_asset_version;
