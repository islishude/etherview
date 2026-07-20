# Architecture Overview

## Runtime Shape

Etherview is a modular monolith packaged as one Go binary. Components are
selected by roles and are instantiated identically whether all roles share one
process or run in separate deployments.

The feature-aware production component manifest is executable architecture:
startup compares it with the exact deduplicated keys registered by the runtime.
The parity suite also proves that `roles=all` is the union of the split-role
graphs, so adding a component without updating both paths fails before serving.

The same supervisor owns the lifecycle of those registered services in every
deployment shape. It advertises process readiness only after all selected
services have entered `Run`, withdraws readiness before canceling them, treats
an early clean exit as a process failure, and bounds peer draining with
`server.shutdown_timeout`. The operational probe combines that lifecycle state
with PostgreSQL liveness. The API probe combines it with durable core-index
readiness, so startup, failure, and termination cannot serve a stale ready
signal.

```text
Execution RPC -> sync/canonicalizer -> PostgreSQL -> durable jobs
                    |                    |          -> enrich/trace/verify/metadata
                    |                    -> runtime status/events -> API replica relays
                    -> expiring pending snapshots
PostgreSQL -> query API -> embedded React SPA
outbox -> optional NATS wake-up
API -> optional Redis cache/rate limit
large blobs -> optional S3-compatible storage
```

PostgreSQL stores all correctness-critical facts, canonical mappings, stage
state, jobs, leases, and outbox records. Optional systems may reduce latency or
storage pressure but never become the only copy of required state.

The application uses pgx through `database/sql` for the shared runtime pool.
Generated sqlc/pgx queries enter production through a small bridge that pins
one stdlib connection for the duration of the callback; this preserves the
generated query boundary without maintaining a second connection pool.

## Chain Correctness

- Each deployment serves one configured chain and binds it with chain ID plus
  genesis hash. Every RPC endpoint is verified against both.
- Blocks are identified by hash. Canonical height is a mutable mapping and
  orphan facts remain queryable.
- Core readiness means block, transaction, receipt, log, and withdrawal facts
  are durably committed. Enrichment has independent completeness states.
- New-head subscriptions are hints. Polling, ancestry checks, and gap scans are
  authoritative.
- The upstream head, indexed position, readiness, and bounded head/reorg/status
  replay window are PostgreSQL facts. Each API replica tails that ledger
  independently; see
  [ADR-0004](../decisions/ADR-0004-durable-runtime-status-and-events.md).
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
  block. Rebuilding ABI, token, statistics, or trace output is an explicit,
  block-hash-scoped reindex operation; active leases are never reset.
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
- `stats@2` derives intervals only from the exact canonical parent. The exact
  configured indexing start has null interval and TPS; every later block
  requires a positive timestamp delta. Aggregate TPS divides transactions by
  total known interval rather than averaging block rates. A block without blob
  transactions has null blob base fee and zero blob burn, while receipt blob
  gas without the required header inputs is a permanent inconsistency; see
  [ADR-0011](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md).
- A trace attempt acquires one trace-purpose RPC endpoint for the entire block.
  Geth `callTracer` may fall back to the compatible `trace_transaction` method
  only on that same endpoint, so one normalized tree never combines node
  histories. Every returned frame is bounded and, where the trace API carries
  identity fields, bound to the requested block hash, block number,
  transaction hash, and transaction position. A completed mined-transaction
  trace has exactly one root whose sender, target/creation kind, value, and
  input match the canonical core transaction. A root-only tree means the
  transaction made no internal calls; a missing stage, unavailable/failed
  stage, or empty `trace_transaction` response is never represented that way.
  A mined transaction temporarily reported as not found remains retryable;
  only a missing method or recognized pruned-history response marks the
  capability unavailable. Payload, frame, input/output-data, and error-text
  budgets apply independently to each transaction and cumulatively to the
  complete block attempt. Work decoded before an adapter fallback remains
  charged to that block budget.
- Derived journal payloads contain only controlled relation-level canonicality
  transitions. They do not contain untrusted RPC data and do not claim storage,
  rollback, or replay of opcode/raw traces; trace journaling covers only the
  normalized call tree.
- `proxy@1` acquires one state endpoint per immutable block. Every code,
  EIP-1967 storage, and beacon `implementation()` read uses that endpoint with
  the same EIP-1898 block-hash selector; exact-state absence is unavailable and
  never falls back to a height or `latest`. It observes creations, standard
  upgrade events, available normalized `CREATE`/`CREATE2` frames, exact
  replays, and ABI-consumed transaction/log/trace targets that lack canonical
  code history. Thus genesis predeploys and non-zero indexing starts receive
  exact code identities when first used. Exact empty code is stored as a
  zero-length value with Keccak-256(empty), not SQL `NULL`.
- Proxy observations retain EIP-1167 immutable-argument variants, direct
  EIP-1967 implementations, and the final implementation behind a beacon.
  Proxy and implementation code hashes, block hash, canonicality, confidence,
  and bounded controlled provenance are one idempotent stage transaction. A
  reorg retains orphan rows and toggles them with the proxy journal; see
  [ADR-0010](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md).
  Ambiguous slots, self/empty implementations, and reverting or malformed
  beacon semantics reject only that candidate after its code observation;
  they cannot fail the block or prevent valid peers from completing. Transport
  errors, exact-state capability loss, and malformed RPC wire remain distinct
  retry, unavailable, and permanent stage outcomes.
