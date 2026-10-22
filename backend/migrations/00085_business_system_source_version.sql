-- +goose Up
-- A business-system working version must be tied to the formal row revision it
-- was created from. Otherwise an old submission can overwrite a newer direct
-- edit made by a group administrator.
ALTER TABLE business_systems
    ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1;
ALTER TABLE inventory_business_system_drafts
    ADD COLUMN IF NOT EXISTS source_system_version integer;

COMMENT ON COLUMN business_systems.version IS
    '正式业务系统乐观锁版本；正式记录每次修改、归档、恢复或删除递增';
COMMENT ON COLUMN inventory_business_system_drafts.source_system_version IS
    '业务系统变更工作版本的来源正式版本；发布时用于防止覆盖并发修改';

-- +goose Down
ALTER TABLE inventory_business_system_drafts DROP COLUMN IF EXISTS source_system_version;
ALTER TABLE business_systems DROP COLUMN IF EXISTS version;
