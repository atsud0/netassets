#!/bin/sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT_DIR"

BACKUP_ROOT=${1:-"$ROOT_DIR/backups"}
TIMESTAMP=$(date -u '+%Y%m%dT%H%M%SZ')
BACKUP_DIR="$BACKUP_ROOT/netassets-$TIMESTAMP"
DATABASE_FILE="$BACKUP_DIR/database.dump"
DATA_FILE="$BACKUP_DIR/app-data.tar.gz"
MANIFEST_FILE="$BACKUP_DIR/manifest.txt"
APP_WAS_RUNNING=0
APP_STOPPED=0

checksum() {
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	else
		echo "缺少 shasum 或 sha256sum，无法生成备份校验值" >&2
		exit 1
	fi
}

restart_app() {
	if [ "$APP_WAS_RUNNING" -eq 1 ] && [ "$APP_STOPPED" -eq 1 ]; then
		podman compose up -d app >/dev/null
		APP_STOPPED=0
	fi
}

trap restart_app EXIT INT TERM

if ! command -v podman >/dev/null 2>&1; then
	echo "未找到 podman" >&2
	exit 1
fi

POSTGRES_ID=$(podman compose ps -q postgres)
if [ -z "$POSTGRES_ID" ] || [ "$(podman inspect --format '{{.State.Running}}' "$POSTGRES_ID")" != "true" ]; then
	echo "PostgreSQL 容器未运行，请先执行 podman compose up -d postgres" >&2
	exit 1
fi

APP_ID=$(podman compose ps -q app 2>/dev/null || true)
if [ -n "$APP_ID" ] && [ "$(podman inspect --format '{{.State.Running}}' "$APP_ID")" = "true" ]; then
	APP_WAS_RUNNING=1
	echo "停止应用容器以创建数据库与附件一致性备份..."
	podman compose stop app >/dev/null
	APP_STOPPED=1
fi

mkdir -p "$BACKUP_DIR"
chmod 700 "$BACKUP_DIR"

echo "导出 PostgreSQL 数据库..."
podman compose exec -T postgres sh -c 'exec pg_dump --username="$POSTGRES_USER" --dbname="$POSTGRES_DB" --format=custom --compress=6 --no-owner --no-privileges' > "$DATABASE_FILE"

echo "归档应用附件数据..."
podman compose run --rm --no-deps -T app sh -c 'exec tar -C /data -czf - .' > "$DATA_FILE"

DATABASE_SHA256=$(checksum "$DATABASE_FILE")
DATA_SHA256=$(checksum "$DATA_FILE")
cat > "$MANIFEST_FILE" <<MANIFEST
format_version=1
created_at_utc=$TIMESTAMP
database_file=database.dump
database_sha256=$DATABASE_SHA256
app_data_file=app-data.tar.gz
app_data_sha256=$DATA_SHA256
MANIFEST
chmod 600 "$DATABASE_FILE" "$DATA_FILE" "$MANIFEST_FILE"

# Validate that both archives can be read before reporting success.
podman compose exec -T postgres pg_restore --list < "$DATABASE_FILE" >/dev/null
tar -tzf "$DATA_FILE" >/dev/null

restart_app
trap - EXIT INT TERM

printf '\n备份完成：%s\n' "$BACKUP_DIR"
printf '包含：PostgreSQL 自定义格式备份、应用附件归档、SHA-256 校验清单。\n'
