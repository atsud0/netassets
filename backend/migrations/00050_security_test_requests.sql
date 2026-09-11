-- +goose Up
CREATE TABLE security_test_requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_code varchar(64) NOT NULL UNIQUE,
    applicant_organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    request_name varchar(255) NOT NULL,
    business_system_id uuid REFERENCES business_systems(id) ON DELETE SET NULL,
    business_system_draft_id uuid REFERENCES inventory_business_system_drafts(id) ON DELETE SET NULL,
    business_system_code_snapshot varchar(64),
    business_system_name_snapshot varchar(255),
    test_type varchar(24) NOT NULL CHECK (test_type IN ('penetration', 'manual', 'scan', 'retest', 'other')),
    expected_launch_at timestamptz NOT NULL,
    test_window_start_at timestamptz,
    test_window_end_at timestamptz,
    contact_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    background text NOT NULL,
    status varchar(24) NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'submitted', 'rejected', 'testing', 'pending_launch', 'cancelled', 'closed')),
    reviewed_by uuid REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at timestamptz,
    review_comment text,
    converted_batch_id uuid REFERENCES security_test_batches(id) ON DELETE SET NULL,
    launch_decided_by uuid REFERENCES users(id) ON DELETE SET NULL,
    launch_decided_at timestamptz,
    launch_comment text,
    reported_task_id uuid REFERENCES inventory_tasks(id) ON DELETE SET NULL,
    created_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_test_requests_status_created_idx ON security_test_requests(status, created_at DESC);
CREATE INDEX security_test_requests_applicant_idx ON security_test_requests(applicant_organization_id, status);

COMMENT ON TABLE security_test_requests IS '子单位上线安全测试申请';
COMMENT ON COLUMN security_test_requests.business_system_draft_id IS '未上报业务系统工作版本；business_system_id 为空时以该版本为准';

CREATE TABLE security_test_request_scope_assets (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL REFERENCES security_test_requests(id) ON DELETE CASCADE,
    published_asset_id uuid REFERENCES assets(id) ON DELETE SET NULL,
    working_asset_id uuid REFERENCES inventory_asset_drafts(id) ON DELETE SET NULL,
    asset_type_snapshot varchar(24) NOT NULL,
    asset_value_snapshot text NOT NULL,
    normalized_value_snapshot text NOT NULL,
    asset_name_snapshot varchar(255),
    organization_id uuid REFERENCES organizations(id) ON DELETE SET NULL,
    organization_code_snapshot varchar(64) NOT NULL,
    organization_name_snapshot varchar(255) NOT NULL,
    business_system_id uuid REFERENCES business_systems(id) ON DELETE SET NULL,
    business_system_code_snapshot varchar(64),
    business_system_name_snapshot varchar(255),
    network_scope_snapshot varchar(16) NOT NULL CHECK (network_scope_snapshot IN ('public', 'private', 'unknown')),
    network_area_name_snapshot varchar(128),
    environment_snapshot varchar(24) NOT NULL,
    importance_snapshot varchar(16) NOT NULL,
    report_action varchar(16) NOT NULL CHECK (report_action IN ('create', 'update', 'delete', 'none')),
    snapshot_at timestamptz NOT NULL DEFAULT now(),
    included_by uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((published_asset_id IS NOT NULL)::int + (working_asset_id IS NOT NULL)::int = 1),
    UNIQUE (request_id, published_asset_id),
    UNIQUE (request_id, working_asset_id)
);
CREATE INDEX security_test_request_scope_request_idx ON security_test_request_scope_assets(request_id, organization_id, asset_type_snapshot);

COMMENT ON TABLE security_test_request_scope_assets IS '申请提交时冻结的公网资产范围快照，支持已发布资产与未上报工作版本';
COMMENT ON COLUMN security_test_request_scope_assets.report_action IS '批准上线时该资源的自动上报动作：create/update/delete 对应工作版本操作，none 表示已发布且无变更';

CREATE TABLE security_test_request_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    request_id uuid NOT NULL REFERENCES security_test_requests(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action varchar(64) NOT NULL,
    from_status varchar(24),
    to_status varchar(24),
    comment text,
    details jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX security_test_request_events_request_created_idx ON security_test_request_events(request_id, created_at, id);

ALTER TABLE security_test_batches
    ADD COLUMN source_request_id uuid REFERENCES security_test_requests(id) ON DELETE SET NULL;
CREATE INDEX security_test_batches_source_request_idx ON security_test_batches(source_request_id) WHERE source_request_id IS NOT NULL;

COMMENT ON COLUMN security_test_batches.source_request_id IS '子单位上线测试申请转入的来源申请；集团直接创建的批次为空';

ALTER TABLE inventory_tasks
    ADD COLUMN task_type varchar(24) NOT NULL DEFAULT 'standard' CHECK (task_type IN ('standard', 'launch_release'));
COMMENT ON COLUMN inventory_tasks.task_type IS '任务类型：standard 常规填报；launch_release 上线发布（测试申请批准上线后自动生成）';

ALTER TABLE security_test_scope_assets
    ADD COLUMN working_version_id uuid REFERENCES inventory_asset_drafts(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX security_test_scope_assets_batch_working_idx ON security_test_scope_assets(batch_id, working_version_id) WHERE working_version_id IS NOT NULL;

COMMENT ON COLUMN security_test_scope_assets.working_version_id IS '申请转入批次中的未上报资产工作版本；该类范围行 asset_id 为空，仅保留快照';

INSERT INTO permissions (permission_key, permission_name)
VALUES ('security_test.request', '发起上线安全测试申请')
ON CONFLICT (permission_key) DO UPDATE SET permission_name = EXCLUDED.permission_name;

INSERT INTO role_permissions (role_id, permission_key)
SELECT r.id, permission.permission_key
FROM roles r
CROSS JOIN unnest(ARRAY['security_test.request']::varchar[]) AS permission(permission_key)
WHERE r.role_key IN ('organization_admin', 'organization_editor')
ON CONFLICT DO NOTHING;

-- +goose Down
DELETE FROM role_permissions rp
USING roles r
WHERE rp.role_id = r.id
  AND r.role_key IN ('organization_admin', 'organization_editor')
  AND rp.permission_key = 'security_test.request';

DELETE FROM permissions WHERE permission_key = 'security_test.request';

DROP INDEX IF EXISTS security_test_scope_assets_batch_working_idx;
ALTER TABLE security_test_scope_assets DROP COLUMN IF EXISTS working_version_id;

DROP INDEX IF EXISTS security_test_batches_source_request_idx;
ALTER TABLE security_test_batches DROP COLUMN IF EXISTS source_request_id;

ALTER TABLE inventory_tasks DROP COLUMN IF EXISTS task_type;

DROP TABLE IF EXISTS security_test_request_events;
DROP TABLE IF EXISTS security_test_request_scope_assets;
DROP TABLE IF EXISTS security_test_requests;