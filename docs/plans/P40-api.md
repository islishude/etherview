# P40 — API & Data Exports

Status: `done`

Browser subscription API and all/split delivery acceptance were completed
under P60-T20; notifications have no persisted history or replay.

## Outcome

P40-T26 delivers the ADR-0058 database-only ENS cutover for address detail,
batch names and search, including immutable publication snapshots and source
status/events.

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
- [ADR-0041](../decisions/ADR-0041-snapshot-stable-ens-primary-names.md)
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

| ID      | Status | Depends on                         | Deliverable                                                                                                                                                                            | Verification                                                                                                                                     |
| ------- | ------ | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| P40-T01 | done   | P00, P10                           | OpenAPI 3.0.3 envelopes/scalars/errors/cursors, generated types, and capability modules with neutral query contracts and fail-closed dependencies                                      | contract, import-graph/source/lint, route/capability and startup tests                                                                           |
| P40-T02 | done   | P40-T01, P20-T06                   | Status/config, block/transaction/address/search resources with narrow projections, normalized withdrawals, ENS forward search and generated batch address-name snapshots               | handler/repository, corruption, exact ENS generation/cursor/reorg, PostgreSQL plan/benchmark, browser and runtime tests                          |
| P40-T03 | done   | P20, P10, P40-T02                  | Token/NFT, contract/source/ABI, stats, pending and verification APIs, including bounded canonical NFT metadata and explicit stale-content identities                                   | capability matrix, OpenAPI/generation, metadata/catalog, PostgreSQL canonicality/reorg and HTTP tests                                            |
| P40-T04 | done   | P40-T01                            | API-key lifecycle, bounded anonymous/keyed local rate buckets and Redis fallback, CORS, health, metrics                                                                                | auth/rate, deterministic cardinality, Redis-fallback race, and metric tests                                                                      |
| P40-T05 | done   | P40-T02                            | Head/reorg SSE with per-write deadlines, non-blocking durable replay, cache-invalidation circuit breaking and typed mempool failure states                                             | HTTP/1.1/HTTP/2, cancellation, replay/reconnect/reorg, Redis/mempool race and runtime tests                                                      |
| P40-T06 | done   | P40-T02, P40-T03, P40-T04          | Agreed `/v2/api` Etherscan compatibility with authoritative withdrawals, holdings, funding, block counts, advanced indexed filters, bounded pagination/work windows, and prepaid reads | contract/golden, generation, PostgreSQL query-plan/coverage/reorg/state/race, billing, and runtime parity tests                                  |
| P40-T07 | done   | P20-T11, P40-T02, P40-T03          | Transaction-scoped logs, token transfers, trace/state changes, successful internal ETH transfers, separate protocol-native transfers and exact-block token decimals                    | contract/cursor, identity/reorg/capability, billing inventory, PostgreSQL and generation tests                                                   |
| P40-T08 | done   | P40-T02, P40-T07                   | Snapshot-stable address transaction/internal-call/ERC-20/NFT/protocol-native activity and canonical withdrawals with numeric index ordering                                            | contract/cursor, migration/query, reorg/capability, billing inventory and integration tests                                                      |
| P40-T09 | done   | P40-T02, P40-T05                   | Writer-authoritative version-fenced home snapshots and centralized SSE fanout with bounded cancellation-safe waits                                                                     | contract/generation, replay, version-floor/concurrency and integration tests                                                                     |
| P40-T10 | done   | P20, P40-T08                       | Snapshot-bound address origins, exact ERC-20 balances, and public wallet-chain configuration                                                                                           | contract, state, cursor, reorg, and configuration tests                                                                                          |
| P40-T11 | done   | P30-T15, P40-T10                   | Public proxy detail, canonical upgrade and initialization histories, and anonymous free verified-artifact reads                                                                        | OpenAPI, generated contract, writer-query, cursor, auth, x402, and integration tests                                                             |
| P40-T12 | done   | P40-T10                            | Protocol fields, block transactions/withdrawals/origin evidence, and exact-block execution/blob base-fee facts                                                                         | OpenAPI/generation, query/cursor/reorg, handler and home-stream tests                                                                            |
| P40-T15 | done   | P40-T12                            | Endpoint-scoped mempool replacement observations and unified included/pending/replaced transaction detail API                                                                          | migration, PostgreSQL, OpenAPI, handler, writer-routing, integration, and runtime E2E tests                                                      |
| P40-T17 | done   | P20-T13, P30-T15, P40-T02          | Exact transaction-list Method projections from published execution identity and verified ABI selectors                                                                                 | OpenAPI/generation, exact identity/canonicality, selector collisions, pagination and integration tests                                           |
| P40-T20 | done   | P20-T17, P40-T02, P40-T07, P40-T08 | Generated global/detail/transaction/address UserOperation resources, userOpHash search, and configuration-bound pagination                                                             | OpenAPI/generation, HTTP/query, coverage/cursor, raw-value, configuration, and runtime parity tests                                              |
| P40-T21 | done   | P20-T18, P40-T03, P40-T06, P60-T13 | Native holder list/count and billable Etherscan list/count from complete current-tip snapshots                                                                                         | OpenAPI/generation, handler, cursor/reorg, billing, compatibility, and PostgreSQL tests                                                          |
| P40-T22 | done   | P20-T19, P20-T20, P40-T12          | Generated consensus/blob metadata, consensus-first slots with execution fallback, and exact-hash binary downloads                                                                      | generated contracts, HTTP/query, integrity, freshness, and runtime tests                                                                         |
| P40-T23 | done   | P60-T18, P40-T08                   | SIWE/CSRF-authenticated address CSV exports, writer snapshot/coverage checks, bounded PostgreSQL admission, and exact complete files                                                   | OpenAPI/generation, snapshot/reorg, row/byte/time limits, JSON-only failure, concurrency, and runtime regressions                                |
| P40-T24 | done   | P20-T21                            | Exact-block execution request API with typed fields and publication-bound pagination                                                                                                   | generated contracts, HTTP validation, PostgreSQL snapshots/cursors and runtime parity                                                            |
| P40-T25 | done   | P40-T08, P40-T20                   | Published partial browsing across native explorer pages with publication-fenced cursors and explicit completeness                                                                      | PostgreSQL publication/replay/reorg, API, bilingual browser and all/split regression gates                                                       |
| P40-T26 | done   | P20-T26                            | Database-only ENS detail/batch/search readers, immutable publication tokens, source status and durable names SSE contracts                                                             | Read-only SQL/no ENS clients or enqueue, negative versus missing data, writer routing, expiry/reorg/cursors, OpenAPI generation and stream tests |

