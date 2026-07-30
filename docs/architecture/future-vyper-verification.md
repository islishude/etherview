# Future Vyper verification reference

Status: reference only. Etherview does not currently accept, compile, store, or
publish Vyper verification data. This document is not a roadmap commitment.
Reintroduction requires a new ADR, threat model, migration, public contract,
and deployment design; the removed native-runner implementation must not be
restored as a shortcut.

## Input and catalog model to reconsider

A future design would need bounded multipart and inline-only Standard JSON
inputs with an exact target filename. Source names, interfaces, search paths,
settings, and output selection must be normalized by the server and resolved
only from the submitted in-memory source map. Import callbacks and filesystem
fallbacks must remain unavailable.

Compiler discovery would need an authenticated, immutable version catalog with
an explicit artifact format, canonical version identity, generation digest,
artifact SHA-256, maximum size, and freshness policy. Each job would bind that
identity and an executor digest under its active lease. No CPU or container
platform may be inferred from the API host. A proposal without a maintained
architecture-neutral compiler artifact and an executor compatible with the
current production-image boundary is incomplete.

## Output and matching boundaries to reconsider

The previous implementation had to account for incompatible output and
auxdata shapes across compiler versions:

- Standard JSON metadata could be an object, and layout appeared only in newer
  versions.
- Creation auxdata used multiple CBOR tuple shapes and length conventions.
- Some older runtimes carried version metadata while newer runtimes separated
  creation metadata from deployed code.
- Immutable suffixes and `layout.code_layout` declarations had to agree before
  a deployed runtime could be accepted.

A future matcher must derive all transformations from the exact compiler
output, reject undeclared wildcards, and independently prove creation and
runtime matches. Metadata-only classification must strip only a validated
terminal format selected by the bound compiler version. Immutable ranges,
constructor data, and layout declarations must be exact, non-overlapping,
in-bounds, and mutually consistent.

## Minimum reintroduction evidence

A new design needs at least:

- official checksum-pinned fixtures for every supported format boundary;
- malformed catalog, artifact, Standard JSON, metadata, layout, immutable, and
  output-amplification regressions;
- deterministic double compilation and exact-version mismatch tests;
- executor permission, timeout, cancellation, output-bound, and cleanup tests;
- migration tests for language constraints and provenance immutability;
- generated OpenAPI and client checks, Etherscan compatibility behavior, and
  browser coverage;
- host-native AMD64 and ARM64 production-image E2E without a fixed deployment
  platform.

Until all of those decisions and gates exist, `vyper-json` remains a stable
unsupported code format and no Vyper task is created.
