# ADR-0053: Shared Python WASM Vyper Distribution

Status: accepted

## Decision

P30-T117–T119 replace ADR-0047/ADR-0049's native Python execution and
platform-specific archive distribution. Their canonical publication, signed
catalog, immutable binding and fresh-process boundaries remain mandatory.

The existing Node SEA gains a Vyper-only protocol. Pyodide 0.29.3 / CPython
3.13.2 and its authenticated WASM PyCryptodome form one image-local read-only
runtime. All 26 existing official compiler releases use architecture-neutral,
digest-authenticated packages. Pure Python cbor2/immutables implementations
remain upstream bytes. No Vyper compiler implementation patch is permitted.
Native Python is a build/reference tool only; production has no native Python,
PyInstaller, package installer, general Node CLI or native Vyper fallback.

One fresh restricted process compiles each original or perturbed input. The
parent grants read access only to the shared runtime and selected package,
passes a secret-free environment, bounds I/O and wall time, and verifies
process-group termination and cleanup before accepting output. No host source
filesystem is mounted into Python. Emscripten's obsolete private fs constants
lookup is supplied only public fs.constants during initialization; the original
binding is restored immediately and every other private binding stays denied.

Vyper mode fixes V8 old-generation memory to 128 MiB and each WASM memory to
384 MiB. These limits do not bound total RSS and do not recreate the previous
512 MiB RLIMIT_AS guarantee. Self-tests prove oversize WASM allocation and host
read/write/network/child-process rejection. This remains execution of trusted
compilers with hostile data, not a sandbox for arbitrary Python/JavaScript.

Signed catalog v2 entries contain one emscripten-wasm32 package per compiler
and the required shared-runtime digest. Official source digest remains compiler
identity. The new node_vyper_wasm_v1 executor identity hashes a domain-separated
concatenation of the host SEA manifest, shared runtime manifest and package
manifest digests. Each signed package entry includes executor_digests for both
Linux host architectures, binding those composite identities to the same
package. PostgreSQL validates the chosen executor digest against that signed
entry. None may change during binding, retry or compilation.

Upgrade drains queued/running Vyper jobs before migration. Terminal provenance
is preserved without rebinding, backfill or a legacy executor. New jobs require
the new catalog and complete installed runtime. Public verification inputs,
matching and publication semantics are unchanged. Both Linux architectures
consume the same package bytes and must independently pass the full matrix and
monolith/split production acceptance before catalog promotion.

## Consequences

Compiler distribution no longer multiplies Python runtime builds by CPU
architecture. Node and its ELF closure remain host-native. Cold compilation is
slower and memory consumption differs; performance evidence must use the exact
selected runtime. Local candidate tests never substitute for Linux acceptance.
