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
- [x] Type 0–4 transactions, unknown types, withdrawals, blob-era fields,
      EIP-7702, and historical PoW fields remain ingestible.
- [x] Reorgs roll back derived journals; a finalized-crossing reorg stops and
      alerts instead of silently rewriting history.

## Current Blockers

None.

## Evidence

- P10-T01: `go test -race ./internal/ethrpc` passes identity, purpose,
  capability, pruning, cooldown, sticky endpoint, receipt fallback, malformed
  batch, throttling, and credential-redaction cases.
- P10-T02: `go test -race ./internal/store` and PostgreSQL 18 integration pass
  hash-keyed canonical/orphan facts, configured-start coverage, checkpoints,
  leases, and concurrent fixed 1,000,000-block partition provisioning with
  atomic DEFAULT evacuation and typed recovery failures.
- P10-T03: `go test -race ./internal/indexer ./internal/ethrpc` passes sticky
  block/receipt acquisition and pre-commit bundle validation. PostgreSQL 18
  `TestPostgresCoreProtocolRoundTripAndReceiptMismatchAtomicity` persists types
  0–4 plus unknown type 127, blob/EIP-7702/PoW/withdrawal fields, and proves a
  mismatched receipt rolls the whole bundle back.
- P10-T04: `go test -race ./internal/syncer ./internal/store` passes independent
  live/backfill lanes, live priority during blocked history, authoritative poll
  after missed WebSocket wakes, durable coverage islands, restart-safe leases,
  moving heads, and gap closure. PostgreSQL coverage/lease restart integration
  also passes.
- P10-T05: `go test -race ./internal/indexer ./internal/store ./internal/syncer`
  and PostgreSQL 18 derived-journal integration pass single/multi-block reorg,
  orphan retention, stale jobs, atomic failure rollback, continuous finality
  ancestry, and fatal finalized/depth/source-inconsistency halts. The halt
  remains Prometheus-visible until operator cancellation.
- P10-T06: `go test -race ./internal/maintenance ./internal/cli` and PostgreSQL
  18 `TestCLIMaintenanceWorkerExecutesRepairAndReindex` pass the real CLI to
  durable request to worker path, terminal audit fields, identity-bound repair,
  and active-lease-preserving reindex. Pruned history is a typed RPC capability
  state and repair never invokes ordinary fork-choice `Apply`.
- P10-T01/P10-T02/P10-T03/P10-T04/P10-T05/P10-T06 commit/PR: none created
  because the repository has no `HEAD` and this task did not authorize a commit
  or pull request; evidence is bound to the current working tree.
- Full PostgreSQL 18 integration passes both normal and race modes:
  `go test -tags=integration ./internal/integration` and
  `go test -race -tags=integration ./internal/integration`.
