# P40 — API

Status: `done`

## Outcome

The explorer exposes a versioned spec-first native REST API, cursor-stable
queries, explicit completeness, API-key quotas, real-time head/reorg events, and
the agreed Etherscan V2 subset.

## References

- [Architecture](../architecture/overview.md)
- [Etherscan V2 compatibility matrix](../architecture/etherscan-v2-compatibility.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0036: Endpoint-scoped mempool replacement observations](../decisions/ADR-0036-endpoint-scoped-mempool-replacements.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P40-T01 | done | P00, P10 | OpenAPI 3.0.3 envelopes, scalars, errors, cursors, generated types | contract tests |
| P40-T02 | done | P40-T01 | Status/config, block, transaction, address, and search endpoints | handler/repository tests |
| P40-T03 | done | P20, P10, P40-T02 | Token/NFT, contract/source/ABI, stats, pending, verification endpoints | capability matrix tests |
| P40-T04 | done | P40-T01 | API-key lifecycle, anonymous/keyed quotas, CORS, health, metrics | auth/rate tests |
| P40-T05 | done | P40-T02 | Head/reorg SSE and cache invalidation | reconnect/reorg tests |
| P40-T06 | done | P40-T02, P40-T03, P40-T04 | Agreed `/v2/api` Etherscan module/action compatibility | golden compatibility tests |
| P40-T07 | done | P20-T11, P40-T02, P40-T03 | Transaction-scoped token transfer, log, trace identity, and state-change resources | contract, cursor, reorg, and capability tests |
| P40-T08 | done | P40-T02, P40-T07 | Snapshot-stable address transaction, internal-call, ERC-20, and NFT activity resources | contract, cursor, reorg, and capability tests |
| P40-T09 | done | P40-T02, P40-T05 | Writer-authoritative home snapshot and centralized SSE fanout | contract, replay, concurrency, and integration tests |
| P40-T10 | done | P20, P40-T08 | Snapshot-bound address origins, exact ERC-20 balances, and public wallet-chain configuration | contract, state, cursor, reorg, and configuration tests |
| P40-T11 | done | P30-T15, P40-T10 | Public proxy detail, canonical upgrade and initialization histories, and anonymous free verified-artifact reads | OpenAPI, generated contract, writer-query, cursor, auth, x402, and integration tests |
| P40-T12 | done | P40-T10 | Protocol detail fields, block-scoped transactions, withdrawals, and block-origin address evidence | OpenAPI, generated contract, query, cursor, reorg, and handler tests |
| P40-T13 | done | P40-T07, P40-T08 | Transaction-scoped successful internal ETH transfers and exact-block token decimals | OpenAPI, query, cursor, reorg, handler, billing-inventory, and generation tests |
| P40-T14 | done | P40-T12 | Exact-block execution and blob base-fee facts on transaction resources | OpenAPI, generated contract, query, home-stream, and generation tests |
| P40-T15 | done | P40-T14 | Endpoint-scoped mempool replacement observations and unified included/pending/replaced transaction detail API | migration, PostgreSQL, OpenAPI, handler, writer-routing, integration, and runtime E2E tests |
| P40-T16 | done | P40-T12 | Snapshot-stable canonical address withdrawal history ordered by numeric withdrawal index | migration, OpenAPI, query, cursor, reorg, handler, billing-inventory, and integration tests |
| P40-T17 | done | P40-T16, P20-T13 | Exact transaction-list method projection from published execution identity and ABI results | OpenAPI, query, canonicality, projection, generation, and integration tests |
| P40-T18 | done | P30-T17, P40-T17 | Verified-selector-backed Method projection for global and address transaction lists | OpenAPI, exact identity, collision, pagination, integration, and generation tests |

## Acceptance

- [x] uint256/wei and other unsafe numbers are strings; addresses are checksummed.
- [x] Responses expose canonicality, finality, coverage, and enrichment status.
- [x] Unsupported optional capability returns a machine-readable unavailable
      state rather than a misleading empty success.
- [x] Cursor order remains stable across pages and reorg boundaries.
- [x] API keys are one-time revealed and only keyed hashes are stored.
- [x] Transaction subresources expose one inclusion identity, stable pagination,
      bounded hostile data, and explicit optional-capability state.
- [x] One transaction-detail operation resolves included, current pending, and
      strictly evidenced direct replacement states from writer-authoritative
      PostgreSQL data without live RPC fallback.
- [x] Proxy detail and histories expose only the current canonical published
      generation, bind pagination to an immutable chain and publication
      snapshot, and return a stable stale-cursor error after reorganization or
      same-block publication replay.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P40-T01, P40-T02, P40-T03, P40-T04, P40-T05, P40-T06,
P40-T07, P40-T08, P40-T09, P40-T10, P40-T11, P40-T12, P40-T13, P40-T14, P40-T15,
P40-T16, P40-T17, P40-T18.

Generated contracts, handler/golden compatibility tests, query/cursor tests
and PostgreSQL normal/race integration passed at the feature revisions.
Acceptance covered exact numeric and block identity, canonical snapshot
pagination, authenticated quotas, durable SSE/reconnect, public verified
artifacts, protocol fields, balances, withdrawals and selector provenance.
Mempool included/pending/replaced reads also passed production runtime parity.
Current action coverage is maintained in the compatibility matrix rather than
a historical action count.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P40-api.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
