\set ON_ERROR_STOP on

BEGIN;

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM users WHERE username = 'admin' AND deleted_at IS NULL) THEN
    RAISE EXCEPTION '未找到 admin 用户，无法复用当前管理员密码创建演示账号';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM organizations WHERE organization_type = 'group' AND deleted_at IS NULL) THEN
    RAISE EXCEPTION '未找到启用中的集团根组织';
  END IF;
END $$;

-- 组织树：3 家启用子公司、3 个下级机构、1 家停用子公司。
INSERT INTO organizations (
  id, parent_id, organization_code, organization_name, short_name,
  organization_type, manager, contact, status, notes, created_at, updated_at
)
VALUES
  (md5('netassets-demo:org:east')::uuid, (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1), 'DEMO_EAST', '华东科技有限公司（演示）', '华东科技', 'subsidiary', '张华', '021-55550001', 'enabled', '[演示数据] 华东区域子公司', now()-interval '360 days', now()-interval '2 days'),
  (md5('netassets-demo:org:east-sh')::uuid, md5('netassets-demo:org:east')::uuid, 'DEMO_EAST_SH', '上海办事处（演示）', '上海办事处', 'department', '李敏', '021-55550002', 'enabled', '[演示数据] 华东科技下级机构', now()-interval '300 days', now()-interval '5 days'),
  (md5('netassets-demo:org:east-hz')::uuid, md5('netassets-demo:org:east')::uuid, 'DEMO_EAST_HZ', '杭州研发中心（演示）', '杭州研发', 'department', '王强', '0571-55550003', 'enabled', '[演示数据] 华东科技下级机构', now()-interval '260 days', now()-interval '8 days'),
  (md5('netassets-demo:org:finance')::uuid, (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1), 'DEMO_FIN', '集团金融服务有限公司（演示）', '金融服务', 'subsidiary', '赵磊', '010-55551001', 'enabled', '[演示数据] 金融板块子公司', now()-interval '340 days', now()-interval '1 day'),
  (md5('netassets-demo:org:finance-bj')::uuid, md5('netassets-demo:org:finance')::uuid, 'DEMO_FIN_BJ', '北京运营中心（演示）', '北京运营', 'department', '孙悦', '010-55551002', 'enabled', '[演示数据] 金融服务下级机构', now()-interval '220 days', now()-interval '6 days'),
  (md5('netassets-demo:org:cloud')::uuid, (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1), 'DEMO_CLOUD', '集团云计算有限公司（演示）', '云计算', 'subsidiary', '周云', '0755-55552001', 'enabled', '[演示数据] 云计算子公司', now()-interval '180 days', now()-interval '3 days'),
  (md5('netassets-demo:org:disabled')::uuid, (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1), 'DEMO_DISABLED', '已停用子公司（演示）', '停用公司', 'subsidiary', '陈旧', '020-55553001', 'disabled', '[演示数据] 用于测试组织恢复和启用', now()-interval '500 days', now()-interval '30 days');

-- 自定义只读审计角色，用于验证角色管理。
INSERT INTO roles (id, role_key, role_name, role_scope, description, is_system, status)
VALUES (md5('netassets-demo:role:auditor')::uuid, 'demo_group_auditor', '集团只读审计员（演示）', 'group', '[演示数据] 仅查看组织和审计日志', false, 'enabled');
INSERT INTO role_permissions (role_id, permission_key)
SELECT md5('netassets-demo:role:auditor')::uuid, permission_key
FROM permissions WHERE permission_key IN ('organization.read', 'audit.read');

-- 所有演示账号复用 admin 当前密码哈希，因此默认密码与 admin 当前密码相同。
WITH admin_password AS (
  SELECT password_hash FROM users WHERE username='admin' AND deleted_at IS NULL LIMIT 1
), demo_users(username, display_name, email, mobile_phone, office_phone) AS (
  VALUES
    ('demo_secops', '集团安全运营员（演示）', 'demo_secops@example.com', '13800001001', '010-55550101'),
    ('demo_auditor', '集团审计员（演示）', 'demo_auditor@example.com', '13800001002', '010-55550102'),
    ('demo_east_admin', '华东子公司管理员（演示）', 'east_admin@example.com', '13800002001', '021-55550201'),
    ('demo_east_editor', '华东资产填报员（演示）', 'east_editor@example.com', '13800002002', '021-55550202'),
    ('demo_fin_admin', '金融子公司管理员（演示）', 'fin_admin@example.com', '13800003001', '010-55550301'),
    ('demo_cloud_editor', '云计算资产填报员（演示）', 'cloud_editor@example.com', '13800004001', '0755-55550401'),
    ('demo_multi_editor', '跨组织填报员（演示）', 'multi_editor@example.com', '13800005001', '400-55550501')
)
INSERT INTO users (
  id, username, password_hash, display_name, email, mobile_phone, office_phone,
  status, must_change_password, password_changed_at, created_at, updated_at
)
SELECT md5('netassets-demo:user:' || username)::uuid, username, admin_password.password_hash,
       display_name, email, mobile_phone, office_phone, 'enabled', false,
       now()-interval '15 days', now()-interval '90 days', now()-interval '2 days'
FROM demo_users CROSS JOIN admin_password;

INSERT INTO user_organizations (user_id, organization_id, is_primary)
VALUES
  (md5('netassets-demo:user:demo_secops')::uuid, (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1), true),
  (md5('netassets-demo:user:demo_auditor')::uuid, (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1), true),
  (md5('netassets-demo:user:demo_east_admin')::uuid, md5('netassets-demo:org:east')::uuid, true),
  (md5('netassets-demo:user:demo_east_editor')::uuid, md5('netassets-demo:org:east')::uuid, true),
  (md5('netassets-demo:user:demo_fin_admin')::uuid, md5('netassets-demo:org:finance')::uuid, true),
  (md5('netassets-demo:user:demo_cloud_editor')::uuid, md5('netassets-demo:org:cloud')::uuid, true),
  (md5('netassets-demo:user:demo_multi_editor')::uuid, md5('netassets-demo:org:east')::uuid, true),
  (md5('netassets-demo:user:demo_multi_editor')::uuid, md5('netassets-demo:org:finance')::uuid, false);

