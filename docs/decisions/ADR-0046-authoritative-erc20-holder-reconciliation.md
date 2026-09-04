# ADR-0046: Authoritative ERC-20 Holder Reconciliation

Status: accepted

## Context

ERC-20 `Transfer` logs identify possible holders but do not prove current
balances. Constructor allocations need not emit a mint event, rebasing or
non-standard contracts may mutate balances silently, and ordinary JSON-RPC
cannot enumerate an arbitrary mapping. Returning an event-delta sum as a
holder list would therefore turn incomplete evidence into false current state.

## Decision

- `holder@1` is an always-scheduled derived stage and a v1 release dependency.
  Holder availability remains separate from Core readiness.
- Candidate addresses come only from canonical high/verified ERC-20 Transfer
  participants and require continuous block-zero-through-tip Token coverage.
- A token becomes available only after every candidate has an exact
  block-hash-scoped `balanceOf` observation, the exact `totalSupply` equals the
  sum of all balances, and the anchor remains canonical. Zero balances remain
  durable facts but are absent from public holder pages.
- Exact calls run outside database snapshots in batches of at most 200 through
  one state endpoint per operation. Publication rechecks canonicality and uses
  lease-fenced short transactions. Partial reconciliation is never readable.
- Forward publication reconciles every Transfer participant and the affected
  supply. Direct code or supported proxy-generation changes require a new full
  baseline. A durable low-priority full audit eventually revisits every holder;
  any disagreement withdraws availability until a new baseline completes.
- PostgreSQL owns immutable observations, snapshot summaries, publication
  generations, current projections, and coverage. Optional infrastructure is
  never authoritative.
- Native cursors bind the exact canonical snapshot and holder publication
  epoch. Reorg or replay at or below that snapshot invalidates the cursor;
  later blocks do not.
- Public reads require Holder coverage through the current canonical tip.
  Lagging, rebuilding, inconsistent, or unavailable state returns a stable
  typed unavailable result. A successful empty result requires an exact zero
  supply and zero holder count.
- Native pages are address-ascending. Historical holder queries, balance-ranked
  Top Holders, NFT holder enumeration, rebasing adapters, and operator
  allowlists are outside this decision.
- Existing databases use explicit bounded Holder reindex requests. Neither a
  migration nor startup performs an unbounded backfill.

## Consequences

Conforming ERC-20 holder pages are repeatable, reorg-safe PostgreSQL reads and
do not issue request-time RPC. Non-conforming or insufficiently covered tokens
are unavailable rather than approximately enumerated. Building and auditing a
large holder set consumes bounded calls over unbounded total time, so release
capacity evidence must include concurrent reconciliation load.
