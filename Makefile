SHELL := /bin/sh

# Tools and configuration

GO ?= go
NODE ?= node
PNPM ?= pnpm
PYTHON ?= python3.13
DOCKER ?= docker
BUILDX ?= .github/scripts/buildx.sh
COMPOSE ?= .github/scripts/compose.sh
HELM ?= helm
MKCERT ?= mkcert

GO_PACKAGES ?= ./...
GO_TEST_FLAGS ?=
INTEGRATION_DATABASE_URL ?=
INTEGRATION_GO_PACKAGES ?=
INTEGRATION_SHARD ?= 1
INTEGRATION_SHARDS ?= 1
GO_BUILD_OUTPUT ?= ./etherview
GO_BUILD_FLAGS ?= -trimpath
GO_BUILD_LDFLAGS ?= -s -w
BENCHTIME ?= 1s

GOVULNCHECK ?= govulncheck
GITLEAKS ?= gitleaks
GO_LICENSES ?= go-licenses
GOLANGCI_LINT ?= golangci-lint

GOVULNCHECK_VERSION ?= v1.9.0
GITLEAKS_VERSION ?= v8.30.1
GO_LICENSES_VERSION ?= v2.0.1
GOLANGCI_LINT_VERSION ?= v2.14.0
WEB_LICENSE_CHECKER_VERSION ?= 5.0.1
MKCERT_VERSION ?= v1.4.4

GENERATED_PATHS := \
	internal/verify/vyper_runtime_policy_generated.go \
	internal/api/gen/models.gen.go \
	internal/db/gen \
	internal/enrich/manifests/safe-proxy-fingerprints.json \
	web/src/api/heyapi

ETHERVIEW_WEB_BRANDING_DIR ?= web/branding/default
export ETHERVIEW_WEB_BRANDING_DIR

IMAGE ?= etherview:local
HARDHAT3_IMAGE ?= etherview-hardhat3:local
# Enable 2 by default only after matching native AMD64/ARM64 measurements.
VERIFICATION_PARALLEL ?= 1
FOUNDRY_IMAGE ?= etherview-foundry:local
PREVIEW_GATEWAY_IMAGE ?= caddy:2.11.7-alpine
export PREVIEW_GATEWAY_IMAGE
HELM_CHART ?= deploy/helm/etherview
PREVIEW_APP_SERVICES := api sync enrich trace metadata maintenance
PREVIEW_RUNTIME_SERVICES := bundler x402-facilitator geth lighthouse validator object-storage ipfs preview-gateway migration $(PREVIEW_APP_SERVICES)
PREVIEW_ENV_FILE ?= .local/preview-push.env
PREVIEW_TLS_DIR := .local/preview-tls
PREVIEW_TLS_CA := $(PREVIEW_TLS_DIR)/rootCA.pem
PREVIEW_GATEWAY_CERT := $(PREVIEW_TLS_DIR)/gateway.crt
PREVIEW_GATEWAY_KEY := $(PREVIEW_TLS_DIR)/gateway.key
X402_LOCAL_COMPOSE := e2e/x402local/compose.yaml
X402_LOCAL_TOPOLOGY ?= monolith

.DEFAULT_GOAL := check
.NOTPARALLEL: check generate generate-check start-preview recreate-preview test-preview-metadata test-preview-consensus test-preview-x402 start-x402-local recreate-x402-local

# Arguments: image reference, failure message. Keep checks at their call sites.
define require-image
$(DOCKER) image inspect "$(1)" >/dev/null 2>&1 || { \
	echo "$(2)"; \
	exit 1; \
}
endef

# Build and generation

.PHONY: pnpm-install web-install compiler-install go-build web-build web-generate generate-go \
	generate generate-check docker-build hardhat3-client-image-build foundry-client-image-build

pnpm-install:
	$(PNPM) install --frozen-lockfile

web-install: pnpm-install

compiler-install: pnpm-install
	$(NODE) compiler/vyper/wasm/prepare.mjs
	$(NODE) compiler/vyper/wasm/packages.mjs --version 0.4.3
	$(PNPM) --dir compiler run build:sea "$(CURDIR)/.local/vyper-wasm/sea/etherview-solcjs"

