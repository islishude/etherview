# P70 — Release

Status: `planned`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.

## References

- [Architecture](../architecture/overview.md)
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

## Acceptance

- [ ] Every P00–P60 plan and root release gate is complete with evidence.
- [ ] Clean deployment, upgrade, rollback, backup/restore, and repair procedures
      are independently reproducible.
- [ ] Security findings have no unresolved critical/high issue.
- [ ] Reference capacity target passes with documented hardware and dataset.
- [ ] Published artifacts are reproducible, checksummed, signed, and accompanied
      by an SBOM.

## Current Blockers

No dependency-plan blocker remains: P00 through P60 are complete. P70-T01
through P70-T05 are still `todo`, so P70-T06 and the v1 release remain blocked
on their conformance, security, release-CI, long-capacity, and documentation
evidence.

## Evidence

None yet.
