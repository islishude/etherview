# P72 — Factory-derived Verification Hardening

Status: `in_progress`

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
| P72-T02 | todo | P72-T01 | Heartbeat-fenced scan/event leases plus one prepared match and short canonical publication transaction | lease-contention, slow-match/reorg, stale-publication, integration-race, and runtime parity tests |
| P72-T03 | todo | P72-T02 | Exact-epoch parent/child provenance with additive direct-verification creation provenance and unchanged wire shape | A-to-B-to-A, FQN conflict, direct-child, code-hash, API, Web, and generation checks |
| P72-T04 | todo | P72-T03 | Split derived-adjacent Go/Web presentation modules and enforce lower production/test structural ceilings | Go/Web lint, unit, browser, and source-boundary checks |
| P72-T05 | todo | P72-T04 | Complete release, topology, migration, documentation, and operator evidence without implementation changes | common, PostgreSQL/race, schema/runtime, Hardhat, Foundry, browser, deployment, and diff gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [ ] Successful scan pages do not consume the consecutive-failure budget, and
      exact full pages plus histories larger than five pages complete.
- [ ] Every published `trace@3` and `proxy@2` generation creates at most one
      durable forward event; replacement forks, same-hash replay, and delayed
      runtime observations request an exact bounded rescan.
- [ ] A worker cannot lose a healthy lease during bounded matching, renew after
      completion, publish after lease loss, or terminate the API process because
      ordinary configured work exceeded one lease period.
- [ ] Candidate matching and result construction hold no PostgreSQL snapshot or
      canonical row lock; the final short transaction rejects changed evidence.
- [ ] Parent and child provenance is bound to the resolved source compilation and
      code epoch. Existing submitted verification may expose additive creation
      provenance without changing its origin or creating another publication.
- [ ] Public routes, JSON fields, enums, configuration keys, CLI arguments, and
      monolith/split behavior remain unchanged.
- [ ] Named large Go/Web modules are split and the approved production/test
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
