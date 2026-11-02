# ADR-0053: Shared Python WASM Vyper Distribution

Status: accepted

[ADR-0054](ADR-0054-vyper-application-lock.md) owns application-locked package
assembly and distribution. This decision owns execution, matching and immutable
provenance; it incorporates the still-applicable verification boundaries from
the retired native-Python and signed-catalog decisions (ADR-0047/ADR-0049).

## Decision

The existing Node SEA gains a Vyper-only protocol. It selects two exact,
image-local read-only runtimes: `pyodide@314.0.7` / CPython 3.14.2 /
PyCryptodome 3.23.0 for Vyper 0.4.0–0.4.3, and the npm alias
`pyodide-legacy@npm:pyodide@0.29.5` / CPython 3.13.2 / PyCryptodome 3.21.0
for the remaining legacy releases. The supported-version policy excludes 0.2.0,
leaving 21 legacy baseline releases; ADR-0054 governs reviewed additions to the
modern set. `compiler/vyper/wasm/runtime-policy.json`
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

The immutable application snapshot records one emscripten-wasm32 package per
compiler version and its required shared-runtime digest. Official source digest
remains compiler identity (`compiler_kind=python_vyper_v1`,
`compiler_platform=python-wheel`, `execution_policy=trusted_subprocess`).
The `node_vyper_wasm_v1` executor identity
hashes a domain-separated concatenation of the host SEA, selected shared runtime
and package manifest digests. The process registers its native host's composite
identity with the application recipe under ADR-0054; PostgreSQL authenticates
binding against that immutable generation, not a remote signed catalog.

Startup validates both shared manifests and requires permission, Python identity
and memory self-tests before readiness. Binding authenticates the installed
package; invocation revalidates all three identities before and after compilation.
The parent freezes host and shared runtime identities and never adopts a
replacement. An identity change makes the executor unavailable. Binding, retry
and invocation cannot silently choose new bytes. Acceptance records carry both
`shared_runtimes` digests and each matrix entry's selected `shared_sha256`.

API/all own Vyper jobs through the shared durable verification worker. Compiler
inputs are bounded inline sources/interfaces with canonical relative paths;
imports never fall back to host sources. User input cannot choose executable
paths, URLs, packages, runtime, environment or process limits. The Go parent
supplies effective positive input/output byte limits to the compile protocol.
Server-owned output selection and bounded source-only search paths replace caller
selection; official built-in interfaces belong to the authenticated runtime.
Native address and standalone Standard JSON/multipart, Etherscan `vyper-json`
and Web submissions use one version-aware pipeline with an explicit target.
Upstream defaults are preserved and unsupported explicit settings fail according
to the version's capabilities. Batch, factory-derived and Sourcify Vyper
verification remain excluded.

Original and whitespace-perturbed sources compile independently with the same
bound identities. Matching authenticates version-specific metadata and exact
compiler-declared immutable ranges; it never masks undeclared differences.
Releases from 0.4.1 carry the five-field integrity footer; older version-only
metadata does not prove full source identity. Missing authenticated metadata is
partial evidence. Constructor arguments remain ABI-canonical. Genesis target
provenance, canonical code/block selection, leases and atomic immutable-result
publication retain ADR-0024's boundaries.

Every runtime upgrade requires draining queued/running Vyper jobs. Terminal
provenance is never rebound or backfilled; earlier development schemas are
recreated under the fresh-database policy. Native Linux AMD64/ARM64 consume the
same package bytes and independently pass the complete matrix and production
all/split acceptance before application release. Local or emulated results do
not replace native evidence.

## Consequences

Compiler distribution no longer multiplies Python runtime builds by CPU
architecture. Node and its ELF closure remain host-native. Cold compilation is
slower and memory consumption differs; performance evidence must use the exact
selected runtime. Local candidate tests never substitute for Linux acceptance.

## Security validation

Custom live PyPI/OSV vulnerability queries are removed for native
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
vulnerability fix. Retired advisory reviews and runtime measurements remain in
Git. Current [validation](../testing.md#vyper-verification-gates) and
[benchmark procedures](../../compiler/vyper/README.md#integrity-and-security-checks)
require evidence bound to the selected runtime and package identities.

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
bytes require application-lock review and fresh native acceptance before application
release. No acceptance record or bound provenance is rebound to new bytes.
