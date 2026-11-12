# Billing and x402 facilitator

[Operations index](../operations.md) · Commands run from the repository root.

- [Prepaid API billing and x402 top-ups](#prepaid-api-billing-and-x402-top-ups)
- [Multiple x402 payment options](#multiple-x402-payment-options)
- [Standalone multi-chain facilitator](#standalone-multi-chain-facilitator)

## Prepaid API billing and x402 top-ups

Initialize the current baseline with both features off. The schema retains
`legacy_request` as an audit-only payment purpose; such rows never produce
account credit. The baseline does not import historical accountless payments.
Configure `features.api_billing`, the CAIP-2 network,
asset, reservation TTLs, and positive `billing.base_price_atomic` first.
Every V2 query price is its catalog complexity weight times this base; the public
billing configuration lists the complete schedule. The former `billing.operations`
map is rejected. Whole-token UI input is converted exactly to atomic units.
Native `/api/v1` reads remain free regardless of this catalog.

Enable `features.x402_topups` only after SIWE and user-owned API keys are
enabled. Configure the public origin, top-up bounds, recipient, ordered
`[eip3009, permit2]` methods, EIP-712 asset fields, HTTPS facilitator origin,
and exact facilitator CIDRs. Inject the independent fingerprint pepper and
optional facilitator headers only into `all`/`api`. Helm requires a dedicated
API/all TCP/443 egress policy; every DNS result must remain inside the
configured CIDRs. `facilitator_unsafe_allow_http` exists only for the
repository-owned disposable local environment and Helm fixes it to false.

Validate the final API-role configuration before enabling top-ups:

```sh
etherview doctor --config /etc/etherview/config.yaml
```

Validate the complete catalog-derived price schedule before enabling billing.
Enable top-ups and validate both EIP-3009 and Permit2 using the
[staging acceptance procedure](../testing/billing.md#real-prepaid-x402-ethereum-sepolia-gate).
There is no per-operation price map or incremental action-pricing rollout. Facilitator failure prevents only new top-ups. Existing account credit
continues to serve priced `/v2/api` requests because consumption has no
facilitator dependency.

A top-up in `settling` with `settlement_unknown` requires independent
facilitator/chain inspection. `settlement_pending` additionally carries one
immutable transaction hash; settled reconciliation must reuse it. Inspect and
reconcile with:

```sh
etherview admin billing inspect \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000

etherview admin billing reconcile \
  --config /etc/etherview/config.yaml \
  --id 00000000-0000-0000-0000-000000000000 \
  --outcome settled \
  --transaction-hash 0x0000000000000000000000000000000000000000000000000000000000000001
```

A settled top-up reconciliation atomically settles the payment and intent,
appends one credit entry, and advances the account. Never retry settlement or
replace a known pending hash. Usage maintenance releases only expired
reservations; it never refunds a committed debit, including when the process
crashes after commit but before response delivery. Administrator corrections
use the authenticated adjustment endpoint with an explicit reason.

For rollback, first block new V2 traffic at ingress and disable new top-ups,
then drain or reconcile active usage and settlement work. Disable
`features.api_billing` and clear `billing.base_price_atomic` together if required.
Do not use a missing price as a traffic stop: missing query prices are errors.
Permanently retain accounts, balances,
entries, usage, intents, payments, and payment events. Never convert legacy
accountless request settlements into credit.

The disposable local environment is:

```sh
make start-x402-local
X402_LOCAL_TOPOLOGY=distributed make recreate-x402-local
make stop-x402-local
```

It uses Anvil, PostgreSQL 18, the production Etherview image, and separate
test-only contract/facilitator images. Its deterministic development keys and
fixtures must never be used outside that environment.

### Ethereum Sepolia prepaid release gate

Use the [live-payment acceptance procedure](../testing/billing.md#real-prepaid-x402-ethereum-sepolia-gate)
for prerequisites, the two required payment methods and independent reconciliation.
An unknown payment must be reconciled by its original identity before any new
attempt; local fixtures do not close the live gate. Current acceptance status
belongs to [P60](../plans/P60-user-auth-billing.md#final-live-payment-and-api-debit-acceptance)
and the separate final-revision [P70 release gates](../plans/P70-release.md).

## Multiple x402 payment options

Configure `billing.payment_chains` with `name`, `network`, `facilitator_url` and `tokens`.
The required `name` is a nonblank display label of 1–128 characters; for example,
`Base Sepolia` for `eip155:84532`. Network identity and authorization use `network`,
never a presentation label.
Each token requires `id`, `name`, `asset`, `asset_decimals`, `recipient`,
`asset_transfer_methods`, `minimum_topup_amount_atomic`,
`maximum_topup_amount_atomic`, `credit_rate_numerator` and
`credit_rate_denominator`; EIP-3009 also requires `asset_eip712_name` and
`asset_eip712_version`. See the four concrete options in
[Preview configuration](../../preview/config.yaml). Ratios convert atomic token
units to atomic credits: an 18-decimal token worth two credits uses numerator
`2` and denominator `1000000000000`. All account, price and adjustment amounts
are six-decimal credits. Chain credentials use the secret-only
`ETHERVIEW_X402_FACILITATOR_HEADERS(_FILE)` JSON map, for example
`{"eip155:11155111":{"Authorization":"Bearer ..."}}`; each map key must identify
a configured payment chain. Legacy flat payment YAML and environment keys are
rejected. Use a fresh database; there is no old-balance conversion.

Policy edits affect new intents only. Keep the option ID and chain facilitator
available while old intents expire or settle; never reuse an ID for a different
chain. Unknown settlements permit status recovery or explicit reconciliation,
not automatic repayment. Disabling a payment chain does not disable existing
credit consumption or explorer reads.

## Standalone multi-chain facilitator

The production `x402facilitator` process is separate from all explorer roles.
Build the shared production image with `make docker-build IMAGE=etherview:local`.
Compose uses `ETHERVIEW_IMAGE` (default `etherview:local`) and overrides the
entrypoint to `/x402facilitator`; the standalone Helm chart uses its `image` value
and the same executable as its container command. The image retains `/etherview`
as its default entrypoint. Pass `--config=/etc/etherview/facilitator.yaml` to the
facilitator process, with configuration and signer secrets mounted separately.
Use `deploy/facilitator/config.example.yaml`, the standalone Compose definition,
or the chart under `deploy/facilitator/helm`. Run the ordinary fresh-database
migrations before starting it. Its database login needs access only to
`facilitator_settlements` (SELECT, INSERT, UPDATE), schema USAGE and the advisory
lock function; do not grant user-account credit writes. It shares the migrated
PostgreSQL database, not API credentials. Each network/account must be reserved
exclusively for this transaction sender.

Use the `server`, `database`, `auth`, `signers` and `chains` sections shown in
the example. Supply the database URL and bearer token through environment or file
injection as described below. Mount the TLS private key and optional encrypted
keystore/password as read-only secrets. Mount the public
TLS certificate separately as ordinary configuration. Standalone Compose expects
`./certs/tls.crt`; Helm requires `certificateConfigMapName` naming a ConfigMap
with `tls.crt`. Its listener is fixed to `0.0.0.0:8081` to match the Service and probes.

The facilitator supports these fixed credential environment pairs:

| Direct value        | File source              | YAML fallback            |
| ------------------- | ------------------------ | ------------------------ |
| `X402_DATABASE_URL` | `X402_DATABASE_URL_FILE` | `database.url_file`      |
| `X402_BEARER_TOKEN` | `X402_BEARER_TOKEN_FILE` | `auth.bearer_token_file` |

Set at most one member of each pair: setting both, even to empty strings, fails.
A nonblank environment value overrides the YAML file without reading it. Blank
direct values or readable empty/whitespace environment files fall back to YAML; an
unreadable environment file or oversized value fails without fallback. A valid
source is mandatory. Limits apply before trimming: 8 KiB for the database URL,
4 KiB for the bearer token, which must retain at least 32 bytes after trimming.
The `database` and `auth` sections may be omitted when environment injection supplies
them. RPC, signer and TLS configuration have no new environment overrides.

Standalone Compose forwards these four variables only when present; do not export
both forms. Preview and E2E Compose do not inherit them from the host. Helm accepts
only Secret selectors through `secretEnv`, for example:

```yaml
secretEnv:
  X402_DATABASE_URL:
    name: facilitator-credentials
    key: database-url
  X402_BEARER_TOKEN:
    name: facilitator-credentials
    key: bearer-token
```

Do not put plaintext credentials in Helm values or the ConfigMap. The selectors
attach only to the facilitator container; TLS and signer file mounts retain their
existing scope. `--check-config` uses the same environment as startup, still without
external calls or signing. Diagnostics never include environment values or paths.

Set ordinary RPC destinations directly in `chains[].rpc.url`. Use `rpc.url_file`
instead when the complete URL contains credentials, including API keys embedded
in paths; automatic credential detection is not reliable. Exactly one source is
required.
Production server TLS is mandatory; `server.allow_insecure_http` is a local-only
exception. Each `rpc.allow_insecure_http` is an independent local-only exception,
false by default. Enabling either one never enables the other.

Replace old configuration files completely: top-level `listen`, `database_url_file`,
`bearer_token_file`, `unsafe_local_http` and chain `rpc_url_file` are rejected.
No configuration aliases, general environment overrides or YAML interpolation are supported.
Database schema and payment protocols do not change. Stop the old singleton,
replace configuration and mounts together, validate, then start the new process.

Run `/x402facilitator --config=/etc/etherview/facilitator.yaml --check-config`
with the deployment mounts before startup. This checks strict YAML and bounded
local files, including the TLS key/certificate pair, without connecting to
PostgreSQL/RPC/KMS or decrypting a signing key. It does not establish readiness.
Errors contain stable codes and schema field positions; signer positions are
zero-based in sorted signer-ID order. Inspect that field and its mounted file
locally; never paste credentials into logs. Startup still validates database
ownership, signing identity, chain IDs and required contracts.

Bind the signer to its expected address. For AWS KMS use a fixed key ARN, region,
`ECC_SECG_P256K1` and `SIGN_VERIFY`; give the facilitator workload only
`kms:GetPublicKey` and `kms:Sign` for that key. Use workload identity, not static
AWS keys in configuration. Only the facilitator receives this identity.

Set each `billing.payment_chains[].facilitator_url` to this HTTPS origin and
inject its bearer credential through the existing network-keyed
`ETHERVIEW_X402_FACILITATOR_HEADERS_FILE` on API/all. Scope facilitator ingress to
those callers, and egress to the configured PostgreSQL, RPC and KMS destinations.
The Helm chart uses one replica and Recreate updates. Its Service publishes
unready endpoints intentionally: a failed chain makes `/readyz` degraded while
healthy chains remain available; `/healthz` remains the liveness probe.

The service checks chain IDs and required code at startup, bounds gas and fees,
and serializes each chain independently. Authenticated `/metrics` exposes per-chain
health, pending journal count, success/failure counters and low native balance.
Low balance compares available funds with the configured maximum transaction
cost. No sensitive RPC or KMS errors are logged or returned.

A signed transaction is committed before broadcast. A pending payment retains
its exact hash and blocks new transactions on that chain until reconciled.
Restart polls existing hashes; it never signs or broadcasts them again. A crash
before signed bytes were committed is terminally rejected during recovery.
Do not submit another authorization to resolve an uncertain existing payment.
Inspect the journal and chain receipt. If the exact transaction needs manual
rebroadcast, stop the normal process and run:

```sh
x402facilitator --config=/etc/etherview/facilitator.yaml --rebroadcast='AUTHORIZATION_ID'
```

This command acquires the same singleton lock and sends only the saved raw bytes;
it cannot change nonce, fee, destination or calldata. Restart the normal service
afterward. Account credit reconciliation remains the existing API-side operator
procedure. Rotation of signer/network configuration requires resolving pending
transactions first; never delete journal rows to bypass a pending payment.

P60-T26 local acceptance is complete. P60-T27 uses a dedicated local facility
using pinned Rustack KMS and two independent Anvil networks, without Geth or real
AWS/public-testnet prerequisites. This does not replace the existing Preview
topology. Use local test credentials and test-scoped emulator endpoints; never
apply emulator endpoint overrides to production facilitator processes.

Run `make test-rustack-x402` for an isolated, disposable run in both role layouts.
It builds Rustack from commit `ffc10378c64280a78bf6b73fff51612bbba0eb66`; the
published 0.10.0 image lacks the required secp256k1 key support. Use
`make test-rustack-x402-prebuilt` only after both the production image and
`etherview-rustack:ffc10378c64280a78bf6b73fff51612bbba0eb66` are loaded. The test
creates its own Compose project, two Anvil state volumes and KMS key, then removes
them on teardown. It publishes ephemeral loopback service ports and temporarily
uses a host RPC fault proxy reachable from Docker for lost-broadcast-response
coverage. KMS outage coverage pauses/resumes the emulator to retain its signing
identity; it makes no KMS restart-persistence claim.

Its [acceptance requirements](../testing/billing.md#standalone-facilitator-acceptance) require
actual AWS SDK HTTP signing, both payment methods on both networks in all/split
layouts, recovery checks and independent chain/writer reconciliation. Local
simulation does not establish real AWS IAM enforcement, production key isolation
or public-network production readiness; P70 release gates remain separate.

## Pending payment and verification recovery

A `x402_verification_retryable` response permits explicit reuse of the exact signed
request only while the intent is still processing. The browser keeps this request
in memory; it is not stored in browser storage. Other unknown outcomes must use
intent polling. Do not send another authorization to resolve an uncertain payment.

The API role automatically queries /result and publishes validated credits even
when the user closes the page. Inspect the original transaction separately from
`settlement_transaction_hash`: a third-party payment does not replace the original
signer nonce or raw transaction. Stop the normal facilitator before exact-byte
rebroadcast with its existing recovery command. No nonce replacement or fee bump
is performed. Keep the original facilitator/network configuration available until
pending authorizations and outstanding signer transactions are resolved.

Monitor pending counts and age in the journal. Scans are bounded and can take
multiple cycles to locate historical payments. Dense blocks, unavailable history,
contract-internal relay calls and consumed nonces lacking matching evidence remain
pending and require operator investigation; do not infer payment from Transfer
alone. Deep reorgs after configured confirmations are an operational incident:
stop affected settlement publication, retain both hashes and audit account entries,
then use audited account adjustments after independently establishing chain facts.
Automatic terminal-credit reversal is not implemented.

Singleton connection loss causes permanent inactive health and nonzero bounded
process exit. Configure the process supervisor to restart it; restart must acquire
the lock before mutation. Never toggle active state in a running failed instance.

## Payment request deadlines

`server.request_timeout` remains 20s for ordinary APIs. Only
`POST /api/v1/billing/topup-intents/{id}/pay` uses
`server.payment_request_timeout` (default 75s, positive and at most 3m), selected
before authentication. Its environment override is
`ETHERVIEW_SERVER_PAYMENT_REQUEST_TIMEOUT`.

With top-ups enabled, `billing.facilitator_timeout` is 30s–1m (default 30s).
The payment budget must be at least twice that timeout plus 15s: sequential
Verify and Settle calls, 10s for authentication/admission/database work, 2s for
bounded result/ledger completion and 3s response margin. A facilitator settlement
has a 25s execution deadline and up to 2s journal completion. A slow earlier stage
still consumes the same finite total budget; this is not a guarantee of success.
`server.write_timeout` (default 100s) must cover `server.read_timeout` plus the
payment budget plus 5s, since net/http starts its write deadline before body reads.
Invalid combinations fail configuration validation in both all and split roles.

Configure ingress response-header deadlines above the payment budget and client
deadlines above ingress. Preview uses 100s for this exact POST route while ordinary
upstreams retain 30s; its acceptance client uses 110s. Changing the payment budget
requires updating ingress/client limits too. The browser payment fetch has no
independent short timer and retains its existing session/wallet invalidation checks.

Paused mining returns `402` with `PAYMENT-RESPONSE`, `settlement_pending` and the
original hash when the facilitator completes within budget. Exhausting the total
budget returns `504 request_timeout`; durable settling/unknown state and any known
hash remain available for polling and automatic result reconciliation. Neither
case retries settlement or credits before confirmation. Recovery uses the original
transaction identity and the existing exactly-once publication fence.
