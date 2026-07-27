#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

VERIFY_ONLY=0
if [ "${1:-}" = "--verify-only" ]; then
	VERIFY_ONLY=1
	shift
fi

BACKUP_DIR=${1:-}
if [ -z "$BACKUP_DIR" ]; then
	echo "用法：$0 [--verify-only] <备份目录>" >&2
	exit 1
fi
case "$BACKUP_DIR" in
	/*) ;;
	*) BACKUP_DIR="$ROOT_DIR/$BACKUP_DIR" ;;
esac

MANIFEST_FILE="$BACKUP_DIR/manifest.txt"
DATABASE_FILE="$BACKUP_DIR/database.dump"
DATA_FILE="$BACKUP_DIR/app-data.tar.gz"

for file in "$MANIFEST_FILE" "$DATABASE_FILE" "$DATA_FILE"; do
	if [ ! -f "$file" ]; then
		echo "备份文件缺失：$file" >&2
		exit 1
	fi
done

checksum() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		echo "缺少 shasum 或 sha256sum，无法校验备份" >&2
		exit 1
	fi
}

manifest_value() {
	key=$1
	sed -n "s/^${key}=//p" "$MANIFEST_FILE" | tail -n 1
}

if [ "$(manifest_value format_version)" != "1" ]; then
	echo "不支持的备份格式版本" >&2
	exit 1
fi
EXPECTED_DATABASE_SHA256=$(manifest_value database_sha256)
EXPECTED_DATA_SHA256=$(manifest_value app_data_sha256)
ACTUAL_DATABASE_SHA256=$(checksum "$DATABASE_FILE")
ACTUAL_DATA_SHA256=$(checksum "$DATA_FILE")
if [ -z "$EXPECTED_DATABASE_SHA256" ] || [ "$EXPECTED_DATABASE_SHA256" != "$ACTUAL_DATABASE_SHA256" ]; then
	echo "数据库备份 SHA-256 校验失败" >&2
	exit 1
fi
if [ -z "$EXPECTED_DATA_SHA256" ] || [ "$EXPECTED_DATA_SHA256" != "$ACTUAL_DATA_SHA256" ]; then
	echo "附件备份 SHA-256 校验失败" >&2
	exit 1
fi

POSTGRES_ID=$(podman compose ps -q postgres)
if [ -z "$POSTGRES_ID" ] || [ "$(podman inspect --format '{{.State.Running}}' "$POSTGRES_ID")" != "true" ]; then
	echo "PostgreSQL 容器未运行，请先执行 podman compose up -d postgres" >&2
	exit 1
fi

podman compose exec -T postgres pg_restore --list < "$DATABASE_FILE" >/dev/null
tar -tzf "$DATA_FILE" >/dev/null
printf '备份校验通过：%s\n' "$BACKUP_DIR"

if [ "$VERIFY_ONLY" -eq 1 ]; then
	exit 0
fi

if [ "${NETASSETS_RESTORE_CONFIRM:-}" != "YES" ]; then
	cat >&2 <<'WARNING'
恢复操作会覆盖当前数据库和全部附件数据。
确认已理解风险后，请设置 NETASSETS_RESTORE_CONFIRM=YES 再执行。
默认会在恢复前自动创建一份安全备份；如确需跳过，设置 NETASSETS_SKIP_PRE_RESTORE_BACKUP=YES。
WARNING
	exit 2
fi

if [ "${NETASSETS_SKIP_PRE_RESTORE_BACKUP:-}" != "YES" ]; then
	echo "创建恢复前安全备份..."
	"$ROOT_DIR/scripts/backup.sh" "${NETASSETS_PRE_RESTORE_BACKUP_ROOT:-$ROOT_DIR/backups/pre-restore}"
fi

APP_WAS_RUNNING=0
APP_STOPPED=0
restart_app() {
	if [ "$APP_WAS_RUNNING" -eq 1 ] && [ "$APP_STOPPED" -eq 1 ]; then
		podman compose up -d app >/dev/null
		APP_STOPPED=0
	fi
}
trap restart_app EXIT INT TERM

APP_ID=$(podman compose ps -q app 2>/dev/null || true)
if [ -n "$APP_ID" ] && [ "$(podman inspect --format '{{.State.Running}}' "$APP_ID")" = "true" ]; then
	APP_WAS_RUNNING=1
	echo "停止应用容器..."
	podman compose stop app >/dev/null
	APP_STOPPED=1
fi

echo "恢复 PostgreSQL 数据库..."
podman compose exec -T postgres sh -c 'exec pg_restore --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --clean --if-exists --no-owner --no-privileges --exit-on-error' < "$DATABASE_FILE"

echo "恢复应用附件数据..."
podman compose run --rm --no-deps -T app sh -c 'find /data -mindepth 1 -maxdepth 1 -exec rm -rf {} +; exec tar -C /data -xzf -' < "$DATA_FILE"

restart_app
trap - EXIT INT TERM
printf '\n恢复完成。请执行 curl http://127.0.0.1:8080/health/ready 检查服务状态。\n'
