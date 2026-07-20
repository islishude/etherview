# P20 — Enrichment

Status: `done`

## Outcome

Core facts are asynchronously enriched with ABI decoding, contract/proxy
versions, ERC-20/721/1155 activity, normalized call traces, state reconciliation,
search documents, and rollup statistics without delaying core readiness.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0008: Versioned token observations and exact state reconciliation](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0011: Snapshot search, statistics, and bounded adapters](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0012: Lease-fenced derived publication](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P20-T01 | done | P10 | Versioned enrichment stages, durable jobs, leases, and idempotency | replay/crash tests |
| P20-T02 | done | P20-T01 | ABI selector/event/error decoding and confidence model | ABI fixture/fuzz tests |
| P20-T03 | done | P20-T02 | Contract creation and EIP-1167/1967/beacon proxy versioning | proxy-upgrade tests |
| P20-T04 | done | P20-T01 | ERC-20/721/1155 discovery, transfers, ownership, balances, supply | standard/nonstandard token tests |
| P20-T05 | done | P20-T01 | Geth callTracer and trace_* adapters with normalized call frames | trace/revert tests |
| P20-T06 | done | P20-T03, P20-T04, P20-T05 | Search, name/price adapters, and aggregate statistics | reorg, adapter, and consistency tests |
| P20-T07 | done | P20-T01, P20-T03, P20-T05 | Reattach-safe durable replay generations and one retry budget | detach/reattach and lease-race tests |
| P20-T08 | done | P20-T02, P20-T04, P20-T05 | Global ABI/Trace resource budgets and builtin/large-batch boundaries | adversarial budget and fixture tests |
| P20-T09 | done | P20-T04 | Immutable exact NFT state and typed token state-capability failures | conflict and capability tests |
| P20-T10 | done | P20-T01, P20-T07 | Lease-fenced atomic publication of derived output, stage result, journal, and job generation | stale-worker and publication-window tests |

## Acceptance

- [x] Slow or failed enrichment never blocks the core checkpoint.
- [x] Replaying a stage version is idempotent and reorg-safe.
- [x] Missing traces are distinguishable from an empty call tree.
- [x] Event-derived token state exposes confidence and can be reconciled against
      a fixed canonical block.
- [x] ABI guesses are never represented as verified facts.
- [x] Proxy and final implementation observations use one endpoint and one
      immutable block-hash selector, retain orphan facts, and replay
      idempotently.
- [x] ABI cannot finish as unbound or a lower-priority guess while its exact
      proxy dependency is incomplete.
- [x] A detached block hash that becomes canonical again cannot retain a stale
      terminal skip, and a replay request racing an active lease is not lost.
- [x] ABI and Trace limits bound total work for one job, including aliased
      dynamic offsets and multi-transaction blocks.
- [x] Exact NFT observations are write-once per block identity, and unsupported
      exact-state RPC is reported as `unavailable` rather than exhausted failure.
- [x] Search pagination remains fixed across label/enrichment changes, retains
      reorg fallback above finality, and rejects a pruned generation.
- [x] `stats@2` distinguishes an indexing start, missing parent interval,
      no-blob block, and inconsistent blob facts without fabricating values.
- [x] Name, price, and API-only state capabilities remain optional, block-bound
      where applicable, SSRF-safe, and expose stable failures without upstream
      error text.
- [x] A successful production attempt publishes derived output, stage result,
      journal, and the matching durable-job generation in one lease-fenced
      PostgreSQL transaction; an expired worker can never make results visible.

## Current Blockers

None. Every P20 work item and acceptance boundary is complete; downstream P30,
P40, P50, and P60 items may now consume the published enrichment contracts.

## Evidence

- P20-T10: all five production derived stages (`proxy@1`, `abi@1`, `token@1`,
  `stats@2`, and `trace@1`) use the PostgreSQL worker's lease-aware processor
  path. Derived output, the exact job/generation stage result, controlled
  journal, durable-job success transition, and append-only publication proof
  commit in one transaction after a token-, expiry-, identity-, and
  generation-fenced compare-and-set. Pending replay rolls writer output back
  to a savepoint before handing off the next generation; a lost or expired
  lease rolls back the whole attempt.
- P20-T10: failed, unavailable, retry-exhausted, and expired-exhausted terminal
  paths publish the exact durable job/generation result in the same transaction
  as completion and intentionally have no journal. This includes non-manifest
  enrichment stages used by durable queue clients; regressions now require
  non-null `durable_job_id` and `job_generation` matching the job's completed
  generation. Direct successful completion of a known derived stage remains
  rejected, and production readers use only `published_block_stage_results`.
- P20-T10: against PostgreSQL 18, `go test -race -tags=integration
  ./internal/integration -run
  'Test(AllDerivedStagesUseOneLeaseFencedPublicationProtocol|ExpiredWriterCannotPublishAfterReplacementLease|PendingReplayDiscardsOwnedWriterAndInvalidatesPublishedView|AtomicPublicationRollsBackDerivedOutputOnJournalFailure|OlderGenerationAndDirectFixtureCannotOverwritePublishedGeneration|DerivedTerminalMarkersArePublishedWithoutJournals|PublicationMigrationAndViewRequireExactDurableTerminalIdentity|LeaseFencedPublicationMigrationReplaysLegacyTerminalsAndGuardsOldWorkers|OlderExhaustionCannotOverwriteNewerOrForeignPublicationMarker|ReplayGenerationHandoffRejectsForeignJournal|StaleCanonicalPublicationRemainsInvisibleAcrossSameHashReattach|CompletedPublicationRemainsInvisibleUntilSameHashReattachReplay)$'
  -count=1` passed. It covers stale and expired writers, replay races,
  rollback on journal failure, direct/older marker exclusion, terminal
  publication identity, migration compatibility, same-hash reattach, and
  ambiguous publication windows.
- P20-T10: against the same PostgreSQL 18 isolation, `go test -race
  -tags=integration ./internal/integration -run
  'TestEnrichment(OutboxCrashRecoveryReplayAndIdempotency|TerminalOutcomesAndExhaustionAreDurable)$'
  -count=1` passed. `go test -tags=integration ./internal/integration
  -count=1` also passed the full integration package after legacy reorg,
  proxy/ABI dependency, replay-generation, and token catalog fixtures were
  moved onto production publication semantics.
- P20-T10: `go test -race ./internal/enrich ./internal/store -count=1`,
  `go test ./... -count=1`, `go vet ./...`, `make toolchain-check`, and `make
  generate-check` passed with the repository-pinned toolchains.
- P20-T10: commit and pull request were not created; completion is recorded in
  the existing shared working tree.

- P20-T06: `stats@2` persists block timestamp, positive parent interval,
  interval-derived TPS, excess/blob fee fields, blob burn, and the existing gas,
  fee, and transaction totals in the same journaled stage transaction. The
  exact configured indexing start is the only parentless success; aggregate
  TPS weights transaction count by elapsed seconds and remains null without a
  known interval. Receipt blob usage without the required header inputs is a
  permanent source-integrity failure.
- P20-T06: search uses a per-chain temporal document catalog. Opaque cursors
  carry chain, canonical number/hash, catalog generation, normalized query,
  and rank/kind/key boundary. Latest-canonical logical selection covers names,
  token observations, and verified contracts bound to exact code; finalized
  compaction retains every observation above finality and advances a cursor
  floor only when old generations are removed. Cross-chain source updates are
  rejected transactionally.
- P20-T06: price and name adapters use the shared HTTPS/SSRF-safe metadata
  client, bounded success/failure TTLs, and stable typed capability codes.
  External name lookup starts only after the search transaction closes, binds
  success to an exact canonical block, and treats a different address or
  resolver for the same registry/name/block hash as `identity_conflict`
  without overwriting the first fact. API-only startup accepts a state-purpose
  RPC pool without requiring history RPC and preserves sanitized state errors.
- P20-T06 hardening: every first-page dotted query now requires a fresh name
  gate, and failure after TTL expiry is typed unavailable instead of returning
  a stale catalog hit. The accepted name/address is frozen into the cursor, so
  later pages avoid an external refetch while still validating the exact
  canonical snapshot and generation. Name publication key-share locks its
  canonical block, identical concurrent observations preserve the first row
  without generation churn, and conflicts remain immutable typed failures.
- P20-T06 hardening: migration `0015_search_catalog_consistency` binds catalog
  trigger/prune function resolution to the migration schema. The production
  maintenance role now registers the same PostgreSQL-only housekeeper in
  monolith and split component graphs; it performs immediate and periodic
  finalized-aware pruning plus bounded expired-adapter deletion under a
  chain-scoped advisory transaction lock. Redacted failures retry without
  withdrawing supervisor readiness, and restart triggers a new immediate
  sweep.
- P20-T06 hardening: `go test ./internal/maintenance ./internal/app
  ./internal/config ./internal/db -count=1` passed. It covers bounded config
  and environment overrides, immediate retry/restart, stable-code log
  redaction, component manifest parity, readiness across a retryable database
  failure, shared shutdown, and the generated-query transaction bridge.
- P20-T06: against PostgreSQL 18, `go test -tags=integration
  ./internal/integration -run
  'TestEmbeddedMigrationsKeepNamedConstraintsSchemaLocal|TestSearchCursorGenerationFreezesLateLabelsAndEnrichment|TestSearchUsesLatestCanonicalLogicalObservationAndPrunePreservesReorgFallback|TestPostgresAdaptersPersistFreshSuccessAndStableFailure'
  -count=1` passed. It applies the full migration set in two independent
  schemas, validates schema-local stats constraints, freezes late search
  changes, expires pruned cursors, exercises canonical reorg fallback, caches
  stable adapter failures, and rejects immutable name conflicts.
- P20-T06: `go test -race ./internal/adapters ./internal/state
  ./internal/query ./internal/catalog ./internal/httpapi ./internal/enrich
  ./internal/app ./internal/maintenance -count=1` and the same PostgreSQL 18
  targeted command with `-race` passed. `go vet ./...` also passed.
- P20-T06 closure hardening: the exact configured indexing start now retains
  null interval/TPS even when older canonical history remains in PostgreSQL;
  incomplete blob header pairs and non-positive receipt blob facts fail rather
  than fabricating fees. Search cursor keys are emitted and compared in the
  same lowercase address order used by SQL, while read-time normalization keeps
  previously issued checksum-address boundaries usable.
- P20-T06 closure: `go test ./... -count=1`, `go vet ./...`, `make
  toolchain-check`, and `make generate-check` passed with the pinned Node 24 and
  repository Go toolchains. The focused search/stats packages and all relevant
  adapter, state, query, catalog, HTTP API, enrichment, app, maintenance,
  config, and database packages also passed with `-count=1`; the applicable
  runtime packages passed under `-race`.
- P20-T06 closure: against PostgreSQL 18, the targeted integration suite passed
  under `-race`, including schema-local migrations and catalog functions,
  dotted-name snapshot gating, canonical detach/name-lock serialization,
  bounded maintenance, frozen/pruned search generations, reorg fallback,
  adapter caching, normalized address pagination, and configured-start stats
  with retained canonical history. The two new pagination/stats regressions
  also passed independently without the race detector.
- P20-T06: commit and pull request were not created; this task explicitly
  required completion in the existing working tree without a commit.

- P20-T07: durable jobs now retain requested, claimed, leased, and completed
  generations plus source-deduplicated replay requests. A replay racing an
  active lease preserves its token and is consumed by `Finish`/`Retry`; if the
  owner has already persisted ABI output and then crashes, the first claim
  after expiry removes that exact old result, journal, and ABI output in the
  same transaction that grants the next-generation lease. P20-T10 separately
  owns fencing successful processor publication itself against a worker that
  continues after lease expiry.
- P20-T07: canonical outbox identities carry monotonic generations. A duplicate
  commit of an already-canonical block remains idempotent, while detaching and
  reattaching the same hash reopens its published outbox row and advances the
  existing enrichment job. Delayed orphan wakes for a reattached hash are
  acknowledged as stale instead of retrying forever.
- P20-T07: the worker no longer has an independent attempt ceiling;
  `durable_jobs.max_attempts` is the only exhaustion decision and defaults to
  the same ten-attempt value used when the outbox creates the durable row.
- P20-T07: migration `0013_durable_replay_generations` is additive and its
  PostgreSQL regression applies it twice over pre-generation queued, leased,
  succeeded, and published-outbox rows. Ownership and terminal state survive,
  counters backfill consistently, and sqlc generation completed with the new
  job/outbox fields and replay-request model.
- P20-T07: `go test -race ./internal/enrich ./internal/store -count=1` and
  `go vet ./internal/enrich ./internal/store ./internal/integration` passed.
- P20-T07: against PostgreSQL 18, `go test -race -tags=integration
  ./internal/integration -run
  'TestDurableReplayGenerationMigrationBackfillsExistingQueueRows|TestWorkerConsumesTheOutboxDurableRetryBudget|TestCanonicalSameHashReattachReplaysTerminalStaleGeneration|TestLateTraceReplayRacingActiveABILeaseIsConsumedByNextGeneration|TestExpiredABILeaseReclaimAtomicallyClearsPersistedPreviousGeneration'
  -count=1` passed. These tests cover migration backfill, one durable retry
  budget, duplicate-commit idempotency, same-hash detach/stale terminal
  skip/reattach, active-lease replay consumption, duplicate-source quiescence,
  and crash-after-output expired-lease reclamation.
- P20-T07: against the same PostgreSQL 18 isolation, `go test
  -tags=integration ./internal/integration -run
  'TestEmbeddedMigrations|TestPostgresDurableJobLifecycle|TestEnrichmentOutboxCrashRecoveryReplayAndIdempotency|TestProxyStageCreationUpgradeBeaconDependencyAndReorg'
  -count=1` passed the prior migration, queue, outbox, and Proxy/ABI contracts.
- P20-T07: a repository-wide `go test ./... -count=1` was not used as pass
  evidence: during concurrent P20 work it failed only in the shared
  `internal/app` state-RPC fixture and the `internal/maintenance` stats stage
  version fixture. The scoped T07 packages and regressions above passed.
- P20-T07: `make plan-check` passed after the work item, acceptance, blocker,
  ADR, architecture, repository rules, and evidence were updated in place.
- P20-T07: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.

- P20-T08: ABI decoding now shares node, work, and decoded-byte budgets across
  every selector candidate and repeated traversal of aliased dynamic offsets.
  Arrays use the independent 4,096-element ceiling instead of the 256
  top-level-argument ceiling, so a valid 257-item ERC-1155 `TransferBatch`
  succeeds while 4,097 items fail deterministically. Solidity `Error(string)`
  and `Panic(uint256)` remain decoder-local built-ins and are never persisted
  or queried as signature-database ABI candidates.
- P20-T08: Trace retains its per-transaction limits and additionally charges
  raw payload, normalized frames, binary data, and error text against one
  block-attempt budget. The budget spans every transaction and a same-endpoint
  `callTracer` to `trace_transaction` fallback, including successfully decoded
  work discarded before fallback; exhaustion is a durable terminal stage
  failure with no partial trace or journal output.
- P20-T08: `go test ./internal/enrich -count=1`, `go test -race
  ./internal/enrich -count=1`, `go vet ./...`, and the integration-tag compile
  `go test -tags=integration ./internal/integration -run '^$' -count=1`
  passed. `FuzzABIDecodeAliasedOffsetsBudget` also passed a one-second run with
  two workers. The targeted fixtures cover shared candidate budgets, aliased
  offsets, aggregate callTracer/trace API/fallback budgets, decoder-local
  built-ins, and the ERC-1155 257/4,097 boundaries.
- P20-T08: against PostgreSQL 18,
  `TestABIStageBindsPriorityRangeAndForkIdentity` and
  `TestTraceStageTerminalOutcomesAreDurable/whole_block_frame_budget` passed.
  They prove built-in errors do not enter signature-database `contract_abis`
  and a whole-block budget failure publishes neither partial normalized traces
  nor a block journal.
- P20-T08: the pinned Go/Node/npm `make toolchain-check` and `make plan-check`
  passed. A full `go test ./... -count=1` was attempted but is not recorded as
  a P20-T08 pass: concurrent/shared fixtures still failed in
  `internal/app` (`TestBuildRPCAcceptsAPIOnlyStateEndpointWithoutHistoryPurpose`)
  and `internal/maintenance`
  (`TestExecutorReindexMapsV1StagesToCanonicalBlockJobs/stats`). Those common
  gates remain for overall P20 closure.
- P20-T08: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.

- P20-T09: exact ERC-721 owner and ERC-1155 balance rows are write-once for
  their complete block identity. Conditional conflict updates accept only an
  identical observation, preserve the first `observed_at`, and return
  `ErrExactNFTObservationConflict` on disagreement; database triggers reject
  direct mutations that bypass the application path.
- P20-T09: token detection and proxy discovery now share the sanitized
  EIP-1898 classifier. Missing methods, invalid block-hash selector support,
  missing trie state, pruned history, and missing headers become terminal
  `unavailable` capability facts. Transport causes remain retryable through
  `errors.Is` without exposing hostile RPC text, while malformed successful
  wire data remains permanent.
- P20-T09: `go test -race ./internal/enrich ./internal/state -count=1` passed.
  Targeted token fixtures cover getCode and eth_call capability variants,
  transport retry identity, malformed code, execution revert, and unchanged
  proxy behavior. `go vet ./internal/enrich ./internal/state` also passed.
- P20-T09: against PostgreSQL 18, `go test -race -tags=integration
  ./internal/integration -run
  'TestExactNFTObservationsAreImmutableUnderConcurrentRPCDisagreement|TestTokenObservationsAndExactNFTStateSurviveRealPostgresReorg'
  -count=1` passed. It deterministically races conflicting and identical RPC
  observations, proves first-writer retention and stable audit timestamps,
  rejects direct SQL mutation, and preserves reorg/cache semantics.
- P20-T09: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.

- P20-T03: production `proxy@1` now observes top-level and normalized-trace
  contract creations, EIP-1967 upgrade events, and every transaction/log/trace
  target without canonical code history. This covers genesis predeploys and
  non-zero indexing starts; exact empty runtime is retained as zero bytes with
  the non-zero Keccak-256(empty) hash.
- P20-T03: every block attempt acquires one state RPC endpoint and uses its
  exact EIP-1898 block-hash selector for `eth_getCode`, both EIP-1967 slots, and
  beacon `implementation()`. EIP-1167 immutable-argument variants, direct
  EIP-1967 implementations, and final beacon implementations persist exact
  proxy/implementation code hashes with bounded details. Missing block-hash
  state is terminal `unavailable`; there is no height/latest fallback.
- P20-T03: proxy-shaped state is isolated per candidate. Ambiguous simultaneous
  slots, invalid/self/empty implementations, and reverting or invalid beacon
  results retain the target's exact code observation but cannot fail the block
  or dependency-poison ABI for valid peers. Transport, exact-state capability,
  and malformed RPC wire errors remain retryable, unavailable, and permanent
  respectively.
- P20-T03: `abi@1` claim selection and its production processor both enforce
  the exact `proxy@1` dependency. Proxy unavailability propagates as ABI
  unavailability instead of an `unbound` success. Proxy completion safely
  removes and requeues an older terminal ABI result; late normalized
  CREATE/CREATE2 output resets terminal proxy then ABI jobs atomically, never
  steals active work, and quiesces after one downstream replay.
- P20-T03: `go test ./internal/enrich ./internal/app -count=1` passed. Targeted
  fixtures and fuzz seeds cover canonical and immutable-argument EIP-1167,
  EIP-1967/beacon resolution, malformed storage, strict EIP-1898 selectors,
  exact-state capability loss, mixed poison/valid candidates, beacon revert
  versus transport failure, derived journal relations, dispatch order, and the
  normalized root trace-path regression. After candidate-isolation hardening,
  `go test -race ./internal/enrich -run Proxy -count=1` and
  `go vet ./internal/enrich` also passed.
- P20-T03: against PostgreSQL 18, `go test -tags=integration
  ./internal/integration -run 'TestProxy' -count=1` passed. It covers creation,
  upgrade, final beacon implementation, one-endpoint-per-attempt, ABI-first
  enqueue ordering, unavailable propagation, duplicate processing, genesis
  predeploy and non-zero-start target code, exact empty code, a same-block
  proxy-shaped poison candidate beside a valid proxy, single late-Trace replay
  without a loop, and canonical/orphan code, proxy, result, and journal
  transitions across a reorg.
- P20-T03: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.

- P20-T05: Geth `callTracer` and compatible `trace_transaction` responses are
  normalized into bounded, ordered trees covering nested reverts,
  `DELEGATECALL`, `CREATE2`, `SELFDESTRUCT`, and raw custom-error output. Each
  block attempt stays on one RPC endpoint, and same-endpoint fallback is
  permitted only for method/capability absence.
- P20-T05: trace API frames must match the requested block hash/number and
  transaction hash/index. Both adapters require one root matching the stored
  canonical transaction sender, target or creation kind, value, and input;
  `trace_transaction: []` is rejected instead of becoming a completed empty
  trace. Catalog/API tests distinguish `missing`, `unavailable`, and `failed`
  stages from a complete root-only tree with no internal calls.
- P20-T05: `go test ./internal/enrich ./internal/catalog ./internal/httpapi
  -count=1` and `go test -race ./internal/enrich ./internal/catalog
  ./internal/httpapi -count=1` passed.
- P20-T05: against PostgreSQL 18, `go test -tags=integration
  ./internal/integration -run
  'TestTraceStageTerminalOutcomesAreDurable|TestDerivedJournalTracksSingleAndMultiBlockReorgs|TestStaleDerivedJobsPersistOnlyNonCanonicalJournals|TestDerivedJournalFailureRollsBackEveryProductionStage'
  -count=1` passed. It proves durable exact stage results for missing trace
  capability, pruned history, timeout exhaustion, and an empty trace response,
  plus journal atomicity across reorg/replay.
- P20-T05: against the same PostgreSQL 18 isolation, `go test
  -tags=integration ./internal/integration -count=1` passed the full
  integration package.
- P20-T05: `go test ./... -count=1`, `go vet ./...`, and `make
  toolchain-check generate-check` passed with the repository-pinned Go, Node,
  and npm executables.
- P20-T05: `make plan-check` passed after the work item, acceptance, references,
  and verification evidence were updated in place.
- P20-T05: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.

- P20-T02: `go test ./internal/enrich ./internal/store -count=1` passed. The ABI
  fixtures and fuzz seed corpus cover exact target/code/range/fork isolation,
  direct-verified/proxy/signature priority, selector collisions, tuple and
  dynamic-array decoding, built-in/custom errors, and bounded malformed input.
- P20-T02: `go test ./internal/app -run
  'EnrichmentDispatcher|ProductionMonolith|ProductionRoleGraph' -count=1`
  passed, covering durable `abi@1` dispatch in the production enrich role and
  unchanged monolith/split component parity.
- P20-T02: with an isolated PostgreSQL 18 schema,
  `go test -tags=integration ./internal/integration -run
  'TestABIStage|TestEmbeddedMigrations' -count=1` passed. It verifies atomic ABI
  binding/decoding/stage-result/journal persistence, exact
  chain/address/code-hash/block-hash/range provenance, verified > historical
  proxy implementation > signature priority, replay idempotency, orphan
  retention, and the database rejection of a verified signature guess.
- P20-T02: `go test -race ./internal/enrich -run 'ABI|SignatureHash' -count=1`
  and, against PostgreSQL 18, `go test -race -tags=integration
  ./internal/integration -run 'TestABIStageBindsPriorityRangeAndForkIdentity'
  -count=1` passed.
- P20-T02: `go test ./... -count=1`, `go vet ./...`, and the full PostgreSQL 18
  `go test -tags=integration ./internal/integration -count=1` passed after all
  concurrent P20 migrations were present.
- P20-T02: `make plan-check` passed with 8 plans, 47 work items, and 38 local
  links after the ADR, architecture, repository-rule, and plan updates.
- P20-T02: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.

- P20-T01: `go test ./internal/enrich -count=1` passed. The unit suite covers
  versioned idempotency keys, lease renewal/fencing, terminal result
  persistence, explicit replay reset, and outbox dispatch idempotency.
- P20-T01: `go test -race ./internal/enrich -count=1` passed.
- P20-T01: `go test ./... -count=1` passed across the repository.
- P20-T01: with an isolated PostgreSQL 18 schema,
  `go test -tags=integration ./internal/integration -run
  'TestEnrichment(OutboxCrashRecoveryReplayAndIdempotency|TerminalOutcomesAndExhaustionAreDurable)|TestPostgresDurableJobLifecycleAndTerminalOnlyRequeue'
  -count=1` passed. It exercises core transactional outbox dispatch, simulated
  worker crash, expired-lease reclamation, stale-token fencing, failed and
  unavailable terminal rows, retry/crash exhaustion, atomic rollback, replay,
  and one-job idempotency.
- P20-T01: with the same PostgreSQL 18 isolation,
  `go test -tags=integration ./internal/integration -count=1` passed the full
  integration package after the queue transaction changes.
- P20-T01: with the same PostgreSQL 18 isolation,
  `go test -race -tags=integration ./internal/integration -run
  'TestEnrichment(OutboxCrashRecoveryReplayAndIdempotency|TerminalOutcomesAndExhaustionAreDurable)|TestPostgresDurableJobLifecycleAndTerminalOnlyRequeue'
  -count=1` passed.
- P20-T01: `go vet ./...` passed.
- P20-T01: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.
- P20-T04: token classifications and metadata are immutable observations keyed
  by chain, address, code hash, and observed block hash. Canonical catalog
  queries fall back to an older observation after a reorg instead of losing it
  to a latest-row upsert.
- P20-T04: ERC-721 owner and ERC-1155 balance responses no longer publish
  event-delta sums as current state. Canonical event activity only discovers
  candidates and candidate membership is not filtered by the delta sign or
  sum. Returned values require `ownerOf`/`balanceOf` at the exact EIP-1898
  canonical block hash, expose `rpc_exact` confidence, recheck canonicality,
  and persist exact block-hash observations for replica reuse. Bounded
  candidate reads release their PostgreSQL snapshot before external RPC work.
- P20-T04: token detection acquires one state endpoint for a complete block;
  tests prove that all contract probes within that block stay pinned while the
  pool can rotate between blocks. ERC-165 `bytes4` arguments are ABI
  left-aligned and covered by the constructor-mint regression.
- P20-T04: `go test ./internal/enrich ./internal/catalog ./internal/state
  ./internal/httpapi ./internal/app -count=1` passed. Coverage includes fake
  `Transfer` evidence, rebasing/nonstandard candidate deltas, ERC-721
  constructor mints, ERC-1155 batch transfer/mint/burn, exact RPC ABI encoding,
  malformed results, unavailable state RPC, and API confidence serialization.
- P20-T04: `go test ./...` passed across the repository.
- P20-T04: with the repository-pinned Node 24.18.0/npm 11.16.0 toolchain,
  `make generate-check` passed after regenerating the Go/TypeScript OpenAPI
  contracts, sqlc output, and deterministic embedded SPA distribution.
- P20-T04: against PostgreSQL 18,
  `go test -count=1 -tags=integration ./internal/integration -run
  'TestTokenObservationsAndExactNFTStateSurviveRealPostgresReorg|TestEmbeddedMigrationsAreIdempotentAndReportCompatibleState'`
  passed. It exercises same-code-hash observation retention, canonical fallback,
  exact ERC-721/ERC-1155 persistence and cache reuse, and rejection of orphaned
  cached observations after a second reorg.
- P20-T04: `make plan-check` passed (8 plans, 47 work items, 37 local links).
- P20-T04: commit/PR not created (the repository has no `HEAD`, and this task
  did not authorize creating a commit or pull request); evidence is bound to
  the current working tree.
