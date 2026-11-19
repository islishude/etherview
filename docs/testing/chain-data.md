# Chain data acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [ENS acceptance](#ens-acceptance)
- [Amsterdam acceptance](#amsterdam-acceptance)
- [Consensus and blob archive acceptance](#consensus-and-blob-archive-acceptance)
- [Execution requests acceptance](#execution-requests-acceptance)
- [Blob content access and S2 regressions](#blob-content-access-and-s2-regressions)
- [Address event logs](#address-event-logs)

## ENS acceptance

The accepted [background ENS design](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)
has a persistence foundation in P20-T27. Its `TestENSSync` PostgreSQL regressions
cover source identity, coverage gaps, immutable facts and publications,
idempotency, retry exhaustion, lease/generation/dependency fencing, local
canonical detach/reattach, and read-only unknown-name lookups. Run them through
`go run ./e2e/cmd/testintegration -packages ./internal/integration -race -run '^TestENSSync'`;
they also run in the maintained full integration/race suites. Schema changes
continue to require the production-image `make test-schema-e2e` gate.

`TestENSCollector` adds PostgreSQL race regressions for autonomous
discovery, ordered versions/deletions/renewal, restart and concurrent cursors,
history/deployment gaps, core attachment changes and reorg/reattachment. Run the combined suite with
`go run ./e2e/cmd/testintegration -packages ./internal/integration -race -run '^TestENS(Sync|Collector)'`.
The RPC fixture is local evidence, not live Mainnet acceptance.

The ENS suite includes real pinned v2 lifecycle/projection fixtures,
background worker/publication and database-only reader tests. Run the combined
PostgreSQL race suite with
`go run ./e2e/cmd/testintegration -packages ./internal/integration -race -run '^TestENS'`.
The v2 regressions cover unknown subregistry links without source-wide shutdown,
untrusted-emitter isolation, link repair, and bounded graph cursor progress across
restarts. Search regressions exercise invalid ENS names through the query reader
and HTTP handler, preserving typed client errors without database access.
It includes source/canonical fences, immutable empty snapshots, HTTP-independent
candidate discovery, catalog withdrawal and bounded idempotent import/replay.
`make test-runtime-e2e` runs the production image in all/split mode with real
pinned v1/v2 registration, migration, factory and resolver transactions on the
same isolated Anvil chain consumed by core sync. `make test-preview-contracts` verifies the shipped `.test`
genesis, autonomous publication before name HTTP reads and split/all restarts.
Neither local RPC identity fixtures nor contract deployment establish actual
Mainnet activation or independent reconciliation. P30-T150/P30-T151 retain
[separate acceptance evidence](../reviews/ens-current-chain-acceptance.md), including
exact source/image identities, core-to-database/API publication, declared scan
windows, candidate provenance and independent comparisons. Bounded samples do
not satisfy full historical coverage; direct resolver success alone does not
satisfy the publication gate.

`make test-e2e` covers the generated-client ENS contract, existing single SSE
stream, explicit manual publication replacement, exact address disclosure, expiry
without requests, bilingual accessibility and mobile layout. Unit tests cover
multi-chunk atomicity and stale responses. API tests reject ENS calls and writes,
including enqueue and snapshot inserts, without changing authentication/billing.

These gates include exact independent registration
profiles, canonical known-name search and `/api/v1/names/{name}`. Real-contract
profile tests cover registrant/controller separation, wrapper expiry, grace,
renewal, regeneration, migration and re-registration. PostgreSQL tests require
partial profile publication before forward resolution, read-only detail/search,
independent invalidation, retained expired names and canonical discovery fences.
Runtime/Preview exercise the same profile worker and generated detail contract.
Earlier Mainnet samples do not establish acceptance of the new profile fields.

ENS operation history adds `TestENSHistory` and event-time shared-resolver
assertions to the same `^TestENS` PostgreSQL race suite. Tests cover grouped
transactions, separately bounded event pagination, unknown names, partial
coverage, new-head cursor stability and immediate core-reorg rejection. Runtime
all/split and Preview contracts assert public history from actual fixture logs.
`name-history.spec.ts` verifies bilingual light/dark mobile and desktop layouts,
keyboard expansion, inert record text, lazy event requests and the single SSE
stream. Fixture coverage remains distinct from complete live history.

## Amsterdam acceptance

`make test-preview-metadata` also sends a real EIP-7708 ETH transfer on the
pinned Reth Amsterdam Preview chain, authenticates its BAL against the header,
compares indexed native balances to exact EIP-1898 reads in all/split roles,
and checks exact transaction/address API
amounts above JavaScript's safe integer range. It then stops the split workers
in its disposable project, recreates the API as `serve --roles=all`, and checks
newly mined transfers through the same contracts. The normal production runtime,
Hardhat and Foundry suites continue to cover their pinned Prague fixtures.

The chainbundle test retains a real Geth contract-creation block whose charged
receipt gas exceeds the header's maximum execution/state dimension. Integration
regressions cover receipt-based fees, native-transfer publication fences, replay,
canonical detach/reattach and scope-bound cursors. Browser tests cover exact
slot values, optional header fields and bilingual native-transfer tabs.

## Consensus and blob archive acceptance

P20, P30, P40, and P50 cover Beacon transport limits, network identity,
root/hash binding, Bellatrix through Gloas projections, slot precedence,
historical gaps, KZG and
sidecar validation, independent object readback, immutable conditional writes,
lease/generation publication, reorg/replay and role parity. Run focused tests
in `internal/consensus`, `internal/accelerator`, `internal/enrich`,
`internal/query`, `internal/httpapi`, `internal/config`, and `internal/app`.
PostgreSQL fixtures belong to `internal/integration`; RustFS exercises the
archive transport in `make test-rustfs`. Browser coverage exercises disabled,
pending, unavailable and successful consensus/blob views.

Required aggregate acceptance includes `make check`, `make source-check`,
`make test-integration-race`, `make test-e2e`, `make test-rustfs`,
`make test-schema-e2e`, and `make test-runtime-e2e`. Protocol fixtures and local
object-store tests do not establish real Beacon historical availability or a
production backup/restore outcome; record live-node evidence separately.

Consensus regressions exercise production PostgreSQL workers for 404, unsupported
endpoints/forks and pre-Merge outcomes, body-root tampering and private presets,
archive-only replay after upstream pruning, and disabled-period reorg discovery.
Cursor-generation races must not overwrite core reorg rewinds. These checks do
not substitute for live-node retention and production backup/restore acceptance.

## Execution requests acceptance

The request suite covers all five EIP-7685 types (including pinned EIP-8282
builder records), uint64 precision, type/list ordering, empty commitments,
malformed/preset-limited input, Beacon/body/envelope identity and request-root/hash
mismatches. PostgreSQL regressions cover publication, filters/cursors, reorg
retention, generation changes, pruned-source replay and corrupted retained data.
The disabled-period discovery regression includes requests cursors. Browser
coverage checks exact fields, type filtering, deep links, bilingual mobile
layout and accessibility against the embedded artifact.

Run focused consensus/enrich/query/httpapi/app/maintenance and frontend tests,
then `make check`, `make test-integration-race`, `make test-e2e`,
`make test-schema-e2e`, `make test-runtime-e2e`, `make test-rustfs`, and
`make test-preview-consensus`. Preview independently reads real Lighthouse
payload envelopes and compares the request commitment with the execution header
and public API in both split and all-role layouts. Synthetic nonempty five-type
fixtures do not establish real builder transactions or public historical retention.

## Blob content access and S2 regressions

Cover anonymous content rejection, removal of browser POST, session-only
browser GET, optional matching Origin, same-origin Fetch Metadata and mandatory CSRF,
successful token-free access with Turnstile enabled or disabled, and rejection
before archive access for missing sessions, invalid Origin or CSRF.
V2 blob tests cover key-only access, exact hash/index validation, weight-5
pricing, successful debit, failed-read release, insufficient credit and free
operator/disabled-billing exceptions. The existing PostgreSQL prepaid tests
remain the authority for concurrent balance reservations.

Archive tests round-trip zero, sparse and incompressible blobs and reject
malformed, oversized, truncated, wrong-length and wrong-digest objects. Retain
conditional-create/readback, replay, reorg and role-parity tests. Preview real
blob tooling reads V2 with an operator key and independently decompresses S3
objects before comparison. Browser tests must assert a single fetch for both
preview-then-download and collapse/reopen, show the download icon only in
expanded details, and discard content on identity changes. Apply the
consensus/blob acceptance gates above.

## Address event logs

P40-T33 and P50-T86 require positioned topic and historical-signature API,
frontend and browser regressions. Run `TestAddressLogs`,
`TestABIStageBindsPriorityRangeAndForkIdentity`, and
`TestPostgresPartitionLifecycle` with the managed PostgreSQL integration runner
and `-race`. The address-log query-plan case uses 30,003 logs across partitions,
selective topic3 values and actual `EXPLAIN (ANALYZE, BUFFERS)` output without
forcing the planner. The published-directory fixture adds 508 logs with repeated,
overloaded and anonymous signatures, verifies ordered projection-index reads,
and checks that directory reads do not scan raw logs or historical decodings.
Partition fixtures cover both derived tables and generated topics; same-hash
reattachment must keep names withdrawn until replay publishes a new generation.
Record index footprint and indexed insert time separately from production capacity.
Run generation, Go/frontend lint, documentation/plan, browser, schema and
production all/split runtime gates before closing the milestones.

P40-T34/P50-T87 extend those checks with inclusive/one-sided/future block ranges,
range-scoped name directories and cursor revisions. Range tests cover every
positioned topic and ABI replay outside the selected interval. Query-plan fixtures
retain cross-partition ranges and exact identity materialization. Frontend tests
cover draft-only edits, URL restoration, in-popup search/loading/pagination,
keyboard focus, late responses and expandable summaries. Production all/split
checks compare exact-block logs and directory contribution membership.

P40-T35/P50-T88 add AND combinations, definition-scoped indexed positions and
recursive ABI inputs. `TestAddressLogsSolidityIndexedDefinitions` publishes the
independent Solidity log fixture through the real ABI worker and queries exact
result membership. `web/src/contracts/eventTopics.test.ts` compares browser
encoding with those emitted topics, including empty and nested values. Regenerate
the fixture with `go run ./e2e/cmd/eventfixture` using the pinned compiler; its
Solidity source remains in `internal/enrich/testdata/indexed-events/`; the shared
JSON (including runtime bytecode) lives in `web/src/contracts/fixtures/`. Browser tests also retain
Read/Write Contract editor regressions, add/remove focus, typed values, hash-only
URL restoration and bilingual mobile acceptance. These local fixtures do not
establish P70 production capacity.

## DeFi positions

Run `go test ./internal/defi ./internal/defiread ./internal/httpapi ./internal/app`
for adapter arithmetic, strict manifests, cursors, HTTP errors and role ownership.
Run `go run ./e2e/cmd/testintegration -packages ./internal/integration -race -run '^TestDeFi'`
for canonical publication, immutable values, read-only queries, worker restart,
lease takeover and incremental background refresh. Browser coverage is in the
address DeFi unit and Playwright suites. These are local fixtures; P30-T158
retains separate real-contract production and live deployment acceptance.
