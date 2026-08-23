# Architecture Overview

## Runtime Shape

Etherview is a modular monolith packaged as one Go binary. Components are
selected by roles and are instantiated identically whether all roles share one
process or run in separate deployments.

The feature-aware production component manifest is executable architecture:
startup compares it with the exact deduplicated keys registered by the runtime.
The parity suite also proves that `roles=all` is the union of the split-role
graphs, so adding a component without updating both paths fails before serving.
The manifest remains independent in `internal/app/component_manifest.go`;
typed builders in `runtime_shared.go` and `runtime_<role>.go` own registration
for each subsystem and role. `runtime_assembly.go` only carries their explicit
dependencies and invokes them in lifecycle order, while `serve.go` owns shared
resource acquisition and final supervision.

Configuration has the same separation: `config.go` owns the model, defaults,
and YAML load; environment files own role-scoped overrides and secret loading;
validation files own pure global, role, and subsystem checks. The split does
not introduce alternate precedence, defaults, keys, or role-specific fallback.

The same supervisor owns the lifecycle of those registered services in every
deployment shape. It advertises process readiness only after all selected
services have entered `Run`, withdraws readiness before canceling them, treats
an early clean exit as a process failure, and bounds peer draining with
`server.shutdown_timeout`. The operational probe combines that lifecycle state
with PostgreSQL liveness. The API probe combines it with durable core-index
readiness, so startup, failure, and termination cannot serve a stale ready
signal.

The API server binds every accepted request context to that same lifecycle.
Long-lived SSE handlers therefore exit before graceful HTTP shutdown waits for
active connections. Ordinary responses retain the configured write timeout;
SSE clears the idle deadline and reapplies it around each individual write and
flush, preserving both indefinite idle subscriptions and a bounded slow-client
write.

The native API mux is composed from explicit operations, identity/billing,
native, catalog, analytics, metadata, verification, and external-surface route
modules. Production assembly declares its required capability set, and handler
construction validates every dependency before registering any route. Optional
features keep their stable disabled routes and typed unavailable responses;
enabled production modules never infer dependencies through reader/catalog/Web
type assertions or silently omit a route.

The public API listener optionally serves TLS from one startup-loaded
certificate/private-key pair as specified by
[ADR-0027](../decisions/ADR-0027-process-native-api-tls.md). TLS is enabled
only when both absolute certificate paths are configured, fails before binding
on invalid material, and never falls back to HTTP. External Ingress
termination remains supported, and an HTTPS `server.public_url` does not by
itself select the listener protocol. The separate operations listener remains
plain HTTP and never receives the API private key.

Each role also runs the same writer-backed PostgreSQL operational metric
collector. It reads only partial-indexed active durable-job, verification,
repair, and x402 stale-settlement facts, excluding unbounded terminal history,
without making metrics a correctness dependency; refresh failure retains the
last snapshot and exposes its age/failure state. Replicas expose the same
chain snapshot, so current gauges—including
`etherview_x402_stale_settling_payments`—are deduplicated with `max`, while
per-process counters such as `etherview_x402_requests_total` aggregate with
`sum`/`rate` or `sum`/`increase`. Optional OTLP/HTTP tracing starts
only with an explicit collector endpoint, propagates W3C trace context through
HTTP, and flushes within the supervisor's bounded shutdown. Collector or
exporter loss never withdraws readiness. Operator response procedures are in
the [operations runbook](../operations.md).

Structured operational events share stable component and event identities.
Durable workers log exact lease-safe job/block context and typed output
summaries only after the matching transition commits; claim/start and per-RPC
detail remain debug-only. Trace and state-difference failures may carry one
exact failing transaction identity, but request logs and adapter diagnostics
retain the existing authentication, billing, URL, and hostile-error redaction
boundaries.

```text
Execution RPC -> sync/canonicalizer -> PostgreSQL writer -> durable jobs
                    |                         |       -> enrich/trace/metadata
                    |                         -> runtime status/events -> API replica relays
                    -> expiring pending snapshots
PostgreSQL reader (optional; otherwise writer) -> projection query API -> embedded React SPA
API verification workers -> restricted Node SEA -> approved solc-js catalogs/artifacts
outbox -> optional NATS wake-up
API -> optional Redis cache/rate limit
large blobs -> optional S3-compatible storage
```

The SPA keeps route dispatch in `router.tsx`, shared explorer primitives in
`pages/pages.tsx`, and block, transaction, address, token/NFT, verification,
and entity dispatch in separate page modules. English and Chinese resources
are merged from the same seven domain modules, preventing one language from
silently acquiring a different key layout. The pinned Biome gate is part of
`web-lint` and checks hooks, unused code, selected complexity, function size,
and production file size before the embedded distribution is built.

Optional accelerator behavior is intentionally asymmetric: NATS carries only
coalesced poll hints, Redis shares rate buckets and caches only the durable
runtime-status model behind an event generation, and S3-compatible storage
caches only exact-generation normalized transaction traces. Every adapter has
a bounded PostgreSQL fallback and is detailed in
[ADR-0015](../decisions/ADR-0015-disposable-runtime-accelerators.md).
S3 uses an explicit static override or the refreshable AWS default credential
chain only inside `all`/`api`; absence of usable credentials cannot withdraw
readiness or turn object storage into a correctness dependency.

PostgreSQL stores all correctness-critical facts, canonical mappings, stage
state, jobs, leases, and outbox records. Optional systems may reduce latency or
storage pressure but never become the only copy of required state.

Every process uses pgx through `database/sql` for its mandatory writer pool.
An `api` or `all` process may additionally open a read-only pool against a
matching PostgreSQL reader endpoint for latency-tolerant projections. Writer
authority is retained for canonical, authentication, verification,
runtime-event, and external-call correctness fences; reader startup checks the
same schema and chain identity, and API readiness fails closed if either pool
is unavailable. Generated sqlc/pgx queries enter production through a small
bridge that pins one stdlib connection from the selected pool for the duration
of the callback. Existing correctness transactions may execute exported,
generated statements through their pinned `database/sql` transaction adapters,
but production SQL still originates only in `internal/db/queries`; the
migration runner and validated partition-DDL module are the only raw-SQL
executors. The routing and lag contract is specified in
[ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md).

