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

`internal/userauth` remains the only browser user and administrator authority.
API keys authenticate native and compatibility API clients and never grant a
user or administrator role. ADR-0035 adds optional user ownership and scoped
self-service issuance without allowing API keys to cross this session boundary.

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

Browser verification has a separate session-authenticated boundary at
`/api/v1/verification-jobs/*`, with exact Origin and CSRF checks on writes.
These endpoints never accept an API key as user identity, and existing native
and Etherscan verification endpoints never accept a Cookie in place of a key.
The status projection and shared execution rules are defined in
[ADR-0024](ADR-0024-verifier-v2-workflow.md#session-authenticated-verification).

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

## Explicit payment-chain scope

SIWE and user/API-key ownership remain bound to the configured explorer chain.
An explicit payment-network selection, user-initiated top-up or balance query may temporarily switch
the same injected provider and account to the payment network returned by the
same-origin billing configuration.
Only expected transitions between configured payment networks within the payment
view scope and its return to the original site chain preserve authentication identity. Wallet operation revisions still
advance, so outstanding operations cannot survive a chain ABA transition.
Redundant observations of the same selected account or current chain do not
create a new identity transition, including delayed restoration acknowledgements. A real chain/account ABA still
invalidates the scope on its first unexpected transition.
Account/provider changes, disconnects and unexpected chain events invalidate
the payment scope and retain normal session revocation. Concurrent payment scopes
are rejected.

Balance reads, exact approvals, receipt polling and EIP-712 signing inside the
scope bind the payment chain. SIWE and unrelated wallet work still require the
site chain. The payment view retains the scope across balance reads and payment outcomes,
and attempts restoration when leaving for history, prices or another route.
Selection immediately requests wallet switching; failure retains the selected
network/token, hides its balance and stops the operation. A later explicit
refresh or payment retries entry; neither passive refresh nor initial rendering
switches networks. Partial entry failure attempts to restore the preceding actual
network without reverting the selection. Closing during a pending request waits
for that request and restores without starting further payment work. Failed
restoration disconnects the local wallet/authentication state without retrying or
misrepresenting a possibly settled payment. Reload does not restore a scope.
Server sessions, CSRF and ledger identities are unchanged, and balances are never
copied between site-chain identities.

## Login chain preparation

An explicit login action first connects the selected provider and prepares the
configured site chain before requesting a challenge. Only an unknown-chain
provider error (4902) permits adding the chain from validated public configuration;
missing or mismatched metadata fails closed. Adding a chain does not prove it
became selected: the client reads the chain again and switches if necessary.

Preparation fences the provider, account and wallet revision. Only the expected
transition to the site chain is accepted, including a missing or delayed event;
account changes, disconnects, unexpected chains and ABA transitions cancel the
attempt. Actual chain and account are checked before challenge creation. Rejection
or failure stops login and permits an explicit retry. Successful login leaves the
wallet on the site chain. This does not preserve a previously authenticated
identity across an unrelated chain change or modify the payment scope.
