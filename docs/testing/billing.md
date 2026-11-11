# Billing and facilitator acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [Real prepaid x402 Ethereum Sepolia gate](#real-prepaid-x402-ethereum-sepolia-gate)
- [Separate payment-chain wallet acceptance](#separate-payment-chain-wallet-acceptance)
- [Multi-option billing regression coverage](#multi-option-billing-regression-coverage)
- [Standalone facilitator acceptance](#standalone-facilitator-acceptance)
- [Top-up progress](#top-up-progress)

## Real prepaid x402 Ethereum Sepolia gate

The live release gate is separate from local and ordinary CI. Both EIP-3009
and Permit2 require independent verification of the complete staging sequence:

1. SIWE authenticates the funded payer.
2. The Account endpoint creates and settles one bounded top-up intent.
3. The writer commits exactly one account credit.
4. A newly issued user-owned `api:read` key commits one priced
   `/v2/api` debit.
5. Independent writer and Ethereum Sepolia RPC checks reconcile the payment,
   credit entry, usage entry, transaction hash, payer, recipient, asset, and
   amount.

The gate is one-shot and non-retrying. Payer signing may remain entirely in MetaMask; do not export its key.
Facilitator keys, independent RPC URLs and writer URLs are file-only `0600` inputs. The operator must also record the
staging Facilitator identity and deployed production image digest. Missing
funds, credentials, staging Facilitator, writer, independent RPC, or image
digest prevents acceptance. Local-only Anvil evidence never closes this gate;
Anvil-indexed queries may be combined with real Sepolia payments and independent
RPC reconciliation as described in the runbook.
After an unknown or incomplete attempt, inspect and reconcile that exact
payment before authorizing any new attempt.

`make test-runtime-e2e` does not spend testnet funds. In addition to the
general runtime suite, it builds the test-only x402 contract and production Facilitator
images and runs the same fixture against production Etherview images in
monolith and seven-role distributed topologies. It verifies real Anvil SIWE,
EIP-3009 and Permit2 settlement, exact Permit2 approval, replay rejection,
shared user balance, concurrent debit, logical-failure release, operator
bypass, and final recipient/account equality. The general runtime and x402
fixture also proves a priced-but-operator-bypassed Etherscan ERC-20 holding
read against the real token and exact Anvil state. The packages execute
serially: each owns independent multi-container topologies,
and the general runtime package's bounded-load thresholds require that the
x402 Docker fixture is not competing for the same runner resources.

For interactive debugging:

```sh
make start-x402-local
X402_LOCAL_TOPOLOGY=distributed make recreate-x402-local
make stop-x402-local
```

The local environment uses deterministic development keys. Never fund or reuse
them on any external network.

See [x402 billing gate](../../preview/docs/testing.md#x402-billing-gate).

## Separate payment-chain wallet acceptance

P60-T25 adds an explicit top-up scope: the injected wallet changes operation
revision on expected payment-chain transitions while keeping site-bound SIWE
identity. Run `make web-test`, `make web-lint`, `make test-e2e`,
`make generate-check`, `make docs-check` and `make plan-check`. Focused coverage
is in `wallet/eip6963.test.tsx`, `auth/AuthProvider.test.tsx` and
`pages/PersonalBilling.test.tsx`: exact expected transitions, missing events,
account/network changes, rejected entry/restoration, stable authentication,
and balance checks only after explicit user action when switching is needed.
The P60-T25 payment view retains its scope across reads and payment outcomes. Cover
immediate selection switching, retained failed selections without old balances,
icon-only retry, unavailable/zero balances, serialized entry, partial failures,
late entry after departure, restoration on history/prices/route departure and
restoration failure. Balance reads create no intents or payment signatures;
account/provider changes and stale results remain fenced. Passive refresh must never trigger a chain switch.
Automated tests never spend Sepolia funds. Real payment and API-debit acceptance is documented in
[P60](../plans/P60-user-auth-billing.md#final-live-payment-and-api-debit-acceptance).
Its one-shot local harness and detailed local records were deleted after
acceptance at the operator's request; the plan retains the acceptance conclusion.

## Multi-option billing regression coverage

P60-T25 adds exact rational conversion and explicit precision tests, configuration
uniqueness/credential routing checks, and PostgreSQL four-option funding into one
account with immutable snapshots and replay rejection. Browser regressions cover
chain/token selection and draft reset. `make test-preview-x402` compares token
and credit increments for all four Preview options in both role layouts through
the Reth HTTPS gateway, then checks recreation and isolated payment-chain outage.
P60-T25 uses local acceptance only; local Permit2 fixture evidence makes no
production-contract compatibility claim.

## Standalone facilitator acceptance

Capacity-refusal regressions cover simultaneous same-chain top-ups, strict client
classification, API failed-payment publication and intent reopening, refusal replay across restart,
pending-chain admission, existing unsigned/signed owners, digest conflicts and
PostgreSQL reservation/refusal races. A refused authorization must never obtain
signed bytes; an existing transaction must retain its original hash.

The monitor-overlap regression holds a real PostgreSQL journal scan behind a
barrier and verifies bounded submission waiting, cancellation without journal
mutation, and recovery of the original signed transaction after service recreation.
HTTP/ledger regressions retain the pending hash through response encoding failure,
reject replacement hashes and replay, and require exactly one reconciled credit.
Encoding failure returns 503 rather than a pending 402 without its settlement
header; the persisted payment remains pending and is never resubmitted.

Facilitator configuration coverage includes mutually exclusive RPC sources, independent listener
and RPC plaintext permissions, strict legacy/unknown/duplicate YAML rejection,
bounded files, redacted field errors and offline `--check-config`. Credential
environment regressions cover direct/file precedence, blank fallback, present-but-empty
pair conflicts, source failures, size limits, CLI log redaction and real PostgreSQL
startup/authentication. Compose checks keep host credentials out of Preview/E2E;
Helm checks render only explicit Secret selectors into the facilitator container. Lifecycle
regressions exercise failed startup followed by immediate lock reacquisition,
reverse cleanup and repeated close. `deploy/facilitator/helm/test.sh` validates
the rendered ConfigMap through the real Go configuration loader and rejects
conflicting sources and listener settings incompatible with probes.

Run `go test -race ./internal/facilitator ./cmd/x402facilitator` for configuration,
protocol routing, authentication and keystore/KMS signature regressions. Run
`go run ./e2e/cmd/testintegration -packages ./internal/facilitator -race` for durable
reservation, nonce uniqueness, immutable transaction bytes and singleton-lock
recovery. The existing `make test-runtime-e2e` and `make test-preview-x402` exercise
the production facilitator and standard EIP-3009/Permit2 contract paths in
all/split topologies. `node --test preview/reth/test.mjs` verifies persisted
standard Permit2 nonce bitmaps and exact approval consumption across restart.
Run generation, lint, schema, deployment, docs and plan gates for this change.
P60-T27 uses a separate local acceptance facility using a pinned
[Rustack](https://github.com/tyrchen/rustack) version or commit and two independent
Anvil networks. It must not start or depend on Geth; existing Preview tests above
retain their topology and do not substitute for T27 evidence. The facility is not
part of the ordinary Preview gate. Run `make test-rustack-x402` to build the
production image and pinned Rustack source, then execute both topologies.
`make test-rustack-x402-prebuilt` runs the same checks with those images already
loaded. The dedicated Go build tags are `runtimee2e,rustacke2e`.

First verify `ECC_SECG_P256K1`, `SIGN_VERIFY`, `GetPublicKey` and
`Sign(DIGEST, ECDSA_SHA_256)` through actual AWS SDK HTTP calls, including public-key
parsing, signature recovery and address matching. Missing emulator capabilities
block acceptance until a pinned revision passes; fake KMS or keystore substitution
cannot satisfy this check.

Use distinct chain IDs, RPC endpoints and persistent directories for both Anvil
instances, with standard EIP-3009/Permit2 contracts and local test funds on each.
Run the production facilitator with Rustack KMS signing for both methods on each
network in all/split topologies. Independently reconcile transactions, token
transfers, PostgreSQL settlement journals, unique credits and priced API debits.
Cover duplicates, KMS outage/recovery, isolated chain failure, delayed mining,
uncertain broadcast and facilitator restart, preserving the original transaction,
nonce uniqueness and exactly-once credit. Scope test credentials and emulator
endpoints to this facility and record version, environment, results and
reconciliation evidence before closing T27.

Local simulation does not establish real AWS IAM enforcement, production key
isolation or public-network production readiness. P70 release gates remain separate.

## Top-up progress

P60-T26 includes wallet-bound progress callbacks, bilingual accessible step lists,
response-order protection, and uncertain-response recovery tests. Run the
focused billing/topup and PersonalBilling tests followed by `make web-test`,
`make web-lint`, and `make test-e2e`; account billing browser tests hold signing
and settlement responses to inspect every main step in both languages.

Run `go run ./e2e/cmd/testintegration -packages "./internal/billing ./internal/facilitator"`
for journal authentication/digest checks, concurrent read-only progress and
writer ownership/hash/terminal fences. The x402 package in
`make test-runtime-e2e` disables Anvil automining until broadcast progress is
visible, then mines and verifies the existing exactly-once credit and all/split
parity. These are local-chain and browser-fixture checks, not live-funded evidence.
Schema changes also require `make test-schema-e2e`; run generation, Go lint,
documentation and plan checks for the public/persistent contract changes.
