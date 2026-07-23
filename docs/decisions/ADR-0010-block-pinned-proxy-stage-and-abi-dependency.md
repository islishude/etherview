# ADR-0010: Block-Pinned Proxy Stage and ABI Dependency

Status: accepted

## Context

Proxy identity is historical state, not a property of an address. EIP-1967
storage and beacon `implementation()` can change across blocks and forks, and
an EIP-1167 clone may append immutable arguments to its canonical runtime.
Reading any of those facts at `latest`, or combining responses from different
RPC endpoints, can bind an ABI to an implementation that never existed in the
indexed block.

The ABI stage also needs an exact code observation for every transaction, log,
or normalized-trace target it may decode. Only observing creation receipts
would miss genesis predeploys, deployments whose indexing starts above zero,
and contracts first encountered through an ordinary call or log. Finally,
`abi@1` and `proxy@1` are independently leased jobs: without an explicit
dependency, ABI could persist an `unbound` or signature-only result immediately
before proxy discovery makes a verified implementation ABI available.

## Decision

- `proxy@1` is a block-hash-scoped derived stage. It considers successful
  top-level creation receipts, available non-reverted normalized
  `CREATE`/`CREATE2` frames, standard `Upgraded(address)` and
  `BeaconUpgraded(address)` events, exact replay observations, and every
  transaction/log/trace target that has no prior canonical code history.
  Upgrade events and creations always force a fresh observation.
- One state-purpose RPC endpoint is acquired for the entire block attempt.
  `eth_getCode`, `eth_getStorageAt`, and beacon `implementation()` calls all use
  the same `{blockHash, requireCanonical:true}` EIP-1898 selector. The worker
  never falls back to a height or `latest`. Method/selector absence or pruned
  exact state produces an explicit `unavailable` stage result.
- Exact empty runtime code is persisted as a zero-length byte string with
  Keccak-256(empty) as its non-zero code hash. SQL `NULL` remains reserved for
  older observations whose code bytes were deliberately not retained.
- EIP-1167 canonical runtimes and immutable-argument variants resolve their
  embedded implementation. EIP-1967 implementation proxies resolve the
  implementation slot; beacon proxies additionally call the beacon and store
  the final implementation. A simultaneous non-zero implementation and beacon
  slot, a self-reference, zero target, malformed address word, or empty
  implementation runtime is rejected instead of becoming a guessed fact.
- Those invalid proxy shapes are candidate-local evidence failures, not block
  failures. A target with both slots set, a storage address with non-zero high
  bytes, a zero/self/missing-code implementation, or a beacon that reverts or
  returns a zero/non-word implementation keeps its exact code observation but
  produces no proxy observation. Processing continues with every other target
  in the block. By contrast, transport/timeouts remain retryable, exact-state
  or EIP-1898 capability loss remains unavailable, and malformed RPC hex or a
  non-word `eth_getStorageAt` response remains a permanent wire failure.
- `contract_code_observations` and `proxy_observations` retain proxy and final
  implementation code hashes, exact block hash, derived canonicality,
  `high` confidence, and bounded controlled details. Conflict updates may
  refresh canonicality and controlled provenance only when all immutable state
  fields still match. Duplicate processing cannot overwrite a contradictory
  exact-block fact.
- `proxy@1` output, its stage result, and its derived journal commit in one
  PostgreSQL transaction. Reorganizations retain rows and toggle their
  canonical flag with the exact block journal; readers still require the exact
  canonical mapping.
- The PostgreSQL claim query will not lease `abi@1` before the same block has a
  terminal `proxy@1` result of `complete` or `unavailable`. The production ABI
  processor rechecks that dependency under the canonical block lock. A
  complete proxy result permits decoding; an unavailable proxy result makes
  ABI explicitly unavailable, never terminal `unbound`; failed or absent proxy
  state remains retryable and dependency-blocked.
- The initial proxy publication is ABI's claim dependency: it unlocks ABI's
  already queued first generation without requesting another one. A later
  proxy generation requests a source-deduplicated ABI generation and removes
  its exact stale output, result, and journal at the safe generation
  transition. Unowned queued or terminal work is refreshed immediately. A
  leased job keeps its token while the pending generation is durable;
  `Finish`/`Retry`, or the first new-generation claim after lease expiry,
  consumes that request atomically.
- A non-empty normalized Trace that arrives after ABI completion uses its own
  stage generation to request ABI replay so its call data is decoded. It
  requests proxy replay first only when it contains a successful, non-reverted
  normalized `CREATE` or `CREATE2` target; ordinary calls and reverted or
  targetless creations do not change proxy discovery. When proxy replay is
  required, the absent proxy result keeps ABI blocked until that replay has
  consumed the new creation targets. Repeating either source generation cannot
  create a replay loop.

## Consequences

- Genesis predeploys and non-zero indexing starts obtain the code identity
  needed by ABI as soon as they are observed by a core or available Trace
  target.
- Nodes without exact block-hash state may still index core history, but proxy
  and dependent ABI completeness is reported unavailable rather than silently
  mixing current state into history.
- An arbitrary called or logging contract cannot poison proxy enrichment for
  the block by placing ambiguous values in standard proxy slots or exposing a
  reverting lookalike beacon. Valid proxies in that block are still persisted,
  so ABI dependency progress is preserved.
- Trace remains optional and cannot delay the first proxy pass. Any non-empty
  late Trace can improve ABI output; only successful, non-reverted
  `CREATE`/`CREATE2` targets also improve proxy output. Both paths use bounded
  idempotent replay without a permanent replay loop.
- The stage reuses the existing code/proxy tables. The generic durable queue
  stores replay generations and source identities; a future proxy
  representation or rollback contract requires a new stage version and a
  reviewed dependency update in both the queue and processor.
