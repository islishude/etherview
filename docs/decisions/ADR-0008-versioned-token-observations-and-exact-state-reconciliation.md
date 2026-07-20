# ADR-0008: Versioned Token Observations and Exact State Reconciliation

Status: accepted

## Context

Token classification and metadata are observed at immutable block hashes. A
contract may retain the same runtime code hash across many observations, and a
later observation may become orphaned. Collapsing those observations by code
hash destroys the older canonical value needed after a reorganization.

Transfer logs are evidence, not authoritative current state. Fake events,
rebasing behavior, constructor-time mints, and non-standard contracts make a
sum of event deltas unsafe to publish as an owner or balance without stating
and improving its confidence.

## Decision

- Token contract observations are immutable per chain, address, code hash, and
  observed block hash. Repeating the same code hash at a later block creates a
  new observation. Readers select the newest observation at or before their
  snapshot whose exact height and hash remain canonical, so an orphaned latest
  observation naturally falls back to the previous canonical version.
- NFT metadata source and document observations follow the same exact-block
  rule. Their PostgreSQL identity includes the observed block hash even when
  address, token ID, logical resource key, or source URI repeats. Exact source
  and terminal document facts are write-once, and media readers select the
  newest retained observation whose exact height/hash remains canonical.
- Event-derived token deltas remain block-scoped candidate/evidence rows. They
  are never returned as authoritative NFT owner or balance state.
- ERC-721 ownership is promoted only by `ownerOf(tokenId)` at the exact
  canonical block hash. ERC-1155 balances are promoted only by
  `balanceOf(owner, tokenId)` at that same selector. The canonical mapping is
  rechecked after all calls and before observations are persisted or returned.
- Exact ERC-721 owner and ERC-1155 balance observations are persisted by block
  hash. A later API replica may reuse a cached observation only for that exact
  still-canonical snapshot. A missing RPC capability or missing exact
  reconciliation returns a typed unavailable state rather than an event-derived
  value.
- The full exact-state key is write-once. Concurrent identical observations
  take a conditional no-op path and retain the first `observed_at`; a different
  owner, balance, block number, state, or confidence returns a stable integrity
  error. PostgreSQL triggers also reject direct mutation so another writer
  cannot bypass the application invariant.
- NFT list candidates may be discovered from canonical standard events, but
  candidate inclusion never depends on the sign or sum of event deltas and
  every returned value carries exact-state confidence. Unknown or conflicting
  token classifications cannot be promoted through a plausible `Transfer`
  layout alone.
- Token detection and reconciliation acquire one state RPC endpoint for an
  entire block-scoped operation. Every call for that operation stays on that
  endpoint; the pool may select another healthy endpoint for the next block.
  Catalog readers copy their bounded candidate set and release the PostgreSQL
  repeatable-read transaction before making these external calls. Exact block
  identity and post-call canonicality checks preserve the snapshot boundary
  without holding a database connection across network latency.
- NFT metadata source discovery uses the same one-endpoint, EIP-1898 and
  post-call canonicality boundary. Public media resolution similarly releases
  the database query before the external image fetch and then rechecks the
  selected exact observation, so neither path holds a PostgreSQL snapshot over
  network latency.
- Token detection and proxy discovery use one exact-state error classifier.
  Missing EIP-1898 support and known pruned or unavailable historical state are
  terminal `unavailable` capability facts for that block. Transport failures
  retain their cause for retry policy while exposing only a stable message;
  malformed successful wire values are permanent failures.
- ERC-20 balance and supply compatibility queries continue to use exact
  block-hash `balanceOf` and `totalSupply` calls with a post-call canonicality
  check. Stored token `total_supply` is an observation at its explicitly
  reported block, not an implicit current value.

## Consequences

- Reorganizations retain all token classification history and canonical
  readers recover without reconstructing overwritten rows.
- A disagreeing RPC replica cannot rewrite an already persisted exact NFT fact;
  operators receive a distinct integrity signal instead.
- NFT state reads may be unavailable when an exact state RPC observation cannot
  be obtained; they never silently fall back to delta sums.
- First reads can incur RPC work, while exact persisted observations make
  repeated reads deterministic and usable by replicas after transient RPC
  loss.
- Adding enumerable ownership indexes or proactive full-snapshot reconciliation
  remains a separate scaling concern and must preserve this exact-block trust
  boundary.
