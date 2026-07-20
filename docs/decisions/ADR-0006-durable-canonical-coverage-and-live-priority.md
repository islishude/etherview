# ADR-0006: Durable canonical coverage and live-head priority

Status: accepted

## Context

A canonical block at a high height does not prove that the explorer has every
block from its configured start. Treating the maximum canonical height as the
indexed checkpoint can make a live-head observation falsely mark a sparse
database ready. A single polling loop also lets slow historical RPC calls hold
WebSocket wakes and head reconciliation behind an entire backfill batch.

In-memory range queues are insufficient because a process can stop after
claiming or committing a range, and split sync replicas must not duplicate
ownership without a durable coordination rule.

## Decision

- PostgreSQL stores the immutable configured start block and normalized,
  non-overlapping, non-adjacent canonical coverage ranges for each chain.
- A core checkpoint is derived transactionally only from the coverage range
  containing the configured start. The highest covered block is tracked
  independently; an isolated live range never advances contiguous readiness.
- Canonical segment commits validate internal ancestry and any immediately
  adjacent lower and upper canonical block before changing facts, mappings,
  coverage, checkpoint, outbox, or events. A boundary mismatch rolls back the
  complete segment.
- The authoritative live lane polls through the head RPC purpose and consumes
  WebSocket wakes independently of historical work. It may persist a sparse
  live range so recent blocks remain observable while backfill continues.
- Historical workers derive missing ranges from PostgreSQL coverage, claim
  bounded PostgreSQL leases, fetch through the history RPC purpose, and commit
  idempotent segments. Coverage, not a queued job row, remains the work truth;
  an expired lease can be reclaimed after a crash, including a crash after the
  segment commit but before lease completion.
- Sparse shallow live reorganizations always detach the complete highest
  isolated range under the same finalized-depth and orphan-retention rules.
  If closing the gap exposes a lower fork, the same transaction also detaches
  every covered canonical block above the discovered ancestor and attaches the
  authoritative continuous branch. Connected coverage continues through the
  ordinary common-ancestor reorg path.
- Runtime status distinguishes the latest observed head, the contiguous
  indexed checkpoint, the highest covered block, and whether backfill is
  complete through the latest known head.
- Safe and finalized markers are persisted only after resolving their exact
  canonical hashes and walking every canonical parent from the current tip to
  the lowest requested marker. A gap or broken parent link rejects the entire
  finality update.
- A finalized-crossing, over-depth, no-common-ancestor, or
  source-inconsistent fork cancels both live and historical lanes. The service
  records a stable `etherview_sync_halted{reason}` gauge and then remains alive
  until operator cancellation, keeping the safety halt observable while
  repair and restart are coordinated.

## Consequences

Backfill ranges may finish out of order without overstating readiness. Multiple
sync workers and restarts can safely converge on the same normalized coverage,
while duplicate post-expiry fetches remain harmless. PostgreSQL receives small
coverage and lease writes per historical segment. Operators can diagnose a
healthy live lane with incomplete history instead of seeing one ambiguous
"tip" value.

Finality cannot bless a sparse or internally inconsistent canonical mapping.
Fatal fork-choice violations stop all core progress without making the process
disappear from monitoring; recovery remains an explicit operator action.
