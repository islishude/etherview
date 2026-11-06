# Durable jobs and publication

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns durable jobs, stage visibility and event publication.
See [operator procedures](../operations/observability.md) and
[acceptance requirements](../testing/runtime.md) for execution and validation.

- [Lease-fenced stage publication](#lease-fenced-stage-publication)
- [Runtime events and cache invalidation](#runtime-events-and-cache-invalidation)
- [Statistics and UserOperations](#statistics-and-useroperations)

## Lease-fenced stage publication

- Proxy/code, ABI, token, statistics, normalized-trace, state-difference, and
  UserOperation production processors atomically persist their block-local
  output, exact durable-job
  generation marker, stage result, one versioned `stage@version` journal, and
  successful lease completion in one PostgreSQL transaction.
  Journal canonicality is derived from the exact chain/height/hash mapping;
  detach and attach transactions retain the output while toggling it and the
  journal together. Readers require the canonical mapping, row flag, and the
  exact lease-published result; see
  [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md) and
  [ADR-0007](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md).

- Durable enrichment delivery uses the same lease fence. The successful
  publisher compare-and-set requires the exact unexpired token and requested,
  claimed, leased, and completed generation relationship; stale worker output
  rolls back. A pending replay discards the writer savepoint, consumes the old
  generation, clears its publication, and queues the new one atomically.
  `failed` and `unavailable` outcomes have no journal but change the job
  terminal state and upsert the exact job/generation result in one transaction.
  `Finish` and `Retry` retry a PostgreSQL-aborted deadlock or serialization
  transaction at most five times with cancellable jittered backoff, rechecking
  the original lease/generation each time. Ambiguous commits and lease loss
  are never retried. These transaction retries do not consume job attempts.
  Retry exhaustion and crash-expired exhaustion use the same result contract, and
  `durable_jobs.max_attempts` is the only attempt limit interpreted by workers.
  Replay reuses the immutable idempotency key and records a unique source key
  against monotonic requested, claimed, leased, and completed generations.
  Unowned jobs are reset immediately. An active lease is never stolen: its
  `Finish`/`Retry` transition consumes the pending generation, or the first
  claim after expiry clears the previous exact result, journal, and ABI output
  in the same transaction before it exposes the new-generation lease.

- Proxy interaction coverage refreshes run as deferred constraint triggers at
  commit, after publication and replay have acquired their per-job locks. This
  keeps coverage atomic with its source rows without holding the chain coverage
  lock while a batch replay waits for another publisher's job lock.

- `published_block_stage_results` is the only readiness relation. It excludes
  direct fixture rows, mismatched or superseded generations, active leases,
  result mismatches, and `stale_canonical_skipped` audit rows. A same-hash
  reattach therefore remains explicitly incomplete until its canonical outbox
  generation is dispatched and successfully republished. Etherscan
  enrichment prechecks and their data reads share one read-only repeatable-read
  snapshot.

- Core canonical outbox rows also carry a generation. A repeated attach of the
  same block hash increments and reopens the existing outbox identity, so a
  hash that was detached, terminally skipped as stale, and reattached receives
  a new enrichment generation. A delayed orphan wake for the now-canonical
  hash is acknowledged as stale rather than retried forever.

- Derived journal payloads contain only controlled relation-level canonicality
  transitions. They do not contain untrusted RPC data and do not claim storage,
  rollback, or replay of opcode/raw traces; trace journaling covers only the
  normalized call tree.

## Runtime events and cache invalidation

- The upstream head, indexed position, readiness, and bounded head/reorg/status
  replay window are PostgreSQL facts. Sync replicas elect one short-lived
  reporter for aggregate status events, so lagging replicas cannot move the
  snapshot backward and backfill-worker count cannot consume the replay
  window. Each API replica tails that ledger independently; see
  [ADR-0004](../decisions/ADR-0004-durable-runtime-status-and-events.md).

- A durable subscriber is registered provisionally while PostgreSQL replay and
  cache invalidation run outside the fanout mutex. Live events published during
  replay are buffered and merged by ID before the subscription becomes active;
  bounded overflow returns replay unavailable instead of delaying established
  subscribers.

- If an API query cache is configured, its idempotent invalidator runs before a
  tailed event advances the replica cursor or reaches SSE subscribers. A failed
  invalidation retries the same PostgreSQL event. Without a configured cache,
  API responses are `no-store` and every query reads the authoritative source.
  Optional Redis-backed caches fail over by disabling or bypassing the cache;
  Redis loss is not itself an invalidation failure.

## Statistics and UserOperations

- `stats@4` derives intervals only from the exact canonical parent. The exact
  configured indexing start has null interval and TPS; every later block
  requires a positive timestamp delta. A sparse live block waits for its exact
  canonical parent in the durable queue without consuming attempts; the
  configured start and orphan cleanup remain claimable. Aggregate TPS divides
  transactions by total known interval rather than averaging block rates.
  A block without blob transactions has null blob base fee and zero blob burn,
  while receipt blob gas without the required header inputs is a permanent
  inconsistency; see
  [ADR-0011](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md).

- Feature-gated `userop@1` accepts only direct successful calls to an explicit
  version-and-range-bound EntryPoint registry. It strictly decodes and
  canonically re-encodes `handleOps`/`handleAggregatedOps`, correlates every
  operation with its ordered EntryPoint outcome and lifecycle events, and
  publishes operations, events, participants, and configuration-digest-bound
  continuous coverage atomically. Malformed or contradictory bundles publish
  nothing; pending Bundler mempools are outside this boundary. See
  [ADR-0045](../decisions/ADR-0045-erc4337-useroperation-index.md).

- Enabled ERC-4337 browsing exposes canonical global, detail, transaction, and
  address UserOperation resources plus exact `userOpHash` search. Pagination
  binds both the canonical snapshot and normalized EntryPoint configuration;
  unavailable coverage is never represented as an authoritative empty result.
