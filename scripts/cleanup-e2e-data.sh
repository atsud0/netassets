#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

MODE=${1:---dry-run}
case "$MODE" in
  --dry-run|--apply) ;;
  *)
    echo "用法：$0 [--dry-run|--apply]" >&2
    exit 1
    ;;
esac

if ! command -v podman >/dev/null 2>&1; then
  echo "未找到 podman" >&2
  exit 1
fi

POSTGRES_ID=$(podman compose ps -q postgres)
if [ -z "$POSTGRES_ID" ] || [ "$(podman inspect --format '{{.State.Running}}' "$POSTGRES_ID")" != "true" ]; then
  echo "PostgreSQL 容器未运行，请先执行 podman compose up -d postgres" >&2
  exit 1
fi

run_psql() {
  podman compose exec -T postgres sh -c 'exec psql --quiet --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -v ON_ERROR_STOP=1 "$@"' -- "$@"
}

common_targets_sql() {
  cat <<'SQL'
CREATE TEMP TABLE e2e_organizations ON COMMIT DROP AS
  SELECT id
  FROM organizations
  WHERE organization_code ~ '^(SUB|SCOPE|ROLE)_PW_[0-9]+$';

CREATE TEMP TABLE e2e_tasks ON COMMIT DROP AS
  SELECT id
  FROM inventory_tasks
  WHERE task_name ~ '^TASK_PW_[0-9]+$';

CREATE TEMP TABLE e2e_batches ON COMMIT DROP AS
  SELECT id
  FROM security_test_batches
  WHERE batch_name ~ '^安全测试批次_PW_[0-9]+$';

CREATE TEMP TABLE e2e_work_orders ON COMMIT DROP AS
  SELECT id
  FROM security_work_orders
  WHERE title ~ '^互联网资产高危漏洞整改_PW_[0-9]+$';

CREATE TEMP TABLE e2e_findings ON COMMIT DROP AS
  SELECT id
  FROM security_findings
  WHERE title ~ '^(未授权访问漏洞|批量导入漏洞)_PW_[0-9]+$'
     OR assignee_organization_id IN (SELECT id FROM e2e_organizations)
     OR test_batch_id IN (SELECT id FROM e2e_batches);

CREATE TEMP TABLE e2e_assets ON COMMIT DROP AS
  SELECT DISTINCT a.id
  FROM assets a
  LEFT JOIN asset_organizations ao ON ao.asset_id = a.id
  WHERE ao.organization_id IN (SELECT id FROM e2e_organizations)
     OR a.asset_value ~ '^(group-pw|restore-pw|history-pw)-[0-9]+[.]example[.]com$'
     OR a.asset_name ~ '^(集团权限测试资产|回收站测试资产|历史测试资产|批量测试资产)_[0-9]+';

CREATE TEMP TABLE e2e_systems ON COMMIT DROP AS
  SELECT id
  FROM business_systems
  WHERE owner_organization_id IN (SELECT id FROM e2e_organizations)
     OR system_code ~ '^(SYS_PW|HISTORY|BATCH_SYS)_[0-9]+';

CREATE TEMP TABLE e2e_network_areas ON COMMIT DROP AS
  SELECT id
  FROM asset_network_areas
  WHERE organization_id IN (SELECT id FROM e2e_organizations)
     OR area_name ~ '^(上海办事处_PW|深圳办事处_PW|批量测试区域)_[0-9]+';

CREATE TEMP TABLE e2e_users ON COMMIT DROP AS
  SELECT id
  FROM users
  WHERE username ~ '^(pw_user|role_pw_(security|admin|editor))_[0-9]+$';

CREATE TEMP TABLE e2e_custom_fields ON COMMIT DROP AS
  SELECT id
  FROM custom_field_definitions
  WHERE field_key ~ '^pw_quality_[0-9]+$';

CREATE TEMP TABLE e2e_quality_rules ON COMMIT DROP AS
  SELECT id
  FROM data_quality_rules
  WHERE rule_name ~ '^DATA_QUALITY_PW_(STALE_)?[0-9]+$'
     OR target_field ~ '^custom:pw_quality_[0-9]+$'
     OR target_field IN (SELECT 'custom:' || field_key FROM custom_field_definitions WHERE id IN (SELECT id FROM e2e_custom_fields));

CREATE TEMP TABLE e2e_quality_batches ON COMMIT DROP AS
  SELECT id
  FROM data_quality_scan_batches
  WHERE organization_id IN (SELECT id FROM e2e_organizations)
     OR id::text IN (
       SELECT resource_id FROM audit_logs
       WHERE request_id LIKE 'e2e-data-quality-%' AND resource_type='data_quality_scan_batch'
     );

CREATE TEMP TABLE e2e_quality_issues ON COMMIT DROP AS
  SELECT id
  FROM data_quality_issues
  WHERE organization_id IN (SELECT id FROM e2e_organizations)
     OR resource_id IN (SELECT id FROM e2e_assets)
     OR resource_id IN (SELECT id FROM e2e_systems)
     OR rule_id IN (SELECT id FROM e2e_quality_rules)
     OR first_detected_batch_id IN (SELECT id FROM e2e_quality_batches)
     OR last_detected_batch_id IN (SELECT id FROM e2e_quality_batches);

CREATE TEMP TABLE e2e_asset_imports ON COMMIT DROP AS
  SELECT id
  FROM asset_imports
  WHERE organization_id IN (SELECT id FROM e2e_organizations);

CREATE TEMP TABLE e2e_system_imports ON COMMIT DROP AS
  SELECT id
  FROM business_system_imports
  WHERE organization_id IN (SELECT id FROM e2e_organizations);

CREATE TEMP TABLE e2e_finding_imports ON COMMIT DROP AS
  SELECT id
  FROM security_finding_imports
  WHERE test_batch_id IN (SELECT id FROM e2e_batches);

CREATE TEMP TABLE e2e_resource_ids ON COMMIT DROP AS
  SELECT id::text AS id FROM e2e_organizations
  UNION SELECT id::text FROM e2e_tasks
  UNION SELECT id::text FROM e2e_batches
  UNION SELECT id::text FROM e2e_work_orders
  UNION SELECT id::text FROM e2e_findings
  UNION SELECT id::text FROM e2e_assets
  UNION SELECT id::text FROM e2e_systems
  UNION SELECT id::text FROM e2e_network_areas
  UNION SELECT id::text FROM e2e_users
  UNION SELECT id::text FROM e2e_asset_imports
  UNION SELECT id::text FROM e2e_system_imports
  UNION SELECT id::text FROM e2e_finding_imports
  UNION SELECT id::text FROM e2e_custom_fields
  UNION SELECT id::text FROM e2e_quality_rules
  UNION SELECT id::text FROM e2e_quality_batches
  UNION SELECT id::text FROM e2e_quality_issues;
SQL
}

