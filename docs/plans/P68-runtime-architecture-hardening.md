# P68 — Runtime and Architecture Hardening

Status: `done`

## Outcome

Make long-lived API streams shutdown-safe and resistant to replay/cache
head-of-line blocking, then replace the repository's largest SQL, runtime,
HTTP, and Web service locators with explicit generated or modular boundaries.
Public REST contracts, page routes, configuration keys, and the fresh-database
schema remain unchanged.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0004: Durable runtime status and event replay](../decisions/ADR-0004-durable-runtime-status-and-events.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0018: API read replica routing](../decisions/ADR-0018-api-read-replica-routing.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P68-T01 | done | P40, P60 | Per-write SSE deadlines and cancellation-safe HTTP shutdown; non-blocking durable replay, cache invalidation circuit breaking, and typed mempool failures | HTTP/1.1 and HTTP/2 service tests; event/Redis/mempool race tests; runtime E2E |
| P68-T02 | done | P68-T01 | Move all static read-path SQL into named sqlc queries without changing snapshots, cursors, ordering, or reader routing | generation, focused query/catalog/compatibility tests, PostgreSQL integration and race |
| P68-T03 | done | P68-T02 | Move correctness/write SQL into sqlc transactions, isolate migration and validated partition DDL as the only raw-SQL executors, and enforce the boundary | generation, source-boundary tests, lease/replay/reorg integration and race |
| P68-T04 | done | P68-T03 | Split runtime assembly and configuration loading/validation into narrow role and subsystem builders while retaining an independent executable component manifest | config, component graph, monolith/split parity, deployment checks |
| P68-T05 | done | P68-T04 | Split HTTP routes into explicit capability modules and reject enabled modules with missing dependencies at startup | handler/route/capability tests, generation, browser and runtime gates |
| P68-T06 | done | P68-T05 | Split core Web pages and language resources by domain and add pinned Biome hook, complexity, function, and file-size linting | TypeScript, Vitest, accessibility, responsive and embedded-browser gates |
| P68-T07 | done | P68-T06 | Close the selected Go complexity/duplication baseline, wire source and SQL checks into repository gates, and complete full acceptance evidence | lint, common gates, PostgreSQL, browser, production topology, Hardhat, Foundry and Preview suites |
| P68-T08 | done | P68-T01, P68-T07 | Align runtime E2E ordinary-response write deadlines with its bounded-load request budget while retaining the longer-idle SSE deadline regression | focused runtime tests, Compose validation, and production-topology E2E |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Idle SSE connections survive longer than `server.write_timeout`, while
      each individual write and flush remains bounded by that timeout.
- [x] Active event and home streams close promptly on process cancellation and
      cannot exhaust the graceful-shutdown budget.
- [x] Durable replay performs no PostgreSQL or optional-adapter I/O while the
      fanout mutex is held; replay/live delivery remains ordered and duplicate
      free under bounded concurrency.
- [x] Redis loss disables cache reads, suppresses repeated backend calls during
      backoff, and permits exactly one recovery probe without affecting API
      correctness or readiness.
- [x] Mempool RPC, invalid-snapshot, and storage failures use typed
      classification rather than error-message inspection.
- [x] Production static SQL originates in `internal/db/queries`; only the
      migration runner and validated partition-DDL module may execute raw SQL.
- [x] Runtime roles, HTTP capabilities, and Web pages have explicit modular
      dependencies with no hidden type assertions or reader fallbacks.
- [x] No hand-written production Go file exceeds 2,000 lines; selected Go and
      Web complexity, function-size, duplication, hook, and file-size gates
      pass without blanket suppressions.
- [x] OpenAPI, database schema, public routes and response shapes, configuration
      keys, and monolith/split behavior remain unchanged except for the approved
      corrected stream and startup-failure semantics.

## Current Blockers

None.

## Evidence

- P68-T08 diagnoses GitHub Actions run 32544852014 job 96961409931 as a
  runtime-fixture budget conflict: one `listTransactions` request completed on
  the server in 259ms with status 200 but crossed the test-only 250ms ordinary
  response write timeout, so the load client observed one transport error in
  120 requests and correctly failed its zero-error threshold. The fixture now
  shares one 2-second budget between ordinary response writes and load
  requests. SSE uses the same production Transport/TLS settings with a
  context-bounded streaming client, stays idle for three times that write
  budget, and retains its per-write deadline assertion. Tagged focused tests,
  `make lint-go plan-check`, `make compose-check`, and the current-baseline
  production image's full `make test-runtime-e2e-prebuilt` pass; the latter
  completes monolith in 38.27s and the six-role topology in 48.23s. Two exact
  `make test-runtime-e2e` rebuild attempts reached no code because Docker timed
  out reading the same two modules from `proxy.golang.org`; this external
  download failure is not recorded as rebuilt-image evidence.
- P68-T01 baseline: current focused event, HTTP, and component tests plus
  `make lint` pass on `main@a860089`. Review identified production SSE lifetime
  and shutdown gaps, durable replay I/O under the fanout mutex, repeated Redis
  invalidation attempts, and string-based mempool failure classification.
- P68-T01 implementation clears the idle SSE deadline and applies the existing
  timeout to each write/flush, binds API requests to component cancellation,
  force-closes a failed drain, provisionally buffers live events while bounded
  replay and cache invalidation execute outside the fanout mutex, deduplicates
  only exact completed invalidations, circuit-breaks Redis cache recovery, and
  uses typed mempool cycle failures. HTTP/1.1 and TLS/HTTP2 streams survive
  beyond a 50ms write timeout and close on lifecycle cancellation.
  `go test -race ./internal/events ./internal/accelerator ./internal/mempool
  ./internal/httpapi ./internal/components -count=1`, `go test ./... -count=1`,
  tagged runtime compilation, `make lint-go`, `make plan-check`, and
  `git diff --check` pass. The rebuilt production image's monolith/six-role
  `make test-runtime-e2e` passes in 74.367s and delivers a durable SSE event
  after three times its configured 250ms write timeout in both topologies.
- P68-T02 moves static analytics, query, catalog, Etherscan compatibility,
  metadata, mempool, state, authentication, contract-artifact, event replay,
  and finalized-height reads into named sqlc query sources. Exported generated
  statements retain the existing `database/sql` scan adapters where those
  adapters encode public projection behavior; event and analytics readers use
  typed pgx/sqlc rows and explicit read-only repeatable-read transactions.
  Snapshot identity, cursor predicates, canonical joins, ordering, optional
  read-pool routing, and compatibility response behavior remain unchanged.
  `make generate-check`, `make lint-go`, `go test ./... -count=1`, focused
  query/catalog/Etherscan tests, `make test-hardhat3-provider-compat`,
  `make test-integration`, `make test-integration-race`, and
  `git diff --check` pass. Both PostgreSQL targets applied migrations through
  `0049_ens_primary_names`, exercised all seven integration-tagged packages,
  and removed their owned PostgreSQL 18 projects and volumes.
- P68-T03 moves production correctness and write statements for administration,
  analytics, authentication, enrichment, runtime events, Genesis import,
  maintenance, mempool, metadata, state reconciliation, canonical storage,
  verified selectors, and verification into named sqlc sources. Existing
  correctness transactions retain their lock order and scan adapters while
  executing only exported generated statements. Dynamic Etherscan ordering,
  ranges, and topic filters are fixed parameterized queries; topic `AND`/`OR`
  folding and exact `stage@version` queue selection are exercised against real
  PostgreSQL. Multi-table canonical cleanup and publication changes use fixed
  writable CTEs, and generated savepoint statements preserve atomic replay.
  `make source-check` enforces that only the migration runner and validated
  partition-DDL module may own raw production SQL and reports 257 checked Go
  files plus 93 SQL sources. `go test ./... -count=1`, focused Go race tests,
  `make generate-check`, `make lint-go` (0 issues),
  `make test-hardhat3-provider-compat`, `make test-integration`, and
  `make test-integration-race` pass. The full PostgreSQL integration package
  passes in 147.531s and its race run in 163.075s; an additional real-database
  fixed-topic regression passes in 133.945s. Every owned PostgreSQL 18 project,
  network, and volume was removed.
- P68-T04 replaces the 900-line runtime registration block with typed shared,
  sync, API, verification, enrich, trace, metadata, maintenance, and disabled
  role builders. Shared acquisition remains in `serve.go`, typed dependency
  transport and ordered invocation live in `runtime_assembly.go`, and the
  independently computed production graph lives in `component_manifest.go`.
  Configuration now separates model/default/YAML loading, role-scoped
  environment and secret overrides, typed scalar override groups, global and
  role validation, and pure subsystem validators without changing keys,
  defaults, precedence, or secret loading. `go test -race ./internal/app
  ./internal/config -count=1`, `go test ./... -count=1`, and `make lint-go`
  pass. `make deployment-check` passes Buildx, all monolith/distributed,
  accelerator, Preview, Hardhat and Foundry Compose renders, plus every Helm
  lint/template/render check. The rebuilt production image's
  `make test-runtime-e2e` passes in 74.387s: monolith in 31.92s and the complete
  six-role topology in 41.29s, including pending identity, publication, reorg,
  RPC/PostgreSQL outage recovery, restart, API/SSE/SPA/load, and process TLS.
- P68-T05 composes the public mux from eight explicit capability modules and
  validates the production Native, Catalog, Analytics, Compatibility, Events,
  Home, Metadata, Proxy, Verification, and Web dependency set before any route
  registration. Catalog/address/delegation/readiness/Web dependencies are now
  passed directly; capability discovery no longer depends on reader, catalog,
  or SPA type assertions. Disabled metadata and verification paths retain
  their stable typed responses. HTTP handlers and models are split by domain,
  reducing `httpapi.go` from 3,588 to 710 lines without route or response
  changes. Capability omission and deterministic-error tests, focused race,
  `go test ./... -count=1`, `make generate-check`, and `make lint-go` pass.
  The embedded production SPA Playwright gate passes 23/23, including CSP,
  reserved-route isolation, deep links, accessibility, responsive layouts,
  SIWE/billing/admin, and wallet boundaries. The rebuilt production
  `make test-runtime-e2e` passes in 76.329s: monolith in 32.71s and the complete
  six-role topology in 42.45s through publication, reorg, outages, restart,
  API/SSE/SPA/load, and process-native TLS.
- P68-T06 splits the former 5,464-line page locator into Entity, Block,
  Transaction, Address, Token/NFT, and Verification domains plus a shared
  explorer module. Every production page file is below 2,000 physical lines;
  the largest is Transaction at 1,889 lines. The former 2,927-line bilingual
  resource locator is now a 76-line merger over seven shared English/Chinese
  domains, each below 711 lines. Exact `@biomejs/biome@2.5.10` is lockfile
  pinned and part of `web-lint`; it rejects unused imports/variables, invalid
  hook placement/dependencies, cognitive complexity above 100, functions above
  600 effective lines, and production files above 2,000 effective lines.
  Transaction detail complexity drops from 112 to 99 without suppressions.
  TypeScript, Biome, 346 Vitest cases, the production Vite build,
  `make generate-check`, and `make lint` pass. The rebuilt embedded-SPA
  Playwright gate passes 23/23, including bilingual deep links, responsive and
  WCAG 2.1 AA checks, hashed asset/CSP isolation, SIWE/billing/admin, and wallet
  boundaries.
- P68-T07 splits the former 2,291-line proxy processor into orchestration,
  candidate loading, block-pinned RPC detection, and transactional persistence
  files, each no larger than 820 lines. Shared catalog address/page parsing
  removes the selected HTTP duplication without changing validation order or
  responses. Production Go now enables `dupl` at 150 tokens and `gocognit` at
  150, with only those two structural checks excluded from test fixtures.
  `make source-check` also rejects hand-written production Go files above 2,000
  physical lines while preserving the generated-source and two raw-SQL
  executor boundaries; it passes over 297 Go files and 93 SQL sources.
- P68-T07 updates the pinned Go license scanner to the maintained v2 module and
  its canonical BSD-2-Clause attribution. Focused config, source-boundary,
  enrichment, and HTTP tests pass ordinarily and under the race detector;
  `make lint-go` reports zero issues. The owned PostgreSQL 18 integration and
  integration-race suites pass all seven tagged packages in 146.847s and
  162.284s. Fresh production-image schema migration passes through
  `0049_ens_primary_names`; the monolith/six-role runtime gate passes in
  74.653s; the embedded Chromium suite passes 23/23; real Hardhat 3 and Foundry
  production gates pass both topologies in 215.587s and 97.303s.
- The strict Preview metadata gate initially rejected two runs after the public
  `ipfs.io` request exceeded the checked-in 10-second budget twice before an
  eventual third-attempt success. Preview alone now permits a bounded
  30-second cold public-gateway fetch, while defaults and the production
  example remain at 10 seconds. Its exact rerun passes in 37.704s with one
  durable attempt, 205 bytes, the fixed SHA-256, public-network policy, and
  restart-stable persistence. The aggregate `make check`, `make plan-check`,
  and `git diff --check` pass on the completed tree.
