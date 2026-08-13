# ADR-0036: Endpoint-scoped mempool replacement observations

Status: accepted

## Context

The pending-transaction API is backed by validated, immutable, expiring
`txpool_content` snapshots. A transaction can leave a node's pool because it
was mined, replaced, evicted, or because observation failed. Disappearance by
itself therefore cannot identify a replacement, and comparing different RPC
endpoints can confuse divergent node-local views with a replacement.

The transaction detail route also needs one stable public identity while a
transaction moves from the mempool into indexed chain data. Correctness
requires writer-authoritative lookup because mempool state and a just-indexed
inclusion must be compared without read-replica lag.

## Decision

- A replacement observation is an immutable PostgreSQL relation from the old
  transaction hash to the new transaction hash and the successful snapshot
  that proves the change. It expires and is deleted with that snapshot under
  the existing `mempool.retention` policy.
- A snapshot is valid only when each `(sender, nonce)` slot occurs once across
  its combined pending and queued pools. A conflicting slot fails the entire
  observation before persistence.
- `StoreSnapshot` may create a direct replacement relation only when the
  current `mempool_status` row still names the immediately preceding complete,
  unexpired snapshot, both snapshots use the same endpoint, the new
  observation time is strictly later, and the same sender and nonce changes
  hash. `last_snapshot_write_id` must also equal that current snapshot, so an
  intervening stale/out-of-order snapshot write breaks continuity even though
  it cannot move the public latest snapshot backward. A failure observation,
  endpoint change, expired predecessor, empty predecessor relation, missing
  status, or stale/out-of-order write breaks the evidence chain. Mere
  disappearance never creates a replacement.
- The new snapshot, replacement relations, status transition, and extension of
  each replaced transaction's expiry to the proving snapshot expiry commit in
  one chain-locked serializable transaction. Direct chains are retained as
  observed: `A -> B -> C` is not collapsed into `A -> C`.
- `GET /api/v1/transactions/{hash}` is the single detail lookup. Its
  discriminated response is `included`, `pending`, or `replaced`. It resolves
  writer-backed canonical or orphan inclusion first, the latest fresh pending
  snapshot second, and a fresh replacement observation third. Thus a later
  indexed inclusion supersedes an earlier node-local replacement observation.
- An enabled mempool without a current complete, unexpired observation returns
  typed `mempool_unavailable` for a hash absent from indexed inclusion data.
  When mempool collection is disabled, the existing inclusion-only `not_found`
  behavior remains. No detail lookup performs a live RPC request or execution
  simulation.
- The new pending transaction may expose only its directly replaced predecessor
  through `replaces_hash`; a replaced transaction exposes only its direct
  successor through `replacement_hash` and the proving observation time.
- The unified lookup is writer-routed whenever mempool collection is enabled.
  It is an explicit exception to ordinary read-replica explorer projections.

## Consequences

Replacement is intentionally an endpoint-scoped observation rather than a
claim about protocol-wide propagation or eventual canonical inclusion. Short
retention bounds both history and polling usefulness. Clients can follow direct
replacement chains and automatically converge to indexed inclusion without
inventing a reason for an unexplained disappearance.
