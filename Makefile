GO            ?= go
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
MOCKERY       ?= $(GO) run github.com/vektra/mockery/v3@v3.8.0
SWAG          ?= $(GO) run github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc6

INTEGRATION_TAG := integration
DB_TAG          := db
COVER_PROFILE   := coverage.out
COMPOSE         := docker compose -f deploy/docker-compose.yml
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
run: ## Run the API server (needs POLYTHOUGHT_* env, see internal/config)
	$(GO) run ./cmd serve

.PHONY: demo
demo: ## Run the spread-watch demo workflow against live Polymarket
	$(GO) run ./cmd demo

## Static checks

.PHONY: fmt
fmt: ## Format all Go files
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs formatting
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Vet unit, integration and database test code
	$(GO) vet ./...
	$(GO) vet -tags $(INTEGRATION_TAG) ./...
	$(GO) vet -tags $(DB_TAG) ./...

.PHONY: lint
lint: ## Run golangci-lint (unit, integration and database test code)
	$(GOLANGCI_LINT) run --build-tags $(INTEGRATION_TAG),$(DB_TAG) ./...

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
db-up: ## Start the local Postgres from deploy/docker-compose.yml
	$(COMPOSE) up -d --wait

.PHONY: db-down
db-down: ## Stop the local Postgres (data is kept in a volume)
	$(COMPOSE) down

.PHONY: db-gen
db-gen: ## Regenerate go-jet code from the migrations (Docker)
	$(GO) run ./internal/tools/dbgen

## Generated code and fixtures

.PHONY: mocks
mocks: ## Regenerate mocks from .mockery.yml
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
	status=$$?; rm -rf "$$tmp"; exit $$status

.PHONY: api-check
api-check: ## Fail if api/openapi.json is stale
	@tmp="$$(mktemp -d)"; \
	$(SWAG_INIT) "$$tmp" && diff -u $(API_SPEC) "$$tmp/swagger.json"; \
	status=$$?; rm -rf "$$tmp"; \
	if [ $$status -ne 0 ]; then echo "$(API_SPEC) is stale: run make api-spec"; exit 1; fi

.PHONY: fixtures
fixtures: ## Refresh client test fixtures from live Polymarket (network)
	$(GO) run ./internal/tools/fixtures

## Aggregates

.PHONY: check
check: fmt-check vet lint test ## Everything that runs offline: format, vet, lint, unit tests

.PHONY: clean
clean: ## Remove build and coverage artefacts
	rm -f $(COVER_PROFILE)