go-build: web-build
	$(GO) build $(GO_BUILD_FLAGS) -ldflags="$(GO_BUILD_LDFLAGS)" -o $(GO_BUILD_OUTPUT) ./cmd/etherview

web-build: web-install
	$(PNPM) --dir web run build

web-generate: web-install
	$(PNPM) --dir api run generate:api

generate-go:
	$(NODE) compiler/vyper/wasm/generate-policy.mjs
	$(GO) generate $(GO_PACKAGES)
	$(GO) tool sqlc generate

generate: generate-go web-generate web-build

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
		for path in $(GENERATED_PATHS); do \
			diff -ru "$$snapshot/$$path" "$$path"; \
		done

docker-build:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-build: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --load --target production --tag "$(IMAGE)" \
		--build-arg "ETHERVIEW_WEB_BRANDING_DIR=$${ETHERVIEW_WEB_BRANDING_DIR:-web/branding/default}" .

hardhat3-client-image-build:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "hardhat3-client-image-build: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --load \
		--tag "$(HARDHAT3_IMAGE)" -f e2e/hardhat3/Dockerfile .

foundry-client-image-build:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "foundry-client-image-build: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --load \
		--tag "$(FOUNDRY_IMAGE)" e2e/foundry

test-preview-gateway:
	$(DOCKER) pull "$(PREVIEW_GATEWAY_IMAGE)"
	PREVIEW_GATEWAY_IMAGE="$(PREVIEW_GATEWAY_IMAGE)" DOCKER="$(DOCKER)" $(GO) test -race -count=1 -tags=previewgatewaye2e -timeout=10m ./e2e/previewgateway


# Quality checks

.PHONY: check toolchain-check plan-check docs-check openapi-check source-check format format-check \
	repo-format-check web-format-check preview-lint lint lint-go golangci-lint test-lint-policy web-lint install-lint-tools \
	lint-tool-check install-security-tools security-tool-check security-check license-tool-check \
	license-check

check: toolchain-check security-tool-check license-tool-check docs-check plan-check openapi-check generate-check lint test test-race security-check license-check deployment-check

toolchain-check:
	@.github/scripts/toolchain-check.sh

plan-check:
	$(GO) run ./cmd/plancheck -root .

docs-check:
	$(NODE) .github/scripts/docs-check.mjs

openapi-check: pnpm-install
	$(PNPM) --dir api run check:schema
	$(PNPM) --dir api run test

source-check:
	$(GO) test -count=1 ./internal/sourcecheck

format: pnpm-install
	$(PNPM) run format

format-check: pnpm-install
	$(PNPM) run format:check

repo-format-check: pnpm-install
	$(PNPM) run format:repo:check

web-format-check: web-install
	$(PNPM) --dir web run format:check

lint: lint-go pnpm-install
	$(PNPM) run lint

lint-go: lint-tool-check source-check web-build
	@unformatted="$$(find . \( -path './.git' -o -path './.local' -o -path './vendor' -o -name node_modules \) -prune -o -type f -name '*.go' -exec gofmt -l {} +)"; \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt is required for:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	$(GO) vet $(GO_PACKAGES)
	@$(MAKE) golangci-lint

golangci-lint: lint-tool-check test-lint-policy
	$(GOLANGCI_LINT) run ./...

test-lint-policy: lint-tool-check
	GOLANGCI_LINT="$(GOLANGCI_LINT)" $(NODE) --test .github/scripts/lint-policy.test.mjs

preview-lint: pnpm-install
	$(PNPM) run lint:preview

web-lint: web-install web-format-check
	$(PNPM) --dir web run lint:code

install-lint-tools:
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)

lint-tool-check:
	@command -v "$(GOLANGCI_LINT)" >/dev/null 2>&1 || { echo "lint-go: missing $(GOLANGCI_LINT); run 'make install-lint-tools'"; exit 1; }

install-security-tools:
	$(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	$(GO) install github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION)
	$(GO) install github.com/google/go-licenses/v2@$(GO_LICENSES_VERSION)