P40-T06's authoritative compatibility reads build on P20, the native API,
P60-T09 user authentication/scoped keys, and P60-T13 prepaid read billing.
They include the operation catalog and generated billing bound, exact canonical
ERC-20/ERC-721
holding pages, reader/writer routing, and production runtime parity. Their
acceptance is independent of P60's still-blocked live-payment gate.

## Acceptance

### Planned ENS acceptance

- [x] P40-T26 changes OpenAPI first and regenerates Go/SPA outputs. All three
      ENS request paths read published views without ENS RPC/gateway access or
      writes to ENS candidates, tasks, observations or snapshot tables.
      Authentication, billing and accounting retain their separate contracts.
- [x] Unindexed, synchronizing, pending, stale, unsupported and failed results
      have stable codes; missing rows cannot become `not_found` or permit custom
      fallback. Search filters unavailable name documents without hiding
      independently matching non-name sources.
- [x] Writer-backed read-only transactions validate retained publication tokens;
      issuance requires no insert, and retention cannot extend result freshness
      or canonicality. Existing cursors never silently switch generations.
- [x] Source/protocol coverage, targets, publication progress, validity deadlines
      and safe scalar encoding are explicit. Durable `names` events share the
      existing SSE stream and replay/ordering rules.
- [x] The read boundary uses neutral contracts rather than worker packages;
      request-time name resolution and snapshot creation are removed together.

### Delivered capability acceptance

- [x] Requests pages read only published PostgreSQL data, preserve exact decimal
      amounts/indices, distinguish collection states, and bind pagination to the
      block, type filter and publication identity.
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
- [x] The compatibility allowlist, operation catalog, generated billing bound,
      and golden inventory agree; current action coverage belongs to the matrix.
- [x] Every compatibility action validates one unambiguous selector mode and
      canonical billing resource.
- [x] Empty withdrawals, funding, counts, or holdings are authoritative only
      after their required continuous coverage proof.
