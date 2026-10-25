# ADR-0024: Verifier v2 Workflow and Dynamic Compiler Catalog

Status: accepted

ADR-0031 supersedes this decision where it requires Vyper, native executable
platform selection, hard compiler isolation, runner images, or runner
provenance. The remaining catalog-integrity, dual-compilation, matching,
canonical-publication, and hostile-input requirements stay accepted.

## Context

The first contract-verification implementation requires one caller-selected
`source:name`, compiles once, rejects unresolved libraries, and publishes only
when both creation and runtime bytecode match. That boundary cannot provide
automatic contract discovery, deployment transformations, Yul, batch
verification, ERC-5202 blueprints, or a dynamically discovered compiler
catalog.

The replacement follows the behavior of Blockscout smart-contract-verifier at
commit `5cb8bdc88ebdbccf779bc138f1aeb8933b37eb07`, but is an independent Go
implementation. Blockscout source is not copied, vendored, linked, or included
as a dependency.

This ADR replaces the earlier verifier-v1 request, compiler-catalog, match,
publication, and Sourcify decisions where they conflict with this decision.
Their general lease fencing, canonicality, hostile-input, redaction, and
fail-closed cleanup requirements remain mandatory.

## Decision

- Compiler work remains asynchronous and PostgreSQL-backed in both monolith and
  split roles. Native REST is the only new public protocol; no Blockscout gRPC
  compatibility or zkSync compiler is added.
- Solidity, Yul, and Vyper Standard JSON and multipart requests compile every
  bounded candidate. Solidity batch requests compile once per input variant and
  compare at most 100 supplied contracts.
- Every compiler input is executed twice with the same exact compiler. The
  second copy appends one ASCII space to every ordinary source. Strictly
  validated differences between the two artifact sets locate compiler metadata
  auxdata; a candidate-set or bytecode-length difference is a compiler-output
  failure.
- Address verification of official solc-js Yul output that omits
  `evm.deployedBytecode` is accepted only for a certified Genesis target and
  only when its link-free, protocol-sized bytecode has the compiler's static
  object wrapper: one exact trailing byte range is copied with `CODECOPY` and
  returned. Arbitrary initcode is not executed. Both compiler executions
  independently derive that bounded runtime before the normal stable-shape and
  byte-for-byte matching rules apply. The derived runtime is compiler candidate
  data, never a substitute for an on-chain creation fact; standalone, batch,
  and ordinary transaction-deployment verifier semantics do not use this path.
- Matching records bounded Verifier Alliance-style transformations and values.
  Creation matching may replace CBOR auxdata and library slots and insert ABI-
  canonical constructor arguments. Runtime matching may replace CBOR auxdata,
  libraries, and compiler-declared immutables. Values repeated under one
  identifier must agree.
- ABI parsing, including method lookup, bounds document bytes, type depth,
  type-name length and expanded fixed-array/tuple shape before reflection-type
  construction. Constructor matching preflights decoded node and byte budgets
  before Unpack/Pack, counting aliased offsets on every visit. Canonical
  re-encoding remains mandatory after this preflight. The Go matching process
  does not rely on the compiler subprocess's heap limit for protection.
- A match is `full` only when authenticated metadata is present and unchanged
  after deployment transformations. An auxdata replacement or absence of
  authenticated metadata is `partial`. Undeclared differences never match.
- Candidate selection is deterministic: both sides full, creation full,
  runtime full, then any partial result; an Etherscan compatibility hint wins
  only within one evidence tier, followed by ascending fully-qualified name.
- Address verification derives creation and runtime bytecode from canonical
  PostgreSQL facts. Missing creation facts are accepted only for a non-empty
  runtime authenticated by a completed Genesis import whose imported block is
  still canonical block zero and whose exact account code and code hash equal
  the selected canonical runtime. That Genesis provenance is bound into the
  durable request and revalidated under the publication transaction; no
  creation match or constructor evidence is fabricated. Ordinary addresses
  still require canonical creation facts. A runtime full or partial match is
  required for publication; creation-only matches remain valid only for
  standalone or batch verifier results. Completion rechecks the exact
  canonical code observation before atomically inserting the immutable result
  and its projection.
- ERC-5202 creation and runtime wrappers must decode to the same non-empty
  initcode when both are supplied. Sourcify results and compiler-list contents
  are hostile external input and never become local publication evidence.
- Compiler lists are fetched hourly into immutable PostgreSQL generations. A
  failed refresh retains the last successful generation; submissions stop when
  it is older than 24 hours. Lists are limited to 8 MiB and 4096 entries.
  Entries require canonical versions, an allowed HTTPS origin, a non-zero
  SHA-256 digest, and an artifact no larger than 200 MiB.
- Compiler artifacts are cached by digest through the existing proxy-free,
  redirect-free, public-network-only downloader. Public compilation streams the
  compiler and Standard JSON over a bounded framed protocol into a generic,
  digest-pinned runner image. The runner rechecks the digest in an executable
  tmpfs before invoking the compiler. Solidity catalog discovery recognizes
  the complete platform directory set published by solc-bin and automatically
  selects from the validated container-runner image or private process host;
  configured catalog URLs are optional approved-mirror overrides. Native
  packages must match their ELF, Mach-O, or PE platform. Emscripten/WASM
  directories are recognized but fail closed while their unlisted sidecar
  inputs cannot be represented by the immutable single-artifact provenance.
  Compiler platform, compiler digest, and runner digest are immutable job provenance.
- Compiler containers keep no network, a read-only root, non-root identity,
  dropped capabilities, no-new-privileges, bounded CPU, memory, PIDs, file
  descriptors, output, and temporary space. The exact random container is
  force-removed before any outcome is accepted; cleanup failure remains fatal
  and leaves the lease for explicit recovery.
