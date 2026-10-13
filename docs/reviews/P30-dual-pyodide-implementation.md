# P30 dual Pyodide implementation review

Date: 2026-10-13. Owner: P30-T127. Decision:
[ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md).

## Artifact and execution boundary

The policy assigns Vyper 0.4.0–0.4.3 to Pyodide 314.0.7 / Python 3.14.2 /
PyCryptodome 3.23.0, and the remaining 22 versions to the exact npm alias
Pyodide 0.29.5 / Python 3.13.2 / PyCryptodome 3.21.0. Generated Go definitions
and Node tools consume the same explicit map. Unknown versions and mismatched
package/runtime/catalog combinations fail without retrying another runtime.

Both runtime manifests and self-tests are required at startup. The parent
freezes both runtime identities and the host; binding and compilation recheck
identity. Each child may read only its selected runtime and package, including
an explicit sibling-runtime denial probe. The 128 MiB V8 old-generation limit,
384 MiB WASM limit, production deadline and process-group cleanup remain.
These limits do not claim to bound total RSS.

Catalog v2 and the executor digest formula are unchanged. Each compiler entry
uses its selected shared digest. Acceptance records now bind `shared_runtimes`;
per-version records retain `shared_sha256`. Old acceptance cannot authorize new
artifacts. macOS-orchestrated Hardhat runs cannot acquire `native_linux` merely
by consuming a Linux matrix record.

All 26 prior release ZIPs were compared entry by entry with the new packages:
no compiler, adapter or dependency member bytes changed. Archive container bytes
can differ from old generated transports; their previous acceptance is not reused.
Original/modified reference files were not regenerated or edited.

## Core inventory and advisory review

Both actual runtime trees, all package distributions and nested vendors are
inventoried. The audit also binds the policy and generated Go map, execution
boundary and upstream notice lock. The inventory includes 591 distribution
records and 93 unique distribution/core identities. Runtime metadata and source
pins are checked independently of the host reference Python.