## Chain Correctness

- Each deployment serves one configured chain and binds it with chain ID plus
  genesis hash. Every RPC endpoint is verified against both.
- Block-zero RPC ingestion authenticates but cannot enumerate the allocation.
  The sync role can read the authoritative Genesis JSON from one mutually
  exclusive server-only source: an absolute `chain.genesis_file` or a
  `chain.genesis_url`; either requires indexing to start at zero. After bounded
  source validation, go-ethereum `core.Genesis.ToBlock()` is authoritative for
  the allocation root, Genesis header semantics, and block hash. Those values
  must match canonical block zero before balances, nonces, code identity, and
  per-account storage roots are stored atomically. Raw storage slots are
  discarded. Missing input is a typed unavailable capability, not an empty
  allocation.
- Remote Genesis bootstrap is restricted to public HTTPS port 443 with no
  credentials, query, fragment, path traversal, redirect, proxy, or
  private/special-use destination. Only identity-encoded, valid JSON within the
  64 MiB cap is accepted, with a JSON, vendor `+json`, octet-stream, or text
  media type. An optional explicit non-zero lowercase SHA-256 must match the
  exact response bytes.
- Sync replicas hold a per-chain session advisory lock across the remote
  completion check and fetch, while the HTTP request and parsing remain outside
  the atomic import transaction. A completed import is the durable source of
  truth and removes the remote runtime dependency; a configured checksum must
  match its persisted document digest. Failures expose only stable redacted
  unavailable or failed states. This source extension changes neither the
  persistent schema nor the public API.
- Multi-statement canonical and coverage writes use READ COMMITTED while
  holding the chain-scoped transaction advisory lock. Fresh statement
  snapshots after lock waits, targeted row locks, and one atomic transaction
  form the cross-role serialization protocol.
- Blocks are identified by hash. Canonical height is a mutable mapping and
  orphan facts remain queryable.
- Core readiness means block, transaction, receipt, log, and withdrawal facts
  are durably committed. Enrichment has independent completeness states.
- Execution RPC ingestion is raw-first. The transport bounds and validates the
  JSON-RPC envelope before protocol decoding and preserves each accepted raw
  object independently of its typed projection. Go-ethereum is authoritative
  for matching Ethereum protocol semantics, including transaction types 0
  through 4. Unsupported future transaction types fail permanently and
  atomically before any block write or coverage advance. `blocks.raw` preserves
  the block result's top-level JSON shape. A PoW block stores its full uncle
  headers only in reserved versioned root-level metadata, which
  `DecodeStoredBlock` removes before exposing the raw block. Legacy rows with
  empty uncles decode directly; a legacy row with non-empty uncle hashes but no
  headers fails permanently with `ErrStoredUncleHeadersUnavailable` and
  requires an exact endpoint- and block-identity-bound RPC repair before it can
  become a verified bundle. Typed geth projections are never marshaled over
  stored raw objects or used to discard unknown fields on supported objects.
  Response limits, batch correlation, endpoint pinning, stable error
  classification, redaction, SQL models, and public API models remain explicit
  Etherview adapters; see
  [ADR-0022](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md).
- New-head subscriptions are hints. Polling, ancestry checks, and gap scans are
  authoritative.
- The upstream head, indexed position, readiness, and bounded head/reorg/status
  replay window are PostgreSQL facts. Sync replicas elect one short-lived
  reporter for aggregate status events, so lagging replicas cannot move the
  snapshot backward and backfill-worker count cannot consume the replay
  window. Each API replica tails that ledger independently; see
  [ADR-0004](../decisions/ADR-0004-durable-runtime-status-and-events.md).
- A durable subscriber is registered provisionally while PostgreSQL replay and
  cache invalidation run outside the fanout mutex. Live events published during
  replay are buffered and merged by ID before the subscription becomes active;
  bounded overflow returns replay unavailable instead of delaying established
  subscribers.
- If an API query cache is configured, its idempotent invalidator runs before a
  tailed event advances the replica cursor or reaches SSE subscribers. A failed
  invalidation retries the same PostgreSQL event. Without a configured cache,
  API responses are `no-store` and every query reads the authoritative source.
  Optional Redis-backed caches fail over by disabling or bypassing the cache;
  Redis loss is not itself an invalidation failure.
- Historical block/receipt coverage and historical state availability are
  separate capabilities.
- The configured indexing start and normalized canonical coverage ranges are
  PostgreSQL facts. The core checkpoint is only the end of the range containing
  that configured start; a higher isolated live range is reported separately
  and cannot make the deployment ready.
- Safe and finalized RPC markers are accepted only when their exact hashes are
  canonical and every parent link from the current tip through the lowest
  requested marker is present and continuous. A height/hash match inside a
  sparse or internally inconsistent mapping is not sufficient finality proof.
- Live-head polling and historical backfill are independent lanes. Bounded
  PostgreSQL range leases coordinate backfill replicas, while coverage remains
  the restart-safe source of missing work; see
  [ADR-0006](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md).
- Operator repair refetches a block through the normal sticky history-RPC path
  and may refresh core rows only when chain, height, hash, and parent still
  match the canonical mapping. The refresh path never invokes normal
  fork-choice `Apply`; it never moves canonicality or checkpoints.
- A finalized-crossing, over-depth, no-common-ancestor, or source-inconsistent
  fork is a canonical-safety halt. The first fatal lane cancels live and
  backfill work, records `etherview_sync_halted{reason}` with a stable reason,
  and keeps the process alive and scrapeable until operator cancellation and a
  repair/restart. The Prometheus rule alerts on that durable in-process signal.
