# Vyper compiler acceptance

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)
- [Vyper verification gates](#vyper-verification-gates)
- [Vyper WASM migration prerequisite](#vyper-wasm-migration-prerequisite)
- [Vyper update proposals](#vyper-update-proposals)

## Commands

- `make benchmark-vyper-wasm`: actual SEA first/repeated dual compilation,
  per-process peak RSS and a native-reference-checked 17-file workload. Every
  sample uses a fresh process. OS caches are not evicted; this local experiment
  does not establish Linux capacity or throughput under concurrency.

## Vyper verification gates

`make compiler-install` builds both shared Python WASM runtimes, Node SEA and
pinned 0.4.3 regression package. `make
security-check` and `make license-check` retain their dependency and license
checks. `make test-go` and `make test-race` exercise real compilation, manifest,
input-limit, cancellation and cleanup regressions. The checked-in supported-version
reference matrix covers normal code, constructors, invalid source and supported
immutables, with ABI/bytecode comparisons against the official compiler.

`make test-vyper-matrix` builds authenticated architecture-neutral packages and
runs all original/modified fixtures with the version-selected Pyodide runtime, then tests every version
through the SEA/Go execution boundary. Candidate outputs go to
`.local/vyper-wasm/`. Linux AMD64 and ARM64 production acceptance remains a
separate release requirement; the local matrix does not satisfy it.
`compiler/vyper/fixtures_versions.py` explicitly regenerates reference fixtures
from locked environments prepared by `node compiler/vyper/reference.mjs`; acceptance never rewrites references.

The [Hardhat suite](verification-clients.md#commands) owns Solidity/Yul/proxy
coverage and its strict persistence counts; it cannot substitute for Vyper acceptance.
`make test-vyper-e2e` builds the production image and runs the independent Vyper
application suite; `make test-vyper-e2e-prebuilt` consumes a loaded image. It uses
`VERIFICATION_PARALLEL=1` locally (CI sets 2), the same 30-minute timeout and both
monolith/split layouts. Six version families through native REST and Etherscan
produce exactly twelve successful jobs and results; each case verifies
constructor/immutable values, ABI/source reads and application-bound provenance.
Application-locked PyPI downloads and local assembly remain real production paths.

The complete production compiler matrix still requires common WASM packages in
`.local/vyper-wasm/packages/` and transport descriptors in `.local/vyper-releases/`.
`node compiler/vyper/wasm/production-matrix.mjs` exercises every original/modified
input in a fresh network-disabled container. Join these records with both application suites using the exact identity and
artifact rules in [CI evidence handoff](ci.md#ci-workflows-and-evidence-handoff).
Neither application job alone emits
release acceptance. A macOS run or emulated architecture never substitutes for
native Linux gates.

The native SEA/Go matrix schedules versions with `t.Parallel`, capped at four
active versions even if a direct invocation uses a higher Go `-parallel` value.
CI and `make test-vyper-matrix` explicitly use `-parallel=4` and `-count=1`.
Every supported version retains its own mutable runtime fixture, all checked-in
original/modified inputs and the final identity-tamper rejection. Inputs within
one version remain serial, and every compilation still starts a fresh restricted
SEA process with unchanged memory limits. Matrix setup probes and each compilation
use the production-container matrix's 30-second per-process deadline, rather
than the ordinary unit fixture's 10-second budget. This accommodates four cold
WASM processes sharing a CI runner without retries or skipped fixtures. Shorter
caller deadlines still win; the timeout regression checks both compiler and
caller cancellation followed by a successful fresh compilation. Version timings
include setup and cleanup and are visible with `-v`. The scheduling regression injects
missing packages at parallelism 1/2/4/8 and requires every version to report
failure and the test process to exit nonzero without leaking a concurrency slot.

After preparing the SEA, shared runtimes and full package set, compare scheduling
on the same native host using the following command with `-parallel=1`, `2`,
and `4`, running each measurement separately:

```sh
ETHERVIEW_TEST_VYPER_MATRIX_ROOT="$PWD/.local/vyper-wasm/packages" \
  go test ./internal/verify -run '^TestVyper(DynamicRuntimeMatrix|StableVersionMatrix)$' \
  -count=1 -parallel=2 -v -timeout=30m
```

Check identical passing version/input sets, subprocess concurrency, CPU and
memory use, and timeout/cleanup behavior alongside wall time. Repeat 2/4 in
reverse order to expose warm-cache or order effects. Local native ARM64 results
do not establish GitHub runner performance or native AMD64 acceptance.

CI retains native AMD64/ARM64 coverage and runs four production fixture
shards in each independent Vyper production job with `steps.parallel`, sharing
its already built image and read-only packages. Each command uses
`--shard-index 0` (through `3`) and `--shard-count 4` with
`production-matrix.mjs`. Versions are assigned by descending
fixture compilation count to the least-loaded shard, with numeric version and
shard-index tie breakers. Each shard writes a distinct record and uses unique
container names. Every original/modified input still gets a fresh restricted
container with the existing 30-second deadline; failures and cancellation remove
that invocation's container.

After all four steps pass, `node compiler/vyper/wasm/merge-production-matrix.mjs
--shard-count 4` rechecks the current image, runtime, packages and fixture hashes,
rejects missing/extra/duplicate or inconsistent evidence, and writes the existing
`matrix-{arch}.json` consumed by the final three-lane aggregation job. No
partial shard is release acceptance.
The no-argument production command remains the sequential local option; use a
clean evidence directory for a new run because evidence files are exclusive-create.
`make test-vyper-release` covers Node package/release/integrity/license regressions,
partition/merge and subprocess failure handling. Go tests authenticate and
reproduce the locked package with the same assembler used by workers.
Compare parallel-group wall time against the sequential baseline on native CI;
record resource contention and any compilation timeout without relaxing limits.
Historical measurements remain in Git and the
[measurement run](https://github.com/islishude/etherview/actions/runs/38184481846).
That run covers its recorded revision and 26-version baseline, before 0.2.0
retirement; it does not replace acceptance for later runtime changes.

PostgreSQL integration tests cover durable binding/publication and reorg
rejection. Embedded lock integrity, unsafe archives and runtime identity
regressions remain passing release gates. Runtime or permission failures must be
fixed, never converted to expected failures or unrestricted execution.

## Vyper WASM migration prerequisite

`make test-vyper-wasm-candidate` builds all supported compiler packages from the existing
hash-locked upstream archives and tests the explicit dual-runtime policy:
Pyodide 314.0.7 / Python 3.14.2 for Vyper 0.4.0–0.4.3, and the exact npm alias
0.29.5 / Python 3.13.2 for the other 21 baseline versions. Each of the checked-in
original/modified inputs starts a fresh process; a failed version does not hide
diagnostics for subsequent versions. Node denies network, writes,
addons and subprocesses during the differential run; its old-generation heap
is limited to 128 MiB and each WASM memory to 384 MiB. These limits are not a
process RSS limit. Results and digest-bound package manifests are written below
`.local/vyper-wasm/`. This candidate gate does not replace the required Linux
matrix, production image checks or native AMD64/ARM64 acceptance.

## Vyper update proposals

`make test-vyper-update` runs complete version-set checks, publication allowlist
and PR lifecycle tests, and offline PyPI metadata regressions. It requires uv;
CI installs the latest uv in `>=0.13.4,<1.0.0` with `astral-sh/setup-uv`, reading
the required range from `compiler/vyper/pyproject.toml`. Metadata tests and
update scripts share the
committed tooling `uv.lock` through `uv run --locked`; no pip bootstrap is needed.
Update recipe generation enforces the same range before invoking its isolated
resolver. `make test-vyper-release` includes these checks alongside the
package integrity and release acceptance regressions. Fixtures cover PEP 440
ordering, prerelease/yanked exclusion, wheel compatibility, digest mismatch,
network failures and unchanged prior recipes.

The updater only generates reference fixtures for the requested new version.
Its isolated candidate comparison does not replace the full native Linux matrix
or production topology gates. Proposals must pass local package integrity checks; maintainers review locked
dependencies and archive identities. The workflow cannot rewrite integrity checks.
