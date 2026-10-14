# P10 — Indexing

Status: `done`

## Outcome

The explorer ingests standard execution-layer history from a configured start
block through the live head, maintains canonical/safe/finalized state, retains
orphan facts, survives restarts, and repairs gaps without depending on optional
enrichment.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0002: Identity-bound repair](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md)
- [ADR-0006: Durable coverage and live priority](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md)
- [ADR-0007: Derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P10-T01 | done | P00 | Purpose-aware RPC pool and chain/genesis/capability probes | fake-RPC contract tests |
| P10-T02 | done | P00 | Hash-keyed core schema, canonical mapping, range partitions, checkpoints | PostgreSQL integration tests |
| P10-T03 | done | P10-T01, P10-T02 | Sticky-endpoint block/receipt ingestion and atomic checkpointing | block fixture and fallback tests |
| P10-T04 | done | P10-T03 | Parallel backfill, live-head priority, polling reconciliation, coverage status | restart/gap tests |
| P10-T05 | done | P10-T03 | Common-ancestor reorg, finality ancestry, orphan retention, rollback journal | single/multi/finalized reorg tests |
| P10-T06 | done | P10-T04, P10-T05 | Repair/reindex tooling, history-pruning detection, audit records | CLI integration tests |

## Acceptance

- [x] Genesis/configured-start to head has no canonical height gaps.
- [x] Transaction and receipt counts/hashes/indexes are validated before commit.
- [x] WebSocket loss, timeout, throttling, stale endpoints, and receipt fallback
      have deterministic recovery.
- [x] Type 0–4 transactions, withdrawals, blob-era fields, EIP-7702, and
      fully authenticated historical PoW fields remain ingestible; transaction
      types unsupported by the selected go-ethereum version fail permanently
      and atomically before persistence or coverage advancement.
- [x] Reorgs roll back derived journals; a finalized-crossing reorg stops and
      alerts instead of silently rewriting history.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P10-T01, P10-T02, P10-T03, P10-T04, P10-T05, P10-T06.

RPC, indexer, store, syncer and maintenance race tests passed. PostgreSQL 18
normal/race integration covered atomic hash-keyed ingestion, restart/gap
recovery, finality and orphan retention, partition provisioning, and the real
CLI repair/reindex worker path. Hostile receipt/batch inputs reject before
publication; pruned history remains an explicit capability failure.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P10-indexing.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
