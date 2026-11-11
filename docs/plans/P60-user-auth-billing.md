# P60 — User Accounts, Billing & Personal Tools

Status: `done`

## Outcome

Etherview supports revocable, writer-authoritative EOA user sessions created
from server-issued EIP-4361 messages and bounded EIP-1193 `personal_sign`
requests. Users and administrators remain separate from API-key identity and
quota policy.

Users fund permanent six-decimal API credit through Account-page x402 top-ups
with a choice of payment chains and tokens. User-owned API keys spend that credit on all supported bounded
Etherscan V2 reads, while native explorer reads stay free and operator keys
retain an audited quota-controlled bypass.

SIWE accounts also own private address watches and session-bound one-shot browser
notifications. Authenticated CSV export is owned by P40-T23
and the bilingual personal workspace by P50-T74. API keys never authorize these
private tools, and watch/notification/export limits do not debit prepaid credit.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0020](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0035](../decisions/ADR-0035-user-owned-scoped-api-keys.md)
- [EIP-4361](https://eips.ethereum.org/EIPS/eip-4361)
- [EIP-1193](https://eips.ethereum.org/EIPS/eip-1193)
- [ADR-0044](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [ADR-0059](../decisions/ADR-0059-production-x402-facilitator.md)
- [Etherscan V2 compatibility](../architecture/etherscan-v2-compatibility.md)
- [x402 v2 specification](https://github.com/x402-foundation/x402/blob/270a08bfc787176816fa564d93662f2470b28225/specs/x402-specification-v2.md)
- [ADR-0051](../decisions/ADR-0051-watchlist-notifications-csv.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status | Depends on                          | Deliverable                                                                                                                                                          | Verification                                                                                                                              |
| ------- | ------ | ----------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| P60-T02 | done   | P40, P50                            | SIWE challenge/session/profile persistence, typed schema/queries, token/CSRF core and public contracts                                                               | generation, migration, unit and PostgreSQL concurrency tests                                                                              |
| P60-T03 | done   | P60-T02                             | Cookie/Origin/CSRF-protected HTTP authorization and administrator role/status/session-revocation CLI                                                                 | handler/replay/expiry, authorization, CLI and PostgreSQL integration tests                                                                |
| P60-T05 | done   | P60-T03                             | Bounded wallet signing, `AuthProvider`, account page, admin users, and bilingual UX                                                                                  | Vitest, boundary, a11y, and browser tests                                                                                                 |
| P60-T09 | done   | P40, P50, P60-T03, P60-T05          | Scoped, user-owned API keys and a tabbed `/account` management workspace                                                                                             | PostgreSQL, browser, schema/runtime, Hardhat, Foundry, security, race, and aggregate gates pass                                           |
| P60-T11 | done   | P60-T03, P60-T09                    | Prepaid accounts, append-only ledger, top-up intents and usage reservations with ADR/API/config/catalog contracts and typed persistence                              | generation/config, migration, PostgreSQL concurrency and race tests                                                                       |
| P60-T12 | done   | P60-T11, P60-T13                    | x402 v2 EIP-3009/Permit2 top-ups, replay identity, pending settlement/reconciliation, and disposable Anvil/Facilitator production parity                             | hostile protocol/transport, real local chain/ledger/usage/replay/outage, monolith/split tests; live gate passed under P60-T17             |
| P60-T13 | done   | P60-T11                             | Complexity-weighted V2 pricing and etherview.* operation identities, account-wide reservations/debits, operator bypass and bounded response capture                  | Catalog/config completeness, quota/failure/race, namespace, generation, PostgreSQL and local payment parity                               |
| P60-T14 | done   | P60-T12                             | Account balance/top-up/usage APIs, exact whole-token input, constrained wallet approval/signing and recoverable progress UI                                          | Generated client, exact decimal/bounds, wallet/session races, browser recovery and accessibility                                          |
| P60-T16 | done   | P60-T03, P60-T05, P60-T13, P60-T14  | Auth/billing metrics, alerts, role-scoped Secret/Helm/Compose deployment, runbooks, administrator adjustment and reconciliation/rollback controls                    | deployment, security/race, browser/E2E, operations, common and role-parity gates                                                          |
| P60-T17 | done   | P60-T16                             | One-shot live EIP-3009 and Permit2 top-up plus API-debit conformance                                                                                                 | testnet transactions and writer/chain/credit/usage reports                                                                                |
| P60-T18 | done   | P60-T03, P60-T09, P10, P20, P40-T01 | Private writer-authoritative watch management, bounded enrollment, fresh schema/typed queries, and ownership isolation                                               | OpenAPI/generation, schema, Cookie/Origin/CSRF, handler, watch-limit, and PostgreSQL regressions                                          |
| P60-T20 | done   | P60-T18, P20-T01                    | Session-bound best-effort Web Push with five watches/account, TTL 0, volatile matching and no persisted history/retry queue                                          | Ownership/revocation, restart/replay, encrypted payloads, browser worker and all/split tests; separate operator provider acceptance       |
| P60-T21 | done   | P60-T12, P60-T16                    | Local x402 top-ups on the existing Geth/Lighthouse Preview, genesis-predeployed token and prepaid usage                                                              | Preview EIP-3009/Permit2, restart/outage and split/all-role acceptance; no public testnet evidence                                        |
| P60-T25 | done   | P60-T12, P60-T14, P60-T21           | Multi-chain/token funding with immutable conversion into unified credit, payment-view wallet scope and persistent second Preview chain                               | Four payment options, exact credit/replay, failed-switch recovery, departure restoration, gateway/restart/outage and all/split acceptance |
| P60-T26 | done   | P60-T25                             | Standalone multi-chain facilitator with keystore/KMS, durable settlement/progress, strict offline-checkable configuration, role-scoped secrets and bounded lifecycle | Protocol/config/CLI race, PostgreSQL journal/nonce/recovery, schema/runtime/Preview and deployment gates; local evidence                  |
| P60-T27 | done   | P60-T26                             | Rustack KMS and two independent Anvil networks for local facilitator acceptance                                                                                      | AWS SDK HTTP signing compatibility, both payment methods on both networks, all/split recovery and independent chain/writer reconciliation |
| P60-T31 | done   | P60-T03, P60-T05, P60-T16           | Administrator-only sync status, address-only users, default overview and empty-list pagination                                                                       | HTTP/API, PostgreSQL, bilingual browser and production all/split acceptance passed                                                        |
| P60-T32 | done   | P60-T26, P60-T27                    | Fenced facilitator recovery, independently verified external settlement, automatic exactly-once top-up reconciliation and pre-settlement verification retry          | PostgreSQL handoff/lease regressions, hostile evidence, both methods, all/split runtime and browser acceptance                            |

## Acceptance

- [x] P60-T32: Session-fenced journal writes, conditional unsigned termination,
      separate original/payment identity, direct EIP-3009/Permit2 evidence,
      read-only results and API-owned exactly-once background credit pass current
      PostgreSQL, all/split, Preview and Rustack recovery acceptance. Verification
      retries reuse the original request under a new attempt lease; settlement
      never retries automatically. Inactive singleton loss exits nonzero within
      the shared shutdown budget, including during a chain RPC outage.

- [x] P60-T31: Sync status requires an active administrator session in both
      production layouts; anonymous, API-key-only, ordinary, disabled and invalid
      sessions cannot authorize it. Address-only user search retains chain isolation
      and query-bound pagination. Compact navigation, settings/detail drawers and
      Billing tabs pass bilingual, light/dark, desktop and 390px browser checks,
      including session cleanup, errors, keyboard focus and overflow. Overview
      defaults to current-chain user counts and the existing billing summary;
      custom display names are removed and empty lists hide pagination.

- [x] Payment-network selection retains failed selections and unavailable balances;
      the payment-view scope preserves site-bound identity and restores the original
      network on departure (P60-T25).
- [x] Durable broadcast-backed progress and recoverable bilingual top-up steps
      preserve exactly-once credit and all/split parity (P60-T26). Rustack KMS evidence
      remains independently recorded under P60-T27.
- [x] P60-T27: AWS SDK HTTP signing through Rustack and both payment methods on
      two independent Anvil networks pass all/split recovery and independent
      chain/writer reconciliation under the acceptance criteria below.

- [x] P60-T13: The etherview.* operation namespace is used end to end without legacy aliases or backfills; live payment acceptance remains separate.

- [x] An invalid signature creates neither a user nor a session; a first valid
      login creates an ordinary active user.
- [x] Wrong address, chain, domain, URI, expiration, reused challenge, wallet
      account, provider, or wallet-session revision fails closed.
- [x] Session and CSRF plaintext never enter PostgreSQL, localStorage, URLs,
      logs, or server-generated browser assets.
- [x] Logout, administrator revocation, and user disablement take effect on the
      next writer-backed authenticated request across API replicas.
- [x] Ordinary users cannot access administrator operations, and role/status
      mutations retain stable timestamps and typed errors.
- [x] The SPA distinguishes wallet connection from authentication and requires
      reauthentication after a wallet identity or configured-chain change.
- [x] A full SPA reload restores a valid Cookie session before wallet
      reconnection; the first observed wallet must match that user and later
      wallet-identity changes still revoke the session.
- [x] SIWE can authorize a sole discovered wallet directly or select among
      multiple wallets inline without requiring a separate connection action.
- [x] Existing API-key, rate-limit, verification, compatibility, and wallet
      contract-call behavior remains compatible.
- [x] `serve --roles=all` and the split API role use the same implementation and
      PostgreSQL session semantics.
- [x] The `/account` workspace exposes deep-linked Overview, Watchlist, API Keys
      and Billing tabs; `/users/me/api-keys` remains an API-only namespace.
- [x] Personal API keys are writer-owned, canonically scoped, bounded by an
      atomic per-user limit, one-time revealed, and permanently revoked when
      their owner is disabled.
- [x] Native, Etherscan, NFT-media, quota, and x402 authorization use the same
      scope identity; a wrong scope fails forbidden without anonymous or paid
      fallback, while API keys never authorize account management.
- [x] Historical accountless request-payment rows remain audit-only and never
      grant credit.
- [x] A settled top-up credits exactly one matching active user account once.
- [x] EIP-3009 and Permit2 top-ups are replay-fenced and payer-bound.
- [x] User-owned keys share account credit; operator keys bypass credit only.
- [x] Only bounded `/v2/api` query logical successes commit a debit.
- [x] Native explorer reads never prompt for or accept x402 payment.
- [x] Reservation, crash, pending-settlement, and reconciliation behavior is durable.
- [x] Account UI exposes only the constrained top-up signing and exact approval flow.
- [x] Monolith and split roles share identical writer and payment semantics.
- [x] Live EIP-3009 and Permit2 staging reports reconcile settlement, account
      credit, API debit and independent writer/chain observations (P60-T17).
- [x] Superseded accountless request-payment execution and configuration are
      removed; immutable historical audit rows remain readable and cannot create
      prepaid credit or change top-up accounting.
- [x] Private watches are limited to 5 addresses/user and start after enrollment
      or re-enablement at an exact canonical tip. Writer SIWE ownership, Origin,
      and CSRF checks isolate users; API keys cannot manage private tools.
- [x] P60-T20 replaces durable notification history and queues with session-bound
      Web Push, TTL 0, no retries/backfill, bounded matching and all/split parity.
      P40 subscription API and P50 browser controls/worker share all/split feature
      acceptance. Real provider delivery is separate from local evidence.

## Current Blockers

P60-T32 local recovery acceptance is complete. P60-T26 and P60-T27 retain
their earlier scope. Rustack SDK compatibility and dual-Anvil all/split reconciliation passed;
real AWS provisioning and public-testnet funding are outside T27's local scope.
Earlier P60-T17 live evidence retains its original scope, and P70 release
gates remain separate.

## Evidence

### Facilitator and automatic payment recovery (P60-T32)

Current Go/race and managed PostgreSQL tests pass, including both unsigned/signing
orders, session loss, competing verification/result leases and exact-once credits.
Production all/split E2E confirms both protocols paid externally before verification
or before the original transaction reverts, preserving both hashes. Preview and
Rustack `ffc10378c64280a78bf6b73fff51612bbba0eb66` prove automatic credit after paused
mining, lost broadcast responses and facilitator restart without operator credit;
Rustack independently reconciles eight unique credits totaling 800 per topology.
KMS failures remain unresolved until canonical expiry with an unused authorization.

OpenAPI, generation, Go/frontend lint, docs/plan, fresh production schema, runtime,
Preview, Rustack and deployment gates pass. All 1,034 frontend tests and the full
270-case browser suite pass; the updated 12 billing browser cases also pass with
identical-header retry and separate hashes. Mobile English/light and desktop
Chinese/dark recovery screenshots were inspected. This is local acceptance only.

### Management navigation and page hierarchy (P60-T31)

HTTP race/API contract tests, full PostgreSQL integration, 1,021 frontend tests
and all 252 embedded browser tests passed. The final user-row simplification
also passed all 10 management browser cases without retries; mobile/desktop,
English/Chinese and light/dark screenshots were inspected. Fresh production
schema and all/split runtime checks passed, including overview authorization.
OpenAPI, generation, frontend/Go lint, asset budgets, documentation and plan
checks passed without changing their acceptance limits.

### Final live payment and API-debit acceptance

Manual MetaMask SIWE, EIP-3009 and Permit2 payments, finalized independent-RPC
reconciliation, unique account credits and successful priced API debits passed.
Permit2's exact approval and consumed allowance were verified. Anvil supplied
normally indexed query data; Sepolia was used only for settlement. The browser
preserves site-bound authentication across expected payment-chain transitions
and rejects unexpected wallet identity changes.

The one-shot harness, report SQL, Make targets and local runtime were removed
after acceptance. At the operator's request, detailed reports, credentials,
transaction identifiers and the local databases were also deleted. This plan
retains only the verified acceptance conclusion; raw evidence is no longer kept.
The production wallet regressions and Go lint fixes are retained. P60-T17 is complete within its original scope; P70 release gates remain separate.

P60-T25's payment-view scope also passed controlled-wallet frontend and bilingual
mobile/desktop browser regressions for immediate network selection, retained failed
selections, unavailable balances, partial-switch recovery and departure races.
Frontend lint, generation and governance checks passed. This UI extension has local
acceptance only; the earlier live-payment conclusion does not extend to it.

### Sessions, keys and prepaid accounting

P60-T02/P60-T03/P60-T05/P60-T09/P60-T16 passed SIWE challenge/replay/expiry,
writer session revocation, Cookie/Origin/CSRF, scoped-key ownership, bilingual account and deployment unit/browser and
PostgreSQL concurrency/race checks at delivery. P60-T11/P60-T12/P60-T13
prepaid ledger, reservation/debit/release, pending settlement and replay passed local real-Anvil
production tests in both layouts. Exact complexity pricing and the etherview.*
namespace passed catalog/config, generation, browser and accounting tests; shared
archive/V2 acceptance is in [P20](P20-enrichment.md#blob-access-and-archive-update).
P60-T14 passed generated API, exact amount, bounded approval/signing and browser
recovery/accessibility checks. These local tests are distinct from the live gate above.

### Private watches and browser push

P60-T18/P60-T20 passed ownership/revocation, concurrent watch limits, encrypted
payload/VAPID, bounded matching, lock-loss cancellation, delayed publication,
restart backlog loss and all/split tests. Chromium worker delivery with the page
closed used a CDP-injected event and does not prove vendor delivery.

On 2026-10-24, the operator confirmed that an earlier manual test successfully
received a real browser notification after all site tabs were closed, and
explicitly accepted this result to close P60-T20. This operator-reported result
closes the outstanding browser-provider delivery gate separately from the local
automated evidence. The test date, deployed build digest, browser/OS versions
and exact permission state were not supplied with the confirmation. This update
records the operator's acceptance; it does not claim an independently rerun
test, native notification-click verification, or broader browser/OS coverage.

### Multi-payment funding and standalone facilitator

P60-T21/P60-T25 passed local Preview EIP-3009/Permit2, four chain/token choices,
exact conversion/credit/replay, wallet-switch failure/restoration, gateway,
persistent Anvil restart/outage and uncertain-settlement recovery in both layouts.
T25's original local Permit2 fixture does not establish production-contract
compatibility; earlier live evidence does not extend to these later changes.

P60-T26 replaced that fixture with the standalone service and standard contracts.
Signer/protocol/configuration/CLI race, PostgreSQL journal/nonce/crash/restart,
strict offline validation, secret isolation, reverse startup cleanup and immediate
lock reacquisition passed. `make check`, schema/runtime, Preview x402 and Rustack
all/split acceptance passed locally. Durable progress also passed browser recovery,
PostgreSQL race and delayed-mining/unknown-response tests. Real AWS IAM and new
public-network acceptance are not established.

### Rustack KMS and dual-Anvil acceptance (P60-T27)

Completed on 2026-11-02 using Rustack commit
`ffc10378c64280a78bf6b73fff51612bbba0eb66`, built from its locked source with the
upstream Dockerfile. The published 0.10.0 image rejected `ECC_SECG_P256K1`;
the pinned source passed actual AWS SDK HTTP `CreateKey`, `GetPublicKey` and
`Sign(DIGEST, ECDSA_SHA_256)`, including `SIGN_VERIFY` metadata, SPKI validation
and Ethereum signature/address recovery. No fake KMS or keystore substitution
was used.

The root production image's standalone facilitator passed against two independent
Anvil v1.8.7 networks (31337/31338), each with its own RPC and persistent volume,
standard EIP-3009/Permit2 contracts and local funds. Both monolith (`all`) and
distributed (`split`) layouts independently reconciled eight successful receipts
and exact token transfer logs with journaled transaction bytes, KMS sender,
contiguous nonces, eight unique account credits totaling 800 and one priced API
debit of 2, leaving 798 available and zero reserved. The facility starts no Geth;
existing Preview topology remains unchanged.

Duplicate payments and exact Permit2 allowance consumption passed. KMS outage
left one unsigned failed reservation with no credit; recovery permitted new
payments. Anvil restart retained transfers and receipts while the other network
continued settling. Delayed mining exposed broadcast progress. A fault proxy
forwarded a real transaction and discarded its broadcast response; facilitator
restart recovered the identical bytes/hash/nonce, and operator reconciliation
credited it once with replay rejection. No pending journal rows remained.

Acceptance: `make test-rustack-x402` built both images and passed both layouts
(91.38 seconds for the Go suite); the prebuilt target also passed. Facilitator
unit/race and PostgreSQL integration/race, `make lint-go`, deployment, workflow
scripts, documentation and plan checks passed on macOS arm64 with Docker Linux
arm64 and PostgreSQL 18. The runtime tests retain no production secrets and
remove their isolated projects and volumes after execution.

This local simulation does not establish real AWS IAM enforcement, production
key isolation or public-network production readiness. KMS outage uses pause/resume
and makes no emulator key-persistence claim. P70 release gates and historical
P60-T17/P60-T26 evidence retain their original scope.
