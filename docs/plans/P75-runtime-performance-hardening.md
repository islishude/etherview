# P75 — Runtime and Performance Hardening

Status: `done`

## Outcome

Make every canonical traversal, historical backfill, compatibility query, core
write, public projection, rate limiter, and embedded-SPA delivery path bounded
and measurably efficient before release. Preserve raw-first Ethereum input,
exact chain/block identity, PostgreSQL correctness authority, lease-fenced
publication, stable public results, and monolith/split behavior.

This plan does not turn optional infrastructure into correctness storage, add
an RPC proxy, weaken canonical or coverage checks, or treat local benchmarks as
the P70 reference-capacity result.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0003: Spec-first API and canonical identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0006: Durable canonical coverage and live priority](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md)
- [ADR-0013: Embedded SPA and browser security](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0018: API read-replica routing](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0022: Go-ethereum and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0044: Prepaid API billing and x402 top-ups](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [Etherscan V2 compatibility](../architecture/etherscan-v2-compatibility.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P75-T01 | done | P68 | Reproducible chainbundle, core-write, public-query, and Web asset baselines plus bounded Go-runtime and writer/reader pool metrics | focused benchmarks, metric contracts, Web build budget, common gates |
| P75-T02 | done | P75-T01 | Reject over-depth/finalized canonical changes before parent RPC, canonical-row traversal, or branch-sized allocation | counting-source/repository unit tests, race and PostgreSQL integration |
| P75-T03 | done | P75-T01 | Bound backfill by aggregate raw bytes and row counts and commit recoverable subsegments inside one durable range lease | cancellation, lease-expiry, partial-progress and race tests; aggregate PostgreSQL gate in P75-T12 |
| P75-T04 | done | P75-T01 | Bound every compatibility pagination/work window and use indexable miner, timestamp, direction, and topic predicates | handler goldens, query-plan fixtures, PostgreSQL/race and compatibility tests |
| P75-T05 | done | P75-T01 | Replace mutable repeatedly decoded chain bundles with one owned immutable validated boundary | hostile wire/storage vectors, source-mutation/race tests, decode/allocation benchmarks |
| P75-T06 | done | P75-T03, P75-T05 | Batch core identity, inclusion, receipt, log, withdrawal, canonical, and outbox persistence without changing chain-lock or coverage atomicity | generated SQL, conflict/reorg/partition tests, PostgreSQL benchmarks and runtime parity |
| P75-T07 | done | P75-T01 | Replace full block-Raw fanout on list/detail reads with exact narrow typed/scalar projections and normalized withdrawals | query/model corruption tests, PostgreSQL plans/benchmarks, API/browser/runtime parity |
| P75-T08 | done | P75-T01 | Make RPC reservations cancellation-safe and bound inactive authenticated and anonymous local rate buckets | deterministic cancellation/cardinality tests, Redis fallback race and metrics |
| P75-T09 | done | P75-T01 | Route-lazy Web chunks, on-demand CodeMirror, precompressed immutable assets with precomputed metadata, and one durable SSE invalidation source instead of live polling | asset-graph budget, handler cache/encoding/CSP tests, Vitest, Playwright and runtime E2E |
| P75-T10 | done | P75-T04 | Remove the superseded accountless request-payment runtime and configuration while retaining immutable payment audit rows and P73 top-up accounting | config, migration/audit, billing, generation, deployment and runtime tests |
| P75-T11 | done | P75-T05, P75-T07, P75-T10 | Move public-query/error/cursor and ABI/proxy/stage contracts out of transport/worker hubs; split oversized production and test scenarios and enforce function boundaries | import graph, source/lint, focused tests, common and topology gates |
| P75-T12 | done | P75-T02–P75-T11 | Complete aggregate security, race, generation, integration, browser, deployment, and monolith/split production-topology acceptance on the final tree | `make check`, explicit integration/race/browser/runtime/Hardhat/Foundry gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] A rejected fork performs only configured bounded RPC/DB work and never
      allocates from an attacker-controlled numeric depth.
- [x] A backfill worker has one explicit aggregate memory/work budget and
      restart-safe partial progress.
- [x] Compatibility list work is bounded independently of response size and
      every common predicate has a verified indexable plan.
- [x] One accepted RPC bundle is fully decoded and authenticated once before
      persistence; later boundaries cannot mutate it.
- [x] Core segment persistence uses bounded relation batches rather than one
      database round trip per transaction, receipt, log, or withdrawal.
- [x] Block and transaction pages do not transfer a complete block Raw object
      per projected row.
- [x] Canceled RPC requests cannot reserve future rate slots, and local bucket
      cardinality remains bounded during Redis loss.
- [x] The root Web route does not preload CodeMirror or feature-only pages;
      direct embedded delivery negotiates a prebuilt compressed representation
      without weakening CSP, ETag, cache, HEAD, or fallback behavior.
- [x] Superseded request-payment execution cannot be configured or reached;
      historical audit and prepaid top-up semantics remain intact.
- [x] Current targeted, generation, source, race, integration, browser,
      deployment, security, and monolith/split gates pass with concise evidence.
- [x] P70 reference capacity remains deferred to P70-T04 and will be rerun
      only on the final accepted revision in the named representative
      environment; no P75 local result is claimed as closure.

## Current Blockers

None.

## Evidence

- P75-T01 claimed after a repository-wide read-only audit. Baseline gates on
  the clean starting tree pass: `make source-check`, `make web-lint`, `make
  lint-go`, `go test ./...`, `npm --prefix web test`, `make web-build`, and
  `make plan-check`. The current root preload graph is 1,638,867 uncompressed
  bytes (474,528 bytes under a local gzip probe) and includes the 319,341-byte
  CodeMirror vendor chunk. Docker, integration, race, runtime, and reference
  soak evidence remains to be rerun after implementation.
- P75-T01 adds bounded Go runtime and writer/reader `database/sql` pool metrics,
  documents their capacity use, and adds maintained `make benchmark` and
  `make benchmark-integration` targets plus a root Web asset-graph budget. On
  Apple M3 Max/ARM64 with PostgreSQL 18 and one iteration, the large fixture
  records 18.25ms/7.08MB for decode, 20.79ms/7.42MB for validation, and
  37.73ms/14.44MB for clone; its idempotent core commit records 390.79ms and
  40.04MB, while a 100-row transaction page records 78.15ms and 119.56MB.
  The managed database and volume were removed. Focused Go tests, integration
  tag compilation, 37 Vitest files/365 tests, two Node asset-budget tests, Web
  build/budget, `make source-check plan-check`, and `git diff --check` pass.
- P75-T02 checks forward gap, minimum detach depth, finalized floor, covered
  sparse cardinality, and numeric detach depth before crossing the configured
  work boundary. A rejected two-block-depth fork performs exactly two parent
  source reads; oversized forward gaps, ordinary detaches, and sparse ranges
  perform no extra parent or canonical-row traversal. Focused ordinary/race
  indexer and sync tests pass. The complete managed PostgreSQL 18 integration
  suite passes all seven tagged packages in 184.924 seconds and removes its
  container, network, and volume. ADR-0006 and the architecture overview now
  record the pre-work bound; final production topology acceptance is owned by
  P75-T12.
- P75-T03 adds explicit `runtime.backfill_batch_bytes` and
  `runtime.backfill_batch_rows` configuration across file/environment,
  reference, Preview, and Helm surfaces. `chainbundle.MeasureWork` counts every
  independently owned Raw slice and relational row without another decode.
  Backfill reserves half of each budget for the next fetched block, commits
  contiguous subsegments under the same lease, signals every durable commit,
  and releases an oversized or failed suffix for exact coverage-driven retry.
  Focused config, chainbundle, app, and sync tests pass; sync race passes and
  covers three one-block subsegments, final coverage, three event wakes, and
  oversized-block lease reclamation. Aggregate PostgreSQL and topology gates
  remain centralized in P75-T12.
- P75-T04 caps every compatibility `page * offset` window at 10,000 and every
  tip-clamped log interval at 100,000 blocks. Migration `0061` adds miner and
  timestamp indexes; mined-block, block-by-time, and log ordering is fixed per
  query. Log candidate SQL uses a one-time branch and the typed topic-zero
  index only when the complete topic expression requires it. The compatibility
  matrix, ADR-0003, architecture, generated sqlc output, and tests are aligned.
  Focused ordinary/race Etherscan tests, generation/source/plan checks,
  Hardhat 3 provider compatibility, and managed PostgreSQL 18 ordinary/race
  `EXPLAIN` fixtures pass; both owned database environments were removed.
- P75-T05 changes bundle ownership so one decode from the authoritative block,
  receipt, and uncle roots produces an independent persistence value and checks
  every typed/derived Raw view in that same pass. Mutated views fail closed and
  later source-byte mutation cannot alter stored or cloned data. Redundant
  segment validation and row-insertion decodes are removed from memory and
  PostgreSQL paths, including reorg attachment ownership. ADR-0022 and the
  architecture document the boundary. Focused ordinary/race chainbundle,
  store, indexer, and sync tests pass. On the original benchmark host, large
  clone drops from 37.73ms/14.44MB to 18.51ms/7.43MB; PostgreSQL large
  idempotent commit drops from 390.79ms/40.04MB to 301.75ms/18.20MB, with the
  owned benchmark database and volume removed.
- P75-T06 replaces per-row core writes with foreign-key-ordered JSONB record
  batches capped at 512 rows and 4 MiB, with an indivisible row isolated in one
  batch. The same chain-locked transaction now batches block/transaction
  identity, inclusions, receipts, logs, withdrawals, canonical attach/detach,
  journal and derived canonicality, and generation-aware core outbox writes.
  Legacy generated statements were removed. On Apple M3 Max/ARM64 with
  PostgreSQL 18 and one iteration, the large idempotent commit drops from the
  original 390.79ms/40.04MB baseline to 52.95ms/13.97MB. Focused ordinary and
  race unit tests, generation/source checks, and managed PostgreSQL 18
  ordinary/race protocol, reorg, sparse coverage, derived-journal, and
  partition tests pass; every owned database, network, and volume was removed.
- P75-T07 adds migration `0062` with stored narrow block projections and moves
  native and Etherscan block/transaction/address/token/log/miner/time reads off
  full `blocks.raw` transfer. Native block models compare authenticated
  Raw-derived counts with normalized inclusions and withdrawals; dynamic-fee
  receipt reads authenticate against exact block identity plus the projected
  base fee. Corrupt count/withdrawal fixtures fail closed. On Apple M3
  Max/ARM64 with PostgreSQL 18 and one iteration, the large block page drops
  from 24.28ms/1.41MB to 2.96ms/28.4KB and the 100-row transaction page drops
  from 77.58ms/119.32MB to 23.70ms/4.64MB. Focused ordinary/race tests,
  generation/source checks, managed PostgreSQL ordinary/race corruption and
  query-plan tests, and Hardhat 3 provider compatibility pass; all owned
  PostgreSQL resources were removed.
- P75-T08 replaces eager RPC time-slot reservation with a FIFO live-waiter
  dispatcher: cancellation removes a queued waiter and advances no future
  slot. The local HTTP fallback now has independent 16,384-anonymous and
  65,536-authenticated LRU bounds, retains each bucket for at least one full
  refill, expires both inactive classes, and rejects a new identity instead of
  evicting active exhausted state. Deterministic third-request cancellation,
  authenticated/anonymous cardinality, concurrent churn, Redis failure/circuit,
  app wiring, observability, ordinary and race tests pass; ADR-0015,
  architecture, and operations guidance record the degraded-mode semantics.
- P75-T09 makes every feature route lazy and loads CodeMirror only for a shown
  verified artifact. The initial graph falls from 1,640,313 raw/475,367 gzip
  bytes to 933,543/290,438 and forbids page, editor, ECharts, x402, verification,
  and billing chunks. The build emits deterministic Brotli/Gzip sidecars and a
  bounded SHA-256 manifest; the Go handler negotiates representations with
  variant ETags and preserves identity ranges, HEAD, CSP nonce, cache, and
  fallback rules. One durable `/api/v1/events` connection now invalidates live
  React Query state; the home page refetches one atomic `GET /api/v1/home`
  publication and list/proxy polling is removed. 38 Vitest files/367 tests,
  three Node build tests, Web lint/build/budget, Go ordinary/race Web/HTTP
  tests, 22 unaffected Playwright cases, and focused ECharts/CWIA remediation
  reruns pass. The single aggregate Playwright/runtime rerun remains in
  P75-T12.
- P75-T10 removes `features.x402_billing`, `billing.routes`, coarse request-
  payment settings/environment aliases, the HTTP payment dispatcher and
  canonical-resource codec, the native quota wrapper, paid API-key context
  bypass, the retired request metric, and the accountless `x402testnet`
  command/support package. Stale YAML and environment keys now
  fail loading and ordinary native/compatibility requests reject payment
  authorization before dispatch. Production payment creation is exposed only
  as `ReserveTopup`; an integration-tag-only seed retains explicit coverage of
  historical `legacy_request` audit and operator reconciliation. P73 prepaid
  usage buffering, x402 top-up transport, immutable payment/event rows, expiry,
  admin reads, and metrics remain. Focused ordinary/race config, billing,
  HTTP, app, observability, Etherscan, managed PostgreSQL billing/top-up/admin,
  and complete deployment/Helm checks pass; ordinary/race database resources
  were removed.
- P75-T11 introduces neutral `publicquery`, `abicontract`, `abicalldata`,
  `proxycontract`, and `stagecontract` owners, then routes HTTP aliases,
  readers, state, maintenance, Genesis, verification replay, and worker aliases
  through those types. `go list -deps` confirms query has no HTTP/enrich
  dependency and state has no HTTP dependency. `source-check` now rejects those
  reverse imports and any neutral-contract import of the worker hub. Route,
  event invalidation, chart route metadata, core batch persistence, billing
  service assembly, and payment-header regressions also live in separate
  semantic files instead of their former hubs. Focused ordinary/race query,
  state, catalog, enrich, maintenance, Genesis, verify, app, source-check, and
  contract-package tests pass.
- P75-T12 closes aggregate acceptance on the final tree. `make check` passes
  generation and source boundaries, vet/lint, ordinary and race suites, 38
  Vitest files/367 tests, three asset-build tests, security/leak/license gates,
  Dockerfile validation, Compose rendering, and Helm lint/render. Explicit
  managed PostgreSQL integration and integration-race suites pass (including
  the 178.311-second and 205.171-second integration packages) and remove their
  owned resources. Playwright passes all 24 Chromium cases in 1.4 minutes.
  Production-image schema migration/status passes through `0062`. Runtime E2E
  passes monolith/distributed core behavior in 85.167 seconds and prepaid x402
  behavior in 38.267 seconds; real Hardhat 3 and Foundry production gates pass
  both topologies in 211.603 and 96.615 seconds respectively. Aggregate image
  testing found and fixed a missing Docker build-context boundary for the Web
  compression scripts and asset budget, after which both `make docker-build`
  and the final common gate pass. The known Hardhat fixture audit result remains
  eight low-severity transitive findings with no upstream fix; the maintained
  high-severity audit gate passes. P70-T04 still owns the representative 500
  RPS/30-minute capacity run; no local benchmark or bounded runtime load is
  claimed as that external release result.
