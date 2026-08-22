# ADR-0042: Solady legacy CWIA identity and verified immutable arguments

Status: accepted

## Context

Solady's legacy `LibCWIA` deploys an immutable delegate shell whose runtime is
not ERC-1167. The shell embeds one fixed implementation, copies its trailing
immutable arguments into every non-empty call, and stores the appended byte
count both in the shell and in a two-byte footer. Treating the runtime as an
ERC-1167 variant would misstate its mechanism; treating arbitrary trailing
bytes as typed arguments would invent a layout that `abi.encodePacked` does not
carry on chain.

The existing proxy model already represents an immutable clone with one
implementation, raw arguments, exact block identity, verified implementation
ABI, no management authority, and generation-fenced publication. The detector
framework separately records family-specific evidence. The verifier already
requests every source AST and compiles the same bounded input twice, so it can
derive access offsets without retaining the complete AST or parsing source text.

## Decision

- Match only the exact legacy Solady shell: 98 fixed runtime bytes followed by
  immutable arguments and one two-byte big-endian appended-length footer. The
  embedded PUSH2 length and footer must both equal runtime length minus 98;
  implementation bytes occupy offsets 65 through 84 and arguments begin at
  offset 98.
- Fixed shell bytes, non-zero/non-self implementation, the existing 24,531-byte
  argument bound, and exact same-block implementation code observation are
  mandatory. The shell itself authenticates its active trailing arguments, so
  no creation trace is required.
- Persist the relation through the existing `proxy@2` representation with
  mechanism `cwia`, pattern `clone`, no standard version, no admin/beacon, and
  no upgrade authority. This is an additive value in the existing immutable
  singular-clone model, not a representation or rollback change, so `proxy@2`
  and `abi@4` remain current.
- Migration and startup never reinterpret or replay existing history. Blocks
  first processed after deployment use the new detector; any future historical
  coverage requires a separately approved bounded reindex.
- Register an independent V2 detector with family `cwia` and variant
  `solady-legacy-libcwia`. The OpenZeppelin adapter must not claim a CWIA
  outcome. V2 storage and public exposure retain their independent existing
  flags, and public V2 stays disabled until its production gate completes.
- Raw immutable arguments remain authoritative and are always preserved. The
  verifier derives an optional bounded schema only when reachable `_getArg*`
  calls resolve through `referencedDeclaration` to a linearized base source
  whose content SHA-256 is the exact canonical Solady `0.1.26` legacy
  `CWIA.sol` digest. An arbitrary same-named helper never qualifies.
- Analysis walks public/external/fallback/receive roots, their reachable
  internal/private routines, and modifiers. It accepts bounded constant
  offsets plus direct single-assignment unsigned length dependencies, and
  covers every canonical address, uint, bytes32, bytes, uint256-array, and
  bytes32-array helper. Dead code, runtime offsets, reassignment, cycles,
  overlaps, conflicts, gaps, and incomplete raw-byte coverage fail closed.
- Original and whitespace-perturbed compiler outputs must yield the identical
  normalized analysis. Only the at-most-8-KiB result is stored in compilation
  artifacts; complete AST documents are discarded. A server-owned analysis
  version participates in the verification request digest so a resubmission
  cannot reuse a pre-analysis successful job.
- Public schema version 2 uses `solidity_ast` and `solady-cwia-offsets`, binds
  the canonical helper digest and its own SHA-256, and represents fixed,
  length-field-multiplied, or remaining byte sizes. Addresses are checksummed,
  uints are decimal strings, bytes are lowercase hex, and supported arrays are
  bounded string arrays. NatSpec declarations, strings, booleans, signed
  integers, and bytes1 through bytes31 are not inferred.
- Missing or invalid AST analysis never invalidates otherwise successful source
  or proxy verification. It produces a bounded unavailable/invalid state and
  no partial typed values. Multiple artifacts at the chosen exact-address or
  code-hash priority must agree on one analysis digest.
- Exact-address verification, reusable artifact availability, and browser
  write authorization are three independent facts. Current proxy
  implementations expose the first through `verification_state` and the
  second through `artifact_resolution`: `verified + exact_address` means the
  implementation address was independently verified, while
  `unverified + code_hash` means only that a verified artifact with identical
  runtime code is available. No artifact resolution is published when neither
  fact exists, and exact-address artifacts always take precedence.
