# Testing and Verification

The Makefile is the command source of truth. Only implemented targets are
listed as runnable commands here; future gates remain in their owning plan
until the Makefile target exists.

## Common Gates

- `make toolchain-check`: require at least Go 1.26.5, Node 24.18.0, and npm
  11.16.0 before generating or validating artifacts. Compatible newer stable
  versions are supported; older, malformed, and prerelease versions fail.
- `make plan-check`: validate plan links, IDs, statuses, dependencies, evidence,
  and parent/child state.
- `make generate-check`: regenerate OpenAPI, SQL, and embedded frontend outputs
  and fail on a diff. It snapshots the checked-in baseline in a temporary
  directory before regeneration, so it also works before the repository has an
  initial Git `HEAD`.
- `make test`: Go and frontend unit tests.
- `make test-race`: Go tests with the race detector.
- `make test-e2e`: build the embedded SPA and a temporary Go E2E binary, then
  run Playwright against that embedded distribution. Local runs use installed
  Chrome; CI sets `PLAYWRIGHT_USE_BUNDLED=1` after installing Playwright
  Chromium. On a restricted macOS automation host that denies Chromium's Mach
  bootstrap rendezvous, use `PLAYWRIGHT_USE_BUNDLED=1
  PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e`; this opt-in fallback gives every
  test an isolated worker because a single-process browser cannot safely reuse
  test contexts. CI remains on the ordinary multi-process browser.
- `make test-integration`: build the embedded SPA, then run real migrations and
  every integration-tagged Go test. When `INTEGRATION_DATABASE_URL` is empty,
  the Go runner owns a fresh PostgreSQL 18 Compose project and removes its
  volume afterward. Supplying the variable uses that explicitly disposable
  external database instead.
- `make test-integration-race`: run the same owned database lifecycle with the
  Go race detector. This expensive variant is explicit and is not part of
  default CI.
- `make test-hardhat3-verify`: install the exact dependency-locked Hardhat 3
  verification fixture and run its Etherscan-provider flow against a Go-owned
  real compatibility handler with in-memory authentication and a stateful fake
  backend. It proves GET source lookup, POST Standard JSON submission, and GET
  pending/success status polling without a chain, database, compiler, Hardhat
  2, Blockscout, or Sourcify.
- `make lint`: Go formatting/vet, `golangci-lint`, and TypeScript type checking.
- `make security-check`: `govulncheck`, API-generator, frontend, and Hardhat 3
  fixture dependency audits, secret scan, and security-focused tests. All
  three npm dependency trees must report zero high-severity vulnerabilities;
  the API generator's
  transitive parser and glob dependencies are constrained by audited
  overrides.
- `make license-check`: Go and production frontend dependency license policy.
- `make deployment-check`: Docker build checks, Compose profile validation,
  and Helm lint/render checks. The render regression proves x402 secrets are
  absent while disabled, are injected only into `all`/`api` while enabled, and
  require a non-empty facilitator CIDR policy with broad HTTPS egress disabled.
  It also checks release/namespace-scoped x402 process-counter and
  writer-backed stale-settlement alerts.
- `make docker-build docker-image-check`: build the production target, run it
  with the numeric non-root identity and hardened runtime flags, and scan its
  exported root filesystem for Node, package-manager, shell, Go, and
  Solidity/Vyper compiler payloads.
- `make test-schema-e2e`: use Go orchestration to migrate a fresh PostgreSQL 18
  volume with the production image and verify exact compatibility through
  `migrate status`.
