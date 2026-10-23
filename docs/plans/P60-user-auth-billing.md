# P60 — User Accounts, Billing & Personal Tools

Status: `blocked`

## Outcome

Etherview supports revocable, writer-authoritative EOA user sessions created
from server-issued EIP-4361 messages and bounded EIP-1193 `personal_sign`
requests. Users and administrators remain separate from API-key identity and
quota policy.

Users fund permanent token-denominated API credit through one Account-page
x402 top-up. User-owned API keys spend that credit on configured bounded
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

| ID      | Status  | Depends on                          | Deliverable                                                                                                                                                        | Verification                                                                                                             |
| ------- | ------- | ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| P60-T02 | done    | P40, P50                            | SIWE challenge/session/profile persistence, typed schema/queries, token/CSRF core and public contracts                                                             | generation, migration, unit and PostgreSQL concurrency tests                                                             |
| P60-T03 | done    | P60-T02                             | Cookie/Origin/CSRF-protected HTTP authorization and administrator role/status/session-revocation CLI                                                               | handler/replay/expiry, authorization, CLI and PostgreSQL integration tests                                               |
| P60-T05 | done    | P60-T03                             | Bounded wallet signing, `AuthProvider`, account page, admin users, and bilingual UX                                                                                | Vitest, boundary, a11y, and browser tests                                                                                |
| P60-T09 | done    | P40, P50, P60-T03, P60-T05          | Scoped, user-owned API keys and a tabbed `/account` management workspace                                                                                           | PostgreSQL, browser, schema/runtime, Hardhat, Foundry, security, race, and aggregate gates pass                          |
| P60-T11 | done    | P60-T03, P60-T09                    | Prepaid accounts, append-only ledger, top-up intents and usage reservations with ADR/API/config/catalog contracts and typed persistence                            | generation/config, migration, PostgreSQL concurrency and race tests                                                      |
| P60-T12 | done    | P60-T11, P60-T13                    | x402 v2 EIP-3009/Permit2 top-ups, replay identity, pending settlement/reconciliation, and disposable Anvil/Facilitator production parity                           | hostile protocol/transport, real local chain/ledger/usage/replay/outage, monolith/split tests; live gate remains P60-T17 |
| P60-T13 | done    | P60-T11                             | `/v2/api` user-key reservation/debit gate, operator bypass, canonical resources, bounded capture, and removal of accountless request-payment runtime/configuration | compatibility, quota/failure/race, config, migration/audit, generation, deployment, and runtime tests                    |
| P60-T14 | done    | P60-T12                             | Account balance/top-up/usage APIs and constrained browser wallet top-up flow                                                                                       | generated client, Vitest, Playwright, and accessibility                                                                  |
| P60-T16 | done    | P60-T03, P60-T05, P60-T13, P60-T14  | Auth/billing metrics, alerts, role-scoped Secret/Helm/Compose deployment, runbooks, administrator adjustment and reconciliation/rollback controls                  | deployment, security/race, browser/E2E, operations, common and role-parity gates                                         |
| P60-T17 | blocked | P60-T16                             | One-shot live EIP-3009 and Permit2 top-up plus API-debit conformance                                                                                               | testnet transactions and writer/chain/credit/usage reports                                                               |
| P60-T18 | done    | P60-T03, P60-T09, P10, P20, P40-T01 | Private writer-authoritative watch management, bounded enrollment, fresh schema/typed queries, and ownership isolation                                             | OpenAPI/generation, schema, Cookie/Origin/CSRF, handler, watch-limit, and PostgreSQL regressions                         |
| P60-T19 | done    | P60-T18, P20-T10                    | Durable transaction/token notifications, generation-fenced delivery, reorg/reattach handling, read watermarks, and bounded cleanup                                 | restart, lease/generation, concurrency, indexed matching, disabled-account replay, and PostgreSQL regressions            |
| P60-T20 | blocked | P60-T18, P60-T19                    | Replace durable notifications with session-bound, best-effort Web Push; five watches/account; align P40 API, P50 browser and P70 topology acceptance               | Local implementation and automated acceptance; operator Preview closed-page provider delivery pending                    |

## Acceptance

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
- [x] Only configured bounded `/v2/api` logical successes commit a debit.
- [x] Native explorer reads never prompt for or accept x402 payment.
- [x] Reservation, crash, pending-settlement, and reconciliation behavior is durable.
- [x] Account UI exposes only the constrained top-up signing and exact approval flow.
- [x] Monolith and split roles share identical writer and payment semantics.
- [ ] Live EIP-3009 and Permit2 staging reports reconcile settlement, account
      credit, API debit and independent writer/chain observations (P60-T17).
- [x] Superseded accountless request-payment execution and configuration are
      removed; immutable historical audit rows remain readable and cannot create
      prepaid credit or change top-up accounting.
- [x] Private watches are limited to 5 addresses/user and start after enrollment
      or re-enablement at an exact canonical tip. Writer SIWE ownership, Origin,
      and CSRF checks isolate users; API keys cannot manage private tools.
- [ ] P60-T20 replaces durable notification history and queues with session-bound
      Web Push, TTL 0, no retries/backfill, bounded matching and all/split parity.
      P40 subscription API, P50 browser controls/worker and P70 runtime acceptance
      share this migration. Real provider delivery is separate from local evidence.

## Current Blockers

P60-T20 awaits the operator's real browser-provider test at
`https://etherview.localhost:8080`. Local receivers and CDP-delivered push events
do not close it. Clear it with the deployed build, browser/OS and permission
state, and an observed notification after all site tabs close. Preview now serves
the packaged worker and Chrome subscription registration survives a page reload;
closed-page provider delivery remains unverified.

P60-T17 requires operator-provided testnet funds, payer credentials, a compatible
staging facilitator and deployed Account top-up surface, matching writer database,
independent RPC endpoint, and
the deployed image/build digest. Local Anvil evidence cannot close it.

The blocker clears only after separately preserved EIP-3009 and Permit2 reports
reconcile SIWE, top-up settlement, account credit, API-key debit, and independent
writer/chain observations against that deployed build. P70 retains the separate
reference-capacity and final release gates.

## Evidence

P60-T20 implements session-bound subscriptions, TTL 0 sends, volatile cursors,
indexed bounded matching, lock-loss cancellation, and removal of history/queues.
Local regressions cover encrypted payloads/VAPID, session ownership/revocation,
concurrent watch limits, delayed Token publication/replay, restart backlog
loss, single-sender behavior, snapshot closure, browser controls and CSV.
P40/P50/P70 share API, browser and production-topology acceptance. Browser
permission and notification tests include a real Chromium worker with the site
page closed, using a CDP-injected event; this is not vendor delivery evidence.

P60-T18/P60-T19's former durable delivery and read-watermark decision is replaced
by ADR-0051/P60-T20. Watch ownership and CSV acceptance remain preserved.

Retained completed milestones: P60-T02, P60-T03, P60-T05, P60-T09, P60-T11, P60-T12,
P60-T13, P60-T14, P60-T16, P60-T18, P60-T19.

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
