# P70 — Release

Status: `blocked`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.
Feature implementation belongs to its owning plan; this plan tracks final
release acceptance and publication only.

## References

- [Root release gates](../../PLAN.md#global-release-gates)
- [Plan catalog](index.md)
- [Architecture](../architecture/overview.md)
- [Accepted ADRs](../decisions/index.md)
- [User Accounts, Billing & Personal Tools](P60-user-auth-billing.md)
- [Operations](../operations.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P70-T01 | blocked | P10, P20, P30, P40, P50, P60 | Execution/API/token/holder/proxy/verification/authentication/billing conformance matrix | conformance suite |
| P70-T02 | blocked | P10, P20, P30, P40, P50, P60 | Threat model, security audit, dependency, compiler, session, holder reconciliation, and payment supply-chain review | security gates |
| P70-T03 | blocked | P10, P20, P30, P40, P50, P60 | Monolith/split E2E, migration/rollback, outage, reorg, holder, payment, and soak suite | release CI |
| P70-T04 | blocked | P10, P20, P30, P40, P50 | 500 RPS reference capacity report including holder reads and concurrent reconciliation, plus tuning guide | load report |
| P70-T05 | blocked | P00, P10, P20, P30, P40, P50, P60 | User/operator/API/authentication/billing/holder/runbook/upgrade documentation | doc review and link check |
| P70-T06 | todo | P70-T01, P70-T02, P70-T03, P70-T04, P70-T05 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release | release verification |

## Acceptance

- [ ] Every required feature plan and root release gate is complete with evidence.
- [ ] Final-revision conformance and monolith/split acceptance cover the same
      public contracts, persistence, outages, reorgs, and payment semantics.
- [ ] Clean deployment, upgrade, rollback, backup/restore and repair are reproducible.
- [ ] Security review has no unresolved critical/high finding.
- [ ] The named reference environment sustains 500 read requests/second for
      30 minutes with common-query p95 below 500 ms, error rate below 0.1%, and
      core lag no more than two blocks under a healthy upstream.
- [ ] User/operator/API documentation and upgrade guidance match the release.
- [ ] Reproducible signed multi-architecture artifacts include checksums and an
      SBOM, and the release is published only after all acceptance gates pass.

## Current Blockers

P70-T01, P70-T02, P70-T03, and P70-T05 are blocked by P60-T17's live EIP-3009
and Permit2 payment acceptance. They become claimable after the preserved
staging reports reconcile settlement, account credit, API debit, and independent
writer/chain observations, and P60 is `done`. P60 owns the required operator
inputs and clearing conditions; local Anvil evidence cannot replace this gate.

P70-T04 is blocked on an operator-provisioned reference environment for the
final clean revision and image digest: at least two failure domains with room
for the documented 9-to-18-pod topology, HA PostgreSQL sized for its connection
budget, a named representative chain snapshot with cardinalities, healthy
purpose-specific RPC behavior, and independent timestamp-aligned resource
monitoring. Running the exact 500 RPS/30-minute target there must include
representative high-cardinality native holder pages, `tokenholderlist`,
`tokenholdercount`, and concurrent incremental holder reconciliation while
preserving the existing latency, error-rate, and lag thresholds. Preserving
the load report, resource peaks, monitoring data, and tuning guide clears the
blocker.

P70-T06 remains dependency-gated until P70-T01 through P70-T05 are complete.
No release is authorized by documentation cleanup.

## Evidence

Final release acceptance remains incomplete. Feature-level historical checks
remain in their owning plans and Git history; they do not close the release
conformance, security, topology, documentation, or reference-capacity gates.
P60-T17 still needs live staging reports, and P70-T04 still needs the final
accepted revision and image digest tested in the named reference environment.
Record final release reports and artifact identities here as the gates complete.