- `make test-runtime-e2e`: rebuild the current working tree's production image
  and run the build-tagged Go E2E suite against the production Compose file in
  monolith and all-seven-role layouts. Each layout gets a deterministic Anvil
  chain and a fresh PostgreSQL volume. The suite verifies an exact pending
  hash, contract creation and a failed call, all six deployed stage
  publications, a distinct competing-hash reorg with orphan/journal retention
  and changed hourly analytics, API/SSE/SPA behavior, RPC and PostgreSQL outage
  recovery, API process restart, bounded load, and final durable/public parity.
  The distributed scenario additionally proves config-only identity binding
  and continues after one of two sync and enrichment replicas is stopped. It
  then recreates the production API with a Go-generated, test-only temporary
  TLS Compose override and verifies trusted TLS 1.2+, HTTP/2, and readiness
  without exposing the key to worker roles or depending on the local mkcert
  trust store.
  A bounded test-only Go RPC adapter removes the orphan `blobGasPrice` field
  emitted by the pinned Anvil fixture when `blobGasUsed` is absent; complete
  blob-fee observations pass through unchanged, so production receipt
  validation remains strict.
  Successful Compose lifecycle output is captured rather than streamed, so the
  terminal shows only the current mode and phase. A failure prints the exact Go
  assertion followed by one bounded summary containing the mode, phase,
  diagnostic directory, and `compose ps` state. Each retained mode directory
  contains `failure-summary.txt`, `compose-ps.txt`, the complete timestamped
  `compose.log`, and any API, durable-state, or load-report JSON produced before
  the failure. Failure artifacts are retained automatically; set
  `RUNTIME_E2E_KEEP_ARTIFACTS=true` to retain the same bundle for successful
  modes too. CI uses the already loaded production image through the internal
  `make test-runtime-e2e-prebuilt` target and uploads a failed run as
  `runtime-e2e-diagnostics-<run-id>-<attempt>` for seven days. Developers
  should continue to use `make test-runtime-e2e`, which rebuilds the current
  working tree first.
  Verification, Sourcify, and pricing stay disabled because they require
  separately approved compiler or external-service boundaries.
- `make test-load`: run the bounded public-API driver. Defaults are a 100 RPS,
  30-second smoke with p95, error-rate, throughput, and final core-lag
  thresholds. Set the typed `ETHERVIEW_LOAD_*` environment inputs, encode the
  route mix as a JSON string array in `ETHERVIEW_LOAD_PATHS`, and describe the
  revision, dataset, hardware, and RPC model.
- `make test-soak`: run the same driver at the P70 reference defaults of
  500 RPS for 30 minutes. The target fixes a 5-second request timeout, p95
  below 500 ms, error rate below 0.1%, final lag no greater than two blocks,
  and successful throughput of at least 99% of target (495 RPS). The 1%
  throughput allowance covers only the bounded final in-flight drain and
  scheduler jitter; dropped admissions and failed responses remain errors, and
  the former 475 RPS floor cannot pass. This is an executable harness, not
  release evidence by itself; P70-T04 still requires the named reference
  deployment, dataset, hardware, RPC behavior, and independently captured
  resource peaks.
- `make check`: source, unit/race, security, license, generation, and deployment
  gates. Browser, integration, parity, load, and soak suites are explicit
  opt-in targets because they require dedicated services or runtimes; CI runs
  the browser, managed integration, schema, and runtime E2E suites, not the
  external 30-minute soak.

All Compose-facing targets use `.github/scripts/compose.sh`, and image builds
use `.github/scripts/buildx.sh`. They prefer Docker's Compose and Buildx
plugins and fall back to the standalone `docker-compose` and `docker-buildx`
binaries. Set `COMPOSE`, `BUILDX`, or `DOCKER` to override those entry points.

Keep lifecycle, readiness polling, API/RPC assertions, SQL state capture,
normalization, parity comparison, and diagnostic artifacts in Go. Repository
shell at this boundary is intentionally limited to the small Compose/Buildx
selectors and the production-image payload inspection script. Extend
`internal/testcompose`, the managed integration runner, or the build-tagged
runtime suite instead of introducing a new shell smoke harness.

For example:

```sh
mkdir -p artifacts
ETHERVIEW_LOAD_BASE_URL=https://explorer.example.invalid \
ETHERVIEW_LOAD_REVISION=0123456789abcdef \
ETHERVIEW_LOAD_DATASET=mainnet-snapshot-2026-07-23 \
ETHERVIEW_LOAD_HARDWARE=kubernetes-reference-profile \
ETHERVIEW_LOAD_RPC_BEHAVIOR=isolated-head-history-state \
ETHERVIEW_LOAD_PATHS='["/api/v1/status","/api/v1/blocks?limit=20&sort=desc"]' \
make test-load >artifacts/load.json
```

Use `ETHERVIEW_LOAD_API_KEY_FILE` or the process environment
`ETHERVIEW_LOAD_API_KEY` for an authenticated profile. Never place a key in a
URL, route argument, report metadata, or command-line value.

## Real x402 Base Sepolia gate

`make test-x402-testnet` is an explicit, one-shot real-payment gate. It is not
a dependency of `check`, CI, `test`, or `test-integration`. Running it spends
the configured Base Sepolia token and must never be used as an ordinary
retrying test. The harness is fixed to payment network `eip155:84532` and
ledger/RPC chain ID `84532`; there is no mainnet switch or implicit asset
default.

