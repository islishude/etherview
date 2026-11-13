# Chain ingestion and canonical history

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns chain identity, ingestion, coverage and recovery authority.
See [operator procedures](../operations/recovery.md) and
[acceptance requirements](../testing/runtime.md) for execution and validation.

- [Chain identity and genesis](#chain-identity-and-genesis)
- [RPC ingestion and atomic persistence](#rpc-ingestion-and-atomic-persistence)
- [Coverage, finality markers and reorgs](#coverage-finality-markers-and-reorgs)
- [Repair and downstream invalidation](#repair-and-downstream-invalidation)
- [Operator Recovery Boundary](#operator-recovery-boundary)
- [Pending transaction snapshots](#pending-transaction-snapshots)

## Chain identity and genesis

- Each deployment serves one configured chain and binds it with chain ID plus
  genesis hash. Every RPC endpoint is verified against both.

- Block-zero RPC ingestion authenticates but cannot enumerate the allocation.
  The sync role can read the authoritative Genesis JSON from one mutually
  exclusive server-only source: an absolute `chain.genesis_file` or a
  `chain.genesis_url`; either requires indexing to start at zero. After bounded
  source validation, go-ethereum `core.Genesis.ToBlock()` is authoritative for
  the allocation root, Genesis header semantics, and block hash. Those values
  must match canonical block zero before balances, nonces, code identity, and
  per-account storage roots are stored atomically. Raw storage slots are
  discarded. Missing input is a typed unavailable capability, not an empty
  allocation.

- Remote Genesis bootstrap is restricted to public HTTPS port 443 with no
  credentials, query, fragment, path traversal, redirect, proxy, or
  private/special-use destination. Only identity-encoded, valid JSON within the
  64 MiB cap is accepted, with a JSON, vendor `+json`, octet-stream, or text
  media type. An optional explicit non-zero lowercase SHA-256 must match the
  exact response bytes.

- Sync replicas hold a per-chain session advisory lock across the remote
  completion check and fetch, while the HTTP request and parsing remain outside
  the atomic import transaction. A completed import is the durable source of
  truth and removes the remote runtime dependency; a configured checksum must
  match its persisted document digest. Failures expose only stable redacted
  unavailable or failed states. This source extension changes neither the
  persistent schema nor the public API.

- Blocks are identified by hash. Canonical height is a mutable mapping and
  orphan facts remain queryable.

- Core readiness means block, transaction, receipt, log, and withdrawal facts
  are durably committed. Enrichment has independent completeness states.

## RPC ingestion and atomic persistence

- Multi-statement canonical and coverage writes use READ COMMITTED while
  holding the chain-scoped transaction advisory lock. Fresh statement
  snapshots after lock waits, targeted row locks, and one atomic transaction
  form the cross-role serialization protocol.

- Execution RPC ingestion is raw-first. The transport bounds and validates the
  JSON-RPC envelope before protocol decoding and preserves each accepted raw
  object independently of its typed projection. Go-ethereum is authoritative
  for matching Ethereum protocol semantics, including transaction types 0
  through 4. Unsupported future transaction types fail permanently and
  atomically before any block write or coverage advance. `blocks.raw` preserves
  the block result's top-level JSON shape. A PoW block stores its full uncle
  headers only in reserved versioned root-level metadata, which
  `DecodeStoredBlock` removes before exposing the raw block. Legacy rows with
  empty uncles decode directly; a legacy row with non-empty uncle hashes but no
  headers fails permanently with `ErrStoredUncleHeadersUnavailable` and
  requires an exact endpoint- and block-identity-bound RPC repair before it can
  become a verified bundle. Typed geth projections are never marshaled over
  stored raw objects or used to discard unknown fields on supported objects.
  Response limits, batch correlation, endpoint pinning, stable error
  classification, redaction, SQL models, and public API models remain explicit
  Etherview adapters; see
  [ADR-0022](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md).

- Each per-endpoint RPC limiter grants FIFO time slots only to live waiters.
  Canceling a queued request removes it without advancing the next slot, so a
  canceled call cannot delay unrelated later work or poison the provider-rate
  schedule.

- Core persistence takes one root-normalized owned copy of each incoming
  bundle. That single decode both validates caller alignment and isolates the
  stored rows from later source mutation; segment ancestry and row insertion
  consume the owned result without repeating full bundle validation.

- The owned segment is written in foreign-key order through relation-specific
  JSONB record batches. A batch contains at most 512 rows and targets 4 MiB of
  encoded input; an indivisible larger row is sent alone and remains bounded by
  the ingestion/backfill input budget. Block identity, transaction identity,
  inclusions, receipts, logs, withdrawals, canonical mappings, journal and
  derived canonical flags, and core outbox messages remain inside the same
  chain-locked transaction. Batching changes round-trip count, not authority or
  publication atomicity.

- Public block, transaction, address-activity, and Etherscan list/detail reads
  never fan out the complete `blocks.raw` JSONB value. Stored generated columns
  retain the validated miner, gas, base-fee, transaction-count, and withdrawals
  presence/count scalars; relational block identity and timestamp remain the
  query keys. Block responses aggregate normalized withdrawal rows and compare
  normalized transaction/withdrawal counts with the authenticated Raw-derived
  counts. Transaction and compatibility receipt validation uses the exact block
  identity plus the narrow base-fee projection. Missing, malformed, or drifting
  projections fail closed rather than silently returning a partial model.

## Coverage, finality markers and reorgs

- New-head subscriptions are hints. Polling, ancestry checks, and gap scans are
  authoritative.

- Historical block/receipt coverage and historical state availability are
  separate capabilities.

- The configured indexing start and normalized canonical coverage ranges are
  PostgreSQL facts. The core checkpoint is only the end of the range containing
  that configured start; a higher isolated live range is reported separately
  and cannot make the deployment ready.

- Safe and finalized RPC markers are accepted only when their exact hashes are
  canonical and every parent link from the current tip through the lowest
  requested marker is present and continuous. A height/hash match inside a
  sparse or internally inconsistent mapping is not sufficient finality proof.

- Live-head polling and historical backfill are independent lanes. Bounded
  PostgreSQL range leases coordinate backfill replicas, while coverage remains
  the restart-safe source of missing work; see
  [ADR-0006](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md).

- A backfill lease is bounded independently by block count, owned raw bytes,
  and relational row work. One worker retains at most half of the configured
  byte/row budget in a pending segment so the next fetched block fits inside
  the total budget, commits each full subsegment through the same canonical
  coverage transaction, and completes the lease only after its complete range
  is covered. A crash or later-block failure retains exact committed coverage
  and makes only the missing suffix reclaimable.

- A finalized-crossing, over-depth, no-common-ancestor, or source-inconsistent
  fork is a canonical-safety halt. The first fatal lane cancels live and
  backfill work, records `etherview_sync_halted{reason}` with a stable reason,
  and keeps the process alive and scrapeable until operator cancellation and a
  repair/restart. The Prometheus rule alerts on that durable in-process signal.

- Fork-choice checks the configured reorg depth and finalized floor before
  crossing either boundary with another parent RPC, canonical-row traversal,
  or branch-sized allocation. A forward head gap larger than that bounded
  ancestry window remains sparse/backfill work rather than being assembled in
  one canonicalizer call.

## Repair and downstream invalidation

- Operator repair refetches a block through the normal sticky history-RPC path
  and may refresh core rows only when chain, height, hash, and parent still
  match the canonical mapping. The refresh path never invokes normal
  fork-choice `Apply`; it never moves canonicality or checkpoints.

- A core refresh invalidates replayable output directly derived from that
  block. Rebuilding proxy, ABI, token, statistics, trace, state-difference, or
  UserOperation output is an
  explicit, block-hash-scoped reindex operation; active leases are never
  reset. A versioned proxy/ABI cutover is replayed in dependency order,
  `proxy` before `abi`, rather than by an unbounded migration enqueue.

## Operator Recovery Boundary

Repair and reindex intentionally have different authority. `repair --stage
core` can rewrite a known block's core bundle after an RPC refetch, but the
database rechecks canonical identity and finality inside one chain-locked
transaction. Refreshing a finalized height requires an explicit audited
override and still cannot replace its hash or parent.

`reindex --stage requests|consensus|blob|native_balance|native_transfer|proxy|abi|token|stats|trace|state_diff|userop|holder` schedules or
replays immutable block-hash-scoped jobs. Existing queued or leased jobs remain
owned by their current worker; only terminal jobs may be reset. A proxy/ABI
cutover is run in dependency order, `proxy` before `abi`. Repair does not
silently infer the downstream range an operator intends to rebuild. See
[ADR-0002](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md).

## Pending transaction snapshots

- Pending transactions come from validated `txpool_content` responses (pending and
  queued pools) and are published as an immutable, expiring PostgreSQL
  snapshot. A cursor is bound to that snapshot; timeout, method absence, or a
  failed poll is reported as unavailable rather than an empty snapshot.
  Sender/nonce slots must be unique across both pools. Consecutive successful,
  unexpired snapshots from the same endpoint may persist a direct old-hash to
  new-hash replacement observation; endpoint changes, failure gaps, stale
  writes, expiry, and disappearance alone never imply replacement. These
  observations share the configured mempool retention. The writer-backed
  transaction detail lookup gives canonical or orphan inclusion precedence,
  then resolves current pending and retained direct replacement observations
  without a live RPC call. See ADR-0036.

## Bounded ingestion and publication work

Head polling pins one endpoint for height, lightweight header validation and,
when identity changes, exact-hash bundle retrieval. An unchanged canonical tip
skips bundle and receipt work while safe/finalized markers continue to advance.
Same-height reorgs and shorter heads still enter canonicalization.

Canonical segment validation locks the mapping and both neighboring boundaries
in one bounded query under the chain lock, then checks parent/hash continuity in
memory. Enrichment claims inspect at most 32 priority-ordered candidates per
attempt, trying publication advisory ownership before nonblocking row ownership.
Busy candidates are skipped, including during exhaustion processing.

Browsing publication triggers compare only publication-relevant fields and merge
transaction-local chain/stage/height work. Deferred refresh drains in that order
before commit. All deferred projection paths acquire locks in the same order:
proxy interaction coverage, chart source, then browsing stages. This prefix also
applies when an earlier source mutation queues browsing ahead of outbox work.
Heartbeat expiry extensions do not refresh publication; changing
whether the expiry is null still does. Canonical switches cover every stage,
while outbox changes affect core only. Revision, coverage and events remain atomic.

## Native balance publication

`native_balance@1` authenticates an exact block header before any BAL request.
Only `BlockAccessListHash` presence enables fetching. The validated body and its
balance projection publish together under the job lease and generation fence.
`native_balance_changes` retains every post-balance by block access index;
`native_balance_observations` stores the final balance per address and exact
request-driven RPC baselines. Orphan history remains available internally.
Publication-aware `browsing_coverage` proves continuity from a known observation
to the requested canonical hash. A read-only BAL account supplies no baseline.
Missing coverage falls back to EIP-1898 and saves the exact observation; conflicting
values fail closed. No balance polling task is introduced. See
[ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md) and the
[chain-data runbook](../operations/chain-data.md#amsterdam-protocol-data).
