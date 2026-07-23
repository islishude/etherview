# Etherview Repository Instructions

This file contains durable, repository-wide working rules. Task progress belongs
in `PLAN.md` and `docs/plans/`, not here.

## Before Starting Work

1. Read `PLAN.md`, the relevant `docs/plans/Pxx-*.md`, and every referenced ADR
   or testing rule.
2. Select only a `todo` item whose dependencies are complete and mark it
   `in_progress` before changing implementation files.
3. Do not let two agents claim the same work-item ID.
4. Preserve staged, unstaged, and untracked user work. Review all three before
   reporting completion.

## Plan Maintenance

- Implementation, tests, and plan state are one atomic change.
- When a work item finishes, mark it `done`, update its acceptance checkboxes,
  record concise verification evidence, and update `PLAN.md` if the child plan
  status changed.
- Never delete completed work items or reuse IDs. Use `dropped` with a reason,
  or `superseded` with a link to the replacement.
- A blocked item must state the objective blocker and the condition that clears
  it. Continue with other unblocked items when possible.
- Long-lived code TODOs must reference a plan item such as `P10-T04`.
- Run `make plan-check` whenever plan documents change.

## Architecture Invariants

- `serve --roles=all` and split-role deployments execute the same component
  implementations and persist the same semantics. The production component
  manifest and the actually registered component keys must agree at startup;
  update their parity test whenever a durable role component changes.
- Every runtime role uses the shared component supervisor. Process readiness is
  published only after every selected service enters its production `Run`
  path, and is withdrawn before cancellation. An early clean exit is a failure;
  peer shutdown is bounded by `server.shutdown_timeout` and must name any
  component that does not stop within that budget.
- Helm starts every application Pod only after checksum-aware `migrate status`
  succeeds, while the revision migration Job runs the advisory-locked
  `migrate up`. ConfigMaps must reject inline database, RPC, API-pepper, NATS,
  Redis, and S3 credentials in favor of existing Secret or ExternalSecret
  references, and every NetworkPolicy exception must remain explicit.
- PostgreSQL is the correctness source for chain data, canonicality, jobs,
  leases, and outbox state. NATS, Redis, and S3 must remain optional.
- Core block, transaction, receipt, log, and withdrawal ingestion must not wait
  for trace, metadata, verification, pricing, or other enrichment.
- A successful production enrichment attempt must commit its derived output,
  exact job/generation stage result, controlled journal, and durable-job
  completion in one lease-fenced PostgreSQL transaction. Public/readiness
  queries use only `published_block_stage_results`; direct fixture rows and
  `stale_canonical_skipped` audit rows are never publication evidence. An
  enrichment job may otherwise enter a terminal state only in the same
  transaction that upserts its exact job/generation result; failed and
  unavailable terminal results have no journal. Expired exhausted leases
  follow that path too, and the row's durable
  `max_attempts` is the only retry budget. Replay reuses that job identity and
  advances a source-deduplicated generation. It never steals an active lease:
  `Finish`/`Retry` consumes pending replay, while an expired lease's first
  next-generation claim atomically clears the old exact result, journal, and
  ABI output before exposing the new lease.
- A canonical core outbox identity has a monotonic generation. Reattaching the
  same block hash reopens its existing row and requests a fresh enrichment job
  generation; an older orphan wake that races the reattach is a publishable
  stale event, not a permanent retry.
- A trace job uses one RPC endpoint for its whole block, including any
  `debug_traceTransaction` to `trace_transaction` fallback. A completed mined
  transaction trace has exactly one root whose block/transaction identity and
  root call fields match the canonical core inclusion; an empty RPC array is
  invalid, not a successful empty call tree. A node reporting a mined
  transaction as temporarily not found is retryable; only an actual missing
  method or known pruned historical state is a terminal capability gap. Trace
  payload, frame, data, and text limits apply both per transaction and across
  the complete block attempt, including discarded fallback work.
