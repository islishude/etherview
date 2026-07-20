# ADR-0012: Lease-Fenced Derived Publication

Status: accepted

## Context

The durable enrichment queue originally fenced `Finish` and `Retry`, while a
processor committed its derived tables, stage result, and journal in a
separate transaction. A worker whose lease expired during RPC or persistence
could therefore commit output before discovering that another worker owned the
job. A replay request could also arrive between the processor commit and
`Finish`, leaving old-generation output temporarily readable.

A raw `block_stage_results` row is not sufficient publication evidence. It may
come from a direct processor fixture, an older generation, a failed attempt, or
an orphan attempt that recorded `stale_canonical_skipped`. Readers need one
durable relation that proves the row and the terminal job describe the same
immutable block, stage version, result, and replay generation.

## Decision

- Production `proxy`, `abi`, `token`, `stats`, and `trace` processors execute
  through a lease-aware path. The stage writer, exact stage result, controlled
  journal, and successful durable-job transition commit in one PostgreSQL
  transaction. The final compare-and-set requires the exact job ID, lease
  token, unexpired lease, claimed and leased generation, no newer requested
  generation, and no prior completion of that generation.
- Long RPC work and stage writes do not lock the durable job row. Heartbeat
  renewal and the final compare-and-set/commit share a per-lease guard, so a
  renewal cannot run after a successful commit and the final transition cannot
  race an in-process renewal.
- If the final compare-and-set loses ownership, the whole stage transaction is
  rolled back. If the same token still owns an unexpired lease but a newer
  replay generation is pending, the transaction first rolls back all writer
  output to a savepoint, then consumes the old generation, clears its exact
  published state, and queues the requested generation atomically.
- `failed` and `unavailable` outcomes have no derived journal. `Finish`, retry
  exhaustion, and expired-lease exhaustion write an exact job/generation stage
  marker in the same transaction that makes the job terminal. A direct
  `Finish(complete)` for any known derived stage name and version is rejected;
  a future stage version cannot silently fall back to the old two-transaction
  path.
- `block_stage_results` and `block_journals` carry a nullable
  `(durable_job_id, job_generation)` pair. Null markers are retained only for
  direct fixtures and pre-migration audit rows. A direct processor may update
  only another null-marker row and cannot overwrite a production marker. A
  production generation may replace only a null marker or an older/equal
  marker for the same durable job.
- `published_block_stage_results` is the only enrichment readiness relation.
  It requires exact job, chain, block number/hash, stage/version, terminal
  status, generation counters, cleared lease fields, and byte-equivalent
  result state/details/error. It excludes direct rows, invalid markers, pending
  replays, and `stale_canonical_skipped` audit results. The latter remain
  unavailable after a same-hash reattach until the canonical outbox requests
  and completes a fresh generation.
- ABI dependency checks, claim gating, catalog completeness, statistics/trace
  reads, and Etherscan capability checks read the publication view. Etherscan
  runs each enrichment precheck and its data query in one read-only
  repeatable-read snapshot, and closes that snapshot before any external state
  RPC.
- An ambiguous successful commit is confirmed from durable state. Confirmation
  accepts either the exact still-published generation or proof that the
  generation was completed and subsequently superseded by a higher requested
  or completed generation; a replay racing confirmation must not turn a
  committed success into a false worker failure.

## Consequences

- An expired worker cannot expose partial or terminal-looking output, and
  generation one cannot overwrite generation two even if its process resumes.
- Replay may retain raw block-scoped derived facts where their own identity and
  canonical flags remain useful, but they are not readiness evidence without
  the matching published stage row. ABI replay continues to remove its stale
  exact output as required by its stronger binding contract.
- Direct `Process(Job)` remains useful for focused processor and reorg fixtures,
  but its null-marker result is intentionally invisible to production readers.
  Deployment code must use the PostgreSQL worker and lease-aware processor
  path.
- Adding another durable derived stage name requires implementing this common
  publication protocol, updating the production registry/parity test, and
  reviewing its controlled journal contract before it can become readable.