security-tool-check:
	@command -v "$(GOVULNCHECK)" >/dev/null 2>&1 || { echo "security-check: missing $(GOVULNCHECK); run 'make install-security-tools'"; exit 1; }
	@command -v "$(GITLEAKS)" >/dev/null 2>&1 || { echo "security-check: missing $(GITLEAKS); run 'make install-security-tools'"; exit 1; }

security-check: security-tool-check web-build compiler-install
	$(GOVULNCHECK) $(GO_PACKAGES)
	GITLEAKS="$(GITLEAKS)" $(NODE) --test .github/scripts/gitleaks-check.test.mjs
	$(GITLEAKS) dir --no-banner --redact .
	@if git rev-parse --verify HEAD >/dev/null 2>&1; then \
		$(GITLEAKS) git --no-banner --redact --log-opts="--all" .; \
	else \
		echo "gitleaks history: SKIP (repository has no commits yet)"; \
	fi
	$(PNPM) audit --audit-level=high
	$(NODE) compiler/vyper/wasm/packages.mjs
	$(NODE) compiler/vyper/wasm/check-integrity.mjs
	$(GO) test ./internal/app ./internal/auth ./internal/billing/... ./internal/cli ./internal/config ./internal/httpapi ./internal/jsonstrict ./internal/metadata ./internal/observability ./internal/userauth ./internal/verify ./web

license-tool-check:
	@command -v "$(GO_LICENSES)" >/dev/null 2>&1 || { echo "license-check: missing $(GO_LICENSES); run 'make install-security-tools'"; exit 1; }
	@grep -Eq '"license-checker-rseidelsohn": "$(WEB_LICENSE_CHECKER_VERSION)"' package.json || { \
		echo "license-check: frontend checker must be pinned at $(WEB_LICENSE_CHECKER_VERSION)"; exit 1; }

license-check: license-tool-check web-install compiler-install
	$(NODE) compiler/vyper/wasm/packages.mjs
	$(NODE) compiler/vyper/wasm/licenses.mjs
	$(NODE) compiler/vyper/wasm/package-licenses.mjs
	$(NODE) --test compiler/check-licenses.test.mjs
	$(NODE) compiler/check-licenses.mjs
	@test -f licenses/LICENSE || { echo "license-check: project LICENSE is missing"; exit 1; }
	@grep -q "Apache License" licenses/LICENSE || { echo "license-check: project LICENSE is not Apache-2.0"; exit 1; }
	@grep -Fq '## Node SEA and solc-js runtime' licenses/THIRD_PARTY_NOTICES.md || { echo "license-check: Node SEA notice is missing"; exit 1; }
	@grep -Eq '^COPY .*licenses /licenses$$' Dockerfile || { echo "license-check: production image must include checked-in licenses"; exit 1; }
	GO="$(GO)" GO_LICENSES="$(GO_LICENSES)" sh .github/scripts/go-license-check.sh $(GO_PACKAGES)
	$(PNPM) run check:licenses


# Tests and benchmarks

.PHONY: test test-go web-test test-race test-workflow-scripts test-integration test-integration-race \
	test-rustfs test-e2e test-schema-e2e test-schema-e2e-prebuilt test-runtime-e2e \
	test-runtime-e2e-prebuilt test-preview-metadata preview-e2e-check test-preview-metadata-prebuilt \
	test-preview-consensus test-preview-consensus-prebuilt test-hardhat3-provider-compat \
	test-hardhat3-e2e test-hardhat3-offline-compile test-hardhat3-e2e-prebuilt test-foundry-e2e \
	test-foundry-offline-compile test-foundry-e2e-prebuilt test-vyper-e2e test-vyper-e2e-prebuilt \
	test-vyper-matrix test-vyper-release test-vyper-wasm-candidate \
	test-vyper-update benchmark benchmark-integration benchmark-vyper-wasm test-load test-soak

test: test-go web-test test-workflow-scripts test-vyper-release

test-go: web-build compiler-install
	$(GO) test $(GO_TEST_FLAGS) $(GO_PACKAGES)

web-test: web-install
	$(PNPM) --dir web run test

test-race: web-build compiler-install
	$(GO) test -race $(GO_TEST_FLAGS) $(GO_PACKAGES)

