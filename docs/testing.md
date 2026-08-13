# Testing and Verification

The Makefile is the command source of truth. Only implemented targets are
listed as runnable commands here; future gates remain in their owning plan
until the Makefile target exists.

## Restricted automation hosts

Codex and other filesystem-restricted automation should preserve the exact
Makefile target and change only host execution plumbing. A writable cache
prefix avoids repeated failures from npm, the Go build cache, and
golangci-lint trying to update user-owned directories:

```sh
env \
  GOCACHE=/tmp/etherview-codex-go-build \
  GOLANGCI_LINT_CACHE=/tmp/etherview-codex-golangci-lint \
  npm_config_cache=/tmp/etherview-codex-npm \
  make check
```

Use the same prefix with focused `go`, npm, or Makefile commands as needed.
Do not set `GOMODCACHE` merely because Go cannot update its optional module
download stat cache: retaining read access to the warmed shared module cache is
faster and supports offline work. Relocate it only when a command proves that
the module contents themselves must be written, and expect that a fresh module
cache may require approved network access.

On a Codex/macOS host already known to restrict browser process bootstrap and
Docker's user state, request sandbox-external execution up front for `make
test-e2e`, `make deployment-check`, and `make check`; the last target includes
the deployment gate. Pass the writable cache prefix to that approved command.
Pure frontend, Go, generation, plan, security, and license targets should stay
inside the sandbox unless their own output proves that broader access is
required.

Use this failure matrix instead of repeatedly trying unrelated workarounds:

| Failure signal | Required response |
| --- | --- |
| npm cache `EPERM`, Go build-cache `operation not permitted`, or golangci-lint cache warnings | Retry the same command with the writable cache prefix above. |
| Chromium `MachPortRendezvousServer`, `bootstrap_check_in`, `SIGABRT`, or `kill EPERM` on macOS | Request sandbox-external execution and rerun the exact browser target. If approval is unavailable, the bundled single-process fallback below is diagnostic evidence, not an automatic substitute for the canonical gate. |
| Docker socket/config denial or Buildx activity/config write failure | Request sandbox-external execution and rerun the exact Docker-backed target. Keep `.github/scripts/compose.sh` and `.github/scripts/buildx.sh`; do not point Docker at an empty temporary configuration or bypass repository wrappers. |
| Browser launches but the app is blank, the root is missing, or locators fail across unrelated routes | Inspect the first browser console error and the built asset graph. This is a runtime/build regression until evidence proves otherwise. |
| Fixed E2E port is already in use | Stop the temporary diagnostic server, confirm the port is free, and rerun the repository target so its own lifecycle remains authoritative. |

An approved sandbox-external run is still a local test run. Record the exact
canonical target and result in plan evidence; mention the host permission
boundary only when it materially affected execution. Read-only inspection in
the in-app browser is useful for diagnosing a built SPA, but it does not
replace a required `make test-e2e` pass.

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
  Chromium. The embedded Go API fixture includes independent EIP-7702
  delegation and clearing transactions so the suite proves lazy Authorization
  loading, deep links and cursor pages, applied/skipped tuples with raw
  signatures, transaction-time delegate calldata identity, clearing without
  stale fallback, bilingual keyboard accessibility, and 390px overflow safety.
  On a restricted macOS automation host that denies Chromium's Mach
  bootstrap rendezvous, first rerun the unchanged target outside the process
  sandbox with approval. If that is unavailable, use
  `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` only as
  the documented diagnostic fallback; it gives every test an isolated worker
  because a single-process browser cannot safely reuse test contexts. A
  launched browser with blank pages still requires console/runtime diagnosis.
  CI remains on the ordinary multi-process browser.
- `make test-integration`: build the embedded SPA, then run real migrations and
  every integration-tagged Go test. When `INTEGRATION_DATABASE_URL` is empty,
  the Go runner owns a fresh PostgreSQL 18 Compose project and removes its
  volume afterward. Supplying the variable uses that explicitly disposable
  external database instead.
- `make test-integration-race`: run the same owned database lifecycle with the
  Go race detector. This expensive variant is explicit and is not part of
  default CI.
- `make test-hardhat3-provider-compat`: install the exact dependency-locked
  Hardhat 3 fixture and run its Etherscan-provider flow against a Go-owned real
  compatibility handler with in-memory authentication and a stateful fake
  backend. This is the fast provider-wire regression only; it has no chain,
  database, compiler, or production process.
- `make test-hardhat3-e2e`: rebuild the host-native production application and
  independent Hardhat client images, then run the real Hardhat CLI against independent
  Anvil/PostgreSQL datasets in monolith and complete six-application-role
  layouts. It compiles and deploys an implementation plus EIP-1967 proxy,
  verifies both sources through the public compatibility API and official
  compiler catalog, polls durable proxy verification, upgrades and rebinds the
  proxy, and checks normalized public/persistent parity. The `all` or `api`
  process consumes the durable job, resolves and checksum-validates the
  architecture-independent `emscripten-wasm32` artifact, and runs each bounded
  Standard JSON compilation in a fresh permission-restricted Node subprocess.
  The first real compilation populates the Compose named cache volume; the
  harness force-recreates the compiler-owning service, submits another job for
  the same version, and proves the identical artifact survives without a new
  installation.
  No application or test client receives a Docker socket or CLI. The test
  fails when the official catalog or compiler cannot be downloaded. CI builds
  and exercises the native production image independently on AMD64 and ARM64;
  no deployment or test field fixes a container platform. CI uses
  `make test-hardhat3-e2e-prebuilt` after loading both images. Redacted
  Hardhat output, Compose state/logs, and proxy summaries are retained on
  failure. The E2E-only API environment permits Docker fake-IP download
  networks while retaining the exact HTTPS origin, TLS, size, and SHA-256
  checks; the base deployment never receives that escape hatch.
- `make test-hardhat3-offline-compile`: run the dependency-locked Hardhat
  client image with no network and compile its production fixture through the
  image's exact `solc@0.8.30` solc-js path. The production verification E2E
  runs this preflight automatically. Product verification still downloads and
  checksum-validates the official catalog artifact through the application;
  only the independent client's fixture compilation is offline.
- `make test-foundry-offline-compile`: run the manifest-digest-pinned Foundry
  v1.7.1 client image with no network and force a Solidity 0.8.30 rebuild. The
  image build already compiles once online and once offline, while this target
  independently proves the loaded client contains the complete compiler cache.
- `make test-foundry-e2e`: rebuild the host-native production application and
  independent Foundry client images, run the offline compiler preflight, and
  exercise source verification against fresh Anvil/PostgreSQL datasets in the
  monolith and complete six-application-role layouts. These are two separate,
  mutually exclusive Compose-profile runs, not two API services in one run:
  `monolith` starts `etherview` with `roles=all`, while `distributed` starts
  `api` plus the split worker services. The Foundry Compose override therefore
  names both base services so Compose can merge the same verification settings
  into the service selected by the active profile; the client URL selects
  `etherview:8080` for monolith and `api:8080` for distributed. Collapsing the
  two service entries would either drop one production topology or stop the
  test from matching the production service graph. Forge deploys a contract
  with a constructor and immutable fields, checks the initial unverified ABI,
  submits Standard JSON through the exact `/v2/api?chainid=1` custom-verifier
  URL, watches status by POST, and confirms the public source and ABI. A second
  Forge invocation must short-circuit as already verified without creating a
  job. Normalized snapshots require one successful job, result, and
  publication; exact constructor arguments and immutable references; the
  official Solidity 0.8.30 compiler URL and SHA-256; and
  `emscripten-wasm32`, `node_solcjs_v1`, `trusted_subprocess`, and executor
  digest parity. `VERIFIER_API_KEY` is the client's only secret and is passed
  by environment name, never a command argument, URL, Compose value, mount, or
  artifact. The Go harness redacts configured secrets from command and failure
  diagnostics. CI uses `make test-foundry-e2e-prebuilt` after loading both
  images on native AMD64 and ARM64, stays quiet on success, and uploads failed
  `etherview-foundry-e2e-*` bundles for seven days. The existing Hardhat 3
  source/proxy/upgrade gate remains independent and unchanged.
  The same production-topology suite also deploys the pinned ethereum/sys-asm
  EIP-7002 creation bytecode, submits its four-file Geas bundle through the
  native address-verification endpoint, and requires exact runtime/creation
  matches, no catalog generation, `go-module`, `etherview_geas_v1`, empty ABI,
  and monolith/six-role publication parity.
- `make lint`: Go formatting/vet, the repository's golangci-lint v2 policy
  (`standard`, `modernize`, and `unparam`) across ordinary tests plus tagged
  integration, Hardhat, Foundry, and runtime E2E source, and TypeScript type
  checking.
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
- `make docker-build docker-image-check`: build the production target for the
  Docker host architecture, run it with the numeric non-root identity and
  hardened runtime flags, validate the exact Node/compiler runtime manifest
  and self-test, and scan its exported root filesystem. The image contains the
  pinned Node executable, read-only solc-js wrapper/dependency tree, and the
  read-only Geas v0.3.3 helper, but no
  npm, npx, corepack, shell, Go toolchain, native solc, or Vyper payload.
  It also validates the non-root-owned mode-0750 compiler cache seed directory
  used when Docker initializes the persistent named volume.
- `make test-schema-e2e`: use Go orchestration to migrate a fresh PostgreSQL 18
  volume with the production image and verify exact compatibility through
  `migrate status`.
- `make test-runtime-e2e`: rebuild the current working tree's production image
  and run the build-tagged Go E2E suite against the production Compose file in
  monolith and all-six-application-role layouts. Each layout gets a
  deterministic Prague chain from the default Foundry `v1.7.1` image and a
  fresh PostgreSQL volume. The Go harness derives deterministic temporary
  keys, funds them with `anvil_setBalance`, signs authorization tuples and raw
  type-4 transactions with go-ethereum, and deploys two delegate contracts; it
  never depends on host `cast` or fixture private keys. The suite submits an
  underpriced same-sender/nonce transaction followed by its signed fee-bumped
  replacement, verifies the old hash as `replaced` and the new hash as
  `pending`, then mines and verifies the new hash as included `success`. It
  also verifies contract creation and a failed call, all six deployed stage
  publications, a distinct competing-hash reorg with orphan/journal retention
  and changed hourly analytics, an orphaned delegation followed by canonical
  delegation, redelegation, ordinary delegated execution, and clearing, plus
  an applied and a signed `nonce_mismatch` tuple in one type-4 transaction.
  It checks exact authorization signatures/outcomes, transaction-time calldata
  execution identity, cleared EOA history/current binding, hidden skipped and
  orphan authorizations, retained orphan PostgreSQL evidence, API/SSE/SPA
  behavior, RPC and PostgreSQL outage recovery, API process restart, bounded
  load, and final durable/public parity.
  The distributed scenario additionally proves config-only identity binding
  and continues after one of two sync and enrichment replicas is stopped. It
  then recreates the production API with a Go-generated, test-only temporary
  TLS Compose override and verifies trusted TLS 1.2+, HTTP/2, and readiness
  without exposing the key to worker roles or depending on the local mkcert
  trust store.
  A bounded test-only Go RPC adapter removes the orphan `blobGasPrice` field
  emitted by the pinned Anvil fixture when `blobGasUsed` is absent. Anvil
  `v1.7.1` also omits geth-style prestate fields for cleared delegation code,
  implicit delegated authority code, and the executed delegate account; the
  adapter restores only the deterministic fixture evidence after verifying the
  deployed runtime bytecode and exact delegated-call transaction hash.
  Complete provider observations pass through unchanged, so production receipt
  and trace validation remain strict.
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
