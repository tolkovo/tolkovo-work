# Single entry points for contributors and CI (docs/00-engineering-rules.md §4.2).
# Frontend targets join as apps/panel, apps/widget and apps/landing appear.

SHELL   := /bin/bash
SERVER  := apps/server
TOOL    := go tool -modfile=tools/go.mod
COMPOSE := docker compose -f deploy/compose/docker-compose.dev.yml --env-file deploy/compose/.env.example
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

# Local defaults match deploy/compose/.env.example.
export TOLKOVO_DATABASE_URL         ?= postgres://tolkovo_app_dev:tolkovo_app_dev@localhost:5432/tolkovo?sslmode=disable
export TOLKOVO_MIGRATE_DATABASE_URL ?= postgres://tolkovo:tolkovo@localhost:5432/tolkovo?sslmode=disable
export TOLKOVO_SYSTEM_DATABASE_URL  ?= postgres://tolkovo_system_dev:tolkovo_system_dev@localhost:5432/tolkovo?sslmode=disable
export TOLKOVO_LOG_FORMAT           ?= text

.PHONY: help dev deps-up deps-down migrate run build gen gen-go gen-web gen-check lint test web-install web-check check

help: ## Show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-10s %s\n", $$1, $$2}'

dev: deps-up migrate run ## Start dependencies, migrate and run the server

deps-up: ## Start PostgreSQL, Redis, Garage (S3) and Mailpit in Docker
	$(COMPOSE) up -d --wait
	deploy/compose/garage-init.sh

deps-down: ## Stop the dependencies (data volumes are kept)
	$(COMPOSE) down

migrate: ## Apply migrations as the table owner
	cd $(SERVER) && go run ./cmd/tolkovo migrate up

run: ## Run the server with role=all
	cd $(SERVER) && go run ./cmd/tolkovo serve

build: ## Build the server binary into bin/
	cd $(SERVER) && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o ../../bin/tolkovo ./cmd/tolkovo

GENERATED := $(SERVER)/internal/*/store $(SERVER)/internal/platform/*/store $(SERVER)/internal/api/oapi

gen: gen-go gen-web ## Generate code from SQL and api/openapi.yaml (later WS schemas)

gen-go: ## Go only: sqlc and oapi-codegen (no Node needed)
	cd $(SERVER) && $(TOOL) sqlc generate
	cd $(SERVER) && $(TOOL) oapi-codegen -config oapi-codegen.yaml ../../api/openapi.yaml

gen-web: ## TypeScript only: packages/api-client types by openapi-typescript
	pnpm -r --if-present run gen

# Go side only, so the server CI job runs without Node; web-check checks the TS side.
gen-check: gen-go ## Fail if generated Go code is out of date
	@git diff --exit-code -- $(GENERATED) || { echo "generated code is stale: run make gen"; exit 1; }

lint: ## gofmt, go vet, golangci-lint
	@cd $(SERVER) && test -z "$$(gofmt -l .)" || { gofmt -l .; echo "run gofmt -w"; exit 1; }
	cd $(SERVER) && go vet ./...
	cd $(SERVER) && $(TOOL) golangci-lint run ./...

test: ## Unit + integration tests (need TOLKOVO_TEST_DATABASE_URL, TOLKOVO_TEST_REDIS_URL)
	cd $(SERVER) && go test -race -count=1 ./...

# Every JS workspace runs the scripts it defines (pnpm-workspace.yaml).
WEB_GENERATED := apps/panel/src/routeTree.gen.ts packages/api-client/src/schema.gen.ts

web-install: ## Install JS dependencies from pnpm-lock.yaml
	pnpm install --frozen-lockfile

web-check: web-install ## Generate, typecheck, lint, test and build panel, landing and packages
	pnpm -r --if-present run gen
	pnpm -r --if-present run typecheck
	pnpm -r --if-present run lint
	pnpm -r --if-present run test
	pnpm -r --if-present run check
	pnpm -r --if-present run build
	@git diff --exit-code -- $(WEB_GENERATED) || { echo "generated TS is stale: run make gen-web and pnpm -C apps/panel build"; exit 1; }

check: gen-check lint test web-check ## Run before every PR