test-workflow-scripts: test-dependency-updates
	$(NODE) --test .github/scripts/workflow-scripts.test.mjs .github/scripts/production-evidence.test.mjs .github/scripts/make-targets.test.mjs

# Without INTEGRATION_DATABASE_URL the Go runner owns a fresh PostgreSQL 18
# Compose project. Supplying a URL remains useful for an explicitly disposable
# external database.
test-integration: web-build compiler-install
	@INTEGRATION_DATABASE_URL="$(INTEGRATION_DATABASE_URL)" COMPOSE="$(COMPOSE)" \
		DOCKER="$(DOCKER)" GO="$(GO)" \
		$(GO) run ./cmd/testintegration -root . -packages "$(INTEGRATION_GO_PACKAGES)" \
			-shard "$(INTEGRATION_SHARD)" -shards "$(INTEGRATION_SHARDS)"

test-integration-race: web-build compiler-install
	@INTEGRATION_DATABASE_URL="$(INTEGRATION_DATABASE_URL)" COMPOSE="$(COMPOSE)" \
		DOCKER="$(DOCKER)" GO="$(GO)" \
		$(GO) run ./cmd/testintegration -root . -packages "$(INTEGRATION_GO_PACKAGES)" -race \
			-shard "$(INTEGRATION_SHARD)" -shards "$(INTEGRATION_SHARDS)"

test-rustfs:
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(GO) test $(GO_TEST_FLAGS) -tags rustfs -run '^TestRustFS$$' -count=1 -timeout=6m ./internal/accelerator

test-e2e: web-build
	@set -eu; \
		server_binary="$$(mktemp /tmp/etherview-web-e2e.XXXXXX)"; \
		trap 'rm -f "$$server_binary"' EXIT INT TERM; \
		$(GO) build -o "$$server_binary" ./web/e2e/server; \
		ETHERVIEW_E2E_SERVER_BINARY="$$server_binary" $(PNPM) --dir web run test:e2e

test-schema-e2e: docker-build
	@$(MAKE) --no-print-directory test-schema-e2e-prebuilt

test-schema-e2e-prebuilt:
	@$(call require-image,$(IMAGE),test-schema-e2e-prebuilt: image $(IMAGE) is not loaded; run 'make docker-build' first)
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) run ./cmd/testschemae2e -root .

test-runtime-e2e: docker-build
	@$(MAKE) --no-print-directory test-runtime-e2e-prebuilt

test-runtime-e2e-prebuilt:
	@$(call require-image,$(IMAGE),test-runtime-e2e-prebuilt: image $(IMAGE) is not loaded; run 'make docker-build' first)
	# Both packages own multi-container production topologies. Keep package tests
	# serial so the bounded runtime load gate does not compete with x402 Compose.
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) test -p=1 -count=1 -v -tags=runtimee2e ./e2e/runtime ./e2e/x402local

test-preview-metadata: pnpm-install preview-cert-check docker-build preview-images
	@$(MAKE) --no-print-directory test-preview-metadata-prebuilt

preview-e2e-check: preview-cert-check
	@for image in "$(IMAGE)" "$(PREVIEW_GATEWAY_IMAGE)" etherview-preview-bundler:local etherview-x402-local-facilitator:local; do \
		$(call require-image,$$image,Preview image $$image is not loaded; run 'make docker-build preview-images' first); \
	done

test-preview-metadata-prebuilt: preview-e2e-check
	@PREVIEW_GATEWAY_IMAGE="$(PREVIEW_GATEWAY_IMAGE)" COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) test -count=1 -v -tags=previewmetadatae2e -run TestPreviewLocalNFTMetadata ./e2e/previewmetadata

.PHONY: test-preview-x402 test-preview-x402-prebuilt

test-preview-x402: preview-cert-check docker-build preview-images
	@$(MAKE) --no-print-directory test-preview-x402-prebuilt

test-preview-x402-prebuilt: preview-e2e-check
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) test -count=1 -v -timeout=12m -tags=runtimee2e,previewx402e2e -run '^TestPreviewX402$$' ./e2e/x402local

test-preview-consensus: preview-cert-check docker-build preview-images
	@$(MAKE) --no-print-directory test-preview-consensus-prebuilt

