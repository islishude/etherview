# Go and frontend tooling

[Development index](../development.md) · Commands run from the repository root.

- [Go lint and build tags](#go-lint-and-build-tags)
- [Formatting and frontend tooling](#formatting-and-frontend-tooling)

## Go lint and build tags

When adding or changing a custom Go build tag, update `run.build-tags` in
`.golangci.yml` in the same change. Include integration, optional-service and
E2E tags, including every tag required by a combined build constraint. Audit
the repository's `//go:build` directives when updating this list; tagged tests
must remain covered by ordinary lint runs. OS and architecture constraints
such as `windows` and `!windows` are selected by the Go target platform, not
enabled together as custom tags.

Apply the configured Go lint rules to test helpers as well as production code.
For simple counted loops, use `for range n`, or `for i := range n` when the
index is needed, as required by `modernize`. When `unparam` identifies a helper
argument that always receives the same value, remove that argument, keep the
fixed value in the helper and update all callers, including tagged tests.
Preserve parameterization where callers actually require different values.
Do not suppress diagnostics or drop build tags to make lint pass.

After changing build-tag coverage or repairing lint findings, run
`make lint-go` and resolve the findings before reporting success. See the
[testing guide](../testing.md#common-gates) for the gate's scope.

## Formatting and frontend tooling

Use `make format` to format repository JavaScript, YAML, Markdown, and the
existing hand-written frontend scope. Root formatting sorts imports; the separate
frontend configuration retains its existing import order. `make preview-lint`
checks all Preview scripts with Oxlint `no-unused-vars` as an error and runs
through both `make lint` and root `pnpm run lint`. Use `make format-check` for a read-only
check; `make lint` and CI include it. Both commands install the locked root pnpm
workspace. `pnpm-workspace.yaml` owns the workspace list, install policy and
dependency overrides; `api`, `web`, `compiler`, and `e2e/hardhat3` share one root
`pnpm-lock.yaml`. Install pnpm 12.18.0 (pinned by `packageManager`), then run
`pnpm install --frozen-lockfile` (or `make pnpm-install`) at the repository root.
When switching an existing checkout from npm, remove the generated root and
workspace `node_modules` directories before installing. Dependency changes use
`pnpm add` in the owning workspace and commit the updated shared lockfile.
Workspace manifests retain their runtime constraints: the compiler requires
exactly Node 26.13.0 and solc 0.8.37, while the Hardhat fixture keeps solc 0.8.30.

The workspace policy enforces engines and disables dependency lifecycle scripts.
Local pnpm must satisfy `engines.pnpm`: at least the pinned minimum, within
the same major version. Run `make toolchain-check` to enforce this range; pnpm
12.16.1 itself does not reliably reject an incompatible `engines.pnpm` range.
`pmOnFail: ignore` keeps the installed pnpm instead of switching to the exact
`packageManager` version; CI uses that exact version. Docker installs the latest
stable pnpm 12 release through the standalone installer and checks its major.
Automatic peer installation is disabled, so the artifact-only Safe fixture's
unused Ethers v5 peer is not installed. Required peers are explicit workspace
dependencies, including the frontend test DOM library. API generation uses
TypeScript 6.0.3 for Hey API, while the Web type-checker remains on TypeScript 7.0.2. Docker uses `node:26.13.0-slim` and curl to download the
[official standalone installer](https://pnpm.io/installation#using-a-standalone-script),
with curl and CA certificates installed for the script and its package downloads,
`PNPM_VERSION=12`, an explicit shell and `PNPM_HOME/bin` on `PATH`, then
uses the same workspace policy with frozen, filtered installs. Dependency caches
mount at `/pnpm-cache`, outside `PNPM_HOME`, so they cannot hide the installed
pnpm runtime and its store links. CI pins
`pnpm/action-setup@v6.1.0`. Dedicated compiler
CI jobs use `pnpm --filter ./compiler install --frozen-lockfile`; a full
`pnpm install --frozen-lockfile` installs all workspaces. Resolve package files
from the owning module instead of assuming a hoisted `node_modules` directory.

After installation, `pnpm run lint`, `pnpm run format`, and `pnpm run format:check`
provide the JavaScript/frontend gates directly. Use
`pnpm --dir web run format` for frontend-only edits.
`make web-lint` runs TypeScript, Oxlint, and a read-only Oxfmt check through the
same pnpm scripts used by CI. The Web Oxlint configuration enables type-aware
`typescript/no-deprecated` checks using the pinned root `oxlint-tsgolint`
dependency. Replace references to declarations marked `@deprecated`, including
types, methods and test assertions, with their supported equivalents. Keep
generated API types under the API generator;
do not format them manually. `api/openapi-ts.config.ts` generates the Hey API
Fetch SDK, domain types and bundled runtime under `web/src/api/heyapi/` from the
API workspace. After changing `api/openapi.yaml`, run `make web-generate` for
the SDK alone, or `make generate` for all generated artifacts and the embedded
SPA. Ordinary `make web-build` and Docker Web builds consume the committed SDK
without running the OpenAPI generator.
Pass `apiClient` explicitly to operation functions, retain domain
aliases, and keep specialized EventSource/raw-response handling at the existing
boundary. `make generate-check` compares the complete regenerated directory.
The [testing guide](../testing.md) owns exact limits,
formatting scope, and tool-policy regression coverage.

### Push worker build

The push-only Service Worker lives in `web/src/workers/push-sw.ts`. Its separate
TypeScript project uses the `WebWorker` library without page DOM globals and
runs as part of the ordinary frontend type check. Shared imports must also be
safe in a worker environment.

Import the entry with Vite's `?worker&url` query and register it as a module
Service Worker. Vite owns TypeScript transformation, shared imports, development
serving and the separate production bundle; no custom worker compiler or public
copy is needed. Worker entries are emitted at the distribution root so their
scope can cover the application without production header overrides. The Vite
development server supplies `Service-Worker-Allowed: /` for source URLs.
Production assets still pass through normal manifest preparation and embedding.
The worker has no fetch handler, offline cache, or notification history.

### UI design changes

Read and follow the [design style guide](../design-style-guide.md) before changing UI
presentation or shared components. Reuse existing components and foundation tokens.
Global visual rule changes must update the guide and affected shared implementation
together; individual pages must not introduce a competing visual system.
Include the guide's [review checklist](../design-style-guide.md#change-workflow-and-review-checklist)
and applicable visual evidence in the PR or completion report. Human review owns
the simple, modern design judgment; existing tests verify measurable behavior.

### Stylesheet ownership

The [stylesheet guide](../../web/src/styles/README.md) owns module responsibilities
and cascade order. Keep common layout in existing Tailwind utilities and
`DesignPrimitives`, complex page rules in their owning CSS module, and shared
theme values in foundation tokens. Colocate self-contained responsive rules
with their owner; retain cross-module overrides in the final responsive module.
Preserve selectors, media conditions, and declaration order during structural
moves, and verify computed layout at the affected breakpoints. Do not add a
preprocessor or bulk-convert existing components for a stylesheet split.
