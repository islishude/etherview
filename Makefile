SHELL := /bin/sh

GO ?= go
NODE ?= node
NPM ?= npm
DOCKER ?= docker
BUILDX ?= .github/scripts/buildx.sh
COMPOSE ?= .github/scripts/compose.sh
HELM ?= helm
MKCERT ?= mkcert

GO_PACKAGES ?= ./...
GO_TEST_FLAGS ?=
INTEGRATION_DATABASE_URL ?=
INTEGRATION_GO_PACKAGES ?=
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
PREVIEW_APP_SERVICES := api sync enrich trace verify metadata maintenance
PREVIEW_TLS_DIR := .local/preview-tls
PREVIEW_TLS_CERT := $(PREVIEW_TLS_DIR)/tls.crt
PREVIEW_TLS_KEY := $(PREVIEW_TLS_DIR)/tls.key
PREVIEW_TLS_CA := $(PREVIEW_TLS_DIR)/rootCA.pem
PREVIEW_CONFIG_URL ?= https://localhost:8080/api/v1/config
PREVIEW_COMPILER_DIR := .local/preview-compiler
PREVIEW_COMPILER_RUNNER_ARCHIVE := $(PREVIEW_COMPILER_DIR)/compiler-runner.tar
PREVIEW_COMPILER_RUNNER_REFERENCE := $(PREVIEW_COMPILER_DIR)/runner-image.txt
PREVIEW_COMPILER_RUNNER_REPOSITORY := etherview-compiler-runner
PREVIEW_COMPILER_RUNNER_TAG := $(PREVIEW_COMPILER_RUNNER_REPOSITORY):preview
PREVIEW_COMPILER_RUNNER_PLATFORM := linux/amd64
PREVIEW_COMPILER_RUNNER_PLACEHOLDER := $(PREVIEW_COMPILER_RUNNER_REPOSITORY)@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa

.DEFAULT_GOAL := check
.NOTPARALLEL: check generate-check start-preview recreate-preview

.PHONY: \
	check compose-check deployment-check \
	docker-build docker-check docker-image-check \
	go-build generate generate generate-check generate-go helm-check install-lint-tools install-security-tools \
	golangci-lint \
	license-check license-tool-check lint lint-go plan-check security-check \
	security-tool-check test test-go toolchain-check \
	test-e2e test-integration test-integration-race test-load test-race test-runtime-e2e \
	test-hardhat3-verify test-schema-e2e test-soak test-x402-testnet \
	web-build web-generate web-install web-lint web-test preview-cert preview-cert-check preview-check preview-compiler \
	start-preview stop-preview recreate-preview

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
		$(NPM) --prefix api run check:api; \
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

test-hardhat3-verify:
	$(NPM) --prefix e2e/hardhat3 ci --ignore-scripts
	ETHERVIEW_HARDHAT3_NODE="$(NODE)" $(GO) test -count=1 -tags=hardhat3verify ./e2e/hardhat3

# Without INTEGRATION_DATABASE_URL the Go runner owns a fresh PostgreSQL 18
# Compose project. Supplying a URL remains useful for an explicitly disposable
# external database.
test-integration: web-build
	@INTEGRATION_DATABASE_URL="$(INTEGRATION_DATABASE_URL)" COMPOSE="$(COMPOSE)" \
		DOCKER="$(DOCKER)" GO="$(GO)" \
		$(GO) run ./cmd/testintegration -root . -packages "$(INTEGRATION_GO_PACKAGES)"

test-integration-race: web-build
	@INTEGRATION_DATABASE_URL="$(INTEGRATION_DATABASE_URL)" COMPOSE="$(COMPOSE)" \
		DOCKER="$(DOCKER)" GO="$(GO)" \
		$(GO) run ./cmd/testintegration -root . -packages "$(INTEGRATION_GO_PACKAGES)" -race

# This opt-in target sends exactly one real Base Sepolia payment. It is
# intentionally absent from check, CI, and the ordinary integration suite.
test-x402-testnet:
	$(GO) run ./cmd/x402testnet

test-load:
	@$(GO) run ./cmd/loadtest

