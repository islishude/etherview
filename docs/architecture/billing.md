# Accounts, billing and settlement

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns account identity, credit and settlement authority.
See [operator procedures](../operations/billing.md) and
[acceptance requirements](../testing/billing.md) for execution and validation.

- [API keys and account maintenance](#api-keys-and-account-maintenance)
- [Prepaid credit and top-up identity](#prepaid-credit-and-top-up-identity)
- [Multi-payment funding policy](#multi-payment-funding-policy)
- [Standalone multi-chain facilitator](#standalone-multi-chain-facilitator)
- [Top-up progress](#top-up-progress)

## API keys and account maintenance

- Operators create, rotate, list, and revoke deployment-wide API keys through
  the single CLI. An explicitly enabled Cookie-authenticated account boundary
  additionally manages only its own scoped keys. Its list and creation tabs
  are separate; users must revoke a key before soft-deleting it from their list.
  Soft deletion retains the revoked credential row and all billing references. Plaintext appears only in
  create/rotate output. Rotation locks the active PostgreSQL row and commits
  the replacement digest plus old-key revocation in one transaction, preserving
  owner, name, scopes, and quota policy. See ADR-0035.

- The same maintenance-role graph conditionally owns writer-only user-auth and
  prepaid-billing cleanup. It releases only expired usage reservations, reopens
  or expires the matching top-up intent when a pre-settlement payment lease
  expires, and expires untouched intents. Each account update and durable state
  transition is one PostgreSQL statement with `SKIP LOCKED` candidates; it
  never refunds committed usage or guesses a settling outcome. Split
  maintenance processes load no session pepper, billing fingerprint pepper, or
  facilitator header Secret. Failures emit only `user_auth_cleanup_failed`,
  `x402_billing_expiry_failed`, or `prepaid_billing_expiry_failed` before a
  bounded retry.

## Prepaid credit and top-up identity

- ADR-0044 replaces per-request x402. Native `/api/v1` reads are free and reject
  `PAYMENT-SIGNATURE`; only the Account top-up payment endpoint accepts it.
  With billing enabled, every supported bounded `/v2/api` query reserves a user
  account's permanent six-decimal credit after API-key quota and full compatibility
  validation. Prices derive from explicit catalog complexity weights times
  `billing.base_price_atomic`; missing query prices fail closed. Personal billing
  converts whole-token decimal input exactly to atomic units and displays the
  complete price schedule. Request-rate quotas remain one token per request.
  Operator keys have no owner and retain a quota-controlled credit bypass.
  A debit commits only for an HTTP 200 Etherscan envelope with `status:"1"`;
  every logical/transport/panic/cancellation/capture failure releases the
  reservation. The committed debit precedes response delivery.

- The superseded `features.x402_billing`, `billing.routes`, coarse request-
  payment limiter settings, native quota wrapper, and HTTP payment dispatcher
  do not exist. Stale YAML and environment keys fail configuration loading.
  The payment ledger accepts new reservations only through the account-top-up
  method; legacy request rows remain readable, immutable audit history and are
  still available to bounded operator inspection/reconciliation.

- Top-up intents bind user, SIWE address, chain, asset, amount, recipient, and
  expiry. x402 v2.23 exact-EVM `authorization` supports only EIP-3009 and
  Permit2. Their replay HMACs use separate domains and exclude the signature;
  Permit2 material includes owner, token, amount, canonical proxy spender,
  nonce, deadline, and witness. A valid pending settlement stores its one
  transaction hash immutably, and operator settlement reconciliation must
  reuse that hash before the atomic payment/intent/credit/account commit.

## Multi-payment funding policy

`billing.payment_chains` owns up to 32 payment networks, each with a required public display `name`, a server-side
facilitator and up to 32 token options. Option IDs are globally unique; addresses
are unique within a chain. Required precision is 0–255. Positive integer rate
numerator/denominator converts token atomic units into six-decimal credit atomic
units with exact integer arithmetic. Fractional, zero or overflowing results fail.
Intents store the full public policy and resulting credit. Payments retain the
on-chain amount; the atomic settlement transaction posts only the snapshotted
credit to the user/explorer-chain account. The frontend selects chain then token,
clears drafts on selection changes and binds wallet operations to that option.
Explicit network selection invokes the injected wallet switch directly. Failed
switches retain the new selection, display unavailable balance and stop further
work until explicit balance-refresh retry succeeds. Payment submission is disabled
and guarded while switching or after failure; dismissing the message, editing the
amount or selecting another token on the same network does not clear the failure.
The notification names the network briefly, while the network selector retains
a separate wallet-readiness explanation. The payment-view scope retains site-bound SIWE identity
and stays on the payment network until history, prices or route departure;
restoration failure disconnects local wallet/authentication state (ADR-0020).
Balance read failures display only unavailable, with an accessible icon retry.
Facilitator credentials are network-keyed, API/all-only secrets and never public.

## Standalone multi-chain facilitator

ADR-0059 introduces an independent authenticated x402 process. Explicit network
and asset routing selects an RPC and keystore/KMS signer. PostgreSQL journals
payment identity and immutable signed transactions before broadcast; a singleton
connection lock and per-chain queues prevent concurrent nonce allocation. Restart
only reconciles original transaction hashes. Account credits remain owned by the
existing API-side fenced ledger.
Configuration decoding, bounded credential resolution and runtime construction are
separate phases. Ordinary RPC URLs live in YAML; sensitive URLs use explicit
file references. Server TLS and per-chain RPC plaintext permissions are independent.
Database and bearer credentials additionally accept the fixed `X402_DATABASE_URL`
and `X402_BEARER_TOKEN` environment pairs with mutually exclusive `_FILE` variants;
nonblank environment sources override YAML file references.
HTTP handlers call transport-independent settlement operations backed by the
sqlc journal interface. Runtime construction owns reverse-order cleanup and
idempotent shutdown; `--check-config` validates local inputs without external calls.

## Top-up progress

The account top-up card presents wallet signature, facilitator submission and
transaction confirmation as distinct steps, with optional Permit2 approval
steps and bilingual unknown-outcome guidance. The API observes the facilitator's
authenticated read-only journal progress while the original settlement runs and
publishes fenced observations on the intent. Browser intent polling begins at
signed submission, preserves terminal results and known hashes, and restores
durable observations on reload. Neither progress nor a transaction hash credits
an account; ADR-0044 and ADR-0059 retain the settlement and publication boundaries.
