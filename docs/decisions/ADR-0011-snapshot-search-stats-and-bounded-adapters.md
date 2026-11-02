# ADR-0011: Snapshot Search, Statistics, and Bounded Adapters

Status: accepted

[ADR-0058](ADR-0058-background-ens-synchronization-and-database-only-reads.md)
owns current-chain background name synchronization and database-only name reads.
This decision owns search catalog, price-adapter, statistics and maintenance
boundaries. The retired request-time name adapter is not an active contract.

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
- Exact block-height, block-hash, and transaction-hash routes resolve operator
  display labels from the same retained catalog generation as text search.
  A height query prefers its height-keyed label and may fall back to the exact
  canonical hash label; a hash query uses only its hash-keyed label so an orphan
  cannot inherit the canonical height's label. Pagination never switches to a
  label edit from a later generation.
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
- Dotted name search reads canonical known-name identities from PostgreSQL,
  including unresolved and expired names, under ADR-0058. It performs no resolver
  calls or writes. Profile and resolution validity are independent of directory
  membership; stale observations never become a fresh resolved identity.
- Price adapters use the shared SSRF-safe HTTPS client and persist bounded
  success/failure observations with expiry and provider-scoped identity. Adapter
  and state-RPC failures expose stable capability states/codes, never nested
  upstream messages or credential-bearing URLs. ENS observations and publication
  use ADR-0058's separate worker, canonical attachment and lease fences.
- The production maintenance role runs search/adapter housekeeping as a shared
  supervisor component in both monolith and split deployments. It sweeps once
  on startup and periodically thereafter, uses a chain-scoped PostgreSQL
  transaction advisory lock, invokes the finalized-aware catalog prune with
  the configured retained generation window, and deletes only one configured
  batch of expired adapter observations per sweep. A database failure is
  logged only with a stable code and retried with bounded backoff; it neither
  exits the component nor withdraws process readiness. PostgreSQL remains the
  only dependency of this cleanup path.
- Block statistics and aggregate reads select the contiguous completed
  `stats@4`/`token@2` prefix beginning at the requested start, capped by the
  requested end. Pending suffixes do not block completed data. Every request,
  including refresh, repeats selection and reports the actual returned range.
  No completed prefix remains typed unavailable; gaps are not counted as zero.
- Statistics use `stats@4` under
  [ADR-0052](ADR-0052-amsterdam-explorer-support.md); earlier stage versions
  cannot satisfy the current projection. Except for the exact configured
  indexing start, a block requires its exact canonical parent and a strictly
  positive timestamp interval. Sparse canonical blocks wait in the durable queue
  until their exact canonical parent is indexed, without consuming attempts or
  becoming exhausted while backfill is pending. The dependency is rechecked at
  claim; orphan jobs remain claimable for the canonical stale-block skip.
  The start block has null interval/TPS. Aggregate TPS is `sum(transaction_count) / sum(block_interval_seconds)` over blocks with
  a known interval, and remains null when no such interval exists. Execution
  fees, priority fees, failed transactions, and successful top-level creation
  counts are authenticated from the exact receipt/block facts as defined by
  [ADR-0025](ADR-0025-historical-execution-analytics.md).
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
- Name search does not depend on resolver availability. Known unresolved names
  remain directory results; only valid published observations supply addresses.
- ENS publication and canonical detach serialize under ADR-0058; withdrawn
  observations cannot remain valid current labels or resolution results.
- Changing cursor identity, catalog temporal semantics, adapter observation
  identity, or statistics formulas requires a new reviewed persistent/public
  contract and, for block statistics, a new stage version.
