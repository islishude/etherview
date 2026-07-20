# ADR-0002: Identity-Bound Core Repair and Explicit Derived Reindex

Status: accepted

## Context

An RPC provider can return an incomplete or malformed core bundle even when
the explorer already knows the block hash. Normal indexing treats an existing
canonical hash as idempotent, so an operator needs a way to refresh those
facts. The same command must not become an alternate fork-choice path, bypass
finality, or silently replay an unbounded amount of derived state.

Durable enrichment jobs use immutable block-hash idempotency keys. Resetting a
queued job or an active lease would permit two workers to process the same
stage concurrently and would break the PostgreSQL ownership model.

## Decision

Core repair and derived reindex are separate operations:

- `repair --stage core` refetches through the normal sticky history endpoint.
  In a serializable, chain-advisory-locked transaction, PostgreSQL rechecks the
  exact chain, height, canonical hash, parent, and finalized boundary. It may
  replace only the bundle facts for that identity. It never changes the
  canonical mapping, checkpoint, or reorg history.
- The canonicalizer refresh entry point validates that the candidate already
  has the exact canonical height/hash and calls only the identity-bound store
  refresh. It must never call the normal `Apply` fork-choice path, including as
  a preflight, because that path is authorized to extend or reorganize history.
- An explicit `--allow-finalized` plus audit reason can authorize a same-hash
  refresh at or below finalized height. It cannot authorize a different hash,
  parent, or a reorg.
- A successful refresh removes replayable block-local output and rollback
  journals that directly depend on the replaced core facts. It does not guess
  how far downstream state must be rebuilt.
- `reindex --stage token|stats|trace` addresses the currently canonical hash at
  every selected height. It creates missing durable jobs and resets only
  terminal jobs. Queued jobs and active leases return a busy result and retain
  their current ownership.
- Repair/reindex requests and reasons remain in PostgreSQL as the operator
  audit trail.

## Consequences

- Repair cannot be used to rewrite canonical history or advance progress.
- A missing height or alternate hash is rejected before any normal
  canonicalization side effect, so repair cannot create a tip or switch a fork.
- A repaired block exposes incomplete derived stages until the operator runs
  the appropriate reindex range; APIs must report that state explicitly.
- Operators choose the downstream replay range because token balances and
  other aggregates can depend on later blocks.
- The normal synchronizer, monolith, and split maintenance role share one
  canonicalization and durable-job implementation.
