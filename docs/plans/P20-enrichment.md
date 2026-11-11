# P20 — Enrichment & Durable Archives

Status: `done`

## Outcome

Core facts are asynchronously enriched with ABI decoding, contract/proxy
versions, ERC-20/721/1155 activity, normalized call traces, state reconciliation,
search documents, and rollup statistics without delaying core readiness.

The enrichment domain also owns configured ERC-4337 UserOperations,
authoritative ERC-20 holder snapshots, optional Beacon observations,
verified execution request sets, and
independently enabled durable blob archives. PostgreSQL owns identity and
publication; immutable blob bodies live in a separate durable object bucket.
Consensus observations never drive execution canonicality, finality, or readiness.

UserOperation support covers explicitly configured EntryPoint v0.6-v0.9
using exact stored bundles without external RPC.
Pending Bundler mempools, submission/simulation, wallet-specific call/signature
interpretation, automatic EntryPoint discovery, and nested EntryPoint calls
remain outside scope. Holder enumeration is exact current ERC-20 state, with
no historical/balance-ranked holder pages or approximate event-delta balances.

ENS consumes current-chain core facts under enrich. Exact resolver observations
can publish before directory coverage completes; event-derived results require
complete relevant history. NFT metadata retains its own refresh boundary.

## References

- [ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)

- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0025](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0005](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0008: Versioned token observations and exact state reconciliation](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0011: Snapshot search, statistics, and bounded adapters](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0012: Lease-fenced derived publication](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [ADR-0045](../decisions/ADR-0045-erc4337-useroperation-index.md)
- [ADR-0046](../decisions/ADR-0046-authoritative-erc20-holder-reconciliation.md)
- [ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status | Depends on                     | Deliverable                                                                                                                                                                    | Verification                                                                                                                     |
| ------- | ------ | ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- |
| P20-T01 | done   | P10                            | Durable bounded stages, retry budgets, reattach-safe generations and lease-fenced atomic output/journal/job publication                                                        | Crash/replay, stale-worker, publication-window, lease and retry regressions                                                      |
| P20-T02 | done   | P20-T01                        | ABI selector/event/error decoding, confidence, and global ABI/Trace resource budgets including builtin and large-batch boundaries                                              | ABI/trace fixture/fuzz and adversarial-budget tests                                                                              |
| P20-T04 | done   | P20-T01                        | ERC-20/721/1155 discovery, transfers, ownership, balances, supply, and separate EIP-7708 protocol-transfer publication                                                         | standard/nonstandard token tests                                                                                                 |
| P20-T05 | done   | P20-T01                        | Geth callTracer and trace_* normalization, block-hash-batched call/state-difference tracing, and bounded envelopes without per-transaction debug fallback                      | trace/revert, envelope/fallback/budget, PostgreSQL, race and production parity tests                                             |
| P20-T06 | done   | P20-T13, P20-T04, P20-T05      | Snapshot search, exact historical analytics/rollups and price adapters                                                                                                         | Unicode/config/security, exact-block and fallback, PostgreSQL generation/snapshot/reorg, race and runtime tests                  |
| P20-T09 | done   | P20-T04                        | Immutable exact NFT/ERC-20 state, cache-miss-only balance batches, typed capability failures, and metadata-role discovery and canonical event-driven NFT refresh               | conflict/cache/reorg, parser/schema, exact RPC, known-ID batch, concurrency and Preview tests                                    |
| P20-T11 | done   | P20-T05, P20-T01               | Exact bounded transaction state differences with independent capability state                                                                                                  | malformed, limit, replay, and reorg tests                                                                                        |
| P20-T13 | done   | P20-T01, P20-T02, P20-T11      | Contract creation and block-pinned proxy@2 / abi@2 observations, standard/Beacon proxy versions, canonical upgrade facts and immutable binding provenance                      | Proxy/ABI fixtures, PostgreSQL race, replay/reorg and binding-source regressions                                                 |
| P20-T17 | done   | P10, P20-T01                   | Configuration-digest-bound EntryPoint v0.6-v0.9 decoding, `userop@1`, durable operations/events/participants, continuous coverage, and explicit reindex                        | decoder/configuration, PostgreSQL normal/race, malformed bundle, replay/reorg, and real-Anvil regressions                        |
| P20-T18 | done   | P10, P20-T04, P20-T01, P20-T09 | Always-scheduled `holder@1`, genesis-covered exact-state reconciliation, immutable observations, incremental publication, and bounded reindex                                  | state/RPC, migration, PostgreSQL, supply/balance, lease/replay/reorg, race, and generation tests                                 |
| P20-T19 | done   | P10, P20-T01                   | Optional Beacon network identity, bounded authenticated ingestion, durable overview, and `consensus@1`                                                                         | fork/body-root, PostgreSQL, unavailable capability, lease/reorg, and discovery-generation regressions                            |
| P20-T20 | done   | P20-T19                        | Independent blob@1 archives: KZG-authenticated S2 bodies, immutable manifests, bounded decode, verified retained-object replay and repair                                      | Integrity/bounds, object-store readback, crash/reorg/repair, PostgreSQL race and Lighthouse acceptance                           |
| P20-T21 | done   | P10, P20-T19                   | Authenticated five-type execution requests, independent requests@1 publication and replay                                                                                      | protocol/SSZ, PostgreSQL race, replay/fencing and real Preview role parity                                                       |
| P20-T27 | done   | P10, P20-T01                   | Current-chain ENS v1/v2 core-backed discovery, lifecycle/recovery, independent resolution and registration profiles, canonical name directory and event-time operation history | Pinned protocol fixtures, coverage/expiry/lease/publication fences and PostgreSQL race; separate runtime/Mainnet evidence in P30 |
| P20-T33 | done   | P20-T06                        | Incremental analytics source/hour completeness, constant-work idle progress and bounded reconciliation                                                                         | Publication/reorg/replay, PostgreSQL concurrency and representative query plans                                                  |

## Acceptance

- [x] Incremental analytics source/hour/chain projections preserve reorg/replay and
      prefix completeness; bounded resumable reconciliation and idle query plans
      pass PostgreSQL integration/race and production topology checks.

- [x] Durable stages retain exact block identity, bounded work, immutable observations,
      atomic lease/generation-fenced publication and orphan-safe replay.
- [x] ABI/proxy/trace/state-difference decoding rejects hostile or ambiguous input;
      exact balances and NFT observations never reuse orphan state.
- [x] NFT ERC-4906/ERC-1155 refresh uses exact event blocks and bounded known-ID
      batches; same-URI signals refetch, and absent signals do not create pollers.
- [x] UserOperation publications bind configured EntryPoint ranges and digest;
      malformed or mismatched direct bundles publish no partial operations.
- [x] Holder snapshots prove continuous genesis-through-snapshot coverage and
      reconcile exact balances/supply before publication. Incremental/audit/code
      changes, replay and reorg preserve complete-state requirements.
- [x] Consensus and five-type requests authenticate network, Beacon body and
      execution identity without driving execution finality or readiness.
- [x] Blob archives use KZG-bound immutable S2 bodies, bounded decoding,
      independent readback, fenced manifests and revalidated retained replay.
- [x] ENS v1/v2 consumes core facts under enrich; exact resolution and registration
      profiles publish independently of directory progress and of each other.
      Canonical directory and event-time history retain expiry/reorg/lease fences.
- [x] Analytics use exact amounts and generation-fenced rollups; native_transfer@1
      keeps authenticated EIP-7708 protocol logs separate from ABI/contract events.
- [x] Optional feature failures never corrupt core readiness. Historical replay
      uses explicit bounded reindex; startup performs no unbounded backfill.

## Current Blockers

The current-chain ENS feature and representative-publication milestones are
complete with [new acceptance evidence](../reviews/ens-current-chain-acceptance.md).
Full directory coverage remains separately reported deployment progress; P70
retains its release security, capacity, recovery and other existing gates.

## Evidence

P20-T33 passed complete PostgreSQL integration/race, schema/runtime all/split and
multi-scale publication/query-plan checks on the reviewed working tree. Reconciliation
is capped at 1,000 identities per 15 minutes with a durable cursor. See the
[review completion report](../reviews/review-fixes-2026-11-11.md) for exact commands,
source identities and local performance limits; P70 reference capacity remains blocked.

P20-T01/P20-T02/P20-T04/P20-T05/P20-T06/P20-T11/P20-T13 passed
stage/ABI/proxy/token/trace/state-difference and PostgreSQL normal/race tests covering immutable block identity, bounded decoding/batches, atomic
lease-fenced publication, replay and orphan retention. P20-T09 NFT event refresh passed
bounded known-ID, same-URI, conflict/reorg and local Kubo persistence checks;
this does not establish public IPFS availability.

P20-T17 UserOperation decoder/configuration tests cover EntryPoint v0.6-v0.9; a real
Anvil packed v0.9 transaction passed both layouts. P20-T18 holder reconciliation passed
exact supply/balance, coverage, incremental publication, lease/replay/reorg and
PostgreSQL race tests. API/Web acceptance remains in P40/P50, topology in P30-T35
and reference capacity in P70-T04.

P20-T27 passed core-reference/attachment, pinned v1/v2 lifecycle/discovery,
independent profile/resolution, canonical directory, expiry and temporal history
regressions, PostgreSQL race, generation and fresh-schema checks. See the
[current-chain report](../reviews/ens-current-chain-acceptance.md) and
[profile extension report](../reviews/ens-current-chain-acceptance.md#registration-profile-local-acceptance).
P30-T150/P30-T151 retain separate local and representative Mainnet acceptance;
profile/history fixtures do not extend the Mainnet sample or prove full history.

P20-T19 consensus fixtures authenticate Bellatrix through Gloas, network/execution
identity and generation-fenced reorg recovery. P20-T21 passed all five nonempty
request-type fixtures and publication/replay fences. Real Lighthouse Preview
covered empty Gloas request sets in both layouts; nonempty builder transactions
and public historical retention are not established.

### Blob access and archive update

P20-T20 includes S2 integrity/bounds, conditional writes, independent readback,
upload-before-publication failure, retained replay and repair. Shared archive,
P40-T22 browser/V2, P50-T75 preview/download and P60-T13 billing acceptance passed
`make check`, `make test-integration-race`, `CI=true make test-e2e` (225 tests),
`make test-rustfs`, `make test-schema-e2e`, `make test-runtime-e2e` and
`make test-preview-consensus-full` on macOS arm64/Docker Linux arm64.

The recorded worktree was based on `88c6d576` plus the browser GET and route-test
correction; runtime/Lighthouse image config was
`sha256:532c4f544adb6a56317d97d04425a637a87d28fc207d27e0bbae9d02d3f79258`.
Full acceptance verified archive bytes, read-only API access, outage/recreation,
all/split publication and nonzero finality. Local EIP-3009/Permit2 settlement and
debit passed separately from live payments. These results do not prove public
Beacon retention, production backup/restore or storage-enforced writer IAM;
P30-T138 retains that limitation.
