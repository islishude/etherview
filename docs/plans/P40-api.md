# P40 — API & Data Exports

Status: `done`

Browser subscription API and all/split delivery acceptance were completed
under P60-T20; notifications have no persisted history or replay.

## Outcome

The explorer exposes a versioned spec-first native REST API, cursor-stable
queries, explicit completeness, API-key quotas, real-time head/reorg events, and
the agreed Etherscan V2 subset, including authoritative withdrawals, exact
address holdings, EOA funding origin, per-block transaction counts, and advanced
from/to filters. The compatibility boundary remains an explicit allowlist with
no RPC proxy, event-derived balances, unbounded holder enumeration, or second
correctness store.

Public projections include snapshot-stable UserOperation browsing/search,
authoritative ERC-20 holder pages/counts, and optional consensus/blob metadata
with same-origin exact-hash downloads. SIWE-owned CSV exports preserve one
canonical snapshot with explicit admission, size, time, and coverage limits.

## References

- [ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)

- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0026](../decisions/ADR-0026-current-capability-status-and-numeric-canonical-tips.md)
- [ADR-0025](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0019](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0005](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [Architecture](../architecture/overview.md)
- [Etherscan V2 compatibility matrix](../architecture/etherscan-v2-compatibility.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0036: Endpoint-scoped mempool replacement observations](../decisions/ADR-0036-endpoint-scoped-mempool-replacements.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [ADR-0008: Versioned token observations and exact state reconciliation](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0018: API read-replica routing](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0035: User-owned scoped API keys](../decisions/ADR-0035-user-owned-scoped-api-keys.md)
- [ADR-0044: Prepaid API billing and x402 top-ups](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0022: Go-ethereum and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0045](../decisions/ADR-0045-erc4337-useroperation-index.md)
- [ADR-0046](../decisions/ADR-0046-authoritative-erc20-holder-reconciliation.md)
- [ADR-0051](../decisions/ADR-0051-watchlist-notifications-csv.md)
- [ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status | Depends on                         | Deliverable                                                                                                                                                                            | Verification                                                                                                             |
| ------- | ------ | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| P40-T01 | done   | P00, P10                           | OpenAPI 3.0.3 envelopes/scalars/errors/cursors, generated types, and capability modules with neutral query contracts and fail-closed dependencies                                      | contract, import-graph/source/lint, route/capability and startup tests                                                   |
| P40-T02 | done   | P40-T01, P20-T06                   | Status/config, block/transaction/address/search resources with narrow projections, normalized withdrawals                                                                              | handler/repository, corruption, PostgreSQL plan/benchmark, browser and runtime tests                                     |
| P40-T03 | done   | P20, P10, P40-T02                  | Token/NFT, contract/source/ABI, stats, pending and verification APIs, canonical NFT metadata and latest completed NFT-owner/statistics reads                                           | capability matrix, OpenAPI/generation, metadata/catalog, PostgreSQL canonicality/reorg and HTTP tests                    |
| P40-T04 | done   | P40-T01                            | API-key lifecycle, bounded anonymous/keyed local rate buckets and Redis fallback, CORS, health, metrics                                                                                | auth/rate, deterministic cardinality, Redis-fallback race, and metric tests                                              |
| P40-T05 | done   | P40-T02                            | Head/reorg SSE with per-write deadlines, non-blocking durable replay, cache-invalidation circuit breaking and typed mempool failure states                                             | HTTP/1.1/HTTP/2, cancellation, replay/reconnect/reorg, Redis/mempool race and runtime tests                              |
| P40-T06 | done   | P40-T02, P40-T03, P40-T04          | Agreed `/v2/api` Etherscan compatibility with authoritative withdrawals, holdings, funding, block counts, advanced indexed filters, bounded pagination/work windows, and prepaid reads | contract/golden, generation, PostgreSQL query-plan/coverage/reorg/state/race, billing, and runtime parity tests          |
| P40-T07 | done   | P20-T11, P40-T02, P40-T03          | Transaction-scoped logs, token transfers, trace/state changes, successful internal ETH transfers, separate protocol-native transfers and exact-block token decimals                    | contract/cursor, identity/reorg/capability, billing inventory, PostgreSQL and generation tests                           |
| P40-T08 | done   | P40-T02, P40-T07                   | Snapshot-stable address transaction/internal-call/ERC-20/NFT/protocol-native activity and canonical withdrawals with numeric index ordering                                            | contract/cursor, migration/query, reorg/capability, billing inventory and integration tests                              |
| P40-T09 | done   | P40-T02, P40-T05                   | Writer-authoritative version-fenced home snapshots and centralized SSE fanout with bounded cancellation-safe waits                                                                     | contract/generation, replay, version-floor/concurrency and integration tests                                             |
| P40-T10 | done   | P20, P40-T08                       | Snapshot-bound address origins, exact ERC-20 balances, and public wallet-chain configuration                                                                                           | contract, state, cursor, reorg, and configuration tests                                                                  |
| P40-T11 | done   | P30-T15, P40-T10                   | Published proxy detail/history and interaction reads on the latest complete canonical snapshot, plus anonymous verified-artifact reads                                                 | Exact hash/generation/cursor fences, refresh/replay/reorg, wallet authorization, generated contracts and PostgreSQL race |
| P40-T12 | done   | P40-T10                            | Protocol fields, block transactions/withdrawals/origin evidence, and exact-block execution/blob base-fee facts                                                                         | OpenAPI/generation, query/cursor/reorg, handler and home-stream tests                                                    |
| P40-T15 | done   | P40-T12                            | Endpoint-scoped mempool replacement observations and unified included/pending/replaced transaction detail API                                                                          | migration, PostgreSQL, OpenAPI, handler, writer-routing, integration, and runtime E2E tests                              |
| P40-T17 | done   | P20-T13, P30-T15, P40-T02          | Exact transaction-list Method projections from published execution identity and verified ABI selectors                                                                                 | OpenAPI/generation, exact identity/canonicality, selector collisions, pagination and integration tests                   |
| P40-T20 | done   | P20-T17, P40-T02, P40-T07, P40-T08 | Generated global/detail/transaction/address UserOperation resources, userOpHash search, and configuration-bound pagination                                                             | OpenAPI/generation, HTTP/query, coverage/cursor, raw-value, configuration, and runtime parity tests                      |
| P40-T21 | done   | P20-T18, P40-T03, P40-T06, P60-T13 | Native and billable Etherscan holder list/count from the latest completed coherent canonical snapshot                                                                                  | Snapshot lag/refresh/reorg, cursor, billing, compatibility, PostgreSQL race and production schema tests                  |
| P40-T22 | done   | P20-T19, P20-T20, P40-T12, P60-T13 | Consensus/blob metadata and slot provenance; session/CSRF-protected free browser content GET and key-only weight-5 V2 blob reads                                                       | Generated contracts, freshness/integrity, authentication, debit/release and all/split archive acceptance                 |
| P40-T23 | done   | P60-T18, P40-T08                   | SIWE/CSRF-authenticated address CSV exports, writer snapshot/coverage checks, bounded PostgreSQL admission, and exact complete files                                                   | OpenAPI/generation, snapshot/reorg, row/byte/time limits, JSON-only failure, concurrency, and runtime regressions        |
| P40-T24 | done   | P20-T21                            | Exact-block execution request API with typed fields and publication-bound pagination                                                                                                   | generated contracts, HTTP validation, PostgreSQL snapshots/cursors and runtime parity                                    |
| P40-T25 | done   | P40-T08, P40-T20                   | Published partial browsing across native explorer pages with publication-fenced cursors and explicit completeness                                                                      | PostgreSQL publication/replay/reorg, API, bilingual browser and all/split regression gates                               |
| P40-T27 | done   | P20-T27                            | Database-only ENS publications, name details/search and transaction-grouped history with independent profile/resolution validity and bounded event cursors                             | Read-only HTTP/query, unknown/expired names, publication/canonical/association snapshots, generation and PostgreSQL race |
| P40-T33 | done   | P40-T07, P40-T25                   | Indexed address event logs, historical event directory, topic0–3 search and publication-fenced pagination                                                                              | Publication/replay/reorg, partition, indexed-query, generation and production all/split checks                           |
| P40-T34 | done   | P40-T33                            | Inclusive address-log block ranges and range-scoped event directory, coverage and cursors                                                                                              | Inclusive ranges, revision/cursor isolation, indexed plans, integration race and all/split runtime                       |
| P40-T35 | done   | P40-T34                            | Multi-topic AND filters and publication-bound historical event definitions with recursive indexed ABI metadata                                                                         | Typed encoding, historical identity, pagination, database/browser and all/split acceptance                               |

## Acceptance

- [x] Spec-first generated contracts use exact strings for unsafe integers,
      stable typed errors and explicit canonicality/coverage/capability states.
- [x] Snapshot-bound cursors reject reorg/replay/publication changes; ordinary
      incomplete lists expose published records without claiming proven absence.
- [x] Initial/refresh proxy, holder, NFT owner and statistics reads select the
      latest completed canonical publication. Holder cursors freeze a coherent
      snapshot with continuous coverage; unfinished head work does not hide it.
- [x] Transaction/address/protocol/native-transfer resources preserve exact block
      and publication identity, bounded projections and explicit provenance.
- [x] Mempool replacement reads use durable endpoint-scoped evidence, never RPC fallback.
- [x] Compatibility allowlist, operation catalog and generated billing bounds
      agree; selectors, work windows, exact state, coverage and indexed queries
      fail closed. Only successful priced responses commit credit debits.
- [x] Quotas stay bounded during Redis failure; SSE/home replay and shutdown
      retain version floors, per-write deadlines and cancellation bounds.
- [x] UserOperations bind configuration and publication; holder list/count retain
      exact complete-state evidence, address order and prepaid compatibility policy.
- [x] ENS detail/search/history are writer-backed read-only snapshots with
      independent profile/resolution validity and bounded transaction/event pages.
- [x] Consensus/requests/blob metadata are PostgreSQL-only. Content reads close
      snapshots before object access and recheck integrity/publication. Browser GET
      is session/CSRF-protected and free; V2 requires an API key and weight-5 billing
      with existing operator/disabled-billing exceptions.
- [x] CSV retains SIWE/CSRF, one writer snapshot and continuous coverage; limits
      remain 31 days, 10,000 rows, 16 MiB, 15 seconds, five starts/minute/account,
      one active export/account and four/deployment. Errors precede CSV bytes.
- [x] NFT metadata selects canonical immutable content, bounds inert fields and
      safe links, and preserves explicit stale-content identity. Media proxy
      authorization and SSRF validation remain independent.
- [x] Monolith/split API components, writer fences and public results agree.

- [x] Contract event logs retain raw Core coverage, exact topic0–3 AND/keyset filters,
      historical definition membership with recursive indexed ABI metadata and
      independent ABI coverage. Definition filters reject non-indexed positions;
      replay/reorg/reattach update the compact event directory atomically.

## Current Blockers

The current-chain ENS feature and representative-publication milestones are
complete with [new acceptance evidence](../reviews/ens-current-chain-acceptance.md).
Full directory coverage remains separately reported deployment progress; P70
retains its release security, capacity, recovery and other existing gates.

## Evidence

P40-T01/P40-T02/P40-T03/P40-T04/P40-T05/P40-T06/P40-T07/P40-T08/
P40-T09/P40-T10/P40-T12/P40-T15/P40-T17 passed generated-contract,
handler/golden, cursor/query and PostgreSQL normal/race checks at delivery. Coverage includes exact scalars and hash identity,
canonical publication, authenticated quotas, SSE/home version fences, pending
replacement, bounded compatibility queries and prepaid debit/release.
Narrow projections and indexed work windows passed corruption/query-plan and
local benchmark checks; those measurements do not establish P70-T04 capacity.

P40-T25 passed publication-island, backfill/replay/reorg, browser, schema and
all/split production tests. P40-T11/P40-T21 and the NFT/statistics reads in P40-T03
passed lag/refresh/reorg, same-hour rollup retention, generation and cursor
regressions, focused Go/PostgreSQL race, 831 frontend and 221 browser tests.
Wallet read/write regressions preserve operation-local identity fences.
These were local results; the running Preview and remote CI were not updated.

P40-T20/P40-T21 UserOperation and holder native/compatibility APIs passed configuration,
coverage, billing and real-Anvil topology checks. P40-T23 passed concurrent
snapshot/reorg/admission, row/byte/time limits, JSON-only failure and real SIWE
export in both production layouts. P40-T24 passed exact-block/type/publication
pagination and disabled/preactivation handling without Beacon calls.

P40-T27 passed generated contracts, read-only unknown/expired/name detail/search,
independent profile validity and transaction/event history pagination regressions.
See the [current-chain](../reviews/ens-current-chain-acceptance.md) and
[profile extension](../reviews/ens-current-chain-acceptance.md#registration-profile-local-acceptance) reports for local
and representative Mainnet limits. New heads preserve history cursors; reorgs
and late associations reject them. Local fixtures do not prove complete history.

P40-T22 passed slot provenance, metadata/freshness, exact-hash integrity,
session/CSRF browser GET and key-only weight-5 V2 debit/release checks. Shared
commands, revision/image and limits are recorded once in
[P20 archive acceptance](P20-enrichment.md#blob-access-and-archive-update).
P30-T35/P30-T138 own other shared runtime evidence. Feature results do not close
P70's final release gates.

P40-T33 passed API/filter/cursor regressions, full managed PostgreSQL integration
race, generated contracts and Go lint, schema and production all/split tests.
Final query refinements additionally passed the address-log and partition race
regressions, including generated topic constraints, DEFAULT evacuation, duplicate
publication, withdrawal, proxy emitters and same-hash reattach/replay.
Local EXPLAIN ANALYZE/BUFFERS used 30,003 logs across partitions: address and
sparse topic3 pages used ordered index ranges (0.298/0.330 ms), with 30,851,072
bytes of log indexes and 689–723 ms per 10,000 indexed inserts. A 508-log repeated
event fixture read 26 signature candidates through the projection index
(0.524 ms); directory reads avoided raw logs/decodings (0.218 ms). These are
local fixture measurements, not P70 capacity or live-chain coverage evidence.

P40-T34 passed inclusive/open/future/uint256 range, four-topic and directory
regressions, interval-scoped revision/cursor checks, full PostgreSQL race and
production schema/all/split gates. Cross-partition plans retained bounded index
reads and partition pruning; directory reads used published block contributions.
No new schema or startup backfill was added. Measurements remain local fixtures.

P40-T35 passed on 2026-11-05 in the working tree based on `e8886e54`
(macOS arm64, PostgreSQL 18, Docker linux/arm64). `make check`,
`make test-integration-race` (all seven packages), `make test-schema-e2e` and
`make test-runtime-e2e` passed, including monolith/distributed and x402 checks.
The production image was
`sha256:f2e457a65a7df9cc82617342abd1c09f8fd2730294dea3ae31319694ae33c3dc`.
Regressions cover AND predicates before pagination, cursor binding, indexed-only
positions, historical layouts sharing a signature, publication/reorg/replay and
ordered cross-partition query plans. Independent Solidity logs pass through the
real ABI worker to verify definition identity and exact result membership.
These are local feature gates, not P70 production capacity or live-chain coverage.