- A core refresh invalidates replayable output directly derived from that
  block. Rebuilding proxy, ABI, token, statistics, or trace output is an
  explicit, block-hash-scoped reindex operation; active leases are never
  reset. A versioned proxy/ABI cutover is replayed in dependency order,
  `proxy` before `abi`, rather than by an unbounded migration enqueue.
- Proxy/code, ABI, token, statistics, and normalized-trace production
  processors atomically persist their block-local output, exact durable-job
  generation marker, stage result, one versioned `stage@version` journal, and
  successful lease completion in one PostgreSQL transaction.
  Journal canonicality is derived from the exact chain/height/hash mapping;
  detach and attach transactions retain the output while toggling it and the
  journal together. Readers require the canonical mapping, row flag, and the
  exact lease-published result; see
  [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md) and
  [ADR-0007](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md).
- Durable enrichment delivery uses the same lease fence. The successful
  publisher compare-and-set requires the exact unexpired token and requested,
  claimed, leased, and completed generation relationship; stale worker output
  rolls back. A pending replay discards the writer savepoint, consumes the old
  generation, clears its publication, and queues the new one atomically.
  `failed` and `unavailable` outcomes have no journal but change the job
  terminal state and upsert the exact job/generation result in one transaction.
  Retry exhaustion and crash-expired exhaustion use the same result contract, and
  `durable_jobs.max_attempts` is the only attempt limit interpreted by workers.
  Replay reuses the immutable idempotency key and records a unique source key
  against monotonic requested, claimed, leased, and completed generations.
  Unowned jobs are reset immediately. An active lease is never stolen: its
  `Finish`/`Retry` transition consumes the pending generation, or the first
  claim after expiry clears the previous exact result, journal, and ABI output
  in the same transaction before it exposes the new-generation lease.
- `published_block_stage_results` is the only readiness relation. It excludes
  direct fixture rows, mismatched or superseded generations, active leases,
  result mismatches, and `stale_canonical_skipped` audit rows. A same-hash
  reattach therefore remains explicitly incomplete until its canonical outbox
  generation is dispatched and successfully republished. Etherscan
  enrichment prechecks and their data reads share one read-only repeatable-read
  snapshot.
- Core canonical outbox rows also carry a generation. A repeated attach of the
  same block hash increments and reopens the existing outbox identity, so a
  hash that was detached, terminally skipped as stale, and reattached receives
  a new enrichment generation. A delayed orphan wake for the now-canonical
  hash is acknowledged as stale rather than retried forever.
- `stats@3` derives intervals only from the exact canonical parent. The exact
  configured indexing start has null interval and TPS; every later block
  requires a positive timestamp delta. Aggregate TPS divides transactions by
  total known interval rather than averaging block rates. A block without blob
  transactions has null blob base fee and zero blob burn, while receipt blob
  gas without the required header inputs is a permanent inconsistency; see
  [ADR-0011](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md).
- A trace attempt acquires one trace-purpose RPC endpoint for the entire block
  and calls `debug_traceBlockByHash` once for the exact job block hash with Geth
  `callTracer` and `withLog=true`. An exact `-32602` retries the complete block
  once with `withLog=false`. The compatible `trace_transaction` method remains
  a same-endpoint fallback only when the block debug method or historical state
  is unavailable; `debug_traceTransaction` is never used. The non-null block
  response must match the stored canonical transaction-inclusion count, order,
  and `txHash`, and each item must contain exactly one of `result` or `error`.
  Any malformed or failed item rejects the complete block, so Trace and its
  journal never publish partially. Every returned frame is bounded and bound
  to the requested block hash, block number, transaction hash, and transaction
  position. A completed mined-transaction
  trace has exactly one root whose sender, target/creation kind, value, and
  input match the canonical core transaction. A root-only tree means the
  transaction made no internal calls; a missing stage, unavailable/failed
  stage, or empty `trace_transaction` response is never represented that way.
  A mined transaction temporarily reported as not found remains retryable;
  only a missing method or recognized pruned-history response marks the
  capability unavailable. Payload, frame, input/output-data, and error-text
  budgets apply independently to each transaction and cumulatively to the
  complete block response and attempt. Work and payload consumed before the
  log-config retry or parity fallback remain charged to that block budget.
- `trace@3` retains direct frame failure separately from ancestor rollback. It
  validates every returned callTracer log against the persisted receipt log's
  global index, emitter, topics, and data before recording a trace path and
  execution code address. `DELEGATECALL` and `CALLCODE` expose frame `to` as the
  storage/call context and use a separate exact execution identity for ABI and
  log attribution. A zero-log
  ordinary trace remains publishable; partial, duplicate, misplaced, or
  contradictory tracer logs fail permanently.
- Derived journal payloads contain only controlled relation-level canonicality
  transitions. They do not contain untrusted RPC data and do not claim storage,
  rollback, or replay of opcode/raw traces; trace journaling covers only the
  normalized call tree.
- `proxy@2` acquires one state endpoint per immutable block. Every code,
  EIP-1967 storage, and beacon `implementation()` read uses that endpoint with
  the same EIP-1898 block-hash selector; exact-state absence is unavailable and
  never falls back to a height or `latest`. It observes creations, strict
  upgrade and initialization events, every available normalized trace target,
  successful non-reverted `CREATE`/`CREATE2` results, exact replays, and
  ABI-consumed transaction/log targets that lack canonical code history. An
  exact code change or change to one of the three supported ERC-1967 slots in
  a published StateDiff also requests proxy replay. Thus genesis predeploys,
  non-zero indexing starts, internally called contracts, and silent supported
  slot changes receive an exact-block probe. Exact empty code is stored as a
  zero-length value with Keccak-256(empty), not SQL `NULL`.
