# ADR-0053: Shared Python WASM Vyper Distribution

Status: accepted

## Decision

P30-T117–T119 replace ADR-0047/ADR-0049's native Python execution and
platform-specific archive distribution. Their canonical publication, signed
catalog, immutable binding and fresh-process boundaries remain mandatory.

The existing Node SEA gains a Vyper-only protocol. P30-T127 selects two exact,
image-local read-only runtimes: `pyodide@314.0.7` / CPython 3.14.2 /
PyCryptodome 3.23.0 for Vyper 0.4.0–0.4.3, and the npm alias
`pyodide-legacy@npm:pyodide@0.29.5` / CPython 3.13.2 / PyCryptodome 3.21.0
for the remaining 22 supported releases. `compiler/vyper/wasm/runtime-policy.json`
owns the explicit version mapping and authenticated shared-manifest digests.
Node tools consume it directly; generated Go definitions are checked by
`make generate-check`. Unknown versions fail; compilation errors never switch
runtimes. The directories are `python-wasm/modern` and `python-wasm/legacy`.
All 26 existing official compiler releases use architecture-neutral,
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

Security review inventories actual compiler bundles (including vendors) and
the pinned WASM core, with live advisory queries and exact-artifact applicability
dispositions. Unknown or changed findings fail the gate except for the explicit
auxiliary-only policy below. Historical EVM compiler
bugs remain reproducible; verification is not contract safety certification.
Reachable pathological normalization remains bounded by subprocess cancellation,
not claimed fixed. See the [security and performance review](../reviews/P30-wasm-security-performance.md)
for measured startup/RSS costs and the accepted execution boundary.

## Auxiliary advisory metadata policy

`osv-cpython-vanir-v1` permits only changes at
`affected[].database_specific.vanir_signatures` and
`affected[].database_specific.vanir_signatures_modified` for explicitly opted-in
OSV `core/cpython` findings. The core must remain `python/cpython`; a PyPI
response, another component or a finding without its own opt-in remains strict.
Currently only CVE-2026-82049 is enabled. The audit does not run Vanir or use its
generated signatures to determine applicability. Introducing such a dependency
requires revisiting this decision before continuing to use the exception.

Each opt-in names one repository baseline whose existing advisory digest must
already appear in that finding's reviewed hashes, with a valid disposition.
The baseline is read-only; live responses never update it or add approved hashes.
An unknown policy, missing/changed baseline, identity mismatch or invalid baseline
fails. Previously reviewed exact hashes remain valid under the existing rule.

For a changed finding, validate the known Vanir v1 Function/Line signature
structure and UTC generation timestamp before removing those two paths from a
copy. Preserve parent objects, every other field, all array ordering and unknown
keys. Unknown signature fields/versions and malformed values require review.
The existing top-level `modified`/`published` exclusions remain; no other field
is excluded. Compare the remainder exactly against the authenticated baseline.
New findings, descriptions, severity, references, aliases, related/upstream
records, credits, withdrawal state, versions, Git ranges/fix commits and unknown
fields continue to block. Mixed auxiliary/substantive changes also block.

Full raw responses remain in the CI artifact. Auxiliary changes record the
policy, baseline/current full-content and decision digests, and changed JSON
pointers (at most 64; raw evidence remains complete). They produce
`pass-with-auxiliary-changes`, not a claim that the new full text was reviewed.
Rejected changes retain a failed classification report before the command exits
nonzero. Query failures retain the existing incomplete report and fail; inventory,
coverage, duplicate finding and full-content cross-query consistency checks are
unchanged. CI summaries expose only fixed status labels/counts, never untrusted
advisory text as workflow commands.

## Build and reference tooling

Node owns authenticated package construction, unsigned release transports,
actual-distribution audit/license checks and their regressions. Archive parsers
are pinned build-only npm dependencies; they are not bundled in the SEA.
Production Python adapter/protocol code and the benchmark's per-child `wait4`
measurement remain Python. Official native reference environments are prepared
independently with locked source/dependency bytes; PyInstaller, frozen native
helpers and ELF-closure packaging are retired. Historical version locks remain
authenticated inputs, with retired packaging tools excluded from installation.

The native 0.4.3 reference audit queries live PyPI metadata directly, checks
complete identity coverage and retains the two existing exact-wheel advisory
range corrections. It no longer needs a host pip-audit environment. The separate
WASM audit retains live PyPI/OSV queries, nested vendor coverage, core commit
queries and exact-content dispositions. Neither audit substitutes for the other.

Producer-language changes can alter compressed bytes without altering compiler
sources. All archive and manifest hashes remain authoritative: changed package
bytes require a new inventory review and fresh native acceptance before catalog
promotion. No acceptance record or bound provenance is rebound to new bytes.
