# ADR-0017: Sourcify Interoperability Boundary

Status: accepted

## Context

Sourcify v2 provides asynchronous Standard JSON verification and detailed
contract lookup. Its contract record is address-scoped rather than bound to
Etherview's exact canonical block hash, and its sources, bytecode, status, and
errors are external input. Importing that record directly into published local
verification state would bypass the durable request, compiler provenance,
exact-code observation, and canonical completion rules. Conversely, submitting
source code grants Sourcify a broad publication licence, so a stored request
flag alone must never cause an unrelated lookup or import to upload sources.

The authoritative upstream contract is the Sourcify v2 OpenAPI 2.1.0 document:
<https://sourcify.dev/server/api-docs/swagger.json>.

## Decision

- The Sourcify client uses only the v2 contract lookup, Standard JSON submit,
  and verification-job status endpoints. Its production base URL is absolute
  HTTPS without credentials, query, or fragment. Requests bypass environment
  proxies, reject redirects, reject DNS answers containing private or
  special-purpose addresses, and bound request and response bytes. HTTP and
  private-network access are package-private test hooks only.
- Upstream status codes and JSON are mapped to stable local errors. Raw response
  bodies, URLs, resolver errors, compiler diagnostics, and Sourcify messages do
  not cross logs or caller-facing error boundaries. Lookup and job responses
  must have bounded, well-formed identities and current v2 shapes.
- A lookup is not local publication evidence. The authenticated import request
  supplies only an address and optional constructor-argument suffix. The API
  resolves the configured chain's newest canonical code hash, block hash,
  runtime bytecode, and creation input from PostgreSQL, proves and removes the
  exact constructor suffix, then gives that immutable target to the adapter.
  The returned Sourcify chain/address and on-chain runtime bytecode must match
  it. Sourcify's on-chain creation bytecode includes constructor arguments and
  is therefore not allowed to replace or veto the server-derived compiler
  creation program after that suffix has been removed. The imported Standard
  JSON, language, compiler version, and fully qualified contract name become a
  normal local verification request, which is compiled, matched, lease-fenced,
  and canonicality-checked through the existing durable service. Import always
  clears `submit_to_sourcify`.
- Upload accepts a durable local job ID rather than an in-memory request. It
  re-reads that job, verifies its persisted request digest and coherent state,
  and requires both its `submit_to_sourcify=true` value and an explicit consent
  argument on the submit call. Lookup, import, local compilation, retries, and
  status polling never infer that consent. The outbound payload contains only
  the validated Standard JSON, compiler version, and contract identifier
  required by Sourcify v2.
- Authenticated lookup, import, upload, and external-status routes remain
  present when the feature is disabled and return the native typed unavailable
  envelope. Upload returns the validated external ticket; it is never treated
  as local completion evidence.

## Consequences

Sourcify can seed a reproducible local verification without becoming a trust
root for canonical chain state or bypassing local compiler provenance. Users
must opt in separately to the irreversible external source publication side
effect. Remote availability and errors remain optional capabilities and cannot
make local verification or core ingestion incorrect.