Modern source provenance comes from the pinned Pyodide
[build environment](https://github.com/pyodide/pyodide/blob/314.0.7/Makefile.envs)
and [CPython build](https://github.com/pyodide/pyodide/blob/314.0.7/cpython/Makefile):
CPython 3.14.2, Emscripten 5.0.3, WASM ABI 2026_0, liblzma/xz 5.2.2, zstd 1.5.7
and SQLite 3.39.0. CPython's pinned sources supply Expat 2.7.3 and its HACL*
revision. Shared unchanged zlib, bzip2, libffi, hiwire and musl source pins retain
their existing core reviews. Separate modern notice entries cover changed core
releases, Python third-party notices and Emscripten runtime libraries. The
legacy core and notice entries remain present.

The modern live OSV commit queries contain 84 CPython, 28 Expat, one xz and
10 SQLite findings; Pyodide, Emscripten, zstd and HACL* queries return no findings.
An empty query is not a claim of absence of vulnerabilities. Existing exact
advisory hashes were reused only where content and applicability match:

- CPython: 80 findings retain the previously reviewed fixed compile path,
  authenticated-archive, strict-codec and bounded-normalization dispositions.
  CVE-2026-12345 / PSF-2026-42 concern concurrent `TemporaryDirectory` cleanup;
  no user-controlled host directory or shared attacker-writable Python tree is
  exposed. CVE-2026-5713 / PSF-2026-19 require remote profiling/introspection of
  another process, which the fixed compiler protocol never performs.
- Expat: the compiler path never parses user XML. This also covers the newly
  inventoried CVE-2026-102633; XML tools are not distributed.
- xz CVE-2022-1271 requires the grep shell utility and attacker-controlled
  filenames. Only the library is linked; those utilities are not shipped.
- SQLite findings require SQLite SQL, changeset, FTS, session, CLI or archive
  extension calls. Neither the compiler nor adapter invokes SQLite. The Go
  service's PostgreSQL connection is separate from this WASM library. Findings
  remain conservatively inventoried even where affected source ranges overmatch.

The live legacy PSF-2026-41 response was reviewed again and its exact new hash
recorded: it concerns tar extraction filters and hard links to symbolic links,
with CVE-2026-82049 aliases. Only authenticated bounded ZIP payloads enter the
runtime; no user TAR archive is extracted. This is an exact-content review,
not an expansion of the auxiliary policy. Only the existing opted-in legacy
CVE-2026-82049 retains its narrow Vanir auxiliary-metadata exception. Unknown
findings, substantive changes and incomplete coverage still fail the audit.

## Evidence and remaining acceptance

The local candidate matrix and actual SEA/Go matrix each passed all 26 versions
and 362 original/modified compilations, with a fresh process per input. Runtime
policy, catalog/signing, cache, release and shard tests cover digest and routing
rejection. Both runtimes exercise memory/permission self-tests, timeout,
cancellation, oversize-input rejection and recovery after cleanup. Replacing
either runtime after startup makes the parent unavailable.

Local evidence is retained under `.local/dual-runtime-implementation` and
`.local/vyper-wasm`. Previous release transports/evidence were preserved under
`.local/dual-runtime-implementation/previous-vyper-releases` before generating
new unsigned transports. The user's stash was not applied or modified.

Native Linux AMD64 and ARM64 must independently run the complete production
image matrix and monolith/split Hardhat acceptance against the same public
artifacts. A local image check, native matrix without topology acceptance, or
macOS-orchestrated topology test does not complete that requirement. P30-T127
remains open until its plan records all required current evidence. No production
signing, commit, push, publication or deployment is part of this implementation.

## Reproduction

Run from the repository root with the pinned toolchains:

```sh
make compiler-install
make test-vyper-matrix test-vyper-release
go run ./cmd/testintegration -root . -packages './internal/integration ./internal/verify' -run Vyper -race
make check
make docker-build docker-image-check
```

The candidate scheduler optionally accepts a separate output root:

```sh
node compiler/vyper/wasm/check.mjs .local/dual-runtime-candidate
```

Generate unsigned common release transports into a new output directory, then
run these acceptance commands on each native Linux architecture, consuming the
same transports under `.local/vyper-releases`:

```sh
node compiler/vyper/wasm/build-release.mjs --output .local/vyper-releases
node compiler/vyper/wasm/production-matrix.mjs
make test-hardhat3-e2e-prebuilt
```

The release directory must not exist when building transports, and the matrix
producer refuses to overwrite an existing matrix record. Preserve earlier
records separately. The Hardhat client image must already be built with
`make hardhat3-client-image-build`. No production signing command is part of
this review.

## Final validation boundary

`make check` passed, including generation, lint, unit tests, race, live security,
licenses and deployment configuration. The focused PostgreSQL Vyper integration
and race suites passed, as did 83 Node tooling/audit/catalog/shard/feasibility
regressions. Actual child observation now precedes cancellation in both-runtime
Go tests; process-group termination and subsequent self-test recovery passed
under race detection. The current source inventory still evaluates to
`pass-with-auxiliary-changes`: 1,759 findings with one existing auxiliary-only
change, not zero vulnerabilities.

The production image passed its full 26-version / 362-input matrix under native
ARM64 Linux in the Docker Desktop VM, using a Linux container as producer and
no CPU emulation. Its image identity is retained in the durable summary.
Monolith and split Hardhat checks both passed (449.74 seconds combined),
including six historical/modern Vyper versions, persistent compiler cache and
provenance, with topology parity. Those Hardhat tests were orchestrated from
macOS; their acceptance record correctly has `native_linux=false`.

One supplementary candidate invocation ended without a complete report; its
partial log included nonpassing counts for 0.3.9 and 0.3.10 without retained
exception details. That incomplete invocation is excluded from acceptance.
The scheduler now persists each version's results and records an input, stage
and error, including timeout errors even when stderr is empty. The complete
repeat passed all 362 cases using the same limits and unchanged fixtures.
The [durable summary](P30-dual-pyodide-implementation.json) retains every
version, fixture/package/runtime identity, image identity and check-log digest.

P30-T127 is blocked on native Linux AMD64 full-matrix execution and native Linux
AMD64/ARM64 monolith/split Hardhat acceptance against the same common artifacts.
The blocker clears only with new matching records from those runs. Local tests,
the ARM64 matrix alone, macOS topology runs and historical records do not close
it. No production readiness, signing, publication or deployment is claimed.

## Per-version matrix results

All three matrices pass below: candidate, actual SEA/Go, and the native ARM64
Linux production image. Counts include original and modified inputs. Hardhat
topology coverage and missing native acceptance are scoped separately above.

| Vyper | Runtime | Inputs | Candidate / SEA-Go / Linux ARM64 |
| --- | --- | ---: | --- |
| 0.2.0 | legacy | 10 | pass / pass / pass |
| 0.2.1 | legacy | 10 | pass / pass / pass |
| 0.2.2 | legacy | 10 | pass / pass / pass |
| 0.2.3 | legacy | 10 | pass / pass / pass |
| 0.2.4 | legacy | 10 | pass / pass / pass |
| 0.2.5 | legacy | 10 | pass / pass / pass |
| 0.2.7 | legacy | 10 | pass / pass / pass |
| 0.2.8 | legacy | 10 | pass / pass / pass |
| 0.2.11 | legacy | 10 | pass / pass / pass |
| 0.2.12 | legacy | 10 | pass / pass / pass |
| 0.2.15 | legacy | 10 | pass / pass / pass |
| 0.2.16 | legacy | 10 | pass / pass / pass |
| 0.3.0 | legacy | 10 | pass / pass / pass |
| 0.3.1 | legacy | 16 | pass / pass / pass |
| 0.3.2 | legacy | 16 | pass / pass / pass |
| 0.3.3 | legacy | 16 | pass / pass / pass |
| 0.3.4 | legacy | 16 | pass / pass / pass |
| 0.3.6 | legacy | 16 | pass / pass / pass |
| 0.3.7 | legacy | 16 | pass / pass / pass |
| 0.3.8 | legacy | 18 | pass / pass / pass |
| 0.3.9 | legacy | 18 | pass / pass / pass |
| 0.3.10 | legacy | 20 | pass / pass / pass |
| 0.4.0 | modern | 20 | pass / pass / pass |
| 0.4.1 | modern | 20 | pass / pass / pass |
| 0.4.2 | modern | 20 | pass / pass / pass |
| 0.4.3 | modern | 20 | pass / pass / pass |
