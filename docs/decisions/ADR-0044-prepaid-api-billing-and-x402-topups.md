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

When top-ups are disabled, intent creation and payment return the typed
`503 topup_payment_unavailable` response; balance and history remain available
while prepaid billing is enabled.

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
Etherscan V2 compatibility matrix, including the explicitly documented native
`blob.getblob` extension. This extension requires an `api:read` key even when
billing is disabled; sessions and Turnstile tokens do not authorize it. Its
fixed-size archive read has complexity weight 5. Ordinary user-owned keys use
the existing reservation/commit/release path, while operator keys and disabled
billing retain their existing free exceptions. Successful results contain the
entire raw blob as 0x-prefixed hexadecimal, never an archive URL. The separate
session/CSRF browser content GET (without Turnstile) remains free under ADR-0055. Every query has an explicit positive integer complexity weight and cost rationale
in the operation catalog. Its price is that weight multiplied by the deployment's
positive `billing.base_price_atomic`, denominated in token atomic units.
Operation IDs use the `etherview.<module>.<action>` namespace across the price
catalog, usage ledger, API responses and metrics, including `etherview.blob.getblob`.
The fresh-database contract does not accept the former `etherscan.*` prefix,
translate historical records, or perform startup backfills. This naming does not
change `/v2/api`, module/action parameters, response envelopes or Sourcify imports.
The complete schedule is validated at startup (including uint256 overflow); missing
query prices fail closed. Per-operation `billing.operations` is rejected.
New supported actions must declare a classification and queries must define a
weight and rationale; catalog coverage tests enforce this requirement. Verification
submission/status actions, intentionally unavailable actions, and every other
mutation are ineligible.

A priced compatibility action requires an authenticated `api:read` key. A
user-owned key reserves credit from its owner's account after request and quota
validation. An operator key with no owner retains a quota-controlled free
bypass. Only an HTTP 200 Etherscan response with `status: "1"` commits the
debit; every logical or transport failure releases it. The committed debit is
durable before response bytes are released, so a crash can charge a request
whose response was not received. No automatic refund guesses delivery.

Account balances are permanent, six-decimal credits keyed by user and explorer chain. PostgreSQL atomically
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
- Price changes affect only new reservations. Payment policy changes affect only
  new intents; existing intents retain their monetary and signing snapshots.
  Removing an option disables its new settlement route without altering existing credit.
- Local/offline Anvil evidence does not replace the explicit live testnet
  top-up, writer, usage, and chain reconciliation gate.

## Local Preview integration

The Geth/Lighthouse Preview enables prepaid billing and both top-up methods on
its private chain (48815). The existing local billing token is predeployed in
genesis with its constructor storage, initial owner balance and funded
Facilitator account. The pinned offline genesis generator executes construction
and exports the complete allocation; ordinary startup sends no deployment or
funding transactions. Network fingerprint validation rejects stale bundles.
The separate local Facilitator has no public port, uses only local development
funds and is not a core readiness dependency. Restart preserves spent
authorizations and balances without reminting.
The API/all role alone receives the fingerprint pepper. Docker bridge CIDRs and
plain HTTP are explicit Preview-only transport exceptions; production defaults
remain strict. ADR-0059 replaces the fixture token's custom witness path with standard
Permit2/proxy contracts. Local results do not replace public-network acceptance.

## Complexity price policy and amount presentation

The fixed weights are a policy based on bounded query work, not measured CPU ratios.
Single-record/published aggregate reads cost 1; exact-state and bounded composite
lookups 2; paged/large-artifact reads 5; derived joins/coverage/batch reads 10;
20-address balance batches 40; bounded log scans 50; holdings enumeration with
state reconciliation 100. The operation catalog assigns every query explicitly,
including aliases. Parameters and result counts do not change a request's price.
Rate limits remain one token per request and operator-owned keys retain their
existing credit bypass. Billing-disabled deployments remain non-billing.

The public price schedule includes the base atomic price, each operation's weight
and derived atomic price. Personal billing accepts and displays whole-token decimal
amounts using the selected token precision. String/BigInt conversion is exact and rejects
excess fractional digits, zero, negative values and uint256 overflow without rounding.
Wire, ledger and wallet authorization values remain atomic integer strings. Historical top-ups and their payment records use the immutable intent precision;
legacy payment records without a snapshot remain in atomic units. Account balances,
prices, usage and adjustments always use six-decimal credit units.

