# Vyper WASM distribution

[ADR-0053](../../docs/decisions/ADR-0053-vyper-wasm-distribution.md) replaces
native runtime distribution with two read-only runtimes and one
architecture-neutral compiler package per supported version. The exact policy
in `wasm/runtime-policy.json` routes Vyper 0.4.0–0.4.3 to Pyodide 314.0.7 /
CPython 3.14.2 and the other 21 baseline versions to the `pyodide-legacy` npm alias at
0.29.5 / CPython 3.13.2. Unknown versions fail without fallback. Node SEA remains
host-native. Production deployment and native release acceptance are tracked
by P30-T127; the local matrix is not production release evidence.

`application-lock.json` owns runtime recipes. `versions/index.json` and per-version
`.lock` files retain the upstream pins used by audits and reference generation.
Vyper 0.2.0 is retired. Remaining compiler implementation bytes and reference
fixtures remain unchanged. Go assembles packages; Node owns
build, release, audit and regression orchestration. Native Python is
retained only for official reference generation and per-child RSS benchmarks.

`make compiler-install` prepares both shared WASM runtimes, the 0.4.3 package and
Node SEA for ordinary local regressions. `make test-vyper-matrix` builds all supported
packages and compares original and modified fixtures using fresh SEA processes.
`make test-vyper-wasm-candidate` runs the candidate comparison with a new restricted process per input. Outputs live
under `.local/vyper-wasm/`.

After building the packages, create the common release transports once:

```sh
node compiler/vyper/wasm/build-release.mjs --output .local/vyper-releases
```

The output directory must not already exist. The builder validates package and
shared identities and atomically publishes the complete supported set of deterministic
archives and unsigned descriptors. Native AMD64 and ARM64 consumers must use
these same archive bytes. Building transports does not assert matrix or
production acceptance.

The transports above are local CI evidence only; production never downloads
these packages. `application-lock.json` is embedded in the Go application.
`adapter.py` is embedded too. All supported compiler and
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
report is `.local/vyper/audit/raw.json`. The same gate builds all supported WASM packages
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
Omit `--version` to prepare all supported environments. Historical version locks remain
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

## Proposed updates from PyPI

Vyper 0.2.0 is retired, including its source archive and build exception. The
remaining 25 baseline versions retain their original compiler bytes and fixtures.
The complete version sets in the application lock, version index, runtime policy,
capabilities and fixtures must agree; `make test-vyper-update` checks this.

Install uv 0.12.24, as required by `pyproject.toml`. CI uses
[astral-sh/setup-uv](https://github.com/astral-sh/setup-uv) to install that version
and cache uv downloads. The tooling project's `uv.lock` pins packaging 26.3 and
its artifact hashes. Discovery, fixture generation and metadata tests use
`uv run --locked`; uv manages their Python environment and refuses stale locks.
The compiler resolver and reference environments continue to use uv's
`pip compile`, `venv`, `pip sync --require-hashes` and `pip check` commands.
These are uv commands and do not require a pip installation. To intentionally
refresh the tooling lock, run `uv lock --project compiler/vyper` and review its
diff separately from compiler update proposals.

Discovery is read-only:

```sh
node compiler/vyper/update.mjs --discover
```

Generate a proposed version in an isolated temporary tree, writing only a patch
and evidence into a new output directory:

```sh
npm --prefix compiler ci --ignore-scripts
node compiler/vyper/update.mjs --version 0.4.4 --output .local/vyper-update-0.4.4
```

The version above is an example, not a claim of upstream availability. Only new
stable versions above the highest supported version qualify. No historical gaps,
pre-releases, post-releases or withdrawn files are automatically selected.
The resolver pins the existing modern runtime's Python/PyCryptodome choices;
other dependencies require compatible pure Python wheels. An incompatible new
version needs manual runtime or adapter work. No native dependency fallback,
old-lock refresh or reference rewrite is performed. Existing special dependency
recipes remain unchanged and are not copied into new versions.

The patch includes exact archives, a generated manifest and runtime mapping,
and the new version's capabilities and official reference fixtures. The candidate
matrix must pass before a patch is emitted. The updater also captures a full
security inventory and live audit results without changing audit approvals.
Changed inventory requires a maintainer review; do not approve solely to clear CI.
Review and apply the patch with Git, then run the complete release gates in the
[testing guide](../../docs/testing.md#vyper-verification-gates).

The `Vyper compiler updates` workflow runs daily at 09:37 Asia/Shanghai and can
be dispatched manually with an optional exact version. Each version has one
`codex/vyper-update-<version>` branch and PR. Open/merged PRs are left untouched,
including their dependency choices and human edits. Closed PRs stay closed unless
explicitly retried with manual dispatch. Manual reopening requires an entirely
bot-owned branch. Orphaned branches are not overwritten and require manual recovery.
Preparation has no write token; publication only applies regular files in the
compiler data/generated-file allowlist. Neither stage changes audit dispositions.

Enable **Allow GitHub Actions to create and approve pull requests** in repository
Actions settings. GITHUB_TOKEN creates the PR; approve its workflows in GitHub
before expecting CI results. A green local candidate is not native release
acceptance. Failed preparation appears in the Actions summary and job logs;
successful proposals include downloadable audit and patch artifacts. There is no
automatic merge, deployment, Issue creation or external notification.