test-soak:
	@ETHERVIEW_LOAD_RATE=500 \
		ETHERVIEW_LOAD_DURATION=30m \
		ETHERVIEW_LOAD_CONCURRENCY=512 \
		ETHERVIEW_LOAD_REQUEST_TIMEOUT=5s \
		ETHERVIEW_LOAD_MAX_P95=500ms \
		ETHERVIEW_LOAD_MAX_ERROR_RATE=0.001 \
		ETHERVIEW_LOAD_MIN_THROUGHPUT_RATIO=0.99 \
		ETHERVIEW_LOAD_MAX_LAG=2 \
		ETHERVIEW_LOAD_PROFILE=p70-reference-capacity \
		$(GO) run ./cmd/loadtest

web-install:
	$(NPM) --prefix web ci
	$(NPM) --prefix api ci

web-generate: web-install
	$(NPM) --prefix api run generate:api

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
	@$(MAKE) golangci-lint

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
	@command -v "$(GOLANGCI_LINT)" >/dev/null 2>&1 || { echo "lint-go: missing $(GOLANGCI_LINT); run 'make install-lint-tools'"; exit 1; }

security-tool-check:
	@command -v "$(GOVULNCHECK)" >/dev/null 2>&1 || { echo "security-check: missing $(GOVULNCHECK); run 'make install-security-tools'"; exit 1; }
	@command -v "$(GITLEAKS)" >/dev/null 2>&1 || { echo "security-check: missing $(GITLEAKS); run 'make install-security-tools'"; exit 1; }

security-check: security-tool-check web-build
	$(GOVULNCHECK) $(GO_PACKAGES)
	$(GITLEAKS) dir --no-banner --redact .
	@if git rev-parse --verify HEAD >/dev/null 2>&1; then \
		$(GITLEAKS) git --no-banner --redact --log-opts="--all" .; \
	else \
		echo "gitleaks history: SKIP (repository has no commits yet)"; \
	fi
	$(NPM) --prefix api audit --audit-level=high
	$(NPM) --prefix web audit --audit-level=high
	$(NPM) --prefix e2e/hardhat3 audit --audit-level=high
	$(GO) test ./internal/app ./internal/auth ./internal/billing/... ./internal/cli ./internal/config ./internal/httpapi ./internal/jsonstrict ./internal/metadata ./internal/observability ./internal/userauth ./internal/verify ./web

license-tool-check:
	@command -v "$(GO_LICENSES)" >/dev/null 2>&1 || { echo "license-check: missing $(GO_LICENSES); run 'make install-security-tools'"; exit 1; }
	@grep -Eq '"license-checker-rseidelsohn": "$(WEB_LICENSE_CHECKER_VERSION)"' web/package.json || { \
		echo "license-check: frontend checker must be pinned at $(WEB_LICENSE_CHECKER_VERSION)"; exit 1; }

license-check: license-tool-check web-install
	@test -f LICENSE || { echo "license-check: root LICENSE is missing"; exit 1; }
	@grep -q "Apache License" LICENSE || { echo "license-check: root LICENSE is not Apache-2.0"; exit 1; }
	@grep -Eq '^COPY .*LICENSE /LICENSE$$' Dockerfile || { echo "license-check: production image must include /LICENSE"; exit 1; }
	@test -f THIRD_PARTY_NOTICES.md || { echo "license-check: third-party notices are missing"; exit 1; }
	@grep -Eq '^COPY .*THIRD_PARTY_NOTICES.md /THIRD_PARTY_NOTICES.md$$' Dockerfile || { echo "license-check: production image must include third-party notices"; exit 1; }
	@test -f licenses/holiman-bloomfilter-MIT.txt || { echo "license-check: checked-in bloomfilter license is missing"; exit 1; }
	@grep -Eq '^COPY --from=go-builder .* /licenses /licenses$$' Dockerfile || { echo "license-check: production image must include reviewed geth licenses"; exit 1; }
	@grep -Eq '^COPY .*licenses /licenses$$' Dockerfile || { echo "license-check: production image must include checked-in third-party licenses"; exit 1; }
	GO="$(GO)" GO_LICENSES="$(GO_LICENSES)" sh .github/scripts/go-license-check.sh $(GO_PACKAGES)
	$(NPM) --prefix web exec -- license-checker-rseidelsohn \
		--start web --production --excludePrivatePackages --summary \
		--onlyAllow '0BSD;Apache-2.0;BSD-2-Clause;BSD-3-Clause;ISC;MIT;MPL-2.0;Unlicense'

