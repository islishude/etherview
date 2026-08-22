# P71 — Factory-derived Contract Verification

Status: `done`

## Outcome

Persist every bounded candidate from an authenticated successful Solidity
compilation, then use canonical CREATE and CREATE2 traces plus exact historical
runtime-code observations to auto-verify uniquely matched child contracts.
Derived publication reuses the ordinary verified-contract transaction, remains
code-epoch and reorg fenced, propagates asynchronously, and exposes bounded
human-readable provenance without extending Sourcify consent.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0024: Verifier v2 workflow](../decisions/ADR-0024-verifier-v2-workflow.md)
- [ADR-0043: Factory-derived verification provenance](../decisions/ADR-0043-factory-derived-verification-provenance.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P71-T01 | done | P30-T17 | Reusable candidate matcher and one submitted/derived publication transaction with unchanged ordinary verification behavior | verifier unit and PostgreSQL publication regressions |
| P71-T02 | done | P71-T01 | Fresh-schema authenticated compilation-unit and bounded candidate persistence on successful address verification | codec, digest/provenance, idempotency, migration, and PostgreSQL round-trip tests |
| P71-T03 | done | P71-T02 | Historical canonical CREATE/CREATE2 scanner, creation/runtime unique matcher, durable attempts, and internal derived publication | Factory-to-child Solidity fixtures and PostgreSQL integration tests |
| P71-T04 | done | P71-T03 | Historical parent code-epoch resolution, publication-time canonical recheck, stale handling, and retry idempotency | reorg, reattach, code replacement, and duplicate-work tests |
| P71-T05 | done | P71-T04 | Trace-stage-completion forward enqueue and transitive asynchronous propagation without ingestion-path matching | future-child, nested-factory, retry, and monolith/split tests |
| P71-T06 | done | P71-T05 | Generated provenance/children API, bilingual Web presentation, bounded configuration, metrics, admin backfill, and operations guidance | generated API, Web, observability, browser, deployment, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Verifying only a Factory persists its complete bounded authenticated
      compilation candidate set and can auto-verify a previously created child
      without another public verification submission.
- [x] Derived verification requires one unique fully qualified candidate to
      match both trace init code and exact canonical deployed runtime code.
- [x] Constructor arguments, libraries, immutables, and compiler auxdata retain
      the same transformation-aware semantics as direct verification.
- [x] Historical and future CREATE/CREATE2 events propagate asynchronously;
      nested children participate one durable event at a time.
- [x] Parent and child evidence is bound to chain, block number/hash, runtime
      code identity, transaction hash, trace path, and compilation provenance.
- [x] Reorgs, address code replacement, retries, and repeated enqueue never
      expose stale or duplicate current verification.
- [x] Derived verification neither invokes debug/archive RPC on its normal path
      nor submits child sources to Sourcify.
- [x] API and Web explain the creator, creation transaction, trace path, call
      type, and parent compilation while keeping hostile output bounded.
- [x] Ordinary address verification, selector publication, proxy replay,
      Sourcify, monolith/split parity, and ingestion latency do not regress.

## Current Blockers

None.

## Evidence

- P71-T01 exposes one-candidate creation/runtime matching through the existing
  transformation engine and extracts one transaction-local publication helper
  for verified contracts, selector indexes, authenticated proxy artifacts, and
  proxy replay. `go test ./internal/verify -count=1`, the complete owned
  PostgreSQL 18 `make test-integration` gate, `make plan-check`, and
  `git diff --check` pass on 2026-08-22.
- P71-T02 is claimed on 2026-08-22.
- P71-T02 adds migration `0051`, immutable authenticated compilation units and
  complete candidate rows, exact normalized Standard JSON payload retention,
  strict candidate rehydration, and worker-to-completion propagation only for
  successful Solidity address verification. `go test ./internal/verify
  ./internal/store -count=1`, `make generate-check`, `make source-check`, the
  complete owned PostgreSQL 18 `make test-integration` gate, and
  `git diff --check` pass on 2026-08-22.
- P71-T03 was claimed on 2026-08-22.
- P71-T03 adds migration `0052`, durable historical scan leases and bounded
  cursor pagination, explicit pending/no-match/runtime-mismatch/ambiguous/stale
  attempts, a database-only unique creation/runtime matcher, and internal
  `derived` jobs that recheck canonical trace/runtime evidence before using the
  shared verified-contract/selector/proxy-replay transaction. Publication is
  feature-gated and defaults off. Focused verify/derived/config/app/store tests,
  `make generate-check`, `make source-check`, `git diff --check`, and the
  complete owned PostgreSQL 18 `make test-integration` gate pass on 2026-08-23,
  including an exact Factory-to-CREATE2-child backfill.
- P71-T04 is claimed on 2026-08-23.
- P71-T04 adds canonical `ResolveAtBlock`, creator-code resolution at every
  trace and publication block, migration `0053` attempt stale-state retention
  across trace detach/reattach, and address-scoped publication serialization.
  Repeated derived completion and pre-existing exact child verification add
  provenance without overwriting or creating a second publication. Focused
  resolver/verifier/derived/store tests, `make source-check`, `git diff
  --check`, and the complete owned PostgreSQL 18 `make test-integration` gate
  pass on 2026-08-23, including code replacement, duplicate completion, and
  detach/reattach current-artifact assertions.
- P71-T05 is claimed on 2026-08-23.
- P71-T05 adds migration `0054`, one durable post-`trace@3` block dispatcher,
  generation-safe scan redispatch, and scan target identity independent of the
  shared compilation unit. A uniquely derived child enqueues its own creator
  code epoch, while publication rechecks either the original authenticated
  source job or a canonical matched parent attempt in the same compilation.
  The matcher never runs in the trace publication transaction. Focused
  derived/verifier/config/app/store tests, `make source-check`, `git diff
  --check`, a dedicated fresh-PostgreSQL Factory-to-Child-to-Grandchild run,
  and the complete owned PostgreSQL 18 `make test-integration` gate pass on
  2026-08-23; the temporary diagnostic database and volume were removed.
- P71-T06 is claimed on 2026-08-23.
- P71-T06 adds migration `0055`, required generated `verification_origin`,
  `derived_from`, and bounded `derived_children` API fields, bilingual
  Factory-derived Web provenance and Created contracts presentation, staged
  environment/Compose/Helm configuration, closed-label operational metrics,
  and an audited reason-required admin backfill request plus runbook. All 359
  Vitest cases, real Chrome `make test-e2e` (24/24, including bilingual 390px
  overflow and accessibility), `make deployment-check`, the complete owned
  PostgreSQL 18 `make test-integration`, and aggregate `make check` pass on
  2026-08-23. `make check` includes generation/source checks, Go vet and strict
  lint, ordinary/race tests, vulnerability/secret/npm audits, license policy,
  Dockerfile/Compose validation, and Helm lint/template/render.
