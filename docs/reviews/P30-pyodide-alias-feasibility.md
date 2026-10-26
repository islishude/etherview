# Dual Pyodide npm alias feasibility

Historical evidence, dated 2026-10-12. P30-T126 evaluated a candidate against
the then-current single-runtime decision.
[ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md) now implements dual-runtime
routing, and ADR-0054 owns application-locked distribution and the retirement of
Vyper 0.2.0. Versions, counts and pending work below describe the original experiment.
**The proposed split passes local compatibility and isolation diagnostics.**
Production implementation and native release acceptance remain separate work.

## Exact installation and routing

Registry inspection returned `latest=314.0.7` and `stable-0.29=0.29.5`. The
experiment pins exact versions instead of retaining mutable tags or ranges:

```json
{
  "dependencies": {
    "pyodide": "314.0.7",
    "pyodide-legacy": "npm:pyodide@0.29.5"
  }
}
```

Both dependencies were installed together into one new diagnostic npm project.
`createRequire(...).resolve(alias)` resolved two distinct package directories;
the modern interpreter reports Python 3.14.2 / Emscripten 5.0.3 / ABI 2026_0,
and the legacy interpreter reports Python 3.13.2 / Emscripten 4.0.9 / ABI 2025_0.
Each uses its own lockfile-authenticated PyCryptodome wheel and indexURL.
The installed runtime file sets total about 14.32 MiB modern plus 13.10 MiB
legacy, excluding common npm dependencies and diagnostic metadata. This is a
local file-size measurement, not an image-size or RSS estimate.

Routing is explicit and closed: only 0.4.0–0.4.3 uses `pyodide`; the other 22
indexed releases use `pyodide-legacy`. Unknown/future versions are rejected.
No fallback retry changes runtime after a compilation failure. Each child gets
only its selected runtime/package read permissions and a secret-free environment.
The sibling runtime is not readable. Compilers never share a process or Python
module cache, and compilation has no network permission.

## Complete matrix

Host: macOS ARM64, Node 26.11.1. All **362/362** original/modified compilations
across all 26 versions match unchanged complete contracts output or expected
diagnostic types: **80 modern and 282 legacy**. Packages were rebuilt through
the current authenticated Node builder into isolated output paths; no compiler
source, dependency lock or reference was changed.

Every original and modified input starts a fresh restricted process, with four
concurrent workers, a 30-second deadline, 128 MiB V8 old-generation heap and
384 MiB WASM memory limit. All child groups are reaped; no matrix invocation
times out. These limits do not establish a total RSS cap.

| Vyper  | Runtime alias    | Matched cases |
| ------ | ---------------- | ------------: |
| 0.2.0  | `pyodide-legacy` |         10/10 |
| 0.2.1  | `pyodide-legacy` |         10/10 |
| 0.2.2  | `pyodide-legacy` |         10/10 |
| 0.2.3  | `pyodide-legacy` |         10/10 |
| 0.2.4  | `pyodide-legacy` |         10/10 |
| 0.2.5  | `pyodide-legacy` |         10/10 |
| 0.2.7  | `pyodide-legacy` |         10/10 |
| 0.2.8  | `pyodide-legacy` |         10/10 |
| 0.2.11 | `pyodide-legacy` |         10/10 |
| 0.2.12 | `pyodide-legacy` |         10/10 |
| 0.2.15 | `pyodide-legacy` |         10/10 |
| 0.2.16 | `pyodide-legacy` |         10/10 |
| 0.3.0  | `pyodide-legacy` |         10/10 |
| 0.3.1  | `pyodide-legacy` |         16/16 |
| 0.3.2  | `pyodide-legacy` |         16/16 |
| 0.3.3  | `pyodide-legacy` |         16/16 |
| 0.3.4  | `pyodide-legacy` |         16/16 |
| 0.3.6  | `pyodide-legacy` |         16/16 |
| 0.3.7  | `pyodide-legacy` |         16/16 |
| 0.3.8  | `pyodide-legacy` |         18/18 |
| 0.3.9  | `pyodide-legacy` |         18/18 |
| 0.3.10 | `pyodide-legacy` |         20/20 |
| 0.4.0  | `pyodide`        |         20/20 |
| 0.4.1  | `pyodide`        |         20/20 |
| 0.4.2  | `pyodide`        |         20/20 |
| 0.4.3  | `pyodide`        |         20/20 |

## Boundary and negative controls

Both runtimes pass host read/write denial, sibling-runtime read denial, network
and subprocess denial, permission inventory, oversized WASM allocation rejection,
a valid source over 5 MiB, and 30-second timeout/forced-kill group cleanup.
Wrong expected Python identities are rejected before compiler execution.

A deliberately misrouted Vyper 0.2.0 plain input on the modern runtime still
fails with `AttributeError: 'Constant' object has no attribute 'n'`, while its
correct legacy route passes. This confirms routing avoids the observed Python
3.14 AST incompatibility rather than concealing it with a shim or modified
reference. Three diagnostic regressions cover all version mappings, unknown
version rejection, and bytecode/error-type comparison strictness.

## Reproduction and evidence

With the ordinary compiler tooling dependencies installed, run:

```sh
make test-vyper-alias-feasibility VYPER_ALIAS_FEASIBILITY_OUTPUT=.local/alias-comparison-new
```

The output path must be new. The actual run used
`.local/pyodide-alias-feasibility-20261012/`; `report.json` retains commands and
probes, `results/` retains every compiler output, `fixtures.json` records reference
hashes, and `packages.json` records compiler/dependency identities. The npm lock
retains the official distribution URLs and integrity values for both aliases.
[Durable summary](P30-pyodide-alias-feasibility.json) retains identities, per-version
counts, probes and the negative control after local output cleanup.

All 81 Node tests across tooling, audit-auxiliary, catalog, shard and new alias
routing suites pass. `make docs-check plan-check`, JavaScript syntax checks and
`git diff --check` pass. Existing production dependency files and ADR are
unchanged. The user's stash was read to reuse the earlier diagnostic child,
but was not applied, popped, dropped or modified.

## Production implications

Npm alias solves installation and path resolution. It does not by itself change
the production executor's single SharedPath, fixed Python/Pyodide validation,
or runtime-bound package and executor identities. Diagnostic package manifests
keep the existing 0.29.5 production pins; the selected candidate runtime is
recorded separately. They must not be presented as signed release artifacts.

A subsequent implementation needs explicit per-version runtime selection,
per-runtime image directories and self-tests, matching package/shared/executor
digests in the signed catalog, and permission grants limited to the selected
runtime. Revisit ADR-0053 and the audit/license inventory before promotion.
Do not reuse old execution provenance or switch a bound job to another runtime.
The legacy route still contains Python 3.13.2: npm alias does not upgrade it to
3.13.16 or remove its existing security-review obligations.

No SEA/Go integration, native Linux AMD64/ARM64 matrix, monolith/split production
acceptance or dual-runtime security/license audit is claimed here. This result
supports proceeding to that implementation and acceptance work; it is not a
production release approval.
