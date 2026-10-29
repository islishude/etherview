# P70 — Release

Status: `blocked`

Browser subscription API and all/split feature delivery acceptance were completed
under P60-T20; notifications have no persisted history or replay. Final release
acceptance remains governed by the gates below.

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.
Feature implementation belongs to its owning plan; this plan tracks final
release acceptance and publication only.

## References

- [ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)

- [Root release gates](../../PLAN.md#global-release-gates)
- [Plan catalog](index.md)
- [Architecture](../architecture/overview.md)
- [Accepted ADRs](../decisions/index.md)
- [User Accounts, Billing & Personal Tools](P60-user-auth-billing.md)
- [Operations](../operations.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status  | Depends on                                  | Deliverable                                                                                                                       | Verification              |
| ------- | ------- | ------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- | ------------------------- |
| P70-T01 | blocked | P10, P20, P30, P40, P50, P60                | Execution/API/token/holder/proxy/verification/authentication/billing and database-only current-chain ENS v1/v2 conformance matrix | conformance suite         |
| P70-T02 | blocked | P10, P20, P30, P40, P50, P60                | Threat model, security audit, dependency, compiler, session, holder/payment and ENS source/CCIP/publication review                | security gates            |
| P70-T03 | blocked | P10, P20, P30, P40, P50, P60                | Monolith/split E2E, migration/rollback, outage, reorg, holder/payment and background ENS recovery/soak suite                      | release CI                |
| P70-T04 | blocked | P10, P20, P30, P40, P50                     | 500 RPS reference capacity report including holder/ENS reads and concurrent background reconciliation, plus tuning guide          | load report               |
| P70-T05 | blocked | P00, P10, P20, P30, P40, P50, P60           | User/operator/API/authentication/billing/holder/ENS synchronization and recovery/runbook/upgrade documentation                    | doc review and link check |
| P70-T06 | todo    | P70-T01, P70-T02, P70-T03, P70-T04, P70-T05 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release                                                                   | release verification      |

## Acceptance

- [x] P20-T27 through P20-T29, P30-T149, P40-T27 and P50-T81 are delivered;
      P30-T150 proves local all/split behavior and P30-T151 supplies independent
      Mainnet v1/v2 activation, coverage and reconciliation evidence. ENS
      request-time fallback, missing-as-absent results and stale identity display
      cannot be accepted as substitutes.

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

The current-chain ENS feature milestones and P30-T150/P30-T151 acceptance are
complete with [new evidence](../reviews/ens-current-chain-acceptance.md). This
clears only that feature dependency. The original P60 blocker and final release
conformance, security, recovery/soak and documentation gates remain open.
Historical or bounded ENS samples do not prove full directory coverage.
The name-directory/profile extension is tracked separately by P20-T30, P40-T28
and P50-T82; its [local evidence](../reviews/ens-name-details-acceptance.md) does
not extend the original Mainnet report. Final conformance/capacity must include
registration-profile jobs, name details, alias search and independent expiry.

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
blocker. Use the documented topology and connection budget with ENS workers
inside enrich. Include published-name reads, bounded indexing,
invalidation and refresh in the final capacity run.

P70-T06 remains dependency-gated until P70-T01 through P70-T05 are complete.
No release is authorized by documentation cleanup.

## Evidence

Final release acceptance remains incomplete. Feature-level historical checks
remain in their owning plans and Git history; they do not close the release
conformance, security, topology, documentation, or reference-capacity gates.
P60-T17 still needs live staging reports, and P70-T04 still needs the final
accepted revision and image digest tested in the named reference environment.
P30-T150 and P30-T151 have new local and representative live evidence for the rewrite.
Full directory coverage remains separately reported deployment progress.
Local ENS acceptance alone closes no release gate. Record final release reports and
artifact identities here as the gates complete.
