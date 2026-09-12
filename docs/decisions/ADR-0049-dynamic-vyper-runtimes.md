# ADR-0049: Signed Dynamic Vyper Runtime Catalog

Status: accepted

## Decision

P30-T107–P30-T110 extend ADR-0047 to non-withdrawn official stable releases.
Etherview builds complete dedicated Python helper trees from locked official
compiler inputs for native Linux AMD64 and ARM64. Each release records the
compiler source digest, Python, dependency lock, helper protocol, complete tree
manifest, transport digest, platform and license inventory. Production does not
install packages, build sources or invoke an unrestricted Python CLI.

A configured Ed25519 public key authenticates a base64 payload and signature
catalog envelope. The signed payload has schema `etherview-vyper-catalog-v1`,
an expiry and version entries, each with both Linux runtime artifacts. Only
validated, non-withdrawn stable entries enter immutable PostgreSQL generations.
Allowed HTTPS origins, no redirects, network restrictions and bounded downloads
remain mandatory. Catalog expiry and the existing 24-hour freshness limit stop
new bindings; previously bound work retains its exact generation and identity.
Catalog failure affects Vyper availability, not independent compiler families.

Compiler identity remains the official wheel or source digest. Executor identity
is the complete runtime manifest digest; executor kind `etherview_vyper_v3`
binds the protocol as well. The manifest and its files are revalidated before
and after every invocation. The downloaded archive is digest authenticated before
bounded extraction; links, special files, traversal and unlisted files are
rejected. Installation is atomic under the existing digest-scoped PostgreSQL
cache lock. Network calls never hold a database snapshot or writer lock.

API/all retain the durable worker and fresh, secret-free subprocess boundary,
resource limits, process-group cancellation and fail-closed cleanup. This is
trusted compiler execution, not a sandbox for malicious Python. No compiler
implementation or runtime is chosen by user-supplied URLs or paths.

Version adapters preserve upstream defaults and reject unsupported explicit
settings. Matching authenticates the version-specific metadata and immutable
layout, retains dual compilation and never masks undeclared differences.
Missing authenticated metadata produces partial evidence. Batch, derived and
Sourcify Vyper verification remain excluded.

The migration requires Vyper queues to drain, preserves terminal provenance and
does not rebind or restore historical jobs. Runtime releases require native
AMD64/ARM64 differential tests and production monolith/split acceptance before
catalog promotion. Future releases are never automatically trusted merely
because they appear upstream.

## Consequences

Operators configure the catalog URL, trusted public key and allowed origins.
The signing private key exists only in the release environment. Cache loss is
recoverable from the authenticated distribution, and never changes job identity.
Runtime publication is separate from source delivery. ADR-0024 publication and
catalog boundaries and ADR-0037 cache coordination continue to apply.
