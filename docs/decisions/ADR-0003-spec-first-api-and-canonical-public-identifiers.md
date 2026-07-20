# ADR-0003: Spec-first API and canonical public identifiers

Status: accepted

## Context

The native API, embedded SPA, Etherscan compatibility layer, operator labels,
and optional data sources all expose identifiers and capability state. If each
surface invents its own JSON shape, numeric encoding, or label key, clients can
silently lose precision and searches can return ambiguous or stale objects.
Optional features also must not make an unavailable upstream look like an
authoritative empty result.

## Decision

- `api/openapi.yaml` is the source of truth for native `/api/v1` HTTP models.
  Go and TypeScript contracts are generated from it and checked for drift.
- The embedded SPA parameterizes its only explorer HTTP client with the
  generated TypeScript `paths` interface. A single adapter fixes that client to
  the same-origin `/api/v1` prefix and is the only production SPA module that
  may call browser `fetch`; wallet RPC remains a separate injected-provider
  boundary.
- Successful native responses use `{data,meta}`. Errors use
  `{error:{code,message,details,request_id}}`.
- Quantities that can exceed JavaScript precision are decimal strings;
  addresses are checksummed at the response boundary; hashes are normalized
  lowercase hexadecimal.
- List cursors are opaque to clients and bind enough immutable identity to
  reject malformed or stale traversal state. Cursor inputs and emitted
  `meta.next_cursor` values share the bounded `OpaqueCursor` schema; clients
  must not decode or construct them.
- Contract tests inspect the raw OpenAPI YAML for duplicate mapping keys and
  enforce the native success envelope, error envelope, decimal quantity, public
  identifier, and opaque-cursor primitives. Both generated Go and TypeScript
  contracts remain drift-checked by the Makefile.
- Persisted operator-label and search keys are canonical Ethereum identities:
  20-byte addresses, 32-byte transaction/block hashes, or canonical decimal
  block heights. Display labels remain separate untrusted text.
- An enabled optional capability returns a machine-readable unavailable state
  when its authoritative source has no fresh observation. A successful empty
  collection is reserved for a fresh observation with no matching records.
- `/v2/api` keeps Etherscan-compatible envelopes at its compatibility boundary;
  it must still report missing trace, archive, price, or verification ability
  explicitly rather than fabricating empty success.
- Address-only `/v2/api` ABI and source lookups first resolve the latest
  canonical contract-code observation and select a verification record by
  chain, address, that code hash, and its validity at the canonical tip. A
  missing current-code observation is unavailable, while a known current code
  without a matching record is unverified; an arbitrary open verification
  interval is never a fallback.
- Public source-verification submission is exposed only when
  `security.public_verification` is enabled and requires an API key. The
  compatibility boundary accepts source and source-status requests by POST,
  binds every job to the latest canonical code observation plus a canonical
  top-level or traced creation input, and recomputes the runtime code hash
  before enqueueing. Client input cannot choose the chain, address binding,
  runtime bytecode, block hash, or Sourcify consent stored in the durable job.
  Constructor arguments and license type are persisted as publication
  metadata, not injected into the compiler's Standard JSON settings.
- Every native or compatibility verification request must carry a non-empty
  runtime bytecode whose Keccak-256 hash equals its code hash. Before publishing
  a successful result, the repository rechecks in the completion transaction
  that chain, address, code hash, and block hash identify one canonical
  `contract_code_observations` row joined to the canonical mapping at the same
  height. A syntactically valid client-supplied identity is not publication
  authority.
- Proxy-verification submission and status remain explicitly unavailable until
  production proxy observations and a durable, GUID-addressable proxy job
  lifecycle exist; the compatibility layer never fabricates proxy success.
- Native `/api/v1` authentication accepts `X-API-Key` only. Legacy `apikey`
  query parameters and URL-encoded POST form fields are parsed solely on the
  exact `/v2/api` route. POST inspection has the same configured body bound as
  verification input and restores the original bytes for the handler;
  conflicting header, query, or form credentials are rejected without
  echoing credential material.
- Authentication and rate-limit middleware preserve the selected API boundary
  even when they reject before routing: native errors retain
  `{error:{code,message,request_id}}`, while `/v2/api` retains the
  Etherscan-compatible `{status,message,result}` envelope.
- API keys are created, rotated, listed, and revoked only through the operator
  CLI. Create and rotate reveal a plaintext token once; persistence contains
  only its HMAC-SHA-256 digest. Rotation preserves the existing name and quota
  policy and atomically inserts one replacement while revoking the locked old
  row, so concurrent rotations cannot leave two active successors or revoke a
  key without a durable replacement.

## Consequences

Public API changes start with the OpenAPI specification and this decision must
be revisited when an identifier or envelope contract changes. Handler tests
cover malformed identifiers, unavailable capabilities, string quantities, and
cursor validation. SPA boundary tests reject ungenerated backend calls,
server-environment injection, and wallet RPC outside the provider module.
Operator CLI validation prevents new non-canonical label
keys, while query readers defensively reject malformed historical rows.