INSERT INTO user_roles (id, user_id, role_id, organization_id, data_scope)
VALUES
  (md5('netassets-demo:user-role:secops')::uuid, md5('netassets-demo:user:demo_secops')::uuid, (SELECT id FROM roles WHERE role_key='security_operator'), NULL, 'all'),
  (md5('netassets-demo:user-role:auditor')::uuid, md5('netassets-demo:user:demo_auditor')::uuid, md5('netassets-demo:role:auditor')::uuid, NULL, 'all'),
  (md5('netassets-demo:user-role:east-admin')::uuid, md5('netassets-demo:user:demo_east_admin')::uuid, (SELECT id FROM roles WHERE role_key='organization_admin'), md5('netassets-demo:org:east')::uuid, 'organization_and_descendants'),
  (md5('netassets-demo:user-role:east-editor')::uuid, md5('netassets-demo:user:demo_east_editor')::uuid, (SELECT id FROM roles WHERE role_key='organization_editor'), md5('netassets-demo:org:east')::uuid, 'organization_and_descendants'),
  (md5('netassets-demo:user-role:fin-admin')::uuid, md5('netassets-demo:user:demo_fin_admin')::uuid, (SELECT id FROM roles WHERE role_key='organization_admin'), md5('netassets-demo:org:finance')::uuid, 'organization_and_descendants'),
  (md5('netassets-demo:user-role:cloud-editor')::uuid, md5('netassets-demo:user:demo_cloud_editor')::uuid, (SELECT id FROM roles WHERE role_key='organization_editor'), md5('netassets-demo:org:cloud')::uuid, 'organization'),
  (md5('netassets-demo:user-role:multi-east')::uuid, md5('netassets-demo:user:demo_multi_editor')::uuid, (SELECT id FROM roles WHERE role_key='organization_editor'), md5('netassets-demo:org:east')::uuid, 'organization_and_descendants'),
  (md5('netassets-demo:user-role:multi-fin')::uuid, md5('netassets-demo:user:demo_multi_editor')::uuid, (SELECT id FROM roles WHERE role_key='organization_editor'), md5('netassets-demo:org:finance')::uuid, 'organization_and_descendants');

-- 每个启用演示组织配置公网区与办公/业务内网区。
WITH orgs(org_key, org_id, label) AS (
  VALUES
    ('EAST', md5('netassets-demo:org:east')::uuid, '华东科技'),
    ('EAST_SH', md5('netassets-demo:org:east-sh')::uuid, '上海办事处'),
    ('EAST_HZ', md5('netassets-demo:org:east-hz')::uuid, '杭州研发'),
    ('FIN', md5('netassets-demo:org:finance')::uuid, '金融服务'),
    ('FIN_BJ', md5('netassets-demo:org:finance-bj')::uuid, '北京运营'),
    ('CLOUD', md5('netassets-demo:org:cloud')::uuid, '云计算')
), kinds(kind, suffix, sort_order) AS (
  VALUES ('PUBLIC', '互联网出口区', 10), ('PRIVATE', '办公及业务内网区', 20)
)
INSERT INTO asset_network_areas (
  id, organization_id, area_name, area_code, status, sort_order, notes, created_by, updated_by
)
SELECT md5('netassets-demo:area:' || org_key || ':' || kind)::uuid,
       org_id, label || suffix, 'DEMO-' || org_key || '-' || kind,
       'enabled', sort_order, '[演示数据] 可配置网络区域',
       (SELECT id FROM users WHERE username='admin'), (SELECT id FROM users WHERE username='admin')
FROM orgs CROSS JOIN kinds;

-- 42 个业务系统，覆盖状态、重要性、互联网暴露和数据安全字段。
WITH orgs(seq, org_id) AS (
  VALUES
    (1, md5('netassets-demo:org:east')::uuid),
    (2, md5('netassets-demo:org:east-sh')::uuid),
    (3, md5('netassets-demo:org:east-hz')::uuid),
    (4, md5('netassets-demo:org:finance')::uuid),
    (5, md5('netassets-demo:org:finance-bj')::uuid),
    (6, md5('netassets-demo:org:cloud')::uuid)
)
INSERT INTO business_systems (
  id, system_code, system_name, short_name, owner_organization_id, department_organization_id,
  owner_user_id, owner_name, owner_contact, system_status, system_level, deployment_type,
  internet_exposure, vendor, description, online_at, created_by, updated_by, created_at, updated_at
)
SELECT md5('netassets-demo:system:' || g::text)::uuid,
       'DEMO-SYS-' || lpad(g::text, 3, '0'),
       '[演示] ' || (ARRAY['客户关系管理系统','统一办公平台','门户网站','供应链平台','财务结算平台','研发协作平台','移动服务平台'])[1 + ((g-1) % 7)] || '-' || lpad(g::text, 2, '0'),
       '演示系统' || g::text,
       o.org_id, o.org_id,
       CASE ((g-1) % 6)+1
         WHEN 1 THEN md5('netassets-demo:user:demo_east_admin')::uuid
         WHEN 2 THEN md5('netassets-demo:user:demo_east_editor')::uuid
         WHEN 3 THEN md5('netassets-demo:user:demo_east_editor')::uuid
         WHEN 4 THEN md5('netassets-demo:user:demo_fin_admin')::uuid
         WHEN 5 THEN md5('netassets-demo:user:demo_fin_admin')::uuid
         ELSE md5('netassets-demo:user:demo_cloud_editor')::uuid
       END,
       CASE WHEN g % 9 = 0 THEN NULL ELSE '系统负责人' || g::text END,
       CASE WHEN g % 9 = 0 THEN NULL ELSE '1381000' || lpad(g::text, 4, '0') END,
       CASE WHEN g % 19=0 THEN 'archived' WHEN g % 13=0 THEN 'offline' WHEN g % 11=0 THEN 'planning' WHEN g % 7=0 THEN 'suspended' ELSE 'running' END,
       CASE WHEN g % 8=0 THEN 'core' WHEN g % 3=0 THEN 'important' ELSE 'normal' END,
       (ARRAY['私有云','公有云','本地机房','混合云'])[1 + ((g-1) % 4)],
       CASE WHEN g % 4=0 THEN 'yes' WHEN g % 4=1 THEN 'partial' WHEN g % 4=2 THEN 'no' ELSE 'unknown' END,
       (ARRAY['集团自研','示例软件供应商A','示例软件供应商B'])[1 + ((g-1) % 3)],
       '[演示数据] 用于业务系统分页、筛选、详情和填报测试',
       current_date - (100 + g * 7),
       (SELECT id FROM users WHERE username='admin'), (SELECT id FROM users WHERE username='admin'),
       now() - make_interval(days => 120-g),
       CASE WHEN g % 10=0 THEN now()-interval '400 days' ELSE now()-make_interval(days => g % 30) END
FROM generate_series(1,42) AS g
JOIN orgs o ON o.seq = ((g-1) % 6)+1;

INSERT INTO business_system_data_security_profiles (
  business_system_id, contains_personal_information, contains_sensitive_personal_information,
  personal_information_types, contains_minor_information, personal_information_subject_count,
  personal_information_record_count, personal_information_volume_range, data_statistical_scope,
  data_baseline_date, data_is_estimated, contains_important_data, contains_core_data,
  data_sensitivity_level, contains_trade_secrets, contains_customer_data, contains_employee_data,
  shares_data_with_third_party, cross_border_transfer, data_storage_location, data_retention_period,
  classification_status, reported_by, confirmed_by, confirmed_at, notes, data_volume_over_100k
)
SELECT id,
       CASE WHEN n % 3=0 THEN 'yes' WHEN n % 3=1 THEN 'no' ELSE 'unknown' END,
       CASE WHEN n % 5=0 THEN 'yes' WHEN n % 3=1 THEN 'no' ELSE 'unknown' END,
       CASE WHEN n % 3=0 THEN '["姓名","手机号","身份证号"]'::jsonb ELSE '[]'::jsonb END,
       CASE WHEN n % 11=0 THEN 'yes' ELSE 'no' END,
       CASE WHEN n % 3=0 THEN 80000+n*5000 ELSE n*100 END,
       CASE WHEN n % 3=0 THEN 120000+n*10000 ELSE n*500 END,
       CASE WHEN n % 3=0 THEN '100001-1000000' ELSE '1000以下' END,
       '截至当前生产库统计', current_date-30, n % 4=0,
       CASE WHEN n % 6=0 THEN 'yes' WHEN n % 7=0 THEN 'pending' ELSE 'no' END,
       CASE WHEN n % 14=0 THEN 'yes' ELSE 'no' END,
       CASE WHEN n % 6=0 THEN 'L4' WHEN n % 3=0 THEN 'L3' ELSE 'L2' END,
       CASE WHEN n % 8=0 THEN 'yes' ELSE 'no' END,
       CASE WHEN n % 3=0 THEN 'yes' ELSE 'no' END,
       CASE WHEN n % 4=0 THEN 'yes' ELSE 'no' END,
       CASE WHEN n % 5=0 THEN 'yes' ELSE 'no' END,
       CASE WHEN n % 17=0 THEN 'yes' ELSE 'no' END,
       '中国境内演示数据中心', '业务存续期加三年',
       CASE WHEN n % 4=0 THEN 'completed' WHEN n % 4=1 THEN 'in_progress' ELSE 'not_started' END,
       created_by, CASE WHEN n % 4=0 THEN created_by ELSE NULL END,
       CASE WHEN n % 4=0 THEN now()-interval '10 days' ELSE NULL END,
       '[演示数据] 数据安全画像', CASE WHEN n % 3=0 THEN 'yes' ELSE 'no' END
