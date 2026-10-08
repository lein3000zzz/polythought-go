GO            ?= go
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
MOCKERY       ?= $(GO) run github.com/vektra/mockery/v3@v3.8.0

INTEGRATION_TAG := integration
COVER_PROFILE   := coverage.out

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## Build and run

.PHONY: build
build: ## Compile every package
	$(GO) build ./...

.PHONY: run
run: ## Run the demo against live Polymarket
	$(GO) run ./cmd

## Static checks

.PHONY: fmt
fmt: ## Format all Go files
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs formatting
	@out="$$(gofmt -l .)"; if [ -n "$$out" ]; then echo "needs gofmt:"; echo "$$out"; exit 1; fi

.PHONY: vet
vet: ## Vet unit and integration code
	$(GO) vet ./...
	$(GO) vet -tags $(INTEGRATION_TAG) ./...

.PHONY: lint
lint: ## Run golangci-lint (unit and integration code)
	$(GOLANGCI_LINT) run --build-tags $(INTEGRATION_TAG) ./...

## Tests

.PHONY: test
test: ## Unit tests with the race detector, no network
	$(GO) test -race -count=1 ./...

.PHONY: test-integration
test-integration: ## Live tests against the public Polymarket APIs (network)
	$(GO) test -race -count=1 -tags $(INTEGRATION_TAG) -run Live ./...

.PHONY: test-all
test-all: test test-integration ## Unit and live tests

.PHONY: test-flaky
test-flaky: ## Repeat unit tests 20 times to catch timing flakes
	$(GO) test -race -count=20 ./...

.PHONY: cover
cover: ## Unit test coverage summary (writes coverage.out)
	$(GO) test -count=1 -coverprofile=$(COVER_PROFILE) ./...
	$(GO) tool cover -func=$(COVER_PROFILE) | tail -1

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

.PHONY: fixtures
fixtures: ## Refresh client test fixtures from live Polymarket (network)
	$(GO) run ./internal/tools/fixtures

## Aggregates

.PHONY: check
check: fmt-check vet lint test ## Everything that runs offline: format, vet, lint, unit tests

.PHONY: clean
clean: ## Remove build and coverage artefacts
	rm -f $(COVER_PROFILE)
