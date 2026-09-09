# ADR-0047: Pinned Python Vyper Verification Executor

Status: accepted

[ADR-0048](ADR-0048-wazero-compiler-executor.md) supersedes the executor,
packaging, runtime-path and identity details at the gated unified WASM cutover.
Compiler trust, canonicality, lease and publication requirements remain in force.

## Context

Vyper 0.4.3 publishes an architecture-independent Python wheel. A dedicated
host-native executor can use the same compiler source on AMD64 and ARM64
without a platform-pinned deployment, Pyodide, or an independent runner.

## Decision

- API and all own Vyper jobs through the existing durable verification worker.
  The only version is 0.4.3, bundled with CPython 3.13.15 and PyInstaller
  6.22.2 in a read-only directory. Python package dependencies and artifacts are
  hash locked. The Docker builder uses the `python:3.13.15-slim-trixie` version
  tag without an OCI digest pin. No general Python CLI, package installer, or
  runtime download exists.
- The dedicated executable accepts only self-test and compile protocols. The
  v2 runtime protocol requires the Go parent to pass its effective input and
  output byte limits as positive decimal compile arguments; submitted sources
  cannot select these limits. Old runtime schemas fail startup validation. Each
  compiler input runs in a fresh process with a secret-free environment, bounded
  I/O and wall time, process-group cancellation and fail-closed cleanup. Linux
  enforces a 512 MiB address-space limit, 64 file descriptors and no core dump.
  File, socket and child-process audit controls are defense in depth for a
  trusted compiler, not a malicious-Python or native-code sandbox.
- Compiler sources are only bounded inline JSONInputBundle data. Paths are
  canonical relative POSIX paths; imports never fall back to host sources.
  The trusted runtime may read only its manifest-listed files. Official built-in
  interfaces belong to that runtime identity. User code cannot select Python
  modules, executable paths, environment, arguments, packages or external URLs.
- A canonical complete-tree runtime manifest binds Python, packaging policy,
  dependencies and helper bytes. Compiler identity is the official 0.4.3 wheel
  SHA-256; executor identity is the manifest SHA-256. Provenance uses
  `compiler_kind=python_vyper_v1`, `compiler_platform=python-wheel`,
  `executor_kind=etherview_vyper_v1`, and `execution_policy=trusted_subprocess`.
  It binds atomically once under the job lease. Catalog fields are absent;
  the compiler list is the validated bundled version, independent of solc
  catalog freshness. API-capable replicas drain bound jobs before changing
  runtime identity.
- Native address and standalone Standard JSON/multipart verification and
  Etherscan `vyper-json` use one normalized pipeline. A target file is required.
  Optimization is none/gas/codesize. Server-owned output selection and bounded
  source-only search paths replace caller selection. Experimental codegen,
  debug, custom storage layouts, Solidity libraries and optimization runs are
  unsupported. Batch, factory-derived and Sourcify Vyper paths are not added.
- Original and whitespace-perturbed sources compile with the same exact
  runtime. A Vyper-specific parser authenticates the 0.4.3 creation CBOR footer
  and compiler layout. A layout leaf has a string-valued type and explicit
  numeric offset/length; module members may themselves be named `type`.
  Immutable suffixes must have exact declared ranges and
  lengths; no undeclared byte differences match. Constructor arguments remain
  ABI-canonical. Runtime without authenticated metadata remains partial.
  Canonical target selection, Genesis provenance, lease fencing, immutable
  results and atomic publication retain ADR-0024 boundaries.
- The migration adds current Vyper contracts without changing historical
  migrations, restoring deleted data, or adding old-schema adapters. Product
  images build natively for each supported Linux architecture; native production
  E2E evidence is required independently for AMD64 and ARM64.

This decision supersedes ADR-0031's Vyper prohibition and extends ADR-0040's
production payload boundary only for the dedicated Vyper runtime. Solidity,
Yul and Geas retain their existing executors and artifact policies.

## Consequences

Images include a second language runtime and its exact license inventory.
Fixed Vyper releases require a source/image release; no dynamic compiler
catalog or egress is needed. Helper self-tests, manifest checks, matching,
publication and production topology tests are ordinary passing release gates.