report_sql() {
  common_targets_sql
  cat <<'SQL'
SELECT 'organizations' AS entity, count(*) AS matching FROM e2e_organizations
UNION ALL SELECT 'inventory_tasks', count(*) FROM e2e_tasks
UNION ALL SELECT 'security_test_batches', count(*) FROM e2e_batches
UNION ALL SELECT 'security_work_orders', count(*) FROM e2e_work_orders
UNION ALL SELECT 'security_findings', count(*) FROM e2e_findings
UNION ALL SELECT 'assets', count(*) FROM e2e_assets
UNION ALL SELECT 'business_systems', count(*) FROM e2e_systems
UNION ALL SELECT 'network_areas', count(*) FROM e2e_network_areas
UNION ALL SELECT 'users', count(*) FROM e2e_users
UNION ALL SELECT 'asset_imports', count(*) FROM e2e_asset_imports
UNION ALL SELECT 'business_system_imports', count(*) FROM e2e_system_imports
UNION ALL SELECT 'security_finding_imports', count(*) FROM e2e_finding_imports
UNION ALL SELECT 'custom_field_definitions', count(*) FROM e2e_custom_fields
UNION ALL SELECT 'custom_field_values', count(*) FROM custom_field_values WHERE field_definition_id IN (SELECT id FROM e2e_custom_fields) OR resource_id IN (SELECT id FROM e2e_assets) OR resource_id IN (SELECT id FROM e2e_systems)
UNION ALL SELECT 'data_quality_rules', count(*) FROM e2e_quality_rules
UNION ALL SELECT 'data_quality_scan_batches', count(*) FROM e2e_quality_batches
UNION ALL SELECT 'data_quality_issues', count(*) FROM e2e_quality_issues
UNION ALL SELECT 'data_quality_issue_events', count(*) FROM data_quality_issue_events WHERE issue_id IN (SELECT id FROM e2e_quality_issues)
UNION ALL SELECT 'data_quality_scan_batch_issues', count(*) FROM data_quality_scan_batch_issues WHERE issue_id IN (SELECT id FROM e2e_quality_issues) OR batch_id IN (SELECT id FROM e2e_quality_batches)
UNION ALL SELECT 'audit_logs', count(*)
  FROM audit_logs
  WHERE organization_id IN (SELECT id FROM e2e_organizations)
     OR user_id IN (SELECT id FROM e2e_users)
     OR resource_id IN (SELECT id FROM e2e_resource_ids)
UNION ALL SELECT 'backup_audit_logs', count(*)
  FROM audit_logs
  WHERE request_id LIKE 'e2e-backup-%'
UNION ALL SELECT 'data_quality_scan_audit_logs', count(*)
  FROM audit_logs
  WHERE request_id LIKE 'e2e-data-quality-%'
ORDER BY entity;
SQL
}