- Store block-scoped facts by block hash. A block number alone is never a stable
  identity, and reorg handling must retain orphan facts.
- ABI material is scoped by chain, target address, runtime code hash, exact
  context block hash, and a covering validity range. Source determines
  confidence: verified artifacts are `verified`, verified historical proxy
  implementations are `high`, and signature-database candidates are `guess`;
  a signature candidate must never be represented as verified. One ABI decode
  shares node, work, and byte budgets across all selector candidates and every
  aliased dynamic-offset branch; array elements use their own limit rather than
  the top-level argument limit. Solidity built-in errors remain decoder-local.
- Proxy/code discovery uses one state RPC endpoint and one EIP-1898 block-hash
  selector for the entire `proxy@1` attempt; it never falls back to a height or
  `latest`. It must create exact code observations for ABI-consumed targets
  without canonical code history, including genesis predeploys and non-zero
  indexing starts. Exact empty code is a zero-length value with
  Keccak-256(empty), not an omitted observation.
- Invalid proxy-shaped contract state is candidate-local: ambiguous slots,
  invalid/self/empty implementations, and reverting or invalid beacon returns
  retain exact code but must not fail the block or poison ABI progress for
  valid peers. Transport, exact-state capability, and malformed RPC wire
  failures retain their retry/unavailable/permanent stage semantics.
- Production `abi@1` is claim- and processor-gated on the exact block's
  `proxy@1` result. Proxy unavailability makes ABI unavailable rather than
  terminal `unbound`. Late proxy or normalized CREATE/CREATE2 facts request
  source-deduplicated downstream generations. Queued work is refreshed,
  active leases remain owned, and completion or expired-lease reclaim consumes
  the pending generation while atomically removing stale ABI output. Repeating
  the same source generation must quiesce.
- Block-number fact partitions use fixed 1,000,000-block ranges. Core writes
  must provision and attach the target range inside the chain-locked write
  transaction; DEFAULT partitions are atomic recovery buffers and never
  steady-state storage.
- The core checkpoint is derived only from the durable coverage range beginning
  at the persisted configured start. A higher live island never implies
  readiness; authoritative head polling and historical leased backfill remain
  independent execution lanes.
- Safe/finalized markers require an uninterrupted canonical parent chain from
  the current tip through the lowest requested marker. A finalized-crossing,
  over-depth, no-common-ancestor, or source-inconsistent fork halts both sync
  lanes, records a stable Prometheus reason, and remains scrapeable until an
  operator cancels and restarts the process after repair.
- Public amounts that can exceed JavaScript precision are serialized as strings.
- RPC credentials, compiler credentials, private keys, and server-only config
  must never enter the embedded SPA or logs.
- Log hostile-boundary failures using stable error codes or concrete error
  types. Do not attach raw RPC, database, compiler, metadata, or panic errors
  to structured logs because nested messages may contain credentials or input.
- OTLP tracing is disabled unless an explicit server-only collector endpoint
  is configured. Collector headers are also server-only Secret inputs. Its
  exporter is flushed through the shared bounded supervisor and exporter loss
  never changes readiness or request results. A remote sampled parent keeps
  its trace identity, but its export decision uses fresh server-side randomness
  independent of the caller-controlled trace ID and replay. An escaping HTTP
  panic preserves the selected native, compatibility, or operational boundary;
  after a committed stream it records a bounded panic signal and aborts without
  appending an envelope. Both HTTP servers discard net/http panic text and log
  only a stable code. Operational
  queue, verification, and repair snapshots come from PostgreSQL; a failed
  refresh retains the prior snapshot and exposes staleness instead of
  fabricating zero. Current backlog gauges scan only partial-indexed active
  rows; because every split-role replica exports the same chain snapshot,
  dashboards and alerts deduplicate them with `max`, never `sum`. Per-worker
  result counters use `sum`/`rate`. Helm ServiceMonitors select and relabel only
  their exact release and namespace, and every bundled alert retains that
  release/namespace identity. Telemetry labels remain closed and low-cardinality.
