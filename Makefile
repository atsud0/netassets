SHELL := /bin/sh
COMPOSE := podman compose

.PHONY: help up down build logs ps test test-backend test-frontend e2e fmt migrate-up migrate-down

help:
	@printf '%s\n' \
	  'make up             Start the application with Podman Compose' \
	  'make down           Stop the application' \
	  'make build          Build application images' \
	  'make logs           Follow application logs' \
	  'make test           Run backend and frontend unit tests' \
	  'make e2e            Run Playwright tests in a container' \
	  'make migrate-up     Apply database migrations' \
	  'make migrate-down   Roll back one migration'

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
	podman compose --profile e2e run --rm playwright

fmt:
	cd backend && gofmt -w ./cmd ./internal

migrate-up:
	$(COMPOSE) run --rm app /app/netassets-migrate up

migrate-down:
	$(COMPOSE) run --rm app /app/netassets-migrate down
