# ADR-0035: User-owned Scoped API Keys

Status: accepted

## Context

Etherview's original API keys were operator-issued quota identities without a
user owner or authorization scope. The embedded account workspace now needs
self-service credentials without allowing an API key to become a browser user,
administrator, or Cookie-session substitute.

## Decision

Self-service issuance is an explicit `features.user_api_keys` capability. It
requires writer-authoritative user authentication and the independent API-key
pepper. The feature is disabled by default. Its non-secret policy fixes the
per-key rate, burst, and per-user active-key maximum for one deployment.

An API key has an optional user owner and one canonical non-empty scope set.
Operator keys have no owner. `api:read` authorizes keyed read quota, NFT media,
and configured `api_key_or_x402` bypass. `contract:verify` authorizes native and
Etherscan verification submission, status, and the ABI/source probes needed by
Hardhat and Foundry. A supplied valid key with the wrong scope fails with a
stable forbidden response and never falls back to anonymous or paid access.

Only an active Cookie-authenticated user may list, create, rotate, or revoke
their own keys. Writes require exact same-origin and the session-bound CSRF
token. Cross-owner prefixes are indistinguishable from missing prefixes. API
keys never authorize account or administrator routes.

Creation locks the active user row and checks the configured active-key maximum
in the same writer transaction as insertion. Rotation atomically stores a new
digest and revokes the old key while preserving owner, name, scopes, and quota.
Revocation is idempotent. Disabling a user permanently revokes all their keys
in the same transaction as the user status change.

PostgreSQL stores only the keyed digest. Create and rotation responses expose
the plaintext token once and are `no-store`; list responses never contain it.
All owner, status, scope, quota, and mutation decisions use the writer, never a
read pool or process cache. Existing operator keys receive both scopes during
migration, and the CLI remains the deployment-wide administration boundary.

## Consequences

- The browser route remains `/account`; `/users/me/api-keys` is only a native
  API resource namespace.
- Monolith and split API roles use the same PostgreSQL transactions and secrets.
- Scope additions or changes to payment-bypass meaning require an ADR update
  and a complete native/Etherscan/billing authorization-matrix regression.