FROM (
  SELECT bs.*, row_number() OVER (ORDER BY system_code)::int AS n
  FROM business_systems bs WHERE system_code LIKE 'DEMO-SYS-%'
) s;

-- 135 个网络资产，超过单页最大数量，便于验证分页、筛选和滚动区域。
WITH orgs(seq, org_key, org_id, owner_user_id) AS (
  VALUES
    (1, 'EAST', md5('netassets-demo:org:east')::uuid, md5('netassets-demo:user:demo_east_admin')::uuid),
    (2, 'EAST_SH', md5('netassets-demo:org:east-sh')::uuid, md5('netassets-demo:user:demo_east_editor')::uuid),
    (3, 'EAST_HZ', md5('netassets-demo:org:east-hz')::uuid, md5('netassets-demo:user:demo_east_editor')::uuid),
    (4, 'FIN', md5('netassets-demo:org:finance')::uuid, md5('netassets-demo:user:demo_fin_admin')::uuid),
    (5, 'FIN_BJ', md5('netassets-demo:org:finance-bj')::uuid, md5('netassets-demo:user:demo_fin_admin')::uuid),
    (6, 'CLOUD', md5('netassets-demo:org:cloud')::uuid, md5('netassets-demo:user:demo_cloud_editor')::uuid)
), generated AS (
  SELECT g, o.*,
         CASE g % 5 WHEN 0 THEN 'domain' WHEN 1 THEN 'ip' WHEN 2 THEN 'url' WHEN 3 THEN 'service' ELSE 'other' END AS asset_type,
         CASE g % 5
           WHEN 0 THEN 'asset-' || g || '.demo.example.com'
           WHEN 1 THEN CASE WHEN g % 2=0 THEN '203.0.113.' || g ELSE '10.20.' || ((g-1)/250)::int || '.' || ((g-1)%250+1) END
           WHEN 2 THEN 'https://service-' || g || '.demo.example.com/app'
           WHEN 3 THEN 'tcp://198.51.100.' || g || ':' || (8000 + g)
           ELSE 'DEMO-OTHER-' || lpad(g::text, 3, '0')
         END AS asset_value,
         CASE WHEN g % 2=0 OR g % 5 IN (0,2,3) THEN 'public' ELSE 'private' END AS network_scope
  FROM generate_series(1,135) AS g
  JOIN orgs o ON o.seq=((g-1)%6)+1
)
INSERT INTO assets (
  id, asset_type, asset_value, normalized_value, asset_name, environment, network_scope,
  status, importance, source, notes, extra_data, created_by, updated_by,
  created_at, updated_at, first_seen_at, last_seen_at, expires_at
)
SELECT md5('netassets-demo:asset:' || g::text)::uuid,
       asset_type, asset_value, lower(asset_value), '[演示] 网络资产-' || lpad(g::text,3,'0'),
       (ARRAY['production','staging','testing','development','office'])[1+((g-1)%5)],
       network_scope,
       CASE WHEN g % 23=0 THEN 'archived' WHEN g % 17=0 THEN 'pending' WHEN g % 13=0 THEN 'disabled' ELSE 'active' END,
       CASE WHEN g % 10=0 THEN 'core' WHEN g % 4=0 THEN 'important' ELSE 'normal' END,
       CASE WHEN g % 4=0 THEN 'import' WHEN g % 4=1 THEN 'manual' WHEN g % 4=2 THEN 'api' ELSE 'plugin' END,
       '[演示数据] 使用保留域名和文档专用 IP，不对应真实网络目标',
       jsonb_build_object('demo_seed','netassets-demo-v1','asset_index',g,'contact','测试责任人' || g),
       (SELECT id FROM users WHERE username='admin'), owner_user_id,
       now()-make_interval(days => 200-(g%120)),
       CASE WHEN g % 12=0 THEN now()-interval '420 days' ELSE now()-make_interval(days => g%45) END,
       now()-make_interval(days => 240-(g%100)), now()-make_interval(days => g%60),
       CASE WHEN g % 20=0 THEN now()-interval '5 days'
            WHEN g % 15=0 THEN now()+interval '7 days'
            WHEN g % 9=0 THEN now()+interval '30 days'
            ELSE now()+make_interval(days => 180+(g%90)) END
FROM generated;

WITH orgs(seq, org_key, org_id, owner_user_id) AS (
  VALUES
    (1, 'EAST', md5('netassets-demo:org:east')::uuid, md5('netassets-demo:user:demo_east_admin')::uuid),
    (2, 'EAST_SH', md5('netassets-demo:org:east-sh')::uuid, md5('netassets-demo:user:demo_east_editor')::uuid),
    (3, 'EAST_HZ', md5('netassets-demo:org:east-hz')::uuid, md5('netassets-demo:user:demo_east_editor')::uuid),
    (4, 'FIN', md5('netassets-demo:org:finance')::uuid, md5('netassets-demo:user:demo_fin_admin')::uuid),
    (5, 'FIN_BJ', md5('netassets-demo:org:finance-bj')::uuid, md5('netassets-demo:user:demo_fin_admin')::uuid),
    (6, 'CLOUD', md5('netassets-demo:org:cloud')::uuid, md5('netassets-demo:user:demo_cloud_editor')::uuid)
)
INSERT INTO asset_organizations (asset_id, organization_id, relationship_type, is_primary, owner_user_id, owner_name)
SELECT md5('netassets-demo:asset:' || g::text)::uuid, o.org_id, 'owner', true, o.owner_user_id, '测试责任人' || g
FROM generate_series(1,135) AS g
JOIN orgs o ON o.seq=((g-1)%6)+1;

