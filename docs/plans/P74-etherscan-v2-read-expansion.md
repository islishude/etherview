# P74 — Etherscan V2 Authoritative Read Expansion

Status: `done`

## Outcome

The explicit `/v2/api` allowlist adds authoritative withdrawals, exact address
holdings, EOA funding origin, per-block transaction counts, and advanced
from/to filters without adding an RPC proxy, event-derived balances, unbounded
holder enumeration, or a second correctness store.

## References

- [Architecture](../architecture/overview.md)
- [Etherscan V2 compatibility matrix](../architecture/etherscan-v2-compatibility.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0008](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0035](../decisions/ADR-0035-user-owned-scoped-api-keys.md)
- [ADR-0044](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P74-T01 | done | P20, P40, P73-T04 | Governance, compatibility contract, operation catalog, and generated billing bound | plan, contract, and generation checks |
| P74-T02 | done | P74-T01 | Withdrawals, block counts, funding origin, and normal/internal/token advanced filters | handler, SQL, PostgreSQL, coverage, and reorg tests |
| P74-T03 | done | P74-T01 | Dense bounded ERC-20 and ERC-721 holding pages from exact canonical state | state, cache, limit, canonicality, and PostgreSQL tests |
| P74-T04 | done | P74-T02, P74-T03 | Reader/writer routing, prepaid billing, production runtime parity, documentation, and completion gates | billing, runtime E2E, race, security, and common gates |
| P74-T05 | done | P74-T04 | Review remediation for latest token classification and candidate-scoped legacy-safe funding proof | focused SQL, PostgreSQL, runtime, race, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] The allowlist contains exactly 34 registered and 28 billing-eligible actions.
- [x] Every new or extended action validates one unambiguous selector mode and canonical billing resource.
- [x] Empty withdrawals, funding, counts, or holdings are authoritative only after their required continuous coverage proof.
- [x] Address holdings return only exact current state and never event-derived balances.
- [x] Holding pages are dense within a fixed 1,000-candidate work bound and fail closed rather than returning partial results.
- [x] `fundedby` accepts current EOAs only and excludes self-transfers, withdrawals, fee recipients, failures, and reverted traces.
- [x] New priced reads retain user debit, operator bypass, logical-failure release, and bounded-response behavior.
- [x] Monolith and split API roles use the same components, reader routing, writer fences, and public results.

## Current Blockers

None.

## Evidence

- P74-T01: the explicit registry now contains 34 actions and the prepaid
  catalog contains 28 bounded reads. The compatibility matrix, ADR-0008,
  architecture, testing guide, OpenAPI billing bound, Helm operation enum,
  generated SQL, and root/release plans are synchronized. `make
  generate-check plan-check source-check` passes.
- P74-T02: named sqlc queries implement canonical withdrawals, exact-block
  Core/Trace/Token counts, EOA-only direct/internal funding origin, and
  mutually exclusive advanced normal/internal/token filters. Handler goldens,
  canonical resource tests, real PostgreSQL coverage/stage/gap/reorg fixtures,
  and `make test-hardhat3-provider-compat` pass.
- P74-T03: address holdings prove genesis-through-tip Core and Token coverage,
  copy at most 1,001 candidate identities, close the snapshot, and reconcile at
  most 1,000 candidates in 200-item exact-state batches. Unit limits and real
  PostgreSQL prove dense positive pages, zero caching, restart reuse, orphan
  exclusion, and fail-closed windows. Real Anvil ERC-20 and ERC-721 production
  paths pass in monolith and distributed topologies.
- P74-T04: reader routing retains writer-fenced state/canonicality, all six new
  actions are priceable with logical-failure release and operator bypass, and
  normalized production results agree across topologies. `make
  test-integration`, `make test-integration-race`, and `make
  test-runtime-e2e-prebuilt` pass; runtime monolith/distributed completed in
  83.56 seconds and x402 monolith/distributed in 35.51 seconds. The final
  `make check` and `git diff --check` pass with no security, lint, generation,
  license, Compose, or Helm failure.
- P74-T05: holding candidate SQL now selects the newest canonical token
  observation before checking ERC-20/ERC-721 standard, so a newer `unknown` or
  different-standard observation cannot resurrect an older classification.
  `fundedby` discovers its candidate before proving Trace completeness, proves
  only through that candidate (or the tip for absence), and uses the normalized
  non-reverted root Trace instead of the post-Byzantium receipt `status` field.
  Structural unit tests and real PostgreSQL fixtures cover reclassification,
  a later unrelated Trace gap, and a legacy root receipt. `make
  test-integration`, `make test-integration-race`, and the rebuilt `make
  test-runtime-e2e` pass; runtime monolith/distributed completed in 83.32
  seconds and x402 monolith/distributed in 34.51 seconds. The final `make
  check`, `make plan-check`, and `git diff --check` pass.