- Public HTTP changes start in `api/openapi.yaml`. Regenerate both Go and
  TypeScript contracts with the Makefile; generated files are never edited by
  hand.
- Native JSON successes use the generated `{data,meta}` contract, and every
  JSON operation declares the shared generated error envelope. Cursor inputs
  and emitted `meta.next_cursor` values use the bounded `OpaqueCursor` schema;
  clients must not decode or construct them.
- SQL changes start in `internal/db/queries/` and generated sqlc files are never
  edited by hand. Production sqlc calls use the shared `internal/db` bridge so
  the generated pgx queries execute only on a pinned pgx stdlib connection.
- Native `/api/v1` credentials are accepted only through `X-API-Key`. The
  `apikey` query parameter and bounded URL-encoded POST form field are confined
  to the exact Etherscan `/v2/api` compatibility boundary. Conflicting header,
  query, or form credentials are rejected before authentication.
- API key plaintext is revealed only by create/rotate CLI output. PostgreSQL
  stores keyed digests; rotation must atomically persist one replacement and
  revoke the old prefix while preserving its name and quota policy.
- Authentication and rate-limit failures occur before route handlers but must
  still preserve the selected boundary contract: native errors include a
  request ID, while `/v2/api` uses the Etherscan-compatible envelope.
- Browser E2E serves the built `go:embed` distribution through the Go test
  harness. A Vite development server is not evidence for binary deep links,
  fallback routing, CSP, or asset headers.
- The SPA shell fallback is limited to safe, non-reserved GET requests that
  accept HTML. API, compatibility, operational, asset-shaped, malformed, HEAD,
  and mutating misses never receive it. The index and misses are `no-store`;
  only exact Vite content-hashed assets are immutable. Every response keeps the
  repository's `default-src 'none'` CSP, `nosniff`, and same-origin security
  headers, with no inline/evaluated script or external browser runtime.
- Browser explorer requests use the `api/openapi.yaml`-generated TypeScript
  `paths` client. Only its fixed same-origin `/api/v1` adapter may invoke
  `fetch`; direct backend paths, runtime environment injection, and server
  credentials elsewhere in production SPA sources are forbidden. Wallet RPC
  methods remain confined to one injected EIP-1193 provider boundary. Raw
  providers never escape that module; EIP-6963 UUID collisions cannot replace
  an existing page-session provider, and the closed RPC allowlist rechecks the
  active account and configured chain before bounded `eth_call` or
  `eth_sendTransaction`. Calls and results bind one wallet-session revision.
  A transaction that reaches the provider without a trustworthy hash or
  matching completion session has an unknown outcome, never a safe-to-retry
  failure. Hostile provider error text is never rendered.
- Tailwind is a pinned build-time Vite plugin, never a CDN or browser runtime.
  Its Preflight layer stays disabled because the established SPA base styles
  own normalization; shared theme tokens and layout primitives use generated
  utilities while preserving the existing light/dark data-attribute contract.
- Core `repair` is identity-bound: it may refresh facts only for the same
  canonical chain/height/hash and never moves canonicality or a checkpoint.
  Its canonicalizer path must not call normal fork-choice `Apply`. Derived
  `reindex` is a separate, explicit operation. Neither operation may steal a
  queued job or an active lease.
- Split-role status and head/reorg replay are PostgreSQL facts. In-process
  trackers, fanout, WebSocket wakes, and future NATS notifications are latency
  aids and may never become the API source of truth.
- A configured query cache is invalidated from the durable runtime-event relay
  before that event is published to SSE clients. Failed invalidation must not
  advance the relay cursor; invalidators are idempotent because retries are
  expected. An optional Redis adapter must disable or bypass its cache when
  Redis is unavailable; backend loss alone must not make invalidation fail.