UPDATE assets a
SET network_area_id = md5('netassets-demo:area:' || o.org_key || ':' || CASE a.network_scope WHEN 'public' THEN 'PUBLIC' ELSE 'PRIVATE' END)::uuid
FROM asset_organizations ao
JOIN (VALUES
    ('EAST', md5('netassets-demo:org:east')::uuid),
    ('EAST_SH', md5('netassets-demo:org:east-sh')::uuid),
    ('EAST_HZ', md5('netassets-demo:org:east-hz')::uuid),
    ('FIN', md5('netassets-demo:org:finance')::uuid),
    ('FIN_BJ', md5('netassets-demo:org:finance-bj')::uuid),
    ('CLOUD', md5('netassets-demo:org:cloud')::uuid)
) AS o(org_key, org_id) ON o.org_id=ao.organization_id
WHERE ao.asset_id=a.id AND ao.is_primary AND a.extra_data->>'demo_seed'='netassets-demo-v1';

-- 每个系统关联 3 个同组织资产。
INSERT INTO system_assets (business_system_id, asset_id, relationship_type, is_primary, created_by)
SELECT bs.id, chosen.id, CASE chosen.rn WHEN 1 THEN 'primary' ELSE 'uses' END,
       chosen.rn=1, (SELECT id FROM users WHERE username='admin')
FROM business_systems bs
JOIN LATERAL (
  SELECT a.id, row_number() OVER (ORDER BY md5(bs.id::text || a.id::text)) AS rn
  FROM assets a
  JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
  WHERE ao.organization_id=bs.owner_organization_id
    AND a.extra_data->>'demo_seed'='netassets-demo-v1'
  ORDER BY md5(bs.id::text || a.id::text)
  LIMIT 3
) chosen ON true
WHERE bs.system_code LIKE 'DEMO-SYS-%';

-- 待确认资产，覆盖待审核、接受、忽略、重复和无效状态。
WITH orgs(seq, org_id) AS (
  VALUES (1,md5('netassets-demo:org:east')::uuid),(2,md5('netassets-demo:org:finance')::uuid),(3,md5('netassets-demo:org:cloud')::uuid)
)
INSERT INTO asset_candidates (
  id, asset_type, asset_value, normalized_value, suggested_data, organization_id,
  source, source_reference, status, matched_asset_id, created_by, reviewed_by, reviewed_at, review_note, created_at
)
SELECT md5('netassets-demo:candidate:'||g)::uuid,
       CASE WHEN g%3=0 THEN 'url' WHEN g%3=1 THEN 'domain' ELSE 'ip' END,
       CASE WHEN g%3=0 THEN 'https://candidate-'||g||'.demo.example.com/' WHEN g%3=1 THEN 'candidate-'||g||'.demo.example.com' ELSE '192.0.2.'||(150+g) END,
       CASE WHEN g%3=0 THEN 'https://candidate-'||g||'.demo.example.com/' WHEN g%3=1 THEN 'candidate-'||g||'.demo.example.com' ELSE '192.0.2.'||(150+g) END,
       jsonb_build_object('asset_name','[演示] 待确认资产-'||g,'network_scope','public','importance',CASE WHEN g%4=0 THEN 'important' ELSE 'normal' END),
       o.org_id, 'manual_import', 'DEMO-CAND-'||lpad(g::text,3,'0'),
       CASE WHEN g<=8 THEN 'pending' WHEN g<=11 THEN 'ignored' WHEN g<=14 THEN 'duplicate' WHEN g<=16 THEN 'invalid' ELSE 'accepted' END,
       CASE WHEN g>16 THEN md5('netassets-demo:asset:'||g)::uuid ELSE NULL END,
       (SELECT id FROM users WHERE username='admin'),
       CASE WHEN g>8 THEN (SELECT id FROM users WHERE username='admin') ELSE NULL END,
       CASE WHEN g>8 THEN now()-make_interval(days=>g%5) ELSE NULL END,
       CASE WHEN g>8 THEN '[演示数据] 人工审核意见' ELSE NULL END,
       now()-make_interval(hours=>g*5)
FROM generate_series(1,18) AS g JOIN orgs o ON o.seq=((g-1)%3)+1;

-- 六类填报任务状态及组织进度。
INSERT INTO inventory_tasks (
  id, task_name, description, start_at, deadline_at, status, required_fields,
  quality_rules, require_review, allow_late_submission, created_by, created_at, updated_at, asset_network_scope
)
VALUES
  (md5('netassets-demo:task:1')::uuid, '[演示] 2026年三季度公网资产专项核查', '只核查公网资产，包含未开始、进行中和已提交组织。', now()-interval '10 days', now()+interval '12 days', 'active', '["asset_name","owner_name"]', '[]', true, false, (SELECT id FROM users WHERE username='admin'), now()-interval '12 days', now()-interval '2 days', 'public'),
  (md5('netassets-demo:task:2')::uuid, '[演示] 逾期资产补充填报', '用于测试逾期和退回状态。', now()-interval '30 days', now()-interval '3 days', 'active', '["asset_name"]', '[]', true, true, (SELECT id FROM users WHERE username='admin'), now()-interval '35 days', now()-interval '1 day', 'all'),
  (md5('netassets-demo:task:3')::uuid, '[演示] 上半年资产盘点（已完成）', '已审核通过的历史任务。', now()-interval '120 days', now()-interval '80 days', 'completed', '[]', '[]', true, false, (SELECT id FROM users WHERE username='admin'), now()-interval '125 days', now()-interval '79 days', 'all'),
  (md5('netassets-demo:task:4')::uuid, '[演示] 内网资产核查草稿', '尚未下发的草稿任务。', now()+interval '5 days', now()+interval '35 days', 'draft', '[]', '[]', true, false, (SELECT id FROM users WHERE username='admin'), now()-interval '2 days', now()-interval '2 days', 'private'),
  (md5('netassets-demo:task:5')::uuid, '[演示] 临时专项核查（已关闭）', '关闭任务状态示例。', now()-interval '70 days', now()-interval '40 days', 'closed', '[]', '[]', false, false, (SELECT id FROM users WHERE username='admin'), now()-interval '72 days', now()-interval '40 days', 'public'),
  (md5('netassets-demo:task:6')::uuid, '[演示] 历史归档任务', '归档任务状态示例。', now()-interval '300 days', now()-interval '250 days', 'archived', '[]', '[]', true, false, (SELECT id FROM users WHERE username='admin'), now()-interval '305 days', now()-interval '200 days', 'all');

