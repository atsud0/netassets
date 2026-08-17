\set ON_ERROR_STOP on

BEGIN;

-- 仅清理由 scripts/seed-demo-data.sql 生成的 DEMO 数据，不处理现有业务记录。
DELETE FROM notification_reads
WHERE notification_key IN (
  SELECT 'ledger_event:' || id::text FROM notification_events WHERE batch_key LIKE 'DEMO:%'
);
DELETE FROM notification_events WHERE batch_key LIKE 'DEMO:%';

DELETE FROM audit_logs
WHERE details ->> 'demo_seed' = 'netassets-demo-v1'
   OR user_id IN (SELECT id FROM users WHERE username LIKE 'demo_%')
   OR organization_id IN (SELECT id FROM organizations WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\');

DELETE FROM data_quality_issue_events
WHERE issue_id IN (
  SELECT id FROM data_quality_issues WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\'
);
DELETE FROM data_quality_scan_batch_issues
WHERE issue_id IN (
  SELECT id FROM data_quality_issues WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\'
);
DELETE FROM data_quality_issues WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\';
DELETE FROM data_quality_scan_batches
WHERE id::text IN (
  SELECT md5('netassets-demo:quality-batch:' || g::text)::uuid::text FROM generate_series(1, 3) AS g
);
DELETE FROM data_quality_rules
WHERE id::text IN (
  SELECT md5('netassets-demo:quality-rule:' || g::text)::uuid::text FROM generate_series(1, 3) AS g
);

DELETE FROM security_work_orders WHERE work_order_no LIKE 'DEMO-WO-%';
DELETE FROM security_findings WHERE finding_code LIKE 'DEMO-FIND-%';
DELETE FROM security_test_batches WHERE batch_code LIKE 'DEMO-BATCH-%';

DELETE FROM inventory_tasks WHERE task_name LIKE '[演示]%';

DELETE FROM asset_candidates WHERE source_reference LIKE 'DEMO-CAND-%';

DELETE FROM business_systems WHERE system_code LIKE 'DEMO-SYS-%';
DELETE FROM assets WHERE extra_data ->> 'demo_seed' = 'netassets-demo-v1';
DELETE FROM asset_network_areas WHERE area_code LIKE 'DEMO-%';

DELETE FROM users WHERE username LIKE 'demo_%';
DELETE FROM role_permissions WHERE role_id IN (SELECT id FROM roles WHERE role_key LIKE 'demo_%');
DELETE FROM roles WHERE role_key LIKE 'demo_%';

-- 一次删除整棵 DEMO 组织树；约束在语句结束时检查。
DELETE FROM organizations WHERE organization_code LIKE 'DEMO\_%' ESCAPE '\';

COMMIT;
