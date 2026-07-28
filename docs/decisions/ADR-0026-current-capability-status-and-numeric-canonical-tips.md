# ADR-0026: Current Capability Status and Numeric Canonical Tips

Status: accepted

## Context

The public status surface combines core indexing readiness with optional data
capabilities. Its original optional states were derived only from feature
configuration, so an enabled Trace or historical-state capability remained
`pending` even after its backing work or RPC probe had succeeded.

Exact current-state reads also selected a canonical block number after casting
it to text. An unqualified `ORDER BY number` resolved to that textual output
column and selected block 99 ahead of block 100 and later heights. The same
ambiguous pattern existed in two historical ABI observation lookups.

## Decision

- `completeness.state` continues to describe the configured historical-state
  RPC capability. It is `complete` when at least one state-purpose endpoint
  successfully probed historical state at the configured start, `pending` when
  no endpoint has a conclusive result, and `unavailable` when the feature is
  disabled, no state endpoint exists, or every state endpoint reports the
  capability unavailable.
- `completeness.trace` describes the exact current canonical indexed block. It
  is `complete`, `unavailable`, or `failed` only when the matching
  `trace@1` result is published for that block number and hash. A missing
  indexed block or matching publication is `pending`. A disabled feature or
  absent Trace RPC is `unavailable`.
- This status does not claim gap-free historical Trace coverage.
  Transaction-level state differences retain their independent
  `state_diff@1` publication state and do not redefine
  `completeness.state`.
- Status reads bind core coverage, the indexed block identity, and its Trace
  publication in one PostgreSQL statement snapshot. A result for an orphaned
  or replaced block hash cannot satisfy the current status.
- Queries may cast public quantities to text for scanning, but ordering and
  range selection use explicitly qualified numeric persistence columns. Exact
  current-state and historical ABI selection must never order by a textual
  output alias.

## Consequences

Optional status changes reflect current durable or probed evidence without
changing the public response shape. A newly indexed block may make Trace
briefly `pending` until its exact publication commits. Historical-state
availability remains distinct from transaction state-difference enrichment.

Address balance, nonce, code classification, Etherscan exact-state reads, NFT
reconciliation, and historical ABI binding select the numerically newest valid
canonical observation while retaining exact block-hash and post-call
canonicality checks.