INSERT INTO inventory_task_organizations (
  task_id, organization_id, status, submitted_by, submitted_at, reviewed_by, reviewed_at,
  return_reason, updated_at, started_at, submission_note, submitted_system_count, submitted_asset_count
)
VALUES
  (md5('netassets-demo:task:1')::uuid, md5('netassets-demo:org:east')::uuid, 'in_progress', NULL, NULL, NULL, NULL, NULL, now()-interval '1 day', now()-interval '6 days', NULL, 0, 0),
  (md5('netassets-demo:task:1')::uuid, md5('netassets-demo:org:finance')::uuid, 'submitted', md5('netassets-demo:user:demo_fin_admin')::uuid, now()-interval '1 day', NULL, NULL, NULL, now()-interval '1 day', now()-interval '8 days', '金融板块已完成公网资产核查', 7, 20),
  (md5('netassets-demo:task:1')::uuid, md5('netassets-demo:org:cloud')::uuid, 'not_started', NULL, NULL, NULL, NULL, NULL, now()-interval '2 days', NULL, NULL, 0, 0),
  (md5('netassets-demo:task:2')::uuid, md5('netassets-demo:org:east')::uuid, 'overdue', NULL, NULL, NULL, NULL, NULL, now()-interval '2 days', now()-interval '20 days', NULL, 0, 0),
  (md5('netassets-demo:task:2')::uuid, md5('netassets-demo:org:finance')::uuid, 'returned', md5('netassets-demo:user:demo_fin_admin')::uuid, now()-interval '5 days', (SELECT id FROM users WHERE username='admin'), now()-interval '4 days', '仍有 2 项公网资产缺少责任人，请补充后重新提交。', now()-interval '4 days', now()-interval '25 days', '首次提交', 6, 18),
  (md5('netassets-demo:task:3')::uuid, md5('netassets-demo:org:east')::uuid, 'approved', md5('netassets-demo:user:demo_east_admin')::uuid, now()-interval '90 days', (SELECT id FROM users WHERE username='admin'), now()-interval '88 days', NULL, now()-interval '88 days', now()-interval '110 days', '历史盘点提交', 14, 45),
  (md5('netassets-demo:task:3')::uuid, md5('netassets-demo:org:finance')::uuid, 'approved', md5('netassets-demo:user:demo_fin_admin')::uuid, now()-interval '91 days', (SELECT id FROM users WHERE username='admin'), now()-interval '89 days', NULL, now()-interval '89 days', now()-interval '112 days', '历史盘点提交', 14, 45),
  (md5('netassets-demo:task:3')::uuid, md5('netassets-demo:org:cloud')::uuid, 'approved', md5('netassets-demo:user:demo_cloud_editor')::uuid, now()-interval '92 days', (SELECT id FROM users WHERE username='admin'), now()-interval '90 days', NULL, now()-interval '90 days', now()-interval '115 days', '历史盘点提交', 7, 22);

-- 为已提交、退回和已通过进度生成提交历史与快照。
WITH submission_seed(id, task_id, org_id, version, submitter, submitted_at, note, review_status, reviewer, reviewed_at, return_reason) AS (
  VALUES
    (md5('netassets-demo:submission:1')::uuid, md5('netassets-demo:task:1')::uuid, md5('netassets-demo:org:finance')::uuid, 1, md5('netassets-demo:user:demo_fin_admin')::uuid, now()-interval '1 day', '金融板块公网资产提交', 'submitted', NULL::uuid, NULL::timestamptz, NULL::text),
    (md5('netassets-demo:submission:2')::uuid, md5('netassets-demo:task:2')::uuid, md5('netassets-demo:org:finance')::uuid, 1, md5('netassets-demo:user:demo_fin_admin')::uuid, now()-interval '5 days', '金融板块首次提交', 'returned', (SELECT id FROM users WHERE username='admin'), now()-interval '4 days', '责任人信息不完整'),
    (md5('netassets-demo:submission:3')::uuid, md5('netassets-demo:task:3')::uuid, md5('netassets-demo:org:east')::uuid, 1, md5('netassets-demo:user:demo_east_admin')::uuid, now()-interval '90 days', '华东历史盘点', 'approved', (SELECT id FROM users WHERE username='admin'), now()-interval '88 days', NULL::text),
    (md5('netassets-demo:submission:4')::uuid, md5('netassets-demo:task:3')::uuid, md5('netassets-demo:org:finance')::uuid, 1, md5('netassets-demo:user:demo_fin_admin')::uuid, now()-interval '91 days', '金融历史盘点', 'approved', (SELECT id FROM users WHERE username='admin'), now()-interval '89 days', NULL::text),
    (md5('netassets-demo:submission:5')::uuid, md5('netassets-demo:task:3')::uuid, md5('netassets-demo:org:cloud')::uuid, 1, md5('netassets-demo:user:demo_cloud_editor')::uuid, now()-interval '92 days', '云计算历史盘点', 'approved', (SELECT id FROM users WHERE username='admin'), now()-interval '90 days', NULL::text)
)
INSERT INTO inventory_submissions (
  id, task_id, organization_id, submission_version, submitted_by, submitted_at,
  submission_note, system_count, asset_count, review_status, reviewed_at, reviewed_by, return_reason
)
SELECT id, task_id, org_id, version, submitter, submitted_at, note,
       (SELECT count(*) FROM business_systems WHERE owner_organization_id=org_id AND system_code LIKE 'DEMO-SYS-%'),
       (SELECT count(*) FROM asset_organizations ao JOIN assets a ON a.id=ao.asset_id WHERE ao.organization_id=org_id AND ao.is_primary AND a.extra_data->>'demo_seed'='netassets-demo-v1'),
       review_status, reviewed_at, reviewer, return_reason
FROM submission_seed;

UPDATE inventory_task_organizations ito
SET latest_submission_id=s.id
FROM inventory_submissions s
WHERE s.task_id=ito.task_id AND s.organization_id=ito.organization_id
  AND s.id::text LIKE '%';

INSERT INTO inventory_submission_items (submission_id, resource_type, resource_id, resource_key, resource_name, snapshot_data)
SELECT s.id, 'business_system', bs.id, bs.system_code, bs.system_name, to_jsonb(bs)
FROM inventory_submissions s
JOIN LATERAL (
  SELECT * FROM business_systems
  WHERE owner_organization_id=s.organization_id AND system_code LIKE 'DEMO-SYS-%'
  ORDER BY system_code LIMIT 5
) bs ON true;
INSERT INTO inventory_submission_items (submission_id, resource_type, resource_id, resource_key, resource_name, snapshot_data)
SELECT s.id, 'network_asset', a.id, a.normalized_value, COALESCE(a.asset_name,a.asset_value), to_jsonb(a)
FROM inventory_submissions s
JOIN LATERAL (
  SELECT a.* FROM assets a JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
  WHERE ao.organization_id=s.organization_id AND a.extra_data->>'demo_seed'='netassets-demo-v1'
  ORDER BY a.created_at LIMIT 10
) a ON true;

-- 数据质量规则、三次巡检及多状态整改问题。
INSERT INTO data_quality_rules (
  id, rule_name, description, resource_type, rule_type, target_field, severity,
  is_enabled, sort_order, created_by, stale_after_days, conditions, expiry_warning_days
)
VALUES
  (md5('netassets-demo:quality-rule:1')::uuid, '[演示] 业务系统负责人必填', '运行中的业务系统必须登记负责人。', 'business_system', 'required', 'owner_name', 'error', true, 10, (SELECT id FROM users WHERE username='admin'), NULL, '[{"field":"system_status","operator":"equals","value":"running"}]', NULL),
  (md5('netassets-demo:quality-rule:2')::uuid, '[演示] 网络资产长期未更新', '超过 180 天未维护的资产需要重新确认。', 'asset', 'stale', 'updated_at', 'warning', true, 20, (SELECT id FROM users WHERE username='admin'), 180, '[]', NULL),
  (md5('netassets-demo:quality-rule:3')::uuid, '[演示] 网络资产到期提醒', '到期前 30 天提醒责任人确认续期或下线。', 'asset', 'expiry', 'expires_at', 'warning', true, 30, (SELECT id FROM users WHERE username='admin'), NULL, '[]', 30);

