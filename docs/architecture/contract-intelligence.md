# Trace, proxy and ABI provenance

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns trace, proxy and ABI provenance.
See [operator procedures](../operations/recovery.md) and
[acceptance requirements](../testing/contracts.md) for execution and validation.

- [Exact-block traces](#exact-block-traces)
- [Proxy identities and publication](#proxy-identities-and-publication)
- [ABI attribution and execution identity](#abi-attribution-and-execution-identity)

## Exact-block traces

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

- `trace@4` retains direct frame failure separately from ancestor rollback. It
  validates every returned callTracer log against the persisted receipt log's
  global index, emitter, topics, and data before recording a trace path and
  execution code address. `DELEGATECALL` and `CALLCODE` expose frame `to` as the
  storage/call context and use a separate exact execution identity for ABI and
  log attribution. A zero-log
  ordinary trace remains publishable; partial, duplicate, misplaced, or
  contradictory tracer logs fail permanently.

## Proxy identities and publication

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

- All proxy reads choose the latest successfully completed canonical proxy
  publication, with identities, summaries and bindings bounded by its exact
  block hash and publication generation. Newer pending, unavailable or failed
  processing does not replace that snapshot; no complete publication returns
  not-ready. The browser holds the response until manual refresh.

- Page loads, manual refresh and pre-operation checks use the same snapshot
  selection. An unprocessed core tip does not block interaction with the completed
  snapshot. A changed identity or binding still requires refreshing the target.

- Once the current proxy projection recognizes mechanism `cwia`, the shell's
  Code tab omits the direct Verified artifact and submission panel. Source and
  ABI presentation remains attached to the fixed implementation identity;
  ordinary contracts and other proxy mechanisms retain their direct artifact
  surface. Published exact-address source remains readable independently of
  pending, unavailable, or failed proxy classification. This does not enable
  interaction or verification-submission controls. Manual refresh applies newly
  published classification, including hiding a recognized CWIA shell artifact;
  no classification recovery polling is introduced.

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

- Proxy detection always runs through one shared exact-block context and
  evidence resolver. Standard/CWIA detectors return authoritative relations
  separately from presentation outcomes; priority cannot grant a binding or
  interaction authority. Required fact failures retain stage error semantics;
  optional family failures are explicit unknowns. Safe and Diamond remain
  independently configurable. Public `proxy_detection` exposes published
  summaries without a second visibility gate. Safe shell and official singleton
  identities remain independent, and CWIA is not an OpenZeppelin outcome; see
  [ADR-0032](../decisions/ADR-0032-evidence-based-proxy-detection.md).
  A non-Safe fingerprint miss adds no Safe RPC. Ambiguous slots, self/empty
  implementations and reverting or malformed Beacon semantics reject only that
  candidate after retaining its code observation. Transport errors, exact-state
  capability loss and malformed RPC wire remain distinct retry, unavailable
  and permanent stage outcomes.

- ERC-2535 Diamond identity is selector-scoped: an exact-block Loupe snapshot
  maps each bytes4 selector to an external facet or to an immutable function on
  the Diamond itself. No facet is projected as the singular implementation.
  Bounded DiamondCut facts retain block/transaction/log/cut order and orphan
  branches; published canonical intervals drive historical function ABI
  attribution, while calls still target the Diamond. Enumeration truncation,
  deterministic validation sampling, absent standard `diamondCut`, and
  inconsistent Loupe/history evidence remain distinct public states; see
  [ADR-0038](../decisions/ADR-0038-selector-scoped-erc2535-diamond-identity.md).

## ABI attribution and execution identity

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

- Transaction logs prefer a published `trace@4` attribution and decode against
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

- `abi@5` first materializes each transaction root's effective execution
  identity without changing `state_diff@3`. Exact raw identities are retained;
  only a known first-hop delegate with a missing code hash may be recovered
  from the matching `trace@4` root plus canonical code history strictly before
  that transaction. Code changes are replayed by transaction index, so current
  and later changes cannot flow backward. The identity, its evidence source,
  ABI decoding, journal, and publication commit atomically. Until `abi@5` is
  published, public readers retain raw state-diff semantics and ignore old ABI
  generations.

- `abi@5` consumes existing canonical code and proxy observations. PostgreSQL
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
  is insufficient. Constructor validation in enrichment, catalog reads and
  verification shares one bounded canonical ABI decoder; resource admission
  precedes unpacking and preserves each caller's error classification.
  Evidence withdrawal uses a distinct
  `stage-invalidation` identity; successful replacement publication uses
  `stage-completion`, so the latter cannot be suppressed by the former. The ABI
  dependency prevents the new ABI generation from publishing before the
  replayed Proxy generation, including when the Trace contains only ordinary
  calls or withdraws prior creation evidence. Queued work is refreshed, leased
  work keeps its token until completion or expiry, and repeating the same
  source generation then quiesces. Loss of Trace capability withdraws dependent
  exact Proxy and ABI publications rather than preserving stale evidence.
