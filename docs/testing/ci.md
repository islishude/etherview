# CI workflows and image handoff

[Testing index](../testing.md) · Commands run from the repository root.

- [CI workflows and evidence handoff](#ci-workflows-and-evidence-handoff)

## CI workflows and evidence handoff

The [CI workflow](../../.github/workflows/ci.yml) runs on pull requests, pushes to
`main`, and daily at 00:17 UTC (08:17 Asia/Shanghai). Scheduled runs execute the
same complete job set on the default branch, including both native architecture
matrices. Event-specific concurrency groups prevent a push from cancelling the
daily run. GitHub activates the schedule after the workflow reaches the default
branch; scheduled start times may be delayed by runner load.

The entry workflow owns triggers, concurrency, Vyper package production and the
native WASM matrix, plus explicit image-to-consumer dependencies. The reusable
[checks workflow](../../.github/workflows/ci-checks.yml) runs independent quality, lint, test, race-test,
PostgreSQL/RustFS, browser, security/license, native provider compatibility and
deployment-static jobs. Quality runs `make toolchain-check docs-check plan-check openapi-check generate-check`;
lint, test and race-test run their respective Makefile targets, with uv installed for test.
`lint-go` builds the embedded SPA before Go analysis, so the standalone lint job
also works from a fresh checkout without `web/dist`.
The browser target owns its frontend build. Shared Go/Node/pnpm setup lives in a fixed local action;
checkout and specialized tooling remain visible in their owning jobs.

Native production images are built once per architecture independently of
`vyper-packages`. Once both artifacts are ready, the architecture's reusable
production verification workflow runs four independent jobs: four Vyper fixture
shards with strict merge, Vyper application E2E, Hardhat application E2E, and
Foundry E2E. Hardhat and Foundry build only their own client images. The existing
Vyper acceptance join still requires exactly fixtures, Vyper and Hardhat;
Foundry is separately required for a successful workflow. AMD64 and ARM64 do
not wait for one another; native WASM matrix and provider compatibility jobs
remain independent.

The reusable [runtime workflow](../../.github/workflows/ci-runtime.yml) consumes
only the AMD64 image, without waiting for compiler packages or verification.
Its schema/runtime job uses the prebuilt targets. Three Preview jobs run on
separate runners. The short gateway/genesis, x402,
[CLI smoke](../../preview/docs/tools.md#acceptance) and consensus/blob smoke checks
share one job and one Preview setup; metadata and contracts/Bundler/ENS
acceptance remain independent. Consensus/blob CI runs one split-role real-blob smoke
check with no finality wait or recovery matrix. The complete suite is opt-in
via `make test-preview-consensus-full` (or its `-prebuilt` variant); see
[Preview acceptance](../../preview/docs/testing.md#real-lighthouse-preview-acceptance).
The grouped checks run sequentially to avoid competing
Compose stacks. After successful setup, each check runs even if a previous
check fails; failures still fail the job. Cancellation or setup failure skips
the remaining checks. Retrying the grouped job reruns its short checks.

All three jobs share a local setup action that installs dependencies, loads the
exact AMD64 production image, prepares ephemeral certificates, builds the small Go
genesis-verification image, and pulls official Rundler, Node cache-helper, Caddy
and external images per runner. `make preview-images-prebuilt` requires the loaded
production image and never rebuilds it; setup verifies its image ID again after
preparation. Runner SDKs remain untouched. Only
metadata and contracts install bundled Chromium. CLI smoke runs one split-role
transactions scenario; the full catalog, dual-layout and browser suite is opt-in
via `make test-preview-tools-e2e-full` (or its `-prebuilt` variant), outside
ordinary CI. Metadata, contracts, CLI tools and consensus upload retained harness
evidence on success or failure with suite-specific artifact names; x402 retains
its existing log and cleanup behavior. Consensus uses a separate temporary
subdirectory so its uploaded evidence does not include CLI reports.
All image consumers use the common load action with an exact producer artifact
ID and validate source revision, architecture and image ID; none rebuilds a
missing image. Successful loading removes only `production-image.tar` to release
transport disk space, retaining `identity.json` for the later `verify` check.
Load failures or load-time identity mismatches retain the archive for diagnosis. Makefile regressions
exercise the real dependency graph; image handoff regressions cover replacement,
missing images, post-load verification without the archive, and failure cleanup.

Checks appear under `Checks / ...`. Runtime checks use `Runtime acceptance / ...`:
`Production schema and runtime E2E`,
`Preview gateway, genesis, x402, CLI and consensus smoke`, `Preview metadata E2E`,
`Preview contracts, Bundler and ENS E2E`.
Required-check integrations must use these three Preview jobs. Replace separate
`Preview gateway and genesis checks`, `Preview x402 E2E`,
`Preview manual tools and browser E2E` (or `Preview CLI smoke E2E`), and
`Preview consensus and blobs E2E` requirements with the grouped
`Preview gateway, genesis, x402, CLI and consensus smoke` check. The intermediate
`Preview gateway, genesis, x402 and CLI smoke` name also becomes this grouped check.
Foundry appears under each architecture's production verification call.
Docker publication and automatic Vyper updates retain
their separate triggers and permissions.

Each application suite owns two isolated monolith/distributed environments and
performs its own counts, provenance, cleanup and normalized parity. Successful
application tests write `.local/production-results/{hardhat,vyper}.json`;
remove old local result files before repeating a suite because writes are
exclusive-create. Application tests do not require production fixture evidence.
They retain the `runtimee2e verificatione2e` Go build tags for shared harness code;
Vyper does not require a Hardhat client image or CLI.

The runtime overlay replaces the production API and metrics port mappings with
ephemeral `127.0.0.1` bindings, matching PostgreSQL, RPC and worker metrics.
Keep this address consistent across the rendered stack: mixing wildcard and
loopback allocations triggers the Docker 28 port-allocation defect documented
in [Moby #50054](https://github.com/moby/moby/pull/50054). The Compose layout
regressions verify both topologies and the Hardhat/Vyper overlays, including
replacement of inherited fixed ports. Do not reduce replica counts or topology
parallelism to hide a port conflict.

All intermediate artifacts carry exact producer IDs and run/attempt-qualified
names with one-day retention. The final aggregation job waits for all three
jobs, downloads their exact output IDs and checks source, architecture, image,
package, lock, runtime and fixture identities before writing the existing
`acceptance-{arch}.json`. Cleanup failure, missing/duplicate roles, parity failure,
non-native execution or stale evidence blocks acceptance. Failed-job reruns reuse
producer output IDs; expired artifacts require rerunning the producer, never an
image rebuild fallback. Additional isolated environments and artifact transfers
increase runner usage; total CI speedup must be measured.

Common CI gates, Vyper package construction, security/license auditing and the
bounded Go assembler regressions use Node and Go without host Python setup. The compiler
interpreter remains bundled Python WASM. Host Python is needed only for optional
official reference generation and `make benchmark-vyper-wasm`; reference
environments are prepared by `node compiler/vyper/reference.mjs` with uv `>=0.13.4,<1.0.0`.
