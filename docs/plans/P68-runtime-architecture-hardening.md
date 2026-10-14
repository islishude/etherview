# P68 — Runtime and Architecture Hardening

Status: `done`

## Outcome

Make long-lived API streams shutdown-safe and resistant to replay/cache
head-of-line blocking, then replace the repository's largest SQL, runtime,
HTTP, and Web service locators with explicit generated or modular boundaries.
The home snapshot contract adds an event-version fence. Page routes, configuration
keys, and the fresh-database schema remain unchanged.

## References

- [ADR-0050: Native pgx and typed queries](../decisions/ADR-0050-native-pgx-and-typed-queries.md)
- [Native pgx acceptance record](../development.md#native-pgx-acceptance-record)
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
| P68-T12 | done | P68-T05 | Explicit verification worker repository and task-local lease-loss recovery with fatal compiler error preservation | worker lifecycle, heartbeat, race, runtime parity |
| P68-T13 | done | P68-T05 | Version-fenced home snapshot API with bounded cancellation-safe waiting | OpenAPI, HTTP concurrency, generation and browser tests |
| P68-T14 | done | P68-T05 | Query-local chain event policies, recoverable single SSE connection, and UserOperation reorg refresh | Vitest, browser reconnect and reorg regressions |
| P68-T19 | done | P68-T05 | Native pgx database access ADR, pools, sqlc and transaction infrastructure | pool configuration, cancellation cleanup, generation and routing regressions |
| P68-T20 | done | P68-T19 | All read paths use typed pgx queries with exact nullable values and bounded keyset scans | reader routing, repeatable snapshots, PostgreSQL and race |
| P68-T21 | done | P68-T19 | All writes use typed pgx queries with unchanged fencing and commit semantics | lease loss, rollback, reorg and ambiguous commit regressions |
| P68-T22 | done | P68-T19 | Native session locks, migrations, partition DDL, CLI and startup checks | physical connection disposal, schema, runtime parity |
| P68-T23 | done | P68-T20, P68-T21, P68-T22 | Native test boundaries, pool metrics, source enforcement and documentation | source, docs, plan and pool telemetry tests |
| P68-T24 | done | P68-T12, P68-T13, P68-T14, P68-T23, P68-T25 | Aggregate six-fix and full native pgx acceptance | common, integration/race, browser, schema, runtime, Hardhat, Foundry, Preview and benchmarks |
| P68-T25 | done | P68-T23 | Local Kubo Preview gateway and streaming single-file IPFS CLI | CLI unit/race, Compose/TLS isolation, offline Kubo roundtrip and full Preview acceptance |
| P68-T30 | done | P68-T25 | Replace Preview nginx with an independent Go HTTPS IPFS gateway | Gateway unit/race, lint, Compose/docs/plan and real-Kubo Preview acceptance |

## Acceptance

- [x] SQL execution and package ownership are enforced at generated boundaries.
- [x] Runtime builders and capability modules fail closed on incomplete dependencies.
- [x] SSE, shutdown and worker leases preserve bounded cancellation and recovery.
- [x] Native pgx retains exact values, snapshots, locks and atomic publication.
- [x] Home snapshots and browser replay are version-fenced and reorg-aware.
- [x] Local Kubo, streaming IPFS CLI and the independent HTTPS gateway pass acceptance.
- [x] Applicable schema, production topology and native architecture gates completed.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P68-T01, P68-T02, P68-T03, P68-T04, P68-T05, P68-T12,
P68-T13, P68-T14, P68-T19, P68-T20, P68-T21, P68-T22, P68-T23, P68-T24, P68-T25,
P68-T30.

SQL/source ownership, typed runtime builders, HTTP shutdown/SSE, worker
lease recovery and version-fenced home state passed focused normal/race,
generation, PostgreSQL and browser gates. Native pgx read/write, cancellation,
session-lock disposal and topology acceptance passed; the development guide
retains the bounded benchmark and native pgx acceptance summary.

[PR 92 CI run 35474508539](https://github.com/islishude/etherview/actions/runs/35474508539)
at `aebb61fee2783e3f6bb8810a81296b05c142bce6` passed all 11 jobs, including
production schema/runtime and native AMD64/ARM64 Hardhat/Foundry. Later local
Kubo acceptance replaced the failed public-gateway prerequisite: the exact
`make test-preview-metadata` passed two canonical metadata versions, one
attempt each, gateway/worker restart and immutable content checks. That local
replacement does not establish public-IPFS availability.

The independent Go HTTPS IPFS gateway passed unit/race, Compose and real-Kubo
Preview checks. Native pgx and gateway evidence are revision-specific; the
historical CI run above does not cover later gateway changes.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P68-runtime-architecture-hardening.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
