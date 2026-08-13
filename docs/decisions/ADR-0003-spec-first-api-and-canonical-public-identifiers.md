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
  `eth_accounts`, `eth_chainId`, `eth_call`, `eth_sendTransaction`, and the
  SIWE-only `personal_sign`, plus `wallet_addEthereumChain` only through the
  generated public add-chain configuration.
  Every contract operation rechecks the selected account and configured chain,
  binds `from` and `chainId` in its call object, bounds calldata/value/results,
  and rejects malformed provider responses without trusting provider-owned
  array methods or getters. Account, chain, and disconnect events are
  provider-identity and session-revision fenced and fail closed, including ABA
  transitions. Once a transaction request reaches the provider, an invalid
  hash or changed completion session is an unknown outcome that must be checked
  in the wallet before retrying. Provider error messages and data never reach
  the DOM; stable local codes select bilingual text.
- `wallet_addEthereumChain` is independent of account connection and SIWE. It
  never requests accounts, switches the active chain, or mutates the selected
  wallet session. The wallet boundary validates and converts the generated
  snake-case public object into one exact EIP-3085 camel-case parameter and
  accepts only a `null` provider result as success. Chain identity, name, and
  native currency come from the existing chain configuration. The capability
  exists only when the operator supplies at least one separate public RPC URL;
  server `rpc.endpoints` are never copied into the public API. Production URLs
  use HTTPS. The checked-in local Preview may advertise an HTTP RPC only when
  its exact hostname is `localhost`; this narrow RPC-only exception does not
  apply to loopback addresses, internal hostnames, block explorer URLs, or icon
  URLs. Every URL list is bounded and rejects credentials, queries, fragments,
  and every other non-HTTPS scheme so private routing material cannot enter the
  browser or wallet.
- `personal_sign` is reachable only through the bounded
  `signSIWEChallenge(AuthChallenge)` capability defined by ADR-0020. The
  capability itself reads the generated public configuration and rejects
  arbitrary messages: the canonical EIP-4361 scheme, authority, URI, chain,
  EIP-55 address, request ID, and expiration must match the current browser
  origin, configured chain, selected account, and challenge envelope. Its
  payload is then the exact server-authored message encoded as UTF-8 hexadecimal
  plus the exact selected account. It has the same
  provider/account/chain/session preflight and completion fence as contract
  operations and cannot be used as a general signing primitive.
- Successful native responses use `{data,meta}`. Errors use
  `{error:{code,message,details,request_id}}`.
- Quantities that can exceed JavaScript precision are decimal strings;
  addresses are checksummed at the response boundary; hashes are normalized
  lowercase hexadecimal.
- List cursors are opaque to clients and bind enough immutable identity to
  reject malformed or stale traversal state. Cursor inputs and emitted
  `meta.next_cursor` values share the bounded `OpaqueCursor` schema; clients
  must not decode or construct them.
- Proxy detail, implementation-upgrade history, and initialization history are
  native spec-first resources under `/api/v1/contracts/{address}/proxy`. Their
  writer-only readers select one canonical tip in a repeatable-read,
  read-only transaction. History cursors bind chain, normalized address,
  resource kind, the exact canonical snapshot number and hash, and the
  published `proxy@2` durable-job generation at that snapshot. They also bind
  an append-only per-chain epoch watermark covering every `proxy@2` replay
  request or publication at or below the snapshot. A reorg or a late replay
  inside that range makes an old cursor a stable `invalid_cursor` error rather
  than silently traversing changed facts; a new canonical block above the
  frozen snapshot does not invalidate the cursor.
- A proxy interaction `bindingId` is the opaque identity of the exact current
  verified binding, not an authorization token. Detail reads expose it only
  while proxy, implementation, management, runtime immutable, published stage
  generation, code epoch, and continuous canonical coverage still agree.
  Generic or partial detection never enables a management or upgrade entry.
  Separately, an unambiguous high-confidence current Clone, EIP-1967, or Beacon
  relation exposes `implementation_interaction` with the proxy,
  implementation, mechanism, and optional beacon code identities. Ordinary
  implementation ABI reads and writes target the proxy and refresh that full
  identity before every call or submission; they do not consume `bindingId`.
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
- `GET /api/v1/transactions/{hash}` preserves one route and operation identity
  while returning a discriminated `included`, `pending`, or `replaced` detail.
  Included PostgreSQL chain data has priority over endpoint-scoped mempool
  observations; unknown hashes return `mempool_unavailable` when an enabled
  mempool lacks a fresh successful snapshot. See ADR-0036.
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
- Native and address-only `/v2/api` ABI and source lookups use one
  writer-authoritative resolver. It first resolves the target's latest
  canonical non-empty runtime-code identity, then prefers the target address's
  range-valid publication and otherwise deterministically selects a verified
  publication with the same chain and code hash. The latter is explicitly a
  `code_hash`/`SimilarMatch` result whose ABI and sources retain their source
  address; it does not independently verify the target, and target constructor
  evidence is omitted. A missing current-code observation is unavailable,
  while known code without a candidate is unverified. Verification submission
  remains strictly address-bound.
- Transaction logs always retain raw address, topics, and data and add one
  structured decoding status. The public projection distinguishes decoded,
  ambiguous, unknown, malformed, and unavailable results; an ambiguous result
  exposes candidates without selecting one signature or argument set.
- `GET /api/v1/transactions/{hash}/calldata` is the native transaction-input
  projection. One read-only repeatable-read snapshot binds the canonical
  inclusion and raw input to a published `state_diff@2` execution-code
  resolution, then exposes that exact context, execution address/code hash,
  ABI source, confidence, warning, and one of `decoded`, `ambiguous`, `unknown`,
  `malformed`, `unavailable`, or `not_applicable`. A missing exact historical
  identity is unavailable; the endpoint never substitutes a height, `latest`,
  the authority's current delegation, or a prior delegate ABI.
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
- Disabling public submission does not disable reads of already durable
  verification data. Verified-artifact GET is anonymous and free so the
  explorer can render ABI-derived contract functions without an API key;
  verification submission and GUID-addressed job reads retain their existing
  API-key boundary. Public configuration's `verification` flag describes
  whether new native submissions are usable, while `sourcify` independently
  describes the optional interoperability surface.
- Every native or compatibility verification request must carry a non-empty
  runtime bytecode whose Keccak-256 hash equals its code hash. Before publishing
  a successful result, the repository rechecks in the completion transaction
  that chain, address, code hash, and block hash identify one canonical
  `contract_code_observations` row joined to the canonical mapping at the same
  height. A syntactically valid client-supplied identity is not publication
  authority.
- Proxy-verification submission and status use the durable GUID-addressable
  lifecycle and only admit an exact current OpenZeppelin interaction target.
  The compatibility layer never promotes generic detection, incomplete
  coverage, an unverified implementation or management contract, or a stale
  binding into proxy success.
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
