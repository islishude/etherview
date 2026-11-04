# Testing and Verification

The [Makefile](../Makefile) is the command source of truth. Only implemented
targets are runnable gates; future gates remain in their owning plans. Run
focused regressions first, then the applicable gates for the changed boundary.
Commands in these guides run from the repository root.

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
milestone for planned work); mention the host permission
boundary only when it materially affected execution. Read-only inspection in
the in-app browser is useful for diagnosing a built SPA, but it does not
replace a required `make test-e2e` pass.

## Evidence Rules

- Record the exact target/command and a concise pass/fail summary in the PR or
  completion report. For planned work, retain milestone acceptance in the child
  plan once, with revision/environment and material limits. Do not paste full
  logs or create plan items merely to record test execution.
- A targeted test is required for every fixed regression. Place it in the suite
  that owns the behavior, named for the contract or input condition rather than
  the incident, audit or redesign that introduced it. Extend an existing table
  or scenario when setup and assertions match. Consolidation must preserve
  distinct edge cases and the separate evidence provided by unit, PostgreSQL
  integration and embedded-browser tests.
- Integration tests that require optional local services must be reproducible
  through documented Compose profiles.
- Load and soak evidence records the revision, dataset, hardware, RPC behavior,
  duration, throughput, latency, error rate, and index lag.
