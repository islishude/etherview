# P68 — Runtime and Architecture Hardening

Status: `blocked`

## Outcome

Make long-lived API streams shutdown-safe and resistant to replay/cache
head-of-line blocking, then replace the repository's largest SQL, runtime,
HTTP, and Web service locators with explicit generated or modular boundaries.
The home snapshot contract adds an event-version fence. Page routes, configuration
keys, and the fresh-database schema remain unchanged.

## References

- [ADR-0050: Native pgx and typed queries](../decisions/ADR-0050-native-pgx-and-typed-queries.md)
- [Native pgx acceptance record](pgx-native-acceptance.md)
- [Architecture](../architecture/overview.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0004: Durable runtime status and event replay](../decisions/ADR-0004-durable-runtime-status-and-events.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0018: API read replica routing](../decisions/ADR-0018-api-read-replica-routing.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P68-T01 | done | P40, P30 | Per-write SSE deadlines and cancellation-safe HTTP shutdown; non-blocking durable replay, cache invalidation circuit breaking, and typed mempool failures | HTTP/1.1 and HTTP/2 service tests; event/Redis/mempool race tests; runtime E2E |
| P68-T02 | done | P68-T01 | Move all static read-path SQL into named sqlc queries without changing snapshots, cursors, ordering, or reader routing | generation, focused query/catalog/compatibility tests, PostgreSQL integration and race |
| P68-T03 | done | P68-T02 | Move correctness/write SQL into sqlc transactions, isolate migration and validated partition DDL as the only raw-SQL executors, and enforce the boundary | generation, source-boundary tests, lease/replay/reorg integration and race |
| P68-T04 | done | P68-T03 | Split runtime assembly and configuration loading/validation into narrow role and subsystem builders while retaining an independent executable component manifest | config, component graph, monolith/split parity, deployment checks |
| P68-T05 | done | P68-T04 | Split HTTP routes into explicit capability modules and reject enabled modules with missing dependencies at startup | handler/route/capability tests, generation, browser and runtime gates |
| P68-T06 | done | P68-T05 | Split core Web pages and language resources by domain and add pinned Biome hook, complexity, function, and file-size linting | TypeScript, Vitest, accessibility, responsive and embedded-browser gates |
| P68-T07 | done | P68-T06 | Close the selected Go complexity/duplication baseline, wire source and SQL checks into repository gates, and complete full acceptance evidence | lint, common gates, PostgreSQL, browser, production topology, Hardhat, Foundry and Preview suites |
| P68-T08 | done | P68-T01, P68-T07 | Align runtime E2E ordinary-response write deadlines with its bounded-load request budget while retaining the longer-idle SSE deadline regression | focused runtime tests, Compose validation, and production-topology E2E |
| P68-T09 | done | P68-T01, P68-T07 | Make the home-feed slow-subscriber race regression wait for the complete fanout operation before inspecting disconnect state | focused repeated race test, HTTP API race tests, and common gates |
| P68-T10 | done | P68-T06 | Replace Biome with pinned Oxlint/Oxfmt gates, preserve size limits, and refactor formatted Web modules | tooling policy regressions, Web unit/browser tests, generation, docs and plan gates |
| P68-T11 | done | P68-T10 | Repair PR 86 lazy-route and Helm schema diagnostic regressions without weakening page or egress assertions | focused route regressions, Web unit/browser tests, Helm 3/4 rendering, generation, docs and plan gates |
| P68-T12 | blocked | P68-T11 | Explicit verification worker repository and task-local lease-loss recovery with fatal compiler error preservation | worker lifecycle, heartbeat, race, runtime parity |
| P68-T13 | done | P68-T11 | Version-fenced home snapshot API with bounded cancellation-safe waiting | OpenAPI, HTTP concurrency, generation and browser tests |
| P68-T14 | done | P68-T11 | Query-local chain event policies, recoverable single SSE connection, and UserOperation reorg refresh | Vitest, browser reconnect and reorg regressions |
| P68-T15 | dropped | P68-T11 | Standard-library sqlc generation and one typed transaction boundary | generation, numeric/null/array codecs and source boundary tests |
| P68-T16 | dropped | P68-T11 | All production read paths use typed generated queries with bounded scan memory | reader routing, snapshot, query and PostgreSQL/race tests |
| P68-T17 | dropped | P68-T11 | All production write paths use typed generated queries and enforce the execution boundary | atomic publication, leases, locks and PostgreSQL/race tests |
| P68-T18 | dropped | P68-T12, P68-T13, P68-T14, P68-T15, P68-T16, P68-T17 | Aggregate acceptance for all six review fixes and full database migration | common, integration/race, browser, schema, runtime, Hardhat, Foundry and Preview gates |
| P68-T19 | done | P68-T11 | Native pgx database access ADR, pools, sqlc and transaction infrastructure | pool configuration, cancellation cleanup, generation and routing regressions |
| P68-T20 | done | P68-T19 | All read paths use typed pgx queries with exact nullable values and bounded keyset scans | reader routing, repeatable snapshots, PostgreSQL and race |
| P68-T21 | done | P68-T19 | All writes use typed pgx queries with unchanged fencing and commit semantics | lease loss, rollback, reorg and ambiguous commit regressions |
| P68-T22 | blocked | P68-T19 | Native session locks, migrations, partition DDL, CLI and startup checks | physical connection disposal, schema, runtime parity |
| P68-T23 | blocked | P68-T20, P68-T21, P68-T22 | Native test boundaries, pool metrics, source enforcement and documentation | source, docs, plan and pool telemetry tests |
| P68-T24 | blocked | P68-T12, P68-T13, P68-T14, P68-T23 | Aggregate six-fix and full native pgx acceptance | common, integration/race, browser, schema, runtime, Hardhat, Foundry, Preview and benchmarks |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [ ] P68-T12–T14 and P68-T19–T24: all six review fixes and full native pgx migration pass their targeted and aggregate acceptance gates.

- [x] P68-T11: cold billing-page rendering awaits asynchronous React work;
      Helm 3/4 both enforce the additional-egress schema and template rejection.

- [x] P68-T10: pinned Oxlint/Oxfmt gates replace Biome with unchanged length limits,
      explicit cyclomatic complexity policy, and passing Web acceptance.

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
      keys, and monolith/split behavior remained unchanged through P68-T11.
      P68-T13 subsequently adds the approved home event-version contract.

## Current Blockers

- P68-T12 and P68-T22: the current production image cannot be built because
  Docker Hub frontend/token access fails (TLS timeout, then connection reset).
  Runtime parity and schema-image acceptance are unverified. Restore that
  external access and pass `make test-schema-e2e test-runtime-e2e` against the
  current working tree to clear these items.
- P68-T23 remains dependency-blocked by P68-T22. Native fixtures, telemetry,
  source enforcement and documentation are implemented and their local checks
  pass; close this work item only after the lifecycle dependency is accepted.
- P68-T24 remains blocked by P68-T12/P68-T23 and the same production-image
  prerequisite. Clear with `make check`, schema/runtime, Hardhat, Foundry and
  Preview Metadata acceptance on the current image. P70/P73 remain independent.


## Evidence

- Current replacement evidence is recorded in [native pgx acceptance](pgx-native-acceptance.md).
  P68-T12/T22 targeted lease, supervisor, native session disposal and pool-close
  deadline regressions pass; their production runtime/schema gates remain blocked.
  P68-T20/T21 remove every manual production query execution outside migration
  and partition DDL. Exact decimal/NULL contracts, bounded snapshot scans,
  generated names/cardinalities and native transaction semantics are implemented.
  Generation, Go/Web lint, unit/race, security/license, Compose and Helm checks
  pass. The final PostgreSQL ordinary run passes all six packages (core 225.143s);
  final PostgreSQL race also passes all six packages (core 229.401s).
  P68-T20/T21 are complete. P68-T13 and P68-T14 also pass all 29 real Chrome tests,
  including expired-cursor 400 recovery, single-connection ownership and
  UserOperation head/reorg handling. Home tests cover replica switching, late
  responses, HTTP-derived version floors and QueryClient isolation. They are done;
  P68 itself remains blocked until the production-image gates above pass.
  Serial core benchmarks and their timing variability are recorded in the linked
  acceptance document; no reference-capacity or latency-improvement claim is made.

- P68-T21 claimed after P68-T19; generated write contracts must preserve
  affected-row fences, original transaction scopes and unknown-commit handling.
  Read/write contracts are now implemented; final acceptance is recorded above.

- P68-T22 claimed after finding that native pool Close waits for borrowed
  connections. Pool teardown must share the supervisor deadline so a timed-out
  service cannot hold process exit open after readiness has been withdrawn.

- P68-T19: native pools, transaction cleanup and pgx test fixtures replace the
  stdlib bridge. `go build ./...`, native database/observability/maintenance
  race regressions, pool configuration tests, and focused query/catalog/state/
  store/Etherscan/enrichment unit suites pass. The owned PostgreSQL 18 runner
  passes migration idempotence, generated identity reads, concurrent role
  binding and home snapshot/reorg tests; a second run passes single-connection
  cancellation cleanup, physical lock-session disposal, writer/reader session
  boundaries, reader startup identity/schema checks and compiler-cache locks.
  Owned containers and volumes were removed. `make generate-check` and
  `make source-check docs-check plan-check` pass. This is foundation evidence,
  not full typed-query or aggregate acceptance.
- P68-T20 claimed after P68-T19 foundation acceptance. Full read-query migration
  must preserve native NULL semantics and bound every former streaming scan.

- P68-T19 claimed for the approved native pgx replacement. The previous dirty
  checkout is preserved unchanged; implementation continues in the isolated
  `codex/pgx-native` worktree from `a805559556025c3f88699a2fb5bc51d137be4d0a`.
- P68-T15, P68-T16, P68-T17 and P68-T18 are dropped because their database/sql architecture was rejected.
  P68-T19–T24 replace them. Earlier targeted results describe the previous
  checkout only; the replacement must rerun its own acceptance.

- Historical abandoned-attempt note: P68-T15–T17 resumed after the first handoff to repair shared projections and nullable finality mappings. This describes the preserved original checkout, not the native replacement.

- Historical abandoned-attempt note: P68-T12–T14 previously passed targeted worker/home race and four Web suites (26 tests). Those results did not close the old P68-T18; current replacement evidence is recorded above.

- P68-T12 claimed on a clean working tree for the accepted six-issue remediation plan.

- P68-T11 reproduces both failures from [PR 86 CI run 35361562536](https://github.com/islishude/etherview/actions/runs/35361562536)
  at `d8a21542af677c577fcc981e293e21cf43fd4400`. The first billing-page
  render suspends while loading the nested Account component; a synchronous
  Testing Library render leaves React's asynchronous `act` work unawaited and
  the Account heading times out. Awaiting the render inside asynchronous `act`
  fixes the original regression without changing dependency versions, page
  assertions, or timeouts. CI's Helm latest-release lookup failed and selected
  its v3.18.4 fallback, whose dotted schema paths did not match the Helm 4
  JSON-pointer assertion. The regression normalizes separators and still
  requires rejection at the additional-egress port path; the independent
  template TCP/443 rejection remains required. Focused Vitest passes 21 tests;
  `make web-lint web-test` passes 39 files / 370 tests and eight script tests.
  `make helm-check` passes with Helm 3.18.4 and 4.3.0; `make deployment-check`,
  `make docs-check plan-check`, and `git diff --check` pass locally on macOS.
  `make generate-check test-e2e` passes generated-contract consistency, asset
  budgets, and all 27 embedded Chrome tests. Remote CI has not been rerun with
  these changes.

- P68-T10 replaces Biome with exact `oxlint@1.83.0` and `oxfmt@0.68.0`
  lockfile pins. `web-lint` retains TypeScript and explicit unused-code/Hook
  checks, adds a read-only formatting gate, and keeps production file/function
  limits at 1,400/400 and test limits at 2,500/1,000 (blank lines excluded,
  comments counted, IIFEs exempt). Classic cyclomatic complexity is capped at
  150 against the measured pre-refactor maximum of 142, with tests exempt;
  this is not an equivalence claim for Biome's prior cognitive metric of 75.
  Transaction presentation and contract proxy facts are split by responsibility;
  wallet operation callbacks retain their original session fences in an internal
  Hook. New Hook diagnostics are resolved through stable defaults, precise
  callback dependencies, captured focus targets, and an explicit ECharts alias.
  Five executable tooling-policy regressions exercise positive/negative limits,
  Hook and unused-code failures, test overrides, generated exclusions, and
  formatting idempotence. The QR regression also asserts restored opener focus.
  `make web-lint web-test generate-check test-e2e` passes: 39 Vitest files / 370
  tests, eight Node script tests, generated-contract consistency, production
  asset budgets, and 27 embedded Chromium tests. `make docs-check plan-check`
  and `git diff --check` pass. A repeated format leaves tracked Web bytes
  unchanged, including the lockfile and generated API types. `npm audit
  --audit-level=high` reports zero vulnerabilities; the maintained production
  license allowlist passes, and the five newly installed tool/runtime packages
  have MIT licenses (all added platform-package lock entries also declare MIT).
  A broader diagnostic scan of all development packages finds the pre-existing
  `chownr@3.0.0` BlueOak-1.0.0 license outside the production allowlist; no
  dependency version or license policy was changed to suppress it. Evidence is
  local macOS validation; remote CI and Linux tool binaries were not rerun.

- P68-T09 diagnoses GitHub Actions run 32549443570 job 96973645743 as a test
  synchronization race: receiving the active subscriber's update did not prove
  that the map-ordered fanout had finished processing the still-buffered slow
  subscriber. The regression now acquires the feed mutex before inspecting the
  post-fanout subscriber count and closed channel, preserving production
  semantics while removing the scheduling assumption. Its race-focused case
  passes 1,000 consecutive runs, the complete HTTP API race package passes 20
  consecutive runs, and the exact CI command `make install-lint-tools
  toolchain-check plan-check generate-check lint test test-race` passes with
  writable repository-specific caches.
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
