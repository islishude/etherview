# Etherview Repository Instructions

Keep this file limited to durable repository rules. Task status and evidence
belong in `PLAN.md` and `docs/plans/`; design detail belongs in
`docs/architecture/` and accepted ADRs. Accepted ADRs remain mandatory even
when their detailed invariants are not repeated here.

## Coding Rule

- Do not preserve backward compatibility.
- Choose the simplest implementation that fully meets the current requirements.
- Prefer established, well-maintained libraries over custom implementations.
- Make architectural decisions for the long term.
  Do not accept a stopgap that only works for now and is meant to be replaced later.

## Workflow

1. Read `PLAN.md`, the relevant child plan, and its linked ADRs and testing
   rules.
2. Before changing implementation, claim one dependency-ready `todo` item by
   marking it `in_progress`. Never let two agents claim the same ID.
3. Preserve existing staged, unstaged, and untracked work, and review all three
   before reporting completion.
4. Finish implementation, regression tests, plan state, acceptance checks, and
   concise verification evidence as one change. Update `PLAN.md` when a child
   plan's status changes.
5. Never delete or reuse work-item IDs. Mark abandoned work `dropped` with a
   reason or `superseded` with its replacement. A `blocked` item must name the
   blocker and the condition that clears it.

Long-lived code TODOs must reference a plan item. Run `make plan-check` after
plan changes.

Completed-plan evidence is historical and may name commands or paths that were
later superseded. Use the current `Makefile`, `docs/testing.md`, and maintained
operator documentation as command truth; do not resurrect a removed test
driver solely because an older evidence entry cites it.

## Restricted automation hosts

- Follow the sandbox-aware command matrix in `docs/testing.md`. Keep the
  Makefile target unchanged when retrying; a host permission workaround must
  not weaken or replace the repository gate.
- When user cache directories are read-only, use repository-specific writable
  cache paths under `/tmp` for npm, the Go build cache, and golangci-lint. Do
  not relocate `GOMODCACHE` by default: the shared module cache is useful even
  when its optional stat-cache writes emit a warning.
- Browser and Docker boundaries are different from ordinary cache writes. If
  macOS denies Chromium Mach bootstrap/process operations, or Docker Buildx
  cannot access its daemon, configuration, or activity state, rerun the exact
  target outside the filesystem/process sandbox with approval. On a host
  already known to enforce those restrictions, request approval before running
  `make test-e2e`, `make deployment-check`, or `make check` (which includes the
  deployment gate) instead of first producing a predictable failure.
- Once a browser process launches, treat a blank page, missing application
  root, or JavaScript console exception as a product/build failure until
  diagnosed. Do not classify it as a sandbox failure or accept a weaker
  single-process browser result without inspecting the first runtime error.
- Stop temporary local servers after diagnosis and confirm their fixed ports
  are free before rerunning repository-owned E2E targets.

## Architecture guardrails

- PostgreSQL is the correctness authority for chain facts, canonicality, jobs,
  leases, users, billing, and runtime events. Redis, NATS, and object storage
  remain optional and disposable.
- `serve --roles=all` and split roles use the same components and persistence
  semantics. Keep the production component manifest, registration, readiness,
  shutdown, and parity tests aligned.
- Identify block-scoped facts by chain and block hash, retain orphan facts, and
  never infer durable coverage from a higher disconnected block.
- Core ingestion never waits for enrichment. Derived output becomes public only
  through the exact generation's lease-fenced, atomically committed stage
  result. Replays never steal active leases.
- Pin each block-scoped RPC operation to one endpoint and exact block identity.
  Never fall back from block-hash state to a height or `latest`, or combine
  results from different nodes.
- Only `api` and `all` may use the optional read pool. It is forced read-only
  and may serve ordinary projections; correctness-sensitive reads,
  authentication, external-call fences, operational state, and all writes stay
  on the writer. A configured reader fails readiness rather than falling back.
- Do not hold a database snapshot across RPC, metadata, compiler, facilitator,
  or other external calls. Copy bounded inputs, close the transaction, call the
  service, then recheck canonicality or ownership before committing.
- Treat external input and providers as hostile: enforce size/time/work limits,
  reject unsafe redirects and network targets, and expose stable typed errors.
  Never log nested upstream errors or raw credentials.
- Use go-ethereum's exported types directly for matching Ethereum protocol
  semantics; do not maintain replacement protocol models or aliases in
  `internal/ethrpc`. Keep RPC ingestion raw-first: supported transaction types
  0 through 4 use geth as their typed authority, unsupported future types fail
  permanently before any block write or coverage advance, and explicit
  transport, redaction, persistence, and public-contract adapters remain
  repository-owned. Preserve the original block's top-level JSON shape in
  `blocks.raw`; PoW uncle headers may appear only in reserved versioned
  root-level metadata that `DecodeStoredBlock` removes. Legacy empty-uncle rows
  decode directly. A legacy row with non-empty uncle hashes but no headers
  fails permanently with `ErrStoredUncleHeadersUnavailable` and requires an
  exact endpoint- and block-identity-bound RPC repair before it can become a
  verified bundle.
