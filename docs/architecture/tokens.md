# Token observations and NFT metadata

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns exact asset observations and hostile metadata boundaries.
See [operator procedures](../operations/chain-data.md) and
[acceptance requirements](../testing/integration.md) for execution and validation.

- [Exact balances, holders and ownership](#exact-balances-holders-and-ownership)
- [NFT metadata and media boundaries](#nft-metadata-and-media-boundaries)

## Exact balances, holders and ownership

- Native ETH balances first use authenticated BAL publication with continuous
  coverage or an exact saved baseline; missing proof falls back to EIP-1898.
  ERC-20 `balanceOf`/`totalSupply` observations use an
  EIP-1898 canonical block-hash selector and recheck that hash after the RPC
  response. Address ERC-20 holdings bulk-read permanent exact observations by
  owner, token, and block hash, batch only cache misses, persist zero as well
  as positive balances, and reject conflicting concurrent observations;
  Etherscan-compatible address-holding pages reuse that exact cache inside
  their fixed candidate bound, while single-token balance and supply reads
  remain uncached.
  The always-scheduled `holder@1` stage is the separate enumerable-owner
  boundary. It accepts only high/verified ERC-20 Transfer participants from a
  block-zero continuous canonical history, reconciles every candidate with
  exact `balanceOf` plus `totalSupply` at one block hash, and publishes a token
  snapshot only when the positive balances and supply agree. Public reads
  reconstruct immutable per-holder observations: ordinary
  forward blocks refresh only exact Transfer participants, while first
  observation, coverage gaps, code/proxy generation changes, and the rotating
  audit lane perform a full candidate reconciliation. Native and compatibility
  holder reads are PostgreSQL-only, address ordered, and require
  Holder coverage through the selected latest completed canonical snapshot.
  Newer pending processing does not block that snapshot. Reorgs and replay
  reject affected publications; the newest retained valid snapshot remains
  readable. If no coherent completed snapshot exists, availability is explicit
  rather than a partial list.
  Orphan cache rows are retained but cannot satisfy a canonical lookup. Token
  classifications are retained per observed block hash, so a reorg can fall
  back to an older canonical observation even when the runtime code hash did
  not change. Event-derived NFT deltas only discover candidates;
  ERC-721 owners and ERC-1155 balances require exact `ownerOf`/`balanceOf`
  observations at the same fixed canonical block, carry `rpc_exact`
  confidence, and may be reused only while that block hash remains canonical.
  These exact NFT rows are write-once: identical concurrent writes preserve the
  original audit timestamp, while disagreement returns a typed integrity error
  instead of overwriting a block fact. Token and proxy exact-state calls share
  one sanitized capability classifier, so unsupported or pruned EIP-1898 state
  is `unavailable` and transient transport failures remain retryable.

## NFT metadata and media boundaries

- NFT media is never an arbitrary URL proxy. The server first resolves an
  `image` URI from an available metadata document bound to a canonical NFT
  observation, releases the database query, then applies DNS/IP/redirect
  policy, byte limits, MIME and image signature checks. Before returning bytes
  it rechecks that the same exact block-hash observation is still the newest
  canonical version. Metadata source discovery uses one state RPC endpoint and
  an EIP-1898 block-hash selector per NFT observation; exact source and terminal
  document facts are immutable and retained across reorgs. Media success and
  every early authentication or rate-limit error use no-store, restrictive CSP,
  nosniff, and same-origin resource headers.

- The metadata role additionally scans canonical logs for exact ERC-4906
  single/batch and ERC-1155 URI update signals. It retains accepted and
  malformed log facts by block hash, treats event payloads only as triggers,
  and reuses the one-endpoint EIP-1898 source discovery boundary at the event
  block. Batch ranges yield only already discovered IDs one at a time. No
  periodic, manual, browser-triggered, or request-time refresh exists.

- NFT metadata display is a separate canonical PostgreSQL projection. It
  returns bounded inert text and scalar traits plus, when syntactically safe, an
  unverified HTTPS navigation target derived from `image` (with `ipfs://`
  converted through the configured HTTPS gateway). The default-empty
  `metadata.image_navigation_allowed_origins` list permits exact HTTPS origins
  to bypass only the navigation localhost/non-public-IP check. It does not enter
  the metadata or media fetch policy. The embedded SPA never
  renders or prefetches that target and requires a bilingual confirmation before
  each opener-free, no-referrer external navigation. The authenticated media
  proxy remains the only server-side image retrieval path.
  A newer pending or failed event refresh may retain the previous canonical
  available document only with explicit latest/content observations and a
  stale marker; reorg automatically removes orphan update influence.