test-preview-consensus-prebuilt: preview-e2e-check
	@PREVIEW_GATEWAY_IMAGE="$(PREVIEW_GATEWAY_IMAGE)" COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" \
		$(GO) test -count=1 -v -timeout=25m -tags=previewmetadatae2e -run TestPreviewConsensus ./e2e/previewmetadata

test-hardhat3-provider-compat: pnpm-install
	$(NODE) --test e2e/hardhat3/safe-artifact.test.mjs e2e/hardhat3/transaction-sender.test.mjs
	ETHERVIEW_HARDHAT3_NODE="$(NODE)" $(GO) test -count=1 -tags=hardhat3verify ./e2e/hardhat3

test-hardhat3-e2e: docker-build hardhat3-client-image-build
	@$(MAKE) --no-print-directory test-hardhat3-e2e-prebuilt

test-hardhat3-offline-compile:
	@$(call require-image,$(HARDHAT3_IMAGE),test-hardhat3-offline-compile: Hardhat client $(HARDHAT3_IMAGE) is not loaded)
	@$(DOCKER) run --rm --network none "$(HARDHAT3_IMAGE)" \
		pnpm exec hardhat --build-profile production compile

test-hardhat3-e2e-prebuilt: test-hardhat3-offline-compile
	@case "$(VERIFICATION_PARALLEL)" in 1|2) ;; *) echo "VERIFICATION_PARALLEL must be 1 or 2"; exit 1;; esac
	@$(call require-image,$(IMAGE),test-hardhat3-e2e-prebuilt: image $(IMAGE) is not loaded)
	@$(call require-image,$(HARDHAT3_IMAGE),test-hardhat3-e2e-prebuilt: Hardhat client $(HARDHAT3_IMAGE) is not loaded)
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" NODE="$(NODE)" \
		ETHERVIEW_HARDHAT3_IMAGE="$(HARDHAT3_IMAGE)" \
		$(GO) test -count=1 -v -timeout=30m -tags='runtimee2e verificatione2e' \
		-parallel=$(VERIFICATION_PARALLEL) -run '^(TestHardhat3(ProductionE2E|VerificationCounts)|TestVerificationTopologyScheduling|TestBytecodeDBProductionE2E)$$' ./e2e/runtime

test-foundry-e2e: docker-build foundry-client-image-build
	@$(MAKE) --no-print-directory test-foundry-e2e-prebuilt

test-foundry-offline-compile:
	@$(call require-image,$(FOUNDRY_IMAGE),test-foundry-offline-compile: Foundry client $(FOUNDRY_IMAGE) is not loaded)
	@$(DOCKER) run --rm --network none "$(FOUNDRY_IMAGE)" \
		forge build --offline --force --silent

test-foundry-e2e-prebuilt: test-foundry-offline-compile
	@$(call require-image,$(IMAGE),test-foundry-e2e-prebuilt: image $(IMAGE) is not loaded)
	@$(call require-image,$(FOUNDRY_IMAGE),test-foundry-e2e-prebuilt: Foundry client $(FOUNDRY_IMAGE) is not loaded)
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" NODE="$(NODE)" \
		ETHERVIEW_FOUNDRY_IMAGE="$(FOUNDRY_IMAGE)" \
		$(GO) test -count=1 -v -tags='runtimee2e foundrye2e' \
		-run '^TestFoundryProductionVerificationE2E$$' ./e2e/runtime

test-vyper-e2e: docker-build
	@$(MAKE) --no-print-directory test-vyper-e2e-prebuilt

test-vyper-e2e-prebuilt:
	@case "$(VERIFICATION_PARALLEL)" in 1|2) ;; *) echo "VERIFICATION_PARALLEL must be 1 or 2"; exit 1;; esac
	@$(DOCKER) image inspect "$(IMAGE)" >/dev/null
	@COMPOSE="$(COMPOSE)" DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" NODE="$(NODE)" \
		$(GO) test -count=1 -v -timeout=30m -tags='runtimee2e verificatione2e' \
		-parallel=$(VERIFICATION_PARALLEL) -run '^TestVyperProductionE2E$$' ./e2e/runtime