- Public quantities beyond JavaScript's safe integer range are strings. Public
  HTTP contracts start in `api/openapi.yaml`; SQL starts in
  `internal/db/queries/`. Regenerate outputs and never hand-edit generated
  files.
- Contract verification compiles bounded Solidity/Yul inputs twice with one
  exact official `emscripten-wasm32` solc-js artifact, discovers candidates
  automatically, and records only declared
  auxdata/library/constructor/immutable transformations. Address publication
  requires a canonical runtime match and an exact immutable result. Dynamic
  compiler catalogs and artifacts are hostile external input: validate the
  catalog generation and artifact SHA-256 before caching, and bind each leased
  job to the artifact format, catalog generation, compiler SHA-256, executor
  kind/policy, and runtime executor digest. The bundled Node permission model
  is defense in depth for trusted checksum-authenticated solc-js, not an
  isolation claim for malicious JavaScript.
- Geas verification is address-only and fixed to `github.com/fjl/geas`
  v0.3.3. Compile required runtime and optional creation entrypoints twice in
  fresh bounded helper subprocesses from an inline read-only virtual
  filesystem, with stack checking. Match byte-for-byte, publish only transitive
  sources with an empty ABI and no transformations, and bind the exact Go
  module checksum plus helper digest under the lease without a solc catalog.

## Security and browser boundaries

- Secrets and server-only configuration never enter logs, URLs, ConfigMaps, the
  embedded SPA, or image layers. Use role-scoped environment or `_FILE` inputs
  and explicit NetworkPolicy egress.
- Native API keys are accepted only through `X-API-Key`; query/form keys exist
  only at the exact Etherscan compatibility boundary.
- API keys, SIWE users, and x402 payers are separate identities. Browser
  sessions and authorization use the PostgreSQL writer; authenticated writes
  require exact same-origin checks and a session-bound CSRF token.
- x402 pricing is an explicit allowlist over bounded native GET operations.
  PostgreSQL fences replay and settlement. An uncertain settlement stays
  unresolved for manual reconciliation and is never automatically retried.
- Browser backend calls use the generated same-origin client. Wallet RPC stays
  inside the injected-provider module and its closed allowlist; raw providers
  never escape. A submitted transaction without a trustworthy matching hash is
  an unknown outcome, not a retryable failure.
- Validate SPA routing, CSP, cache, and security headers against the built
  distribution served by Go, not a Vite development server.

Consult the accepted ADRs before changing a public API, persistent contract,
security boundary, external-service boundary, or monolith/split runtime model;
add or update an ADR when that decision changes.

Update this file when durable commands, invariants, layout, or review rules
change. Add a nested `AGENTS.md` only for genuinely different subtree rules.

## Verification

The Makefile is the command source of truth; `docs/testing.md` defines scope and
evidence rules.

- Toolchains are minimums, not exact ceilings: Go 1.26.5, Node.js 24.18.0, and
  npm 11.16.0. Compatible newer stable versions must pass `make
  toolchain-check`.
- Add regressions for malformed RPC data, reorgs, optional-capability loss,
  numeric limits, concurrency, and security-sensitive parsers.
- Run the smallest targeted tests first, then the applicable common gates.
  `make check` excludes service-backed, browser, parity, load, and soak suites;
  run their explicit targets when the change touches those boundaries.
- `make test-integration` is self-contained: when
  `INTEGRATION_DATABASE_URL` is empty, its Go runner owns a fresh PostgreSQL 18
  Compose project, applies real migrations, runs only integration-tagged
  packages, and removes the volume. Do not require developers to start
  PostgreSQL manually or silently skip this target. A caller-supplied URL must
  identify an explicitly disposable database. Use
  `make test-integration-race` only when the database boundary needs the
  explicit race variant.
- `make test-schema-e2e` and `make test-runtime-e2e` are the production-image
  deployment gates. The runtime suite is Go-owned and covers both the monolith
  and the complete six-application-role split topology, including exact pending
  identity, six-stage publication, a distinct-hash reorg, dependency outages,
  restart, bounded load, and durable/public parity. Extend this suite for new
  cross-process behavior instead of adding another shell smoke driver.
- Keep service orchestration, polling, API/RPC assertions, SQL state capture,
  normalization, and diagnostics in Go. Shell under the test/deployment
  boundary should remain limited to small portable selectors or image
  inspection where direct process tooling is the contract.
- All Makefile Compose and Buildx calls go through
  `.github/scripts/compose.sh` and `.github/scripts/buildx.sh`. They prefer
  Docker plugins and fall back to standalone binaries. Preserve the
  `COMPOSE`, `BUILDX`, and `DOCKER` overrides rather than hard-coding one local
  installation shape.
- Run `make generate-check` after OpenAPI, SQL, generated-client, or embedded
  SPA changes; run `make plan-check` after governance changes.
- A work item is complete only when its targeted tests and applicable common
  gates pass and the child plan records concise evidence.