- Proxy observations retain EIP-1167 immutable arguments, exact Solady legacy
  LibCWIA fixed implementations and raw arguments, direct EIP-1967
  implementations, OpenZeppelin 5.x pattern evidence, admin and beacon code
  identities, and the final implementation behind a beacon. A shared beacon
  implementation is read and observed once per beacon and immutable block,
  then resolved for every associated proxy. Standard upgrade and initialization
  events retain exact transaction/log order, including multiple transitions in
  one block. A transaction, log, Trace, or StateDiff target is only a discovery
  candidate: ordinary `CALL` evidence alone does not establish an exact proxy
  pattern or authorize management interaction. Exact pattern publication
  additionally requires the applicable runtime, immutable, verified-artifact,
  fixed-block probe, and code-identity evidence; incomplete evidence remains
  generic or partial.
- A legacy LibCWIA runtime is exact only when its complete 98-byte shell, both
  appended-length words, fixed implementation position, product-bounded raw
  arguments, and non-zero/non-self target agree. It is mechanism `cwia` and
  immutable pattern `clone`, not ERC-1167, and needs no creation trace because
  the shell actively copies its trailing arguments into calldata. Typed values
  come only from the verifier's bounded, dual-compiled Solidity AST analysis
  of calls that resolve to the exact canonical Solady `0.1.26` legacy helper
  source. Raw bytes remain authoritative; a matching verified implementation
  code hash may serve typed reads, while writes additionally require the exact
  current binding, exact-address implementation artifact, and a complete
  decoded schema digest. Decoded fields use a bounded accessible
  Name/Type/Offset/Data table, with complete copyable array parents and at most
  64 offset-derived element rows per array. Migration and startup
  schedule no historical CWIA replay; see
  [ADR-0042](../decisions/ADR-0042-solady-legacy-cwia-identity.md).
- Current CWIA, EIP-1167, EIP-1967, and Beacon implementation identities keep
  exact address verification separate from same-runtime artifact reuse:
  `verified + exact_address` is independently verified, while
  `unverified + code_hash` is read-only source/ABI availability. Exact address
  takes precedence. Proxy shells, admins, beacons, management targets,
  historical implementations, Diamonds, and Safe singletons never receive
  this code-hash substitution, and code-hash availability never creates a
  binding or browser write authority.
- Exact legacy CWIA is additionally permitted to use the observed
  implementation code hash as a read-only ABI route for events, Trace calls,
  Method selectors, and transaction calldata. The exact implementation
  artifact is preferred; otherwise a same-chain same-code artifact contributes
  `proxy_implementation/high` provenance with its real source address. The
  route remains block-hash-bound and generation-fenced, performs no RPC during
  reads, and cannot change verification state or authorize writes. Other proxy
  mechanisms retain their existing implementation-artifact rules.
- Every implementation-as-proxy operation still fetches a fresh proxy
  snapshot. A transient `unavailable` latest proxy stage aborts only that
  operation before wallet RPC; it is not treated as target-change evidence and
  therefore cannot erase the last published target or redirect the active tab.
  The next attempt refetches, while real identity or binding differences keep
  the existing refresh-and-fail-closed fence.
- Once the current proxy projection recognizes mechanism `cwia`, the shell's
  Code tab omits the direct Verified artifact and submission panel. Source and
  ABI presentation remains attached to the fixed implementation identity;
  ordinary contracts and other proxy mechanisms retain their direct artifact
  surface. A transient unavailable classification suppresses this choice and
  is re-read every 500 ms until publication, preventing a direct-entry flash
  without serializing the independent artifact request.
- Proxy, implementation, admin, and beacon code hashes, block hash,
  canonicality, confidence, and bounded controlled provenance commit in one
  idempotent stage transaction. Append-only proxy and beacon generation
  witnesses bind observations to the exact durable `proxy@2` lease generation;
  detection evidence carries the same lease identity. Observation readers
  require that witness and its matching `published_block_stage_results`
  generation; upgrade and initialization readers require the event's exact
  canonical block to have a published `proxy@2` result. A raw row alone is not
  public readiness. A reorg retains orphan observations and events and toggles
  them with the proxy journal; see
  [ADR-0010](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md).
- Feature-gated proxy detection V2 runs evidence-producing detectors through
  one exact-block memoized context and retains every resolver outcome. Its Safe
  detector fingerprints the already-fetched runtime before reading slot 0, so
  a non-Safe bulk candidate adds no Safe RPC. Canonical Safe shell identity and
  official singleton identity remain independent. Its dedicated CWIA detector
  reports the exact legacy shell without attributing it to OpenZeppelin. V2 is generation-fenced
  additive evidence and cannot authorize legacy proxy interaction; see
  [ADR-0032](../decisions/ADR-0032-evidence-based-proxy-detection.md).
  Ambiguous slots, self/empty implementations, and reverting or malformed
  beacon semantics reject only that candidate after its code observation;
  they cannot fail the block or prevent valid peers from completing. Transport
  errors, exact-state capability loss, and malformed RPC wire remain distinct
  retry, unavailable, and permanent stage outcomes.
- ERC-2535 Diamond identity is selector-scoped: an exact-block Loupe snapshot
  maps each bytes4 selector to an external facet or to an immutable function on
  the Diamond itself. No facet is projected as the singular implementation.
  Bounded DiamondCut facts retain block/transaction/log/cut order and orphan
  branches; published canonical intervals drive historical function ABI
  attribution, while calls still target the Diamond. Enumeration truncation,
  deterministic validation sampling, absent standard `diamondCut`, and
  inconsistent Loupe/history evidence remain distinct public states; see
  [ADR-0038](../decisions/ADR-0038-selector-scoped-erc2535-diamond-identity.md).
