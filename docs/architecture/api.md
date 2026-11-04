# Public API and query contracts

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns public read contracts, query snapshots and browsing semantics.
See [operator procedures](../operations/chain-data.md) and
[acceptance requirements](../testing/chain-data.md) for execution and validation.

- [Public Boundaries](#public-boundaries)
- [Search identities and retention](#search-identities-and-retention)
- [Etherscan compatibility](#etherscan-compatibility)
- [Partial explorer browsing](#partial-explorer-browsing)
- [Contract event log browsing](#contract-event-log-browsing)

## Public Boundaries

The native API mux is composed from explicit operations, identity/billing,
native, catalog, analytics, metadata, verification, and external-surface route
modules. Production assembly declares its required capability set, and handler
construction validates every dependency before registering any route. Optional
features keep their stable disabled routes and typed unavailable responses;
enabled production modules never infer dependencies through reader/catalog/Web
type assertions or silently omit a route.

- Native HTTP API lives under `/api/v1`; Etherscan compatibility lives at
  `/v2/api`; operational endpoints are separate.

- Large integers cross JSON boundaries as strings.

- Compiler execution and metadata retrieval are hostile-input boundaries and
  require resource isolation, network policy, and explicit size/time limits.

- `api/openapi.yaml` is the public HTTP contract. Go server models and SPA
  TypeScript types are generated from that single specification.

- Native JSON success models use `{data,meta}` and every JSON operation declares
  the common `{error:{code,message,details,request_id}}` failure model. Cursor
  parameters and emitted `meta.next_cursor` values share the bounded opaque
  cursor schema.

- Optional API capabilities return a typed unavailable error when no fresh,
  authoritative source exists. Its optional details contain only controlled
  `capability`, `state`, and `code` identifiers; an empty successful list means
  the capability was available and observed no matching objects.

## Search identities and retention

Price adapters retain a bounded HTTPS observation contract. Their failure cannot
manufacture an authoritative price; optional capability errors follow the public
error contract above.

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

- The maintenance role owns a PostgreSQL-only search-catalog housekeeper in
  the same production component graph used by `roles=all` and split processes.
  It runs immediately and on a configured interval, uses a chain-scoped
  transaction advisory lock, retains the configured finalized-aware catalog
  generation window, and deletes bounded batches of expired adapter
  observations plus ENS snapshots and retained generations. Cleanup failure
  emits a stable redacted code and retries with
  bounded backoff without making readiness or core correctness depend on it.

## Etherscan compatibility

- The exact Etherscan V2 module/action, method, parameter, API-key, capability,
  unavailable-action, and wire-difference contract is maintained in the
  [Etherscan V2 compatibility matrix](etherscan-v2-compatibility.md).

- The compatibility allowlist includes bounded canonical withdrawals,
  per-block Core/Trace/Token counts, current-EOA first funding, and mutually
  exclusive legacy versus advanced from/to list selectors. Address ERC-20 and
  ERC-721 holding reads require genesis-through-tip Core and Token coverage and
  fail closed when a dense requested page cannot be proven within 1,000
  candidates; they never introduce a holder ledger or RPC proxy.

- Every compatibility list additionally caps `page * offset` at 10,000, and a
  log request covers at most 100,000 tip-clamped blocks. Fixed-direction miner
  and timestamp queries use dedicated indexes; log topic zero uses the typed
  index only when the full AND/OR expression makes it mandatory.

- Etherscan address-only ABI and source lookups resolve the address's latest
  canonical code observation, then require a verified artifact with the same
  chain, address, code hash, and a validity range covering the canonical tip.
  Missing code state is unavailable and a code hash without such an artifact
  is unverified; older open-ended artifacts are never returned as a fallback.

- `/v2/api` authentication accepts the legacy API key from a header, query, or
  bounded URL-encoded POST form. Header takes precedence when equal credentials
  are repeated across sources; any conflicting sources are rejected. Form
  bytes are restored before compatibility routing, and form/query credentials
  are never recognized on native routes.

## Partial explorer browsing

Native token/address transfer and internal-call lists, token discovery,
address asset candidates, UserOperation lists and protocol-native transfers
return published canonical records even while other blocks are incomplete.
`meta.browsing` distinguishes complete, partial and pending ranges, binds the
canonical snapshot hash and exposes a publication revision. Empty incomplete
pages do not prove absence. Precise balances, ownership, holder totals and
aggregate statistics retain their authoritative-state requirements.

The fresh baseline's browsing projection is maintained by deferred triggers on
canonical mappings, stage results, jobs, journals and canonical outbox rows.
It stores compressed completed ranges and per-block monotonic revisions. All
reads still join the authoritative published-stage view. Snapshot-range changes
invalidate list cursors, including replay and same-hash reattachment; newer
blocks above the snapshot do not. Publication changes emit existing `status`
events so the single browser SSE connection marks affected snapshots and
blocks stale cursors without requests or page resets. A page refresh button
updates the overview and visible tab and restarts only its visible pagers.
Only home and chain status refresh from events; verification and payment jobs
retain terminal polling. Navigation can load stale queries; focus and network
recovery do not. Search, exports and Etherscan retain their strict
snapshot and coverage contracts.

Holder first-page/count, NFT owner, block statistics and historical analytics
reads select their latest completed canonical publications on each request,
including manual refresh. Holder continuation cursors retain the selected
snapshot. Statistics report their actual completed range; unfinished suffixes
at the chain head do not block published data. Exact NFT calls stay pinned to
the selected hash. Wallet actions do not inherit unrelated browsing query
invalidation; their wallet, chain and interaction-target checks remain local
to the operation.

## Contract event log browsing

Contract address pages offer mutually exclusive historical-name and positioned
topic filters. The generated same-origin API reads canonical PostgreSQL data;
raw logs remain available before ABI enrichment. The existing manual refresh
and single SSE flow marks stale pages without automatic history replacement.

`logs.topic0` through `topic3` are stored generated bytea columns from receipt
JSON. Address and address/topic indexes include descending block, transaction
and log positions. Fixed SQL per topic selects bounded identities before raw
bodies and shared historical decoding are loaded. The existing global topic0
index remains available to compatibility queries.

`event_signatures` holds compact IDs and complete signatures.
`address_event_logs` indexes exact published signature membership;
`address_event_blocks` aggregates block contributions;
`address_event_directory` maintains address/name lookup counts. The two
block-scoped projections use the existing million-block partition lifecycle.
Directory prefix indexes cap indexed name text at 128 characters while keeping
complete names and signatures in responses. Core and ABI browsing revisions
fence snapshots, and the directory is updated transactionally on publication
and withdrawal. These are fresh-baseline structures, with no startup backfill
or old-schema adapter.

Address-log interval queries reuse the ordered address/topic/signature indexes
and partition bounds, with an inclusive lower bound and snapshot-capped upper
bound. The directory tests block contributions in the same interval; browsing
coverage and revision queries share that interval and the caller's repeatable-read
snapshot. No schema or publication changes are required for range selection.

The event tab separates draft controls from applied URL filters. The name selector
loads paged prefix results inside its popup, while logs change only on explicit
submission. Compact log summaries expand the shared detail renderer; transaction
pages keep their original presentation. Keyboard focus, cancellation, 250 ms
search debounce and manual-refresh pagination apply to the selector as well.
