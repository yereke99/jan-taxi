# Jan Taxi: developer and operator commands. Run `make help`.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
MAKEFLAGS += --no-print-directory

# Docker Compose deployment (.env) and local `go run` development (.env.dev).
ENV_FILE ?= .env
DEV_ENV ?= .env.dev
COMPOSE ?= docker compose --env-file $(ENV_FILE)
# Compose project name (volume prefix): COMPOSE_PROJECT_NAME from the env file.
PROJECT ?= $(or $(subst ",,$(shell sed -n 's/^COMPOSE_PROJECT_NAME=//p' "$(ENV_FILE)" 2>/dev/null | tail -n 1)),jantaxi)
SERVICE ?=

# Local PostGIS for tests and development (multi-arch image, also on Apple Silicon).
TEST_DB_CONTAINER ?= jantaxi-devdb
TEST_DB_IMAGE ?= imresamu/postgis:17-3.5-alpine
TEST_DB_PORT ?= 55432
TEST_DATABASE_URL ?= postgres://jantaxi:jantaxi@localhost:$(TEST_DB_PORT)/postgres?sslmode=disable

PNPM ?= pnpm
REDOCLY_VERSION ?= 1.34.21
OSRM_IMAGE ?= ghcr.io/project-osrm/osrm-backend:v26.10.0-debian
# Host backup directory: BACKUP_HOST_DIR from the env file, default ./backups.
BACKUP_DIR ?= $(patsubst ./%,%,$(or $(subst ",,$(shell sed -n 's/^BACKUP_HOST_DIR=//p' "$(ENV_FILE)" 2>/dev/null | tail -n 1)),backups))
SKIP_PREFLIGHT ?=

# Shared Mini App core that must be byte-identical in both Mini Apps.
SHARED_SRC_A := passenger/miniapp/src
SHARED_SRC_B := driver/miniapp/src
SHARED_DIRS := telegram maps styles components/ui i18n/core
SHARED_FILES := services/http.ts services/realtime.ts services/auth.ts \
	services/http.test.ts services/realtime.test.ts services/config.ts services/config.test.ts
SHARED_SCRIPTS := scripts/verify-shared.mjs scripts/lint.mjs

GO_DIRS := admin passenger driver

APP ?= passenger
START ?=
TG_ID ?= 100000001

define require_env_file
	@test -f "$(ENV_FILE)" || { echo "$(ENV_FILE) not found. Run: cp .env.example .env  (then fill it in, see docs/DEPLOYMENT.md)"; exit 1; }
endef

define require_dev_env
	@test -f "$(DEV_ENV)" || { echo "$(DEV_ENV) not found. Run: cp deploy/dev/dev.env.example $(DEV_ENV)"; exit 1; }
endef

# Loads $(DEV_ENV) into the recipe's shell.
LOAD_DEV_ENV = set -a; . ./$(DEV_ENV); set +a

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "; print "Usage: make <target> [VAR=value]"} \
		/^##@/ {printf "\n%s\n", substr($$0, 5)} \
		/^[a-zA-Z0-9_-]+:.*## / {printf "  %-16s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ Stack (Docker Compose, reads .env)

.PHONY: up down restart build ps logs health config preflight
up: ## Check .env (deploy/preflight.sh), build missing images and start the stack
	$(require_env_file)
	@if [ "$(SKIP_PREFLIGHT)" != 1 ]; then sh deploy/preflight.sh "$(ENV_FILE)"; fi
	$(COMPOSE) up -d --remove-orphans

down: ## Stop and remove containers (volumes and backups are kept)
	$(require_env_file)
	$(COMPOSE) down

restart: ## Restart all services, or one: make restart SERVICE=api
	$(require_env_file)
	$(COMPOSE) restart $(SERVICE)

build: ## Build images (tag releases with IMAGE_TAG=<git sha>), or one: make build SERVICE=api
	$(require_env_file)
	$(COMPOSE) build --pull $(SERVICE)

ps: ## List containers and their health
	$(require_env_file)
	$(COMPOSE) ps

logs: ## Follow logs of all services, or one: make logs SERVICE=api
	$(require_env_file)
	$(COMPOSE) logs -f --tail=200 $(SERVICE)

health: ## Check API readiness (/readyz) and both bots' health endpoints
	$(require_env_file)
	@status=0; \
	echo "== api /readyz"; \
	if $(COMPOSE) exec -T api /usr/local/bin/api healthcheck --url http://127.0.0.1:8080/readyz; then \
		body=$$($(COMPOSE) exec -T web wget -q -O - http://api:8080/readyz 2>/dev/null || true); \
		echo "ready $$body"; \
	else status=1; fi; \
	for bot in passenger-bot driver-bot; do \
		echo "== $$bot"; \
		if $(COMPOSE) exec -T $$bot /usr/local/bin/$$bot healthcheck; then echo "healthy"; else status=1; fi; \
	done; \
	echo "== containers"; \
	$(COMPOSE) ps --format 'table {{.Service}}\t{{.Status}}'; \
	exit $$status

preflight: ## Check the env file for missing values, placeholders and inconsistent URLs
	$(require_env_file)
	sh deploy/preflight.sh "$(ENV_FILE)"

config: ## Validate compose.yaml against the env file
	$(require_env_file)
	$(COMPOSE) config -q && $(COMPOSE) --profile ocr config -q && echo "compose configuration is valid (including OCR)"

##@ Local live stack (Docker + Cloudflare quick tunnel, development only; docs/DEPLOYMENT.md §10)

# State directory outside the repository: compose.env (secrets), tunnel.env,
# logs, backups. Example: make local-up STACK_DIR=../jan-taxi-local
STACK_DIR ?=
LOCAL_STACK = JT_STACK_DIR="$(STACK_DIR)" sh deploy/local/stack.sh

define require_stack_dir
	@test -n "$(STACK_DIR)" || { echo "set STACK_DIR to a directory outside the repository, e.g. make $@ STACK_DIR=../jan-taxi-local"; exit 2; }
endef

.PHONY: local-env local-up local-rebuild local-retunnel local-status local-verify local-down local-test
local-env: ## Write STACK_DIR/compose.env from .env (or JT_SOURCE_ENV)
	$(require_stack_dir)
	$(LOCAL_STACK) env

local-up: ## Start everything behind a quick tunnel, point the bots at it, start the watchdog
	$(require_stack_dir)
	$(LOCAL_STACK) up

local-rebuild: ## Rebuild api/bots/web from the current tree, migrate, restart them (tunnel URL kept)
	$(require_stack_dir)
	$(LOCAL_STACK) rebuild

local-retunnel: ## New quick tunnel; api, bots and menu buttons follow it
	$(require_stack_dir)
	$(LOCAL_STACK) retunnel

local-status: ## Health of every service, tunnel URL, bot health JSON, menu buttons
	$(require_stack_dir)
	$(LOCAL_STACK) status

local-verify: ## Deep links and WebSocket through the tunnel, client IP, admin login
	$(require_stack_dir)
	$(LOCAL_STACK) verify

local-down: ## Stop the watchdog and remove the local stack's containers (volumes kept)
	$(require_stack_dir)
	$(LOCAL_STACK) down

local-test: ## Test deploy/local/stack.sh without Docker (stub docker; migration redo is opt-in, backup first)
	sh deploy/local/stack_test.sh

##@ Database and administration

.PHONY: migrate migrate-status admin-create backup restore
migrate: ## Apply pending database migrations (one-shot migrate service)
	$(require_env_file)
	$(COMPOSE) run --rm migrate

migrate-status: ## Show the database schema version
	$(require_env_file)
	$(COMPOSE) run --rm migrate migrate status

admin-create: ## Create an admin: make admin-create EMAIL=a@b.kz ROLE=super_admin NAME="Full Name"
	$(require_env_file)
	@test -n "$(EMAIL)" -a -n "$(ROLE)" || { \
		echo "usage: make admin-create EMAIL=... ROLE=super_admin|operator|driver_moderator|finance_manager|auditor [NAME=...]"; exit 2; }
	@read -r -s -p "Password for $(EMAIL) (12+ characters): " pw; echo; \
	read -r -s -p "Repeat password: " pw2; echo; \
	if [ "$$pw" != "$$pw2" ]; then echo "passwords do not match"; exit 1; fi; \
	printf '%s\n' "$$pw" | $(COMPOSE) run --rm -T migrate admin create --email "$(EMAIL)" --role "$(ROLE)" --name "$(NAME)"

backup: ## Run a database + files backup now (into ./backups)
	$(require_env_file)
	$(COMPOSE) run --rm --entrypoint /bin/sh db-backup /opt/jantaxi/backup/backup.sh

restore: ## Restore FILE=backups/db/<name>.dump into a NEW database, or unpack FILE=backups/files/<name>.tar.gz (asks first)
	$(require_env_file)
	@test -n "$(FILE)" || { echo "usage: make restore FILE=backups/db/jantaxi-<timestamp>.dump | backups/files/files-<timestamp>.tar.gz"; exit 2; }
	@rel="$(FILE)"; rel="$${rel#./}"; rel="$${rel#$(BACKUP_DIR)/}"; \
	test -f "$(BACKUP_DIR)/$$rel" || { echo "$(BACKUP_DIR)/$$rel not found"; exit 1; }; \
	$(COMPOSE) run --rm --entrypoint /bin/sh db-backup /opt/jantaxi/backup/restore.sh "/backups/$$rel"

##@ Routing and geocoding data

.PHONY: osrm-prepare geocoder-import dev-osrm
osrm-prepare: ## Download the Kazakhstan OSM extract and build the OSRM dataset (needs ~6 GB RAM)
	$(require_env_file)
	$(COMPOSE) --profile osrm-prepare run --rm --no-deps osrm-download
	$(COMPOSE) --profile osrm-prepare run --rm --no-deps osrm-prepare
	@echo "Dataset ready. Start or reload the router: $(COMPOSE) --profile osrm up -d --force-recreate osrm"

geocoder-import: ## Build the self-hosted Photon index for Kazakhstan (optional geocoder profile)
	$(require_env_file)
	$(COMPOSE) --profile geocoder-import build photon-import
	$(COMPOSE) --profile geocoder-import run --rm photon-import
	@echo "Index ready. Start or reload Photon: $(COMPOSE) --profile geocoder up -d --force-recreate photon"

dev-osrm: ## Serve the prepared OSRM dataset on localhost:5000 for local development
	docker run --rm -d --name $(PROJECT)-dev-osrm -p 127.0.0.1:5000:5000 \
		-v $(PROJECT)_osrm-data:/data:ro $(OSRM_IMAGE) \
		osrm-routed --algorithm mld /data/kazakhstan-latest.osrm
	@echo "OSRM on http://localhost:5000 (stop: docker stop $(PROJECT)-dev-osrm)"

##@ Local development (go run, reads .env.dev)

.PHONY: dev-api dev-bots devlink fe-dev test-db-up test-db-down
test-db-up: ## Start the local PostGIS container on localhost:55432 (tests and dev-api)
	@if docker inspect $(TEST_DB_CONTAINER) >/dev/null 2>&1; then \
		docker start $(TEST_DB_CONTAINER) >/dev/null; \
	else \
		docker run -d --name $(TEST_DB_CONTAINER) \
			-e POSTGRES_USER=jantaxi -e POSTGRES_PASSWORD=jantaxi -e POSTGRES_DB=jantaxi \
			-p 127.0.0.1:$(TEST_DB_PORT):5432 \
			-v $(TEST_DB_CONTAINER)-data:/var/lib/postgresql/data \
			$(TEST_DB_IMAGE) >/dev/null; \
	fi
	@for i in $$(seq 1 60); do \
		if docker exec $(TEST_DB_CONTAINER) pg_isready -q -U jantaxi -d jantaxi; then echo "PostGIS ready on localhost:$(TEST_DB_PORT)"; exit 0; fi; \
		sleep 1; \
	done; echo "PostGIS did not become ready"; exit 1

test-db-down: ## Stop and remove the local PostGIS container (its data volume is kept)
	docker rm -f $(TEST_DB_CONTAINER)
	@echo "Data kept in volume $(TEST_DB_CONTAINER)-data (if created by test-db-up). Delete with: docker volume rm $(TEST_DB_CONTAINER)-data"

dev-api: ## Run the API locally (migrates on start) with .env.dev
	$(require_dev_env)
	@$(LOAD_DEV_ENV); go run ./admin/backend/cmd/api serve

dev-bots: ## Run both bots locally against dev-api (needs real test bot tokens in .env.dev)
	$(require_dev_env)
	@$(LOAD_DEV_ENV); \
	HEALTH_PORT=8090 go run ./passenger/bot/cmd & \
	HEALTH_PORT=8091 go run ./driver/bot/cmd & \
	wait

devlink: ## Print a signed local Mini App URL: make devlink APP=passenger|driver [START=taxi] [TG_ID=100000001]
	$(require_dev_env)
	@$(LOAD_DEV_ENV); go run ./admin/backend/cmd/devlink --app "$(APP)" --tg-id "$(TG_ID)" $(if $(START),--start-param "$(START)")

fe-dev: ## Start a Vite dev server: make fe-dev APP=passenger|driver|admin
	@case "$(APP)" in \
		passenger) pkg=@jantaxi/passenger-miniapp ;; \
		driver) pkg=@jantaxi/driver-miniapp ;; \
		admin) pkg=@jantaxi/admin-frontend ;; \
		*) echo "APP must be passenger, driver or admin"; exit 2 ;; \
	esac; \
	$(PNPM) --filter "$$pkg" dev

##@ Quality

.PHONY: test lint openapi-lint verify-shared fe-install fe-build fe-test fe-typecheck fe-lint caddy-validate
test: ## Go tests with the race detector (needs make test-db-up)
	TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -race -count=1 ./...

lint: ## go vet, gofmt check and frontend type checks
	go vet ./...
	@out=$$(find $(GO_DIRS) -name '*.go' -not -path '*/node_modules/*' -print0 | xargs -0 gofmt -l); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi; echo "gofmt: ok"
	$(MAKE) fe-typecheck

openapi-lint: ## Lint openapi.yaml (Redocly CLI via npx) and check every mounted route is documented
	npx --yes @redocly/cli@$(REDOCLY_VERSION) lint --config deploy/openapi/redocly.yaml admin/backend/api/openapi.yaml
	JT_CHECK_OPENAPI=1 TEST_DATABASE_URL='$(TEST_DATABASE_URL)' go test -count=1 -run 'TestRoutesDocumented|TestSpecOperationsParser' ./admin/backend/internal/app/

verify-shared: ## Fail if the shared Mini App core differs between passenger and driver
	@status=0; \
	for d in $(SHARED_DIRS); do diff -r "$(SHARED_SRC_A)/$$d" "$(SHARED_SRC_B)/$$d" || status=1; done; \
	for f in $(SHARED_FILES); do diff "$(SHARED_SRC_A)/$$f" "$(SHARED_SRC_B)/$$f" || status=1; done; \
	for f in $(SHARED_SCRIPTS); do diff "passenger/miniapp/$$f" "driver/miniapp/$$f" || status=1; done; \
	diff -r -x 'twogis.ts' -x 'twogis.test.ts' "$(SHARED_SRC_A)/utils" "$(SHARED_SRC_B)/utils" || status=1; \
	if [ $$status -ne 0 ]; then echo "Shared Mini App core drifted (see diff above)."; exit 1; fi; \
	echo "Shared Mini App core is identical: $(SHARED_DIRS) utils (except twogis) $(SHARED_FILES) $(SHARED_SCRIPTS)"

fe-install: ## Install frontend dependencies from the lockfile
	$(PNPM) install --frozen-lockfile

fe-build: ## Build the passenger, driver and admin apps
	$(PNPM) -r build

fe-test: ## Run frontend unit tests (vitest)
	$(PNPM) -r test

fe-typecheck: ## Type-check all frontend apps (vue-tsc)
	$(PNPM) -r typecheck

fe-lint: ## Run the frontend lint scripts
	$(PNPM) -r lint

CADDY_IMAGE ?= caddy:2-alpine
caddy-validate: ## Validate the Caddyfile (production domains, local http and local tunnel variants)
	docker run --rm -v "$(CURDIR)/Caddyfile:/etc/caddy/Caddyfile:ro" \
		-e APP_DOMAIN=app.jantaxi.example -e ADMIN_DOMAIN=admin.jantaxi.example -e ACME_EMAIL=ops@jantaxi.example \
		$(CADDY_IMAGE) caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
	docker run --rm -v "$(CURDIR)/Caddyfile:/etc/caddy/Caddyfile:ro" \
		-e APP_DOMAIN=http://localhost:8088 -e ADMIN_DOMAIN=http://localhost:8089 -e ACME_EMAIL=dev@localhost \
		-e "CADDY_GLOBAL_OPTIONS=auto_https off" \
		$(CADDY_IMAGE) caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
	docker run --rm -v "$(CURDIR)/Caddyfile:/etc/caddy/Caddyfile:ro" \
		-e APP_DOMAIN=:8088 -e ADMIN_DOMAIN=:8089 -e ACME_EMAIL=dev@localhost \
		-e "CADDY_GLOBAL_OPTIONS=auto_https off" -e "CADDY_SERVER_OPTIONS=trusted_proxies static private_ranges" \
		$(CADDY_IMAGE) caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