INSERT INTO data_quality_scan_batches (
  id, organization_id, resource_type, severity, asset_count, system_count,
  enabled_rule_count, total_issue_count, error_count, warning_count, auto_closed_count,
  created_by, created_at, trigger_type, scheduled_for
)
VALUES
  (md5('netassets-demo:quality-batch:1')::uuid, NULL, NULL, NULL, 135, 42, 3, 24, 4, 20, 0, (SELECT id FROM users WHERE username='admin'), now()-interval '1 day', 'manual', NULL),
  (md5('netassets-demo:quality-batch:2')::uuid, NULL, 'asset', 'warning', 135, 0, 2, 16, 0, 16, 3, NULL, now()-interval '7 days', 'scheduled', current_date-7),
  (md5('netassets-demo:quality-batch:3')::uuid, NULL, NULL, NULL, 135, 42, 3, 20, 4, 16, 2, NULL, now()-interval '14 days', 'scheduled', current_date-14);

WITH selected AS (
  SELECT a.id, COALESCE(a.asset_name,a.asset_value) AS resource_name, ao.organization_id,
         o.organization_code, o.organization_name,
         row_number() OVER (ORDER BY a.id)::int AS n
  FROM assets a
  JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
  JOIN organizations o ON o.id=ao.organization_id
  WHERE a.extra_data->>'demo_seed'='netassets-demo-v1' AND a.expires_at <= now()+interval '30 days'
  ORDER BY a.id LIMIT 16
)
INSERT INTO data_quality_issues (
  id, rule_id, resource_type, resource_id, resource_name, organization_id,
  organization_code, organization_name, rule_name, target_field, severity, status,
  assignee_user_id, rectification_note, submitted_by, submitted_at, review_note,
  reviewed_by, reviewed_at, resolution_type, first_detected_batch_id,
  last_detected_batch_id, first_detected_at, last_detected_at, occurrence_count, closed_at, updated_by
)
SELECT md5('netassets-demo:quality-issue:asset:'||n)::uuid,
       CASE WHEN n%2=0 THEN md5('netassets-demo:quality-rule:3')::uuid ELSE md5('netassets-demo:quality-rule:2')::uuid END,
       'asset', id, resource_name, organization_id, organization_code, organization_name,
       CASE WHEN n%2=0 THEN '[演示] 网络资产到期提醒' ELSE '[演示] 网络资产长期未更新' END,
       CASE WHEN n%2=0 THEN 'expires_at' ELSE 'updated_at' END, 'warning',
       CASE n%5 WHEN 0 THEN 'closed' WHEN 1 THEN 'pending' WHEN 2 THEN 'in_progress' WHEN 3 THEN 'pending_review' ELSE 'exempted' END,
       CASE WHEN organization_code LIKE 'DEMO_FIN%' THEN md5('netassets-demo:user:demo_fin_admin')::uuid
            WHEN organization_code LIKE 'DEMO_CLOUD%' THEN md5('netassets-demo:user:demo_cloud_editor')::uuid
            ELSE md5('netassets-demo:user:demo_east_admin')::uuid END,
       CASE WHEN n%5 IN (2,3,0) THEN '已更新责任人并确认续期计划。' ELSE NULL END,
       CASE WHEN n%5 IN (3,0) THEN md5('netassets-demo:user:demo_east_admin')::uuid ELSE NULL END,
       CASE WHEN n%5 IN (3,0) THEN now()-interval '2 days' ELSE NULL END,
       CASE WHEN n%5=0 THEN '复核通过。' ELSE NULL END,
       CASE WHEN n%5=0 THEN (SELECT id FROM users WHERE username='admin') ELSE NULL END,
       CASE WHEN n%5=0 THEN now()-interval '1 day' ELSE NULL END,
       CASE WHEN n%5=0 THEN 'corrected' WHEN n%5=4 THEN 'exempted' ELSE NULL END,
       md5('netassets-demo:quality-batch:3')::uuid,
       md5('netassets-demo:quality-batch:1')::uuid,
       now()-interval '14 days', now()-interval '1 day', 3,
       CASE WHEN n%5=0 THEN now()-interval '1 day' ELSE NULL END,
       (SELECT id FROM users WHERE username='admin')
FROM selected;

WITH selected AS (
  SELECT bs.id, bs.system_name, bs.owner_organization_id, o.organization_code, o.organization_name,
         row_number() OVER (ORDER BY bs.system_code)::int AS n
  FROM business_systems bs JOIN organizations o ON o.id=bs.owner_organization_id
  WHERE bs.system_code LIKE 'DEMO-SYS-%' AND bs.owner_name IS NULL
)
INSERT INTO data_quality_issues (
  id, rule_id, resource_type, resource_id, resource_name, organization_id,
  organization_code, organization_name, rule_name, target_field, severity, status,
  assignee_user_id, first_detected_batch_id, last_detected_batch_id,
  first_detected_at, last_detected_at, occurrence_count, updated_by
)
SELECT md5('netassets-demo:quality-issue:system:'||n)::uuid,
       md5('netassets-demo:quality-rule:1')::uuid, 'business_system', id, system_name,
       owner_organization_id, organization_code, organization_name,
       '[演示] 业务系统负责人必填', 'owner_name', 'error',
       CASE WHEN n%2=0 THEN 'in_progress' ELSE 'pending' END,
       CASE WHEN organization_code LIKE 'DEMO_FIN%' THEN md5('netassets-demo:user:demo_fin_admin')::uuid
            WHEN organization_code LIKE 'DEMO_CLOUD%' THEN md5('netassets-demo:user:demo_cloud_editor')::uuid
            ELSE md5('netassets-demo:user:demo_east_admin')::uuid END,
       md5('netassets-demo:quality-batch:3')::uuid, md5('netassets-demo:quality-batch:1')::uuid,
       now()-interval '14 days', now()-interval '1 day', 3, (SELECT id FROM users WHERE username='admin')
FROM selected;

INSERT INTO data_quality_scan_batch_issues (batch_id, issue_id)
SELECT md5('netassets-demo:quality-batch:1')::uuid, id
FROM data_quality_issues WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\';
INSERT INTO data_quality_issue_events (issue_id, actor_user_id, event_type, from_status, to_status, note, details, created_at)
SELECT id, (SELECT id FROM users WHERE username='admin'), 'detected', NULL, status,
       '[演示数据] 数据质量巡检发现', '{"demo_seed":"netassets-demo-v1"}', first_detected_at
FROM data_quality_issues WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\';

