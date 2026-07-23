SHELL := /bin/sh

GO ?= go
NODE ?= node
NPM ?= npm
DOCKER ?= docker
HELM ?= helm

GO_PACKAGES ?= ./...
GO_TEST_FLAGS ?=
INTEGRATION_DATABASE_URL ?=
GO_BUILD_OUTPUT ?= ./etherview
GO_BUILD_FLAGS ?= -trimpath
GO_BUILD_LDFLAGS ?= -s -w

GOVULNCHECK ?= govulncheck
GITLEAKS ?= gitleaks
GO_LICENSES ?= go-licenses
GOLANGCI_LINT ?= golangci-lint

GOVULNCHECK_VERSION ?= v1.6.0
GITLEAKS_VERSION ?= v8.30.1
GO_LICENSES_VERSION ?= v1.6.0
GOLANGCI_LINT_VERSION ?= v2.12.2
WEB_LICENSE_CHECKER_VERSION ?= 5.0.1

GENERATED_PATHS := \
	internal/api/gen/models.gen.go \
	internal/db/gen \
	web/src/api/schema.gen.ts

IMAGE ?= etherview:local
HELM_CHART ?= deploy/helm/etherview

.DEFAULT_GOAL := check
.NOTPARALLEL: check generate-check

.PHONY: \
	check compose-check compose-schema-smoke deployment-check docker-build docker-check \
	go-build generate generate generate-check generate-go helm-check install-lint-tools install-security-tools \
	golangci-lint \
	license-check license-tool-check lint lint-go plan-check security-check \
	security-tool-check test test-go toolchain-check \
	test-e2e test-integration test-race web-build web-generate web-install web-lint web-test

go-build: web-build
	$(GO) build $(GO_BUILD_FLAGS) -ldflags="$(GO_BUILD_LDFLAGS)" -o $(GO_BUILD_OUTPUT) ./cmd/etherview

plan-check:
	$(GO) run ./cmd/plancheck -root .

toolchain-check:
	@.github/scripts/toolchain-check.sh

generate-go:
	$(GO) generate $(GO_PACKAGES)
	$(GO) tool sqlc generate

generate: generate-go web-build

generate-check:
	@set -eu; \
		snapshot="$$(mktemp -d /tmp/etherview-generate-check.XXXXXX)"; \
		trap 'rm -rf "$$snapshot"' EXIT INT TERM; \
		for path in $(GENERATED_PATHS); do \
			test -e "$$path" || { echo "generate-check: missing baseline $$path"; exit 1; }; \
			mkdir -p "$$snapshot/$$(dirname "$$path")"; \
			cp -R "$$path" "$$snapshot/$$path"; \
		done; \
		$(MAKE) --no-print-directory generate; \
		$(NPM) --prefix web run check:api; \
		for path in $(GENERATED_PATHS); do \
			diff -ru "$$snapshot/$$path" "$$path"; \
		done

test-go: web-build
	$(GO) test $(GO_TEST_FLAGS) $(GO_PACKAGES)

test: test-go web-test

test-e2e: web-build
	@set -eu; \
		server_binary="$$(mktemp /tmp/etherview-web-e2e.XXXXXX)"; \
		trap 'rm -f "$$server_binary"' EXIT INT TERM; \
		$(GO) build -o "$$server_binary" ./web/e2e/server; \
		ETHERVIEW_E2E_SERVER_BINARY="$$server_binary" $(NPM) --prefix web run test:e2e

test-race: web-build
	$(GO) test -race $(GO_TEST_FLAGS) $(GO_PACKAGES)

# Integration tests are explicitly skipped when no disposable PostgreSQL URL
# is supplied. CI always supplies one and exercises both migration actions
# before running integration-tagged tests.
test-integration:
	@set -eu; \
	if [ -z "$$INTEGRATION_DATABASE_URL" ]; then \
		echo "test-integration: SKIP (set INTEGRATION_DATABASE_URL to a disposable PostgreSQL database)"; \
		exit 0; \
	fi; \
	ETHERVIEW_DATABASE_URL="$$INTEGRATION_DATABASE_URL" $(GO) run ./cmd/etherview migrate up; \
	ETHERVIEW_DATABASE_URL="$$INTEGRATION_DATABASE_URL" $(GO) run ./cmd/etherview migrate status; \
	ETHERVIEW_TEST_DATABASE_URL="$$INTEGRATION_DATABASE_URL" \
		$(GO) test -count=1 -tags=integration $(GO_PACKAGES)

web-install:
	$(NPM) --prefix web ci --ignore-scripts

web-generate: web-install
	$(NPM) --prefix web run generate:api

web-lint: web-install
	$(NPM) --prefix web run lint

web-test: web-install
	$(NPM) --prefix web run test

web-build: web-generate
	$(NPM) --prefix web run build

