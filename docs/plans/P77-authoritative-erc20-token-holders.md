# P77 — Authoritative ERC-20 Token Holders

Status: `done`

## Outcome

ERC-20 contracts with genesis-through-tip canonical Transfer coverage and a
complete exact-state reconciliation expose current holder counts and
address-ordered holder pages through native, Etherscan-compatible, and
bilingual Web surfaces. Missing coverage, inconsistent supply, unsupported
token behavior, RPC loss, replay, or reorganization fails closed.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0006](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md)
- [ADR-0007](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0008](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0046](../decisions/ADR-0046-authoritative-erc20-holder-reconciliation.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P77-T01 | done | P20, P30, P68, P75 | `holder@1`, exact-state reconciliation, immutable PostgreSQL facts, coverage, replay, and reorg handling | stage, RPC, migration, PostgreSQL, reorg, lease, race, and generation tests |
| P77-T02 | done | P77-T01, P40, P50, P74 | Native holder list/count, Etherscan list/count billing, and bilingual Token-page browsing | OpenAPI, handler, billing, Web, browser, and compatibility tests |
| P77-T03 | done | P77-T02 | Runtime parity, bounded reindex, observability, operations, capacity workload, and v1 release-gate evidence | schema, runtime, monolith/split, deployment, documentation, license, security, and common gates |

## Acceptance

- [x] `holder@1` is always scheduled and uses one exact block-hash state RPC endpoint per bounded reconciliation operation.
- [x] Candidate discovery requires canonical high/verified ERC-20 Transfer facts continuously covered from block zero.
- [x] No holder row is public until every candidate balance and total supply agree at one still-canonical snapshot.
- [x] Forward Transfer participants are reconciled exactly; code/proxy generation changes and detected drift withdraw availability until a full baseline succeeds.
- [x] Reorg, replay, restart, lease expiry, and conflicting exact observations cannot expose partial or stale holder state.
- [x] Native list/count and Etherscan list/count return only current-tip complete PostgreSQL snapshots; empty success is proven, never inferred.
- [x] Native cursors bind chain, token, snapshot, holder epoch, and address boundary; address ordering is stable and balance ranking is absent.
- [x] Both compatibility actions are billable and retain existing pagination, response-size, authentication, and prepaid-credit boundaries.
- [x] The embedded Token page is bilingual, accessible, generated-client-only, and responsive at 390px.
- [x] Existing databases require explicit bounded `reindex --stage holder`; migrations and startup never enqueue unbounded history.
- [x] Monolith and split roles publish identical durable and public results, and the P70 reference workload includes holder reads plus reconciliation.
- [x] Every applicable focused, integration, race, generation, browser, schema, runtime, deployment, documentation, and common gate passes.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P77-T01, P77-T02, P77-T03.

Holder unit/race and PostgreSQL normal/race tests passed exact supply and
balance reconciliation, coverage, immutable facts, publication fences,
incremental updates, replay and reorg withdrawal. Generated API, billing,
frontend and all 26 then-current Chromium cases passed. Schema/runtime,
Hardhat, Foundry, deployment, security/license and common gates completed;
external compiler download failures required unchanged retries and do not
establish upstream availability. Reference-capacity evidence remains P70 work.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P77-authoritative-erc20-token-holders.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