- ABI candidates are looked up only by exact chain, target address, runtime
  code hash, context block number/hash, and an inclusive range covering that
  context. Direct verified artifacts outrank verified historical proxy
  implementation artifacts, which outrank re-hashed signature candidates.
  Same-chain verified artifacts with the same runtime code hash retain their
  source address and have `high` confidence when exact-address validity does
  not cover the context, including later verification of an earlier
  same-address same-code observation. This code-hash reuse never extends the
  address's verified range. PostgreSQL fixes source and confidence mappings.
  Candidate decoding and recursive dynamic-offset traversal
  share one node, work, and byte budget for the complete decode, so aliased
  offsets cannot multiply work outside the configured bound. Array cardinality
  is independent of the top-level argument limit, and Solidity `Error(string)`
  and `Panic(uint256)` remain decoder-local rather than signature-database
  bindings; see
  [ADR-0009](../decisions/ADR-0009-block-bound-abi-provenance.md).
- Transaction logs prefer a published `trace@3` attribution and decode against
  its execution code address while preserving the original emitter and raw
  receipt bytes. Without exact attribution they use only the emitter,
  published historical proxy observations, and same-code verified artifacts.
  Trace call-like frames expose candidate-bound inputs, successful outputs, and
  independent direct-revert data; successful children remain output-decodable
  even when an ancestor later rolls back; see
  [ADR-0033](../decisions/ADR-0033-trace-bound-log-attribution-and-call-decoding.md).
- `state_diff@3` first calls `debug_traceBlockByHash` with `prestateTracer` and
  `diffMode=true`, then replays geth-owned EIP-7702 authorization tuples against
  each exact ordered transaction item and publishes first-hop execution-code
  identity. If diff-only evidence omits or cannot resolve any top-level call
  target, the same endpoint receives one additional exact-block
  `debug_traceBlockByHash` with complete prestate. Its transaction order and
  hashes are bound independently, and only the top-level target plus a present
  first-hop delegate may supplement execution identity; an absent target proves
  empty code, while an absent delegate remains unavailable. It has no
  per-transaction debug fallback. A missing block method, recognized unavailable
  history, missing item evidence, or any failed item makes the whole stage
  unavailable or failed without partial rows or journal; contradictory
  nonce/code evidence fails permanently rather than consulting block-end or
  latest state. See
  [ADR-0034](../decisions/ADR-0034-eip7702-execution-identity-and-constructor-decoding.md).
- `abi@4` first materializes each transaction root's effective execution
  identity without changing `state_diff@3`. Exact raw identities are retained;
  only a known first-hop delegate with a missing code hash may be recovered
  from the matching `trace@3` root plus canonical code history strictly before
  that transaction. Code changes are replayed by transaction index, so current
  and later changes cannot flow backward. The identity, its evidence source,
  ABI decoding, journal, and publication commit atomically. Until `abi@4` is
  published, public readers retain raw state-diff semantics and ignore old ABI
  generations.
- `abi@4` consumes existing canonical code and proxy observations. PostgreSQL
  claim selection and the production processor both require the exact
  same-version `proxy@2` result first. Complete proxy facts permit decoding;
  unavailable proxy state makes ABI unavailable instead of terminal `unbound`,
  while a failed or absent proxy result remains dependency-blocked. The initial
  proxy publication unlocks ABI's already queued first generation; only later
  proxy generations request ABI replay, so concurrency cannot manufacture a
  topology-specific generation. ABI does not wait for Trace. Any normalized
  traces already present are decoded in the same atomic stage transaction.
  Every complete Trace generation that arrives later, including an empty
  replacement, records one source-deduplicated Proxy replay request first and
  then an ABI replay request. Exact successful creation matches may also decode
  constructor arguments after byte-for-byte re-encoding; runtime equality alone
  is insufficient. Evidence withdrawal uses a distinct
  `stage-invalidation` identity; successful replacement publication uses
  `stage-completion`, so the latter cannot be suppressed by the former. The ABI
  dependency prevents the new ABI generation from publishing before the
  replayed Proxy generation, including when the Trace contains only ordinary
  calls or withdraws prior creation evidence. Queued work is refreshed, leased
  work keeps its token until completion or expiry, and repeating the same
  source generation then quiesces. Loss of Trace capability withdraws dependent
  exact Proxy and ABI publications rather than preserving stale evidence.

## Partition Lifecycle and Identity Boundary

Block-scoped fact tables use fixed half-open ranges of 1,000,000 block
numbers. The partition manager covers `transaction_inclusions`, `receipts`,
`logs`, `withdrawals`, `token_events`, `token_balance_deltas`,
`normalized_traces`, `trace_log_attributions`, `abi_decodings`,
`transaction_effective_execution_identities`, and
`address_activities`. Before a core bundle can write
facts in a new range, its chain-locked database transaction takes the global
partition lifecycle lock, rechecks the PostgreSQL catalog, creates every table
in a fixed dependency order, evacuates any matching DEFAULT rows child-first,
and attaches the new partitions parent-first. That catalog recheck uses READ
COMMITTED statement snapshots so a process that waited for another process's
DDL sees the committed relations. The DEFAULT partitions are a recoverable
upgrade buffer, not steady-state storage.

Automatic recovery is atomic: a failed copy, delete, or attach rolls back both
DDL and data movement. A partially hand-managed range whose existing child
partitions make automatic foreign-key movement ambiguous returns a typed
partition-recovery error naming the table and range. Operators then stop
writers, preserve and repair rows in dependency order, and retry the same
idempotent operation.

`blocks` deliberately remains unpartitioned by `number`. Its durable identity
requires both `(chain_id, number, hash)` for block-scoped foreign keys and a
global unique `(chain_id, hash)` lookup. PostgreSQL unique and primary-key
constraints on a range-partitioned table must include the partition key, so a
`number` partition cannot enforce the hash-only identity required by canonical
and orphan lookups. `transactions` is also unpartitioned: `(chain_id, hash)` is
the transaction identity, while block number belongs to a potentially
non-canonical `transaction_inclusions` row and one transaction hash may be
retained across inclusions.

A future split may keep small, globally unique block/transaction identity
directories and partition separate block-scoped payload tables. Moving the
identity tables themselves requires a PostgreSQL/global-index design that can
still enforce the current hash uniqueness and foreign-key contracts; storage
size alone is not sufficient justification to weaken those invariants.

