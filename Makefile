GO            ?= go
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
MOCKERY       ?= $(GO) run github.com/vektra/mockery/v3@v3.8.1 --config tools/mockery/.mockery.yml
SWAG          ?= $(GO) run github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc6
GOVULNCHECK   ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0
# pnpm, installed itself (D423): Node no longer brings corepack. Whatever version is
# installed, it runs the one web/package.json and docs/package.json pin, as their
# packageManager says, fetching it the first time.
PNPM          ?= pnpm

INTEGRATION_TAG := integration
DB_TAG          := db
E2E_TAG         := e2e
COVER_PROFILE   := coverage.out
# deployments/docker-compose.override.yml, a machine's own and not committed, joins when it is there.
COMPOSE         := docker compose -f deployments/docker-compose.yml $(addprefix -f ,$(wildcard deployments/docker-compose.override.yml))
# The commit images built from this checkout report (D425); their version stays dev.
export POLYTHOUGHT_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
# The app built from this checkout, rather than the released images the compose file pulls.
APP_COMPOSE     := docker compose -f deployments/docker-compose.yml -f deployments/docker-compose.build.yml \
	$(addprefix -f ,$(wildcard deployments/docker-compose.override.yml)) --env-file .env --profile app
DEV_DATABASE    := postgres://polythought:polythought@localhost:5432/polythought?sslmode=disable
E2E_COMPOSE     := docker compose -p polythought-e2e -f deployments/docker-compose.yml -f deployments/docker-compose.build.yml \
	-f deployments/docker-compose.e2e.yml --profile app
API_SPEC        := api/openapi.json

# swag writes swagger.json into a directory; the targets move it to API_SPEC.
SWAG_INIT = $(SWAG) init --quiet --v3.1 --requiredByDefault --parseInternal --outputTypes json \
	--generalInfo doc.go --dir internal/adapters/httpapi --output

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Build and run

.PHONY: build
build: ## Compile every package
	$(GO) build ./...

.PHONY: run
run: ## Run the API on :8080 with settings from .env, for the UI from make web-dev (needs make db-up)
	@test -f .env || { echo "no .env: copy .env.example to .env and fill it in"; exit 1; }
	@set -a; . ./.env; set +a; \
	POLYTHOUGHT_DATABASE_URL="$${POLYTHOUGHT_DATABASE_URL:-$(DEV_DATABASE)}" \
	POLYTHOUGHT_PUBLIC_URL="$${POLYTHOUGHT_PUBLIC_URL:-http://localhost:5173}" \
	$(GO) run -tags stdjson ./cmd/polythought serve

.PHONY: up
up: ## Run everything in Docker at http://localhost:3000, built from this checkout, with settings from .env (Ctrl+C stops)
	@test -f .env || { echo "no .env: copy .env.example to .env and fill it in"; exit 1; }
	$(APP_COMPOSE) up --build

.PHONY: down
down: ## Stop everything make up or make db-up started (data is kept in a volume)
	$(APP_COMPOSE) down

.PHONY: demo
demo: ## Run the spread-watch demo workflow against live Polymarket
	$(GO) run ./cmd/polythought demo

## Static checks

.PHONY: fmt
fmt: ## Format all Go files
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs formatting
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Vet unit, integration, database and e2e test code
	$(GO) vet ./...
	$(GO) vet -tags $(INTEGRATION_TAG) ./...
	$(GO) vet -tags $(DB_TAG) ./...
	$(GO) vet -tags $(E2E_TAG) ./...

.PHONY: lint
lint: ## Run golangci-lint (unit, integration, database and e2e test code)
	$(GOLANGCI_LINT) run --build-tags $(INTEGRATION_TAG),$(DB_TAG),$(E2E_TAG) ./...

## Tests

.PHONY: test
test: ## Unit tests with the race detector, no network
	$(GO) test -race -count=1 ./...

.PHONY: test-integration
test-integration: ## Live tests against the public Polymarket APIs (network)
	$(GO) test -race -count=1 -tags $(INTEGRATION_TAG) -run Live ./...

