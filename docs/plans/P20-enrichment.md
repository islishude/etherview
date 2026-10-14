# P20 — Enrichment

Status: `done`

## Outcome

Core facts are asynchronously enriched with ABI decoding, contract/proxy
versions, ERC-20/721/1155 activity, normalized call traces, state reconciliation,
search documents, and rollup statistics without delaying core readiness.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0008: Versioned token observations and exact state reconciliation](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0011: Snapshot search, statistics, and bounded adapters](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0012: Lease-fenced derived publication](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P20-T01 | done | P10 | Versioned enrichment stages, durable jobs, leases, and idempotency | replay/crash tests |
| P20-T02 | done | P20-T01 | ABI selector/event/error decoding and confidence model | ABI fixture/fuzz tests |
| P20-T03 | done | P20-T02 | Contract creation and EIP-1167/1967/beacon proxy versioning | proxy-upgrade tests |
| P20-T04 | done | P20-T01 | ERC-20/721/1155 discovery, transfers, ownership, balances, supply | standard/nonstandard token tests |
| P20-T05 | done | P20-T01 | Geth callTracer and trace_* adapters with normalized call frames | trace/revert tests |
| P20-T06 | done | P20-T03, P20-T04, P20-T05 | Search, name/price adapters, and aggregate statistics | reorg, adapter, and consistency tests |
| P20-T07 | done | P20-T01, P20-T03, P20-T05 | Reattach-safe durable replay generations and one retry budget | detach/reattach and lease-race tests |
| P20-T08 | done | P20-T02, P20-T04, P20-T05 | Global ABI/Trace resource budgets and builtin/large-batch boundaries | adversarial budget and fixture tests |
| P20-T09 | done | P20-T04 | Immutable exact NFT state and typed token state-capability failures | conflict and capability tests |
| P20-T10 | done | P20-T01, P20-T07 | Lease-fenced atomic publication of derived output, stage result, journal, and job generation | stale-worker and publication-window tests |
| P20-T11 | done | P20-T05, P20-T10 | Exact bounded transaction state differences with independent capability state | malformed, limit, replay, and reorg tests |
| P20-T13 | done | P20-T03, P20-T07, P20-T10, P20-T11 | OpenZeppelin 5.6.1-aware `proxy@2`, dependent `abi@2`, shared Beacon observations, canonical upgrade/initialization facts, and replay-safe coverage evidence | proxy/ABI unit, PostgreSQL integration/race, reorg, replay, and immutable binding-source tests |
| P20-T14 | done | P20-T05, P20-T11 | Block-hash-batched Geth call and state-difference tracing without per-transaction debug fallback | block envelope, fallback, budget, PostgreSQL, runtime, and common-gate tests |
| P20-T15 | done | P20-T09 | Batch uncached exact ERC-721/ERC-1155 balance calls without changing snapshot or persistence semantics | mixed-batch, failure, cache, and reorg tests |
| P20-T16 | done | P20-T15 | Persist immutable exact-block ERC-20 address-holdings observations and batch only cache misses | migration, cache, conflict, and reorg tests |

## Acceptance

- [x] Durable stage work is bounded, restartable and generation/lease-fenced.
- [x] ABI, proxy, token, trace and state-difference facts retain exact block identity.
- [x] Canonical publication is atomic; reorg replay retains orphan observations.
- [x] Exact balance observations reject conflicts and never reuse orphan state.
- [x] Capability loss and hostile input fail explicitly without corrupting readiness.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P20-T01, P20-T02, P20-T03, P20-T04, P20-T05, P20-T06,
P20-T07, P20-T08, P20-T09, P20-T10, P20-T11, P20-T13, P20-T14, P20-T15, P20-T16.

Stage, ABI, token, proxy, trace and state-difference unit/fuzz/race checks
and PostgreSQL integration passed at the delivery revisions. Tests covered
lease-fenced atomic publication, detach/reattach replay, exact immutable NFT
observations, hostile decoding budgets and historical proxy bindings.

Later block-batched trace and exact-balance work passed `make check`,
`make test-integration-race`, generation/source gates and schema acceptance.
Fixtures prove one-endpoint batches, cache-miss-only reconciliation, conflict
rejection and no orphan-cache reuse. Current stage versions and contracts live
in the architecture and ADRs.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P20-enrichment.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
