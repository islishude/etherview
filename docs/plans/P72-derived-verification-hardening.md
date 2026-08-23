# P72 — Factory-derived Verification Hardening

Status: `done`

## Outcome

Make factory-derived verification pagination, forward propagation, leases,
publication, and public provenance exact across large histories, delayed runtime
observations, reorganizations, and repeated stage generations. Preserve the
existing public HTTP shape, configuration keys, default-off production rollout,
and submitted-verification behavior while reducing directly related structural
complexity.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0043: Factory-derived verification provenance](../decisions/ADR-0043-factory-derived-verification-provenance.md)
- [Development](../development.md)
- [Testing](../testing.md)
- [Operations](../operations.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P72-T01 | done | P71 | Generation-bound trace/proxy forward events, fork-aware scan rewind, canonical attempt writes, and success-reset pagination budgets | state-machine unit and PostgreSQL reorg/pagination regressions; generation/source/plan checks |
| P72-T02 | done | P72-T01 | Heartbeat-fenced scan/event leases plus one prepared match and short canonical publication transaction | lease-contention, slow-match/reorg, stale-publication, integration-race, and runtime parity tests |
| P72-T03 | done | P72-T02 | Exact-epoch parent/child provenance with additive direct-verification creation provenance and unchanged wire shape | A-to-B-to-A, FQN conflict, direct-child, code-hash, API, Web, and generation checks |
| P72-T04 | done | P72-T03 | Split derived-adjacent Go/Web presentation modules and enforce lower production/test structural ceilings | Go/Web lint, unit, browser, and source-boundary checks |
| P72-T05 | done | P72-T04 | Complete release, topology, migration, documentation, and operator evidence without implementation changes | common, PostgreSQL/race, schema/runtime, Hardhat, Foundry, browser, deployment, and diff gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Successful scan pages do not consume the consecutive-failure budget, and
      exact full pages plus histories larger than five pages complete.
- [x] Every published `trace@3` and `proxy@2` generation creates at most one
      durable forward event; replacement forks, same-hash replay, and delayed
      runtime observations request an exact bounded rescan.
- [x] A worker cannot lose a healthy lease during bounded matching, renew after
      completion, publish after lease loss, or terminate the API process because
      ordinary configured work exceeded one lease period.
- [x] Candidate matching and result construction hold no PostgreSQL snapshot or
      canonical row lock; the final short transaction rejects changed evidence.
- [x] Parent and child provenance is bound to the resolved source compilation and
      code epoch. Existing submitted verification may expose additive creation
      provenance without changing its origin or creating another publication.
- [x] Public routes, JSON fields, enums, configuration keys, CLI arguments, and
      monolith/split behavior remain unchanged.
- [x] Named large Go/Web modules are split and the approved production/test
      structural limits pass without new exclusions.

## Current Blockers

None.

## Evidence

- P72-T01 was claimed on 2026-08-23 after a read-only review reproduced scan
  pagination exhaustion, fork-insensitive cursors, missing lease renewal,
  canonical-lock amplification, and epoch/provenance projection defects in the
  completed P71 implementation.
- P72-T01 adds migration `0057`, binds trace/proxy forward events to immutable
  `durable_stage_publications` generations, merges a fork-aware rescan floor,
  wakes pending-runtime attempts after exact proxy publication, canonicalizes
  non-match writes, and resets the failure budget after every successful page.
  Focused derived/app/observability/store/verifier tests, `make source-check`,
  `make plan-check`, `git diff --check`, and the complete owned PostgreSQL 18
  `make test-integration` gate pass on 2026-08-23. The integration regression
  covers five exact 100-trace pages plus the terminal empty page, same-hash new
  trace generation, recoverable failed-scan rewind, duplicate event idempotency,
  proxy-generation pending wake, and detach-before-attempt persistence.
- P72-T02 adds shared heartbeat guards for scan and forward-event leases, exact
  renew/final CAS fencing, one opaque prepared match per trace, and a short
  publication transaction that compares creation/runtime/Standard-JSON digests
  before publishing. Matcher work and result construction now hold no database
  transaction or canonical row lock. Derived/verifier unit and race tests pass;
  the owned PostgreSQL 18 `make test-integration-race` gate passes in 180.020s,
  including a 300ms lease with observed renewals across five 100-trace pages and
  stale prepared-evidence rejection. `make test-runtime-e2e` passes monolith and
  distributed topologies in 82.36s after one unchanged retry for a Docker Hub
  authentication TLS timeout.
- P72-T03 binds parent FQN to the authenticated compilation source or exact
  transitive parent attempt and scopes created contracts by source compilation
  plus the current same-code epoch boundary. Directly submitted children retain
  `verification_origin=submitted` while exposing additive exact-address
  `derived_from`; code-hash reuse exposes no target creation claim. OpenAPI
  descriptions and bilingual Web copy document the unchanged wire shape. The
  focused owned PostgreSQL 18 integration package passes in 155.791s with
  late-verification, A-to-B-to-A, direct-child/no-second-job, transitive, and
  code-epoch assertions. All 360 Vitest cases, Web/Go lint, generation,
  source/plan checks, and `git diff --check` pass on 2026-08-23.
- P72-T04 splits transaction internal-transfer orchestration, proxy detection
  facts/history mapping, ABI types/budgets, API providers, ABI/proxy/state-diff/
  trace helpers, the E2E server, and oversized Hardhat/integration/Web tests.
  Source checking now caps hand-written Go production/test files at 1,500/2,500
  lines; Go cognitive complexity is 100; Biome enforces production
  1,400-file/400-function/75-cognitive and test 2,500-file/1,000-function
  ceilings without new exclusions. Go lint reports zero issues, targeted Go and
  build-tag compilation pass, and all 360 Vitest cases plus Web lint pass.
  The first canonical real-Chrome request was rejected when the Codex host
  reached its external-execution usage limit; after the user resumed the task,
  the unchanged `make test-e2e` gate passed all 24 Chromium cases in 44.3s.
- P72-T04 sandbox-safe acceptance additionally passes aggregate `make test` and
  `make test-race`, all 35 Web files/360 Vitest cases, TypeScript, Biome, Go vet
  and lint, source/generation/plan checks, build-tag compilation, and `git diff
  --check`. The host initially rejected the fourth Git commit with the same
  usage limit; the task was resumed without altering or losing the worktree.
- P72-T05 records the coordinated migration `0057` rollout, redacted aggregate
  pre-backfill capture, explicit Preview recovery reason, and closed-label
  generation/rewind/lease observations in the runbook. The unchanged final
  gates pass on 2026-08-23: `make check`; PostgreSQL 18 `make
  test-integration` (core integration package 172.289s) and `make
  test-integration-race` (184.245s); `make test-schema-e2e`; monolith/split
  `make test-runtime-e2e` (82.49s); `make test-hardhat3-e2e` (221.40s); and
  `make test-foundry-e2e` (96.60s). The real-Chromium `make test-e2e` gate
  passes 24/24 in 44.3s. Final generation/source/plan and whitespace checks
  also pass. Implementation remains split into independent commits
  `d8f1568` (T01), `07c1793` (T02), `33abd82` (T03), and `3549302` (T04);
  T05 contains documentation, plan state, and acceptance evidence only.
