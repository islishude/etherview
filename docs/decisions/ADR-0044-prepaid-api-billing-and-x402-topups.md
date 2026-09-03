# ADR-0044: Prepaid API Billing with x402 Top-ups

Status: accepted

## Context

The former accountless design attached one x402 authorization and settlement
to each selected native API read. The product now requires a prepaid account:
an authenticated
user funds one durable balance, then user-owned API keys spend that balance on
priced Etherscan V2 compatibility actions. Explorer reads must not prompt for or
silently create payments.

Top-up settlement remains an external one-shot boundary. API usage, however,
is entirely PostgreSQL-local and can reserve, commit, release, and expire
credit without a facilitator call. The two flows need separate state machines
and failure semantics.

## Decision

`features.api_billing` enables prepaid accounts and priced `/v2/api` actions.
It requires SIWE user authentication and user-owned API keys. A separately
controlled `features.x402_topups` enables account funding and is the only
feature that constructs a facilitator client or reads x402 Secrets.

All native explorer reads under `/api/v1` are free and reject payment headers.
The native billing endpoints are a free control plane for configuration,
balances, top-up intents, top-up/payment history, usage history, and
administrator inspection.

The superseded request-payment configuration and execution surface is removed,
not merely disabled. `features.x402_billing`, `billing.routes`, its coarse
limiter settings and environment aliases are rejected, and no native quota or
HTTP payment dispatcher is assembled. The writer payment ledger exposes new
reservation only for `account_topup`; its decoder, history queries, append-only
events, expiration, and operator reconciliation continue to accept historical
`legacy_request` rows without turning them into prepaid credit.
The retired accountless `x402testnet` command and support package are also removed;
live acceptance belongs to the user-bound top-up and prepaid-usage workflow.

The billable operation catalog is the closed bounded-read subset of the
Etherscan V2 compatibility matrix. A configured price is denominated in the
configured token's atomic units. Missing prices mean free actions. Verification
submission/status actions, intentionally unavailable actions, and every other
mutation are ineligible.

A priced compatibility action requires an authenticated `api:read` key. A
user-owned key reserves credit from its owner's account after request and quota
validation. An operator key with no owner retains a quota-controlled free
bypass. Only an HTTP 200 Etherscan response with `status: "1"` commits the
debit; every logical or transport failure releases it. The committed debit is
durable before response bytes are released, so a crash can charge a request
whose response was not received. No automatic refund guesses delivery.

Account balances are permanent and asset-specific. PostgreSQL atomically
maintains cumulative credits, debits, and active reservations alongside an
append-only entry ledger. Credit is never inferred from historical accountless
request payments. Administrative corrections are explicit, reason-bound credit or
debit entries; users cannot withdraw or self-refund.

An authenticated user creates a bounded, expiring top-up intent. The payer must
equal that user's SIWE address. The intent's dedicated POST payment endpoint is
the only route that accepts `PAYMENT-SIGNATURE`. It advertises x402 v2.23 exact
EVM authorization requirements for configured `eip3009` and `permit2` methods.
Permit2 uses a direct approval for exactly the current top-up amount; gas
sponsoring, ERC-7710, and non-authorization flows are rejected.

The top-up payment, settled intent, account credit, and append-only credit entry
commit in one writer transaction. `settlement_pending` is accepted only with a
strict non-zero transaction hash and remains operator-reconcilable. Unknown
settlement never causes a second automatic settle attempt. The Account page may
poll the intent after an uncertain response but never resubmit automatically.

## Consequences

- The former accountless request-payment design is superseded. Its rows remain
  immutable audit history and do not create prepaid credit.
- Facilitator failure prevents new top-ups but does not affect spending an
  existing balance.
- User-owned keys for one user share the same account and concurrency fence;
  operator keys never consume user balances.
- Price changes affect only new reservations. Asset rotation requires clearing
  prices and draining active top-up/usage work; balances are never converted.
- Local/offline Anvil evidence does not replace the explicit live testnet
  top-up, writer, usage, and chain reconciliation gate.