docker-check:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-check: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --check .

docker-build:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-build: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --load --target production --tag "$(IMAGE)" .

docker-image-check:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-image-check: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" deploy/check-image.sh

compose-check:
	@DOCKER="$(DOCKER)" $(COMPOSE) version >/dev/null 2>&1 || { echo "compose-check: Docker Compose is required"; exit 1; }
	@command -v "$(NODE)" >/dev/null 2>&1 || { echo "compose-check: Node.js is required for rendered environment checks"; exit 1; }
	@test ! -e compose.tls.yaml || { echo "compose-check: compose.tls.yaml must remain removed"; exit 1; }
	DOCKER="$(DOCKER)" $(COMPOSE) --profile monolith config --quiet
	DOCKER="$(DOCKER)" $(COMPOSE) --profile distributed config --quiet
	DOCKER="$(DOCKER)" $(COMPOSE) --profile accelerators config --quiet
	ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$(PREVIEW_COMPILER_RUNNER_PLACEHOLDER)" \
		DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml config --quiet
	@DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml config --format json | \
		$(NODE) -e 'const config = JSON.parse(require("fs").readFileSync(0, "utf8")); const roles = ["api", "sync", "enrich", "trace", "verify", "metadata", "maintenance"]; const tlsKeys = ["ETHERVIEW_SERVER_TLS_CERT_FILE", "ETHERVIEW_SERVER_TLS_KEY_FILE"]; const tlsTargets = ["/run/etherview-tls/tls.crt", "/run/etherview-tls/tls.key"]; if (config.services.etherview) throw new Error("Preview monolith service must not exist"); for (const role of roles) { const service = config.services[role]; if (!service) throw new Error("missing Preview role service " + role); if (service.environment.ETHERVIEW_ROLES !== role) throw new Error("Preview role mismatch for " + role); if (!Object.hasOwn(service.environment, "ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL")) throw new Error("Preview sync progress interval override missing from " + role); if (!service.command.includes("--roles=" + role)) throw new Error("Preview command mismatch for " + role); for (const dependency of ["postgres", "migration", "reth"]) if (!service.depends_on[dependency]) throw new Error("Preview " + role + " missing dependency " + dependency); if (role !== "api" && service.ports?.length) throw new Error("Preview worker must not publish ports: " + role); } const api = config.services.api; const apiTargets = new Set(api.ports.map((port) => port.target)); if (apiTargets.size !== 2 || !apiTargets.has(8080) || !apiTargets.has(9090)) throw new Error("Preview API must publish only application and metrics ports"); if (!api.environment.ETHERVIEW_SESSION_PEPPER) throw new Error("Preview API session pepper is required"); for (const key of tlsKeys) if (!api.environment[key]) throw new Error("Preview API TLS environment missing: " + key); for (const target of tlsTargets) if (!api.volumes.some((volume) => volume.target === target && volume.read_only)) throw new Error("Preview API read-only TLS mount missing: " + target); for (const [name, service] of Object.entries(config.services)) { if (name !== "api" && Object.hasOwn(service.environment || {}, "ETHERVIEW_SESSION_PEPPER")) throw new Error("Preview session pepper leaked to " + name); if (name !== "api" && tlsKeys.some((key) => Object.hasOwn(service.environment || {}, key))) throw new Error("Preview TLS environment leaked to " + name); if (name !== "api" && (service.volumes || []).some((volume) => tlsTargets.includes(volume.target))) throw new Error("Preview TLS material leaked to " + name); }'
	@ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$(PREVIEW_COMPILER_RUNNER_PLACEHOLDER)" \
		DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml config --format json | \
		ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$(PREVIEW_COMPILER_RUNNER_PLACEHOLDER)" \
		$(NODE) .github/scripts/preview-compose-check.mjs
	@ETHERVIEW_METADATA_IPFS_GATEWAY=https://gateway.example.com \
		ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$(PREVIEW_COMPILER_RUNNER_PLACEHOLDER)" \
		DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml config --format json | \
		$(NODE) -e 'const services = JSON.parse(require("fs").readFileSync(0, "utf8")).services; for (const role of ["api", "sync", "enrich", "trace", "verify", "metadata", "maintenance"]) if (services[role].environment.ETHERVIEW_METADATA_IPFS_GATEWAY !== "https://gateway.example.com") throw new Error("Preview metadata gateway override missing from " + role);'
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml -f e2e/runtime/compose.yaml \
		--profile monolith config --quiet
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml -f e2e/runtime/compose.yaml \
		--profile distributed config --quiet
	@unset ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS ETHERVIEW_LOG_LEVEL ETHERVIEW_LOG_FORMAT ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL; \
		DOCKER="$(DOCKER)" $(COMPOSE) --env-file /dev/null --profile monolith config --format json | \
		$(NODE) -e 'const env = JSON.parse(require("fs").readFileSync(0, "utf8")).services.etherview.environment; for (const key of ["ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS", "ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS", "ETHERVIEW_LOG_LEVEL", "ETHERVIEW_LOG_FORMAT", "ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL"]) { if (env[key] !== null) throw new Error(key + " must remain unset when no Compose override is supplied"); }'
	@ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS=7 ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS=1 ETHERVIEW_LOG_LEVEL=debug ETHERVIEW_LOG_FORMAT=text ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL=45s \
		DOCKER="$(DOCKER)" $(COMPOSE) --env-file /dev/null --profile monolith config --format json | \
		$(NODE) -e 'const env = JSON.parse(require("fs").readFileSync(0, "utf8")).services.etherview.environment; if (env.ETHERVIEW_DATABASE_READ_MAX_CONNECTIONS !== "7" || env.ETHERVIEW_DATABASE_READ_MIN_CONNECTIONS !== "1") throw new Error("Compose reader-pool overrides were not preserved"); if (env.ETHERVIEW_LOG_LEVEL !== "debug" || env.ETHERVIEW_LOG_FORMAT !== "text" || env.ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL !== "45s") throw new Error("Compose logging overrides were not preserved");'

