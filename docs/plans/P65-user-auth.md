# P65 — User Authentication & Management

Status: `done`

## Outcome

Etherview supports revocable, writer-authoritative EOA user sessions created
from server-issued EIP-4361 messages and bounded EIP-1193 `personal_sign`
requests. Users and administrators remain separate from API-key identity and
quota policy.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0020](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0035](../decisions/ADR-0035-user-owned-scoped-api-keys.md)
- [EIP-4361](https://eips.ethereum.org/EIPS/eip-4361)
- [EIP-1193](https://eips.ethereum.org/EIPS/eip-1193)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P65-T01 | done | P40, P50 | ADR, OpenAPI contract, database migration, and sqlc queries | `make generate-check plan-check`, migration tests |
| P65-T02 | done | P65-T01 | `internal/userauth` SIWE, token/CSRF, challenge, session, and PostgreSQL repository | unit and PostgreSQL concurrency tests |
| P65-T03 | done | P65-T02 | Auth/session/profile/admin HTTP boundary with Cookie, Origin, CSRF, and writer authorization | handler, replay, expiry, and authorization tests |
| P65-T04 | done | P65-T02 | `etherview admin user` role, status, and session-revocation operations | CLI and PostgreSQL integration tests |
| P65-T05 | done | P65-T03 | Bounded wallet signing, `AuthProvider`, account page, admin users, and bilingual UX | Vitest, boundary, a11y, and browser tests |
| P65-T06 | done | P65-T03, P65-T04, P65-T05 | Config, Secret/Helm/Compose, operations docs, E2E, and security closure | race, E2E, security, Helm, and role-parity gates |
| P65-T09 | done | P40, P50, P65-T03, P65-T05 | Scoped, user-owned API keys and a tabbed `/account` management workspace | PostgreSQL, browser, schema/runtime, Hardhat, Foundry, security, race, and aggregate gates pass |

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

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P65-T01, P65-T02, P65-T03, P65-T04, P65-T05, P65-T06,
P65-T09.

SIWE challenge/session, replay, expiry, Cookie/Origin/CSRF, writer
authorization and administrative revocation tests passed with PostgreSQL
concurrency coverage. Generated contracts, bilingual wallet/account browser
flows, role-scoped deployment, security and monolith/split acceptance passed.
Scoped user API keys additionally passed schema/runtime, Hardhat/Foundry,
ordinary/race and aggregate gates. Reload and wallet-identity regressions remain
part of the maintained auth suite.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P65-user-auth.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
