# Production schema and runtime acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)
- [Shared production E2E overlay contract](#shared-production-e2e-overlay-contract)
- [Multi-instance observation and publication](#multi-instance-observation-and-publication)
- [Runtime smoke verification fixture (development only)](#runtime-smoke-verification-fixture-development-only)

## Commands

- `make deployment-check`: Docker build checks, Compose profile validation,
  and Helm lint/render checks. The render regression proves x402 secrets are
  absent while disabled, are injected only into `all`/`api` while enabled, and
  require a non-empty facilitator CIDR policy with broad HTTPS egress disabled.
  It also checks release/namespace-scoped x402 process-counter and
  writer-backed stale-settlement alerts.
- `make docker-build docker-image-check`: build the production target for the
  Docker host architecture, run it with the numeric non-root identity and
  hardened runtime flags, validate the exact SEA/compiler runtime manifest,
  recursive ELF closure, and self-test, and scan its exported root filesystem.
  The image contains one read-only Node 26.13.0 SEA, only the automatically
  discovered private libraries missing from the final base rootfs, and the
  read-only Geas v0.3.3 helper, but no general Node executable, wrapper source,
  package metadata, `node_modules`, npm, npx, corepack, shell, Go toolchain,
  native solc, or general Python/Vyper CLI. The read-only shared Python WASM
  directory contains the pinned runtime, crypto wheel and manifest.
  It also validates the non-root-owned mode-0750 compiler cache seed directory
  used when Docker initializes the persistent named volume.
- `make test-schema-e2e`: use Go orchestration to migrate a fresh PostgreSQL 18
  volume with the production image and verify exact compatibility through
  `migrate status`. The domain baseline spans `0001_core` through
  `0018_browsing`, with one checksummed ledger entry per file. Integration
  tests additionally cover repeat/concurrent application, later-file checksum
  and unknown-version rejection, rejection of the previous single-file baseline,
  and isolated-schema function bindings. A conflict in a late file must roll
  back earlier files' DDL and ledger entries, including when the ledger already
  exists; retry succeeds after removing the conflict.
  The baseline source guard checks every file and rejects scattered ALTER
  definitions outside the explicit dependency/catalog-preservation exceptions.
  Baseline refactors also compare PostgreSQL 18 schema-only dumps before and
  after, excluding random
  dump restriction tokens; object names, definitions and relationships must match.
  Earlier development schemas, including the single-file baseline, must be
  recreated; historical upgrade paths are not part of acceptance.
- `make test-runtime-e2e`: rebuild the current working tree's production image
  and run the build-tagged Go E2E suite against the production Compose file in
  monolith and all-seven-application-role layouts. Each layout gets a
  deterministic Prague chain from the default Foundry `v1.9.0` image and a
  fresh PostgreSQL volume. The Go harness derives deterministic temporary
  keys, funds them with `anvil_setBalance`, signs authorization tuples and raw
  type-4 transactions with go-ethereum, and deploys two delegate contracts; it
  never depends on host `cast` or fixture private keys. The suite submits an
  underpriced same-sender/nonce transaction followed by its signed fee-bumped
  replacement, verifies the old hash as `replaced` and the new hash as
  `pending`, then mines and verifies the new hash as included `success`. It
  also verifies contract creation and a failed call, all nine enabled stage
  publications, a distinct competing-hash reorg with orphan/journal retention
  and changed hourly analytics, an orphaned delegation followed by canonical
  delegation, redelegation, ordinary delegated execution, and clearing, plus
  an applied and a signed `nonce_mismatch` tuple in one type-4 transaction.
  After the required Core/Trace/Token publications, the same production API
  verifies the Etherscan V2 advanced transaction filter, current-EOA
  `fundedby`, and exact-block transaction counts and includes them in the
  normalized monolith/split parity snapshot.
  It checks exact authorization signatures/outcomes, transaction-time calldata
  execution identity, cleared EOA history/current binding, hidden skipped and
  orphan authorizations, retained orphan PostgreSQL evidence, API/SSE/SPA
  behavior, an SSE event delivered after an idle period longer than the
  production server's test write timeout, RPC and PostgreSQL outage recovery,
  API process restart, bounded load, and final durable/public parity. The
  initial Prague block additionally executes the exact packed v0.9 `handleOps`
  ABI at a configured, transaction-deployed EntryPoint fixture and requires
  the canonical UserOperation list/detail/transaction/address/search
  projections and decoded revert event in both topologies. Decoder unit vectors
  independently cover the v0.6-v0.9 wire layouts and aggregated operations. The
  test-only ordinary-response write timeout and load request timeout share one
  2-second budget; the SSE client is independently context-bounded and stays
  idle for three times that budget before requiring the durable event.
  The distributed scenario additionally proves config-only identity binding
  and continues after one of two sync and enrichment replicas is stopped. It
  then recreates the production API with a Go-generated, test-only temporary
  TLS Compose override and verifies trusted TLS 1.2+, HTTP/2, and readiness
  without exposing the key to worker roles or depending on the local mkcert
  trust store.
  A bounded test-only Go RPC adapter removes the orphan `blobGasPrice` field
  emitted by Anvil when `blobGasUsed` is absent. Anvil
  can also omit geth-style prestate fields for cleared delegation code,
  implicit delegated authority code, and the executed delegate account. The
  adapter normalizes only the explicit clearing post-state gap; it deliberately
  does not add authority or delegate code to transaction prestate. The runtime
  therefore exercises `abi@5` recovery from the exact root Trace and prior
  canonical code observations. Both monolith and the seven-role topology assert that the
  adapter observed `debug_traceBlockByHash` and no `debug_traceTransaction`
  calls. Complete provider observations pass through unchanged, so production
  receipt and trace validation remain strict. The runtime gate does not require
  an orphan blob fee field to occur; synthetic receipt regressions cover both
  normalization and unchanged complete observations.
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
  Preview metadata has a [separate acceptance gate](../../preview/docs/testing.md#metadata-gate).

## Shared production E2E overlay contract

`e2e/runtime/compose.yaml` is shared infrastructure, not a fixture owned only
by `make test-runtime-e2e`. The general runtime, Hardhat, and Foundry harnesses
all layer it over the production Compose file. A feature change in its shared
environment therefore has three consumers with deliberately different feature
sets.

Follow all of these rules whenever an optional feature or its required
configuration enters that shared overlay:

1. The Compose interpolation defaults the feature to `false`. Never hardcode a
   new optional feature to `true` in the shared environment anchor.
2. Every Go harness passes the intended feature state explicitly through
   `runtimeEnvironment`. Only the harness that owns the fixture opts in. If the
   feature requires addresses, credentials, endpoints, or another companion
   value, that harness must populate the complete valid configuration before
   starting any application service.
3. Shared readiness and parity helpers derive expected stages and components
   from the effective feature set. A globally hardcoded stage count is invalid:
   the always-on Holder and native-transfer stages make the UserOperation
   runtime publish nine stages, while feature-off Hardhat and Foundry
   verification topologies publish eight.
4. Extend the shared verification assertions in `.github/scripts/compose-check.mjs`
   to assert the feature-off value
   and absence or empty value of its companion configuration on every
   application service. A successful render without these assertions is not
   isolation evidence.
5. Run all affected consumers. Passing the general runtime suite alone is
   insufficient because it cannot prove that feature-off verification
   topologies still start.

The required local sequence for a shared-overlay feature change is:

```sh
make compose-check
make test-runtime-e2e
make test-hardhat3-e2e
make test-foundry-e2e
```

`make test-schema-e2e-prebuilt` requires the loaded production image.
`make test-preview-metadata-prebuilt` and `make test-preview-consensus-prebuilt`
require the loaded production/gateway images and readable Preview certificates.
They check these prerequisites without building images or creating certificates.
After loading the production image, prepare external Preview images once with
`make preview-images-prebuilt`; it fails before preparation if that image is
missing. `make preview-images` builds the production image first. The ordinary
schema and Preview targets continue to build/prepare before invoking the same
prebuilt test recipes. Preview network generation and archive initialization
remain owned by each isolated test harness.

The `*-prebuilt` variants are CI-equivalent only after the exact working tree's
production and client images have been built and inspected. Do not use an
older loaded image as evidence for a harness or Compose change.

Use these failure signatures to route diagnosis before changing product
correctness checks:

| Failure signal                                                                               | Meaning and required response                                                                                                                                                                                                                |
| -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `features.<name> requires ...` while a verification service repeatedly restarts              | A shared overlay enabled a feature without its companion configuration. Inspect the fully rendered environment for every application service; restore default-off isolation and explicit owner opt-in.                                       |
| Canonical wait reports eight complete stages while the harness requires nine, or the reverse | The topology started, but a shared assertion encoded one feature set globally. Derive the expected count from the harness's explicit effective feature state.                                                                                |
| `panic: test timed out after 10m0s` after migrations succeeded                               | The managed integration runner did not apply its explicit package timeout. Confirm the emitted command contains `-timeout=15m0s`; do not drop packages, tests, tags, or concurrency to make CI green.                                        |
| `compiler_unavailable` after the bounded external download deadline                          | This is an external compiler-artifact boundary, not proof of a Compose feature regression. Retain diagnostics and rerun the unchanged exact target once; repeated failure requires source/network diagnosis rather than weaker verification. |

For managed PostgreSQL timeout diagnosis, see
[integration invocation](integration.md#runner-timeouts).

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
Go-owned disposable Compose teardown includes all project resources so named
volumes created by explicitly targeted services in inactive profiles cannot
survive `down --volumes`.

## Multi-instance observation and publication

P30-T154 requires real PostgreSQL regressions for live sync, maintenance repair,
mempool polling, NFT source discovery/admission and compiler catalog refresh.
Hold one replica in external work, verify contenders skip, terminate only its
owned database session, let another replica publish, then release the stale
response and assert it cannot write. Retain genuine shorter-head and backfill
conflict recovery cases. Session cleanup covers success, failure, cancellation,
panic and absence of an open transaction during external work. Concurrent
identical NFT enqueue must return one durable job without SQLSTATE 40001.
Production runtime also runs `TestCompilerCacheReplicasColdStart`: three fresh
named volumes each receive eight simultaneous container creations, covering
Docker copy-up before application or PostgreSQL coordination can run.

Run the `TestLiveSyncReplicas`, `TestMaintenanceReplicas`, `TestMempoolReplicas`,
`TestMetadataReplicas`, `TestRuntimeSessionLock` and `TestCompilerCatalogReplicas`
cases through the managed PostgreSQL runner with `-race`, then common,
generation, Go lint, documentation/plan, full integration race and production
runtime E2E gates. Preview metadata covers source discovery, URI changes and
restart persistence; Hardhat covers catalogs and verification in both role
layouts. These checks do not close P70 capacity or external release gates.

## Runtime smoke verification fixture (development only)

The runtime parity smoke target defaults `ETHERVIEW_RUNTIME_FIXTURE_IMAGE` to
Foundry `v1.9.0` and starts anvil on the Prague hard fork as its deterministic
chain fixture source. Its Go harness generates temporary EIP-7702 keys and raw
transactions, funds them through `anvil_setBalance`, and does not require host
`cast` or reusable private keys. Do not rely on hardcoded chain, block, or
transaction hashes in manual checks; capture identity from
`eth_getBlockByNumber('0x0')` and transaction submission results at runtime and
reuse the captured values.
Set `ANVIL_ARGS` only for local launch tuning (for example alternate anvil
defaults) and keep these test-only overrides out of production runbooks.

## Database deadlock acceptance

Both production layouts use the same `internal/testdbguard` database-wide
zero-new-deadlocks gate as managed integration. The observer starts before
migration/application startup. Cleanup stops application writers and closes
harness business connections, checks statistics before removing PostgreSQL,
and makes incomplete or failed checks fail acceptance. Intentional temporary
PostgreSQL pauses may interrupt samples; the observer reconnects and still
requires unchanged database/statistics identity and complete final evidence.

The runtime scenario verifies an exact blocked replay transaction while the
fixture mines the next block and deployed workers are active. Existing reorg,
restart and outage phases then verify convergence and all/split parity.
`concurrent-replay.json` records the verified overlap and `deadlocks.json`
records bounded waits and final database evidence in existing runtime failure
artifacts. This bounded CI scenario is separate from the P70 reference soak.
