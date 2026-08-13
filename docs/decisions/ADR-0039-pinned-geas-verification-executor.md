# ADR-0039: Pinned Geas Verification Executor

Status: accepted

## Context

ethereum/sys-asm publishes Ethereum system-contract sources in Geas assembly.
Its current build pins Geas v0.3.3 and uses multi-file relative `#include`
directives plus `assemble()` from constructor sources. Etherview's maintained
verification executor supports only Solidity and Yul through checksum-addressed
official solc-js artifacts. Geas does not use Solidity Standard JSON, compiler
metadata auxdata, ABI generation, or the solc release catalog.

Running a compiler library directly in the long-lived API process would let
hostile source consume that process's memory or trigger a compiler panic.
Treating Geas as another remotely downloaded compiler would add a supply-chain
surface that is unnecessary for sys-asm compatibility.

## Decision

- Public Geas support is additive only to native address verification, compiler
  listing, published artifact reads, and the Web form. Standalone and batch
  Solidity routes, Sourcify, and Etherscan submission remain unchanged.
- The only accepted compiler version is `github.com/fjl/geas` v0.3.3, built as
  a separate host-native helper from the repository's exact Go dependency.
  There is no runtime release catalog, download, cache, Git URL, or caller-
  supplied executable.
- Requests contain bounded inline sources, a required runtime entrypoint, an
  optional creation entrypoint, and an optional display-name hint. Canonical
  slash-separated relative paths form a read-only in-memory filesystem.
  Absolute paths, traversal, backslashes, non-canonical paths, duplicate JSON
  keys, and resolution outside that source bundle are rejected.
- Every entrypoint compiles twice in fresh helper processes with stack checking,
  a bounded include depth and diagnostic count, bounded stdin/stdout/stderr,
  the existing compile timeout, a private temporary directory, an empty
  secret-free environment, a Go memory limit, and process-group cleanup.
  Both bytecode and the transitive source-read set must agree.
- The helper never reads submitted sources from disk and has no network or
  subprocess capability in its protocol. A panic becomes a stable compiler
  runtime failure without exposing the panic value or nested compiler error.
- API and `all` startup require a regular, non-writable helper file, a successful
  fixed self-test, exact Go build-info module path/version/checksum with no
  replacement, and a helper SHA-256. Standard images supply the helper at a
  fixed path; an operator override must pass the same checks.
- Geas provenance is `compiler_kind=go_geas_v1`,
  `compiler_platform=go-module`, compiler digest derived from the authenticated
  module checksum, `executor_kind=etherview_geas_v1`,
  `execution_policy=trusted_subprocess`, and helper SHA-256. Catalog fields are
  absent. Provenance binds once while a valid lease is active and is immutable.
- Exact compiled runtime is required for address publication. If a creation
  entrypoint is supplied, its output must equal the complete canonical creation
  input. Geas permits no metadata, library, immutable, constructor-argument, or
  auxdata transformation. Successful supplied sides are `full`; a mismatch is
  an ordinary verification failure.
- Published Geas artifacts retain only the transitive sources actually read,
  empty ABI and artifact objects, empty libraries, entrypoints, and
  `stack_check=true`. Etherscan reads expose `CompilerType=geas` and the runtime
  file name; Etherscan writes remain Solidity-only.
- Compiler work is claimed by family availability. A stale solc catalog cannot
  block Geas, and a missing Geas helper cannot block Solidity, proxy, or
  Sourcify work. Metrics and alerts distinguish `solcjs` and `geas`.
- API replicas in one deployment use identical helper and module identities.
  Operators drain bound Geas jobs before upgrading either identity. Terminal
  publications remain readable across upgrades.

This decision extends ADR-0031's trusted-subprocess and deployment model for a
second, statically pinned compiler family. ADR-0024 remains authoritative for
leases, canonical publication, stable errors, bounds, and hostile input except
that Geas exact matching has no dual-source metadata perturbation or partial
match. ADR-0037 remains solc-js-only.

## Consequences

Production images gain one small native helper and the LGPL-3.0 Geas module.
Image and license checks must retain its exact license and dependency identity.
Geas version upgrades require a source release and drained jobs, but do not
require external compiler storage or network access. Verification can display
and authenticate sys-asm source while intentionally exposing no callable ABI.
