# ADR-0023: Exact Transaction State Differences

Status: accepted

## Context

The transaction detail surface needs account, balance, nonce, code, and storage
changes without making a browser or public HTTP request depend on live RPC.
State-difference RPC is optional, provider-specific, potentially large, and
block scoped. A response fetched from a different endpoint or canonical
lifetime than the stored transaction could silently describe another chain
view. Publishing a worker response without the repository's generation and
lease fences would also let stale work survive a replay or reorganization.

## Decision

- `state_diff@1` is an independent optional enrichment stage. It uses
  `debug_traceTransaction` with geth's `prestateTracer` in diff mode and never
  falls back to an unpinned height, `latest`, a browser provider, or an
  on-demand public-request RPC call.
- One block attempt acquires one trace-capable endpoint. Every transaction is
  identified from the exact stored block inclusion before the external call.
  The stage copies bounded inputs, closes database reads, performs RPC, then
  rechecks canonicality, lease ownership, and generation while atomically
  publishing the result.
- Normalization accepts only canonical addresses, quantities, bytecode, storage
  keys, and storage words after strict hostile-input validation. Per-transaction
  and per-block limits cover payload bytes, accounts, storage slots, code bytes,
  and total normalized values. Provider bodies and nested errors never enter
  logs, durable result details, or public errors.
- State changes are flattened into immutable rows keyed by chain, block number,
  block hash, transaction hash, account address, field kind, and optional
  storage key. Rows retain orphan history through their block hash and
  canonical flag.
- The stage writes its rows, controlled canonicality journal, exact stage
  result, durable job generation, and successful transition through the
  lease-fenced atomic publication contract from ADR-0012. `failed` and
  `unavailable` terminal results publish no state-change rows.
- Absence of debug trace support is `unavailable`, not an empty result. A
  complete published generation with zero normalized changes is the only
  authoritative empty result. Public readers require the exact canonical
  inclusion and matching `published_block_stage_results` generation.
- Public transaction subresources carry their resolved block number, block
  hash, transaction hash, and transaction index. Clients must not combine
  subresources whose block identity differs from the overview transaction.

## Consequences

Transaction state changes are replayable, reorg-safe, and independent of live
RPC health at read time. The extra trace work and storage are bounded and
optional. Deployments without the required debug capability remain core-ready
and expose a stable unavailable state instead of fabricated empty data.

Adding another state-difference adapter, widening limits, changing the durable
row identity, or changing publication semantics requires a new stage version
and a reviewed update to this decision.
