SHELL := /bin/sh
COMPOSE := podman compose

.PHONY: help up down build logs ps test test-backend test-frontend e2e fmt migrate-up migrate-down backup restore-verify restore cleanup-e2e

help:
	@printf '%s\n' \
	  'make up             Start the application with Podman Compose' \
	  'make down           Stop the application' \
	  'make build          Build application images' \
	  'make logs           Follow application logs' \
	  'make test           Run backend and frontend unit tests' \
	  'make e2e            Run Playwright tests in a container' \
	  'make migrate-up     Apply database migrations' \
	  'make migrate-down   Roll back one migration' \
	  'make backup         Back up PostgreSQL and application attachments' \
	  'make restore-verify BACKUP=path  Verify a backup without restoring' \
	  'make restore BACKUP=path NETASSETS_RESTORE_CONFIRM=YES  Restore a backup' \
	  'make cleanup-e2e   Preview removable local E2E test data'

up:
	$(COMPOSE) up -d --build

down:
	$(COMPOSE) down

build:
	$(COMPOSE) build

logs:
	$(COMPOSE) logs -f

ps:
	$(COMPOSE) ps

test: test-backend test-frontend

test-backend:
	cd backend && go test ./...

test-frontend:
	podman run --rm -v "$$(pwd)/web:/workspace:Z" -w /workspace node:22-alpine sh -lc 'npm ci && npm test -- --run'

e2e:
	NETASSETS_E2E_CLEANUP_CONFIRM=YES ./scripts/cleanup-e2e-data.sh --apply
	@status=0; podman compose --profile e2e run --rm playwright || status=$$?; \
	  NETASSETS_E2E_CLEANUP_CONFIRM=YES ./scripts/cleanup-e2e-data.sh --apply; \
	  rm -rf e2e/test-results; \
	  exit $$status

fmt:
	cd backend && gofmt -w ./cmd ./internal

migrate-up:
	$(COMPOSE) run --rm app /app/netassets-migrate up

migrate-down:
	$(COMPOSE) run --rm app /app/netassets-migrate down

backup:
	./scripts/backup.sh

restore-verify:
	@test -n "$(BACKUP)" || (echo "请通过 BACKUP=备份目录 指定待校验备份" >&2; exit 1)
	./scripts/restore.sh --verify-only "$(BACKUP)"

restore:
	@test -n "$(BACKUP)" || (echo "请通过 BACKUP=备份目录 指定待恢复备份" >&2; exit 1)
	./scripts/restore.sh "$(BACKUP)"

cleanup-e2e:
	./scripts/cleanup-e2e-data.sh --dry-run