- [x] Address holdings return only exact current state, never event-derived
      balances; dense pages stay within a fixed 1,000-candidate work bound and
      fail closed rather than returning partial results.
- [x] `fundedby` accepts current EOAs only and excludes self-transfers,
      withdrawals, fee recipients, failures, and reverted traces.
- [x] Priced compatibility reads retain user debit, operator bypass, logical-failure
      release, and bounded-response behavior.
- [x] Monolith and split API roles use the same components, reader routing,
      writer fences, and public results.
- [x] Compatibility database work is bounded independently of response size;
      miner, timestamp, direction, and topic predicates have verified indexable plans.
- [x] Block, transaction, and compatibility list/detail projections use exact
      narrow fields and normalized withdrawals instead of repeating full block Raw
      values; malformed or inconsistent projections fail closed.
- [x] Inactive anonymous and authenticated local rate buckets stay bounded,
      including during Redis loss, without evicting active or exhausted quota state.
- [x] UserOperation global/address pagination binds canonical publication revision
      and configuration digest; absent detail is not authoritative across a gap.
      Raw operation bytes, decimal-string quantities, and typed errors are preserved.
- [x] Native browsing lists expose published records across gaps, distinguish
      complete/partial/pending ranges, and reject stale cursors after late
      publication or replay. Exact state retains its existing evidence requirements.
- [x] Browsing pages retain available records with one-line bilingual notices;
      durable publication events refresh data and pagination without a new head.
- [x] Native and Etherscan holder reads require current-tip complete PostgreSQL
      snapshots. Empty success is proven; cursors bind chain, token, snapshot,
      holder epoch, and address. Pages are address-ordered without balance ranking.
- [x] Both holder compatibility actions retain prepaid billing, authentication,
      pagination, and response bounds.
- [x] Consensus slots take precedence only with a verified association, otherwise
      execution-header slots remain the fallback with explicit source provenance.
      Metadata is PostgreSQL-only; downloads close snapshots before object access,
      verify bytes, and recheck publication without exposing credentials or URLs.
- [x] CSV requires SIWE and CSRF, continuous Core/applicable Token coverage, and
      one writer repeatable-read snapshot. Exact values and formula protection
      survive export; errors precede CSV headers or bytes and never return partial success.
- [x] Export limits are 31 days, 10,000 rows, 16 MiB, and 15 seconds; admission
      allows five starts/minute/account, one active export/account, and four per
      deployment. Expiring reservations recover crashes without charging API credit.
- [x] The API returns only the newest retained NFT metadata observation whose
      exact height and hash remain canonical; orphan-only history is distinct
      from a missing observation.
- [x] Available documents expose bounded name, description, and ordered scalar
      traits without JavaScript number coercion, raw JSON, metadata source URIs,
      animation URLs, or external URLs.
- [x] HTTPS image destinations are syntactically safe and IPFS destinations use
      the configured HTTPS gateway; unsupported or unsafe destinations never
      become links.
- [x] The authenticated `/media` proxy, its API-key requirement, and its
      SSRF/content validation remain unchanged.
- [x] Pending or failed refreshes retain the prior canonical available content
      with explicit latest/content observation identities and a bilingual stale
      warning; reorg removes the warning and restores the prior version.
- [x] Home snapshots and browser replay are version-fenced and reorg-aware.
- [x] Capability modules reject missing dependencies; SSE per-write deadlines,
      non-blocking replay and cache circuit breaking keep shutdown/cancellation
      bounded. Home waits enforce a version floor without changing page contracts.
- [x] ENS forward search and batch address-name results bind retained generations
      and pagination snapshots; custom-chain reorg invalidates detached identities.

- [x] Genesis account/origin reads require authenticated canonical block zero.
      Historical analytics expose bounded snapshot-consistent exact values and
      pending dirty ranges. Capability status uses current durable/probed evidence;
      exact state and ABI selection order by numeric block height, never text.
- [x] Native-transfer transaction/address pages bind canonical writer snapshots
      and published stage coverage; a gap is not empty success. EIP-7708 log
      decoding joins the exact published transfer in the same snapshot, rejects
      corrupt receipt/row mismatches and never falls back to contract ABI.

## Current Blockers

