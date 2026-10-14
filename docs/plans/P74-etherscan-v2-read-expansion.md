# P74 — Etherscan V2 Authoritative Read Expansion

Status: `done`

## Outcome

The explicit `/v2/api` allowlist adds authoritative withdrawals, exact address
holdings, EOA funding origin, per-block transaction counts, and advanced
from/to filters without adding an RPC proxy, event-derived balances, unbounded
holder enumeration, or a second correctness store.

## References

- [Architecture](../architecture/overview.md)
- [Etherscan V2 compatibility matrix](../architecture/etherscan-v2-compatibility.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0008](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0035](../decisions/ADR-0035-user-owned-scoped-api-keys.md)
- [ADR-0044](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P74-T01 | done | P20, P40, P73-T04 | Governance, compatibility contract, operation catalog, and generated billing bound | plan, contract, and generation checks |
| P74-T02 | done | P74-T01 | Withdrawals, block counts, funding origin, and normal/internal/token advanced filters | handler, SQL, PostgreSQL, coverage, and reorg tests |
| P74-T03 | done | P74-T01 | Dense bounded ERC-20 and ERC-721 holding pages from exact canonical state | state, cache, limit, canonicality, and PostgreSQL tests |
| P74-T04 | done | P74-T02, P74-T03 | Reader/writer routing, prepaid billing, production runtime parity, documentation, and completion gates | billing, runtime E2E, race, security, and common gates |

## Acceptance

- [x] The allowlist contains exactly 34 registered and 28 billing-eligible actions.
- [x] Every new or extended action validates one unambiguous selector mode and canonical billing resource.
- [x] Empty withdrawals, funding, counts, or holdings are authoritative only after their required continuous coverage proof.
- [x] Address holdings return only exact current state and never event-derived balances.
- [x] Holding pages are dense within a fixed 1,000-candidate work bound and fail closed rather than returning partial results.
- [x] `fundedby` accepts current EOAs only and excludes self-transfers, withdrawals, fee recipients, failures, and reverted traces.
- [x] New priced reads retain user debit, operator bypass, logical-failure release, and bounded-response behavior.
- [x] Monolith and split API roles use the same components, reader routing, writer fences, and public results.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P74-T01, P74-T02, P74-T03, P74-T04.

Handler goldens, generated contracts, real PostgreSQL coverage/reorg/query
fixtures and Hardhat provider compatibility passed. Exact ERC-20/ERC-721
holdings passed candidate/batch bounds, zero caching, restart reuse and orphan
exclusion tests. `make check`, integration/race and production runtime/x402
parity passed; later regressions protect latest token classification and
candidate-scoped funding provenance.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P74-etherscan-v2-read-expansion.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