-- 安全测试批次、测试范围、漏洞和工单。
INSERT INTO security_test_batches (
  id, batch_code, batch_name, test_type, status, provider, authorization_note,
  authorized_by, authorized_at, start_at, end_at, started_at, completed_at,
  cancelled_at, notes, created_by, updated_by, created_at, updated_at
)
SELECT md5('netassets-demo:batch:'||g)::uuid, 'DEMO-BATCH-'||lpad(g::text,3,'0'),
       '[演示] '||(ARRAY['季度互联网资产渗透测试','核心系统人工测试','公网弱点扫描','漏洞复测','重大活动保障测试','已取消测试'])[g],
       (ARRAY['penetration','manual','scan','retest','other','scan'])[g],
       (ARRAY['draft','authorized','in_progress','completed','completed','cancelled'])[g],
       CASE WHEN g%2=0 THEN '集团安全团队' ELSE '示例第三方安全机构' END,
       '已取得测试范围内资产所属组织授权，仅允许在约定时段开展非破坏性测试。',
       CASE WHEN g=1 THEN NULL ELSE md5('netassets-demo:user:demo_secops')::uuid END,
       CASE WHEN g=1 THEN NULL ELSE now()-make_interval(days=>30-g*3) END,
       now()-make_interval(days=>25-g*3), now()-make_interval(days=>18-g*3),
       CASE WHEN g IN (3,4,5) THEN now()-make_interval(days=>24-g*3) ELSE NULL END,
       CASE WHEN g IN (4,5) THEN now()-make_interval(days=>18-g*3) ELSE NULL END,
       CASE WHEN g=6 THEN now()-interval '2 days' ELSE NULL END,
       '[演示数据] 安全测试批次', md5('netassets-demo:user:demo_secops')::uuid,
       md5('netassets-demo:user:demo_secops')::uuid, now()-make_interval(days=>32-g*3), now()-make_interval(days=>g)
FROM generate_series(1,6) AS g;

INSERT INTO security_test_scope_assets (
  id, batch_id, asset_id, asset_type_snapshot, asset_value_snapshot, asset_name_snapshot,
  organization_id, organization_code_snapshot, organization_name_snapshot,
  business_system_id, business_system_code_snapshot, business_system_name_snapshot,
  network_scope_snapshot, network_area_name_snapshot, environment_snapshot,
  importance_snapshot, snapshot_at, included_by
)
SELECT md5('netassets-demo:scope:'||b.g||':'||a.rn)::uuid,
       md5('netassets-demo:batch:'||b.g)::uuid, a.id, a.asset_type, a.asset_value, a.asset_name,
       a.organization_id, a.organization_code, a.organization_name,
       a.business_system_id, a.system_code, a.system_name,
       a.network_scope, a.area_name, a.environment, a.importance,
       now()-make_interval(days=>25-b.g*3), md5('netassets-demo:user:demo_secops')::uuid
FROM generate_series(1,6) b(g)
JOIN LATERAL (
  SELECT a.id, a.asset_type, a.asset_value, a.asset_name, ao.organization_id,
         o.organization_code, o.organization_name, bs.id AS business_system_id,
         bs.system_code, bs.system_name, a.network_scope, ana.area_name, a.environment, a.importance,
         row_number() OVER (ORDER BY md5(b.g::text||a.id::text))::int AS rn
  FROM assets a
  JOIN asset_organizations ao ON ao.asset_id=a.id AND ao.is_primary
  JOIN organizations o ON o.id=ao.organization_id
  LEFT JOIN asset_network_areas ana ON ana.id=a.network_area_id
  LEFT JOIN LATERAL (
    SELECT bs.id, bs.system_code, bs.system_name FROM system_assets sa
    JOIN business_systems bs ON bs.id=sa.business_system_id
    WHERE sa.asset_id=a.id ORDER BY sa.is_primary DESC, bs.system_code LIMIT 1
  ) bs ON true
  WHERE a.extra_data->>'demo_seed'='netassets-demo-v1' AND a.network_scope='public' AND a.status='active'
  ORDER BY md5(b.g::text||a.id::text) LIMIT 12
) a ON true;

INSERT INTO security_test_batch_events (batch_id, actor_user_id, action, from_status, to_status, comment, details, created_at)
SELECT id, md5('netassets-demo:user:demo_secops')::uuid, 'created', NULL, 'draft',
       '[演示数据] 创建安全测试批次', '{"demo_seed":"netassets-demo-v1"}', created_at
FROM security_test_batches WHERE batch_code LIKE 'DEMO-BATCH-%';

WITH targets AS (
  SELECT s.*, row_number() OVER (ORDER BY s.batch_id,s.id)::int AS n
  FROM security_test_scope_assets s
  WHERE s.batch_id IN (SELECT id FROM security_test_batches WHERE batch_code LIKE 'DEMO-BATCH-%')
  LIMIT 36
)
INSERT INTO security_findings (
  id, finding_code, title, severity, status, assignee_organization_id,
  asset_id, business_system_id, vulnerability_type, description, evidence,
  remediation_advice, source, discovered_at, created_by, updated_by,
  created_at, updated_at, test_batch_id, risk_accepted_at, risk_accepted_until,
  risk_acceptance_reason, risk_accepted_by, risk_accepted_from_status
)
SELECT md5('netassets-demo:finding:'||n)::uuid, 'DEMO-FIND-'||lpad(n::text,3,'0'),
       '[演示] '||(ARRAY['未授权访问','弱口令风险','SQL 注入风险','敏感信息泄露','TLS 配置不当','组件版本过旧'])[1+((n-1)%6)]||'-'||lpad(n::text,2,'0'),
       (ARRAY['critical','high','medium','low','info'])[1+((n-1)%5)],
       CASE n%6 WHEN 0 THEN 'resolved' WHEN 1 THEN 'open' WHEN 2 THEN 'assigned' WHEN 3 THEN 'accepted_risk' WHEN 4 THEN 'false_positive' ELSE 'open' END,
       organization_id, asset_id, business_system_id,
       (ARRAY['访问控制','身份认证','注入漏洞','信息泄露','安全配置','组件漏洞'])[1+((n-1)%6)],
       '[演示数据] 此漏洞仅用于功能测试，不代表真实资产存在安全问题。',
       '演示请求与响应证据 #'||n, '请先限制访问范围，再升级组件并完成复测。',
       CASE WHEN n%3=0 THEN 'import' ELSE 'manual' END,
       now()-make_interval(days=>n%25), md5('netassets-demo:user:demo_secops')::uuid,
       md5('netassets-demo:user:demo_secops')::uuid, now()-make_interval(days=>n%25), now()-make_interval(days=>n%10),
       batch_id,
       CASE WHEN n%6=3 THEN now()-interval '3 days' ELSE NULL END,
       CASE WHEN n%6=3 THEN now()+interval '90 days' ELSE NULL END,
       CASE WHEN n%6=3 THEN '业务下线窗口暂未到，采取临时访问控制措施。' ELSE NULL END,
       CASE WHEN n%6=3 THEN md5('netassets-demo:user:demo_secops')::uuid ELSE NULL END,
       CASE WHEN n%6=3 THEN 'assigned' ELSE NULL END
FROM targets;

