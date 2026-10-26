# ADR-0007: Block-Scoped Derived Canonicality Journals

Status: accepted

## Context

ABI bindings and decodings, token events and balance deltas, block statistics,
and normalized call traces are produced asynchronously after the core block transaction. A worker can
retry the same immutable block hash, and a canonical block can later become an
orphan or be reattached. Updating only the derived rows would leave no durable
record that the stage participates in reorganization replay; writing the stage
result or journal in a separate transaction would also allow one to commit
without the other.

Journal payloads are persisted data contracts. Copying RPC responses, decoded
logs, result details, or trace input into them would make rollback behavior
depend on untrusted and potentially unbounded data. Treating the payload as
arbitrary executable SQL would create a second migration and security surface.

## Decision

- Every successful or stale production `requests@1`, `consensus@1`, `blob@1`,
  `native_transfer@1`, `proxy@2`, `abi@5`, `token@2`, `holder@1`, `stats@4`,
  `trace@4`, `state_diff@3`, and `userop@1`
  attempt upserts exactly one journal identified by chain, immutable block
  hash, full `stage@version`, and sequence `1`.
- The journal, `block_stage_results` row, and all output written by that stage
  share one PostgreSQL transaction. A failure to encode or persist the journal
  rolls back the stage result and every derived write in that attempt.
- Under [ADR-0012](ADR-0012-lease-fenced-derived-publication.md), successful
  production lease completion shares the output transaction. Its final
  compare-and-set checks the token, expiry and exact generation; lost ownership
  rolls back output. Terminal `failed` and `unavailable` attempts use the
  queue completion path to commit job state and the exact stage result together,
  intentionally without a derived journal.
- A retry that reaches its durable maximum, or an expired crashed lease already
  at that maximum, records `failed` through the same stage-result contract
  before clearing lease ownership. A stage-result foreign-key or persistence
  failure rolls the job transition back instead of exposing a terminal job
  without its result. `durable_jobs.max_attempts` is the sole attempt budget;
  the worker has no second in-memory exhaustion threshold.
- Replay reuses the existing immutable job/idempotency key. Each distinct
  durable source key advances the job's requested generation exactly once;
  repeating that source after a crash is a no-op. Requested, claimed, leased,
  and completed generations make a replay racing an active owner durable
  without changing its token. `Finish` or `Retry` releases that owner and
  schedules the pending generation atomically. If the worker instead crashes
  after persisting output, the first claim after expiry removes the old exact
  stage result, journal, and ABI output in the same transaction before the new
  lease becomes visible. Unowned queued or terminal jobs are reset immediately.
- Canonical core outbox rows retain one topic/hash identity plus a monotonic
  generation. Reattaching the same block hash reopens the published row and
  uses that outbox generation as a replay source, so an earlier terminal stale
  skip cannot suppress the new canonical lifetime. A delayed orphan row for a
  hash already canonical again is acknowledged as stale.
- The payload uses the versioned
  `etherview.derived-canonicality` schema. It contains only controlled
  `set_canonical` rollback/replay descriptions and the stage-specific relation
  allowlists in `internal/enrich/journal.go`. These cover proxy/code, Beacon
  observations, request sets, blob manifests, native transfers, ABI/effective
  identities, tokens, holder snapshots/balances, statistics, normalized traces
  and log attribution, state differences/EIP-7702, and UserOperation facts.
  Child request rows inherit manifest canonicality. The payload is descriptive,
  not executable, and never includes RPC/log/trace input or worker result details.
- Journal canonicality is calculated inside the upsert from the exact
  `canonical_blocks(chain_id, number, block_hash)` mapping. Workers do not
  supply a trusted boolean. Conflict updates refresh the same payload and
  canonical state, making retries and explicit reindex idempotent.
- Canonical detach and attach transactions change both the block journal and
  the corresponding derived rows by block hash. Detach retains the rows with
  `canonical=false`; reattaching the same hash restores them to
  `canonical=true`.
- Public and aggregate readers continue to require both the canonical mapping
  and the derived row's canonical flag. A stage result on an orphan is not
  evidence that the replacement canonical block has enrichment output.
- A `trace@4` completion for a block with transactions contains exactly one
  normalized root per canonical transaction. The adapter binds trace API
  identity fields and the normalized root call to the stored core inclusion;
  an empty trace response is a failed source response, while a transaction
  without internal calls is represented by its root frame. A transient
  transaction-not-found response is retried and is not classified as a pruned
  history capability gap.

## Boundaries

The trace journal covers the normalized call tree in `normalized_traces` and
the receipt-validated frame ownership in `trace_log_attributions`. Etherview
does not persist opcode traces or raw trace responses, and this decision does
not claim rollback or replay support for either. Token contract observations remain canonical-by-observation join and
are not represented as a `set_canonical` target by the token journal.
ABI bindings and decodings follow the provenance and range rules in
[ADR-0009](ADR-0009-block-bound-abi-provenance.md).
Proxy/code discovery and its ABI dependency follow
[ADR-0010](ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md).

## Consequences

- A PostgreSQL failure cannot expose a completed stage without its rollback
  contract, or expose rows from an attempt whose journal failed.
- A failed or unavailable durable job cannot be terminal without a matching
  stage result. An unowned replay, or an owned replay consumed by
  `Finish`/`Retry`/expired-lease claim, removes the previous result before it
  makes the next generation available.
- Reorganizations preserve auditable orphan output and allow a previously seen
  hash to be reattached without duplicate journal or durable-job identities;
  generations distinguish each canonical lifetime and replay source.
- Adding a stage, changing its output relations, or changing rollback/replay
  semantics requires a new stage version and a reviewed payload-contract
  change.
- The journal is a durable description of the canonicality transition; the
  reorganization transaction remains the only code authorized to apply it.

[ADR-0012](ADR-0012-lease-fenced-derived-publication.md) supersedes the original
two-transaction publication window. Production output and successful lease
completion are atomic even when replay or lease expiry races the worker.

The `state_diff@2` journal extension introduced authorization and
transaction execution-code relations; `trace@3` journals normalized frames and
trace-log attribution. Reorg, replay, refresh, and cleanup therefore move these
facts with their exact block hash.

The current baseline uses the `state_diff@3` StateDiff witness. Its
complete-prestate supplement changes only how the same versioned authorization and execution-code relations are derived; their
journal identity, lease fence, reorg behavior, and controlled relation set are
unchanged.

The `abi@4` journal extension adds
`transaction_effective_execution_identities` to the ABI journal. Effective
identities therefore detach, reattach, refresh, cleanup, and replay with the
same immutable block hash and ABI generation as their decodings; no reader can
observe a partially rebuilt generation.
