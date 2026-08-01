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
| P65-T07 | done | P65-T05 | Direct SIWE login with inline injected-wallet selection and no separate preconnection step | Vitest, boundary, a11y, and embedded browser tests |
| P65-T08 | done | P65-T03, P65-T05 | Preserve a valid writer-backed SIWE session across an SPA reload while retaining wallet-identity revocation after an observed connection | Vitest, embedded browser reload regression, and live Preview verification |

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

## Current Blockers

None. P70 retains the repository-wide legal, dependency supply-chain,
conformance, release-CI, artifact, and long-soak review; P65 completion does
not promote those release gates.

## Evidence

- P65-T01 governance and contract: ADR-0020 establishes writer-authoritative
  SIWE sessions and ADR-0003 plus `AGENTS.md` now include only the bounded
  SIWE `personal_sign` extension. The root and release plans include P65/P66
  without promoting P70.
- P65-T01 persistence and generation: migration `0023_user_auth.sql` creates
  the three bounded tables and indexes; `user_auth.sql` generates the challenge,
  user, session, administration, and cleanup queries through sqlc. The native
  OpenAPI defines all eight operations, Cookie/CSRF security, generated Go
  models, and generated TypeScript paths.
- P65-T01 verification: `make plan-check` passes with 10 plans, 68 work items,
  and 63 checked local links. `go test ./internal/api ./internal/db
  ./internal/store -count=1` and `make generate-check` pass.
- P65-T02 implementation: `internal/userauth` uses the pinned `siwe-go v1.0.0`
  `VerifyWith` EOA-only path, domain-separated HMAC token/CSRF material, a
  server-authored exact SIWE message, and writer transactions for conditional
  challenge consumption, user state, login, session creation, revocation, and
  administration. No contract-verifier or RPC fallback is installed.
- P65-T02 verification: normal, wrong-binding, expiration, replay, high-S,
  invalid-v, zero-value CSRF, concurrent consumption, and concurrent disable
  regressions pass under `go test ./internal/userauth -count=1` and
  `go test -race ./internal/userauth -count=1`. Tagged PostgreSQL tests compile
  and skip without `ETHERVIEW_TEST_DATABASE_URL`; scoped vet, lint,
  govulncheck, gitleaks, module verification, and diff checks pass apart from
  the separately recorded license classification.
- P65-T03 implementation: the native HTTP boundary uses only the session
  Cookie and writer-backed user service, enforces the exact configured Origin
  before every auth write, applies single-value constant-time CSRF validation
  to authenticated writes, emits fixed-path secure Cookie attributes, and
  keeps session inspection anonymous on invalid or expired credentials.
  Profile and administrator operations retain typed authorization errors and
  chain-bound opaque user cursors; `serve` constructs both monolith and split
  API capabilities from the writer pool.
- P65-T03 verification: `go test ./internal/httpapi ./internal/userauth
  ./internal/app ./internal/cli -count=1` passes, including exact Origin,
  duplicate JSON/header, Cookie clearing, CSRF, nullable profile, ordinary
  user denial, pagination, and redacted error regressions.
- P65-T04 implementation: `etherview admin user set-role`, `set-status`, and
  `revoke-sessions` resolve an existing chain-scoped wallet user through the
  writer repository. Disabling a user atomically revokes active sessions;
  re-enabling never revives them, and bounded JSON output exposes no session
  material.
- P65-T04 verification: normal and race runs of `go test ./internal/cli
  ./internal/app -count=1`, scoped `go vet`, and scoped `golangci-lint` pass.
  The integration-tagged cross-chain, idempotency, and revocation scenarios
  compile and skip only because `ETHERVIEW_TEST_DATABASE_URL` is unset.
- P65-T05 implementation: the only new wallet RPC is a bounded SIWE
  `personal_sign` capability over the exact server-authored UTF-8 message and
  selected address, with provider, account, chain, and wallet-session revision
  checks before and after signing. `AuthProvider` retains CSRF only in memory,
  validates every hostile API response, clears authority across wallet
  identity changes, and best-effort revokes any stale server Cookie. The
  bilingual account, profile, and administrator views retain generated-client,
  opaque-cursor, responsive, and accessible boundaries.
- P65-T05 verification: `npm --prefix web run lint`, all 14 Vitest files and
  100 tests, and `git diff --check -- web` pass. A Linux Playwright v1.61.1
  Chromium container executed all eight browser tests against the temporary
  Go binary serving the built `go:embed` distribution; the SIWE flow verifies
  exact `personal_sign`, Origin and CSRF headers, memory-only CSRF, profile and
  administrator mutations, pagination, bilingual narrow layout, axe checks,
  and wallet-identity logout.
- P65-T05 final wallet-boundary review: `WalletContext` no longer exposes a
  general message-signing function. Its `signSIWEChallenge` capability accepts
  the generated challenge envelope and rejects any message whose canonical
  scheme, authority, URI, configured chain, EIP-55 account, request ID, or
  expiration differs from the live browser/session binding before
  `personal_sign` can run.
