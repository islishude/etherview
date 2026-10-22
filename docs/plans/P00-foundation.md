# P00 — Foundation

Status: `done`

## Outcome

The repository has enforceable plan governance, a reproducible Go/React
toolchain, a single role-aware CLI, validated configuration, native pgx
persistence, typed SQL, neutral package and API contracts, and a deterministic embedded-SPA build.

## References

- [ADR-0050](../decisions/ADR-0050-native-pgx-and-typed-queries.md)
- [Architecture](../architecture/overview.md)
- [Development](../development.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P00-T03 | done | — | Single CLI, role model, YAML/env config, validation, doctor command | Go unit tests and CLI smoke tests |
| P00-T04 | done | — | Native pgx pools, typed sqlc read/write and transaction boundaries, migrations/session locks, OpenAPI generation, and enforced neutral package contracts | generation/source/import-graph, exact codec, routing/snapshot, cancellation/lock/commit, PostgreSQL/race, schema and production parity tests |
| P00-T05 | done | — | React/Vite shell and deterministic `go:embed` asset serving | frontend build and deep-link test |

## Acceptance

- [x] A clean checkout can run the documented common checks.
- [x] `etherview serve --roles=all` and split role selections resolve the same
      component graph.
- [x] Invalid chain/RPC/database/security configuration fails before serving.
- [x] The Go binary serves embedded hashed assets and SPA deep links without a
      Node process.
- [x] Plan/document drift fails CI.
- [x] SQL execution and package ownership are enforced at generated boundaries.
- [x] Native pgx retains exact values, snapshots, locks and atomic publication.
- [x] Public-query/error/cursor and ABI/proxy/stage contracts are independent of
      transport/worker hubs; oversized production/test scenarios are split and
      file/function boundaries are enforced.
- [x] Native pgx/sqlc retain writer/reader routing, isolation, exact nullable
      numeric/UUID/JSONB/array values and bounded keyset scans. Snapshots close
      before external calls; migration and validated partition DDL are the only
      handwritten-SQL executors.
- [x] Cancellation explicitly rolls back with bounded cleanup; ambiguous commits
      are confirmed without retrying them. Uncertain session locks discard and
      physically close the acquired connection. Pool closure respects the shared
      shutdown deadline, including stuck borrowers; native metrics remain truthful.

## Current Blockers

None. P60/P70 retain independent release blockers.

## Evidence

P00-T04 includes typed SQL/native pgx and neutral contract acceptance: exact
values, routing, snapshots, lease/commit fencing, cancellation, session-lock
connection disposal and import/source boundaries passed focused normal/race,
PostgreSQL and generation checks. The development guide retains revision-bound
benchmarks; P30-T35 retains production-image/native architecture evidence.

Retained completed milestones: P00-T03, P00-T04, P00-T05.

CLI/configuration unit and smoke tests, PostgreSQL migration and generated
API contracts, and frontend build/deep-link checks passed for the initial
foundation. Toolchain, lint and governance maintenance now belongs to the
normal development workflow, not additional foundation milestones.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P00-foundation.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
