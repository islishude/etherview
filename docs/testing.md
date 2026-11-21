# Testing and Verification

The [Makefile](../Makefile) is the command source of truth. Only implemented
targets are runnable gates; future gates remain in their owning plans. Run
focused regressions first, then the applicable gates for the changed boundary.
Commands in these guides run from the repository root.

## Test design

For Node.js (including TypeScript), Go and Solidity, maintain the smallest
suite that protects against regressions. Test counts, assertion counts and
coverage percentages are not goals. Honor additional verification the user
explicitly requests.

### Selecting coverage

- Prefer E2E as the sole verification mechanism when it suffices. Add another
  layer only when a system needs isolated testing. Security-critical
  verification and required CI gates still apply.
- Before writing implementation or tests for a system that needs isolation,
  define its boundary and enumerate every distinct failure reachable through
  supported use within it. For each, describe the trigger, observable outcome
  and verification method. Group equivalent inputs and permutations.
- Inspect existing tests first. In order, reuse them, extend them, or merge
  equivalent cases using tables or parameters. Add a test only for an
  independent, material risk to observable behavior, a documented contract,
  a real regression or an important invariant. Decide what realistic defect
  it detects, whether existing coverage detects it, whether removing it
  materially reduces confidence and why the chosen layer fits.
- Keep representative cases and distinct boundaries. Assert public behavior
  and protocol invariants. A new function, branch, file or coverage target
  alone does not justify a test. Overlap across layers must detect different
  failures.

### Matching the change

- For documentation, formatting, comments and nonbehavioral tidying, use
  relevant static checks only. For behavior-preserving refactors, run existing
  tests and extend them only for an uncovered compatibility risk.
- For a bug fix, prefer one focused regression in the suite that owns the
  behavior. Reproduce the failure before the fix when feasible and verify it
  passes afterward. Add cases for independent failures only.
- For new behavior or an external contract change, cover the necessary
  success paths and material failure conditions in existing suites first.
  Patch size does not determine test count. Run applicable existing checks
  even when the change needs no new test.

### Stability and maintenance

Use deterministic tests with clear behavioral assertions. Avoid sleeps,
fragile timing, external services and unstable fixtures. Reproduce optional
service integration through documented Compose profiles. Avoid combinatorial
cases, repeated assertions, trivial getters, pass-through wrappers and repeated
error-text checks. Introduce test frameworks, fixtures, mocks, snapshots,
helpers or dependencies only for a demonstrated maintenance benefit. Merge
directly related redundant tests when safe. Preserve valuable security and
regression coverage regardless of suite size or CI duration.

### Security-critical behavior

Scale verification to the risk for accounting, balances, withdrawals,
permissions, upgrades, replay, reentrancy and conservation of funds. Apply the
same standard to consensus, validator rewards, state transitions, deterministic
execution, cryptographic commitments and proof verification, cross-chain
deposits and withdrawals, bridge messages, finality and adversarial inputs or
transitions. Prefer high-value invariants, property tests and adversarial
scenarios covering independent attack surfaces and critical boundaries.
Correctness and security take precedence over runtime budgets and test counts.

### Language conventions

- **Node.js and TypeScript.** Exercise public behavior and meaningful module
  boundaries in existing test files. Parameterize distinct behavioral cases.
  Avoid excessive mocking, snapshots and retesting library behavior.
- **Go.** Use concise table-driven tests for distinct behaviors. Private
  helpers, new branches and equivalent permutations need no separate cases.
  Fuzz when the input space, parser, consensus or security risk warrants it.
  Keep an individual fuzz finding as an example only to preserve a regression.
- **Solidity.** Exercise external contract behavior, permissions, accounting,
  state machines and protocol invariants. Use unit tests for clear examples
  and invariant or fuzz tests for properties across inputs or operation
  sequences. Cover independent precision, rounding, replay and failure paths
  that affect funds or protocol correctness. Avoid copying every fuzz input
  into a unit test.

### Running tests

Start with the narrowest relevant test and rerun affected tests after a fix.
Expand verification for shared interfaces, cross-module behavior, consensus,
cryptography and security boundaries. Avoid repeated full-suite runs and
preserve required CI gates. Keep default PR checks fast and deterministic.
Schedule expensive stress, load, long fuzz and exhaustive invariant runs in
dedicated periodic or release jobs unless a critical change needs them in
the PR. Measure runtime and remove duplicate work without test quotas.

## Common Gates

- `make check`: documentation, source, unit/race, security, license, generation,
  and deployment
  gates. Browser, integration, parity, load, and soak suites are explicit
  opt-in targets because they require dedicated services or runtimes; CI runs
  the browser, managed integration, schema, and runtime E2E suites, not the
  external 30-minute soak.