- NATS messages are content-free, coalesced wake hints; outbox dispatch and job
  claims always re-read PostgreSQL and retain periodic polling. Redis rate-limit
  failures fall back to the process-local bucket. S3-compatible trace cache
  keys bind the exact block hash and published job generation, and every hit is
  checksum/limit validated plus PostgreSQL-postchecked; NFT media, durable job
  payloads, and stage completion never depend on object storage.
- Pending transactions are expiring observations from one successfully polled
  RPC endpoint and one immutable PostgreSQL snapshot. An unavailable poll is
  not an authoritative empty mempool.
- Event-derived token deltas are not authoritative current state. Balance and
  supply APIs must use a fixed canonical-block RPC observation or a persisted
  reconciliation at that exact block; otherwise they report unavailable. NFT
  list candidates may come from canonical event activity, but their inclusion
  must not depend on the sign or sum of event-derived deltas.
- External RPC or metadata calls must not run while an API read transaction is
  holding a PostgreSQL snapshot. Copy bounded inputs, close the transaction,
  then use an immutable block identity and post-call canonicality check.
- Search cursors bind both the exact canonical tip and a retained per-chain
  catalog generation. Search-source changes are temporal and chain-immutable;
  compaction may collapse only a finalized baseline and must retain every
  reorgable source version above finality. An expired generation is an invalid
  cursor, never permission to silently switch snapshots. Search catalog
  trigger and prune functions must bind their migration schema explicitly;
  they may not inherit a pooled connection's mutable `search_path`.
- `stats@2` gives the exact configured indexing start a null interval/TPS and
  requires every later block's exact canonical parent and positive interval.
  Aggregate TPS is transactions divided by total known interval. No-blob
  blocks have null blob base fee and zero blob burn; inconsistent receipt and
  header blob facts are permanent failures rather than fabricated statistics.
- External name and price adapters are bounded optional capabilities. Persist
  a name success only while holding a key-share lock on its exact canonical
  block and never overwrite a different registry/name/block-hash identity;
  an identical concurrent write is a no-op. A first-page dotted search must
  refresh the name, verify its exact address in the new canonical snapshot, and
  filter name-source candidates to that accepted address; an issued cursor
  freezes that identity and does not refetch on later pages. Name observation
  caches include a non-secret SHA-256 namespace of the configured provider URL,
  so a provider change never reuses old successes or failures. Cache failures
  only through their short TTL and expose stable typed states/codes without
  nested upstream text.
- Search and adapter retention runs as a maintenance-role supervisor component
  in both monolith and split deployments. It uses only PostgreSQL, a
  chain-scoped advisory transaction lock, finalized-aware catalog pruning, and
  a bounded expired-observation batch. Sweep failure is a redacted retryable
  event and must not withdraw readiness or become a correctness dependency.
- Token classification and metadata observations are immutable per exact block
  hash, even when the address and runtime code hash repeat. Current reads may
  reuse exact state observations only while that same block hash is canonical.
- Exact ERC-721 owner and ERC-1155 balance observations are also write-once for
  their full block-hash identity. An identical concurrent observation is an
  idempotent no-op that preserves its audit timestamp; a conflicting result is
  a typed integrity failure and must never overwrite the first durable fact.
  Token and proxy EIP-1898 reads share the same unavailable-versus-retryable
  capability classification and never expose raw RPC error text.
- Token detection and state reconciliation pin every RPC call for one
  block-scoped operation to one endpoint. Endpoint selection may rotate only
  between blocks; no single result may combine state from multiple nodes.
- Public metadata media is resolved only from a current canonical stored NFT
  document, fetched through the SSRF-safe client, signature-checked, rechecked
  against the same newest canonical exact-block observation after the external
  call, and never permanently mirrored. Do not expose a general-purpose URL
  proxy. NFT metadata source discovery pins one state RPC endpoint and one
  EIP-1898 block-hash selector per observation; exact source and terminal
  document facts are immutable and retained across reorgs.
