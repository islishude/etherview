# Quality and generation gates

[Testing index](../testing.md) · Commands run from the repository root.

- [Commands](#commands)
- [Dependency update validation](#dependency-update-validation)

## Commands

- `make test-workflow-scripts`: exercise extracted workflow helpers with temporary
  Git repositories and stub commands, covering metadata, digest export,
  PR flags, toolchain versions, Makefile entry points, production-image/evidence
  handoff and failure propagation without pushing images or PRs.
  `make compose-check` runs the production deployment rendering matrix through
  `.github/scripts/compose-check.mjs`, with shared in-process assertions for
  compiler ownership, credentials and verification-suite isolation.
  Preview deployment behavior is exercised by its existing business E2E gates.
- `make toolchain-check`: require at least Go 1.27.3, Node 26.13.0, and pnpm
  12.16.1 before generating or validating artifacts. Compatible newer stable
  versions are supported, with pnpm restricted to the minimum version's major;
  older, malformed, prerelease, and higher-major pnpm versions fail. Full pnpm installation and compiler builds additionally
  enforce the compiler workspace's exact Node 26.13.0 engine.
- `make plan-check`: run `TestRepositoryPlans` in `internal/plancheck` with
  caching disabled to validate current repository plan links, IDs, statuses,
  dependencies, evidence, and parent/child state. The same test also runs in
  the ordinary Go test suite; fixture tests cover invalid planning documents.
- `make docs-check`: compare maintained high-risk documentation with current
  Makefile/Compose profiles, Preview Genesis lifecycle, compiler runtime
  identity, enrichment stages, and the Etherscan action allowlist. Validate local
  Markdown file links, reference links and heading anchors across repository
  documentation, including new untracked topic files. Fenced examples and
  inline code are excluded; remote URLs are not fetched.
- `make source-check`: run `go test -count=1 ./internal/sourcecheck` to reject
  `.sql` sources outside `internal/db/queries` and `internal/store/migrations`,
  and embedded SQL literals in production Go outside the migration runner and
  validated partition-DDL module. Both checks are ordinary Go tests, with no
  separate CLI or shell script. The filesystem scan includes untracked files
  and Go files outside the active build tags; `.git`, `node_modules`,
  `internal/db/gen` and `internal/api/gen` are excluded. Regressions cover directory
  exclusions, similar directory prefixes, whitespace in paths, and scan failures.
  Disabling the Go test cache here ensures that changes outside the test package
  always receive a fresh scan. `make lint-go` includes this boundary;
  ordinary Go tests also run both checks.
- `make golangci-lint`: enforce the configured Go analysis policy, including
  `depguard` for package direction, native-pgx-only imports in production and
  tests, and test-fixture imports restricted to tests. Type-aware `forbidigo`
  rejects raw execution through pgx, pgxpool, pgconn and the repository's native
  database interfaces, including method values and import/type aliases; unrelated
  methods such as `url.URL.Query` remain allowed. New database interface types
  must be included in the forbidigo policy. Generated sources, `_test.go`,
  `internal/store/migrate.go` and `internal/store/partition.go` retain their raw
  execution exemptions. Revive's `file-length-limit` caps both hand-written
  production and test Go files at 2,500 physical lines, counting comments and
  blank lines (`max: 2500`, `skip-comments: false`, `skip-blank-lines: false`).
  Only this revive rule is enabled; generated files remain excluded.
  `.golangci.yml` enables tests and all repository custom build tags through
  `run.build-tags`, so integration, optional-service and E2E sources receive
  the same analysis. New custom tags must be added in the same change as their
  sources; platform constraints remain selected by `GOOS`/`GOARCH`.
  Lint analyzes tagged tests without executing them: a passing lint run does
  not establish integration or E2E runtime acceptance. The lint-policy fixture
  below checks integration-tag coverage, not completeness of the tag list.
  `make lint-go` runs both this target and `source-check`.
- `make test-lint-policy`: run the installed golangci-lint against an isolated
  fixture module with the unchanged repository configuration and pinned pgx.
  Assert exact diagnostics for forbidden imports and raw execution, plus allowed
  tests, generated sources, migration/partition paths, typed sqlc access and
  unrelated same-name methods. File-length fixtures check 2,499/2,500/2,501 lines
  in production and test files, with comments, blank lines and optional final
  newlines, plus the generated-file exemption. Tagged fixtures verify
  integration-tag coverage.
  This regression runs as part of `make golangci-lint`, including the CI lint job;
  it requires the tool installed by `make install-lint-tools`.
- `make openapi-check`: validate `api/openapi.yaml` against the bundled OpenAPI
  schema with pinned Swagger Parser, resolve local references, and fail on
  invalid types, missing required fields, or broken references. HTTP reference
  resolution is disabled, so validation needs no network after dependency
  installation. Runs CLI regressions including null versus empty/omitted
  parameter arrays. Both `make check` and the CI quality job include this gate.
- `make generate-check`: regenerate OpenAPI, SQL, and embedded frontend outputs
  and fail on a diff. It snapshots the checked-in baseline in a temporary
  directory before regeneration, so it also works before the repository has an
  initial Git `HEAD`. The Hey API output is a complete directory snapshot;
  regeneration removes stale files and recursive comparison detects additions,
  removals and content drift. `client-types.test.ts` is compiled by the frontend
  TypeScript gate and locks request/body/header constraints, discriminated
  responses, nullable fields and string quantities. Runtime client tests cover
  URL confinement, credentials, errors, 204, raw binary responses and cancellation;
  the embedded browser gate retains native EventSource recovery acceptance.
- `make solidity-format` / `make solidity-format-check`: format / check every
  Git-tracked `.sol` file with Forge's default style, including Hardhat, runtime
  and Preview fixtures outside Foundry projects. Install Foundry 1.9.0 first;
  `FORGE` can select its executable. Missing or mismatched versions fail clearly.
  Checks never rewrite sources. `make format`, `make format-check`, and
  `make lint` include the corresponding Solidity target, so `make check` also
  checks Solidity. The CI lint job installs the pinned version through
  `foundry-rs/foundry-toolchain`. After formatting source-owned Preview contracts,
  regenerate affected artifacts through their maintained generators and run
  `make preview-contracts-check` to verify source and artifact consistency.
- `make format` / `make format-check`: write formatting / check without writing,
  using Forge for Solidity and locked Oxfmt 0.70.0 from the root pnpm workspace. The root configuration covers
  `.js`, `.mjs`, `.cjs`, `.yaml`, `.yml`, and `.md`, including hidden workflow
  and script directories. The existing frontend scope runs separately, using
  its own configuration. Root `pnpm run format` and `pnpm run format:check`
  run repository formatting followed by `pnpm -r run format` and
  `pnpm -r run format:check`, respectively. Recursive execution discovers child
  workspaces with the matching script and excludes the root aggregator.
  `make format` and `make format-check` use these root entry points.
  `make lint` runs Go checks and root `pnpm run lint`, which runs Preview lint,
  `pnpm -r run lint`, and repository format checking. Child workspace lint
  scripts own their format checks, so frontend formatting runs once.
  Individual `make web-lint` and `make preview-lint` targets remain available.
  Root exclusions cover dependencies, build outputs, local runtime files,
  generated `*.gen.*` / `*_generated.*` files, third-party license texts,
  `internal/**/testdata/**`, and Helm `templates/**`; ordinary Helm values and
  Chart YAML remain included. Git-ignored files are also excluded.
  Markdown preserves prose wrapping and leaves fenced code untouched. Both
  configurations retain two spaces, double quotes, semicolons, 100 columns,
  LF and trailing commas. Root formatting enables import sorting; frontend
  import order and the other sorting options remain unchanged. Tooling-policy regressions cover
  selection, exclusions, read-only failure, parse errors, and idempotence.
- `make preview-lint`: run Oxlint on all scripts under `preview/`, including
  contract generators and tests. Unused imports and variables fail with
  `eslint(no-unused-vars)`. Both `make lint` and root `pnpm run lint` include this gate.
- `make web-lint`: run TypeScript project checking, pinned Oxlint 1.85.0, and
  Oxfmt 0.70.0 format checking. Type-aware lint uses pinned `oxlint-tsgolint`
  7.0.2005 and rejects `@deprecated` references through
  `typescript/no-deprecated`, including aliased imports, types, methods and
  test code. Oxlint checks hand-written `src` TS/TSX for unused code, hook
  placement/dependencies, and size. Production files/functions
  are capped at 1,400/400 lines; test files/functions at 2,500/1,000. Blank lines
  are excluded, comments count, and IIFEs are exempt from function length.
  Production classic cyclomatic complexity is capped at 150; test complexity
  is excluded. This is a new metric baseline, not an equivalent conversion of
  the former Biome cognitive complexity threshold of 75. Generated OpenAPI
  types remain the only source exception in `web/.oxlintrc.json`.
  `pnpm --dir web run format` formats hand-written frontend sources, CSS,
  scripts, configuration, HTML, and Playwright tests; `format:check` is read-only.
  `web/.oxfmtrc.json` excludes generated API types, lockfiles, distributions,
  dependencies, browser reports, and Go files. Formatting uses two spaces,
  double quotes, semicolons, 100 columns, LF, and trailing commas without import,
  Tailwind-class, or package-key sorting. `make web-test` includes executable
  tooling-policy regressions for limits, exclusions, hooks, deprecated symbols,
  and idempotence. Deprecated-symbol fixtures exercise installed React types,
  aliased imports, DOM methods, and references into ignored generated modules;
  unrelated non-deprecated declarations with the same name remain valid.
- `make test`: Go and frontend unit tests.
  Browser ABI regressions include the published Solidity 0.5.3 ERC1820Registry
  ABI and unmodified native Standard JSON ABI output from official solc-js
  0.4.15, 0.4.16, 0.5.11 and 0.6.0. The historical fixture in
  `web/src/contracts/testdata/historical-solidity-abi.json` records each compiler
  URL, manifest-verified SHA-256, exact version and source. Reproduce with
  `Historical.sol`, language `Solidity`, and `outputSelection: {"*":{"*":["abi"]}}`
  through the compiler's native Standard JSON entry point; no wrapper ABI
  translation is applied. Tests consume checked-in fixtures without downloads
  and cover historical defaults, conflicting flags, entry boundaries,
  recursive tuples, resource limits, read/write classification and forms.
- `make test-race`: Go tests with the race detector.
- `make lint`: Go formatting/vet, the repository's golangci-lint v2 policy
  (including `standard`, `modernize`, `unparam`, `depguard`, `forbidigo`, and `revive`) across ordinary tests plus tagged
  integration, Hardhat, Foundry, and runtime E2E source, and TypeScript type
  checking, Oxlint, and repository/frontend format checking.
- `make security-check`: `govulncheck`, API-generator, frontend, and Hardhat 3
  fixture and compiler dependency audits, secret scan, and security-focused tests.
  The WASM gate performs local runtime/manifest/payload and actual-distribution
  integrity checks, including nested vendors. Both runtimes retain tampering,
  unexpected/missing file, symlink, compiler identity and metadata regressions.
  Custom PyPI/OSV advisory queries and approval records are retired under
  [ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md#security-validation).
  The single root pnpm audit covers all four workspaces and shared tooling and
  must report zero high-severity vulnerabilities; the API generator's
  transitive parser and glob dependencies are constrained by audited
  overrides.
  pnpm installs do not run automatic audits. The security job runs the explicit
  root audit and fails if the audit cannot complete.
- `make license-check`: Go, production frontend/compiler, all Vyper package
  and pinned shared WASM component notice checks. Checked-in texts under
  `licenses/` are verified read-only; missing or changed notices fail the gate.
  The frontend and compiler gates
  resolve their installed production dependency closures from each owning module,
  follow pnpm symlinks, and require a license result for every selected package,
  including transitive dependencies and npm aliases. Missing required dependencies
  fail closed. Workspace regressions also execute
  both distinct solc versions and check the pinned Pyodide and client versions.

## Dependency update validation

`make test-dependency-updates` is an offline gate included in
`make test-workflow-scripts`. It validates registered version positions and
Dependabot ownership, numeric stable-version ordering, major/zero-major policy,
linked updates, upstream failures, clean-worktree protection and isolated
candidate preparation. Publication tests use a temporary local Git remote to
exercise PR-creation failure recovery and repeat runs without rewriting a
branch; HTTP and GitHub API results are fixtures, not live publication evidence.
JSON regressions cover whitespace and property order, multiple registered fields
in one file, missing or non-string fields, version disagreement, duplicate field
ownership and rejection of unrelated field changes during publication.

Candidate preparation installs or resolves the selected tool/package where
applicable, regenerates only its allowed locks, and runs workflow-script and
documentation regressions plus `git diff --check`. The publisher verifies the
base commit, patch hash, file inventory, normal file modes and exact registered
source replacements. Candidate scripts never receive the publishing token.
A failed query or check is a failure, never evidence that dependencies are current.

After the workflow reaches `main`, manually dispatch it and inspect the discovery
artifact and job summary. An actual available candidate must produce one PR;
repeat dispatch must retain that PR. Approve its workflows and require the full
existing CI, including native runtime gates for affected images, before merging.
If there is no newer version, record discovery as tested and publication as not
tested; do not manufacture an obsolete or downgraded candidate. Local fixtures
cannot establish GitHub permissions, hosted scheduling or real PR CI behavior.
