#!/bin/sh
set -eu

ACTION="${1:-seed}"
CONNECTION="${PODMAN_CONNECTION:-podman-machine-default-root}"
PROJECT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

case "$ACTION" in
  seed)
    cat "$PROJECT_DIR/scripts/cleanup-demo-data.sql" "$PROJECT_DIR/scripts/seed-demo-data.sql" |
      podman --connection "$CONNECTION" compose -f "$PROJECT_DIR/compose.yaml" exec -T postgres \
        psql -U "${POSTGRES_USER:-netassets}" -d "${POSTGRES_DB:-netassets}" -v ON_ERROR_STOP=1
    ;;
  clean)
    cat "$PROJECT_DIR/scripts/cleanup-demo-data.sql" |
      podman --connection "$CONNECTION" compose -f "$PROJECT_DIR/compose.yaml" exec -T postgres \
        psql -U "${POSTGRES_USER:-netassets}" -d "${POSTGRES_DB:-netassets}" -v ON_ERROR_STOP=1
    ;;
  *)
    echo "用法: $0 [seed|clean]" >&2
    exit 2
    ;;
esac