- Compiler diagnostics are bounded domain outcomes and are never logged.
  Sandbox, transport, cache, runtime, and nested upstream diagnostics remain
  stable redacted system failures.
- Explicit Sourcify source import uses `POST
/api/v1/contracts/{address}/verification/sourcify` with `contract:verify`
  authorization and a required decimal-string `chain_id` selecting the source
  chain on Sourcify. This positive uint64 may differ from the deployment's chain:
  only source lookup uses it. The local target remains the same address on the
  deployment's chain. The API resolves the exact canonical target, closes its
  database snapshot, and performs a bounded HTTPS GET for existing sources.
  It checks remote identity against the requested source chain/address and requires
  identical remote/local runtime bytes. It rejects mismatches and unsupported or
  non-inline compiler inputs, then queues an ordinary `address` job containing
  the retrieved input and the original local target. Source retrieval is not
  local verification: pinned dual compilation and lease/canonical publication
  fences still determine local source and ABI publication. Retries use durable
  input, never refetch it inside a worker transaction. Solidity/Yul and supported
  Vyper inputs retain their ordinary compiler policies, including authenticated
  Genesis runtime-only publication. Import performs no external source upload.
  The Web defaults to direct import and offers a separate import action after
  external publication, with separate local and external job results.
  Direct import exposes the source chain with the deployment's chain as its
  default. Source publication has separate source-file and compiler-metadata
  upload controls; each replacement preserves the other selection and revokes
  publication consent. The submitted file-map contract and total bounds remain
  unchanged.
- Calling a Sourcify verification endpoint is the explicit publication
  consent. Sourcify jobs are bounded to three attempts, a 20-second request,
  one-second polling, and 120 polls. Their success is returned as an external
  result only.
- Etherscan imports accept an optional, write-only `etherscan_api_key`, separate
  from Etherview's `X-API-Key` authentication. Submission authorizes forwarding
  that credential to the configured Sourcify service in the JSON `apiKey` field.
  The API encrypts it before durable enqueue using AES-256-GCM with a fresh
  random nonce and authenticated chain/address context. Independent encryption
  and HMAC deduplication keys are derived with versioned, domain-separated
  HKDF-SHA256 from the API role's existing `security.api_key_pepper` secret.
  The stored request contains only a versioned ciphertext envelope and a keyed
  fingerprint; randomized ciphertext is excluded from request identity. Raw
  credentials never enter database parameters, logs, outcomes or job responses.
  Workers decrypt only for the outbound request; missing keys or failed
  authentication fail closed. All API/all-role replicas share the same secret.
  Drain pending imports before rotating the pepper and retain the matching
  secret separately from backups needed to restore encrypted jobs.
- Sourcify requests use HTTPS with public-network address checks by default.
  The independent `sourcify.unsafe_allow_private_networks` override permits local
  Fake-IP/private-network development. It defaults off outside Preview; Preview
  explicitly enables it in its shared configuration.
  It relaxes only the Sourcify client's IP classification: TLS verification,
  checked-IP dialing, redirect rejection, disabled environment proxies, request
  bounds and explicit source-publication consent remain mandatory.
- The consolidated baseline creates the current verification contracts in a
  fresh database. Earlier development schemas must be recreated; there is no
  dual read, data conversion, or rollback format.

### Session-authenticated verification

The embedded Verify page uses four Cookie-authenticated writes under
`/api/v1/verification-jobs`: `local`, `sourcify-import`, `sourcify`, and
`sourcify-from-etherscan`. Local and source-import requests carry `address` in
the JSON body; source import also carries the source `chain_id`. All writes
require an active writer-authoritative SIWE session, exact configured Origin,
and its CSRF token before input parsing, external work or enqueueing. The
existing native and Etherscan protocols still require `contract:verify` API
keys; neither credential substitutes for the other.

`GET /api/v1/verification-jobs/{id}` requires an active session and remains
available when new public submissions are disabled. It returns only job state,
match evidence and durable target address/chain, excluding submitted sources,
compiler artifacts and upstream credentials. As with the keyed job reader,
authorized callers may read a known UUID; jobs are not user-owned and no
personal history is introduced. The existing rate admission, input/work bounds,
compiler identity, PostgreSQL queue and publication fences are shared across
both submission channels. Browser verification requires user authentication to
be enabled and never provisions or embeds a substitute API key.

## Consequences

Verification can discover the matching contract and preserve the exact
transformations needed to reproduce deployed code. Dynamic compiler coverage
does not permit a retry to change compiler or runner identity. Operators retain
any needed backup before recreating an earlier development database; the new
baseline does not import its verification records. Authenticated Genesis
predeploys can publish runtime source without inventing a deployment
transaction, initcode, or constructor arguments; an incomplete or stale
Genesis proof remains unavailable rather than weakening the creation boundary.

## Optional browser submission challenge

`turnstile.verification_enabled` defaults off. When enabled, all four browser
submission POSTs require a single `X-Turnstile-Token`, after SIWE, Origin, CSRF
and capability checks but before source parsing, target resolution, upstream
calls or enqueueing. Cloudflare Siteverify must confirm success, action
`verification`, and the hostname from `server.public_url`. Native/Etherscan
API-key operations and job GETs retain their existing authentication semantics.
Failures return redacted `turnstile_invalid` (403) or `turnstile_unavailable`
(503); upstream outages never withdraw core readiness. The fixed HTTPS request
has a five-second deadline, no redirects or retries, and a 16 KiB response cap.
Tokens are at most 2048 characters, single-use and valid for five minutes.
Secrets are environment/file-only and read by API/all roles; public runtime
config exposes only the site key and switches. No database snapshot is held
while validating, and tokens are never persisted in job inputs.
