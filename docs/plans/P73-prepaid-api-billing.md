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
| P73-T01 | done | P40, P30, P65 | Governance, ADR, public contracts, feature/config schema, and operation catalog | `make generate-check plan-check` |
| P73-T02 | done | P73-T01 | Prepaid accounts, append-only entries, top-up intents, usage reservations, migration, and generated SQL | PostgreSQL concurrency, migration, and race tests |
| P73-T03 | done | P73-T02 | x402 v2 EIP-3009/Permit2 top-up adapter, strict replay identity, pending settlement, and reconciliation | protocol vectors and hostile transport tests |
| P73-T04 | done | P73-T02 | `/v2/api` user-key reservation/debit gate, operator bypass, canonical action resources, and bounded capture | compatibility, quota, failure, and race matrix |
| P73-T05 | done | P73-T03 | Account balance/top-up/usage APIs and constrained browser wallet top-up flow | generated client, Vitest, Playwright, and accessibility |
| P73-T06 | done | P73-T03, P73-T04 | Disposable local Anvil/Facilitator environment and monolith/split production-image E2E | local chain, ledger, usage, replay, and outage reconciliation |
| P73-T07 | done | P73-T04, P73-T05 | Metrics, alerts, role-scoped deployment, runbook, administrator adjustment control, reconciliation CLI, and rollback | deployment, security, operations, and common gates |
| P73-T08 | blocked | P73-T07 | One-shot live EIP-3009 and Permit2 top-up plus API-debit conformance | testnet transactions and writer/chain/credit/usage reports |

## Acceptance

- [x] Historical accountless request-payment rows remain audit-only and never
      grant credit.
- [x] A settled top-up credits exactly one matching active user account once.
- [x] EIP-3009 and Permit2 top-ups are replay-fenced and payer-bound.
- [x] User-owned keys share account credit; operator keys bypass credit only.
- [x] Only configured bounded `/v2/api` logical successes commit a debit.
- [x] Native explorer reads never prompt for or accept x402 payment.
- [x] Reservation, crash, pending-settlement, and reconciliation behavior is durable.
- [x] Account UI exposes only the constrained top-up signing and exact approval flow.
- [x] Monolith and split roles share identical writer and payment semantics.
- [ ] Live EIP-3009 and Permit2 staging reports reconcile settlement, account
      credit, API debit and independent writer/chain observations (P73-T08).

## Current Blockers

P73-T08 requires operator-provided testnet funds, payer credentials, a compatible
staging facilitator, matching writer database, independent RPC endpoint, and
the deployed image/build digest. Local Anvil evidence cannot close it.

## Evidence

Retained completed milestones: P73-T01, P73-T02, P73-T03, P73-T04, P73-T05, P73-T06,
P73-T07.

Protocol/hostile-transport and PostgreSQL 18 concurrency/race tests passed
for single credit, account-wide spend, reservation/release, replay, pending
settlement and reconciliation. Generated API, frontend/browser, security,
role-scoped deployment and `make check` passed at implementation closure.
Real local Anvil/Facilitator production-image tests passed SIWE, EIP-3009 and
Permit2 top-ups, two keys, concurrent debit and final chain/account equality
in monolith and split roles. This is local payment evidence only; the live
staging reconciliation gate remains blocked.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P73-prepaid-api-billing.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