WITH findings AS (
  SELECT f.*, row_number() OVER (ORDER BY finding_code)::int AS n
  FROM security_findings f WHERE finding_code LIKE 'DEMO-FIND-%' ORDER BY finding_code LIMIT 24
)
INSERT INTO security_work_orders (
  id, work_order_no, title, assignee_organization_id, severity, status,
  description, remediation_requirement, due_at, issued_at, accepted_at,
  submitted_at, verified_at, closed_at, remediation_progress,
  remediation_result, verification_result, return_reason, created_by, updated_by,
  created_at, updated_at
)
SELECT md5('netassets-demo:work-order:'||n)::uuid, 'DEMO-WO-'||lpad(n::text,3,'0'),
       '[演示] '||title||'整改工单', assignee_organization_id, severity,
       (ARRAY['draft','issued','in_progress','pending_verification','returned','closed'])[1+((n-1)%6)],
       '根据安全测试发现下发的演示整改工单。', '请在期限内完成修复、提交证据并申请复测。',
       CASE WHEN n%4=0 THEN now()-interval '2 days' ELSE now()+make_interval(days=>3+n%25) END,
       CASE WHEN n%6=1 THEN NULL ELSE now()-make_interval(days=>n%12) END,
       CASE WHEN n%6 IN (3,4,5,0) THEN now()-make_interval(days=>n%8) ELSE NULL END,
       CASE WHEN n%6 IN (4,0) THEN now()-interval '2 days' ELSE NULL END,
       CASE WHEN n%6=0 THEN now()-interval '1 day' ELSE NULL END,
       CASE WHEN n%6=0 THEN now()-interval '1 day' ELSE NULL END,
       CASE WHEN n%6 IN (3,4,5,0) THEN '已完成初步排查，整改进度 '||(40+n%60)||'% 。' ELSE NULL END,
       CASE WHEN n%6 IN (4,0) THEN '已升级组件并补充访问控制策略。' ELSE NULL END,
       CASE WHEN n%6=0 THEN '复测通过，未再发现同类问题。' ELSE NULL END,
       CASE WHEN n%6=5 THEN '修复证据缺少变更记录，请补充。' ELSE NULL END,
       md5('netassets-demo:user:demo_secops')::uuid, md5('netassets-demo:user:demo_secops')::uuid,
       now()-make_interval(days=>n%20+5), now()-make_interval(days=>n%5)
FROM findings;

INSERT INTO security_work_order_findings (work_order_id, finding_id)
SELECT md5('netassets-demo:work-order:'||n)::uuid, md5('netassets-demo:finding:'||n)::uuid
FROM generate_series(1,24) AS n;
INSERT INTO security_work_order_events (
  work_order_id, actor_user_id, actor_organization_id, action, from_status, to_status, comment, details, created_at
)
SELECT wo.id, wo.created_by,
       (SELECT id FROM organizations WHERE organization_type='group' AND deleted_at IS NULL ORDER BY created_at LIMIT 1),
       'created', NULL, 'draft', '[演示数据] 创建安全整改工单', '{"demo_seed":"netassets-demo-v1"}', wo.created_at
FROM security_work_orders wo WHERE wo.work_order_no LIKE 'DEMO-WO-%';

-- 站内消息事件，结合填报任务动态消息测试消息中心。
INSERT INTO notification_events (
  id, event_type, category, target_organization_id, actor_user_id, required_permission,
  resource_type, resource_id, resource_label, batch_key, item_count, level, details, occurred_at, updated_at
)
SELECT md5('netassets-demo:notification:'||g)::uuid,
       CASE g%4 WHEN 0 THEN 'asset.status_changed' WHEN 1 THEN 'asset.network_area_changed' WHEN 2 THEN 'security_finding.created_on_asset' ELSE 'working_version.published' END,
       CASE g%4 WHEN 0 THEN 'asset_change' WHEN 1 THEN 'asset_change' WHEN 2 THEN 'finding_received' ELSE 'working_version' END,
       (ARRAY[md5('netassets-demo:org:east')::uuid,md5('netassets-demo:org:finance')::uuid,md5('netassets-demo:org:cloud')::uuid])[1+((g-1)%3)],
       (SELECT id FROM users WHERE username='admin'),
       CASE g%4 WHEN 2 THEN 'security_finding.manage' WHEN 3 THEN 'inventory.manage' ELSE 'asset.manage' END,
       CASE g%4 WHEN 2 THEN 'security_finding' WHEN 3 THEN 'inventory_submission' ELSE 'asset' END,
       CASE g%4 WHEN 2 THEN md5('netassets-demo:finding:'||LEAST(g,36))::uuid::text WHEN 3 THEN md5('netassets-demo:task:1')::uuid::text ELSE md5('netassets-demo:asset:'||g)::uuid::text END,
       '[演示] 消息中心事件-'||lpad(g::text,2,'0'), 'DEMO:notification:'||g,
       CASE WHEN g%7=0 THEN 3 ELSE 1 END,
       CASE WHEN g%5=0 THEN 'danger' WHEN g%3=0 THEN 'warning' ELSE 'info' END,
       jsonb_build_object('demo_seed','netassets-demo-v1','action_path',CASE WHEN g%4=2 THEN '/security-work-orders' ELSE '/assets' END),
       now()-make_interval(hours=>g*4), now()-make_interval(hours=>g*4)
FROM generate_series(1,20) AS g;

-- 150 条演示审计日志，用于分页、筛选和滚动验证。
INSERT INTO audit_logs (
  user_id, organization_id, action, resource_type, resource_id, summary,
  details, request_ip, request_id, result, created_at
)
SELECT CASE WHEN g%5=0 THEN md5('netassets-demo:user:demo_secops')::uuid ELSE (SELECT id FROM users WHERE username='admin') END,
       CASE g%3 WHEN 0 THEN md5('netassets-demo:org:east')::uuid WHEN 1 THEN md5('netassets-demo:org:finance')::uuid ELSE md5('netassets-demo:org:cloud')::uuid END,
       (ARRAY['asset.update','asset.list','business_system.update','inventory_task.review','security_work_order.issue','data_quality.scan'])[1+((g-1)%6)],
       (ARRAY['asset','asset','business_system','inventory_task','security_work_order','data_quality'])[1+((g-1)%6)],
       'DEMO-'||lpad(g::text,4,'0'), '[演示] 审计日志记录 '||g,
       jsonb_build_object('demo_seed','netassets-demo-v1','sequence',g),
       ('192.0.2.'||((g-1)%200+1))::inet, 'demo-request-'||g,
       CASE WHEN g%17=0 THEN 'failure' ELSE 'success' END,
       now()-make_interval(mins=>g*17)
FROM generate_series(1,150) AS g;

COMMIT;

SELECT 'organizations' AS item, count(*) AS total FROM organizations WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\'
UNION ALL SELECT 'users', count(*) FROM users WHERE username LIKE 'demo_%'
UNION ALL SELECT 'business_systems', count(*) FROM business_systems WHERE system_code LIKE 'DEMO-SYS-%'
UNION ALL SELECT 'assets', count(*) FROM assets WHERE extra_data->>'demo_seed'='netassets-demo-v1'
UNION ALL SELECT 'inventory_tasks', count(*) FROM inventory_tasks WHERE task_name LIKE '[演示]%'
UNION ALL SELECT 'quality_issues', count(*) FROM data_quality_issues WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\'
UNION ALL SELECT 'security_batches', count(*) FROM security_test_batches WHERE batch_code LIKE 'DEMO-BATCH-%'
UNION ALL SELECT 'security_findings', count(*) FROM security_findings WHERE finding_code LIKE 'DEMO-FIND-%'
UNION ALL SELECT 'work_orders', count(*) FROM security_work_orders WHERE work_order_no LIKE 'DEMO-WO-%'
UNION ALL SELECT 'notifications', count(*) FROM notification_events WHERE batch_key LIKE 'DEMO:%'
UNION ALL SELECT 'audit_logs', count(*) FROM audit_logs WHERE details->>'demo_seed'='netassets-demo-v1'
ORDER BY item;
