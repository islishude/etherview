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

- `state_diff@3` is an independent optional enrichment stage. It first calls
  `debug_traceBlockByHash` for the job's exact block hash with geth's
  `prestateTracer` in diff mode. If any top-level call target has no execution
  identity or remains unavailable, it makes one additional block-level call on
  the same endpoint and exact block hash with complete prestate. It never calls
  `debug_traceTransaction` and never falls back to an unpinned height,
  `latest`, a browser provider, or an on-demand public-request RPC call.
- One block attempt acquires one trace-capable endpoint. Every transaction is
  identified from the exact stored block inclusion before the external call.
  Every requested block response must be a non-null array with exactly one item
  per stored inclusion in the same order and with the same `txHash`; malformed,
  missing, duplicate, reordered, or item-error responses cannot publish a
  partial state difference or journal. The stage copies bounded inputs, closes
  database reads, performs RPC, then rechecks canonicality, lease ownership,
  and generation while atomically publishing the result.
- Normalization accepts only canonical addresses, quantities, bytecode, storage
  keys, and storage words after strict hostile-input validation. Per-transaction
  and per-block limits cover payload bytes, accounts, storage slots, code bytes,
  and total normalized values. Provider bodies and nested errors never enter
  logs, durable result details, or public errors.
- Complete prestate supplements only a transaction's top-level target and, when
  present, its first-hop EIP-7702 delegate. Absence of the top-level target from
  the complete prestate is exact empty-account evidence. A delegation
  designator whose delegate account is absent remains unavailable. Unrelated
  accounts are validated and budgeted but never become execution identity, and
  complete prestate does not create additional state-change rows.
- State changes are flattened into immutable rows keyed by chain, block number,
  block hash, transaction hash, account address, field kind, and optional
  storage key. Rows retain orphan history through their block hash and
  canonical flag.
- The stage writes its rows, controlled canonicality journal, exact stage
  result, durable job generation, and successful transition through the
  lease-fenced atomic publication contract from ADR-0012. `failed` and
  `unavailable` terminal results publish no state-change rows.
- Absence of block-level debug trace support and recognized pruned or missing
  historical state are `unavailable`, not empty results. Other provider item
  failures are stable retryable failures without nested upstream text. A
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

ADR-0034 upgrades this stage to `state_diff@2`. Each exact transaction-bound
item in the block-level pre/post response proves EIP-7702 tuple application and
execution-code identity; absent evidence is unavailable and a nonce/code
contradiction is permanent.

Migration `0047` upgrades the current witness to `state_diff@3` so an unchanged
top-level target omitted by diff mode is resolved from exact complete prestate.
Existing `state_diff@2` output remains historical and requires an explicit
bounded reindex before it can satisfy current public completeness.