## Public Boundaries

- Native HTTP API lives under `/api/v1`; Etherscan compatibility lives at
  `/v2/api`; operational endpoints are separate.
- Large integers cross JSON boundaries as strings.
- The SPA uses the public API for explorer data. Contract `eth_call` and
  `eth_sendTransaction` use only an injected wallet provider.
- The SPA's sole explorer transport is an `openapi-fetch` client parameterized
  by the generated TypeScript `paths` contract. Its adapter fixes requests to
  the same-origin `/api/v1` prefix, adds no-store credentials policy, and is
  the only production SPA module allowed to call browser `fetch`.
- Tailwind is compiled by its pinned Vite plugin into the content-hashed CSS
  asset. The existing base stylesheet remains authoritative, so Tailwind
  Preflight is intentionally omitted; theme-backed and responsive utilities
  implement shared layout primitives without a CDN or frontend runtime.
- The embedded file handler serves real files with `GET`/`HEAD`, but a missing
  route receives the SPA shell only for a non-reserved `GET` that accepts HTML.
  API, compatibility, operational, asset-shaped, malformed, HEAD, and mutating
  misses stay distinguishable. The index is `no-store`; exact Vite
  content-hashed assets are immutable with SHA-256 ETags. A `default-src
  'none'` CSP explicitly permits only required same-origin resource types and
  forbids inline/evaluated script and external browser runtimes; see
  [ADR-0013](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md).
- Compiler execution and metadata retrieval are hostile-input boundaries and
  require resource isolation, network policy, and explicit size/time limits.
- `api/openapi.yaml` is the public HTTP contract. Go server models and SPA
  TypeScript types are generated from that single specification.
- Native JSON success models use `{data,meta}` and every JSON operation declares
  the common `{error:{code,message,details,request_id}}` failure model. Cursor
  parameters and emitted `meta.next_cursor` values share the bounded opaque
  cursor schema.
- Public labels and search documents use canonical external identities: a
  normalized address, transaction/block hash, or canonical decimal block
  height. Human display text is never used as a persistence key. Search
  cursors bind the exact canonical tip and a retained per-chain catalog
  generation, so labels and late enrichment cannot change later pages. The
  exact block-height, block-hash, and transaction-hash routes overlay labels
  visible at that same generation instead of bypassing the temporal catalog. A
  height-keyed block label cannot leak onto an orphan merely because it shares
  a labeled canonical height. The latest canonical logical name, token, or
  verified code wins; pruning retains every reorgable version above finality
  and rejects cursors older than the retained generation floor. Search-source
  chain identity is immutable, and catalog trigger/prune functions bind their
  own migration schema instead of inheriting a pooled connection's
  `search_path`.
- Optional API capabilities return a typed unavailable error when no fresh,
  authoritative source exists. Its optional details contain only controlled
  `capability`, `state`, and `code` identifiers; an empty successful list means
  the capability was available and observed no matching objects.
- Price adapters retain their bounded HTTPS observation contract. ENS is a
  separate explicitly enabled API capability governed by ADR-0041. One
  resolution generation pins an exact Ethereum Mainnet finalized block and,
  when configured, one exact local canonical custom-ENS block plus sticky RPC
  endpoint identities. Go input uses `adraffy/go-ens-normalize`; the SPA uses
  Viem `normalize`. Primary-name display additionally requires reverse and
  matching forward resolution for the explored chain's coin type.
- Official ENS no-record may fall through to the configured current-chain
  custom Universal Resolver. RPC, CCIP, normalization, malformed-result, and
  forward-mismatch failures never change namespaces. Gateway-aware Universal
  Resolver calls use only configured HTTPS batch gateways through the shared
  SSRF-safe transport and never persist or log RPC URLs.
- Immutable forward/primary/no-record observations publish into the temporal
  search catalog. Dotted search cursors bind the exact accepted observation;
  address-name pages bind a retained generation snapshot so later batches use
  the same source blocks. Official observations need no local block, while a
  custom observation is visible only while its exact local block is canonical.
  External calls remain outside PostgreSQL snapshots and ENS failure never
  blocks core ingestion or ordinary projections.
- The maintenance role owns a PostgreSQL-only search-catalog housekeeper in
  the same production component graph used by `roles=all` and split processes.
  It runs immediately and on a configured interval, uses a chain-scoped
  transaction advisory lock, retains the configured finalized-aware catalog
  generation window, and deletes bounded batches of expired adapter
  observations plus ENS snapshots and retained generations. Cleanup failure
  emits a stable redacted code and retries with
  bounded backoff without making readiness or core correctness depend on it.
- The same maintenance-role graph conditionally owns writer-only user-auth
  cleanup and x402 reservation expiry. Each enabled feature runs one immediate
  and then periodic chain-scoped batch of at most 1,000 rows; authentication
  cleanup removes expired challenges and expired or revoked sessions, while
  billing expiry advances only timed-out `reserved`/`verified` rows and appends
  their events. Replica sweeps use `SKIP LOCKED` candidate selection.
  Feature-off processes register neither component. Split maintenance
  processes load no session pepper, billing fingerprint pepper, or facilitator
  header Secret, and transient writer failures emit only the stable
  `user_auth_cleanup_failed` or `x402_billing_expiry_failed` code before a
  bounded retry.
- The exact Etherscan V2 module/action, method, parameter, API-key, capability,
  unavailable-action, and wire-difference contract is maintained in the
  [Etherscan V2 compatibility matrix](etherscan-v2-compatibility.md).
- Etherscan address-only ABI and source lookups resolve the address's latest
  canonical code observation, then require a verified artifact with the same
  chain, address, code hash, and a validity range covering the canonical tip.
  Missing code state is unavailable and a code hash without such an artifact
  is unverified; older open-ended artifacts are never returned as a fallback.