attachment_paths_sql() {
  cat <<'SQL'
SELECT storage_path
FROM security_work_order_attachments
WHERE work_order_id IN (
  SELECT id FROM security_work_orders
  WHERE title ~ '^互联网资产高危漏洞整改_PW_[0-9]+$'
)
UNION ALL
SELECT storage_path
FROM security_test_batch_attachments
WHERE batch_id IN (
  SELECT id FROM security_test_batches
  WHERE batch_name ~ '^安全测试批次_PW_[0-9]+$'
);
SQL
}

apply_sql() {
  common_targets_sql
  cat <<'SQL'
DELETE FROM audit_logs
WHERE organization_id IN (SELECT id FROM e2e_organizations)
   OR user_id IN (SELECT id FROM e2e_users)
   OR resource_id IN (SELECT id FROM e2e_resource_ids)
   OR request_id LIKE 'e2e-backup-%'
   OR request_id LIKE 'e2e-data-quality-%';

DELETE FROM data_quality_issues WHERE id IN (SELECT id FROM e2e_quality_issues);
DELETE FROM data_quality_scan_batches WHERE id IN (SELECT id FROM e2e_quality_batches);
DELETE FROM data_quality_rules WHERE id IN (SELECT id FROM e2e_quality_rules);
DELETE FROM custom_field_values
WHERE field_definition_id IN (SELECT id FROM e2e_custom_fields)
   OR resource_id IN (SELECT id FROM e2e_assets)
   OR resource_id IN (SELECT id FROM e2e_systems);
DELETE FROM custom_field_definitions WHERE id IN (SELECT id FROM e2e_custom_fields);

DELETE FROM security_work_orders WHERE id IN (SELECT id FROM e2e_work_orders);
DELETE FROM security_findings WHERE id IN (SELECT id FROM e2e_findings);
DELETE FROM security_test_batches WHERE id IN (SELECT id FROM e2e_batches);
DELETE FROM inventory_tasks WHERE id IN (SELECT id FROM e2e_tasks);
DELETE FROM business_systems WHERE id IN (SELECT id FROM e2e_systems);
DELETE FROM assets WHERE id IN (SELECT id FROM e2e_assets);
DELETE FROM asset_network_areas WHERE id IN (SELECT id FROM e2e_network_areas);
DELETE FROM organizations WHERE id IN (SELECT id FROM e2e_organizations);
DELETE FROM users WHERE id IN (SELECT id FROM e2e_users);
SQL
}

printf '%s\n' '识别到的 E2E 测试数据：'
{
  printf '%s\n' 'BEGIN;'
  report_sql
  printf '%s\n' 'ROLLBACK;'
} | run_psql

if [ "$MODE" = "--dry-run" ]; then
  cat <<'NOTICE'

当前为预览模式，未删除任何数据。
执行清理前建议先运行：./scripts/backup.sh
确认后执行：NETASSETS_E2E_CLEANUP_CONFIRM=YES ./scripts/cleanup-e2e-data.sh --apply
NOTICE
  exit 0
fi

if [ "${NETASSETS_E2E_CLEANUP_CONFIRM:-}" != "YES" ]; then
  cat >&2 <<'WARNING'
拒绝执行：该操作会永久删除带有严格 E2E 测试标识的数据、其关联附件和可归属的审计记录。
确认已完成备份后，请设置 NETASSETS_E2E_CLEANUP_CONFIRM=YES 再执行。
WARNING
  exit 2
fi

TMP_PATHS=$(mktemp "${TMPDIR:-/tmp}/netassets-e2e-attachments.XXXXXX")
trap 'rm -f "$TMP_PATHS"' EXIT INT TERM
attachment_paths_sql | run_psql -At > "$TMP_PATHS"

printf '%s\n' '开始清理 E2E 测试数据库记录...'
{
  printf '%s\n' 'BEGIN;'
  apply_sql
  printf '%s\n' 'COMMIT;'
} | run_psql

if [ -s "$TMP_PATHS" ]; then
  printf '%s\n' '删除关联的 E2E 测试附件文件...'
  podman compose run --rm --no-deps -T app sh -c '
    set -eu
    while IFS= read -r storage_path; do
      case "$storage_path" in
        ""|/*|../*|*"/../"*|*"//"*)
          echo "发现不安全的附件路径，拒绝删除：$storage_path" >&2
          exit 1
          ;;
      esac
      rm -f "/data/$storage_path"
    done
  ' < "$TMP_PATHS"
fi

printf '\n%s\n' '清理完成，剩余可识别的 E2E 测试数据：'
{
  printf '%s\n' 'BEGIN;'
  report_sql
  printf '%s\n' 'ROLLBACK;'
} | run_psql