test-vyper-matrix: compiler-install test-vyper-wasm-candidate
	ETHERVIEW_TEST_VYPER_MATRIX_ROOT="$(CURDIR)/.local/vyper-wasm/packages" $(GO) test ./internal/verify -run '^TestVyper(DynamicRuntimeMatrix|StableVersionMatrix|MatrixSchedulingFailure)$$' -count=1 -parallel=4 -timeout=30m

test-vyper-release: test-vyper-update
	$(NODE) --test compiler/vyper/wasm/tooling.test.mjs
	$(NODE) --test compiler/vyper/acceptance.test.mjs compiler/vyper/wasm/production-shards.test.mjs

test-vyper-wasm-candidate: pnpm-install
	$(NODE) --test compiler/vyper/wasm/tooling.test.mjs
	$(NODE) compiler/vyper/wasm/check.mjs

test-vyper-update: pnpm-install
	$(NODE) compiler/vyper/update.mjs --check
	$(NODE) --test compiler/vyper/update.test.mjs
	uv run --project compiler/vyper --locked python compiler/vyper/update_metadata_test.py

benchmark:
	$(GO) test -run '^$$' -bench '^BenchmarkBundleBoundary$$' -benchmem \
		-benchtime="$(BENCHTIME)" ./internal/chainbundle

benchmark-integration: web-build
	@INTEGRATION_DATABASE_URL="$(INTEGRATION_DATABASE_URL)" COMPOSE="$(COMPOSE)" \
		DOCKER="$(DOCKER)" GO="$(GO)" \
		$(GO) run ./cmd/testintegration -root . -packages "./internal/integration" \
		-benchmark '^BenchmarkCorePersistenceAndProjection$$' -benchtime "$(BENCHTIME)"

benchmark-vyper-wasm: compiler-install
	$(NODE) compiler/vyper/wasm/packages.mjs
	$(PYTHON) compiler/vyper/wasm/benchmark.py

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


# Deployment checks

.PHONY: deployment-check docker-check docker-image-check compose-check helm-check

deployment-check: docker-check compose-check helm-check

docker-check:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-check: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" $(BUILDX) build --check .

docker-image-check:
	@command -v "$(DOCKER)" >/dev/null 2>&1 || { echo "docker-image-check: docker is required"; exit 1; }
	DOCKER="$(DOCKER)" IMAGE="$(IMAGE)" deploy/check-image.sh

compose-check: preview-network-check
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" NODE="$(NODE)" \
		HARDHAT3_IMAGE="$(HARDHAT3_IMAGE)" FOUNDRY_IMAGE="$(FOUNDRY_IMAGE)" \
		$(NODE) .github/scripts/compose-check.mjs

helm-check:
	@command -v "$(HELM)" >/dev/null 2>&1 || { echo "helm-check: helm is required"; exit 1; }
	$(HELM) lint "$(HELM_CHART)"
	$(HELM) lint "$(HELM_CHART)" -f "$(HELM_CHART)/values-distributed.yaml"
	$(HELM) lint "$(HELM_CHART)" -f "$(HELM_CHART)/values-reference-capacity.yaml"
	$(HELM) template etherview "$(HELM_CHART)" >/dev/null
	$(HELM) template etherview "$(HELM_CHART)" -f "$(HELM_CHART)/values-distributed.yaml" >/dev/null
	$(HELM) template etherview "$(HELM_CHART)" -f "$(HELM_CHART)/values-reference-capacity.yaml" >/dev/null
	HELM="$(HELM)" "$(HELM_CHART)/tests/render.sh" "$(HELM_CHART)"


# Preview lifecycle

.PHONY: preview-cert preview-cert-check preview-network-check preview-env preview-network-runtime \
	preview-images preview-x402-images start-preview stop-preview recreate-preview test-preview-gateway

start-preview recreate-preview stop-preview preview-images preview-network-runtime verify-preview-genesis clean-preview-compiler-cache: export COMPOSE_ENV_FILES = $(PREVIEW_ENV_FILE)