- Etherscan source verification reuses the native durable verification service
  and is gated by both the public-verification safety switch and an API key.
  The server derives code, code hash, block hash, and creation input from
  canonical PostgreSQL facts, verifies the stored runtime hash, then returns
  the durable verification-job UUID as the compatibility GUID. Source submit
  uses POST and source status permits GET or POST. Proxy submission uses POST
  and proxy status uses GET. Proxy jobs pin the exact canonical proxy mapping
  and require source publications for both code identities; an API-owned worker
  performs no compilation for them and atomically publishes an immutable
  binding only after rechecking canonicality and both sources.
- Native and compatibility verification both resolve the current canonical
  code, block, runtime bytecode, and creation input from PostgreSQL; native
  callers cannot submit those identities. An optional constructor-argument
  suffix is stripped only after exact comparison with the canonical creation
  input. Both boundaries reject empty runtime bytecode or a code hash that
  differs from its Keccak-256 digest. A durable submission
  digest covers the exact request payload and the server-derived public sandbox
  requirement; only the same active or successful digest is idempotent. A
  leased API worker resolves and binds the job to one exact catalog generation,
  `emscripten-wasm32` artifact identity, compiler digest, and runtime executor
  digest before execution, and expired leases
  stop at their persisted attempt budget. Successful worker output is
  publishable only when the completion transaction finds an exact canonical
  code observation for the request's chain, address, code hash, and block hash.
  A stale target becomes a stable terminal failure, while successful results are
  immutable provenance rows projected deterministically to the verified
  contract read model. Publication-guard migrations freeze concurrent DML
  before replacing guards or validating data by taking write-conflicting
  relation locks in the production write order: immutable results, verified
  projections, then terminal job updates. See
  [ADR-0014](../decisions/ADR-0014-durable-verification-identity-and-publication.md).
- Verification prepares duplicate-key-free, inline-source Solidity and Yul
  Standard JSON inputs with bounded server-owned outputs. It compiles the
  original sources and one whitespace-modified copy with the same exact
  compiler, uses their strictly validated differences to locate compiler
  auxdata, and automatically compares every bounded candidate. Matches retain
  Verifier Alliance-style auxdata, library, constructor, and immutable
  transformations and are classified as full or partial. Address publication
  requires a canonical runtime match and the exact immutable job result; a
  creation-only result is never address publication evidence.
- Successful Solidity address verification also retains one immutable bounded
  compilation unit. Factory-derived scans match its complete candidate set
  against canonical non-reverted CREATE/CREATE2 input and exact child runtime
  observations without RPC or recompilation. Each successful `trace@3` or
  `proxy@2` publication generation creates one durable forward event; events
  merge a fork-aware rescan floor, proxy publication wakes pending-runtime
  attempts, and successful pages reset rather than consume the failure budget.
- The scan lease heartbeats during bounded work. Candidate hydration, matching,
  uniqueness, and result construction occur once without a database snapshot;
  the final short transaction rechecks the canonical block, trace, creator code
  epoch, child runtime, immutable compilation identity, and prepared digests.
  Public creation provenance is exact-address and additive: submitted source
  origin remains submitted, children are scoped to the source compilation/code
  epoch, and code-hash artifact reuse never claims target-address creation.
- The `all` or `api` process owns official catalog discovery, checksum-addressed
  artifact caching, and execution. Only the published
  `emscripten-wasm32/list.json` catalog is accepted; this is an
  architecture-independent compiler artifact format, not a Docker or CPU
  platform. Resolution validates approved HTTPS origins, redirects, catalog
  size, artifact size, and SHA-256. The API persists the generation, artifact
  identity, compiler digest, executor kind/policy, and executor digest, then
  atomically binds that complete identity under the worker lease.
  An artifact is streamed into a same-directory temporary file, authenticated,
  then installed under its digest while holding a writer PostgreSQL session
  advisory lock only for the final destination recheck, rename, and stable-file
  validation. Replicas may download one cold miss concurrently, but lock
  waiters do not retain a writer connection between attempts and a cache hit
  does not acquire the lock. Compose keeps this rebuildable cache across
  application replacement; Helm may mount one operator-owned shared PVC or
  retain its default memory-backed cache. Every replica sharing one cache must
  use the same writer database lock domain. Cache persistence never overrides
  catalog freshness or provenance. See
  [ADR-0037](../decisions/ADR-0037-persistent-solcjs-artifact-cache.md).
- Native address verification also accepts a bounded inline Geas v0.3.3 source
  filesystem with a required runtime entrypoint and optional creation
  entrypoint. Each entrypoint is assembled twice with stack checking in fresh
  helper subprocesses; runtime and supplied creation input match exactly, with
  no Solidity transformations or ABI. Only transitively read source files are
  published. The helper's exact Go module checksum and executable digest bind
  once under the job lease without a compiler catalog. See
  [ADR-0039](../decisions/ADR-0039-pinned-geas-verification-executor.md).
- The production image includes one Node 26.7.0 SEA containing the
  `solc@0.8.36` wrapper protocol, plus a canonical read-only runtime manifest
  and any target-rootfs-missing ELF libraries discovered recursively at build
  time. It contains no general Node executable, npm tree, wrapper source, or
  bundled default compiler. Startup verifies every manifest path and digest and
  performs a permission self-test. Each deterministic compilation is a separate subprocess with a
  minimal environment, private temporary directory, 384 MiB V8 heap,
  input/output/time bounds, and whole-process-group termination. The subprocess
  may read only its runtime and selected compiler artifact and receives no
  network, child-process, worker, addon, WASI, FFI, or inspector permission.
  The permission model is defense in depth for trusted checksum-pinned solc-js,
  not a claim that Node isolates malicious JavaScript.
- API readiness is independent of temporary solc catalog availability. When no
  validated catalog generation is available, the version surface reports
  unavailable and Solidity/Yul jobs remain queued rather than being executed
  with an unbound artifact. Geas, proxy, or Sourcify jobs may continue. There is no
  standalone runner, runner network, native compiler fallback, or CPU-platform
  selection. See
  [ADR-0031](../decisions/ADR-0031-api-owned-solc-js-executor.md) and
  [ADR-0040](../decisions/ADR-0040-sea-packaged-solcjs-executor.md).
