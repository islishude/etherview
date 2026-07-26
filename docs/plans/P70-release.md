# P70 — Release

Status: `in_progress`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P70-T01 | todo | P10–P60 | Execution/API/token/proxy/verification conformance matrix | conformance suite |
| P70-T02 | todo | P10–P60 | Threat model, security audit, dependency and compiler supply-chain review | security gates |
| P70-T03 | todo | P10–P60 | Monolith/split E2E, migration/rollback, outage, reorg, and soak suite | release CI |
| P70-T04 | todo | P60 | 500 RPS reference capacity report and tuning guide | load report |
| P70-T05 | todo | P00–P60 | User/operator/API/runbook/upgrade documentation | doc review and link check |
| P70-T06 | todo | P70-T01–P70-T05 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release | release verification |
| P70-T07 | done | P60 | Database read/write pool split configuration, deployment wiring, and capacity guidance | helm config/schema tests |

## Acceptance

- [ ] Every P00–P60 plan and root release gate is complete with evidence.
- [ ] Clean deployment, upgrade, rollback, backup/restore, and repair procedures
      are independently reproducible.
- [ ] Security findings have no unresolved critical/high issue.
- [ ] Reference capacity target passes with documented hardware and dataset.
- [ ] Published artifacts are reproducible, checksummed, signed, and accompanied
      by an SBOM.
- [x] P70-T07: only API-bearing processes open the optional read-only pool;
      startup validates its schema and chain identity, readiness covers both
      pools without automatic fallback, and every correctness-sensitive read or
      write remains writer-routed.
- [x] P70-T07: configuration, Compose, Helm Secret/ExternalSecret wiring,
      effective connection bounds, and API-only capacity accounting have
      regression coverage and pass the applicable repository gates.

## Current Blockers

No dependency-plan blocker remains: P00 through P60 are complete. P70-T01
through P70-T05 are still `todo`, so P70-T06 and the v1 release remain blocked
on their conformance, security, release-CI, long-capacity, and documentation
evidence.

## Evidence

- P70-T07 configuration: YAML, environment, and `_FILE` inputs support an
  optional reader URL plus independently bounded pool sizes. Zero reader
  values inherit writer settings; negative, overflowed, malformed, and
  effective `min > max` inputs fail before runtime.
- P70-T07 runtime: only `api`/`all` opens the forced-read-only reader pool.
  Startup checks its migration ledger and exact chain/genesis identity.
  Ordinary projections and the explicit Etherscan read inventory use it;
  canonical/RPC fences, runtime and metric state, authentication,
  verification, external observations, media, mempool, and all writes remain
  writer-backed. Both operational and public readiness fail closed, with the
  latter bypassing Redis status cache.
- P70-T07 deployment: Compose preserves mounted YAML sizing unless an operator
  supplies an environment override. Helm injects the optional reader Secret
  only into API-bearing containers, supports an optional ExternalSecret key,
  rejects inline credentials and invalid effective bounds, and documents
  restart-on-secret-rotation. The reference maximum is 216 writer plus 96
  reader connections (312 steady state, 624 for full old/new overlap).
- P70-T07 verification: `go test ./internal/config ./internal/app
  ./internal/httpapi -count=1`, the corresponding `go test -race`, and
  `go test ./... -count=1` pass. `make toolchain-check`, `make lint`,
  `make generate-check`, `make helm-check`, and `make compose-check` pass.
  `make plan-check` and `git diff --check` pass after the evidence update.
- P70-T07 integration boundary: `make test-integration` passes against a
  disposable PostgreSQL 18 database after applying and checking every
  migration. The integration-tag regression verifies real writer/reader
  session settings, SQLSTATE `25006`, reader schema compatibility, and
  chain/genesis matching. Both pools used the same PostgreSQL endpoint, so no
  asynchronous replica-lag or reader-outage result is claimed by this scoped
  item.
