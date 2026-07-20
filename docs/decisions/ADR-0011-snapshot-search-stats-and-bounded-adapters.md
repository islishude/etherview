# ADR-0011: Snapshot Search, Statistics, and Bounded Adapters

Status: accepted

## Context

Search results are assembled from core chain facts, operator labels, names,
token observations, code observations, and verified contracts. The canonical
tip alone is not a sufficient pagination snapshot because labels and late
enrichment can change while that tip remains fixed. Selecting every historical
row also exposes stale logical names, token metadata, and verified code after
an upgrade or reorganization.

Block-rate statistics need an explicit temporal contract. Genesis and a
configured non-zero indexing start have no indexed parent interval, while an
ordinary block without its exact canonical parent is incomplete rather than a
zero-second interval. Blob fee statistics additionally have to distinguish a
block without blob transactions from inconsistent header and receipt facts.

Names and fiat prices are useful optional capabilities, but neither may become
a correctness dependency. Their network responses can be stale, hostile, or
bound to a block that is no longer canonical. An API-only process may also
have a state-purpose RPC endpoint without a history-purpose endpoint and must
still expose state-backed capabilities without weakening their error contract.

## Decision

- Searchable source changes advance a serialized, per-chain PostgreSQL catalog
  generation and close the previous temporal document. A cursor binds the
  chain, exact canonical tip number and hash, retained catalog generation,
  normalized query, and stable rank/kind/key boundary. A later page executes
  against that same generation; a cursor older than `min_generation` is
  rejected instead of silently reading current documents. Trigger and pruning
  functions have a function-local `search_path` bound to their migration
  schema, so a pooled connection cannot redirect their writes into another
  Etherview schema.
- Search returns only the latest canonical row for one logical name or token.
  Verified-contract matches additionally require the code observation selected
  for that address at the cursor's canonical snapshot and a validity range
  covering that snapshot. Reorganizations therefore fall back to retained
  older observations rather than leaking the detached version.
- Catalog pruning may collapse redundant history only at or below the current
  finalized baseline. It retains every reorgable version above finality and
  advances `min_generation` only after closing and deleting documents outside
  the configured generation window. Search-source `chain_id` is immutable;
  moving a source across chains is rejected transactionally.
- A first-page dotted search requires a fresh external name resolution before
  opening its repeatable-read search transaction. Resolver absence, expiry
  followed by fetch failure, or a resolved identity not visible at the new
  exact canonical snapshot is typed unavailable; stale catalog data is never
  a fallback. The cursor records the accepted resolved address, so later pages
  validate that same identity at the frozen tip and generation without
  refetching merely because the adapter TTL expires.
- A successful name observation is accepted only for the exact configured
  chain and canonical block number/hash. Its statement takes `FOR KEY SHARE`
  on that canonical row through the complete insert, serializing the
  check-to-write window with a detach. The first identity for a registry,
  name, and block hash is immutable; a concurrent identical response is an
  idempotent no-op that preserves its first audit timestamp and catalog
  generation, while a conflicting address, resolver, or height is a typed
  `identity_conflict`.
- Name and price adapters use only the shared SSRF-safe HTTPS client and persist
  bounded success or failure observations with an expiry. Name successes are
  canonical-block facts; failure observations intentionally require no block
  identity and suppress repeated hostile calls only for their short TTL.
  Adapter and state-RPC failures cross public boundaries as stable capability
  states and codes, never nested upstream messages or credential-bearing URLs.
- The production maintenance role runs search/adapter housekeeping as a shared
  supervisor component in both monolith and split deployments. It sweeps once
  on startup and periodically thereafter, uses a chain-scoped PostgreSQL
  transaction advisory lock, invokes the finalized-aware catalog prune with
  the configured retained generation window, and deletes only one configured
  batch of expired adapter observations per sweep. A database failure is
  logged only with a stable code and retried with bounded backoff; it neither
  exits the component nor withdraws process readiness. PostgreSQL remains the
  only dependency of this cleanup path.
- Statistics advance to `stats@2`. Except for the exact configured indexing
  start, a block requires its exact canonical parent and a strictly positive
  timestamp interval. The start block has null interval/TPS. Aggregate TPS is
  `sum(transaction_count) / sum(block_interval_seconds)` over blocks with a
  known interval, and remains null when no such interval exists.
- Blob base fee and burn use the exact block header and receipt facts. A block
  with no blob transactions records null blob base fee and zero blob burn.
  Receipt blob gas without the required header inputs is permanent source
  corruption, not a fabricated value or successful empty statistic.
- API-only startup may construct the RPC pool from a state-purpose endpoint
  without requiring a history-purpose endpoint. Each state operation remains
  pinned to one endpoint and immutable block identity and retains the same
  typed unavailable/retryable semantics as split enrichment roles.

## Boundaries

This decision does not make an external name service authoritative for chain
state or add a general-purpose URL proxy. It does not infer historical account
state from indexed events, and it does not promise price availability. A
missing adapter configuration remains an explicit unavailable capability.

Search generation retention is a pagination contract, not a permanent copy of
every label edit. Core canonical and orphan facts remain governed by their own
retention invariants. Finality is used only as the safe floor for catalog
history compaction; it is never inferred from the catalog itself.

Housekeeping is a storage-retention aid, not a chain-data correctness or API
availability prerequisite. A skipped advisory lock or failed sweep leaves the
prior catalog and adapter facts intact for a later retry.

## Consequences

- A caller can traverse a stable search result set while labels and enrichment
  continue to arrive, and receives an invalid-cursor error once that retained
  snapshot is deliberately pruned.
- Reorgable name, token, and verified-contract candidates remain available for
  canonical fallback without returning multiple stale logical versions.
- Rate and blob statistics expose unavailable temporal inputs as null or a
  typed failure instead of reporting plausible but false zeros.
- Optional adapter failure, network loss, or missing history RPC cannot block
  core ingestion or make PostgreSQL cease to be the correctness source.
- Dotted search never converts an unavailable fresh resolution into a stale
  success, while an already-issued cursor remains traversable for its retained
  snapshot without depending on the external adapter's later health.
- Concurrent name publication and canonical detach have one serial order: a
  detach that wins rejects the stale observation, while a name write that wins
  is subsequently marked noncanonical by that detach and disappears from
  current search.
- Changing cursor identity, catalog temporal semantics, adapter observation
  identity, or statistics formulas requires a new reviewed persistent/public
  contract and, for block statistics, a new stage version.