## Separate explorer and payment chains

The payment network may differ from the indexed explorer chain. Browser top-ups
use the explicit wallet payment-chain scope in ADR-0020; only the generated
same-origin billing configuration selects that network. Typed-data domains,
allowances and transaction receipts bind the payment chain, while SIWE, API keys
and account ownership remain bound to the explorer chain. Existing payment-only
accounts are not transferred into a new indexed deployment. Live acceptance for
the new identity includes new operator-driven payments and real successful priced
queries; Anvil facts must be produced and indexed normally, never inserted as
synthetic database records.

## Multiple payment assets and unified credit (P60-T25)

The contract replaces asset-specific prepaid balances with one six-decimal
credit balance per user and explorer-chain identity. Operators configure up to
32 EVM payment chains and 32 tokens per chain. Each token has an explicit 0–255
precision and a positive rational conversion from token atomic units to credit
atomic units; fractional credit, zero credit and overflow are rejected. Intents
snapshot the selected payment option and its conversion, while payment records
retain actual network, token and transferred amount. Settlement and unique
credit remain one fenced writer transaction. No bridge, market-rate oracle,
legacy balance conversion or startup backfill is introduced. P60-T25 tracks
local acceptance; this decision alone is not completion evidence.
P60-T25 does not require a new live testnet gate. P60-T25 evidence used the former local Permit2 fixture;
P60-T26 migrates it to standard contracts and P60-T27 owns Rustack KMS and
dual-Anvil local acceptance.

Preview additionally runs a payment-only Anvil network (31337), leaving indexing
and SIWE on 48815. Both networks predeploy six- and eighteen-decimal local tokens.
Anvil is reachable through the existing loopback TLS gateway at
`anvil.etherview.localhost`, with the same local-wallet HTTP/WSS policy as Geth.
Its facilitator stays internal. The deterministic seed is used only for a new
volume; graceful restarts preserve state, and a seed mismatch or missing state
fails closed. Periodic checkpoints are not a guarantee against abrupt host loss:
uncertain payments still require reconciliation before any new attempt.

## Production facilitator

[ADR-0059](ADR-0059-production-x402-facilitator.md) defines the standalone
multi-chain signing and durable transaction boundary. P60-T26 replaces the local
custom Permit2 settlement fixture with the standard contract path. P60-T27 uses
separate local acceptance using Rustack KMS and two independent Anvil networks,
without Geth or real AWS/public-testnet prerequisites. This dedicated test topology
does not replace Preview. Simulation does not establish real AWS IAM enforcement,
production key isolation or public-network production readiness; P70 release gates
remain separate.

## Payment network display names

Each configured payment chain requires a nonblank `name` of 1–128 characters.
The billing configuration API exposes this name alongside its CAIP-2 `network`
and token options. Names are presentation metadata only: wallet transitions,
authorizations and immutable payment identities continue to use `network`.
The UI displays token addresses beside selection and retains payer/recipient
details in history. Loading and durable payment outcomes stay in the top-up
card until the user chooses another top-up or views history.

## Top-up progress observations

A nullable submission progress observation (`submitting`, `confirming`, or
`unknown`) and immutable observed transaction hash belong to the top-up intent,
separately from the financial state. The API polls the authenticated facilitator
progress endpoint at most once per second only while its original settlement
request runs. Each query is bounded to one second. Writer updates lock the
payment and intent, require the current reservation owner and active settling
payment, reject older observations and hash changes, and cannot replace a terminal
result. The observer is cancelled and joined before settlement publication.
A final bounded writer operation records outcomes even if the browser disconnects.

The browser starts three-second intent polling when the signed payment is sent.
Wallet signing and optional exact Permit2 approval steps are local observations;
reload recovers only durable server observations. A hash alone does not prove
broadcast. Unknown transport outcomes retain the original intent and never
automatically resubmit payment. Only the existing atomic credit publication
completes the top-up. Progress observation never credits or reconciles an account.
