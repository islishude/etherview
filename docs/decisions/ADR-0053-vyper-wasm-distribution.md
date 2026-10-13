# ADR-0053: Shared Python WASM Vyper Distribution

Status: accepted

[ADR-0054](ADR-0054-vyper-application-lock.md) supersedes the hosted-package,
independent signing/catalog and remote freshness decisions below. Runtime
isolation, immutable provenance, matching and native acceptance remain mandatory.

## Decision

P30-T117–T119 replace ADR-0047/ADR-0049's native Python execution and
platform-specific archive distribution. Their canonical publication, signed
catalog, immutable binding and fresh-process boundaries remain mandatory.

The existing Node SEA gains a Vyper-only protocol. P30-T127 selects two exact,
image-local read-only runtimes: `pyodide@314.0.7` / CPython 3.14.2 /
PyCryptodome 3.23.0 for Vyper 0.4.0–0.4.3, and the npm alias
`pyodide-legacy@npm:pyodide@0.29.5` / CPython 3.13.2 / PyCryptodome 3.21.0
for the remaining legacy releases. P30-T130 retires 0.2.0, leaving 21 legacy
baseline releases; ADR-0054 governs reviewed additions to the modern set. `compiler/vyper/wasm/runtime-policy.json`
owns the explicit version mapping and authenticated shared-manifest digests.
Node tools consume it directly; generated Go definitions are checked by
`make generate-check`. Unknown versions fail; compilation errors never switch
runtimes. The directories are `python-wasm/modern` and `python-wasm/legacy`.
All supported official compiler releases use architecture-neutral,
digest-authenticated packages. Pure Python cbor2/immutables implementations
remain upstream bytes. No Vyper compiler implementation patch is permitted.
Native Python is a build/reference tool only; production has no native Python,
PyInstaller, package installer, general Node CLI or native Vyper fallback.

One fresh restricted process compiles each original or perturbed input. The
parent grants read access only to the selected runtime and compiler package,
including denial of reads from the sibling runtime,
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
entry. Startup validates both shared manifests and requires successful permission,
Python identity and memory self-tests for both runtimes before readiness.
Binding authenticates the installed package; invocation revalidates all three
identities before and after compilation. A running parent freezes the host and both runtime identities and never adopts
a replacement. A changed identity makes the executor unavailable. None may
change during binding, retry or compilation. The catalog v2 format and executor
digest formula remain unchanged: each version uses its selected shared digest.
Internal acceptance records carry `shared_runtimes` with both digests, while
each matrix entry retains the actual `shared_sha256`.

Every runtime upgrade drains queued/running Vyper jobs before deployment.
This dual-runtime change adds no database migration or compatibility adapter. Terminal provenance
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

## Security validation

P30-T134 removes the custom live PyPI/OSV vulnerability queries for native
reference dependencies, historical Vyper packages and both Pyodide runtime
cores. CVE dispositions, advisory-content hashes, metadata exceptions and
stored advisory snapshots are retired. CI and automatic compiler proposals do
not query these feeds or require advisory approval records. This supersedes
the previous exact-content and auxiliary-advisory policies; it does not repair
upstream vulnerabilities or establish that the distributed runtime has none.

Historical compiler implementations must remain reproducible. Bundled CPython
updates depend on compatible Pyodide builds and complete runtime acceptance.
Blocking every build on upstream advisory metadata drift does not enforce such
an upgrade, so that custom gate is removed at the user's explicit direction.

`wasm/check-integrity.mjs` retains local manifest and payload authentication,
exact runtime directory coverage, compiler/runtime identity and actual package
metadata checks for both runtimes. Official archive/lock checks, nested vendor
licenses, fresh-process isolation, resource/cancellation regressions and native
acceptance remain mandatory. Go vulnerability checks, npm dependency audits
and secret scanning are unchanged. There is no replacement CVE allowlist.

Verification is not contract safety certification. Historical EVM compiler
bugs remain reproducible, and bounded pathological input is not an upstream
vulnerability fix. The [historical security and performance review](../reviews/P30-wasm-security-performance.md)
retains measured startup/RSS evidence and the original threat analysis.

## Build and reference tooling

Node owns authenticated package construction, unsigned release transports,
local distribution integrity/license checks and their regressions. Archive parsers
are pinned build-only npm dependencies; they are not bundled in the SEA.
Production Python adapter/protocol code and the benchmark's per-child `wait4`
measurement remain Python. Official native reference environments are prepared
independently with locked source/dependency bytes; PyInstaller, frozen native
helpers and ELF-closure packaging are retired. Historical version locks remain
authenticated inputs, with retired packaging tools excluded from installation.

Producer-language changes can alter compressed bytes without altering compiler
sources. All archive and manifest hashes remain authoritative: changed package
bytes require application-lock review and fresh native acceptance before catalog
promotion. No acceptance record or bound provenance is rebound to new bytes.