.PHONY: test-db
test-db: ## Repository tests against a throwaway Postgres (Docker)
	$(GO) test -race -count=1 -tags $(DB_TAG) ./internal/adapters/postgres/...

.PHONY: test-sandbox
test-sandbox: ## The sandbox's tests on Linux, in Docker, as the server runs there: plugins and code runners (D337)
	@# No VCS stamps in the test plugins' builds: the mounted checkout's owner may not be the container's.
	docker run --rm -v "$(CURDIR)":/src -w /src -e GOFLAGS=-buildvcs=false \
		-v polythought-gomod:/go/pkg/mod -v polythought-gocache:/root/.cache/go-build \
		golang:1.27.3-bookworm go test -count=1 ./internal/adapters/sandbox/ ./internal/adapters/pluginhost/ \
		./internal/adapters/coderunner/

.PHONY: examples
examples: ## Vet, lint and test the example plugin, a module of its own (D360) that ./... does not reach
	cd examples/plugins/movers && $(GO) vet ./... && $(GOLANGCI_LINT) run ./... && $(GO) test -count=1 ./...

.PHONY: vulncheck
vulncheck: ## Known vulnerabilities the Go code reaches, and those of what the app and the docs ship (network)
	$(GOVULNCHECK) ./...
	$(PNPM) --dir web audit --prod
	$(PNPM) --dir docs audit --prod

.PHONY: test-all
test-all: test test-db test-integration ## Unit, database and live tests

.PHONY: test-flaky
test-flaky: ## Repeat unit tests 20 times to catch timing flakes
	$(GO) test -race -count=20 ./...

.PHONY: cover
cover: ## Unit test coverage summary (writes coverage.out)
	$(GO) test -count=1 -coverprofile=$(COVER_PROFILE) ./...
	$(GO) tool cover -func=$(COVER_PROFILE) | tail -1

## Database

.PHONY: db-up
db-up: ## Start the local Postgres from deployments/docker-compose.yml
	$(COMPOSE) up -d --wait

.PHONY: db-down
db-down: ## Stop the local Postgres (data is kept in a volume)
	$(COMPOSE) down

.PHONY: db-gen
db-gen: ## Regenerate go-jet code from the migrations (Docker)
	$(GO) run ./tools/dbgen

## Generated code and fixtures

BUF ?= $(GO) run github.com/bufbuild/buf/cmd/buf@v1.74.0

.PHONY: proto
proto: ## Lint the plugin contract's protobuf and regenerate pkg/plugin/v1
	$(BUF) lint
	$(BUF) generate

.PHONY: proto-check
proto-check: ## Fail if pkg/plugin/v1 is stale
	@snapshot="$$(mktemp -d)"; cp -R pkg/plugin/v1 "$$snapshot/"; \
	$(MAKE) --no-print-directory proto >/dev/null && diff -r "$$snapshot/v1" pkg/plugin/v1 >/dev/null; \
	status=$$?; rm -rf "$$snapshot"; \
	if [ $$status -ne 0 ]; then echo "pkg/plugin/v1 was stale and has been regenerated"; exit 1; fi

.PHONY: mocks
mocks: ## Regenerate mocks from tools/mockery/.mockery.yml
	$(MOCKERY)

.PHONY: mocks-check
mocks-check: ## Fail if the mocks on disk are stale
	@snapshot="$$(mktemp -d)"; cp -R internal/mocks "$$snapshot/"; \
	$(MOCKERY) && diff -r "$$snapshot/mocks" internal/mocks >/dev/null; \
	status=$$?; rm -rf "$$snapshot"; \
	if [ $$status -ne 0 ]; then echo "mocks were stale and have been regenerated"; exit 1; fi

.PHONY: api-spec
api-spec: ## Regenerate api/openapi.json from the handler annotations
	@tmp="$$(mktemp -d)"; \
	$(SWAG_INIT) "$$tmp" && mkdir -p $(dir $(API_SPEC)) && mv "$$tmp/swagger.json" $(API_SPEC); \
	status=$$?; rm -rf "$$tmp"; \
	if [ $$status -eq 0 ]; then echo "updated $(API_SPEC); run make web-api to update the UI's types"; fi; \
	exit $$status