P40-T26 is complete. No additional external API blocker is established;
P30-T148 owns live-source acceptance. P60/P70 retain independent release blockers.

## Evidence

P40-T26: read-only writer transaction regressions cover missing names without
enqueue, immutable empty snapshots, dependency withdrawal and search filtering.
The old request-time service/cache schema is removed. Targeted HTTP/event and
PostgreSQL race tests plus generation, Go lint, docs and plan checks pass.
Production/browser acceptance is recorded in P30-T147/P50-T80; live acceptance
is recorded separately in P30-T148.

P40-T25 passed `make check`, full PostgreSQL race integration, all 153 browser
regressions, fresh production-image schema acceptance and all/split runtime
acceptance locally on ARM64. Publication islands, late backfill, replay,
canonical reorg, compact bilingual states and pagination refresh are covered.
The coordinated Node 26.13.0 upgrade passed local SEA and production checks;
native AMD64 and the full release compiler matrices remain CI acceptance.

P40-T24 passed generated contracts and handler bounds, PostgreSQL race tests
for exact-block/filter/publication cursors, disabled/preactivation states and
reads without Beacon access. Production runtime and real Preview confirmed
writer routing and both role layouts. Pending transient retries expose no
unverified request rows.

P40-T02/P40-T03/P40-T07/P40-T08/P40-T10 retain authenticated Genesis origins,
analytics, numeric canonical selection and current capability coverage.
Native-transfer pagination and protocol-log projection passed generated API,
PostgreSQL, publication/corruption/reorg and runtime parity tests locally.

P40-T02/P40-T03 retain generated ENS and canonical NFT metadata contracts,
HTTP/catalog and PostgreSQL normal/race acceptance, including snapshot reuse,
orphan-only states, bounded projections and stale-content identities.
P40-T01/P40-T05/P40-T09 retain import/route/startup, HTTP shutdown/SSE,
version-fenced home concurrency, generation and production parity coverage.
These are delivery-revision results; this consolidation reruns documentation gates.

P40-T20 passed generated-contract, coverage/cursor/configuration, and HTTP/query
checks plus real-Anvil monolith/split durable/public parity. P40-T21 passed
native/compatibility holder, billing, exact-state, and reorg checks with
PostgreSQL normal/race acceptance.
P40-T22 passed generated contracts and HTTP/query slot-provenance, archive-status,
exact-hash retrieval, and freshness tests. P40-T23 passed exact repeatable-read
CSV, concurrent reorg/admission, 10,000-row/16 MiB boundaries, and JSON-only
failures. Real SIWE export passed production images in both role layouts.
These are historical feature checks; shared aggregate evidence remains in
P30-T35 and P30-T138, and no release gate is closed by this consolidation.

Retained completed milestones: P40-T01, P40-T02, P40-T03, P40-T04, P40-T05, P40-T06,
P40-T07, P40-T08, P40-T09, P40-T10, P40-T11, P40-T12, P40-T15, P40-T17, P40-T20,
P40-T21, P40-T22, P40-T23.

Generated contracts, handler/golden compatibility tests, query/cursor tests
and PostgreSQL normal/race integration passed at the feature revisions.
Acceptance covered exact numeric and block identity, canonical snapshot
pagination, authenticated quotas, durable SSE/reconnect, public verified
artifacts, protocol fields, balances, withdrawals and selector provenance.
Mempool included/pending/replaced reads also passed production runtime parity.
Current action coverage is maintained in the compatibility matrix rather than
a historical action count.

Authoritative compatibility reads passed handler goldens, generated contracts,
real PostgreSQL coverage/reorg/query fixtures, and Hardhat provider compatibility.
Exact ERC-20/ERC-721 holdings passed candidate/batch bounds, zero caching,
restart reuse, and orphan exclusion tests. `make check`, integration/race, and
production runtime/x402 parity passed; later regressions protect latest token
classification and candidate-scoped funding provenance.

Compatibility work windows and indexed predicates passed handler goldens,
PostgreSQL query-plan and race checks. Narrow projections passed query/model
corruption, API/browser/runtime parity, and reproducible public-query benchmarks.
Local rate buckets passed deterministic cardinality, Redis-fallback race, and
metric tests. These local baselines do not establish P70-T04 reference capacity.

[API completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P40-api.md#evidence).
These are historical results, not tests rerun by this plan consolidation.
