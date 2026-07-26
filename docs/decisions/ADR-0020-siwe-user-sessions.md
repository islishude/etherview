# ADR-0020: SIWE User Sessions with Writer Authority

Status: accepted

## Context

Etherview needs a user identity for profile state, administrative access, and
optional payment history without introducing passwords, OAuth, bearer JWTs, or
a second authority beside PostgreSQL. The existing API-key boundary identifies
operator-issued API clients and applies quotas; it does not prove control of a
wallet or establish a browser user account.

An injected provider is hostile input. A signed message is useful only when the
server authors and later verifies every origin, chain, address, nonce, and time
field. Browser sessions also need immediate revocation across API replicas,
which rules out self-contained client-side session state.

## Decision

`internal/userauth` is independent of `internal/auth`. API keys continue to
authenticate native and compatibility API clients and never grant a user or
administrator role.

The server constructs one canonical EIP-4361 message for a checksummed EOA
address. It binds the normalized `server.public_url` authority and root URI,
the configured chain ID, a cryptographically random alphanumeric nonce, the
challenge UUID, issued-at time, and a short expiration. The client submits only
the challenge UUID and a bounded 65-byte signature; it cannot replace the
stored message. Verification uses EIP-191 EOA recovery only. ERC-1271 and
EIP-6492 are not accepted.

One writer transaction conditionally consumes the still-active challenge,
creates an ordinary user when the address is new, checks the current user
status, and creates the session. Existing role and status are never overwritten
by login. Concurrent verification of one challenge can create at most one
session. A valid challenge for a disabled user is consumed without creating a
session.

Session and CSRF values are domain-separated HMAC constructions using one
server-only pepper. PostgreSQL stores only fixed-size digests. The opaque
session token is carried in the `etherview_session` HttpOnly, SameSite=Lax
Cookie scoped to `/api/v1`; HTTPS deployments set Secure and no deployment sets
a Domain attribute. Sessions have an absolute lifetime and no sliding renewal.
Changing the pepper intentionally invalidates every existing session.

Every authenticated operation joins the session digest to the current user on
the writer. There is no JWT, process cache, or reader-pool authorization path.
Logout, administrator revocation, status, and role changes are therefore
visible to the next request on every API replica. `last_used_at` is updated at
most once per bounded interval to avoid a write for every read.

All authentication POST and PATCH requests require a single `Origin` exactly
matching the normalized public origin. An authenticated write also requires a
single session-derived `X-CSRF-Token` compared in constant time. The general
CORS allowlist, Host, and forwarded host headers are never authentication
authority. Authentication responses are `no-store`.

The embedded wallet boundary exposes only `signSIWEChallenge`, which accepts
the generated `AuthChallenge` instead of arbitrary message text. Before
encoding anything, it requires the server's exact canonical, statement-free
EIP-4361 layout and binds its scheme, authority, and URI to the current browser
origin; its chain to the public configuration; its EIP-55 address to the
selected account; and its request ID and expiration to the challenge envelope.
Only then may it invoke `personal_sign` with the exact UTF-8 hexadecimal message
and selected account. The provider remains private, and account, configured
chain, provider identity, and session revision are checked before and after
signing. Provider errors map to stable local codes and never reach the DOM.

The first administrator first proves wallet ownership through ordinary login,
then an operator promotes that existing user through the writer-backed CLI.
Public registration never creates an administrator.

## Consequences

- User sessions require PostgreSQL and are identical in monolith and split API
  deployments.
- Only `api` and `all` processes require the session pepper.
- A configured reader cannot authorize a stale session or role.
- Wallet connection and server authentication are separate UI states, but a
  wallet account, chain, provider, or session-revision change forces
  reauthentication.
- Adding contract-wallet authentication, cross-origin login, or federated
  identity requires a new decision.