.PHONY: api-check
api-check: ## Fail if api/openapi.json is stale
	@tmp="$$(mktemp -d)"; \
	$(SWAG_INIT) "$$tmp" && diff -u $(API_SPEC) "$$tmp/swagger.json"; \
	status=$$?; rm -rf "$$tmp"; \
	if [ $$status -ne 0 ]; then echo "$(API_SPEC) is stale: run make api-spec"; exit 1; fi

.PHONY: fixtures
fixtures: ## Refresh client test fixtures from live Polymarket (network)
	$(GO) run ./tools/fixtures

# The official client our order signing is checked against (D151), at a pinned commit.
ORDER_VECTORS_SDK := 206bb0fead733d9a64fade8c2832778f1e2d8942
ORDER_VECTORS     := pkg/clients/polymarket/testdata/order_vectors.json
ORDER_VECTORS_NODE := node:24-alpine

.PHONY: order-vectors
order-vectors: ## Regenerate the order-signing vectors from Polymarket's official client (network, Docker)
	@set -e; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	git -C "$$tmp" init -q; \
	git -C "$$tmp" fetch -q --depth 1 https://github.com/Polymarket/ts-sdk.git $(ORDER_VECTORS_SDK); \
	git -C "$$tmp" checkout -q FETCH_HEAD; \
	cp tools/ordervectors/generate.ts "$$tmp/packages/client/src/ordervectors.ts"; \
	docker run --rm -v "$$tmp":/sdk -w /sdk -e SDK_COMMIT=$(ORDER_VECTORS_SDK) -e COREPACK_ENABLE_DOWNLOAD_PROMPT=0 \
		$(ORDER_VECTORS_NODE) sh -c 'corepack enable >/dev/null && pnpm install --frozen-lockfile --ignore-scripts >&2 \
		&& cd packages/client && pnpm dlx tsx@4.23.15 src/ordervectors.ts' > "$$tmp/vectors.json"; \
	mv "$$tmp/vectors.json" $(ORDER_VECTORS)

## Web UI (web/, needs Node 26 and pnpm)

.PHONY: web-install
web-install: ## Install the UI's dependencies
	$(PNPM) --dir web install --frozen-lockfile

.PHONY: web-dev
web-dev: ## Run the UI on :5173, proxying /api to :8080 (start the API with POLYTHOUGHT_PUBLIC_URL=http://localhost:5173)
	$(PNPM) --dir web dev

.PHONY: web-api
web-api: ## Regenerate the UI's API types from api/openapi.json
	$(PNPM) --dir web gen:api

.PHONY: web-check
web-check: sdk-types-check ## UI typecheck, lint, format, API and SDK types up to date, tests
	$(PNPM) --dir web check

SDK_DIR := internal/adapters/tscompiler/sdk

.PHONY: sdk-types
sdk-types: ## Type-check the code SDK and regenerate its declarations (sdk/types/sdk.d.ts)
	$(PNPM) --dir web exec tsc -p ../$(SDK_DIR)/tsconfig.json

.PHONY: sdk-types-check
sdk-types-check: ## Fail if the code SDK or the node declarations do not type-check, or the SDK's are stale
	$(PNPM) --dir web exec prettier --config .prettierrc.json --check ../$(SDK_DIR)/sdk.ts ../$(SDK_DIR)/big.d.mts ../$(SDK_DIR)/types-example.ts ../$(SDK_DIR)/workflow.ts
	$(PNPM) --dir web exec tsc -p ../$(SDK_DIR)/tsconfig.types.json
	@tmp="$$(mktemp -d)"; \
	$(PNPM) --dir web exec tsc -p ../$(SDK_DIR)/tsconfig.json --outDir "$$tmp" && diff -u $(SDK_DIR)/types/sdk.d.ts "$$tmp/sdk.d.ts"; \
	status=$$?; rm -rf "$$tmp"; \
	if [ $$status -ne 0 ]; then echo "$(SDK_DIR)/types/sdk.d.ts is stale: run make sdk-types"; exit 1; fi

