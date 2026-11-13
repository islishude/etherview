# ADR-0060: Background DeFi positions

Status: accepted

## Decision

Address DeFi reads consume PostgreSQL publications from the current configured
chain. Supported adapters are Uniswap V2/V3/V4, Aave V3, Curve and Lido. A
reviewed deployment manifest binds chain/genesis, deployment block/hash,
contract code and supported implementation identities. Symbols and event
signatures alone never establish protocol identity.

Discovery is independent of public requests. Canonical logs and authenticated
registries supply candidates; exact block-hash reads establish ownership and
amounts. Database snapshots close before external calls. One operation pins
one state endpoint. Publication rechecks canonical attachment, source generation
and worker ownership in one transaction. Orphan facts remain retained.

Each deployment advances independently using bounded resumable work. Completed
publications expose their actual block, discovery coverage and failures. A
partial scan, unavailable call or unsupported structure never means zero.
Unknown hooks and pool implementations cannot acquire invented values. Public
GET requests do not call RPC, enqueue work or mutate observations.

Direct holdings only: no external Gauge/Farm/Vault/custody or Lido stVault
look-through. Curve base-pool expansion is bounded and cycle checked. Values
are integer protocol quantities, without USD totals, APR or PnL. V3 recorded
claimable amounts may contain withdrawn principal and must not be labelled
entirely as fees. Aave debts remain distinct from assets. Lido pending requests
are not guaranteed claimable ETH.

The API is spec-first, generated and same-origin. Cursors bind the owner,
filter and selected publication identities; reorg and source changes invalidate
them. The browser uses explicit refresh and existing status invalidation.
Tokens/NFTs retain receipt tokens; DeFi explains their economic rights without
adding cross-tab totals.

## Delivery and evidence

P20-T35 owns discovery, adapters and publication; P40-T36 owns read-only APIs;
P50-T90 owns browsing; P30-T158 owns production all/split and live deployment
acceptance. Local fixtures do not establish live compatibility or complete
historical coverage. P70 release gates remain separate.
