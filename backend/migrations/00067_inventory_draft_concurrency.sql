-- +goose Up
-- 草稿是可恢复的受控工作对象：版本用于 CAS，分配和接管字段用于明确编辑责任。
ALTER TABLE inventory_asset_drafts
    ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS assigned_to uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS last_edited_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS takeover_reason text,
    ADD COLUMN IF NOT EXISTS taken_over_at timestamptz;

ALTER TABLE inventory_business_system_drafts
    ADD COLUMN IF NOT EXISTS version integer NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS assigned_to uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS last_edited_by uuid REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS takeover_reason text,
    ADD COLUMN IF NOT EXISTS taken_over_at timestamptz;

-- 既有草稿的创建/最后修改人作为初始责任人，避免历史数据突然变成无人可编辑。
UPDATE inventory_asset_drafts
SET assigned_to = COALESCE(assigned_to, created_by),
    last_edited_by = COALESCE(last_edited_by, updated_by, created_by)
WHERE assigned_to IS NULL OR last_edited_by IS NULL;
UPDATE inventory_business_system_drafts
SET assigned_to = COALESCE(assigned_to, created_by),
    last_edited_by = COALESCE(last_edited_by, updated_by, created_by)
WHERE assigned_to IS NULL OR last_edited_by IS NULL;

CREATE INDEX IF NOT EXISTS inventory_asset_drafts_assignee_idx
    ON inventory_asset_drafts(organization_id, assigned_to, draft_status, updated_at DESC)
    WHERE draft_status IN ('editing', 'returned');
CREATE INDEX IF NOT EXISTS inventory_business_system_drafts_assignee_idx
    ON inventory_business_system_drafts(organization_id, assigned_to, draft_status, updated_at DESC)
    WHERE draft_status IN ('editing', 'returned');

COMMENT ON COLUMN inventory_asset_drafts.version IS '草稿乐观锁版本；每次内容或责任人变更递增';
COMMENT ON COLUMN inventory_business_system_drafts.version IS '草稿乐观锁版本；每次内容或责任人变更递增';
COMMENT ON COLUMN inventory_asset_drafts.assigned_to IS '当前明确负责编辑该草稿的用户';
COMMENT ON COLUMN inventory_business_system_drafts.assigned_to IS '当前明确负责编辑该草稿的用户';

-- +goose Down
DROP INDEX IF EXISTS inventory_business_system_drafts_assignee_idx;
DROP INDEX IF EXISTS inventory_asset_drafts_assignee_idx;
ALTER TABLE inventory_business_system_drafts
    DROP COLUMN IF EXISTS taken_over_at,
    DROP COLUMN IF EXISTS takeover_reason,
    DROP COLUMN IF EXISTS last_edited_by,
    DROP COLUMN IF EXISTS assigned_to,
    DROP COLUMN IF EXISTS version;
ALTER TABLE inventory_asset_drafts
    DROP COLUMN IF EXISTS taken_over_at,
    DROP COLUMN IF EXISTS takeover_reason,
    DROP COLUMN IF EXISTS last_edited_by,
    DROP COLUMN IF EXISTS assigned_to,
    DROP COLUMN IF EXISTS version;
