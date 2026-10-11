#!/bin/sh
# NetAssets 生产上线只读验收。该脚本不创建、修改或删除任何业务数据。
set -u

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

MODE=${1:-}
ENV_FILE=${ENV_FILE:-.env}
BASE_URL=${BASE_URL:-http://127.0.0.1:8080}
FAILURES=0
WARNINGS=0

usage() {
  cat <<USAGE
用法：
  $0 --preflight   检查生产配置和 Compose 定义（不要求容器已启动）
  $0 --running     检查运行状态、健康端点、迁移、内置角色、持久化卷和备份

可选环境变量：
  ENV_FILE=/path/to/.env
  BASE_URL=https://assets.example.com
USAGE
}

pass() { printf '  [通过] %s\n' "$*"; }
warn() { printf '  [警告] %s\n' "$*"; WARNINGS=$((WARNINGS + 1)); }
fail() { printf '  [失败] %s\n' "$*"; FAILURES=$((FAILURES + 1)); }

require_command() {
  if command -v "$1" >/dev/null 2>&1; then pass "已安装命令：$1"; else fail "缺少命令：$1"; fi
}

load_env() {
  if [ ! -f "$ENV_FILE" ]; then
    fail "环境文件不存在：$ENV_FILE"
    return 1
  fi
  set -a
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  set +a
  pass "已读取环境文件：$ENV_FILE"
}

check_secret() {
  name=$1
  value=$2
  min_length=$3
  if [ -z "$value" ]; then
    fail "$name 未配置"
    return
  fi
  if [ "${#value}" -lt "$min_length" ]; then
    fail "$name 长度不足 $min_length 位"
    return
  fi
  case $(printf '%s' "$value" | tr '[:upper:]' '[:lower:]') in
    netassets|password|password123|changeme|admin123456|请替换为至少10位的强密码)
      fail "$name 仍使用默认或示例密码"
      return
      ;;
  esac
  pass "$name 已配置且未命中弱默认值"
}

compose_container_id() {
  podman compose --env-file "$ENV_FILE" ps -q "$1" 2>/dev/null || true
}

check_container() {
  service=$1
  container_id=$(compose_container_id "$service")
  if [ -z "$container_id" ]; then
    fail "$service 容器不存在"
    return 1
  fi
  running=$(podman inspect --format '{{.State.Running}}' "$container_id" 2>/dev/null || printf false)
  health=$(podman inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$container_id" 2>/dev/null || printf unknown)
  if [ "$running" != "true" ]; then
    fail "$service 容器未运行"
    return 1
  fi
  if [ "$health" = "healthy" ]; then
    pass "$service 容器运行且健康"
  elif [ "$health" = "none" ]; then
    warn "$service 容器运行，但未配置健康检查"
  else
    fail "$service 容器健康状态为：$health"
  fi
}

http_check() {
  path=$1
  label=$2
  code=$(curl -k -sS -o /tmp/netassets-acceptance-body.$$ -w '%{http_code}' "${BASE_URL%/}$path" 2>/dev/null || printf 000)
  if [ "$code" = "200" ]; then
    pass "${label}：$path"
  else
    fail "${label}失败：$path 返回 HTTP $code"
  fi
  rm -f /tmp/netassets-acceptance-body.$$
}

preflight() {
  printf '%s\n' 'NetAssets 生产上线配置预检'
  require_command podman
  load_env || true

  if [ "${APP_ENV:-}" = "production" ]; then pass 'APP_ENV=production'; else fail "APP_ENV 必须为 production，当前为 ${APP_ENV:-未配置}"; fi
  if [ "${SESSION_COOKIE_SECURE:-}" = "true" ]; then pass 'SESSION_COOKIE_SECURE=true'; else fail '生产环境必须启用 SESSION_COOKIE_SECURE=true'; fi
  check_secret POSTGRES_PASSWORD "${POSTGRES_PASSWORD:-}" 16
  check_secret BOOTSTRAP_ADMIN_PASSWORD "${BOOTSTRAP_ADMIN_PASSWORD:-}" 12

  if [ -n "${BOOTSTRAP_GROUP_NAME:-}" ] && [ "${BOOTSTRAP_GROUP_NAME:-}" != "示例集团" ]; then
    pass '集团名称已替换示例值'
  else
    fail 'BOOTSTRAP_GROUP_NAME 未配置或仍为“示例集团”'
  fi
  if [ -n "${BOOTSTRAP_ADMIN_USERNAME:-}" ] && [ "${BOOTSTRAP_ADMIN_USERNAME:-}" != "admin" ]; then
    pass '初始化管理员用户名已替换默认值'
  else
    warn '初始化管理员用户名仍为 admin；如保留，应配合强密码、访问控制和登录监控'
  fi

  case "${DATABASE_URL:-}" in
    '') fail 'DATABASE_URL 未配置' ;;
    *'netassets:netassets@'*) fail 'DATABASE_URL 仍包含默认数据库口令' ;;
    *'sslmode=disable'*) warn 'DATABASE_URL 使用 sslmode=disable；仅当 PostgreSQL 位于受控的本机容器网络时可接受' ;;
    *) pass 'DATABASE_URL 已配置且未命中默认口令' ;;
  esac

  if podman compose --env-file "$ENV_FILE" config >/dev/null 2>&1; then
    pass 'podman compose 配置可解析'
  else
    fail 'podman compose 配置解析失败'
  fi

  case "$BASE_URL" in
    https://*) pass '外部验收地址使用 HTTPS' ;;
    http://127.0.0.1:*|http://localhost:*) warn '当前按本机地址验收；正式对外地址仍需单独验证 HTTPS、证书链和反向代理' ;;
    *) fail '生产外部验收地址应使用 HTTPS' ;;
  esac
}