test-schema-e2e: docker-build
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) run ./cmd/testschemae2e -root .

test-runtime-e2e: docker-build
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) test -count=1 -v -tags=runtimee2e ./e2e/runtime

helm-check:
	@command -v "$(HELM)" >/dev/null 2>&1 || { echo "helm-check: helm is required"; exit 1; }
	$(HELM) lint "$(HELM_CHART)"
	$(HELM) lint "$(HELM_CHART)" -f "$(HELM_CHART)/values-distributed.yaml"
	$(HELM) lint "$(HELM_CHART)" -f "$(HELM_CHART)/values-reference-capacity.yaml"
	$(HELM) template etherview "$(HELM_CHART)" >/dev/null
	$(HELM) template etherview "$(HELM_CHART)" -f "$(HELM_CHART)/values-distributed.yaml" >/dev/null
	$(HELM) template etherview "$(HELM_CHART)" -f "$(HELM_CHART)/values-reference-capacity.yaml" >/dev/null
	HELM="$(HELM)" "$(HELM_CHART)/tests/render.sh" "$(HELM_CHART)"

deployment-check: docker-check compose-check helm-check

check: toolchain-check security-tool-check license-tool-check plan-check generate-check lint test test-race security-check license-check deployment-check

preview-cert:
	@command -v "$(MKCERT)" >/dev/null 2>&1 || { echo "preview-cert: mkcert is required"; exit 1; }
	@mkdir -p "$(PREVIEW_TLS_DIR)"
	$(MKCERT) -install
	$(MKCERT) -ecdsa -cert-file "$(PREVIEW_TLS_CERT)" -key-file "$(PREVIEW_TLS_KEY)" \
		etherview.localhost localhost 127.0.0.1 ::1
	@set -eu; \
		ca_root="$$("$(MKCERT)" -CAROOT)"; \
		test -r "$$ca_root/rootCA.pem" || { \
			echo "preview-cert: mkcert public root CA is unreadable" >&2; \
			exit 1; \
		}; \
		cp "$$ca_root/rootCA.pem" "$(PREVIEW_TLS_CA)"
	@chmod 600 "$(PREVIEW_TLS_KEY)"
	@chmod 644 "$(PREVIEW_TLS_CERT)" "$(PREVIEW_TLS_CA)"
	@echo "preview-cert: wrote $(PREVIEW_TLS_CERT), $(PREVIEW_TLS_KEY), and public $(PREVIEW_TLS_CA)"