preview-cert:
	@command -v "$(MKCERT)" >/dev/null 2>&1 || { echo "preview-cert: mkcert is required"; exit 1; }
	@mkdir -p "$(PREVIEW_TLS_DIR)"
	$(MKCERT) -install
	$(MKCERT) -ecdsa -cert-file "$(PREVIEW_GATEWAY_CERT)" -key-file "$(PREVIEW_GATEWAY_KEY)" \
		'*.etherview.localhost'
	@set -eu; \
		ca_root="$$("$(MKCERT)" -CAROOT)"; \
		test -r "$$ca_root/rootCA.pem" || { \
			echo "preview-cert: mkcert public root CA is unreadable" >&2; \
			exit 1; \
		}; \
		cp "$$ca_root/rootCA.pem" "$(PREVIEW_TLS_CA)"
	@chmod 600 "$(PREVIEW_GATEWAY_KEY)"
	@chmod 644 "$(PREVIEW_GATEWAY_CERT)" "$(PREVIEW_TLS_CA)"
	@echo "preview-cert: wrote $(PREVIEW_GATEWAY_CERT), $(PREVIEW_GATEWAY_KEY), and public $(PREVIEW_TLS_CA)"

preview-cert-check:
	@test -r "$(PREVIEW_TLS_CA)" && \
		test -r "$(PREVIEW_GATEWAY_CERT)" && test -r "$(PREVIEW_GATEWAY_KEY)" || { \
		echo "Preview TLS certificate or public root CA missing; run 'make preview-cert' first"; \
		exit 1; \
	}

preview-network-check:
	$(NODE) --test preview/scripts/network.test.mjs preview/scripts/env.test.mjs preview/scripts/bundler-start.test.mjs preview/verification/verify.test.mjs

preview-env:
	$(NODE) preview/scripts/env.mjs "$(PREVIEW_ENV_FILE)"

preview-network-runtime: preview-env
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/network.mjs check

preview-x402-images:
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml build x402-facilitator

preview-images: preview-env preview-x402-images preview-bundler-image
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml pull geth lighthouse validator object-storage archive-init ipfs preview-gateway
	$(DOCKER) pull ethpandaops/ethereum-genesis-generator:6.2.7

start-preview: preview-env preview-cert-check docker-build preview-images
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/compiler-cache.mjs init
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/network.mjs prepare
	PREVIEW_GATEWAY_IMAGE="$(PREVIEW_GATEWAY_IMAGE)" ETHERVIEW_IMAGE="$(IMAGE)" DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml \
		up --no-build --wait --wait-timeout 180 --remove-orphans
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml run --rm archive-init
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/ready.mjs
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/verify.mjs
	@printf '\nPreview: https://app.etherview.localhost (HTTPS port %s)\n' "$${PREVIEW_HTTPS_PORT:-443}"

stop-preview:
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml --profile archive-init down --volumes --remove-orphans
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/network.mjs clean

recreate-preview: preview-env preview-cert-check preview-network-runtime docker-build preview-x402-images preview-bundler-image
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/compiler-cache.mjs init
	PREVIEW_GATEWAY_IMAGE="$(PREVIEW_GATEWAY_IMAGE)" ETHERVIEW_IMAGE="$(IMAGE)" DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml \
		rm -fs $(PREVIEW_RUNTIME_SERVICES)
	PREVIEW_GATEWAY_IMAGE="$(PREVIEW_GATEWAY_IMAGE)" ETHERVIEW_IMAGE="$(IMAGE)" DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml \
		up -d --no-build --wait --wait-timeout 180 --remove-orphans $(PREVIEW_RUNTIME_SERVICES)
	DOCKER="$(DOCKER)" $(COMPOSE) -f compose.yaml run --rm archive-init
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/ready.mjs

.PHONY: clean-preview-compiler-cache
clean-preview-compiler-cache: preview-bundler-image
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/compiler-cache.mjs clean


# Local x402 lifecycle

.PHONY: start-x402-local stop-x402-local recreate-x402-local

