# P10 — Indexing

Status: `done`

## Outcome

The explorer ingests standard execution-layer history from a configured start
block through the live head, maintains canonical/safe/finalized state, retains
orphan facts, survives restarts, and repairs gaps without depending on optional
enrichment. Traversal, backfill, and persistence use explicit work budgets
and retain raw-first input, exact block identity, and atomic publication.

## References

- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0019](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [Architecture](../architecture/overview.md)
- [ADR-0002: Identity-bound repair](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md)
- [ADR-0006: Durable coverage and live priority](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md)
- [ADR-0007: Derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0001: Modular roles and PostgreSQL truth](../decisions/ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0022: Go-ethereum and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status | Depends on       | Deliverable                                                                                                                                                                         | Verification                                                                                                                                       |
| ------- | ------ | ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| P10-T01 | done   | P00              | Purpose-aware RPC pool, chain/genesis/capability probes, and cancellation-safe rate reservations                                                                                    | fake-RPC contracts and deterministic cancellation/rate tests                                                                                       |
| P10-T02 | done   | P00              | Hash-keyed core schema, canonical mapping, range partitions, checkpoints                                                                                                            | PostgreSQL integration tests                                                                                                                       |
| P10-T03 | done   | P10-T01, P10-T02 | Sticky-endpoint ingestion, one owned immutable validated bundle, bounded relation-batch persistence, authenticated Genesis import, protocol compatibility, and atomic checkpointing | hostile wire/storage, source-mutation/race, block fixtures/fallback, generated SQL, conflict/reorg/partition, benchmarks, and runtime parity tests |
| P10-T04 | done   | P10-T03          | Parallel backfill with aggregate raw-byte/row budgets, recoverable subsegments under one range lease, live-head priority, polling reconciliation, and coverage status               | restart/gap, cancellation, lease-expiry, partial-progress, race, and PostgreSQL tests                                                              |
| P10-T05 | done   | P10-T03          | Bounded common-ancestor reorg, pre-work depth/finality rejection, finality ancestry, orphan retention, and rollback journal                                                         | counting-source/repository, single/multi/finalized reorg, race, and PostgreSQL tests                                                               |
| P10-T06 | done   | P10-T04, P10-T05 | Repair/reindex tooling, history-pruning detection, audit records                                                                                                                    | CLI integration tests                                                                                                                              |

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
- [x] Over-depth or finalized-crossing forks are rejected before further parent
      RPC, canonical-row traversal, or attacker-sized branch allocation.
- [x] Backfill bounds aggregate raw bytes and relational rows; committed
      subsegments remain restart-safe under the same durable range lease.
- [x] Persistence owns one fully decoded and authenticated immutable bundle;
      later source mutation cannot change accepted facts or force repeated decoding.
- [x] Bounded batches cover identities, inclusions, receipts, logs, withdrawals,
      canonical mappings, and outbox rows without changing chain-lock, coverage,
      checkpoint, or publication atomicity.
- [x] Canceled RPC waiters cannot reserve future rate slots or delay later work.

- [x] Authenticated local/remote Genesis import matches both the canonical
      block-zero hash and state root, publishes allocation/predeploy observations
      atomically, and distinguishes unavailable input from a proven empty allocation.
- [x] Reviewed go-ethereum protocol types preserve raw-input, persistence and
      public-contract boundaries. Amsterdam slot/BAL-hash presence, receipt/header
      gas and contract-size compatibility follow ADR-0052 with hostile-input bounds;
      no BAL body fetch or Engine API dependency is introduced.

## Current Blockers

None. P60/P70 retain independent release blockers.

## Evidence

P10-T03 retains authenticated Genesis root/persistence/security and reviewed
protocol compatibility acceptance. Amsterdam header/gas validation passed local
Go/race, PostgreSQL and real-Geth Preview checks at its delivery revision;
P30-T35 owns the shared production-topology evidence.

Retained completed milestones: P10-T01, P10-T02, P10-T03, P10-T04, P10-T05, P10-T06.

RPC, indexer, store, syncer and maintenance race tests passed. PostgreSQL 18
normal/race integration covered atomic hash-keyed ingestion, restart/gap
recovery, finality and orphan retention, partition provisioning, and the real
CLI repair/reindex worker path. Hostile receipt/batch inputs reject before
publication; pruned history remains an explicit capability failure.

Bounded traversal/backfill and cancellation-safe RPC reservations passed
counting-source, cancellation, lease-expiry, partial-progress, and race checks.
Immutable bundles and batched writes passed hostile wire/storage, mutation,
conflict/reorg/partition, generated SQL, PostgreSQL, and runtime-parity coverage.
Reproducible chainbundle decode/allocation and core-write benchmarks provide
local regression baselines; they do not satisfy P70-T04 reference capacity.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P10-indexing.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