lint-go: lint-tool-check
	@unformatted="$$(find . \( -path './.git' -o -path './vendor' -o -path './web/node_modules' \) -prune -o -type f -name '*.go' -exec gofmt -l {} +)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt is required for:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	$(GO) vet $(GO_PACKAGES)
	$(MAKE) golangci-lint

golangci-lint: lint-tool-check
	$(GOLANGCI_LINT) run ./...

lint: lint-go web-lint

install-security-tools:
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)
	$(GO) install github.com/google/go-licenses@$(GO_LICENSES_VERSION)

install-lint-tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint-tool-check:
	@test -x "$(GOLANGCI_LINT)" || { echo "lint-go: missing $(GOLANGCI_LINT); run 'make install-lint-tools'"; exit 1; }

security-tool-check:
	@test -x "$(GOVULNCHECK)" || { echo "security-check: missing $(GOVULNCHECK); run 'make install-security-tools'"; exit 1; }
	@test -x "$(GITLEAKS)" || { echo "security-check: missing $(GITLEAKS); run 'make install-security-tools'"; exit 1; }

security-check: security-tool-check web-build
	$(GOVULNCHECK) $(GO_PACKAGES)
	$(GITLEAKS) dir --no-banner --redact .
	@if git rev-parse --verify HEAD >/dev/null 2>&1; then \
		$(GITLEAKS) git --no-banner --redact --log-opts="--all" .; \
	else \
		echo "gitleaks history: SKIP (repository has no commits yet)"; \
	fi
	$(NPM) --prefix web audit --audit-level=high
	$(GO) test ./internal/auth ./internal/metadata ./internal/verify ./web

license-tool-check:
	@test -x "$(GO_LICENSES)" || { echo "license-check: missing $(GO_LICENSES); run 'make install-security-tools'"; exit 1; }
	@grep -Eq '"license-checker-rseidelsohn": "$(WEB_LICENSE_CHECKER_VERSION)"' web/package.json || { \
		echo "license-check: frontend checker must be pinned at $(WEB_LICENSE_CHECKER_VERSION)"; exit 1; }

license-check: license-tool-check web-install
	@test -f LICENSE || { echo "license-check: root LICENSE is missing"; exit 1; }
	@grep -q "Apache License" LICENSE || { echo "license-check: root LICENSE is not Apache-2.0"; exit 1; }
	@grep -Eq '^COPY .*LICENSE /licenses/LICENSE$$' Dockerfile || { echo "license-check: production image must include /licenses/LICENSE"; exit 1; }
	$(GO_LICENSES) check $(GO_PACKAGES) --allowed_licenses=0BSD,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0
	$(NPM) --prefix web exec -- license-checker-rseidelsohn \
		--start web --production --excludePrivatePackages --summary \
		--onlyAllow '0BSD;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;MIT;MPL-2.0;Unlicense'

docker-check:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-check: docker is required"; exit 1; }
	$(DOCKER) buildx build --check .

docker-build:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-build: docker is required"; exit 1; }
	$(DOCKER) build --target production --tag "$(IMAGE)" .

compose-check:
	@$(DOCKER) compose version >/dev/null 2>&1 || { echo "compose-check: Docker Compose v2 is required"; exit 1; }
	$(DOCKER) compose --profile monolith config --quiet
	$(DOCKER) compose --profile distributed config --quiet
	$(DOCKER) compose --profile accelerators config --quiet

# This schema smoke uses a unique project and disposable volume. API and
# monolith/split-role smoke tests require a deterministic mock RPC and belong
# to the later runtime/release plans.
compose-schema-smoke:
	@set -eu; \
	project="etherview-smoke-$$$$"; \
	export POSTGRES_PASSWORD=etherview-smoke ETHERVIEW_IMAGE="$(IMAGE)"; \
	cleanup() { $(DOCKER) compose -p "$$project" --profile distributed down --volumes --remove-orphans >/dev/null 2>&1 || true; }; \
	trap cleanup EXIT INT TERM; \
	$(DOCKER) compose -p "$$project" --profile distributed up -d --wait postgres; \
	$(DOCKER) compose -p "$$project" --profile distributed run --rm --no-deps migration; \
	$(DOCKER) compose -p "$$project" --profile distributed run --rm --no-deps migration \
		migrate status --config=/etc/etherview/config.yaml

helm-check:
	@command -v "$(HELM)" >/dev/null 2>&1 || { echo "helm-check: helm is required"; exit 1; }
	$(HELM) lint "$(HELM_CHART)"
	$(HELM) lint "$(HELM_CHART)" -f "$(HELM_CHART)/values-distributed.yaml"
	$(HELM) template etherview "$(HELM_CHART)" >/dev/null
	$(HELM) template etherview "$(HELM_CHART)" -f "$(HELM_CHART)/values-distributed.yaml" >/dev/null
	HELM="$(HELM)" "$(HELM_CHART)/tests/render.sh" "$(HELM_CHART)"

deployment-check: docker-check compose-check helm-check

check: toolchain-check security-tool-check license-tool-check plan-check generate-check lint test test-race security-check license-check deployment-check
