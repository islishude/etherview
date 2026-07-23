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
- Injected-wallet discovery uses the EIP-6963 request/announce event loop.
  Provider metadata is bounded and validated as UUIDv4, data-image URI, and
  reverse-DNS identity; the first valid announcement owns a UUID for the page
  lifetime. The raw EIP-1193 provider never leaves the wallet boundary: UI
  consumers receive only bounded display metadata, the selected account, and
  normalized chain ID.
- The wallet boundary has a closed RPC allowlist: `eth_requestAccounts`,
  `eth_accounts`, `eth_chainId`, `eth_call`, and `eth_sendTransaction`.
  Every contract operation rechecks the selected account and configured chain,
  binds `from` and `chainId` in its call object, bounds calldata/value/results,
  and rejects malformed provider responses without trusting provider-owned
  array methods or getters. Account, chain, and disconnect events are
  provider-identity and session-revision fenced and fail closed, including ABA
  transitions. Once a transaction request reaches the provider, an invalid
  hash or changed completion session is an unknown outcome that must be checked
  in the wallet before retrying. Provider error messages and data never reach
  the DOM; stable local codes select bilingual text.
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
- `/v2/api` is an explicit allowlist rather than an upstream proxy. Its complete
  module/action, method, parameter, authentication, capability, and intentional
  wire-difference contract is maintained in the
  [Etherscan V2 compatibility matrix](../architecture/etherscan-v2-compatibility.md).
  A compatibility action is not registered unless the handler allowlist,
  production backend dispatch, matrix, and golden envelope/method inventory all
  agree.
- A Core-backed compatibility list proves continuous durable Core coverage for
  its tip-clamped canonical block range before an empty result is authoritative.
  A range wholly above the canonical tip has no records; an absent, gapped, or
  non-covering Core range is an explicit unavailable capability. Trace- and
  Token-backed lists first pass that Core proof and only then distinguish an
  incomplete enrichment stage from a genuinely empty published range. Block
  countdown cadence is sampled only inside the single coverage interval that
  contains the canonical tip; sample heights must be continuous, and a
  one-block interval is estimate-unavailable rather than permission to bridge
  an older coverage island.
- Compatibility wire models are action-specific. Account, token, block, and
  statistics quantities remain decimal strings, while `logs.getLogs` uses
  lowercase RPC-style hexadecimal quantities. `contract.getsourcecode`
  includes `CompilerType` and `ContractFileName`; its `MatchKind` field is an
  explicit Etherview extension. Mined-block results omit `blockReward` because
  the durable Core model cannot authoritatively derive consensus issuance or a
  complete execution reward; the API never substitutes zero.
- Address-only `/v2/api` ABI and source lookups first resolve the latest
  canonical contract-code observation and select a verification record by
  chain, address, that code hash, and its validity at the canonical tip. A
  missing current-code observation is unavailable, while a known current code
  without a matching record is unverified; an arbitrary open verification
  interval is never a fallback.
- Public source-verification submission is exposed only when
  `security.public_verification` is enabled and requires an API key. The
  native and compatibility submission boundaries bind every job to the latest
  canonical code observation plus a canonical top-level or traced creation
  input, and recompute the runtime code hash before enqueueing. Native input
  names the address, compiler input, and optional constructor-argument suffix;
  it cannot supply code hash, block hash, creation bytecode, or runtime
  bytecode. The exact constructor suffix must match the canonical creation
  input before it is stripped. Constructor arguments and license type are
  persisted as publication metadata, not injected into the compiler's Standard
  JSON settings. Compatibility input likewise cannot choose the chain, address
  binding, runtime bytecode, block hash, or Sourcify consent stored in the
  durable job.
- Disabling public submission does not disable authenticated reads of already
  durable verification jobs and verified artifacts. Public configuration's
  `verification` flag describes whether new native submissions are usable,
  while `sourcify` independently describes the optional interoperability
  surface.
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
