# P70 — Release

Status: `blocked`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0019: Authenticated genesis state import](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0020: SIWE user sessions](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0021: x402 request billing](../decisions/ADR-0021-x402-request-billing.md)
- [ADR-0022: Go-ethereum type and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0025: Historical execution analytics](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0026: Current capability status and numeric canonical tips](../decisions/ADR-0026-current-capability-status-and-numeric-canonical-tips.md)
- [ADR-0027: Process-native API TLS](../decisions/ADR-0027-process-native-api-tls.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P70-T01 | blocked | P10–P66 | Execution/API/token/proxy/verification/authentication/billing conformance matrix | conformance suite |
| P70-T02 | blocked | P10–P66 | Threat model, security audit, dependency, compiler, session, and payment supply-chain review | security gates |
| P70-T03 | blocked | P10–P66 | Monolith/split E2E, migration/rollback, outage, reorg, payment, and soak suite | release CI |
| P70-T04 | blocked | P60 | 500 RPS reference capacity report and tuning guide | load report |
| P70-T05 | blocked | P00–P66 | User/operator/API/authentication/billing/runbook/upgrade documentation | doc review and link check |
| P70-T06 | todo | P70-T01–P70-T05, P70-T08, P70-T09 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release | release verification |
| P70-T07 | done | P60 | Database read/write pool split configuration, deployment wiring, and capacity guidance | helm config/schema tests |
| P70-T08 | done | P10, P20, P30-T07, P40, P50, P60 | Authenticated local/remote genesis account state, predeploy enrichment, native API, and block-zero UI | root, persistence, API, browser, security, and split-role tests |
| P70-T09 | done | P10, P20, P30-T07, P40, P50, P60 | Replace duplicative Ethereum RPC/domain types and codecs with reviewed go-ethereum equivalents while retaining explicit hostile-input, persistence, and public-contract adapters | focused compatibility, integration, generation, security, license, and common gates |
| P70-T10 | done | P60 | Configurable process log level and JSON/text output across file, environment, CLI, and deployment surfaces | config, CLI, observability, Compose, and Helm tests |
| P70-T11 | done | P50 | Keep embedded-browser native-value assertions aligned with configured decimal display | focused Playwright E2E and common frontend gates |
| P70-T12 | done | P20, P60 | Align durable stage-name validation with the deployed `state_diff@1` manifest | focused stage validation and Compose runtime smoke |
| P70-T13 | done | P50, P60 | Split the full-stack Preview Compose deployment into all seven runtime roles | Compose render assertions and Preview runtime smoke |
| P70-T14 | done | P10, P60 | Add reporter-fenced rate-limited sync progress and durable worker outcome logs | focused logging, race, deployment, and Preview tests |
| P70-T15 | in_progress | P30-T02, P60, P70-T13 | Enable Preview public verification and NFT metadata with a digest-pinned isolated compiler runtime | Compose render, compiler preflight, image-boundary, and Preview runtime tests |
| P70-T16 | done | P20, P40, P50, P60 | Etherscan-inspired execution analytics with `stats@3`, reorg-safe hourly rollups, native history APIs, and overview/detail charts | stage, migration, API, browser, reorg, load, and production Compose E2E |
| P70-T17 | done | P20, P40, P50, P60 | Report current Trace and historical-state capability accurately and select exact state/ABI observations by numeric block height | PostgreSQL, API, browser, ABI, reorg, and Preview tests |
| P70-T18 | done | P40-T10, P50-T12 | Release validation for address origins, exact ERC-20 balances, and the add-network browser flow | PostgreSQL integration and embedded Playwright E2E |
| P70-T19 | done | P20, P40, P50, P60 | Go-native managed PostgreSQL integration tests and production-Compose schema/runtime E2E orchestration | integration, schema, runtime, outage, reorg, parity, and load tests |
| P70-T20 | done | P60 | Optional native TLS for API listeners with Preview-local Compose and Helm certificate delivery | config, HTTPS service, Preview Compose, Helm, security, and common gates |
| P70-T21 | done | P30-T11, P40-T06 | Hardhat 3 Etherscan-provider source-verification submission and GET status-polling compatibility | handler goldens, pinned Hardhat 3 provider test, security, documentation, and common gates |
| P70-T22 | done | P70-T16, P70-T19 | Clock-stable historical analytics rollup integration regression | targeted managed PostgreSQL regression and governance gates |
| P70-T23 | in_progress | P70-T20 | Keep the production-container TLS runtime fixture readable by the fixed non-root UID on native Linux hosts | focused file-mode regression and production Compose runtime E2E |
| P70-T24 | done | P70-T19 | Replace noisy distributed runtime output with phase-bound failure summaries and retained CI diagnostics | focused orchestration regressions and production Compose runtime E2E |

## Acceptance

- [ ] Every P00–P66 plan and root release gate is complete with evidence.
- [ ] Clean deployment, upgrade, rollback, backup/restore, and repair procedures
      are independently reproducible.
- [ ] Security findings have no unresolved critical/high issue.
- [ ] Reference capacity target passes with documented hardware and dataset.
- [ ] Published artifacts are reproducible, checksummed, signed, and accompanied
      by an SBOM.
- [x] P70-T10: every configuration-bearing command applies exact lowercase
      `debug|info|warn|error` and `json|text` logging settings with CLI,
      environment, file, and default precedence.
- [x] P70-T10: JSON and text handlers retain the same bounded fields,
      redaction, trace correlation, and stable HTTP internal-error boundary.
- [x] P70-T07: only API-bearing processes open the optional read-only pool;
      startup validates its schema and chain identity, readiness covers both
      pools without automatic fallback, and every correctness-sensitive read or
      write remains writer-routed.
- [x] P70-T07: configuration, Compose, Helm Secret/ExternalSecret wiring,
      effective connection bounds, and API-only capacity accounting have
      regression coverage and pass the applicable repository gates.
- [x] P70-T13: the full-stack Preview runs the production union of all seven
      split roles, exposes public and metrics ports only from the API role, and
      keeps the API session pepper out of migration and worker containers.
- [x] P70-T14: only the active PostgreSQL sync-status reporter emits changed
      sync progress, immediately for its first valid state and then no more than
      once per bounded configured interval, without idle heartbeats or delaying
      failure and safety-boundary logs.
- [x] P70-T14: enrichment, trace, verification, metadata, and maintenance
      outcomes emit bounded event-driven logs only when their worker observers
      report a completed durable transition; API request logging remains the
      existing per-request boundary and catalog success is logged only after an
      executed sweep.
- [x] P70-T19: `make test-integration` owns a fresh PostgreSQL 18 lifecycle
      when no external disposable URL is supplied; the explicit race variant,
      production-image schema E2E, and unified plugin/standalone Compose
      selector share maintainable Go orchestration.
- [x] P70-T19: the build-tagged production Compose E2E replaces the shell smoke
      and drives monolith plus all seven split roles through exact pending,
      contract creation/failure, six-stage publication, distinct-hash reorg and
      analytics repair, replica survival, RPC/PostgreSQL outages, restart,
      API/SSE/SPA, bounded-load, and final parity assertions.
- [x] P70-T19: the managed integration, production-image schema, and full
      runtime E2E targets pass with a working Docker daemon; failures retain
      scenario artifacts and timestamped Compose logs.
- [x] P70-T20: configuring one absolute certificate/key pair makes only the
      public `api`/`all` listener serve TLS 1.2+ with HTTP/2, fails before
      binding on invalid material, and preserves default HTTP plus the plain
      operations listener when TLS is absent.
- [x] P70-T20: Preview Compose and Helm deliver certificate material only to
      API-capable main containers; enabled Helm probes, Service, and Ingress
      backend use HTTPS while external Ingress termination remains independent.
- [x] P70-T21: the Etherscan V2 compatibility boundary accepts authenticated
      Hardhat 3 source-verification submission by POST and status polling by
      GET while retaining the existing authenticated POST status method.
- [x] P70-T21: a dependency-locked Hardhat 3 provider regression exercises
      unverified source lookup, Standard JSON submission, pending status, and
      successful status against the real compatibility handler without adding
      Hardhat 2 or multi-provider behavior.
- [x] P70-T22: the historical analytics regression derives its worker clock
      from PostgreSQL scheduling time, so a hard-coded date cannot make newly
      dirtied hours appear deferred while newest-first and reorg assertions
      retain deterministic historical buckets.
- [ ] P70-T23: the ephemeral TLS fixture remains private on the host while its
      two read-only bind-mounted files are readable by the production image's
      fixed non-root UID on native Linux Compose hosts.
- [x] P70-T24: successful runtime E2E output contains only bounded phase
      progress, while failures identify the exact mode and phase and retain one
      complete redacted diagnostic bundle for local and CI inspection.
- [x] P70-T16: `stats@3` publishes receipt-authenticated execution fees,
      priority fees, failed transactions, and successful top-level creations;
      additive UTC hourly rollups, dirty generations, and fenced newest-first
      backfill expose only complete canonical history.
- [x] P70-T16: generated native overview/detail APIs and the embedded
      bilingual `/charts` plus `/charts/:metric` experience expose the fixed
      execution-layer metric allowlist, exact decimal values, URL-bound
      controls, zoom, CSV, and accessible table fallback without external
      resources.
- [x] P70-T16: the production Compose E2E creates two distinct hashes at the
      same height, demonstrates the affected public rollup changing after
      detach/attach, and verifies retained orphan and journal closure. This
      deterministic gate replaces the non-distinct Reth `debug_setHead`
      Preview attempt.
- [x] P70-T17: canonical state and ABI observation queries order numeric block
      columns rather than text projections; address reads bind balance, nonce,
      and account type to the highest canonical hash and reject a result if
      canonicality changes across the RPC boundary.
- [x] P70-T17: status State reports configured historical-state RPC capability,
      while Trace reports the exact current indexed block's `trace@1`
      publication state in the same PostgreSQL statement snapshot and cannot
      reuse a replaced hash's result.
- [x] P70-T17: generated API descriptions and the embedded bilingual status
      page call the section “Data capabilities and current completeness” /
      “数据能力与当前完整度” without changing the public response shape, same-origin
      API boundary, or persistent schema.
- [x] P70-T08: an optional bounded Genesis JSON source is authenticated against
      block zero and exposes exact EOA/predeploy account facts through
      PostgreSQL, proxy/ABI enrichment, native API, and the embedded block-zero
      UI; missing input remains explicitly unavailable.
- [x] P70-T09: reviewed go-ethereum types own recognized protocol semantics,
      including transaction types 0 through 4, while unsupported future
      transaction types fail permanently and atomically before persistence or
      coverage advancement; `blocks.raw` keeps the original block top-level
      shape, versioned root metadata carries validated PoW uncle headers and is
      stripped by `DecodeStoredBlock`, legacy empty-uncle rows remain readable,
      and legacy non-empty uncle hashes without headers fail permanently
      pending exact RPC-backed repair. Receipt fields outside the trie,
      including gas-use deltas, creation address, and effective gas price, are
      authenticated against the matching transaction and block context.
      Transport limits, redaction, supported-object unknown-field retention,
      SQL persistence, and public contracts remain explicit compatible
      adapters.

## Current Blockers

P00 through P65 are complete, while P66 is `blocked`: P66-T08 still needs
operator-provided Base Sepolia funding, payer credentials, a compatible
staging facilitator and priced route, the matching writer and independent RPC
endpoint, and the deployed image/build digest. One preserved successful
payment and writer/chain reconciliation report clears P66-T08 and makes P66
`done`.

P70-T01, P70-T02, P70-T03, and P70-T05 are blocked by their P66 dependency.
They become claimable after the P66-T08 live report is recorded and P66 is
`done`; none of their conformance, security, release-CI, or documentation
deliverables is represented as complete before then.

P70-T04 is blocked on an operator-provisioned reference environment for the
final clean revision and image digest: at least two failure domains with room
for the documented 9-to-18-pod topology, HA PostgreSQL sized for its connection
budget, a named representative chain snapshot with cardinalities, healthy
purpose-specific RPC behavior, and independent timestamp-aligned resource
monitoring. Running the exact 500 RPS/30-minute target there and preserving the
load report, resource peaks, monitoring data, and tuning guide clears the
blocker.

P70-T15 is in progress with an explicit Preview-only verification-download
exception for local transparent-proxy fake IPs. The exception must remain
disabled by default, apply only to the Preview `verify` role, and leave API,
metadata, production Compose, and the shared public-network policy unchanged.
A fresh Preview start must keep all seven roles ready and demonstrate catalog
publication, one real Solidity and one real Vyper compilation through the
exact isolated runner, and one bounded public NFT metadata fetch before this
item can become `done`.

P70-T20 is complete, including the aligned `https://localhost:8080` browser,
session-origin, and wallet explorer metadata contract. P70-T06 remains `todo`
and dependency-gated until P70-T01 through P70-T05, P70-T08, and P70-T09 are
all complete; the v1 release cannot close before those gates.

## Evidence

- P70-T24 implementation and evidence: runtime Compose projects and host Docker
  helpers now capture successful command output silently, record the active
  mode and phase, and write `failure-summary.txt`, `compose-ps.txt`, and the
  complete timestamped `compose.log` before teardown. The terminal retains only
  the original Go failure, bounded summary, and one `compose ps`; CI reuses the
  prebuilt image, retains successful topology evidence for final parity
  failures, and uploads only failed-run diagnostics for seven days. Focused
  executor and diagnostic regressions pass. Both `make test-runtime-e2e`
  (70.675 seconds) and
  `TMPDIR=/private/tmp RUNTIME_E2E_KEEP_ARTIFACTS=true make
  test-runtime-e2e-prebuilt` (68.631 seconds) pass monolith, seven-role split,
  reorg, recovery, load, TLS, and parity checks; the retained modes contain all
  three diagnostic files plus API, durable-state, and load snapshots. `make
  check`, `make plan-check`, and `git diff --check` pass. The first common-gate
  attempt reached `npm audit` but encountered `ECONNRESET`; the complete rerun
  with working registry access passed.
- P70-T23 diagnosis: GitHub Actions job
  [90600001114](https://github.com/islishude/etherview/actions/runs/30459040132/job/90600001114)
  passed the production build, image-boundary, and PostgreSQL schema steps but
  failed the runtime E2E after 4 minutes 37 seconds. The failure began with the
  P70-T20 TLS runtime check: it created host-owned certificate and key files
  with mode `0600`, then bind-mounted them into the production API container
  running as UID/GID 65532. Native Linux preserves that ownership and mode, so
  the API cannot read the key and HTTPS readiness consumes the three-minute
  wait. Docker Desktop for macOS did not reproduce that host permission
  boundary.
- P70-T23 implementation and local evidence: the temporary directory remains
  private while the two files are immutable mode `0444` and still mounted
  read-only only into the API service. The focused file-mode regression,
  `make test-runtime-e2e` (monolith and all seven split roles, including
  process-native HTTPS), `make plan-check`, and `git diff --check` pass. The
  exact native-Linux GitHub Actions rerun remains required before P70-T23 is
  marked `done`.
- P70-T04 harness correction: `make test-soak` now fixes the release target at
  500 RPS for 30 minutes with a five-second request timeout, p95 below 500 ms,
  error rate below 0.1%, lag no greater than two, and at least 99% successful
  throughput. The regression proves the bounded final drain can retain more
  than 495 RPS while the former 475 RPS floor fails; dropped admissions and
  failed responses still count as errors.
- P70-T04 local calibration, not closure evidence: an isolated clean archive of
  revision `5966347f873adccaf6aa744d1f9dc460298dd124` sustained 15,000/15,000
  successful responses at 500.014 RPS for 30 seconds, with p95 3.356 ms, p99
  7.433 ms, zero final lag, and ready/complete core status. It used a
  single-host monolith, empty local chain, and sparse container observations,
  so it neither represents the named reference topology/dataset nor satisfies
  the 30-minute capacity acceptance gate.
- P70-T20 final origin regression: Preview's public URL and wallet explorer
  metadata now use the documented `https://localhost:8080` browser origin,
  preserving exact SIWE origin checks and the localhost wallet-RPC exception.
  Focused config/origin, HTTP API, and race tests plus Compose rendering pass.
- P70-T15 implementation: Preview enables public verification and NFT metadata,
  injects the same exact local runner content digest into API provenance and
  the verify role, and fixes the verify component graph to start its compiler
  catalog refresher. A networkless one-shot grants only the compiler cache and
  client volumes to UID/GID 65532; an exact-pinned CLI then proves cache
  writability, loads and executes the fixed `linux/amd64` runner under the
  production sandbox limits, and exposes only a private nested-daemon network
  to verify. No application container receives the host Docker socket.
- P70-T15 bounded runtime evidence, not closure evidence: the exact
  `etherview-compiler-runner@sha256:a3affbddab6c198b7eee69cfc9b4b6682aa5e9e2f3802e606e7881c51f2ff02e`
  image loaded and executed as `linux/amd64`; cache ownership was
  `65532:65532` with mode `0750`, and both volume init and compiler preflight
  exited zero. Compose's own `--wait` returned success before verify entered a
  catalog-download restart loop. The Go-owned Preview checker then rejected
  that topology by inspecting every required service and one-shot, probing all
  seven role readiness endpoints, checking the HTTPS feature contract and
  exact runner binding, and observing restart stability.
- P70-T15 blocker evidence: Docker's embedded resolver returned
  `198.18.16.46` for `binaries.soliditylang.org`; the public-network policy
  correctly rejected that RFC 2544 address and verify reported the stable
  redacted `download compiler catalog` failure. No public-DNS bypass, weakened
  `PublicIP` rule, or mutable address snapshot was added. Focused ordinary/race
  tests, vet/lint, Compose rendering, production image-boundary checks, and
  whitespace validation pass, but no real catalog, Solidity/Vyper compile, or
  metadata fetch is claimed.
- P70-T15 Preview fake-IP repair: only the Preview `verify` role sets the
  explicit `ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS`
  escape hatch. It is disabled by default and absent from API, metadata, all
  other Preview roles, base Compose, and Helm. Focused config, verification,
  and application tests plus `make lint`, `make compose-check`, `make
  plan-check`, and `git diff --check` pass. `make recreate-preview` rebuilt the
  exact runner and all application roles while preserving PostgreSQL and Reth;
  the complete topology passed the 15-second restart-stability check, verify
  remained running with restart count zero, and PostgreSQL published Solidity
  generation 1 with 94 entries and Vyper generation 2 with 52 entries. This
  removes the catalog restart blocker; real compiler execution and bounded NFT
  metadata evidence remain before closure.
- P70 final common gates: `make check`, `make docker-image-check`,
  `make plan-check`, and `git diff --check` pass. The aggregate check covers
  ordinary and race Go suites, all 149 frontend tests, generation drift,
  vulnerability, secret, dependency, and license checks, Docker build
  validation, Compose rendering, and Helm lint/render tests.
- P70-T22 regression: the newest-first/reorg integration fixture now advances
  its injected worker clock to at least the maximum PostgreSQL
  `next_attempt_at` and `dirtied_at` for the active dirty rows, both before the
  initial rollups and after the replacement branch publishes. Historical block
  timestamps remain fixed, so bucket identity and series assertions stay
  deterministic without expiring when wall time passes a hard-coded date.
- P70-T22 verification: the managed `./internal/integration` package passes
  with all adjacent PostgreSQL regressions, and
  `DOCKER_CONFIG=/tmp/etherview-docker-config make test-integration` passes
  migrations plus all six integration-tagged packages. `make plan-check` and
  `git diff --check` pass.
- P70-T21 compatibility: `contract.checkverifystatus` accepts authenticated
  GET and POST while source submission remains POST-only. Golden handler tests
  cover both methods, query-key stripping, missing or invalid credentials,
  wrong chain, missing GUID, and non-GET/POST rejection without backend
  dispatch. The maintained compatibility matrix documents the Hardhat 3
  `chainDescriptors` configuration and explicit `verify etherscan` command.
- P70-T21 provider and gates: the dependency-locked
  `hardhat@3.11.1`/`@nomicfoundation/hardhat-verify@3.0.21` regression drives
  the official Etherscan provider through GET source lookup, POST Standard JSON
  submission, GET pending status, and GET success status against the real
  authenticated handler. `go test -race ./internal/etherscan ./internal/auth
  ./internal/httpapi`, `make test-hardhat3-verify`, `make plan-check`,
  `git diff --check`, and `make check` pass. The Hardhat fixture audit reports
  no high or critical vulnerabilities; eight upstream low-severity findings
  have no available fix.
- P70-T20 runtime: `server.tls_cert_file` and `server.tls_key_file` accept
  paired absolute YAML or environment paths. The API loads one matching PEM
  key pair before binding, configures Go TLS with a TLS 1.2 minimum, serves
  through `ServeTLS` with HTTP/2, and preserves the existing HTTP path when
  both fields are empty. Partial, malformed, unreadable, or mismatched input
  fails before listener creation without fallback; the operations listener is
  unchanged.
- P70-T20 deployment and documentation: the base Compose deployment remains
  HTTP and the removed `compose.tls.yaml` is replaced by one Preview-local
  workflow. `make preview-cert` explicitly installs mkcert trust and writes an
  ignored localhost/loopback pair with a mode-0600 private key; Preview mounts
  it read-only only into `api`, and start/recreate preflight it without changing
  host trust. Helm's disabled-by-default `apiTLS` values retain role-scoped
  Secret delivery and HTTPS probes, Service, Ingress backend, and test wiring.
  ADR-0027 and maintained deployment/operations guidance describe both paths.
- P70-T20 verification: focused ordinary and race tests for
  `./internal/config`, `./internal/httpapi`, and `./internal/app` pass.
  `make preview-cert`, `make start-preview`, `make compose-check`,
  `make helm-check`, `make security-check`, `make plan-check`,
  `GOLANGCI_LINT_CACHE=/tmp/etherview-golangci-lint-cache make check`, and
  `git diff --check` pass. The local Preview returns trusted HTTPS readiness
  with HTTP/2 while its plain HTTP operations readiness remains healthy. The
  full check includes all 144 frontend tests, ordinary/race Go suites, audits,
  licenses, generation, Docker build checks, and deployment rendering.
- P70-T20 production E2E: `make test-runtime-e2e` rebuilds the production
  image and passes the existing monolith and seven-role split scenarios. After
  split parity, the Go harness creates a temporary test-only Compose override,
  recreates only the API, and verifies a trusted TLS 1.2+ HTTP/2 readiness
  response without mkcert. The complete run passes in 66.85 seconds: monolith
  31.03 seconds and distributed 35.82 seconds.
- P70-T19 implementation: `.github/scripts/compose.sh` and
  `.github/scripts/buildx.sh` select the Docker plugins or standalone
  binaries; `internal/testcompose` owns project arguments, random host-port
  resolution, lifecycle, and diagnostics.
  `cmd/testintegration` provisions PostgreSQL 18 unless an explicit disposable
  URL is supplied, while `cmd/testschemae2e` drives the production migration
  image. The build-tagged `e2e/runtime` Go suite replaces the 651-line runtime
  shell driver and its psql state script; the test-only load image stage is no
  longer built. Its bounded Go RPC fixture adapter removes only Anvil's orphan
  `blobGasPrice` observation and leaves complete blob-fee pairs untouched.
- P70-T19 local verification: `go test ./...`,
  `go test ./internal/testcompose ./cmd/testintegration
  ./cmd/testschemae2e`, `go test -run '^$' -tags=runtimee2e ./e2e/runtime`,
  focused ordinary/tagged `go vet` and `golangci-lint`, `make compose-check`,
  `make plan-check`, and `git diff --check` pass. Compose and Buildx exercised
  the available standalone binaries. `make test-integration` owns PostgreSQL
  18, applies migrations through `0028`, and passes every integration-tagged
  package; `make test-schema-e2e` validates the same schema through the
  production image; `make test-runtime-e2e` passes monolith and all-seven-role
  layouts with distinct-hash reorg, six-stage publication, outage/restart,
  replica-survival, API/SSE/SPA, bounded-load, and final parity checks.
- P70-T18: P40-T10 and P50-T12 implementation checks pass, including the full
  ordinary Go suite, 144 Vitest tests, frontend lint/build, generated-contract
  drift checks, plan and whitespace validation, and the aggregate `make check`
  ordinary/race, security, license, Dockerfile, Compose, and Helm gates. The
  address-assets E2E now scopes its repeated exact-RPC confidence label to the
  named NFT region instead of assuming the label is unique across ERC-20 and
  NFT holdings. The focused capability flow and all 9 embedded Playwright
  cases pass with the documented bundled-Chromium single-process fallback.
  The managed `make test-integration` PostgreSQL 18 lifecycle now passes the
  release validation without requiring `INTEGRATION_DATABASE_URL`.
- P70-T17 implementation: `PostgresCanonicalSource.Tip` and both ABI history
  lookups now order qualified numeric columns while converting quantities to
  text only for scanning. Address state remains EIP-1898 block-hash pinned and
  rechecks canonicality after RPC. Startup maps the configured historical-state
  probe report to State, while the status reader joins the contiguous indexed
  height and hash to the exact `trace@1` stage result in one statement and maps
  `complete|unavailable|failed` without treating absent publication as
  complete. ADR-0026 records these semantics; OpenAPI and the bilingual page
  describe State as a capability rather than `state_diff@1` history.
- P70-T17 regression evidence: focused ordinary and race tests for
  `./internal/state`, `./internal/query`, `./internal/enrich`, and
  `./internal/app` pass. PostgreSQL 18 integration covers canonical heights
  99/100/169 plus same-height hash replacement, address canonicality change,
  and ABI code/proxy observations at 99 versus 100/169. The full integration
  suite, 142 web tests, all 9 embedded Playwright tests, `make
  generate-check`, `make plan-check`, `make compose-runtime-smoke`, and `make
  check` pass. The Playwright run used the documented bundled-Chromium
  single-process fallback because the restricted macOS host aborted installed
  Chrome's Mach bootstrap.
- P70-T17 Preview evidence: the previously reported Preview containers and
  their active project data volumes were absent before this run, so a fresh
  PostgreSQL/Reth Preview was built rather than claiming preserved-volume
  continuity. All seven application roles plus PostgreSQL and Reth are
  running, and migration exited successfully. The final application-image
  rebuild retained PostgreSQL container `e632b5046ee3` and Reth container
  `2c5fa096bdc3` while recreating the application roles. A bounded poll at
  height 142 returned zero lag with State and Trace both `complete`. Replaying
  the exact signed transaction produced
  `0x7cbf6ee5a54b9530a27f364a8283b36fd90af687c14453d901fd22e502a94960`;
  the API reports it successful in block 6. Both address reads selected block
  142 hash
  `0x4f02c4eb8715c4d09e566fb7cab374f38a9640aac765eb190ceb33939ea8d198`.
  At that same hash, the API and Reth both returned sender nonce `1`, and both
  returned recipient balance `1000000000000000000` wei.
- P70-T16 statistics and persistence: `stats@3` derives exact execution gas
  fees from verified receipt gas use and effective gas price, keeps blob fees
  independent, rejects incomplete or uint256-overflowing receipt facts
  permanently, and persists sums plus sample counts instead of floating-point
  averages. Migration `0028_historical_execution_analytics` adds UTC hourly
  rollups, dirty generations, and backfill coverage without removing
  `stats@2`. Canonical, stats, token, and stage publication transitions dirty
  affected hours in their database transaction; the maintenance worker holds a
  chain lock and generation fence and publishes newest hours first.
- P70-T16 API and web: OpenAPI, generated Go/TypeScript contracts, the native
  operation catalog, and optional x402 inventory expose overview plus the fixed
  18-metric detail allowlist. Query aggregation remains within one repeatable
  snapshot, rejects more than 500 explicit buckets with `422`, and returns
  analytics-pending rather than stale rollups. `/charts` renders categorized
  7-day previews, while `/charts/:metric` provides URL-bound presets/custom UTC
  dates, intervals, ECharts zoom/reset, reduced motion, semantic summaries,
  browser CSV, and an always-present exact table.
- P70-T16 verification: ordinary and race Go suites, 142 web tests, all 9
  embedded Playwright cases, the PostgreSQL 18 integration suite, `make
  generate-check`, `make plan-check`, `make deployment-check`, `make
  compose-runtime-smoke`, and aggregate `make check` pass. The integration
  suite covers numeric canonical ordering across 9/10/99/100 and a
  competing-hash detach/attach with generation-fenced rollup correction. A
  ten-year hourly dataset returned at most 500 monthly points with measured
  query p95 `91.412333ms`, below the existing 500 ms common-query target.
- P70-T16 Preview evidence: the current image and migration were applied while
  preserving the PostgreSQL and Reth volumes, and only the seven application
  roles were recreated. Audited stats reindex request `1` completed through
  the then-current tip. The indexed chain advanced beyond height 100; overview
  returned all 18 metrics with `backfill_progress=100`, `dirty_hours=0`, and
  `pending=false`; the hourly detail endpoint returned exact partial and
  completed buckets, and both embedded `/charts` routes returned `200`.
  Submitting a new transaction changed the current transaction total from 1 to
  2 and the maintenance worker cleared the dirty hour. `debug_setHead` rewound
  the Reth dev head, but Reth selected its stored identical-hash descendants,
  so this run is not claimed as the remaining competing-hash Preview reorg
  evidence.
- P70-T14 implementation: `observability.sync_progress_log_interval` defaults
  to `30s`, accepts YAML and
  `ETHERVIEW_SYNC_PROGRESS_LOG_INTERVAL`, and rejects values outside `1s` to
  `1h`. Compose, Preview, Helm, capacity values, example configuration, and
  operations guidance expose the same setting. The sync service logs only
  after the active reporter successfully persists a changed runtime status;
  its last emitted snapshot and timestamp coalesce interval-local changes
  without adding a timer, query, or heartbeat.
- P70-T14 business logs: a composed observer preserves the existing bounded
  Prometheus counters and emits stage/result or operation/result fields after
  enrich, trace, verification, metadata, and maintenance observers report an
  outcome. Successful, unavailable, and stale-target outcomes use `info`;
  retry, failed, rejected, resource, and error outcomes use `warn`. Unknown
  values collapse to `other`, so raw task inputs, URLs, credentials, and nested
  errors cannot enter these records. Catalog maintenance emits success only
  when its durable sweep reports `Ran`.
- P70-T14 verification: focused ordinary and race tests for
  `./internal/config`, `./internal/observability`, `./internal/syncer`,
  `./internal/maintenance`, and `./internal/app` pass. `make lint`, `make
  compose-check`, `make helm-check`, `make plan-check`, `make check`, and `git
  diff --check` pass. The full check includes all Go and web tests, the complete
  Go race suite, generation, security, license, Docker, Compose, and Helm
  gates.
- P70-T14 Preview evidence: `make recreate-preview` replaced all seven
  application roles while the PostgreSQL and Reth container IDs remained
  unchanged; migration completed, every role ran, and API readiness returned
  `200`. With Reth continuously producing blocks, sync emitted at
  `08:42:53.318Z` for height `340` and next at `08:43:24.020Z` for the coalesced
  height `346`, with no per-block records between them; the status API then
  reached height `350` at zero lag. Enrich and trace emitted immediate bounded
  success records, catalog maintenance logged its executed sweep, and API kept
  its existing request-completion records. Disabled verification and metadata
  features produced no idle business heartbeat.
- P70-T13: `compose.preview.yaml` replaces the `roles=all` monolith with API,
  sync, enrichment, trace, verification, metadata, and maintenance processes
  built from the same image. All roles share PostgreSQL, Reth, migration,
  config, and genesis dependencies; only API publishes ports and receives the
  session pepper. Preview start and application-only recreation remove orphaned
  services, while recreation preserves PostgreSQL and Reth.
- P70-T13 verification: `make compose-check`,
  `BUILDX_CONFIG=/tmp/etherview-buildx make deployment-check`, `make
  plan-check`, and `git diff --check` pass. A fresh
  `BUILDX_CONFIG=/tmp/etherview-buildx make start-preview` ran all seven roles,
  completed migration, served ready API and embedded SPA responses, and reached
  the Reth head with zero lag. Application container IDs all changed after
  `BUILDX_CONFIG=/tmp/etherview-buildx make recreate-preview`, while PostgreSQL
  and Reth container IDs remained unchanged and indexed height advanced from 4
  to 18 with zero lag.
- P70-T10: `observability.log_level` and `observability.log_format` default to
  `info` and `json`; exact YAML and `ETHERVIEW_LOG_*` values are validated
  before startup. Every configuration-bearing command accepts the same
  `--log-level` and `--log-format` overrides after its command name, with CLI
  precedence and duplicate rejection. The entrypoint creates and injects the
  logger only after the effective configuration is known.
- P70-T10: JSON and text output share level filtering, redaction, URL
  contraction, trace correlation, and the stable net/http internal-error
  adapter. Compose preserves mounted YAML when host overrides are absent and
  forwards exact overrides when present; Helm schema and render regressions
  accept the supported enums and reject invalid values.
- P70-T10 verification: targeted ordinary and race tests for
  `./cmd/etherview`, `./internal/config`, `./internal/cli`,
  `./internal/observability`, `./internal/httpapi`, and `./internal/app` pass.
  `go test ./... -count=1`, targeted `go vet`, `make lint`, `make
  toolchain-check`, `make compose-check`, `make helm-check`, `make plan-check`,
  and `git diff --check` pass.
- P70-T07 configuration: YAML, environment, and `_FILE` inputs support an
  optional reader URL plus independently bounded pool sizes. Zero reader
  values inherit writer settings; negative, overflowed, malformed, and
  effective `min > max` inputs fail before runtime.
- P70-T04 tooling: the Compose parity runtime-fixture container and load driver
  share one runtime-tools image, with loadtest emitted by the existing
  `go-builder`. The anvil fixture is supplied by the configured image while the
  load service overrides its entrypoint. The target builds successfully; both
  binary entrypoints, the numeric non-root user, Compose configuration, plan
  validation, shell syntax, and whitespace checks pass. This scoped
  maintenance is not 500 RPS or soak evidence.
- P70-T07 runtime: only `api`/`all` opens the forced-read-only reader pool.
  Startup checks its migration ledger and exact chain/genesis identity.
  Ordinary projections and the explicit Etherscan read inventory use it;
  canonical/RPC fences, runtime and metric state, authentication,
  verification, external observations, media, mempool, and all writes remain
  writer-backed. Both operational and public readiness fail closed, with the
  latter bypassing Redis status cache.
- P70-T07 deployment: Compose preserves mounted YAML sizing unless an operator
  supplies an environment override. Helm injects the optional reader Secret
  only into API-bearing containers, supports an optional ExternalSecret key,
  rejects inline credentials and invalid effective bounds, and documents
  restart-on-secret-rotation. The reference maximum is 216 writer plus 96
  reader connections (312 steady state, 624 for full old/new overlap).
- P70-T07 verification: `go test ./internal/config ./internal/app
  ./internal/httpapi -count=1`, the corresponding `go test -race`, and
  `go test ./... -count=1` pass. `make toolchain-check`, `make lint`,
  `make generate-check`, `make helm-check`, and `make compose-check` pass.
  `make plan-check` and `git diff --check` pass after the evidence update.
- P70-T07 integration boundary: `make test-integration` passes against a
  disposable PostgreSQL 18 database after applying and checking every
  migration. The integration-tag regression verifies real writer/reader
  session settings, SQLSTATE `25006`, reader schema compatibility, and
  chain/genesis matching. Both pools used the same PostgreSQL endpoint, so no
  asynchronous replica-lag or reader-outage result is claimed by this scoped
  item.
- P70-T08 implementation: bounded duplicate-key-safe Genesis JSON parsing uses
  Ethereum account/storage tries and the execution header through Amsterdam,
  and requires the configured plus canonical block-zero hash and state root.
  Migration `0022_genesis_state` persists database-guarded immutable balance,
  nonce, code, code hash, and storage-root facts without raw slots; exact
  predeploy code plus its proxy wake commit atomically. Missing input remains a
  typed unavailable capability. The source is either the existing absolute
  local file or a mutually exclusive public-HTTPS URL, and both remain limited
  to indexing from block zero.
- P70-T08 remote boundary: URL validation rejects credentials, query, fragment,
  non-443 ports, path traversal, redirects, environment proxies, and any DNS
  answer in a private or special-use network. Direct verified-IP dialing,
  identity encoding, the 64 MiB declared/streamed limit, an explicit MIME
  allowlist, and JSON validation bound the GET. An optional non-zero lowercase
  SHA-256 authenticates the exact response bytes before JSON validation.
  Network and hostile-content failures expose only stable redacted states.
- P70-T08 remote lifecycle: the importer checks immutable completion before any
  request, waits for canonical block zero, and holds a per-chain PostgreSQL
  session advisory lock across its second completion check and fetch. HTTP,
  checksum, and parsing remain outside the import transaction. Successful
  completion removes the remote dependency; a configured digest is compared
  directly with the persisted digest. Concurrent replicas fetch once, and
  checksum or canonical-identity failures commit no account, code, or proxy-job
  fact.
- P70-T08 public surfaces: generated Go/TypeScript contracts expose cursor-bound
  `/api/v1/genesis/accounts` pages with decimal-string quantities. The embedded
  bilingual SPA exposes `/genesis` and links canonical block zero to it.
  Compose supports file or remote environment inputs. Helm either mounts the
  PVC file or injects URL, digest, and timeout only into `all`/`sync` roles; it
  rejects source conflicts, unmounted inline values, non-zero starts, invalid
  digests, and out-of-range timeouts.
- P70-T08 verification: trie/hash/parser, capability API, cursor,
  proxy-candidate, split-role parity, immutable-observation, remote
  configuration, SSRF/DNS, proxy, redirect, status, MIME, encoding, size,
  timeout, checksum-order, and error-redaction tests pass, as do 68
  browser-component tests. Reference vectors cover fixed roots/hashes,
  100-account branching, and an Amsterdam genesis header. PostgreSQL 18
  `make test-integration` verifies file/URL byte-equivalent facts, one fetch for
  concurrent importers, completed offline restart, persisted-digest conflict,
  canonical completion reauthentication, and zero partial writes on checksum,
  block-hash, state-root, or temporary HTTP failure.
- P70-T08 common gates: `go test ./...`, `make test-race`,
  `make generate-check`, `make security-check`, `make lint` with a writable
  lint cache, `make toolchain-check`, `make compose-check`, `make helm-check`,
  `make license-check`, `make plan-check`, and `git diff --check` pass. The API
  generator override uses the patched `minimatch`/`brace-expansion` chain;
  both npm audits report zero vulnerabilities, `govulncheck` reports zero
  reachable vulnerabilities, and both working-tree and history secret scans
  are clean.
- P70-T08 browser boundary: the explicit restricted-macOS fallback uses bundled
  Chromium in single-process mode and isolates every test in its own worker,
  while ordinary local and CI runs retain their multi-process browser. The
  complete embedded-server suite passed all eight deep-link, bilingual,
  theme, canonical/orphan, Genesis/capability, WCAG, SIWE/billing/admin, and
  wallet-boundary cases.
- P70-T11: transaction-list and address-detail E2E assertions now use the
  configured 18-decimal native display instead of the former raw wei integer,
  and finalized status selects the unique high-visibility badge rather than
  colliding with the hidden More-details value. `PLAYWRIGHT_USE_BUNDLED=1
  PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` passed all 8 tests; `npm --prefix
  web test` passed all 131 tests, and `npm --prefix web run lint` passed.
- P70-T12: `StageID.Validate` now accepts the same lowercase alphanumeric,
  hyphen, and underscore alphabet as maintenance-stage validation, while
  continuing to reject uppercase, whitespace, slash, and punctuation. The
  production trace-enabled dispatcher test validates every scheduled stage,
  including `state_diff@1`. Focused ordinary and race tests for
  `internal/enrich`, `internal/app`, and `internal/maintenance` passed, and
  focused `golangci-lint` reported zero issues. The Docker-backed runtime smoke
  passes both monolith and seven-role split layouts, replica survival, short
  load, and normalized database/API/SPA parity.
- P70-T09 type ownership: `internal/ethrpc` retains only bounded transport,
  endpoint-pool, capability, scheduling, observation, and stable-error
  concepts. Protocol scalars and recognized RPC objects use go-ethereum
  `common`, `hexutil`, `core/types`, `rpc`, and `core.Genesis` types directly;
  there are no repository aliases or replacement Ethereum models in that
  package. Genesis JSON decodes directly into `core.Genesis`, and
  `core.Genesis.ToBlock()` owns the state root and block identity after a narrow
  duplicate-key, normalized-map-collision, resource, and uint256 preflight.
- P70-T09 raw and persistence boundary: raw-first block acquisition preserves
  supported-object unknown fields and validates header, transaction, receipt,
  log, withdrawal, sender, root, inclusion, uncle, gas-delta, contract-address,
  and effective-gas-price relationships before any atomic repository write.
  Geth transaction types 0 through 4 are supported; type 127 and other
  unsupported formats fail permanently before persistence or coverage
  advancement. Root-preserving versioned metadata carries validated PoW uncle
  headers, and legacy rows missing required non-empty headers return
  `ErrStoredUncleHeadersUnavailable`.
- P70-T09 compatibility: downstream indexing, enrichment, mempool, metadata,
  billing, x402, native API, Etherscan adapters, stores, and test fixtures use
  the direct geth values while SQL and OpenAPI contracts remain unchanged.
  Dynamic effective gas price is authenticated against the matching
  transaction and block base fee; fresh receipts require it, compatible stored
  rows may derive a missing value, and poisoned or context-free values are
  never exposed as verified. The state-difference adapter accepts geth's
  JSON-number nonce while retaining bounded quoted-quantity compatibility and
  persists the schema's required empty storage key for non-storage fields;
  malformed, negative, fractional, and oversized nonce inputs still fail
  closed.
- P70-T09 license closure: the reviewed go-ethereum v1.17.2 scanner baseline
  now includes the separately licensed `crypto/bn256` package. The transitive
  `github.com/holiman/bloomfilter/v2` v2.0.3 module archive omits its
  repository-root MIT license, so the narrow scanner exception fixes the
  version, checked-in text SHA-256, dependency presence, and exact scanner
  result without expanding the permissive allowlist. The production image
  copies all reviewed license texts, and its rootfs check requires each path.
- P70-T09 verification: `go test ./... -count=1`, `go test -race ./...
  -count=1`, `make test-integration`, and the integration-tag race suite pass
  against disposable PostgreSQL 18. `make toolchain-check`, `make plan-check`,
  `make generate-check`, `make lint`, `make security-check`, `make
  license-check`, `make helm-check`, `make compose-check`, and `git diff
  --check` pass. One initial ordinary test overlapped the full race run and
  timed out waiting for Docker in
  `TestContainerCompilerValidatesAndAppliesIsolation`; the isolated case and
  serialized full suite both passed immediately afterward. `make docker-check
  docker-image-check`, `make compose-runtime-smoke`, and the aggregate `make
  check` pass on the completed working tree.
