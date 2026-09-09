# ADR-0048: Unified wazero Compiler Executor

Status: accepted

## Context

P30-T100 established successful Solidity/Yul and CPython WASI Vyper execution
with wazero. It also exposed incomplete Emscripten exceptions/callbacks and
Vyper's native Keccak dependency. These prototype gaps must close before the
production switch; the prototype is not a production runtime.

## Decision

- API/all retain catalog, artifact cache, leases and publication. Each input
  executes in a new dedicated `etherview-wasm` Go subprocess and fresh wazero
  instance. Geas is unchanged. No general interpreter/runner CLI is shipped.
- Pin wazero 1.12.0, CPython 3.13.15 and WASI SDK 24. Solidity/Yul continue using
  exact SHA-256-authenticated official emscripten-wasm32 soljson artifacts. Go
  statically extracts their unchanged WASM and bindings without executing JS.
  Preserve the complete pinned migration catalog's actual baseline behavior,
  including older compilers' unsupported features. Unknown formats/imports fail
  closed; no no-op imports or native/Node fallback is permitted.
- Implement Emscripten initialization, calls, exceptions, cleanup and wrapper
  compatibility. Compile errors remain compiler JSON, not successful empty
  outputs or leaked host errors. Missing import and SMT behavior matches the
  pinned reference wrapper with no caller-provided callbacks.
- Build CPython WASI from pinned source. Keep the official Vyper 0.4.3 wheel
  unchanged, use upstream immutables/cbor2 pure-Python implementations, and
  statically link a minimal Python extension calling a bounded Go Keccak-256
  host function implemented with golang.org/x/crypto/sha3. Package a narrow
  Python API adapter; do not ship PyCryptodome or diagnostic cryptography.
- The guest receives only fixed read-only runtime files and task-local source
  data, bounded standard streams and explicitly required host capabilities.
  No host filesystem mounts, inherited secrets, network, subprocess, dynamic
  library or package-install capability is exposed. Contract source is data.
- Preserve configured I/O, worker count and wall timeout; cap stderr at 1 MiB
  and WASM linear memory at 512 MiB. Enable context termination and bound host
  calls. Parent cancellation terminates the entire process group and confirms
  cleanup before lease release. Linear-memory limits are not total RSS limits.
- Cache authenticated original compiler artifacts only. Do not persist native
  compiled-code caches or retain guest processes between inputs.
- Runtime configuration is verification.wasm_path, defaulting to
  /opt/etherview/wasm/etherview-wasm. Retire executor_path and vyper_path without
  aliases at cutover. Runtime manifests bind helper bytes, wazero, adapters,
  CPython, Python packages, build policy and licenses. Solidity compiler identity
  remains its original soljson digest; Vyper remains its official wheel digest.
- New work uses executor_kind=etherview_wazero_v1 and
  execution_policy=wasm_subprocess_v1. Solidity compiler_kind=solc_wasm_v1,
  platform=emscripten-wasm32. Vyper compiler_kind=python_vyper_v1 and
  platform=python-wheel. Never reuse old executor digests or rebind a job.
- Preserve terminal history. A new migration refuses outstanding nonterminal
  jobs bound to old executors; operators pause admissions and drain them before
  migration. No cancellation, backfill, rebinding or dual-executor rollout.
- Complete both implementations and native AMD64/ARM64 production acceptance
  before unified cutover/removal of the old runtime. The old production paths
  remain authoritative during implementation. Node/native Python may remain in
  development-only differential tests.

This supersedes ADR-0031, ADR-0040 and ADR-0047 only for the compiler runtime,
packaging, configuration and executor identity decisions at completed cutover.
Their compiler trust, lease, canonicality and publication requirements, and
ADR-0037's artifact-cache rules, remain mandatory.

## Consequences

Production sheds Node and native Python shared-library closures, while taking
ownership of explicit Emscripten and Python host ABIs. Full baseline differential
and resource/error-path tests are required, not just successful compilation.
Cold startup, throughput and RSS may regress; record comparison evidence without
relaxing current timeouts or substituting emulation for native architecture gates.

Code generation uses at most four wazero compilation workers, capped by the
process Go CPU concurrency. Verification worker count and fresh-process/guest
isolation remain unchanged; no native-code cache is introduced. The exported
experimental compilation-worker API is pinned to wazero 1.12.0.
