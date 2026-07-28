# P70 — Release

Status: `in_progress`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0019: Authenticated genesis state import](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0020: SIWE user sessions](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0021: x402 request billing](../decisions/ADR-0021-x402-request-billing.md)
- [ADR-0022: Go-ethereum type and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P70-T01 | todo | P10–P66 | Execution/API/token/proxy/verification/authentication/billing conformance matrix | conformance suite |
| P70-T02 | todo | P10–P66 | Threat model, security audit, dependency, compiler, session, and payment supply-chain review | security gates |
| P70-T03 | todo | P10–P66 | Monolith/split E2E, migration/rollback, outage, reorg, payment, and soak suite | release CI |
| P70-T04 | in_progress | P60 | 500 RPS reference capacity report and tuning guide | load report |
| P70-T05 | todo | P00–P66 | User/operator/API/authentication/billing/runbook/upgrade documentation | doc review and link check |
| P70-T06 | todo | P70-T01–P70-T05, P70-T08, P70-T09 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release | release verification |
| P70-T07 | done | P60 | Database read/write pool split configuration, deployment wiring, and capacity guidance | helm config/schema tests |
| P70-T08 | done | P10, P20, P30-T07, P40, P50, P60 | Authenticated local/remote genesis account state, predeploy enrichment, native API, and block-zero UI | root, persistence, API, browser, security, and split-role tests |
| P70-T09 | done | P10, P20, P30-T07, P40, P50, P60 | Replace duplicative Ethereum RPC/domain types and codecs with reviewed go-ethereum equivalents while retaining explicit hostile-input, persistence, and public-contract adapters | focused compatibility, integration, generation, security, license, and common gates |
| P70-T10 | done | P60 | Configurable process log level and JSON/text output across file, environment, CLI, and deployment surfaces | config, CLI, observability, Compose, and Helm tests |
| P70-T11 | done | P50 | Keep embedded-browser native-value assertions aligned with configured decimal display | focused Playwright E2E and common frontend gates |
| P70-T12 | done | P20, P60 | Align durable stage-name validation with the deployed `state_diff@1` manifest | focused stage validation and Compose runtime smoke |
| P70-T13 | done | P50, P60 | Split the full-stack Preview Compose deployment into all seven runtime roles | Compose render assertions and Preview runtime smoke |
| P70-T14 | done | P10, P60 | Add reporter-fenced rate-limited sync progress and durable worker outcome logs | focused logging, race, deployment, and Preview tests |
| P70-T15 | in_progress | P30-T02, P60, P70-T13 | Enable Preview public verification and NFT metadata with a digest-pinned isolated compiler runtime | Compose render, compiler preflight, image-boundary, and Preview runtime tests |

## Acceptance

- [ ] Every P00–P66 plan and root release gate is complete with evidence.
- [ ] Clean deployment, upgrade, rollback, backup/restore, and repair procedures
      are independently reproducible.
- [ ] Security findings have no unresolved critical/high issue.
- [ ] Reference capacity target passes with documented hardware and dataset.
- [ ] Published artifacts are reproducible, checksummed, signed, and accompanied
      by an SBOM.
- [x] P70-T10: every configuration-bearing command applies exact lowercase
      `debug|info|warn|error` and `json|text` logging settings with CLI,
      environment, file, and default precedence.
- [x] P70-T10: JSON and text handlers retain the same bounded fields,
      redaction, trace correlation, and stable HTTP internal-error boundary.
- [x] P70-T07: only API-bearing processes open the optional read-only pool;
      startup validates its schema and chain identity, readiness covers both
      pools without automatic fallback, and every correctness-sensitive read or
      write remains writer-routed.
- [x] P70-T07: configuration, Compose, Helm Secret/ExternalSecret wiring,
      effective connection bounds, and API-only capacity accounting have
      regression coverage and pass the applicable repository gates.
- [x] P70-T13: the full-stack Preview runs the production union of all seven
      split roles, exposes public and metrics ports only from the API role, and
      keeps the API session pepper out of migration and worker containers.
- [x] P70-T14: only the active PostgreSQL sync-status reporter emits changed
      sync progress, immediately for its first valid state and then no more than
      once per bounded configured interval, without idle heartbeats or delaying
      failure and safety-boundary logs.
- [x] P70-T14: enrichment, trace, verification, metadata, and maintenance
      outcomes emit bounded event-driven logs only when their worker observers
      report a completed durable transition; API request logging remains the
      existing per-request boundary and catalog success is logged only after an
      executed sweep.
- [x] P70-T08: an optional bounded Genesis JSON source is authenticated against
      block zero and exposes exact EOA/predeploy account facts through
      PostgreSQL, proxy/ABI enrichment, native API, and the embedded block-zero
      UI; missing input remains explicitly unavailable.
- [x] P70-T09: reviewed go-ethereum types own recognized protocol semantics,
      including transaction types 0 through 4, while unsupported future
      transaction types fail permanently and atomically before persistence or
      coverage advancement; `blocks.raw` keeps the original block top-level
      shape, versioned root metadata carries validated PoW uncle headers and is
      stripped by `DecodeStoredBlock`, legacy empty-uncle rows remain readable,
      and legacy non-empty uncle hashes without headers fail permanently
      pending exact RPC-backed repair. Receipt fields outside the trie,
      including gas-use deltas, creation address, and effective gas price, are
      authenticated against the matching transaction and block context.
      Transport limits, redaction, supported-object unknown-field retention,
      SQL persistence, and public contracts remain explicit compatible
      adapters.

## Current Blockers

P00 through P65 are complete, while P66 remains `in_progress`: P66-T08 is
blocked on operator-provided Base Sepolia funding, payer credentials, a
compatible staging facilitator and priced route, the matching writer and
independent RPC endpoint, and the deployed image/build digest needed for live
settlement and ledger reconciliation evidence.

P70-T01 through P70-T03 and P70-T05 remain `todo`; P70-T04 is `in_progress`
while its reference-capacity tooling and final report are prepared.

P70-T09, P70-T12, P70-T13, and P70-T14 are complete. P70-T06 and the v1 release
remain blocked on P66 completion, conformance, security, release-CI,
long-capacity, and documentation evidence.

## Evidence

- P70-T14 implementation: `observability.sync_progress_log_interval` defaults
  to `30s`, accepts YAML and
  `ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL`, and rejects values outside `1s` to
  `1h`. Compose, Preview, Helm, capacity values, example configuration, and
  operations guidance expose the same setting. The sync service logs only
  after the active reporter successfully persists a changed runtime status;
  its last emitted snapshot and timestamp coalesce interval-local changes
  without adding a timer, query, or heartbeat.
- P70-T14 business logs: a composed observer preserves the existing bounded
  Prometheus counters and emits stage/result or operation/result fields after
  enrich, trace, verification, metadata, and maintenance observers report an
  outcome. Successful, unavailable, and stale-target outcomes use `info`;
  retry, failed, rejected, resource, and error outcomes use `warn`. Unknown
  values collapse to `other`, so raw task inputs, URLs, credentials, and nested
  errors cannot enter these records. Catalog maintenance emits success only
  when its durable sweep reports `Ran`.
- P70-T14 verification: focused ordinary and race tests for
  `./internal/config`, `./internal/observability`, `./internal/syncer`,
  `./internal/maintenance`, and `./internal/app` pass. `make lint`, `make
  compose-check`, `make helm-check`, `make plan-check`, `make check`, and `git
  diff --check` pass. The full check includes all Go and web tests, the complete
  Go race suite, generation, security, license, Docker, Compose, and Helm
  gates.
- P70-T14 Preview evidence: `make recreate-preview` replaced all seven
  application roles while the PostgreSQL and Reth container IDs remained
  unchanged; migration completed, every role ran, and API readiness returned
  `200`. With Reth continuously producing blocks, sync emitted at
  `08:42:53.318Z` for height `340` and next at `08:43:24.020Z` for the coalesced
  height `346`, with no per-block records between them; the status API then
  reached height `350` at zero lag. Enrich and trace emitted immediate bounded
  success records, catalog maintenance logged its executed sweep, and API kept
  its existing request-completion records. Disabled verification and metadata
  features produced no idle business heartbeat.
- P70-T13: `compose.preview.yaml` replaces the `roles=all` monolith with API,
  sync, enrichment, trace, verification, metadata, and maintenance processes
  built from the same image. All roles share PostgreSQL, Reth, migration,
  config, and genesis dependencies; only API publishes ports and receives the
  session pepper. Preview start and application-only recreation remove orphaned
  services, while recreation preserves PostgreSQL and Reth.
- P70-T13 verification: `make compose-check`,
  `BUILDX_CONFIG=/tmp/etherview-buildx make deployment-check`, `make
  plan-check`, and `git diff --check` pass. A fresh
  `BUILDX_CONFIG=/tmp/etherview-buildx make start-preview` ran all seven roles,
  completed migration, served ready API and embedded SPA responses, and reached
  the Reth head with zero lag. Application container IDs all changed after
  `BUILDX_CONFIG=/tmp/etherview-buildx make recreate-preview`, while PostgreSQL
  and Reth container IDs remained unchanged and indexed height advanced from 4
  to 18 with zero lag.
- P70-T10: `observability.log_level` and `observability.log_format` default to
  `info` and `json`; exact YAML and `ETHERVIEW_LOG_*` values are validated
  before startup. Every configuration-bearing command accepts the same
  `--log-level` and `--log-format` overrides after its command name, with CLI
  precedence and duplicate rejection. The entrypoint creates and injects the
  logger only after the effective configuration is known.
- P70-T10: JSON and text output share level filtering, redaction, URL
  contraction, trace correlation, and the stable net/http internal-error
  adapter. Compose preserves mounted YAML when host overrides are absent and
  forwards exact overrides when present; Helm schema and render regressions
  accept the supported enums and reject invalid values.
- P70-T10 verification: targeted ordinary and race tests for
  `./cmd/etherview`, `./internal/config`, `./internal/cli`,
  `./internal/observability`, `./internal/httpapi`, and `./internal/app` pass.
  `go test ./... -count=1`, targeted `go vet`, `make lint`, `make
  toolchain-check`, `make compose-check`, `make helm-check`, `make plan-check`,
  and `git diff --check` pass.
- P70-T07 configuration: YAML, environment, and `_FILE` inputs support an
  optional reader URL plus independently bounded pool sizes. Zero reader
  values inherit writer settings; negative, overflowed, malformed, and
  effective `min > max` inputs fail before runtime.
- P70-T04 tooling: the Compose parity runtime-fixture container and load driver
  share one runtime-tools image, with loadtest emitted by the existing
  `go-builder`. The anvil fixture is supplied by the configured image while the
  load service overrides its entrypoint. The target builds successfully; both
  binary entrypoints, the numeric non-root user, Compose configuration, plan
  validation, shell syntax, and whitespace checks pass. This scoped
  maintenance is not 500 RPS or soak evidence.
- P70-T07 runtime: only `api`/`all` opens the forced-read-only reader pool.
  Startup checks its migration ledger and exact chain/genesis identity.
  Ordinary projections and the explicit Etherscan read inventory use it;
  canonical/RPC fences, runtime and metric state, authentication,
  verification, external observations, media, mempool, and all writes remain
  writer-backed. Both operational and public readiness fail closed, with the
  latter bypassing Redis status cache.
- P70-T07 deployment: Compose preserves mounted YAML sizing unless an operator
  supplies an environment override. Helm injects the optional reader Secret
  only into API-bearing containers, supports an optional ExternalSecret key,
  rejects inline credentials and invalid effective bounds, and documents
  restart-on-secret-rotation. The reference maximum is 216 writer plus 96
  reader connections (312 steady state, 624 for full old/new overlap).
- P70-T07 verification: `go test ./internal/config ./internal/app
  ./internal/httpapi -count=1`, the corresponding `go test -race`, and
  `go test ./... -count=1` pass. `make toolchain-check`, `make lint`,
  `make generate-check`, `make helm-check`, and `make compose-check` pass.
  `make plan-check` and `git diff --check` pass after the evidence update.
- P70-T07 integration boundary: `make test-integration` passes against a
  disposable PostgreSQL 18 database after applying and checking every
  migration. The integration-tag regression verifies real writer/reader
  session settings, SQLSTATE `25006`, reader schema compatibility, and
  chain/genesis matching. Both pools used the same PostgreSQL endpoint, so no
  asynchronous replica-lag or reader-outage result is claimed by this scoped
  item.
- P70-T08 implementation: bounded duplicate-key-safe Genesis JSON parsing uses
  Ethereum account/storage tries and the execution header through Amsterdam,
  and requires the configured plus canonical block-zero hash and state root.
  Migration `0022_genesis_state` persists database-guarded immutable balance,
  nonce, code, code hash, and storage-root facts without raw slots; exact
  predeploy code plus its proxy wake commit atomically. Missing input remains a
  typed unavailable capability. The source is either the existing absolute
  local file or a mutually exclusive public-HTTPS URL, and both remain limited
  to indexing from block zero.
- P70-T08 remote boundary: URL validation rejects credentials, query, fragment,
  non-443 ports, path traversal, redirects, environment proxies, and any DNS
  answer in a private or special-use network. Direct verified-IP dialing,
  identity encoding, the 64 MiB declared/streamed limit, an explicit MIME
  allowlist, and JSON validation bound the GET. An optional non-zero lowercase
  SHA-256 authenticates the exact response bytes before JSON validation.
  Network and hostile-content failures expose only stable redacted states.
- P70-T08 remote lifecycle: the importer checks immutable completion before any
  request, waits for canonical block zero, and holds a per-chain PostgreSQL
  session advisory lock across its second completion check and fetch. HTTP,
  checksum, and parsing remain outside the import transaction. Successful
  completion removes the remote dependency; a configured digest is compared
  directly with the persisted digest. Concurrent replicas fetch once, and
  checksum or canonical-identity failures commit no account, code, or proxy-job
  fact.
- P70-T08 public surfaces: generated Go/TypeScript contracts expose cursor-bound
  `/api/v1/genesis/accounts` pages with decimal-string quantities. The embedded
  bilingual SPA exposes `/genesis` and links canonical block zero to it.
  Compose supports file or remote environment inputs. Helm either mounts the
  PVC file or injects URL, digest, and timeout only into `all`/`sync` roles; it
  rejects source conflicts, unmounted inline values, non-zero starts, invalid
  digests, and out-of-range timeouts.
- P70-T08 verification: trie/hash/parser, capability API, cursor,
  proxy-candidate, split-role parity, immutable-observation, remote
  configuration, SSRF/DNS, proxy, redirect, status, MIME, encoding, size,
  timeout, checksum-order, and error-redaction tests pass, as do 68
  browser-component tests. Reference vectors cover fixed roots/hashes,
  100-account branching, and an Amsterdam genesis header. PostgreSQL 18
  `make test-integration` verifies file/URL byte-equivalent facts, one fetch for
  concurrent importers, completed offline restart, persisted-digest conflict,
  canonical completion reauthentication, and zero partial writes on checksum,
  block-hash, state-root, or temporary HTTP failure.
- P70-T08 common gates: `go test ./...`, `make test-race`,
  `make generate-check`, `make security-check`, `make lint` with a writable
  lint cache, `make toolchain-check`, `make compose-check`, `make helm-check`,
  `make license-check`, `make plan-check`, and `git diff --check` pass. The API
  generator override uses the patched `minimatch`/`brace-expansion` chain;
  both npm audits report zero vulnerabilities, `govulncheck` reports zero
  reachable vulnerabilities, and both working-tree and history secret scans
  are clean.
- P70-T08 browser boundary: the explicit restricted-macOS fallback uses bundled
  Chromium in single-process mode and isolates every test in its own worker,
  while ordinary local and CI runs retain their multi-process browser. The
  complete embedded-server suite passed all eight deep-link, bilingual,
  theme, canonical/orphan, Genesis/capability, WCAG, SIWE/billing/admin, and
  wallet-boundary cases.
- P70-T11: transaction-list and address-detail E2E assertions now use the
  configured 18-decimal native display instead of the former raw wei integer,
  and finalized status selects the unique high-visibility badge rather than
  colliding with the hidden More-details value. `PLAYWRIGHT_USE_BUNDLED=1
  PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` passed all 8 tests; `npm --prefix
  web test` passed all 131 tests, and `npm --prefix web run lint` passed.
- P70-T12: `StageID.Validate` now accepts the same lowercase alphanumeric,
  hyphen, and underscore alphabet as maintenance-stage validation, while
  continuing to reject uppercase, whitespace, slash, and punctuation. The
  production trace-enabled dispatcher test validates every scheduled stage,
  including `state_diff@1`. Focused ordinary and race tests for
  `internal/enrich`, `internal/app`, and `internal/maintenance` passed, and
  focused `golangci-lint` reported zero issues. The Docker-backed runtime smoke
  passes both monolith and seven-role split layouts, replica survival, short
  load, and normalized database/API/SPA parity.
- P70-T09 type ownership: `internal/ethrpc` retains only bounded transport,
  endpoint-pool, capability, scheduling, observation, and stable-error
  concepts. Protocol scalars and recognized RPC objects use go-ethereum
  `common`, `hexutil`, `core/types`, `rpc`, and `core.Genesis` types directly;
  there are no repository aliases or replacement Ethereum models in that
  package. Genesis JSON decodes directly into `core.Genesis`, and
  `core.Genesis.ToBlock()` owns the state root and block identity after a narrow
  duplicate-key, normalized-map-collision, resource, and uint256 preflight.
- P70-T09 raw and persistence boundary: raw-first block acquisition preserves
  supported-object unknown fields and validates header, transaction, receipt,
  log, withdrawal, sender, root, inclusion, uncle, gas-delta, contract-address,
  and effective-gas-price relationships before any atomic repository write.
  Geth transaction types 0 through 4 are supported; type 127 and other
  unsupported formats fail permanently before persistence or coverage
  advancement. Root-preserving versioned metadata carries validated PoW uncle
  headers, and legacy rows missing required non-empty headers return
  `ErrStoredUncleHeadersUnavailable`.
- P70-T09 compatibility: downstream indexing, enrichment, mempool, metadata,
  billing, x402, native API, Etherscan adapters, stores, and test fixtures use
  the direct geth values while SQL and OpenAPI contracts remain unchanged.
  Dynamic effective gas price is authenticated against the matching
  transaction and block base fee; fresh receipts require it, compatible stored
  rows may derive a missing value, and poisoned or context-free values are
  never exposed as verified. The state-difference adapter accepts geth's
  JSON-number nonce while retaining bounded quoted-quantity compatibility and
  persists the schema's required empty storage key for non-storage fields;
  malformed, negative, fractional, and oversized nonce inputs still fail
  closed.
- P70-T09 license closure: the reviewed go-ethereum v1.17.2 scanner baseline
  now includes the separately licensed `crypto/bn256` package. The transitive
  `github.com/holiman/bloomfilter/v2` v2.0.3 module archive omits its
  repository-root MIT license, so the narrow scanner exception fixes the
  version, checked-in text SHA-256, dependency presence, and exact scanner
  result without expanding the permissive allowlist. The production image
  copies all reviewed license texts, and its rootfs check requires each path.
- P70-T09 verification: `go test ./... -count=1`, `go test -race ./...
  -count=1`, `make test-integration`, and the integration-tag race suite pass
  against disposable PostgreSQL 18. `make toolchain-check`, `make plan-check`,
  `make generate-check`, `make lint`, `make security-check`, `make
  license-check`, `make helm-check`, `make compose-check`, and `git diff
  --check` pass. One initial ordinary test overlapped the full race run and
  timed out waiting for Docker in
  `TestContainerCompilerValidatesAndAppliesIsolation`; the isolated case and
  serialized full suite both passed immediately afterward. `make docker-check
  docker-image-check`, `make compose-runtime-smoke`, and the aggregate `make
  check` pass on the completed working tree.