- ABI candidates are looked up only by exact chain, target address, runtime
  code hash, context block number/hash, and an inclusive range covering that
  context. Direct verified artifacts outrank verified historical proxy
  implementation artifacts, which outrank re-hashed signature candidates.
  PostgreSQL fixes those sources to `verified`, `high`, and `guess`
  respectively. Candidate decoding and recursive dynamic-offset traversal
  share one node, work, and byte budget for the complete decode, so aliased
  offsets cannot multiply work outside the configured bound. Array cardinality
  is independent of the top-level argument limit, and Solidity `Error(string)`
  and `Panic(uint256)` remain decoder-local rather than signature-database
  bindings; see
  [ADR-0009](../decisions/ADR-0009-block-bound-abi-provenance.md).
- `abi@1` consumes existing canonical code and proxy observations. PostgreSQL
  claim selection and the production processor both require the exact
  `proxy@1` result first. Complete proxy facts permit decoding; unavailable
  proxy state makes ABI unavailable instead of terminal `unbound`, while a
  failed or absent proxy result remains dependency-blocked. ABI does not wait
  for Trace. Any normalized traces already present are decoded in the same
  atomic stage transaction. If Trace arrives later, it requests one
  source-deduplicated generation for proxy and ABI and removes stale ABI output
  at the safe generation transition. The absent proxy result blocks ABI until
  one proxy replay consumes the new creation targets. Queued work is refreshed,
  leased work keeps its token until completion or expiry, and repeating the
  same source generation then quiesces.

## Partition Lifecycle and Identity Boundary

Block-scoped fact tables use fixed half-open ranges of 1,000,000 block
numbers. The partition manager covers `transaction_inclusions`, `receipts`,
`logs`, `withdrawals`, `token_events`, `token_balance_deltas`,
`normalized_traces`, `abi_decodings`, and `address_activities`. Before a core bundle can write
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
  latest canonical logical name, token, or verified code wins; pruning retains
  every reorgable version above finality and rejects cursors older than the
  retained generation floor. Search-source chain identity is immutable, and
  catalog trigger/prune functions bind their own migration schema instead of
  inheriting a pooled connection's `search_path`.
- Optional API capabilities return a typed unavailable error when no fresh,
  authoritative source exists; an empty successful list means the capability
  was available and observed no matching objects.
- Price and external-name adapters persist short-lived success or stable
  failure facts in PostgreSQL. Every first-page dotted search must obtain a
  fresh resolution before opening its read snapshot, then verify that exact
  name/address is visible at the chosen canonical tip and catalog generation;
  an unavailable refresh never falls back to stale catalog data. Its cursor
  freezes the accepted address and later pages do not refetch after TTL expiry.
  A name success takes a key-share lock on its exact canonical block through
  publication and is immutable for its registry/name/block-hash identity; an
  identical concurrent write preserves the first fact without catalog churn,
  while a conflict is unavailable rather than an overwrite. Adapter fetches
  use the shared SSRF-safe HTTPS client outside API read transactions. An
  API-only role may use a state-purpose RPC endpoint without configuring
  history RPC, but state calls retain exact block binding and typed capability
  failures.
- The maintenance role owns a PostgreSQL-only search-catalog housekeeper in
  the same production component graph used by `roles=all` and split processes.
  It runs immediately and on a configured interval, uses a chain-scoped
  transaction advisory lock, retains the configured finalized-aware catalog
  generation window, and deletes one bounded batch of expired adapter
  observations. Cleanup failure emits a stable redacted code and retries with
  bounded backoff without making readiness or core correctness depend on it.
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
  and status use POST; proxy status uses GET, but both proxy operations report
  unavailable until a durable proxy verification workflow exists.
- Native and compatibility verification both reject empty runtime bytecode or
  a code hash that differs from its Keccak-256 digest. Successful worker output
  is publishable only when the completion transaction finds an exact canonical
  code observation for the request's chain, address, code hash, and block hash.
- `/v2/api` authentication accepts the legacy API key from a header, query, or
  bounded URL-encoded POST form. Header takes precedence when equal credentials
  are repeated across sources; any conflicting sources are rejected. Form
  bytes are restored before compatibility routing, and form/query credentials
  are never recognized on native routes.
- Operators create, rotate, list, and revoke API keys through the single CLI.
  Plaintext appears only in create/rotate output. Rotation locks the active
  PostgreSQL row and commits the replacement digest plus old-key revocation in
  one transaction, preserving the key name and quota policy.
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
- Pending transactions come from one validated pending-block response and are
  published as an immutable, expiring PostgreSQL snapshot. A cursor is bound
  to that snapshot; timeout, method absence, or a failed poll is reported as
  unavailable rather than an empty snapshot.
- NFT media is never an arbitrary URL proxy. The server first resolves an
  `image` URI from an available metadata document bound to a canonical NFT
  observation, then applies DNS/IP/redirect policy, byte limits, MIME and image
  signature checks, and returns the bytes with no-store headers.

## Operator Recovery Boundary

Repair and reindex intentionally have different authority. `repair --stage
core` can rewrite a known block's core bundle after an RPC refetch, but the
database rechecks canonical identity and finality inside one chain-locked
transaction. Refreshing a finalized height requires an explicit audited
override and still cannot replace its hash or parent.

`reindex --stage token|stats|trace` schedules or replays immutable
block-hash-scoped jobs. Existing queued or leased jobs remain owned by their
current worker; only terminal jobs may be reset. Repair does not silently infer
the downstream range an operator intends to rebuild. See
[ADR-0002](../decisions/ADR-0002-identity-bound-repair-and-explicit-reindex.md).

## Source-of-Truth Routing

- This file: current architecture facts.
- `docs/decisions/`: why consequential choices were made.
- `docs/plans/`: pending and completed delivery work.
- `docs/testing.md` and Makefile: stable validation commands.
