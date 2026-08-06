-- +goose Up
-- 将既有“任务草稿”扩展为组织日常工作版本。正式 assets/business_systems 表继续代表集团已发布台账。
ALTER TABLE inventory_asset_drafts ALTER COLUMN task_id DROP NOT NULL;
ALTER TABLE inventory_business_system_drafts ALTER COLUMN task_id DROP NOT NULL;

ALTER TABLE inventory_asset_drafts
    ADD COLUMN business_system_draft_id uuid REFERENCES inventory_business_system_drafts(id) ON DELETE SET NULL,
    ADD COLUMN custom_values jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN selected_at timestamptz;
ALTER TABLE inventory_business_system_drafts
    ADD COLUMN selected_at timestamptz;

ALTER TABLE inventory_asset_drafts
    DROP CONSTRAINT IF EXISTS inventory_asset_drafts_task_id_organization_id_asset_type_normalized_value_key;
ALTER TABLE inventory_business_system_drafts
    DROP CONSTRAINT IF EXISTS inventory_business_system_drafts_task_id_organization_id_system_code_key;
DROP INDEX IF EXISTS inventory_asset_drafts_task_source_unique;
DROP INDEX IF EXISTS inventory_business_system_drafts_task_source_unique;

-- 一个组织对同一已发布资源只能保留一个活动工作版本；已发布/放弃的历史版本不受限制。
CREATE UNIQUE INDEX inventory_asset_drafts_active_source_unique
    ON inventory_asset_drafts(organization_id, source_asset_id)
    WHERE source_asset_id IS NOT NULL AND draft_status IN ('editing','submitted','returned');
CREATE UNIQUE INDEX inventory_business_system_drafts_active_source_unique
    ON inventory_business_system_drafts(organization_id, source_system_id)
    WHERE source_system_id IS NOT NULL AND draft_status IN ('editing','submitted','returned');

-- 未上报的新资源在工作台账内同样执行唯一性约束。
CREATE UNIQUE INDEX inventory_asset_drafts_active_value_unique
    ON inventory_asset_drafts(asset_type, normalized_value)
    WHERE source_asset_id IS NULL AND draft_status IN ('editing','submitted','returned');
CREATE UNIQUE INDEX inventory_business_system_drafts_active_code_unique
    ON inventory_business_system_drafts(system_code)
    WHERE source_system_id IS NULL AND draft_status IN ('editing','submitted','returned');

CREATE INDEX inventory_asset_drafts_workspace_idx
    ON inventory_asset_drafts(organization_id, draft_status, updated_at DESC)
    WHERE draft_status IN ('editing','submitted','returned');
CREATE INDEX inventory_business_system_drafts_workspace_idx
    ON inventory_business_system_drafts(organization_id, draft_status, updated_at DESC)
    WHERE draft_status IN ('editing','submitted','returned');

COMMENT ON COLUMN inventory_asset_drafts.task_id IS '为空表示组织日常工作版本；选择纳入填报任务后绑定任务';
COMMENT ON COLUMN inventory_business_system_drafts.task_id IS '为空表示组织日常工作版本；选择纳入填报任务后绑定任务';
COMMENT ON COLUMN inventory_asset_drafts.business_system_draft_id IS '关联尚未发布的业务系统工作版本';

-- +goose Down
DROP INDEX IF EXISTS inventory_business_system_drafts_workspace_idx;
DROP INDEX IF EXISTS inventory_asset_drafts_workspace_idx;
DROP INDEX IF EXISTS inventory_business_system_drafts_active_code_unique;
DROP INDEX IF EXISTS inventory_asset_drafts_active_value_unique;
DROP INDEX IF EXISTS inventory_business_system_drafts_active_source_unique;
DROP INDEX IF EXISTS inventory_asset_drafts_active_source_unique;
ALTER TABLE inventory_business_system_drafts DROP COLUMN IF EXISTS selected_at;
ALTER TABLE inventory_asset_drafts DROP COLUMN IF EXISTS selected_at, DROP COLUMN IF EXISTS custom_values, DROP COLUMN IF EXISTS business_system_draft_id;
ALTER TABLE inventory_business_system_drafts ALTER COLUMN task_id SET NOT NULL;
ALTER TABLE inventory_asset_drafts ALTER COLUMN task_id SET NOT NULL;
ALTER TABLE inventory_asset_drafts ADD CONSTRAINT inventory_asset_drafts_task_id_organization_id_asset_type_normalized_value_key UNIQUE(task_id, organization_id, asset_type, normalized_value);
ALTER TABLE inventory_business_system_drafts ADD CONSTRAINT inventory_business_system_drafts_task_id_organization_id_system_code_key UNIQUE(task_id, organization_id, system_code);
CREATE UNIQUE INDEX inventory_asset_drafts_task_source_unique ON inventory_asset_drafts(task_id, organization_id, source_asset_id) WHERE source_asset_id IS NOT NULL;
CREATE UNIQUE INDEX inventory_business_system_drafts_task_source_unique ON inventory_business_system_drafts(task_id, organization_id, source_system_id) WHERE source_system_id IS NOT NULL;
