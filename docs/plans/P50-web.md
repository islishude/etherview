# P50 — Embedded Web

Status: `done`

## Outcome

A responsive Chinese/English React SPA covers the core explorer workflows,
communicates only with versioned APIs, is embedded in the Go binary, and uses an
injected EIP-1193 wallet for all contract reads and writes.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0013: Embedded SPA serving and browser security](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [EIP-1193: Ethereum Provider JavaScript API](https://eips.ethereum.org/EIPS/eip-1193)
- [EIP-6963: Multi Injected Provider Discovery](https://eips.ethereum.org/EIPS/eip-6963)
- [Tailwind CSS with Vite](https://tailwindcss.com/docs/installation/using-vite)
- [Tailwind Preflight](https://tailwindcss.com/docs/preflight)
- [Testing](../testing.md)

## Work Items

| ID      | Status      | Depends on    | Deliverable                                                              | Verification              |
| ------- | ----------- | ------------- | ------------------------------------------------------------------------ | ------------------------- |
| P50-T01 | done        | P00           | React/Vite routing, generated API client, i18n, theme, design primitives | frontend unit/build tests |
| P50-T02 | done        | P40           | Home, blocks/orphans, transactions, addresses, and search pages          | Playwright core flows     |
| P50-T03 | done        | P20, P30-T04, P40 | Token/NFT, contract, verify, charts, pending, and sync-status pages      | capability UI tests       |
| P50-T04 | done        | P30-T01, P40      | EIP-6963 discovery and wallet-only contract read/write forms             | provider/mismatch tests   |
| P50-T05 | done        | P50-T01       | Embedded assets, deep-link fallback, cache headers, CSP, accessibility   | binary E2E and a11y tests |
| P50-T06 | done        | P40-T07       | Etherscan-inspired tabbed transaction detail system                      | frontend, embedded E2E, responsive and a11y tests |
| P50-T07 | done        | P50-T02, P60-T03 | Numeric latest-page cursor, live-head Preview wake, and activity refresh | query/frontend regressions and Preview smoke |
| P50-T08 | done        | P50-T06       | Persistent transaction copy controls and receipt-backed creation address | query/frontend regressions and generation checks |
| P50-T09 | done        | P50-T02       | Compact home, relative recent-block time, and shared brand icon          | frontend, embedded E2E, and generation checks |
| P50-T10 | done        | P40-T08       | Address activity tabs, lazy assets, and contract-address entry           | frontend, embedded E2E, and generation checks |
| P50-T11 | done        | P40-T09       | Atomic home snapshot EventSource with no REST polling fallback            | frontend, embedded E2E, and generation checks |
| P50-T12 | done        | P40-T10       | Address origins, QR/copy header, ERC-20 assets, configurable native labels, and add-network wallet flow | frontend unit, build, and generation checks |

## Acceptance

- [x] No frontend runtime service or external CDN is required.
- [x] Chinese/English, light/dark, keyboard, responsive, and WCAG 2.1 AA flows
      cover all primary pages.
- [x] Optional unavailable capabilities are explained or hidden consistently.
- [x] Contract calls require a discovered wallet on the configured chain; the
      backend never receives private key material or signs transactions.
- [x] RPC credentials and server-only settings do not exist in built assets.
- [x] `web/dist` is treated as a build-only artifact and must be generated
      during build/test pipelines, not checked into git.
- [x] Transaction detail provides deep-linkable lazy tabs for overview, token
      transfers, logs, trace, and exact state changes in both languages and
      themes.

## Current Blockers

None.

## Evidence

- P50-T11: the home route owns exactly one same-origin `/api/v1/home/stream`
  EventSource. A bounded strict validator accepts only complete status, block,
  transaction, and coverage envelopes and replaces all three UI regions in
  one state update. Initial failure is visible, while malformed messages and
  reconnect errors preserve an existing snapshot; unmount closes the stream.
  The local one-second relative-time clock remains independent of chain
  events, and the home route has no REST polling or HTTP fallback.
- P50-T11 verification: TypeScript lint, all 19 Vitest files (141 tests), and
  the production build pass. The full embedded Playwright run passes all 9
  flows; its dedicated home flow observes one SSE request, an atomic head,
  metrics, recent-block, and recent-transaction update, and no `/status`,
  `/blocks`, or `/transactions` requests during the monitoring window. It
  also passes Axe at 390px in Chinese dark mode. The tagged PostgreSQL
  integration test compiles and is ready for the disposable-database gate;
  the local `make test-integration` run reported its documented skip because
  `INTEGRATION_DATABASE_URL` is unset.
- P50-T11 common gates: `make generate-check`, `make plan-check`, and
  `git diff --check` pass. `make check` passed generation, Go and TypeScript
  lint, ordinary and race tests, security scans, dependency audits, and
  license checks; its final deployment check could not start because the
  workspace sandbox denied Docker Buildx activity-cache writes. An
  out-of-sandbox retry was unavailable because the workspace approval service
  had no remaining credits.
- P50-T10: the address summary now contains only address, name, account type,
  native balance, and nonce. Five query-string-addressable sections expose
  transactions, internal transactions, ERC-20 transfers, NFT transfers, and
  lazy NFT assets with independent cursor histories. Only the active section
  requests data; activity first pages refresh every two seconds while cursor
  history pages stay fixed. Contract accounts receive a code-hash-prefilled
  link to the existing contract page without rendering the hash in the address
  summary; EOAs do not.
- P50-T10: frontend lint, all 18 Vitest files (138 tests), production build,
  and `make generate-check` passed. The embedded E2E fallback
  `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` passed
  all 8 flows, covering contract and EOA addresses, direct tab links,
  browser-history navigation, English/Chinese, light/dark, keyboard access,
  390-pixel containment, and WCAG scanning. The ordinary installed-Chrome run
  could not launch under the host's Mach bootstrap sandbox; the documented
  bundled fallback completed successfully. The full `make check` sequence
  passed through generation, plan, lint, ordinary/race tests, security, and
  licenses; its sandboxed Buildx activity-file write was rerun successfully
  outside the filesystem sandbox, followed by passing Compose and Helm gates.
- P50-T09: the home page removes its hero and dedicated responsive artwork,
  starts directly with live chain metrics, and renders recent-block ages from
  a one-second localized clock without changing absolute timestamps elsewhere.
  The header and browser favicon share one content-hashed SVG block-shaped `E`
  mark served by the embedded Go handler with SVG MIME, immutable caching, and
  the standard security headers.
- P50-T09: focused Vitest passes 2 files and 20 tests; the full frontend suite
  passes 18 files and 137 tests. `npm --prefix web run lint`,
  `npm --prefix web run build`, `go test ./web -count=1`,
  `make generate-check`, and
  `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` pass,
  with all 8 embedded-distribution browser flows green. In-app browser checks
  at desktop and 390x844 confirm zero horizontal overflow, no hero, live
  Chinese/English relative time, the 34-pixel mark, and clean light/dark
  rendering with no console warnings or errors.
- P50-T08: the native transaction contract exposes an optional checksummed
  `contract_address` only when the validated stored receipt names a successful
  top-level creation. The transaction overview keeps all hash/address/input
  copy controls visible, shows the created address with a bilingual creation
  label, omits the transaction completeness panel, and never fabricates an
  address for failed creation.
- P50-T08: `go test ./internal/query -count=1`,
  `npm --prefix web test -- src/pages/CorePages.test.tsx` (16/16),
  `npm --prefix web run lint`, and `make generate-check` pass. With writable
  Go cache under `/tmp`,
  `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` passes
  all 8 embedded-distribution flows, including persistent copy controls,
  receipt-backed creation display, completeness removal, and 390-pixel
  transaction-detail overflow coverage. The final sandbox-scoped `make check`,
  with Go, golangci-lint, and Buildx caches under `/tmp`, passes generation,
  plan, lint, 134 frontend tests, ordinary and race Go tests, security,
  licenses, Docker/Compose, and Helm gates.
- P50-T07: Preview now supplies both the authoritative Reth HTTP endpoint and
  its WebSocket endpoint, registering the production `new-head-wake`
  component while retaining polling as fallback. Status, first-page blocks,
  and first-page transactions refresh every two seconds; opaque historical
  cursor pages remain stable and do not poll.
- P50-T07: the shared block/transaction/search first-page cursor now qualifies
  `canonical.number` in its ordering. This prevents PostgreSQL from resolving
  the `number` output name to `number::text` and ordering heights
  lexicographically, which had pinned the live lists at block 99 after the
  chain passed block 100.
- P50-T07: the rebuilt Preview retained its PostgreSQL and Reth volumes,
  reported `core_ready=true`, `backfill_complete=true`, and zero lag, and
  returned transaction
  `0x8b50660fefae2985db7e24c63f87fa7c3a3c5ee40454e8526b84802532693a95`
  from both detail and first-page list APIs. After the numeric cursor fix, the
  list head and indexed head both reported block 320; a live block 323-to-324
  sample observed API visibility in 4 ms after the RPC head changed.
- P50-T07: `docker compose -f compose.preview.yaml config --quiet`,
  `go test ./internal/query -count=1`, `npm --prefix web run lint`,
  `npm --prefix web test` (18 files, 132 tests), `make generate-check`, and
  `make plan-check` pass. `make check` passed generation, lint, all ordinary
  and race tests, security, and license gates before the sandboxed default
  Buildx activity directory stopped its deployment phase;
  `BUILDX_CONFIG=/tmp/etherview-buildx make deployment-check` then passed the
  Dockerfile, Compose, and Helm gates. Focused regressions require numeric tip
  ordering and prove an initially empty transaction page updates when the next
  poll makes a newly indexed transaction visible.
- P50-T06: `/tx/:hash` now preserves five generated-client-backed tabs in the
  `tab` query parameter. Logs, trace, and state changes load only when active;
  the overview's bounded token-transfer read supplies the deterministic action
  summary without protocol guesses. Arrow/Home/End keyboard navigation,
  back/forward-compatible links, horizontal narrow-screen tabs, mismatch
  refetch fencing, authoritative empty/capability states, copied identifiers,
  and the accessible More-details disclosure have focused regressions.
- P50-T06: the overview keeps the navigable block height without repeating the
  block hash; confirmations/time, native-symbol amounts, gas used, success,
  failure, orphan and finalized badges, long hex data, trace indentation, log
  cards, and account-grouped state changes are bilingual and theme-aware.
  `npm --prefix web test`, lint, and build passed with 18 files and 131 tests;
  the transaction-focused run passed 2 files and 24 tests.
- P50-T06: the Go embedded E2E fixture exposes all transaction subresources. A
  real in-app browser verified the built distribution at desktop and 390x844:
  direct state-change deep links, ArrowRight tab activation, English/light and
  Chinese/dark state, intended tab-strip scrolling, and zero document
  overflow. `make test-e2e` built the same distribution, but both bundled and
  system Chromium command-line launches were denied by this macOS environment
  before page creation, so no new Playwright/Axe pass is claimed from this run.
- P50-T06: `make plan-check`, `make generate-check`, and `make check` passed
  after the final source state. `make test-integration` reported its documented
  skip because no disposable `INTEGRATION_DATABASE_URL` was configured.

- P50-T01: `make toolchain-check` passes the Go 1.26.5, Node 24.18.0,
  and npm 11.16.0 supported baselines. A clean
  `npm --prefix web ci` followed by
  `npm --prefix api run check:api`, `npm --prefix web run lint`,
  `npm --prefix web run test`, and `npm --prefix web run build` passes with 8
  test files and 32 tests. Coverage includes typed deep-link/search routing,
  the sole same-origin generated OpenAPI transport, large string quantities,
  first-load and switched Chinese/English document language, persisted
  light/dark theme, and Tailwind-backed design primitives; Vite emits only
  local content-hashed assets.
- P50-T01: `go test -race ./web -count=1` passes the embedded
  distribution checks, including absence of server configuration markers and
  external entrypoints in the built assets.
- P50-T01 commit/PR: none created because the repository has no `HEAD` and this
  task did not authorize a commit or pull request; evidence is bound to the
  current working tree.
- P50-T05: `make toolchain-check`, `go test -race ./web -count=1`,
  `npm --prefix web run lint`, and `npm --prefix web run test` pass; Vitest
  reports 9 files and 33 tests. Handler regressions cover HTML media-range
  precedence, malformed quality values, reserved and asset-shaped misses,
  exact eight-character Vite base64url hashes, revalidating non-hashed files,
  real-file `HEAD`, SHA-256 ETags, conditional responses, and security headers.
- P50-T05: `make test-e2e` explicitly builds a temporary Go binary containing
  the `go:embed` distribution and passes 4 Playwright flows. The suite proves
  deep-link isolation, no-store shell/miss behavior, immutable hashed assets,
  conditional-response CSP/security headers, no external browser requests,
  keyboard skip navigation, narrow layout, reduced motion, and WCAG 2.1 A/AA
  scans in both light English and dark Chinese. The expanded scan found the
  dark-theme filled-control contrast regression and verified its fix. The
  `make generate-check` no-drift gate also passes.
- P50-T05 commit/PR: none created because this task explicitly requested no
  commit; evidence is bound to the current working tree.
- P50-T02: `npm --prefix web run check:api`, `npm --prefix web run lint`,
  `npm --prefix web run test`, and `npm --prefix web run build` pass with 10
  test files and 37 tests. Regressions cover opaque server-issued cursor
  round-trips and history, cache-bypassing invalid-cursor restart with a fresh
  first-page request, contiguous coverage versus higher live islands,
  exact-hash retained-orphan navigation, canonical-only block and transaction
  lists, exact-block address state, localized stage/state/account types with a
  stable diagnostic code, bilingual labels, and string-preserved large
  amounts.
- P50-T02: `PLAYWRIGHT_USE_BUNDLED=1 make test-e2e` builds the embedded SPA into
  a temporary Go binary and passes all 5 Playwright flows. The core flow covers
  Home, paginated blocks and transactions, transaction and address detail,
  search pagination, retained orphan detail, Chinese switching, keyboard
  activation, narrow populated-table layout, and complete WCAG 2.1 A/AA Axe
  scans in light English and dark Chinese. The scan found a bundled-Chromium
  contrast-analysis deadlock on the translucent body gradient and verifies the
  production solid theme background fix without disabling any WCAG rule.
- P50-T02 review closure: targeted
  `npm --prefix web run test -- CorePages.test.tsx App.test.tsx` passes 2 files
  and 15 tests; `npm --prefix web run lint`, `npm --prefix web run build`, and
  the full 10-file/37-test Vitest suite pass. With the rebuilt embedded
  distribution, `PLAYWRIGHT_USE_BUNDLED=1 npm --prefix web run test:e2e`
  passes all 5 flows, including an actual transaction second page whose cursor
  contains reserved `?`, `+`, `&`, `/`, `#`, and `=` characters and must
  round-trip unchanged to select its fixture. `make generate-check` also
  passes after the embedded-SPA changes.
- P50-T02: `go test -race ./web -count=1`, `make toolchain-check`,
  and `make generate-check` pass. No commit or pull request was created;
  evidence is bound to the current working tree.
- P50-T03: the generated same-origin client now drives token discovery and
  exact canonical NFT balances/ownership, code-hash-bound published
  verification artifacts, durable verification-job reads and guarded new
  submissions, `stats@2` ranges and exact-value tables, immutable pending
  snapshots, and separate indexed-stage/configured-feature status. Typed
  unavailable, failed, expired, disabled, and invalid-cursor states remain
  distinct from authoritative empty results. API keys stay in request headers
  and are excluded from URLs and query-cache identities.
- P50-T03: closing public verification submission no longer hides authenticated
  reads of already-published artifacts or durable jobs. Standard JSON receives
  bounded UTF-8, duplicate-key, nesting, object-shape, and safe-number checks
  before submission. Capability labels, exact large quantities, match/status
  values, and all new controls are covered in both Chinese and English.
- P50-T03: `npm --prefix api run check:api`,
  `npm --prefix web run lint`, `npm --prefix web run test`, and
  `npm --prefix web run build` pass; Vitest reports 11 files and 50 tests.
  `make toolchain-check generate-check`, `make lint`, `make test`,
  `go test -race ./web -count=1`, and `git diff --check` also pass.
- P50-T03: `PLAYWRIGHT_USE_BUNDLED=1 make test-e2e` builds the temporary
  Go binary with its `go:embed` distribution and passes all 6 Playwright flows.
  The added capability flow traverses token-to-NFT deep links, exact owner
  balances, disabled-submission job/artifact reads, aggregate charts, pending
  snapshots, and sync/capability state. It scans every P50-T03 route under
  light English and dark Chinese at desktop and narrow widths with the complete
  WCAG 2.1 A/AA ruleset, while proving keyboard activation, zero document
  overflow, and no external browser request.
- P50-T03 security checks: `gitleaks dir --no-banner --redact .` reports no
  leaks; `go test ./internal/auth ./internal/metadata ./internal/verify ./web`
  passes; the clean frontend install reports zero vulnerabilities. A fresh
  `govulncheck ./...` database refresh was attempted twice but
  `vuln.go.dev` timed out/reset the connection, so no fresh Go vulnerability
  result is claimed.
- P50-T03 commit/PR: none created because this task did not request a commit or
  pull request; evidence is bound to the current working tree.
- P50-T04: EIP-6963 discovery validates and snapshots bounded provider metadata,
  preserves the first provider for a UUID, caps the discovery set, and keeps the
  raw EIP-1193 provider private to the wallet boundary. The public context
  exposes only bounded display metadata and fixed `readContract` /
  `sendTransaction` capabilities. The exact five-method allowlist is
  `eth_requestAccounts`, `eth_accounts`, `eth_chainId`, `eth_call`, and
  `eth_sendTransaction`; every operation rechecks the selected account and
  configured chain.
- P50-T04: provider arrays, strings, quantities, calldata, and results are
  validated within explicit count/byte bounds before use. Account parsing does
  not trust array instance methods or Proxy getters. Preflight and completion
  bind the same wallet-session object, so disconnect/reconnect and account or
  chain ABA events cannot admit an old result. A write that reached the provider
  without a trustworthy hash or completion session reports an unknown outcome
  and requires wallet inspection before retry; hostile messages and data never
  reach the DOM.
- P50-T04: the address-only contract entry reaches one accessible shared
  read/write workbench without requiring a verification code hash. Shared value
  and calldata are bound to both payable reads and writes. Inputs stay frozen
  until an in-flight wallet request settles, stale write outcomes remain
  explicit, and connection/wrong-chain state remain distinct. Connect,
  disconnect, and external provider-disconnect transitions restore focus;
  disconnects are also announced through an always-mounted live region.
- P50-T04: targeted wallet, static-boundary, and capability-page regressions
  pass 3 files and 33 tests. The full `npm --prefix web run lint` and
  `npm --prefix web run test` gates pass with 11 files and 67 tests. Static
  checks prove the production SPA contains only the fixed wallet method
  allowlist, does not expose the raw provider, and has no wallet path through
  the generated backend transport. Regressions cover discovery/account/result
  limits, malformed and Proxy-backed responses, silent drift, request error
  codes, same-provider reconnect, account/chain ABA, and uncertain writes.
- P50-T04: `PLAYWRIGHT_USE_BUNDLED=1 make test-e2e` builds the temporary Go
  binary with its `go:embed` distribution and passes all 7 Playwright flows.
  The same-chain flow proves exact payable read/write provider payloads and
  bounded results, zero backend requests after entering the wallet boundary,
  absence of private-key or wallet-signing surfaces, stable rejection and
  uncertain-write handling, operation locking across account ABA, connect and
  disconnect focus recovery, external-disconnect announcements, long metadata
  containment, and complete WCAG 2.1 A/AA scans. The mismatch flow proves no
  call or transaction request is sent. The expanded 320-pixel pass found and
  verified the header-overflow regression fix.
- P50-T04 common gates: `make toolchain-check generate-check`, `make lint`,
  `make test`, `make security-check`, `go test -race ./web -count=1`, and
  `git diff --check` pass.
  `govulncheck ./...` reports no reachable code or imported-package
  vulnerabilities (one vulnerable required module is not called);
  `gitleaks dir --no-banner --redact .` reports no leaks;
  `go test ./internal/auth ./internal/metadata ./internal/verify ./web` passes;
  and the clean web install reports zero vulnerabilities.
- P50-T04 dependency closure: `api/package.json` overrides the generator's
  transitive `js-yaml` to 4.3.0; `npm --prefix api ls js-yaml --all` confirms
  the override, while clean API install and `npm --prefix api audit
  --audit-level=high` report zero vulnerabilities. `make security-check` now
  permanently audits both the API generator and frontend; its refreshed full
  run passes both audits, Go vulnerability analysis, worktree and Git-history
  leak scans, the generated-client build, and focused security tests.
- P50-T04 commit/PR: none created because this task did not request a commit or
  pull request; evidence is bound to the current working tree.
- P50-T12 implementation and non-browser verification are complete:
  Contract/Address headings, de-duplicated copy/QR header, origin states,
  independent ERC-20/NFT holdings, configured native labels, and the
  account-independent EIP-6963 add-network flow are covered by 144 passing
  Vitest tests. `npm --prefix web run lint`, `npm --prefix web run build`, and
  `make generate-check` pass. The aggregate `make check` also passes, including
  the embedded asset build, full Go ordinary/race suites, security and license
  scans, and deployment rendering.
- P50-T12 browser release validation is tracked by P70-T18. Three
  `make test-e2e` attempts, including an approved unsandboxed rerun and a
  `PLAYWRIGHT_SINGLE_PROCESS=1` fallback, built the embedded binary but local
  Chrome exited with `SIGABRT`; Playwright then reported `kill EPERM`.
