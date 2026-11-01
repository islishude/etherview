# P60 — User Accounts, Billing & Personal Tools

Status: `in_progress`

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
- [Etherscan V2 compatibility](../architecture/etherscan-v2-compatibility.md)
- [x402 v2 specification](https://github.com/x402-foundation/x402/blob/270a08bfc787176816fa564d93662f2470b28225/specs/x402-specification-v2.md)
- [ADR-0051](../decisions/ADR-0051-watchlist-notifications-csv.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status | Depends on                          | Deliverable                                                                                                                                                             | Verification                                                                                                                                              |
| ------- | ------ | ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| P60-T02 | done   | P40, P50                            | SIWE challenge/session/profile persistence, typed schema/queries, token/CSRF core and public contracts                                                                  | generation, migration, unit and PostgreSQL concurrency tests                                                                                              |
| P60-T03 | done   | P60-T02                             | Cookie/Origin/CSRF-protected HTTP authorization and administrator role/status/session-revocation CLI                                                                    | handler/replay/expiry, authorization, CLI and PostgreSQL integration tests                                                                                |
| P60-T05 | done   | P60-T03                             | Bounded wallet signing, `AuthProvider`, account page, admin users, and bilingual UX                                                                                     | Vitest, boundary, a11y, and browser tests                                                                                                                 |
| P60-T09 | done   | P40, P50, P60-T03, P60-T05          | Scoped, user-owned API keys and a tabbed `/account` management workspace                                                                                                | PostgreSQL, browser, schema/runtime, Hardhat, Foundry, security, race, and aggregate gates pass                                                           |
| P60-T11 | done   | P60-T03, P60-T09                    | Prepaid accounts, append-only ledger, top-up intents and usage reservations with ADR/API/config/catalog contracts and typed persistence                                 | generation/config, migration, PostgreSQL concurrency and race tests                                                                                       |
| P60-T12 | done   | P60-T11, P60-T13                    | x402 v2 EIP-3009/Permit2 top-ups, replay identity, pending settlement/reconciliation, and disposable Anvil/Facilitator production parity                                | hostile protocol/transport, real local chain/ledger/usage/replay/outage, monolith/split tests; live gate passed under P60-T17                             |
| P60-T13 | done   | P60-T11                             | `/v2/api` user-key reservation/debit gate, operator bypass, canonical resources, bounded capture, and removal of accountless request-payment runtime/configuration      | compatibility, quota/failure/race, config, migration/audit, generation, deployment, and runtime tests                                                     |
| P60-T14 | done   | P60-T12                             | Account balance/top-up/usage APIs and constrained browser wallet top-up flow                                                                                            | generated client, Vitest, Playwright, and accessibility                                                                                                   |
| P60-T16 | done   | P60-T03, P60-T05, P60-T13, P60-T14  | Auth/billing metrics, alerts, role-scoped Secret/Helm/Compose deployment, runbooks, administrator adjustment and reconciliation/rollback controls                       | deployment, security/race, browser/E2E, operations, common and role-parity gates                                                                          |
| P60-T17 | done   | P60-T16                             | One-shot live EIP-3009 and Permit2 top-up plus API-debit conformance                                                                                                    | testnet transactions and writer/chain/credit/usage reports                                                                                                |
| P60-T18 | done   | P60-T03, P60-T09, P10, P20, P40-T01 | Private writer-authoritative watch management, bounded enrollment, fresh schema/typed queries, and ownership isolation                                                  | OpenAPI/generation, schema, Cookie/Origin/CSRF, handler, watch-limit, and PostgreSQL regressions                                                          |
| P60-T19 | done   | P60-T18, P20-T10                    | Durable transaction/token notifications, generation-fenced delivery, reorg/reattach handling, read watermarks, and bounded cleanup                                      | restart, lease/generation, concurrency, indexed matching, disabled-account replay, and PostgreSQL regressions                                             |
| P60-T20 | done   | P60-T18, P60-T19                    | Replace durable notifications with session-bound, best-effort Web Push; five watches/account; align P40 API, P50 browser and P70 topology acceptance                    | Local automated acceptance and operator-confirmed closed-page provider delivery; evidence limits below                                                    |
| P60-T21 | done   | P60-T12, P60-T16                    | Local x402 top-ups on the existing Geth/Lighthouse Preview, genesis-predeployed token and prepaid usage                                                                 | Preview EIP-3009/Permit2, restart/outage and split/all-role acceptance; no public testnet evidence                                                        |
| P60-T22 | done   | P60-T21                             | Whole-token top-up input and mandatory complexity-weighted pricing for every V2 query                                                                                   | Exact decimal/UI, catalog/config, accounting, generation and local all/split x402 gates                                                                   |
| P60-T23 | done   | P60-T22                             | Use etherview.module.action IDs throughout pricing, usage, API, metrics and billing UI; retain Sourcify and V2 naming                                                   | Catalog/ledger/API/UI, integration race, browser and all/split local-payment acceptance                                                                   |
| P60-T24 | done   | P60-T12, P60-T22                    | Payment-view wallet scope with immediate network selection, retained failed selections and site-bound SIWE identity; Anvil indexing and real Sepolia payments                                                             | controlled transitions, failed-selection retry, departure restoration, hostile wallet/session races, exact approval and separate live payment/API-debit evidence                                   |
| P60-T25 | done   | P60-T24                             | Multi-chain, multi-token x402 funding with token-specific precision and fixed conversion into unified credit; persistent Preview Anvil payment chain behind the gateway | Four chain/token combinations, atomic credit and replay, browser selection, gateway, restart/outage and all/split acceptance; local acceptance only       |
| P60-T26 | done   | P60-T25                             | Production standalone multi-chain facilitator, keystore/KMS, durable settlement/progress, recoverable top-up steps and shared Preview                                                                      | Unit/race, durable journal and concurrent-chain recovery, standard-contract dual-chain, schema/runtime and deployment gates passed; local-only acceptance |
| P60-T27 | todo   | P60-T26                             | Real AWS KMS and multi-network testnet facilitator acceptance                                                                                                           | Requires operator-provided KMS identity, funded testnet accounts and independent reconciliation                                                           |

## Acceptance

- [x] Payment-network selection retains failed selections and unavailable balances;
      the payment-view scope preserves site-bound identity and restores the original
      network on departure (P60-T24).
- [x] Durable broadcast-backed progress and recoverable bilingual top-up steps
      preserve exactly-once credit and all/split parity (P60-T26). Local acceptance
      does not satisfy P60-T27's real-environment requirements.

- [x] P60-T23: The etherview.* operation namespace is used end to end without legacy aliases or backfills; live payment acceptance remains separate.

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
- [x] The browser exposes only `/account` with deep-linked Overview, API Keys,
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
      P40 subscription API, P50 browser controls/worker and P70 runtime acceptance
      share this migration. Real provider delivery is separate from local evidence.

## Current Blockers

P60-T26 local delivery is complete. P60-T27 real-environment acceptance is
separate and awaits an authorized AWS KMS identity/key, funded multi-network
accounts and independent RPC/writer reconciliation. Its clearing conditions are
recorded under the standalone facilitator acceptance below. Earlier P60-T17/T24
live evidence does not cover this new service. P70 release gates remain separate.

## Evidence

### Final live payment and API-debit acceptance (P60-T17 / P60-T24)

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
The production wallet regressions and Go lint fixes are retained. P60-T17 and
P60-T24 are complete within their original scope; P70 release gates remain separate.

P60-T24's payment-view scope also passed controlled-wallet frontend and bilingual
mobile/desktop browser regressions for immediate network selection, retained failed
selections, unavailable balances, partial-switch recovery and departure races.
Frontend lint, generation and governance checks passed. This UI extension has local
acceptance only; the earlier live-payment conclusion does not extend to it.

### Prior feature acceptance

P60-T20 implements session-bound subscriptions, TTL 0 sends, volatile cursors,
indexed bounded matching, lock-loss cancellation, and removal of history/queues.
Local regressions cover encrypted payloads/VAPID, session ownership/revocation,
concurrent watch limits, delayed Token publication/replay, restart backlog
loss, single-sender behavior, snapshot closure, browser controls and CSV.
P40/P50/P70 share API, browser and production-topology acceptance. Browser
permission and notification tests include a real Chromium worker with the site
page closed, using a CDP-injected event; this is not vendor delivery evidence.

On 2026-10-24, the operator confirmed that an earlier manual test successfully
received a real browser notification after all site tabs were closed, and
explicitly accepted this result to close P60-T20. This operator-reported result
closes the outstanding browser-provider delivery gate separately from the local
automated evidence. The test date, deployed build digest, browser/OS versions
and exact permission state were not supplied with the confirmation. This update
records the operator's acceptance; it does not claim an independently rerun
test, native notification-click verification, or broader browser/OS coverage.

P60-T18/P60-T19's former durable delivery and read-watermark decision is replaced
by ADR-0051/P60-T20. Watch ownership and CSV acceptance remain preserved.

Retained completed milestones: P60-T02, P60-T03, P60-T05, P60-T09, P60-T11, P60-T12,
P60-T13, P60-T14, P60-T16, P60-T18, P60-T19, P60-T20.

SIWE challenge/session, replay, expiry, Cookie/Origin/CSRF, writer
authorization and administrative revocation tests passed with PostgreSQL
concurrency coverage. Generated contracts, bilingual wallet/account browser
flows, role-scoped deployment, security and monolith/split acceptance passed.
Scoped user API keys additionally passed schema/runtime, Hardhat/Foundry,
ordinary/race and aggregate gates. Reload and wallet-identity regressions remain
part of the maintained auth suite.

Protocol/hostile-transport and PostgreSQL 18 concurrency/race tests passed
for single credit, account-wide spend, reservation/release, replay, pending
settlement and reconciliation. Generated API, frontend/browser, security,
role-scoped deployment and `make check` passed at implementation closure.
Real local Anvil/Facilitator production-image tests passed SIWE, EIP-3009 and
Permit2 top-ups, two keys, concurrent debit and final chain/account equality
in monolith and split roles. This is local payment evidence only; the live
staging reconciliation gate remains blocked.

Removal of the accountless request-payment runtime and configuration passed
config rejection, migration/audit, billing, generation, deployment, and runtime
checks. Historical rows remain audit-only and top-up accounting is unchanged;
this completed cleanup does not close the live staging gate.

These are historical feature results, not tests rerun by this plan consolidation
or evidence that the live-payment release gate has passed.

### Preview x402 acceptance (P60-T21)

The record below predates the genesis-predeployment migration. Its initializer
reuse evidence describes that earlier implementation; the current gate checks
block-zero token code and Facilitator recreation without chain-state changes.
See [current Preview acceptance](../../preview/docs/testing.md#x402-billing-gate).

On 2026-10-28, the working tree based on `69dedc09` passed local macOS/Docker
Desktop acceptance with production image
`sha256:7da318c5cd1a29eccf9a3710a8888a87af6c0501b4bb84237f8a548b83a84356`.
After `make docker-build preview-x402-images`, `make check`, `make test-e2e`
(149 browser tests), `make test-rustfs`, `make test-runtime-e2e-prebuilt`,
`make test-preview-x402-prebuilt`, `make test-preview-metadata-prebuilt` and
`make test-preview-consensus-prebuilt` passed.

Preview's chain 48815 settled both methods: split-mode credit/debit/available
was 200/1/199; after all-role continuation it was 400/2/398, matching on-chain
recipient balances. Exact allowance consumption, replay rejection, initialization
reuse without chain-state changes, restart and spending during Facilitator outage
passed. Review regressions additionally paused the validator after payment
broadcast, retained the exact pending hash, resumed mining and reconciled one
credit without resending (final credit/debit/available 401/2/399). RPC/race tests
cover broadcast uncertainty, receipt timeouts/errors, hash mismatch, indexing and
confirmed reverts. The test-only Permit2 witness path and these local results do not establish
production Permit2/proxy compatibility, remote CI or P60-T17 live acceptance.

### Whole-token input and complexity pricing (P60-T22)

All 30 bounded V2 queries have explicit positive weights and cost reasons;
public prices and writer reservations derive from one validated base price.
Catalog completeness and missing-price rejection prevent implicit free queries.
Whole-token input and personal billing display use exact decimal conversion,
including precision/uint256 bounds and asset-aware historical formatting.

Local acceptance passed `make check`, `CI=true make test-e2e` (221 tests, no
retries), billing PostgreSQL integration/race, and production runtime/x402
all/split tests. Preview EIP-3009/Permit2, replay, exact allowance, restart/outage
and pending-hash reconciliation passed: split credit/debit/available was
200/2/198, all-role continuation 400/4/396, and final reconciled credit/debit
401/4. The local Anvil suite additionally verifies the 40-weight batch query
against the same shared writer balance. These results do not close P60-T17.

### Etherview operation namespace (P60-T23)

The namespace follows the fresh-database contract without legacy aliases or
backfills. Independent live-payment acceptance is recorded above under P60-T17.

The complete `make check`, `make test-integration-race`, `CI=true make test-e2e`
and `make test-runtime-e2e` gates passed, including local payment settlement,
concurrent priced debit, reservation release and operator bypass in both role layouts.
Shared revision/image identity and environment are recorded in
[P20-T32 acceptance](P20-enrichment.md#blob-access-and-archive-update).
Those local-only results did not close P60-T17; the separate real Sepolia
payment and API-debit reports above satisfy that independent gate.

### Multi-payment funding (P60-T25)

Production multi-option configuration, immutable conversion snapshots, unified
credit SQL/API, browser chain/token selection and persistent Preview Anvil are
complete. Both all/split layouts fund one account through four options and
reconcile actual token transfers with fixed-rate credits; replay, exact allowance,
Anvil recreation, payment-chain outage and unknown-settlement recovery passed.

Acceptance: `make check`, final generation/lint/docs/plan checks, PostgreSQL
integration and race, 935 Web unit tests, 226 browser E2E tests plus 12 final
billing-layout checks, schema/runtime E2E, Preview gateway, x402 and contract
E2E, and deterministic genesis/Anvil fixture checks all passed.

Acceptance is local-only by scope decision. This milestone used the former local
Permit2 fixture; these results make no production-contract compatibility claim.
No new live testnet gate is required for T25. Prior T17/T24 live evidence retains
its original scope.

### Standalone multi-chain facilitator (P60-T26)

The independent authenticated service supports keystore and AWS KMS signers,
standard EIP-3009/Permit2, PostgreSQL-owned signed transactions and singleton
execution with independent chain queues. Authenticated durable broadcast progress
and recoverable bilingual top-up steps preserve settlement and credit ownership.
Preview now uses one process for Geth
and Anvil; the custom Permit2 settlement path and old command are removed.

Local acceptance passed: signer/protocol race tests, PostgreSQL crash-boundary,
concurrent-chain and restart/replay tests, OpenAPI/generation/Go lint, schema and
full runtime E2E, Preview x402 in all/split layouts including chain outage and
paused-chain reconciliation, deployment renders, deterministic contract rebuilds,
Anvil persistence, license/dependency, docs and plan checks. Durable progress also
passed frontend/browser recovery regressions, PostgreSQL race checks and delayed-
mining Anvil all/split parity, including uncertain-response recovery.

P60-T27 remains deferred: supply the real KMS workload identity and key, funded
accounts on two EVM networks with approved token/proxy deployments, and independent
RPC/writer observations. Close it only after real signing, both payment methods,
unique account credit and priced API debit have matching reconciliation evidence.
Mock KMS and local chains do not satisfy that acceptance.