running() {
  printf '%s\n' 'NetAssets 生产运行状态验收'
  require_command podman
  require_command curl
  load_env || true
  check_container postgres || true
  check_container app || true

  http_check /health/live '存活检查'
  http_check /health/ready '就绪检查'
  http_check /api/v1/system/info '系统信息接口'

  expected_migration=$(find backend/migrations -maxdepth 1 -type f -name '0*.sql' -exec basename {} \; | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -1)
  migration_version=$(podman compose --env-file "$ENV_FILE" exec -T postgres sh -c \
    'exec psql --quiet --tuples-only --no-align --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -v ON_ERROR_STOP=1 -c "SELECT COALESCE(max(version_id) FILTER (WHERE is_applied),0) FROM goose_db_version"' 2>/dev/null | tr -d '[:space:]')
  if [ -n "$expected_migration" ] && [ "$migration_version" = "$expected_migration" ]; then
    pass "数据库迁移版本为 $migration_version"
  else
    fail "数据库迁移版本为 ${migration_version:-不可读取}，代码要求 ${expected_migration:-不可识别}"
  fi

  boundary_sql="SELECT (SELECT count(*) FROM roles WHERE role_key IN ('group_admin','security_operator','organization_admin','organization_editor')) || '|' || (SELECT count(*) FROM permissions WHERE permission_key IN ('security_test.manage','security_finding.manage','security_workorder.manage')) || '|' || (SELECT count(*) FROM role_permissions rp JOIN roles r ON r.id=rp.role_id WHERE r.role_key='security_operator' AND rp.permission_key IN ('security_test.manage','security_finding.manage','security_workorder.manage'))"
  boundary_result=$(podman compose --env-file "$ENV_FILE" exec -T postgres sh -c \
    'exec psql --quiet --tuples-only --no-align --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" -v ON_ERROR_STOP=1 -c "$1"' -- "$boundary_sql" 2>/dev/null | tr -d '[:space:]')
  if [ "$boundary_result" = '4|3|3' ]; then
    pass '四个内置角色及集团安全运营三项权限映射完整'
  else
    fail "内置角色或权限映射不完整：${boundary_result:-不可读取}，预期 4|3|3"
  fi

  postgres_id=$(compose_container_id postgres)
  app_id=$(compose_container_id app)
  postgres_mount=$(podman inspect --format '{{range .Mounts}}{{if eq .Destination "/var/lib/postgresql/data"}}{{.Type}}:{{.Name}}{{end}}{{end}}' "$postgres_id" 2>/dev/null || true)
  app_mount=$(podman inspect --format '{{range .Mounts}}{{if eq .Destination "/data"}}{{.Type}}:{{.Name}}{{end}}{{end}}' "$app_id" 2>/dev/null || true)
  if [ -n "$postgres_mount" ]; then pass "PostgreSQL 数据目录已持久化：$postgres_mount"; else fail 'PostgreSQL 数据目录未发现持久化挂载'; fi
  if [ -n "$app_mount" ]; then pass "应用数据目录已持久化：$app_mount"; else fail '应用 /data 未发现持久化挂载'; fi

  backup_summary=$(podman compose --env-file "$ENV_FILE" exec -T app sh -c '
    set -eu
    if [ ! -d /data/backups ]; then printf "0||"; exit 0; fi
    count=$(find /data/backups -mindepth 1 -maxdepth 1 -type d ! -name ".creating-*" | wc -l | tr -d " ")
    latest=$(find /data/backups -mindepth 2 -maxdepth 2 -name manifest.txt -type f -print0 2>/dev/null | xargs -0 -r ls -1t 2>/dev/null | head -1 || true)
    if [ -n "$latest" ]; then created=$(sed -n "s/^created_at_utc=//p" "$latest" | head -1); else created=""; fi
    printf "%s|%s|%s" "$count" "$latest" "$created"
  ' 2>/dev/null || printf '0||')
  backup_count=$(printf '%s' "$backup_summary" | cut -d '|' -f 1)
  backup_manifest=$(printf '%s' "$backup_summary" | cut -d '|' -f 2)
  backup_created=$(printf '%s' "$backup_summary" | cut -d '|' -f 3)
  if [ "${backup_count:-0}" -gt 0 ] 2>/dev/null && [ -n "$backup_manifest" ]; then
    pass "发现 $backup_count 个应用备份，最近备份时间：${backup_created:-清单未记录}"
  else
    # make backup stores consistent database + attachment archives on the host.
    # Accept that documented path only after the restore tool verifies it.
    host_backup_manifest=''
    for candidate in "$ROOT_DIR"/backups/netassets-*/manifest.txt; do
      [ -f "$candidate" ] || continue
      if [ -z "$host_backup_manifest" ] || [ "$candidate" -nt "$host_backup_manifest" ]; then
        host_backup_manifest=$candidate
      fi
    done
    if [ -n "$host_backup_manifest" ]; then
      host_backup_dir=$(dirname "$host_backup_manifest")
      if "$ROOT_DIR/scripts/restore.sh" --verify-only "$host_backup_dir" >/dev/null 2>&1; then
        pass "宿主机备份校验通过：$host_backup_dir"
      else
        fail "宿主机最近备份校验失败：$host_backup_dir"
      fi
    else
      fail '未发现应用或宿主机备份；上线前必须完成备份和恢复演练'
    fi
  fi
}

case "$MODE" in
  --preflight) preflight ;;
  --running) running ;;
  *) usage; exit 2 ;;
esac

printf '\n验收脚本结果：失败 %d 项，警告 %d 项。\n' "$FAILURES" "$WARNINGS"
if [ "$FAILURES" -gt 0 ]; then exit 1; fi