- P65-T05 review verification: `npm --prefix web run lint`,
  `npm --prefix web test` (17 files and 116 tests), and
  `npm --prefix web run build` pass. The embedded Chromium SIWE fixture now
  supplies and asserts the complete canonical EIP-4361 message rather than a
  generic signing string; its focused flow also passes in the Playwright
  v1.61.1 Linux container against a freshly built Go `go:embed` harness. The
  rebuilt full suite passes 8/8 and proves an account ABA during a pending
  write keeps the transaction-outcome-unknown warning visible across the new
  wallet revision without hiding later independent wallet alerts.
- P65-T06 configuration and deployment: auth remains off by default; strict
  YAML rejects Secret-only session/billing fields without echoing values, and
  a final `serve --roles` override is applied before role-scoped Secret files
  are read. Compose and Helm inject the independent session pepper only into
  enabled `all`/`api` containers, reject malformed public origins and inline
  peppers, and leave migrations, init containers, and worker roles clean.
- P65-T06 operational and security closure: the runbook documents first-admin
  promotion, writer-only role/status/revocation, disable/re-enable semantics,
  pepper rotation, and rollback. Explicit regressions prove API keys cannot
  authorize user/admin routes and a changed pepper invalidates old sessions.
  The focused security gate includes config, CLI, app, HTTP, SIWE, billing,
  and strict-JSON boundaries.
- P65-T06 production housekeeping: an enabled authentication feature registers
  one maintenance-role, writer-only supervisor component that immediately and
  periodically removes bounded batches of expired challenges and expired or
  revoked sessions. Chain-scoped `SKIP LOCKED` candidates allow split
  maintenance replicas to make progress without crossing another chain.
  Feature-off registers nothing; transient failures retain readiness and log
  only `user_auth_cleanup_failed`. `admin user` forces the same non-API
  configuration boundary as billing administration and therefore never opens
  session, fingerprint, or facilitator-header Secret files.
- P65-T06 dependency and image closure: the go-ethereum scanner exception is
  fixed to v1.17.2, its exact non-`cmd` dependency/license attribution and
  upstream file hashes, and rejects any GPL `cmd/` import. The production image
  carries the LGPL, keccak/secp256k1/metrics BSD, bundled libsecp256k1 MIT, and
  third-party notice files; `make license-check` and
  `make docker-build docker-image-check` pass.
- P65-T06 verification: normal and race tests for config, CLI, app, HTTP, and
  userauth pass. PostgreSQL 18 executed migrations through `0024` and the full
  `make test-integration`, including dual-repository session, role, and
  revocation visibility. `make compose-check`, `make helm-check`,
  `make security-check`, and the rebuilt embedded Chromium suite (8/8) pass.
  Focused app/CLI supervisor, feature graph, bounded retry, cancellation,
  redacted failure, and minimal-role Secret-loading regressions pass under
  normal and race execution. PostgreSQL race coverage proves cleanup leaves a
  second chain's challenge and session untouched.
- P65-T07 implementation: the account page and wallet menu share one direct
  SIWE control. A sole discovered provider is authorized immediately; multiple
  providers are selected inline. The wallet boundary returns only a frozen
  public identity snapshot, fences SIWE against that exact provider, account,
  chain, and revision, and never exposes the raw provider. `AuthProvider`
  permits only the login-owned disconnected-to-connected transition, rejects a
  wrong chain before challenge creation, and preserves existing post-connect
  identity invalidation and stale-Cookie cleanup.
- P65-T07 verification: `npm --prefix web run lint`,
  `npm --prefix web test` (20 files and 155 tests),
  `npm --prefix web run build`, `make test`, and `make generate-check` pass.
  The embedded Chromium suite passes 9/9 with the SIWE scenario starting from
  a disconnected wallet and asserting the complete connect, chain/account
  preflight, exact `personal_sign`, and completion-fence sequence.
- P65-T08 implementation: `AuthProvider` no longer interprets the deliberately
  memory-only wallet's initial disconnected state after a full page load as an
  observed identity mismatch. A valid writer-backed Cookie session restores
  independently; the first later wallet connection is retained only for the
  same account and chain, while mismatches and subsequent provider, account,
  chain, or revision changes still clear local authority and revoke the server
  session.
- P65-T08 verification: the focused AuthProvider suite passes 27/27 and the
  complete frontend suite passes 21 files and 157 tests. Frontend lint/build,
  `make generate-check`, `make plan-check`, and the embedded Chromium suite
  (9/9) pass; its SIWE scenario now covers login, full reload, matching-wallet
  reconnection, and later account-change revocation. The rebuilt Preview uses
  one production image across all six application roles with zero restarts,
  and `make preview-check` passes the complete topology and 15-second stability
  window. Live Preview recorded `verify` 201 followed by repeated restored
  `auth/session` 200 requests without `auth/logout`; PostgreSQL retained one
  active, unrevoked session.