- Verification requests must bind `code_hash` to the Keccak-256 hash of a
  non-empty runtime bytecode. Submission identity covers the exact request and
  server-derived hard-isolation policy; only the same active or successful
  digest is idempotent, while failed or cancelled work may be resubmitted. A
  worker binds the first compiler kind, artifact digest, and isolation property
  before execution, and later leases may not change them. Durable attempt count
  is the only reclaim budget. Completion additionally requires an exact
  canonical `contract_code_observations` row for the same chain, address, code
  hash, and block hash; a stale target is a terminal failure. Successful
  results are immutable job/request/compiler facts, and `verified_contracts`
  is only their deterministic exact-first projection. A migration that
  strengthens this publication boundary must take write-conflicting relation
  locks before replacing guards or validating rows, in production write order:
  `verification_results`, `verified_contracts`, then `verification_jobs`.
- Verification Standard JSON rejects duplicate keys and external source
  indirection, then canonicalizes a version-aware, exact-target
  `outputSelection` before persistence and digesting; the repository repeats
  that step for direct submissions. Caller wildcard/group outputs are never
  compiled; pre-0.4.0 Vyper receives only the minimal non-target `userdoc`
  selections its formatter requires. Vyper layout is required from 0.4.1,
  while 0.3.10 through 0.4.0 authenticate immutable size from creation auxdata;
  older or metadata-disabled formats without a size declaration may match only
  a zero-length immutable suffix. Solidity `metadata.appendCBOR=false` disables
  metadata-only matching and footer-boundary interpretation entirely.
- Compiler artifacts are canonical SHA-256 identities, not version labels.
  Process downloads are private-only, proxy-free, redirect-free, SSRF-safe,
  size-bounded, and installed only as checksum-verified `0500` cache files.
  Public verification requires startup-validated digest-pinned local images;
  compilation never pulls or uses a network and must force-remove its randomly
  named container before accepting any outcome, including a runtime panic.
  Failed or hung removal and compiler-runtime invariant failures are fatal to
  the worker and must not terminalize the leased job.
- Sourcify is an optional, untrusted interoperability boundary. Import must
  accept only an address and optional constructor suffix, server-resolve its
  chain, code hash, block hash, runtime bytecode, and creation input from exact
  canonical PostgreSQL facts, then enter the normal durable verification path;
  lookup is never publication evidence. Upload requires both the persisted
  request opt-in and separate call-site consent, and sends only the bounded
  Standard JSON, compiler version, and contract identifier through the
  restricted outbound client.

## Changes Requiring Documentation

- Update or add an ADR before changing a public API, persistent data contract,
  security boundary, or the monolith/split-role runtime model.
- Keep architecture facts in `docs/architecture/`, decisions in
  `docs/decisions/`, and stable commands in `docs/testing.md` or the Makefile.
- Agents are authorized and required to update this file without separate
  approval when durable repository commands, invariants, layout, or review
  rules change. Do not record transient task state here.
- Add a nested `AGENTS.md` only when a subtree has genuinely additional rules;
  never copy the root rules into it.

## Verification and Review

- Use the Makefile targets documented in `docs/testing.md`.
- Go, Node, and npm declare minimum supported versions rather than exact local
  runtime pins. Run `make toolchain-check`; compatible newer stable releases
  must pass, while older, malformed, and prerelease versions fail. `.nvmrc`,
  `packageManager`, CI, and container-builder versions are reproducible defaults,
  not ceilings; keep them compatible with the documented minimums when updated.
- `doctor` reports a configuration as valid only after runnable-role
  validation; a parseable file with missing RPC, database, or safety inputs is
  invalid and must produce a non-zero exit.
- Add regressions for malformed RPC data, reorgs, optional capability loss,
  numeric boundaries, and security-sensitive parsers.
- A work item is not complete until its targeted tests and applicable common
  gates pass and their evidence is recorded in its plan.
- Use `make generate-check` after OpenAPI, SQL query, or embedded-SPA changes;
  use `make plan-check` after governance-document changes.
