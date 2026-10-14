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
| P75-T12 | done | P75-T02, P75-T03, P75-T04, P75-T05, P75-T06, P75-T07, P75-T08, P75-T09, P75-T10, P75-T11 | Complete aggregate security, race, generation, integration, browser, deployment, and monolith/split production-topology acceptance on the final tree | `make check`, explicit integration/race/browser/runtime/Hardhat/Foundry gates |

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

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P75-T01, P75-T02, P75-T03, P75-T04, P75-T05, P75-T06,
P75-T07, P75-T08, P75-T09, P75-T10, P75-T11, P75-T12.

Focused unit/race, hostile-input, PostgreSQL query-plan and benchmark checks
passed for bounded traversal/backfill, immutable bundles, batched persistence,
narrow reads, rate reservations, Web delivery and package boundaries.
The final `make check`, integration/race, browser, schema, runtime/x402,
Hardhat and Foundry production gates passed. Short local benchmark results
established regression coverage, not the P70 reference-capacity target.
The retired accountless payment runtime is removed; retained historical rows
are audit-only and do not create prepaid credit.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P75-runtime-performance-hardening.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
