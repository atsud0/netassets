SHELL := /bin/sh
COMPOSE := podman compose
E2E_COMPOSE := podman compose -p netassets-e2e -f compose.e2e.yaml
DEV_COMPOSE := podman compose -p netassets-dev -f compose.dev.yaml

.PHONY: help up down build logs ps test test-backend test-frontend e2e e2e-roles acceptance-preflight acceptance-running fmt migrate-up migrate-down backup restore-verify restore cleanup-e2e

help:
	@printf '%s\n' \
	  'make up             Start the application with Podman Compose' \
	  'make down           Stop the application' \
	  'make build          Build application images' \
	  'make logs           Follow application logs' \
	  'make test           Run tests and frontend build in isolated Podman containers' \
	  'make fmt            Format Go code inside Podman' \
	  'make e2e            Run Playwright tests in a container' \
	  'make e2e-roles      Run all built-in role boundary regression tests' \
	  'make acceptance-preflight  Validate production configuration' \
	  'make acceptance-running    Validate a running production deployment' \
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
	$(DEV_COMPOSE) run --rm backend

test-frontend:
	$(DEV_COMPOSE) run --rm web

e2e:
	@$(E2E_COMPOSE) down -v --remove-orphans >/dev/null 2>&1 || true
	@status=0; $(E2E_COMPOSE) build app && $(E2E_COMPOSE) run --rm playwright || status=$$?; \
	  $(E2E_COMPOSE) down -v --remove-orphans >/dev/null 2>&1 || true; \
	  rm -rf e2e/test-results; \
	  exit $$status

e2e-roles:
	@$(E2E_COMPOSE) down -v --remove-orphans >/dev/null 2>&1 || true
	@status=0; $(E2E_COMPOSE) build app && $(E2E_COMPOSE) run --rm playwright npx playwright test tests/role-boundaries.spec.ts || status=$$?; \
	  $(E2E_COMPOSE) down -v --remove-orphans >/dev/null 2>&1 || true; \
	  rm -rf e2e/test-results; \
	  exit $$status

acceptance-preflight:
	./scripts/production-acceptance.sh --preflight

acceptance-running:
	./scripts/production-acceptance.sh --running

fmt:
	$(DEV_COMPOSE) run --rm --no-deps backend gofmt -w ./cmd ./internal

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