- Code-hash artifact reuse applies uniformly to the current singular
  implementation behind CWIA, EIP-1167, EIP-1967, and Beacon proxies. It does
  not apply to the proxy shell, admin, beacon, management target, historical
  implementation identities, Diamond facets, or Safe singletons. A current
  verified proxy binding always projects its implementation as
  `verified + exact_address`.
- An exact high-confidence CWIA shell observation permits implementation reads
  when the public interaction projection carries matching current proxy and
  implementation code identities and a verified implementation artifact
  resolves by that code hash. A code-hash-resolved AST schema may serve typed
  reads before proxy verification. Implementation writes additionally require
  an exact verified proxy binding and successful complete schema decoding; the
  browser's
  fresh pre-submit fence compares binding, proxy and implementation identities,
  raw-argument-bearing proxy code hash, and schema digest. CWIA exposes no
  management or upgrade controls.
- A code-hash-resolved implementation artifact may provide source, ABI, and
  implementation-as-proxy getters, but never creates a binding or authorizes a
  browser write. The pre-submit interaction fence includes artifact resolution
  so an exact-address/code-hash transition is a target change. Upgrade,
  ProxyAdmin, and Beacon-management interaction continues to require the exact
  current binding and exact-address identities.
- A fresh-operation response whose latest proxy stage is `unavailable` is not
  evidence that the displayed binding or code identity changed. The operation
  fails before `eth_call` or wallet submission with a retryable unavailable
  result, but does not trigger the binding-change callback, replace the last
  published interaction target, or redirect its selected tab. A subsequent
  operation always performs another fresh fence. Actual binding, code,
  artifact-resolution, schema, or management changes retain the existing
  target-change refresh and fail-closed behavior.
- The exact CWIA shell observation may also route that same code-hash-resolved
  implementation ABI into block-bound transaction events, call-like Trace
  frames, transaction Method selectors, and calldata reads. Exact-address
  implementation artifacts are considered first; the fallback requires the
  same chain and observed implementation runtime code hash, retains the
  artifact's actual source address, and publishes only
  `proxy_implementation/high` ABI provenance. Missing, conflicting, excessive,
  unpublished, or noncanonical evidence remains unavailable or ambiguous.
  Read-time correction performs no RPC, write, replay, or verification-state
  mutation.
- The Web presents decoded immutable arguments as an accessible,
  horizontally-scrollable `Name / Type / Offset / Data` table. Scalar and
  dynamic values occupy one row; supported arrays retain one copyable complete
  JSON parent row and at most 64 element rows whose offsets advance by 32
  bytes, with an explicit per-array omitted count beyond that bound. Raw
  immutable arguments and AST provenance remain visible.
- A recognized CWIA shell address does not render the direct Verified artifact
  panel or verification-submission entry. Its meaningful source and ABI surface
  is the current implementation artifact exposed through Proxy identity and
  implementation-as-proxy tabs. The implementation address's own Code page and
  artifact-resolution provenance remain unchanged; non-CWIA contracts and
  proxies retain their direct artifact entry. While the latest proxy stage is
  `unavailable`, the page withholds the direct entry and refetches classification
  every 500 ms until a published state proves CWIA or non-CWIA; the independent
  artifact request may load concurrently but cannot flash the wrong surface.
- Etherscan `getabi` and `getsourcecode` may resolve the exact verified
  implementation when the immutable shell itself has no source artifact, but
  only through the current writer-authoritative binding. `getsourcecode`
  reports `Proxy=1`, the fixed implementation, and clears implementation
  constructor arguments rather than attributing them to the CWIA address.

## Consequences

CWIA contracts reuse the current proxy, verification, ABI, and interaction
lifecycle without being mislabeled as ERC-1167. Typed values are
source-authenticated but remain visibly subordinate to the exact raw bytes.
Unsupported variants and ambiguous layouts fail closed, existing proxy facts
and bindings remain current, and production proxy-V2 promotion remains a
separate operator decision. Existing verification artifacts are not backfilled;
resubmission under the new analysis version derives the schema. Existing
same-code artifacts become immediately useful for read-only current
implementation presentation without rewriting verification history or
upgrading the implementation address's verification state. Blocks first
processed with the CWIA ABI route may also persist implementation-code-hash ABI
decodings; older blocks are not automatically reindexed, although an existing
complete published observation can support a pure read-time projection.