- Sourcify v2 remains an optional interoperability adapter rather than a local
  trust root. Calling its dedicated verification endpoint is explicit source
  publication consent. Bounded asynchronous polling returns an external result
  only; local address publication still requires the ordinary compiler,
  transformation, runtime, lease, and canonicality checks. See
  [ADR-0024](../decisions/ADR-0024-verifier-v2-workflow.md).
- Verification reads and submissions are separate runtime capabilities.
  API-key-protected job reads and anonymous, free verified-artifact reads
  remain backed by PostgreSQL when public compilation submission is disabled;
  the public configuration advertises only actually usable submission and
  Sourcify surfaces.
- `/v2/api` authentication accepts the legacy API key from a header, query, or
  bounded URL-encoded POST form. Header takes precedence when equal credentials
  are repeated across sources; any conflicting sources are rejected. Form
  bytes are restored before compatibility routing, and form/query credentials
  are never recognized on native routes.
- Operators create, rotate, list, and revoke deployment-wide API keys through
  the single CLI. An explicitly enabled Cookie-authenticated account boundary
  additionally manages only its own scoped keys. Plaintext appears only in
  create/rotate output. Rotation locks the active PostgreSQL row and commits
  the replacement digest plus old-key revocation in one transaction, preserving
  owner, name, scopes, and quota policy. See ADR-0035.
- Native balances and ERC-20 `balanceOf`/`totalSupply` observations use an
  EIP-1898 canonical block-hash selector and recheck that hash after the RPC
  response. Token classifications are retained per observed block hash, so a
  reorg can fall back to an older canonical observation even when the runtime
  code hash did not change. Event-derived NFT deltas only discover candidates;
  ERC-721 owners and ERC-1155 balances require exact `ownerOf`/`balanceOf`
  observations at the same fixed canonical block, carry `rpc_exact`
  confidence, and may be reused only while that block hash remains canonical.
  These exact NFT rows are write-once: identical concurrent writes preserve the
  original audit timestamp, while disagreement returns a typed integrity error
  instead of overwriting a block fact. Token and proxy exact-state calls share
  one sanitized capability classifier, so unsupported or pruned EIP-1898 state
  is `unavailable` and transient transport failures remain retryable.
- Pending transactions come from validated `txpool_content` responses (pending
  + queued pools) and are published as an immutable, expiring PostgreSQL
  snapshot. A cursor is bound to that snapshot; timeout, method absence, or a
  failed poll is reported as unavailable rather than an empty snapshot.
  Sender/nonce slots must be unique across both pools. Consecutive successful,
  unexpired snapshots from the same endpoint may persist a direct old-hash to
  new-hash replacement observation; endpoint changes, failure gaps, stale
  writes, expiry, and disappearance alone never imply replacement. These
  observations share the configured mempool retention. The writer-backed
  transaction detail lookup gives canonical or orphan inclusion precedence,
  then resolves current pending and retained direct replacement observations
  without a live RPC call. See ADR-0036.
- NFT media is never an arbitrary URL proxy. The server first resolves an
  `image` URI from an available metadata document bound to a canonical NFT
  observation, releases the database query, then applies DNS/IP/redirect
  policy, byte limits, MIME and image signature checks. Before returning bytes
  it rechecks that the same exact block-hash observation is still the newest
  canonical version. Metadata source discovery uses one state RPC endpoint and
  an EIP-1898 block-hash selector per NFT observation; exact source and terminal
  document facts are immutable and retained across reorgs. Media success and
  every early authentication or rate-limit error use no-store, restrictive CSP,
  nosniff, and same-origin resource headers.
- The metadata role additionally scans canonical logs for exact ERC-4906
  single/batch and ERC-1155 URI update signals. It retains accepted and
  malformed log facts by block hash, treats event payloads only as triggers,
  and reuses the one-endpoint EIP-1898 source discovery boundary at the event
  block. Batch ranges yield only already discovered IDs one at a time. No
  periodic, manual, browser-triggered, or request-time refresh exists.
- NFT metadata display is a separate canonical PostgreSQL projection. It
  returns bounded inert text and scalar traits plus, when syntactically safe, an
  unverified HTTPS navigation target derived from `image` (with `ipfs://`
  converted through the configured HTTPS gateway). The embedded SPA never
  renders or prefetches that target and requires a bilingual confirmation before
  each opener-free, no-referrer external navigation. The authenticated media
  proxy remains the only server-side image retrieval path.
  A newer pending or failed event refresh may retain the previous canonical
  available document only with explicit latest/content observations and a
  stale marker; reorg automatically removes orphan update influence.

## Operator Recovery Boundary

Repair and reindex intentionally have different authority. `repair --stage
core` can rewrite a known block's core bundle after an RPC refetch, but the
database rechecks canonical identity and finality inside one chain-locked
transaction. Refreshing a finalized height requires an explicit audited
override and still cannot replace its hash or parent.

`reindex --stage proxy|abi|token|stats|trace` schedules or replays immutable
block-hash-scoped jobs. Existing queued or leased jobs remain owned by their
current worker; only terminal jobs may be reset. A proxy/ABI cutover is run in
dependency order, `proxy` before `abi`. Repair does not silently infer the
downstream range an operator intends to rebuild. See
[ADR-0002](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md).

## Source-of-Truth Routing

- `AGENTS.md`: compact repository instruction entry point.
- `docs/development.md`: engineering workflow and change-routing rules.
- This file: current architecture facts.
- `docs/decisions/`: why consequential choices were made.
- `docs/plans/`: pending and completed delivery work.
- `docs/testing.md` and Makefile: stable validation commands.
- `docs/operations.md`: telemetry interpretation and operator repair/admin
  procedures.
