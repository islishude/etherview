# ADR-0024: Verifier v2 Workflow and Dynamic Compiler Catalog

Status: accepted

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

This ADR supersedes ADR-0014, ADR-0016, and ADR-0017 where their verifier-v1
request, compiler-catalog, match, publication, or Sourcify decisions conflict
with this decision. Their general lease fencing, canonicality, hostile-input,
redaction, and fail-closed cleanup requirements remain mandatory.

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
- Matching records bounded Verifier Alliance-style transformations and values.
  Creation matching may replace CBOR auxdata and library slots and insert ABI-
  canonical constructor arguments. Runtime matching may replace CBOR auxdata,
  libraries, and compiler-declared immutables. Values repeated under one
  identifier must agree.
- A match is `full` only when authenticated metadata is present and unchanged
  after deployment transformations. An auxdata replacement or absence of
  authenticated metadata is `partial`. Undeclared differences never match.
- Candidate selection is deterministic: both sides full, creation full,
  runtime full, then any partial result; an Etherscan compatibility hint wins
  only within one evidence tier, followed by ascending fully-qualified name.
- Address verification derives creation and runtime bytecode from canonical
  PostgreSQL facts. A runtime full or partial match is required for publication;
  creation-only matches remain valid only for standalone or batch verifier
  results. Completion rechecks the exact canonical code observation before
  atomically inserting the immutable result and its projection.
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
- Calling a Sourcify verification endpoint is the explicit publication
  consent. Sourcify jobs are bounded to three attempts, a 20-second request,
  one-second polling, and 120 polls. Their success is returned as an external
  result only.
- The v2 migration intentionally deletes the v1 verification jobs, immutable
  results, and verified-contract projections. There is no dual read, data
  conversion, or rollback format.

## Consequences

Verification can discover the matching contract and preserve the exact
transformations needed to reproduce deployed code. Dynamic compiler coverage
does not permit a retry to change compiler or runner identity. The destructive
schema reset requires an operator backup before upgrade, and old verification
records are not available through the new API.
