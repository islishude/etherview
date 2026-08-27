# P73 — Prepaid API Billing and x402 Top-ups

Status: `blocked`

## Outcome

Users fund permanent token-denominated API credit through one Account-page
x402 top-up. User-owned API keys spend that credit on configured bounded
Etherscan V2 reads, while native explorer reads stay free and operator keys
retain an audited quota-controlled bypass.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0044](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [ADR-0020](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0035](../decisions/ADR-0035-user-owned-scoped-api-keys.md)
- [Etherscan V2 compatibility](../architecture/etherscan-v2-compatibility.md)
- [x402 v2 specification](https://github.com/x402-foundation/x402/blob/270a08bfc787176816fa564d93662f2470b28225/specs/x402-specification-v2.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P73-T01 | done | P40, P60, P65 | Governance, ADR, public contracts, feature/config schema, and operation catalog | `make generate-check plan-check` |
| P73-T02 | done | P73-T01 | Prepaid accounts, append-only entries, top-up intents, usage reservations, migration, and generated SQL | PostgreSQL concurrency, migration, and race tests |
| P73-T03 | done | P73-T02 | x402 v2.23 EIP-3009/Permit2 top-up adapter, strict replay identity, pending settlement, and reconciliation | protocol vectors and hostile transport tests |
| P73-T04 | done | P73-T02 | `/v2/api` user-key reservation/debit gate, operator bypass, canonical action resources, and bounded capture | compatibility, quota, failure, and race matrix |
| P73-T05 | done | P73-T03 | Account balance/top-up/usage APIs and constrained browser wallet top-up flow | generated client, Vitest, Playwright, and accessibility |
| P73-T06 | done | P73-T03, P73-T04 | Disposable local Anvil/Facilitator environment and monolith/split production-image E2E | local chain, ledger, usage, replay, and outage reconciliation |
| P73-T07 | done | P73-T04, P73-T05 | Metrics, alerts, role-scoped deployment, runbook, administrator adjustment control, reconciliation CLI, and rollback | deployment, security, operations, and common gates |
| P73-T08 | blocked | P73-T07 | One-shot live EIP-3009 and Permit2 top-up plus API-debit conformance | testnet transactions and writer/chain/credit/usage reports |
| P73-T09 | done | P73-T06, P73-T07 | Serialize the production-runtime and x402-local Docker E2E packages so their independent load/topology gates do not contend in CI | Makefile command regression and `test-runtime-e2e-prebuilt` |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Historical P66 payments remain audit-only and never grant credit.
- [x] A settled top-up credits exactly one matching active user account once.
- [x] EIP-3009 and Permit2 top-ups are replay-fenced and payer-bound.
- [x] User-owned keys share account credit; operator keys bypass credit only.
- [x] Only configured bounded `/v2/api` logical successes commit a debit.
- [x] Native explorer reads never prompt for or accept x402 payment.
- [x] Reservation, crash, pending-settlement, and reconciliation behavior is durable.
- [x] Account UI exposes only the constrained top-up signing and exact approval flow.
- [x] Monolith and split roles share identical writer and payment semantics.

## Current Blockers

P73-T08 requires operator-provided testnet funds, payer credentials, a compatible
staging facilitator, matching writer database, independent RPC endpoint, and
the deployed image/build digest. Local Anvil evidence cannot close it.

## Evidence

- P73-T01: ADR-0044 and generated OpenAPI bind x402 v2.23 to the sole top-up
  payment endpoint; generation and governance checks pass.
- P73-T03: Go protocol tests cover EIP-3009, Permit2,
  multi-accept ordering, separate replay domains, payer mismatch, strict JSON,
  hostile Facilitator responses, and immutable pending hashes.
- P73-T02: PostgreSQL 18 migration/integration and targeted race runs cover
  concurrent single credit, shared accounts, reserve/commit/release,
  expiry, adjustments, pending reconciliation, and legacy no-credit behavior.
- P73-T04: The 22-action compatibility inventory covers GET/POST canonical resources,
  quota-before-reserve, operator bypass, logical failure release, and bounded
  response commit-before-delivery.
- P73-T05: all 37 Vitest files (365 tests), TypeScript/Biome, the production
  web build, and 24 embedded Chromium E2E tests pass. The Account flow validates every typed-data field, wallet
  account/chain/revision, exact Permit2 approval, pending recovery, bilingual
  content, and the browser transport boundary.
- P73-T06: the disposable PostgreSQL 18/Anvil/test-Facilitator environment
  starts with the production image. Runtime E2E passes both monolith and
  six-role distributed topologies with real SIWE, EIP-3009 and Permit2 top-ups,
  replay rejection, two user keys, concurrent debit, logical-failure release,
  operator bypass, and final chain/account equality.
- P73-T07: `make check` passes, including generated/source/plan, full Go,
  race, web, security, license, Docker/Compose, and Helm lint/render gates; the
  full PostgreSQL integration suite also passes. Role-scoped secrets,
  dual-stack Facilitator CIDRs, prepaid metrics/alerts, rollback ordering, and
  operator reconciliation are documented. P73-T08 remains the sole blocker.
- P73-T09: CI run 33122804081 showed the general runtime and x402-local Go
  packages overlapping, which made the existing monolith bounded-load gate
  miss its unchanged thresholds. The prebuilt target now runs package tests
  with `-p=1`; the complete target passes runtime monolith/distributed in
  83.282 seconds, followed by x402 monolith/distributed in 37.837 seconds.