Every non-secret expectation is required and validated before any Secret file
is opened:

- `ETHERVIEW_X402_TESTNET_CONFIRM` must equal
  `BASE_SEPOLIA_REAL_PAYMENT`.
- `ETHERVIEW_X402_TESTNET_REVISION` is the full 40-character lowercase Git
  revision of the local harness checkout. The running Go binary must carry that
  exact clean Git VCS revision; this proves harness provenance, not the
  revision deployed at the target URL.
- `ETHERVIEW_X402_TESTNET_TARGET_URL` is the exact HTTPS priced GET to call.
- `ETHERVIEW_X402_TESTNET_EXPECTED_RESOURCE_URL` is the exact canonical HTTPS
  resource expected in the x402 requirement, including sorted query defaults.
- `ETHERVIEW_X402_TESTNET_EXPECTED_OPERATION` is one billing-eligible operation
  ID whose catalog ServeMux pattern matches both URLs.
- `ETHERVIEW_X402_TESTNET_EXPECTED_ACCESS` is exactly `x402` or
  `api_key_or_x402`. The harness supplies neither an API key nor a Cookie.
- `ETHERVIEW_X402_TESTNET_EXPECTED_ASSET`,
  `ETHERVIEW_X402_TESTNET_EXPECTED_RECIPIENT`, and
  `ETHERVIEW_X402_TESTNET_EXPECTED_PAYER` are exact non-zero EIP-55 addresses.
- `ETHERVIEW_X402_TESTNET_EXPECTED_ASSET_DECIMALS`,
  `ETHERVIEW_X402_TESTNET_EXPECTED_ASSET_EIP712_NAME`, and
  `ETHERVIEW_X402_TESTNET_EXPECTED_ASSET_EIP712_VERSION` bind the explicit
  asset domain returned by `/api/v1/billing/config`.
- `ETHERVIEW_X402_TESTNET_EXPECTED_AMOUNT_ATOMIC` is a canonical positive
  uint256 decimal string.
- `ETHERVIEW_X402_TESTNET_EXPECTED_MAX_TIMEOUT_SECONDS` is a canonical decimal
  integer from 1 through 60.
- `ETHERVIEW_X402_TESTNET_LEDGER_CHAIN_ID` must explicitly equal `84532`.

The three credential inputs are file-only:

- `ETHERVIEW_X402_TESTNET_PRIVATE_KEY_FILE`
- `ETHERVIEW_X402_TESTNET_RPC_URL_FILE`
- `ETHERVIEW_X402_TESTNET_WRITER_DATABASE_URL_FILE`

Use absolute paths to ordinary, non-symlink files with mode `0600`. Generate a
fresh funded test key through trusted wallet tooling; its file contains exactly
64 lowercase hexadecimal characters without `0x`, optionally followed by one
newline. The payer expectation must match that key. The RPC file contains one
HTTPS Base Sepolia endpoint and the writer file contains one `postgres` or
`postgresql` URL. Plaintext `..._PRIVATE_KEY`, `..._RPC_URL`, and
`..._WRITER_DATABASE_URL` environment variables are rejected.

After configuring the staging route and independently checking the price and
wallet balance, export the public expectations and Secret file paths, then run
exactly once:

```sh
make test-x402-testnet
```

Success prints one bounded JSON report containing the reviewed SDK version,
`harness_revision`, operation, public payment facts, ledger payment ID,
transaction hash, protected-response digest, and receipt block evidence. It
never prints a URL, credential, authorization, facilitator body, protected
response, or database error. Record the independently verified staging
image/build digest beside this report; the harness does not infer remote
deployment provenance from its own Git revision. A failure prints only a
stable code. Never rerun automatically. In particular,
`x402_testnet_paid_outcome_unknown` means authorization may have reached the
server, while `x402_testnet_paid_reconciliation_incomplete` means the
facilitator confirmed payment but the evidence pass did not finish. Do not
rerun either target; inspect the ledger, facilitator, and Base Sepolia
transaction and use the manual reconciliation procedure in the operations
runbook.

## Evidence Rules

- Record the exact target/command and a concise pass/fail summary in the child
  plan. Do not paste full logs.
- A targeted test is required for every fixed regression.
- Integration tests that require optional local services must be reproducible
  through documented Compose profiles.
- Load and soak evidence records the revision, dataset, hardware, RPC behavior,
  duration, throughput, latency, error rate, and index lag.
