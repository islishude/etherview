# P58 — Evidence-based Proxy Detection

Status: `in_progress`

## Outcome

Preserve the reviewed OpenZeppelin 5.6.1 behavior while replacing the
single-branch proxy recognizer with evidence-producing, block-pinned detectors
and adding GnosisSafeProxy/SafeProxy recognition. Bulk indexing must add no Safe
RPC for a runtime that misses the maintained fingerprint manifest; deep
enrichment remains optional and bounded. EIP-7702 accounts and arbitrary
delegatecall inference are explicitly out of scope.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0028: Proxy verification and Hardhat E2E](../decisions/ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [ADR-0032: Evidence-based proxy detection](../decisions/ADR-0032-evidence-based-proxy-detection.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P58-T01 | done | P20-T13 | OZ 5.6.1 call graph, behavior audit, fixed regression inventory, and explicit change baseline | focused proxy tests; `make plan-check` |
| P58-T02 | done | P58-T01 | Shared block-pinned detection context, detector interface, resolver, structured outcomes, and memoized RPC accounting | detector/resolver unit and fuzz tests; existing OZ suite |
| P58-T03 | done | P58-T02 | Generated Safe runtime/singleton/factory manifests plus bulk and deep Safe detectors, including slot 0 and `masterCopy()` consistency | generated-manifest check; positive, negative, adversarial, and fixed-block integration fixtures |
| P58-T04 | in_progress | P58-T03 | Additive API/UI persistence, shadow-mode diffing, metrics, feature flag, runbook, rollback, and bounded backfill | OpenAPI generation; integration/race/browser/runtime gates; production sample review |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Existing OZ fixtures pass unchanged unless a reviewed finding and a regression test record the change.
- [x] Every applicable detector returns structured evidence and distinguishes confirmed, candidate, inconsistent, not-detected, and unknown outcomes.
- [x] A resolver retains simultaneous and conflicting detector outcomes; detection is never first-match-wins.
- [x] Every state read uses one exact block identity and cache identity includes chain, address, block, and detector version.
- [x] Canonical GnosisSafeProxy and SafeProxy shells resolve the current slot-0 singleton without conflating shell identity with official singleton identity.
- [x] Slot 0 or `masterCopy()` alone cannot confirm Safe; unknown compatible runtimes are at most candidates.
- [x] Bulk Safe detection adds zero RPC calls for a runtime fingerprint miss.
- [x] RPC failure is unknown, not not-detected, and inconsistent canonical shells retain their warning evidence.
- [x] Factory provenance keeps initial and current singleton identities separate and is chain-allowlisted.
- [x] Shadow mode, bounded metrics, independent disablement, rollback, and idempotent fixed-block backfill are documented and tested.
- [x] Monolith and split-role semantics match.

## Current Blockers

Production shadow sampling requires an operator-controlled deployment and the
review cohort defined in the rollout runbook. Enabling
`features.proxy_detection_v2` in that environment and completing the recorded
sample clears the remaining P58-T04 merge condition.

## Evidence

- P58-T01: [the OZ baseline review](../reviews/P58-oz-proxy-baseline.md)
  records the complete production call graph, persistent/API/UI consumers,
  fixed-block regression inventory, five actionable findings, confirmed
  block-pinning and evidence invariants, and the compatibility boundary for the
  framework adapter. Focused proxy, query, and HTTP tests passed on 2026-08-08.
- P58-T02: `proxy-detectors@1` adds typed modes, statuses, confidence,
  implementation roles, evidence, a fixed-block memoized RPC context with
  explicit cache identity and counters, an order-independent conflict-preserving
  resolver, and an OZ 5.6.1 adapter. The adapter retains the legacy `proxy@2`
  result for persistence while producing a shadow V2 outcome; no ABI or write
  authorization consumes V2. Context, resolver, fuzz, full enrichment, query,
  and HTTP tests passed on 2026-08-08.
- P58-T03: generated Safe manifests derive the exact GnosisSafeProxy 1.3.0 and
  SafeProxy 1.4.1 runtime hashes from integrity-pinned official npm artifacts
  and bind mainnet singleton/factory records to a pinned `safe-deployments`
  commit. Bulk detection performs no Safe RPC after a fingerprint miss; a hit
  reads slot 0 and singleton code. Deep detection strictly compares
  `masterCopy()`, supports medium-confidence compatible shells, retains initial
  versus current singleton provenance, and parses both historical factory event
  layouts. Adversarial unit/fuzz cases and two fixed mainnet fixtures pass;
  `go test ./...` and `make generate-check` passed on 2026-08-08.
- P58-T04 implementation: migration 0038 stores generation-fenced V2 evidence
  without changing legacy proxy authority. Independent shadow/public flags,
  per-address legacy diff reasons, bounded metrics, generated OpenAPI types,
  a Safe-aware read-only UI, rollback, and fixed-block reindex instructions are
  in place. On 2026-08-09, lint, unit, web (235 tests), race, generation, plan,
  PostgreSQL 18 integration, production-image schema, Chromium (10 tests), and
  monolith/six-role runtime E2E gates passed. The required production shadow
  cohort review remains an operator rollout action.