preview-cert-check:
	@test -r "$(PREVIEW_TLS_CERT)" && test -r "$(PREVIEW_TLS_KEY)" && test -r "$(PREVIEW_TLS_CA)" || { \
		echo "Preview TLS certificate or public root CA missing; run 'make preview-cert' first"; \
		exit 1; \
	}

preview-check: preview-cert-check
	@test -r "$(PREVIEW_COMPILER_RUNNER_REFERENCE)" || { \
		echo "Preview compiler reference missing; run 'make preview-compiler' first"; \
		exit 1; \
	}
	@set -eu; \
		runner_image="$$(sed -n '1p' "$(PREVIEW_COMPILER_RUNNER_REFERENCE)")"; \
		COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" $(GO) run ./cmd/previewcheck \
			-root=. \
			-project=etherview-preview \
			-compose="$(COMPOSE)" \
			-compose-file=compose.preview.yaml \
			-docker="$(DOCKER)" \
			-runner-image="$$runner_image" \
			-config-url="$(PREVIEW_CONFIG_URL)" \
			-ca-file="$(PREVIEW_TLS_CA)"

preview-compiler:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "preview-compiler: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --load --platform "$(PREVIEW_COMPILER_RUNNER_PLATFORM)" \
		--target compiler-runner \
		--tag "$(PREVIEW_COMPILER_RUNNER_TAG)" .
	@mkdir -p "$(PREVIEW_COMPILER_DIR)"
	@set -eu; \
		runner_id="$$( "$(DOCKER)" image inspect \
			--format '{{.Id}}' "$(PREVIEW_COMPILER_RUNNER_TAG)" )"; \
		case "$$runner_id" in \
			sha256:*) ;; \
			*) echo "preview-compiler: build did not produce an exact image content digest" >&2; exit 1 ;; \
		esac; \
		runner_digest="$${runner_id#sha256:}"; \
		test "$${#runner_digest}" -eq 64 || { \
			echo "preview-compiler: malformed runner digest" >&2; exit 1; \
		}; \
		runner_image="$(PREVIEW_COMPILER_RUNNER_REPOSITORY)@$$runner_id"; \
		"$(DOCKER)" image inspect "$$runner_image" >/dev/null; \
		archive_tmp="$(PREVIEW_COMPILER_RUNNER_ARCHIVE).tmp"; \
		reference_tmp="$(PREVIEW_COMPILER_RUNNER_REFERENCE).tmp"; \
		trap 'rm -f "$$archive_tmp" "$$reference_tmp"' EXIT INT TERM; \
		"$(DOCKER)" image save --output "$$archive_tmp" "$(PREVIEW_COMPILER_RUNNER_TAG)"; \
		chmod 0444 "$$archive_tmp"; \
		printf '%s\n' "$$runner_image" >"$$reference_tmp"; \
		mv -f "$$archive_tmp" "$(PREVIEW_COMPILER_RUNNER_ARCHIVE)"; \
		mv -f "$$reference_tmp" "$(PREVIEW_COMPILER_RUNNER_REFERENCE)"; \
		trap - EXIT INT TERM; \
		echo "preview-compiler: prepared $$runner_image"

start-preview: preview-cert-check docker-build preview-compiler
	@set -eu; \
		runner_image="$$(sed -n '1p' "$(PREVIEW_COMPILER_RUNNER_REFERENCE)")"; \
		ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$$runner_image" \
			DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml \
			up --no-build --wait --wait-timeout 180 --remove-orphans
	@$(MAKE) --no-print-directory preview-check

stop-preview:
	@DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml down --volumes --remove-orphans

recreate-preview: preview-cert-check docker-build preview-compiler
	@set -eu; \
		runner_image="$$(sed -n '1p' "$(PREVIEW_COMPILER_RUNNER_REFERENCE)")"; \
		ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$$runner_image" \
			DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml \
			rm -fs compiler-volumes-init compiler-preflight $(PREVIEW_APP_SERVICES); \
		ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE="$$runner_image" \
			DOCKER="$(DOCKER)" $(COMPOSE) -f compose.preview.yaml \
			up -d --no-build --wait --wait-timeout 180 --remove-orphans $(PREVIEW_APP_SERVICES)
	@$(MAKE) --no-print-directory preview-check