start-x402-local: docker-build
	@case "$(X402_LOCAL_TOPOLOGY)" in monolith|distributed) ;; *) echo "X402_LOCAL_TOPOLOGY must be monolith or distributed"; exit 1;; esac
	@DOCKER="$(DOCKER)" $(COMPOSE) -f "$(X402_LOCAL_COMPOSE)" build x402-fixture x402-facilitator
	@ETHERVIEW_IMAGE="$(IMAGE)" DOCKER="$(DOCKER)" $(COMPOSE) -f "$(X402_LOCAL_COMPOSE)" \
		--profile "$(X402_LOCAL_TOPOLOGY)" up -d --no-build --wait --wait-timeout 180 --remove-orphans
	@echo "x402 local: explorer=http://localhost:$${X402_LOCAL_PORT:-18080} anvil=http://localhost:$${X402_LOCAL_ANVIL_PORT:-18545} topology=$(X402_LOCAL_TOPOLOGY)"

stop-x402-local:
	@DOCKER="$(DOCKER)" $(COMPOSE) -f "$(X402_LOCAL_COMPOSE)" \
		--profile monolith --profile distributed down --volumes --remove-orphans

recreate-x402-local: stop-x402-local start-x402-local

# Dependency maintenance (network discovery stays outside deterministic check).
.PHONY: dependency-check dependency-update test-dependency-updates install-preview-tools
dependency-check:
	$(NODE) .github/scripts/dependency-updates.mjs check $(if $(DEP),--dependency "$(DEP)")

dependency-update:
	$(NODE) .github/scripts/dependency-updates.mjs update --dependency "$(DEP)" --version "$(VERSION)"

test-dependency-updates:
	$(NODE) --test .github/scripts/dependency-updates.test.mjs

install-preview-tools:
	$(GO) install filippo.io/mkcert@$(MKCERT_VERSION)

.PHONY: preview-bundler-image preview-contracts-generate preview-contracts-check test-preview-contracts test-preview-contracts-prebuilt
preview-bundler-image:
	DOCKER="$(DOCKER)" $(BUILDX) build --load -t etherview-preview-bundler:local -f preview/bundler.Dockerfile .

preview-contracts-generate: pnpm-install
	$(NODE) preview/contracts/ens-upstream.mjs
	$(NODE) preview/contracts/upstream.mjs
	$(NODE) preview/contracts/generate.mjs

preview-contracts-check: pnpm-install
	$(NODE) preview/contracts/generate.mjs --check
	$(NODE) preview/verification/generate.mjs --check
	$(NODE) --test preview/contracts/test.mjs

test-preview-contracts: pnpm-install preview-cert-check docker-build preview-images
	@$(MAKE) --no-print-directory test-preview-contracts-prebuilt

test-preview-contracts-prebuilt: preview-e2e-check
	IMAGE="$(IMAGE)" DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(GO) test -count=1 -v -timeout=20m -tags=previewmetadatae2e -run '^TestPreviewContracts$$' ./e2e/previewmetadata

.PHONY: verify-preview-genesis preview-verification-check
verify-preview-genesis: preview-env
	DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(NODE) preview/scripts/verify.mjs

preview-verification-check: pnpm-install
	$(NODE) preview/verification/generate.mjs --check
	$(NODE) --test preview/verification/verify.test.mjs

# Explicit manual scenarios; never start or reset the daily Preview implicitly.
.PHONY: preview-tool test-preview-tools test-preview-tools-e2e test-preview-tools-e2e-prebuilt
preview-tool:
	@NODE_EXTRA_CA_CERTS="$${NODE_EXTRA_CA_CERTS:-$(CURDIR)/$(PREVIEW_TLS_CA)}" $(NODE) preview/contracts/tools/cli.mjs $(ARGS)

test-preview-tools: pnpm-install
	$(NODE) --test preview/contracts/tools/*.test.mjs
	$(GO) test ./cmd/previewblob

test-preview-tools-e2e: pnpm-install preview-cert-check docker-build preview-images
	@$(MAKE) --no-print-directory test-preview-tools-e2e-prebuilt

test-preview-tools-e2e-prebuilt: preview-e2e-check
	IMAGE="$(IMAGE)" DOCKER="$(DOCKER)" COMPOSE="$(COMPOSE)" $(GO) test -count=1 -v -timeout=60m -tags=previewmetadatae2e -run '^TestPreviewTools$$' ./e2e/previewmetadata

check: test-preview-tools
