# Vyper WASM distribution

[ADR-0053](../../docs/decisions/ADR-0053-vyper-wasm-distribution.md) replaces
native runtime distribution with two read-only runtimes and one
architecture-neutral compiler package per supported version. The exact policy
in `wasm/runtime-policy.json` routes Vyper 0.4.0–0.4.3 to Pyodide 314.0.7 /
CPython 3.14.2 and the other 22 versions to the `pyodide-legacy` npm alias at
0.29.5 / CPython 3.13.2. Unknown versions fail without fallback. Node SEA remains
host-native. Production deployment and native release acceptance are tracked
by P30-T127; the local matrix is not production release evidence.

`application-lock.json` owns runtime recipes. `versions/index.json` and per-version
`.lock` files retain the upstream pins used by audits and reference generation.
Version 0.2.0 uses its embedded upstream source archive. Compiler implementation
bytes and reference fixtures remain unchanged. Go assembles packages; Node owns
build, release, audit and regression orchestration. Native Python is
retained only for official reference generation and per-child RSS benchmarks.

`make compiler-install` prepares both shared WASM runtimes, the 0.4.3 package and
Node SEA for ordinary local regressions. `make test-vyper-matrix` builds all 26
packages and compares original and modified fixtures using fresh SEA processes.
`make test-vyper-wasm-candidate` runs the candidate comparison with a new restricted process per input. Outputs live
under `.local/vyper-wasm/`.

After building the packages, create the common release transports once:

```sh
node compiler/vyper/wasm/build-release.mjs --output .local/vyper-releases
```

The output directory must not already exist. The builder validates package and
shared identities and atomically publishes the complete set of 26 deterministic
archives and unsigned descriptors. Native AMD64 and ARM64 consumers must use
these same archive bytes. Building transports does not assert matrix or
production acceptance.

The transports above are local CI evidence only; production never downloads
these packages. `application-lock.json` is embedded in the Go application.
`versions/vyper-0.2.0.tar.gz` and `adapter.py` are embedded too. Other compiler and
dependency wheels are downloaded by exact URL and SHA-256, then assembled by
`bundle.go` into the same authenticated output used in build checks.

`node compiler/vyper/wasm/packages.mjs` downloads exact locked archives into
`.local/vyper-wasm/downloads` and invokes the Go assembler. It never chooses a
new wheel using live metadata. To deliberately update an approved recipe, edit
its exact inputs, populate the authenticated cache, then run:

```sh
go run ./compiler/vyper/cmd/bundle --update-lock compiler/vyper/application-lock.json
```

Review input and output digest changes and rerun the complete matrix/audit.
The normal command omits `--update-lock` and rejects output identity changes.
Native AMD64/ARM64 matrix and monolith/split acceptance remain mandatory:

```sh
node compiler/vyper/acceptance.mjs --artifacts .local/vyper-releases \
  --acceptance acceptance.json --output application-acceptance.json
```

This validates exact descriptors, fixtures, both runtime identities and every
version on both native Linux architectures. It cannot sign or publish a catalog.
No production private key or independently hosted distribution is required.
See [ADR-0054](../../docs/decisions/ADR-0054-vyper-application-lock.md).

## Dependency audit corrections

`make security-check` runs `audit.mjs` against the retained native 0.4.3
reference dependencies using live PyPI advisories. Retired PyInstaller tools
are excluded because reference preparation no longer installs them. Its raw
report is `.local/vyper/audit/raw.json`. The same gate builds all 26 WASM packages
and runs `wasm/audit.mjs` against their actual metadata (including vendored packages),
PyCryptodome and the pinned core source commits. PyPI and OSV are queried live;
service failure, missing coverage, changed inventory or unreviewed/changed
advisories fail the gate. Raw findings, inventory and the result are retained
in `.local/vyper-wasm/audit/` and uploaded by CI.

`wasm/audit-review.json` binds explicit applicability dispositions to exact
artifact and execution-boundary digests. A passing audit is not a zero-CVE
claim: historical compiler bytecode defects remain for faithful verification,
unreachable library interfaces remain distributed, and normalization CPU work
is bounded by subprocess cancellation. See the [security and performance
review](../../docs/reviews/P30-wasm-security-performance.md). Do not regenerate
dispositions merely to silence a failing gate.

`make benchmark-vyper-wasm` measures all versions with the actual restricted
SEA: one first pair and three repeated pairs, every compilation in a fresh
process. It also checks a 17-file workload against an official native output
digest. The first pair is process-cold; OS page caches are not evicted.
Optional `--native-python /absolute/path/to/venv/bin/python` records a 0.4.3
native reference comparison. Reports are written to
`.local/vyper-wasm/benchmark.json`; RSS uses per-child `wait4` high-water marks,
not a cumulative child-process maximum.

Two database records incorrectly include the exact official Vyper 0.4.3 wheel
in an open-ended affected range. Corrections are restricted to package `vyper`,
version `0.4.3`, the pinned wheel SHA-256 and these exact advisory IDs:

- `PYSEC-2023-142`: the [maintainer advisory](https://github.com/vyperlang/vyper/security/advisories/GHSA-5824-cm3x-3c38)
  identifies 0.2.15, 0.2.16 and 0.3.0 as affected; the fix is in 0.3.1.
- `GHSA-vgf2-gvx8-xwc3`: the [maintainer advisory](https://github.com/vyperlang/vyper/security/advisories/GHSA-vgf2-gvx8-xwc3)
  identifies versions through 0.4.0 as affected and 0.4.1 as patched.

These are version-range corrections, not acceptance of vulnerable compilation
or general advisory suppressions. Changing compiler version or adding another
advisory requires a new review; the raw findings remain available.

## Optional native reference tools

Install Node dependencies with `npm --prefix compiler ci --ignore-scripts`.
`node compiler/vyper/reference.mjs --version 0.4.3` prepares the locked official
reference environment using uv 0.12.24 and the version index's exact Python.
Omit `--version` to prepare all 26 environments. Historical version locks remain
frozen source/dependency evidence; the preparer excludes PyInstaller and its
packaging-only dependencies. No native helper, ELF closure or native release
archive is built. Reference environments are developer tools, never production
fallbacks, and must not execute untrusted Python.

`python3 compiler/vyper/fixtures.py /absolute/path/to/build-venv/bin/python
/absolute/path/to/output` regenerates the 0.4.3 specialized fixtures directly
with the official compiler. `python3 compiler/vyper/fixtures_versions.py`
regenerates all version fixtures and capability profiles from the prepared
environments. These are deliberate reference-writing commands; acceptance
never rewrites checked-in fixtures.

The Node ZIP/USTAR producers preserve upstream file contents, fixed timestamps,
permissions and deterministic output. Compression and archive headers can differ
from the retired Python producers, so transport and package digests change.
Old acceptance records cannot promote the new bytes: rebuild the inventory,
review its exact identity changes, and rerun native release acceptance before
application release. `make test-vyper-release` checks hostile archives, audit coverage,
reviewed advisory revisions and atomic complete-set publication. The Go archive
roundtrip invokes the actual Node producer.
