# P20 — Enrichment & Durable Archives

Status: `done`

## Outcome

Core facts are asynchronously enriched with ABI decoding, contract/proxy
versions, ERC-20/721/1155 activity, normalized call traces, state reconciliation,
search documents, and rollup statistics without delaying core readiness.

The enrichment domain also owns configured ERC-4337 UserOperations,
authoritative ERC-20 holder snapshots, optional Beacon observations, and
independently enabled durable blob archives. PostgreSQL owns identity and
publication; immutable blob bodies live in a separate durable object bucket.
Consensus observations never drive execution canonicality, finality, or readiness.

UserOperation support covers explicitly configured EntryPoint v0.6-v0.9
using exact stored bundles without external RPC.
Pending Bundler mempools, submission/simulation, wallet-specific call/signature
interpretation, automatic EntryPoint discovery, and nested EntryPoint calls
remain outside scope. Holder enumeration is exact current ERC-20 state, with
no historical/balance-ranked holder pages or approximate event-delta balances.

API-owned ENS resolution, immutable name generations and bounded standard-event
NFT metadata refresh share these exact identity and reorg boundaries.

## References

- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0025](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0005](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [ADR-0041](../decisions/ADR-0041-snapshot-stable-ens-primary-names.md)
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

| ID      | Status | Depends on                         | Deliverable                                                                                                                                                                                  | Verification                                                                                                    |
| ------- | ------ | ---------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| P20-T01 | done   | P10                                | Versioned durable stages, leases, idempotency, reattach-safe replay generations, and one retry budget                                                                                        | replay/crash, detach/reattach, lease-race and retry tests                                                       |
| P20-T02 | done   | P20-T01                            | ABI selector/event/error decoding, confidence, and global ABI/Trace resource budgets including builtin and large-batch boundaries                                                            | ABI/trace fixture/fuzz and adversarial-budget tests                                                             |
| P20-T03 | done   | P20-T02                            | Contract creation and EIP-1167/1967/beacon proxy versioning                                                                                                                                  | proxy-upgrade tests                                                                                             |
| P20-T04 | done   | P20-T01                            | ERC-20/721/1155 discovery, transfers, ownership, balances, supply, and separate EIP-7708 protocol-transfer publication                                                                       | standard/nonstandard token tests                                                                                |
| P20-T05 | done   | P20-T01                            | Geth callTracer and trace_* normalization, block-hash-batched call/state-difference tracing, and bounded envelopes without per-transaction debug fallback                                    | trace/revert, envelope/fallback/budget, PostgreSQL, race and production parity tests                            |
| P20-T06 | done   | P20-T03, P20-T04, P20-T05          | Snapshot search, exact historical analytics/rollups, price adapters, and opt-in API-owned ENS normalization, exact-block Universal Resolver/CCIP-Read, immutable generations and maintenance | Unicode/config/security, exact-block and fallback, PostgreSQL generation/snapshot/reorg, race and runtime tests |
| P20-T09 | done   | P20-T04                            | Immutable exact NFT/ERC-20 state, cache-miss-only balance batches, typed capability failures, and metadata-role discovery and canonical event-driven NFT refresh                             | conflict/cache/reorg, parser/schema, exact RPC, known-ID batch, concurrency and Preview tests                   |
| P20-T10 | done   | P20-T01                            | Lease-fenced atomic publication of derived output, stage result, journal, and job generation                                                                                                 | stale-worker and publication-window tests                                                                       |
| P20-T11 | done   | P20-T05, P20-T10                   | Exact bounded transaction state differences with independent capability state                                                                                                                | malformed, limit, replay, and reorg tests                                                                       |
| P20-T13 | done   | P20-T03, P20-T01, P20-T10, P20-T11 | OpenZeppelin 5.6.1-aware `proxy@2`, dependent `abi@2`, shared Beacon observations, canonical upgrade/initialization facts, and replay-safe coverage evidence                                 | proxy/ABI unit, PostgreSQL integration/race, reorg, replay, and immutable binding-source tests                  |
| P20-T17 | done   | P10, P20-T10                       | Configuration-digest-bound EntryPoint v0.6-v0.9 decoding, `userop@1`, durable operations/events/participants, continuous coverage, and explicit reindex                                      | decoder/configuration, PostgreSQL normal/race, malformed bundle, replay/reorg, and real-Anvil regressions       |
| P20-T18 | done   | P10, P20-T04, P20-T10, P20-T09     | Always-scheduled `holder@1`, genesis-covered exact-state reconciliation, immutable observations, incremental publication, and bounded reindex                                                | state/RPC, migration, PostgreSQL, supply/balance, lease/replay/reorg, race, and generation tests                |
| P20-T19 | done   | P10, P20-T10                       | Optional Beacon network identity, bounded authenticated ingestion, durable overview, and `consensus@1`                                                                                       | fork/body-root, PostgreSQL, unavailable capability, lease/reorg, and discovery-generation regressions           |
| P20-T20 | done   | P20-T19                            | Independently enabled `blob@1`, KZG-authenticated immutable object archives, fenced manifests, retained-object replay, and repair                                                            | object-store, independent readback, corruption, conditional write, crash, and replay regressions                |

## Acceptance

- [x] Durable stage work is bounded, restartable and generation/lease-fenced.
- [x] ABI, proxy, token, trace and state-difference facts retain exact block identity.
- [x] Canonical publication is atomic; reorg replay retains orphan observations.
- [x] Exact balance observations reject conflicts and never reuse orphan state.
- [x] Capability loss and hostile input fail explicitly without corrupting readiness.
- [x] EntryPoint ranges and every UserOperation publication bind one configuration
      digest; disabled or mismatched configurations cannot expose another digest.
- [x] Only successful direct `handleOps`/`handleAggregatedOps` calls with exactly
      agreeing calldata and EntryPoint events publish; malformed bundles publish
      no partial rows. Lifecycle, failure, and participant facts survive replay,
      detach/reattach, and orphan retention.
- [x] UserOperation history uses explicit bounded `reindex --stage userop`;
      migrations never enqueue unbounded history.
- [x] Holder candidates require continuous block-zero-through-tip high/verified
      canonical ERC-20 Transfer coverage. One exact block-hash state endpoint reconciles
      every candidate and total supply before an immutable snapshot is public.
- [x] Forward holder updates reconcile Transfer participants; code/proxy generation
      changes and audit drift require a full baseline. Reorg, replay, lease expiry,
      RPC loss, or conflicting state cannot expose partial or stale holdings.
- [x] Existing holder history uses explicit bounded `reindex --stage holder`;
      migrations and startup never enqueue unbounded history.
- [x] Disabled consensus/blob features issue no upstream requests. Published
      observations authenticate the pinned Beacon SSZ body root and exact execution
      hash; time-derived slots are locator hints only.
- [x] Blob bytes are KZG-bound immutable objects verified by independent readback
      before PostgreSQL publishes manifests. Objects survive reorgs; retained
      replay revalidates network, identity, canonicality, lease, generation, and bytes.
- [x] Reorgs rewind discovery even while collection is disabled; generation fencing
      rejects stale scans and publication. Optional failures remain worker-local
      capability states and never withdraw core execution readiness.
- [x] Canonical ERC-4906 single/batch and ERC-1155 URI events create immutable,
      orphan-retained update observations without trusting their URI payloads.
- [x] Direct update signals refresh their exact Token ID, while a batch signal
      visits only already discovered IDs in its closed range with bounded work.
- [x] Every event-driven source call stays on one state endpoint and uses the
      exact event block hash; stale blocks never enqueue or publish a refresh.
- [x] A same-URI standard update still refetches the document, while contracts
      that emit no standard update event receive no periodic or request-driven
      refresh.
- [x] Official resolution uses Ethereum Mainnet finality and the configured chain coin
      type; custom resolution uses one exact local canonical block.
- [x] Custom resolution runs only after an official no-record or unsupported-coin-type
      result, never after transport, CCIP, normalization, or validation failure.
- [x] Resolution generations, search cursors, and address-name snapshots remain stable
      across pagination and invalidate correctly on custom-chain reorg.
- [x] Core indexing, ordinary API responses, and readiness do not depend on ENS availability.
- [x] Go ENS canonicalization uses `github.com/adraffy/go-ens-normalize`;
      `github.com/ensdomains/go-ens/v4` owns hashing/wire encoding. Primary names
      require a normalized reverse result whose forward record matches the
      exact address; CCIP callbacks retain the pinned endpoint/block and bounds.

- [x] Historical execution analytics retain exact fee totals and generation-fenced,
      reorg-safe hourly rollups; dirty or incomplete ranges never publish stale
      aggregates. Amsterdam gas semantics follow the current stats contract.
- [x] `native_transfer@1` derives EIP-7708 ETH transfers only from authenticated
      post-Amsterdam receipts and publishes output, journal, stage and lease
      atomically. Replay retains orphan facts; Token/ABI/Trace keep protocol logs
      separate from contract events and never fabricate ABI execution provenance.

## Current Blockers

None. P60/P70 retain independent release blockers.

## Evidence

P20-T04/P20-T06 retain local native-transfer and historical-analytics acceptance:
stage/publication, exact amounts, migration, reorg/replay, PostgreSQL/race and
load regressions passed. Current protocol/stage semantics remain owned by the
ADRs; P30-T35 retains historical runtime/Preview parity and evidence limits.

P20-T06 includes ENS resolver/configuration/security, Unicode and exact-block
acceptance; PostgreSQL normal/race covered immutable generations, official-only
failure handling and snapshot reuse. Hashing/wire-library maintenance retained
ENSIP-15 boundaries. Live upstream availability is not inferred.
P20-T09 includes NFT parser/schema, event-driven ERC-4906/ERC-1155 refresh,
known-ID batch bounds, concurrency and reorg acceptance. Offline local Kubo
Preview proves exact observations and refresh persistence, not public IPFS.

P20-T17 passed EntryPoint v0.6-v0.9 decoder/configuration and PostgreSQL
normal/race tests. A real Anvil packed v0.9 transaction passed both role layouts;
API/Web and runtime evidence belongs to P40-T20, P50-T72, and P30-T35.

P20-T18 passed exact supply/balance reconciliation, continuous coverage,
immutable facts, publication fences, incremental updates, replay, and reorg
withdrawal in unit/race and PostgreSQL normal/race tests. Its public and runtime
acceptance is recorded in P40-T21, P50-T73, and P30-T35; reference capacity
remains P70-T04 work.

P20-T19 protocol/PostgreSQL fixtures authenticated Bellatrix through Gloas
bodies against pinned roots, including private presets and independent geth
root checks. They rejected forged execution associations/network changes and
covered unavailable worker outcomes, leases, bounded initial discovery, and
generation-fenced catch-up after disabled-period reorgs.
P20-T20 passed duplicate references, conditional writes, independent readback,
upload-before-publication failure, replay after upstream pruning, generation
changes during readback, missing-object repair, and fixed-network replay checks.
These are historical protocol-fixture/container results. Real private Beacon
acceptance and deferred storage-side IAM enforcement belong to P30-T138;
public-network retention and production backup/restore are not established.

Retained completed milestones: P20-T01, P20-T02, P20-T03, P20-T04, P20-T05, P20-T06,
P20-T09, P20-T10, P20-T11, P20-T13, P20-T17, P20-T18, P20-T19, P20-T20.

Stage, ABI, token, proxy, trace and state-difference unit/fuzz/race checks
and PostgreSQL integration passed at the delivery revisions. Tests covered
lease-fenced atomic publication, detach/reattach replay, exact immutable NFT
observations, hostile decoding budgets and historical proxy bindings.

Later block-batched trace and exact-balance work passed `make check`,
`make test-integration-race`, generation/source gates and schema acceptance.
Fixtures prove one-endpoint batches, cache-miss-only reconciliation, conflict
rejection and no orphan-cache reuse. Current stage versions and contracts live
in the architecture and ADRs.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P20-enrichment.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