| Change or validation task                                | Required entry points and detailed scope                                                                                     |
| -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Toolchain, lint, unit/race, security or licenses         | [Quality gates](testing/quality.md): `make lint`, `make test`, `make test-race`, `make security-check`, `make license-check` |
| OpenAPI, SQL, generated clients or embedded SPA          | [Generation gates](testing/quality.md): `make openapi-check` for OpenAPI; `make generate-check` for generated surfaces       |
| Database execution or package boundaries                 | [Source/lint gates](testing/quality.md): `make lint-go`, including `source-check`                                            |
| Maintained docs or deployment/runtime surfaces           | [Documentation gates](testing/quality.md): `make docs-check`                                                                 |
| Plans, ADR links or governance                           | [Plan validation](testing/quality.md): `make plan-check`                                                                     |
| PostgreSQL behavior or object storage                    | [Integration](testing/integration.md): `make test-integration`, explicit race and RustFS suites                              |
| Schema, runtime roles, images or shared Compose overlays | [Production runtime](testing/runtime.md): schema/runtime E2E and every affected overlay consumer                             |
| Browser behavior and embedded asset delivery             | [Browser acceptance](testing/browser.md): `make test-e2e`                                                                    |
| Hardhat/Foundry compatibility or production verification | [Client acceptance](testing/verification-clients.md) and [compiler acceptance](testing/compilers.md)                         |
| Sourcify, proxy detection or source discovery            | [Contract verification](testing/contracts.md)                                                                                |
| ENS, protocol data, archives or event logs               | [Chain data acceptance](testing/chain-data.md)                                                                               |
| Payments, facilitator, wallet chains or top-up progress  | [Billing acceptance](testing/billing.md), with live-funded evidence kept separate                                            |
| Load, soak or performance claims                         | [Performance gates](testing/performance.md)                                                                                  |
| CI jobs, native architectures or image/evidence handoff  | [CI workflows](testing/ci.md)                                                                                                |
| Local Preview fixtures, gateway, contracts or tools      | [Preview acceptance](../preview/docs/testing.md)                                                                             |

## Restricted automation hosts

Codex and other filesystem-restricted automation should preserve the exact
Makefile target and change only host execution plumbing. A writable cache
prefix avoids repeated failures from pnpm, the Go build cache, and
golangci-lint trying to update user-owned directories:

```sh
env \
  GOCACHE=/tmp/etherview-codex-go-build \
  GOLANGCI_LINT_CACHE=/tmp/etherview-codex-golangci-lint \
  pnpm_config_cache_dir=/tmp/etherview-codex-pnpm-cache \
  pnpm_config_store_dir=/tmp/etherview-codex-pnpm-store \
  make check
```

Use the same prefix with focused `go`, pnpm, or Makefile commands as needed.
Do not set `GOMODCACHE` merely because Go cannot update its optional module
download stat cache: retaining read access to the warmed shared module cache is
faster and supports offline work. Relocate it only when a command proves that
the module contents themselves must be written, and expect that a fresh module
cache may require approved network access.

On a Codex/macOS host already known to restrict browser process bootstrap and
Docker's user state, request sandbox-external execution up front for `make
test-e2e`, `make deployment-check`, and `make check`; the last target includes
the deployment gate. Pass the writable cache prefix to that approved command.
Pure frontend, Go, generation, plan, security, and license targets should stay
inside the sandbox unless their own output proves that broader access is
required.

Use this failure matrix instead of repeatedly trying unrelated workarounds:

| Failure signal                                                                                       | Required response                                                                                                                                                                                                                                 |
| ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| pnpm cache `EPERM`, Go build-cache `operation not permitted`, or golangci-lint cache warnings        | Retry the same command with the writable cache prefix above.                                                                                                                                                                                      |
| Chromium `MachPortRendezvousServer`, `bootstrap_check_in`, `SIGABRT`, or `kill EPERM` on macOS       | Request sandbox-external execution and rerun the exact browser target. If approval is unavailable, the [bundled single-process fallback](testing/browser.md#commands) is diagnostic evidence, not an automatic substitute for the canonical gate. |
| Docker socket/config denial or Buildx activity/config write failure                                  | Request sandbox-external execution and rerun the exact Docker-backed target. Keep `.github/scripts/compose.sh` and `.github/scripts/buildx.sh`; do not point Docker at an empty temporary configuration or bypass repository wrappers.            |
| Browser launches but the app is blank, the root is missing, or locators fail across unrelated routes | Inspect the first browser console error and the built asset graph. This is a runtime/build regression until evidence proves otherwise.                                                                                                            |
| Fixed E2E port is already in use                                                                     | Stop the temporary diagnostic server, confirm the port is free, and rerun the repository target so its own lifecycle remains authoritative.                                                                                                       |

An approved sandbox-external run is still a local test run. Record the exact
canonical target and result in the PR or completion report (and the owning
milestone for planned work). Read-only inspection in
the in-app browser is useful for diagnosing a built SPA, but it does not
replace a required `make test-e2e` pass.

## Evidence Rules

- Deliver a reproducible artifact. Give its repository-relative path, relevant
  version, necessary protocol inputs, commands from the repository root, and
  expected and observed results so a collaborator can verify the conclusion.
  Retain only the evidence supporting acceptance and any outstanding gates.
- Keep host runtime information out of acceptance evidence, including host
  and user names, absolute local paths, OS and hardware details, processes,
  environment variables, ports and host logs. For required performance or
  fault checks, retain the protocol inputs and measured acceptance results.
- Lead with the strongest verified result and use the project's wording.
  Group material limits in one section. Report unresolved issues, failed
  acceptance and outstanding required gates accurately. Omit intermediate
  errors, discarded approaches, course corrections and explanations of
  omitted work. Honor explicit requests to omit a topic. Omit AI attribution
  such as `Generated by` or `Co-authored-by`, formulaic transitions and
  introductory filler.
- In the PR or completion report, identify existing tests reused or updated,
  the independent regression or invariant protected by each new test, checks
  actually run and important checks outstanding. Claim existing coverage
  suffices only after inspecting it. For planned work, record acceptance once
  in the owning milestone. These rules require no separate test plan or report.