.PHONY: web-build
web-build: ## Build the UI into web/dist
	$(PNPM) --dir web build

## Docs site (docs/, needs Node 26 and pnpm)

.PHONY: jsruntime-wasm
jsruntime-wasm: ## Rebuild the JavaScript engine for user code from pinned QuickJS-NG sources (Docker)
	sh internal/adapters/jsruntime/engine/build.sh

.PHONY: jsruntime-check
jsruntime-check: ## Fail if the committed JavaScript engine is not what its sources build (Docker)
	@wasm=internal/adapters/jsruntime/engine/engine.wasm; before="$$(shasum -a 256 $$wasm)"; \
	sh internal/adapters/jsruntime/engine/build.sh >/dev/null; \
	if [ "$$before" != "$$(shasum -a 256 $$wasm)" ]; then echo "$$wasm was stale and has been rebuilt"; exit 1; fi

.PHONY: docs-install
docs-install: ## Install the docs site's dependencies, and the headless Chromium drawing their diagrams
	$(PNPM) --dir docs install --frozen-lockfile
	$(PNPM) --dir docs exec playwright install --only-shell chromium

.PHONY: docs-dev
docs-dev: ## Run the docs on :4321/docs with live reload (make web-dev serves them at /docs too)
	ASTRO_TELEMETRY_DISABLED=1 $(PNPM) --dir docs dev

.PHONY: docs-build
docs-build: ## Build the docs into docs/dist, with their Content-Security-Policy
	ASTRO_TELEMETRY_DISABLED=1 $(PNPM) --dir docs build

.PHONY: docs-gen
docs-gen: ## Regenerate the docs' reference pages from the code (a test fails when they are stale)
	$(GO) run ./tools/docgen

.PHONY: e2e
e2e: ## Browser and agent tests against a throwaway stack in Docker (UI on :3100, removed afterwards)
	$(PNPM) --dir web exec playwright install --only-shell chromium
	@# The host's test plugin, for the containers' platform, to install (5c2).
	$(GO) run ./tools/pluginpack -o web/e2e/.plugins/echo.tar.gz -platforms linux_amd64,linux_arm64 \
		./internal/adapters/pluginhost/testdata/echo
	@# And the example plugin, as an admin installs it (5f).
	$(GO) run ./tools/pluginpack -o web/e2e/.plugins/movers.tar.gz -platforms linux_amd64,linux_arm64 \
		./examples/plugins/movers
	@# An agent through the Go SDK's client first (D398), then the browser tests; E2E_ONLY=go
	@# runs the agent's alone, E2E_ONLY=<spec> one browser spec.
	@set -e; \
	export POLYTHOUGHT_SECRET_KEYS="e2e:$$(openssl rand -base64 32)" \
		POLYTHOUGHT_ADMIN_EMAIL=admin@e2e.test POLYTHOUGHT_ADMIN_PASSWORD=e2e-admin-password \
		POLYTHOUGHT_REGISTRATION=invite; \
	trap '$(E2E_COMPOSE) down -v --remove-orphans >/dev/null 2>&1' EXIT; \
	$(E2E_COMPOSE) up -d --build --wait; \
	export E2E_ADMIN_EMAIL=$$POLYTHOUGHT_ADMIN_EMAIL E2E_ADMIN_PASSWORD=$$POLYTHOUGHT_ADMIN_PASSWORD; \
	if [ -z "$(E2E_ONLY)" ] || [ "$(E2E_ONLY)" = go ]; then \
		E2E_BASE_URL=http://localhost:3100 $(GO) test -tags $(E2E_TAG) -count=1 ./internal/e2e/...; \
	fi; \
	if [ "$(E2E_ONLY)" != go ]; then $(PNPM) --dir web exec playwright test $(E2E_ONLY); fi

## Aggregates

.PHONY: check
check: fmt-check vet lint test examples ## Everything that runs offline: format, vet, lint, unit tests, the example plugin

.PHONY: clean
clean: ## Remove build and coverage artefacts
	rm -f $(COVER_PROFILE)
	rm -rf web/dist
