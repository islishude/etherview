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
- [ADR-0036: Endpoint-scoped mempool replacement observations](../decisions/ADR-0036-endpoint-scoped-mempool-replacements.md)
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
| P50-T13 | done        | P50-T12       | Add-network flow consolidated into the wallet menu                         | frontend unit, embedded E2E, responsive and a11y tests |
| P50-T14 | done        | P40-T11, P50-T13 | Etherscan-style verified ABI read/write forms for contracts, implementations through proxies, exact management targets, and proxy histories, with real OpenZeppelin 5.6.1 Preview acceptance | Vitest, generated client, embedded browser, responsive, accessibility, Hardhat 3, monolith/split, and seven-role Preview tests |
| P50-T15 | done        | P50-T14       | Viem-compatible ABI result and revert formatting, including ERC-20 `decimals()`, plus fail-closed rejection of codec-unsupported types | focused ABI/form tests and common frontend gates |
| P50-T16 | done        | P50-T15       | Etherscan-inspired verified source workspace, structured compiler settings, and summarized contract artifacts | focused frontend, embedded browser, responsive, accessibility, generation, security, and license gates |
| P50-T17 | done        | P50-T16       | CSP-compatible CodeMirror layout with aligned source lines and gutters | focused frontend and live Preview browser regression |
| P50-T18 | done        | P50-T17       | CSP-compatible stable semantic syntax highlighting for verified source | focused frontend and live Preview browser regression |
| P50-T19 | done        | P50-T18       | Compact ABI read/write forms, stacked parameter inputs, copyable calldata, and explicit wallet connection guidance | focused ABI form tests, frontend lint/build, and applicable repository gates |
| P50-T20 | done        | P50-T19       | ABI address inputs provide a current-wallet Self shortcut | focused ABI form and frontend regression tests |
| P50-T21 | done        | P50-T20       | Compact Self control is visually inset within address inputs | focused frontend style and ABI form regression tests |
| P50-T22 | done        | P50-T21       | Read/Write actions expose wallet guidance on hover when unavailable | focused ABI form and frontend regression tests |
| P50-T23 | done        | P50-T22       | Remove the Actual call target label while retaining exact target visibility | focused ABI form and frontend regression tests |
| P50-T24 | done        | P50-T23       | Replace unreliable native hover title with an explicit wallet guidance tooltip | focused ABI form and stylesheet regression tests |
| P50-T25 | done        | P50-T24       | Refine wallet guidance tooltip into a compact floating bubble | focused ABI form and stylesheet regression tests |
| P50-T26 | done        | P50-T25       | Remove the contract target address from ABI form presentation | focused ABI form regression tests |
| P50-T27 | done        | P50-T26       | Close the wallet popover when the user clicks outside it | focused wallet-menu regression tests |
| P50-T28 | done        | P50-T27       | Keep proxy identity and proxy histories scoped to actual proxy contracts, with identity shown in Code | focused ContractPage regression tests and frontend gates |
| P50-T29 | done        | P50-T28       | Make the Proxy identity section collapsed by default in the Code tab | focused ContractPage tests and frontend gates |
| P50-T30 | done        | P50-T29       | Keep unavailable-wallet action guidance complete and remove duplicate inline wallet messaging | focused ABI form and stylesheet regression tests |
| P50-T31 | done        | P50-T30       | Decode verified constructor arguments into readable ABI parameter rows while retaining raw hex | focused ABI, component, and embedded browser regressions |
| P50-T32 | done        | P50-T30       | Embed Contract beside address activity tabs and support hash-driven contract subpage deep links | focused frontend, embedded browser, accessibility, and generation regressions |
| P50-T33 | done        | P50-T32       | Replace the verified-source flat file list with an accessible expandable directory tree on desktop and narrow layouts | focused tree-model, component, accessibility, responsive, and common frontend gates |
| P50-T34 | done        | P50-T33       | Compact consecutive single-child source directories into one tree node while preserving exact source paths | focused tree-model, component, frontend, and browser regressions |
| P50-T35 | done        | P50-T34       | Align the address Contract tab with ordinary activity tabs and explain unverified contracts clearly | focused frontend, build, and embedded browser regressions |
| P50-T36 | done        | P50-T35       | Apply ordinary activity-tab styling to the Delegation entry and keep its active state distinct | focused frontend, build, and embedded browser regressions |
| P50-T37 | done        | P50-T36       | Make inactive address tabs explicitly transparent and reserve the green background for the active state | focused frontend and stylesheet regressions |
| P50-T38 | done        | P50-T37       | Enforce the shared address-tab visual states against legacy Contract styling overrides | focused frontend, production build, and embedded browser regressions |
| P50-T39 | done        | P40-T12, P50-T38 | Protocol detail tabs for transactions and blocks, withdrawals, typed transaction fields, and address origin evidence | focused frontend, generated-client, responsive, accessibility, and embedded browser regressions |
| P50-T40 | done        | P50-T39 | Compact decoded transaction logs with topic format conversion and copy controls | focused frontend, responsive, accessibility, and common frontend gates |
| P50-T41 | done        | P50-T40 | Structured ABI provenance and recursive Name/Type/Indexed/Data event log decoding | focused frontend, responsive, accessibility, and common frontend gates |
| P50-T42 | done        | P50-T41 | Consolidate topic display into the expanded More details disclosure | focused frontend, topic conversion, responsive, and accessibility gates |
| P50-T43 | done        | P50-T42 | Expose convertible topics and data directly in More details, including anonymous-event topic zero | focused frontend, topic conversion, responsive, accessibility, and embedded browser gates |
| P50-T44 | done        | P50-T43 | Align transaction-log data presentation with the Topics heading and value column | focused frontend, responsive, accessibility, and embedded browser gates |
| P50-T45 | done        | P50-T44 | Separate decoded and raw transaction calldata with compact ABI evidence and read-only raw conversion | focused frontend, bilingual, responsive, accessibility, and embedded browser gates |
| P50-T46 | done        | P50-T39 | Accept protocol withdrawal and typed-transaction fields in atomic home snapshots | focused home-stream, frontend build, and live Preview browser regression |
| P50-T47 | done        | P50-T45 | Soft-wrap read-only raw transaction calldata within its textarea | focused frontend, responsive, and embedded browser regressions |
| P50-T48 | done        | P50-T47 | Link trustworthy contract-write transaction hashes to the transaction overview | focused ABI form, embedded browser, build, generation, and plan regressions |
| P50-T49 | done        | P40-T13, P50-T48 | Transaction-overview internal ETH transfers and exact-decimal ERC-20 transfer quantities | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |
| P50-T50 | done        | P50-T49 | Move transaction internal ETH transfers from Overview into a dedicated tab immediately before Token transfers | focused frontend, tab order, lazy loading, bilingual, embedded browser, and common gates |
| P50-T51 | done        | P50-T50 | Remove the standalone Contracts navigation and route, and link definitively unverified contracts to address-prefilled Web verification | focused frontend, bilingual, embedded browser, accessibility, and common gates |
| P50-T52 | done        | P50-T51 | Organize the global stylesheet into documented responsibility-based modules without visual or interaction changes | stylesheet regressions, complete frontend tests, lint, build, generation, plan check, and diff check |
| P50-T53 | done        | P40-T14, P50-T52 | Merge transaction gas limit and usage and expose execution and blob fee settings | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |
| P50-T54 | done        | P50-T53, P61-T13 | Classify transaction actions from exact transaction-time execution code instead of calldata presence | focused frontend, bilingual, execution-identity, embedded browser, and common gates |
| P50-T55 | done | P40-T15, P50-T54 | Preview pending and replaced transaction details with automatic transitions and iconized transaction statuses | focused frontend, bilingual, responsive, accessibility, embedded browser, and runtime E2E gates |
| P50-T56 | done | P40-T16, P50-T55 | Lazy address withdrawal history and exact Ether display for address and block withdrawals | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |
| P50-T57 | done | P40-T17, P50-T56 | Compact transaction-list Method column with full-signature disclosure and exact fallback labels | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |

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
- [x] Transaction detail provides deep-linkable lazy tabs for overview,
      internal transactions, token transfers, logs, trace, and exact state
      changes in both languages and themes.
- [x] Verified contract pages generate bounded typed ABI forms without manual
      API keys, ABI JSON, or calldata; every as-proxy or management write is
      fenced by the latest binding, chain, and account identity.
- [x] Verified code pages expose a bilingual, theme-aware, strictly read-only
      multi-file source workspace and summary-first compiler and artifact
      details without external browser resources.
- [x] Pending and replaced hashes provide a retention-bounded basic detail
      preview, automatic inclusion transition, direct replacement links, and
      icon-plus-text transaction statuses across all transaction surfaces.

## Current Blockers

None.

## Evidence

- P50-T57 adds Method/方法 only to the global transaction table after Hash.
  The compact 12rem cell preserves the full method in the DOM, exposes the
  complete decoded signature through title and accessible name, falls back to
  the projected semantic/selector value or `—`, and does not issue calldata
  requests. Focused frontend tests pass all 48 cases and the complete suite
  passes 30 files and 328 tests. Host-authorized `make test-e2e` passes all 18
  embedded Chromium flows, including bilingual keyboard access, signature
  disclosure, ellipsis styling, pagination, and 390px page-overflow coverage;
  `make generate-check`, `make plan-check`, the complete host-authorized `make
  check`, and `git diff --check` also pass.

- P50-T56 adds an always-visible, deep-linkable Withdrawals/提款 address tab
  immediately after Internal Transactions. It lazily preserves API order and
  independent cursor pagination, links exact block hashes, and provides
  bilingual empty, error, and pagination states. Address and block withdrawal
  tables share exact BigInt Gwei-to-Ether formatting, including
  `3200000000 -> 3.2 Ether` and `1 -> 0.000000001 Ether`.
- P50-T56 verification passes the focused frontend suite (2 files, 61 tests),
  the complete frontend suite (30 files, 327 tests), TypeScript lint,
  production build, `make generate-check`, `make helm-check`, owned PostgreSQL
  18 `make test-integration`, `make plan-check`, and `git diff --check`.
  Host-authorized `make test-e2e` passes all 18 flows, including lazy loading,
  exact ordering and links, bilingual keyboard access, block Ether rendering,
  and 390px overflow coverage; the complete host-authorized `make check` gate
  also passes.
- P50-T55 makes pending-list hashes deep-link to the shared transaction route,
  renders only the retained mempool Overview for pending/replaced observations,
  follows direct predecessor/successor hashes, polls every two seconds only
  until the last known expiry, and automatically enables the existing included
  detail tabs when chain inclusion wins. `lucide-react@1.31.0` supplies the
  shared `CircleCheck`, `CircleX`, `Clock3`, `ArrowRightLeft`, `GitBranch`, and
  `CircleHelp` status system; visible localized text, color, border, and
  non-semantic icons remain present at narrow widths in both themes.
- P50-T55 focused status/Pending/CorePages tests pass 58 assertions, and the
  complete frontend suite passes 30 files and 320 tests. TypeScript lint,
  production build, `make lint`, `make test`, `make test-race`, `make
  security-check`, `make license-check`, PostgreSQL 18 integration, Compose
  render, and Helm render gates pass. The 17-flow embedded Chromium suite
  passes in 37.6s, including Pending -> Replaced -> Included, direct links, no
  eager derived requests, English/light and Chinese/dark Axe scans, 320px
  overflow, and status icons. The post-continuity-marker real Anvil replacement
  flow passes monolith (31.91s) and complete split (42.66s) runtime E2E, and
  the aggregate `make check` gate passes.

- P50-T54 classifies contract creation directly and every transaction with a
  target from exact matching transaction-time execution evidence: direct and
  EIP-7702 delegate code are contract interactions, while empty execution is a
  native transfer or EOA transaction according to calldata. Loading, request
  failure, unavailable resolution, and stale identity remain explicitly
  fail-closed in both languages. The focused CorePages suite passes 44 tests;
  the complete frontend suite passes 29 files and 312 tests. `make web-lint`,
  `make web-build`, `make generate-check`, `make plan-check`, and `git diff
  --check` pass, as do the host-authorized 16-flow `make test-e2e` and complete
  host-authorized `make check` gates.

- P50-T53 merges transaction Gas limit and usage with an exact BigInt-based,
  two-decimal percentage; exposes Base, Max, and Max Priority fee settings with
  the agreed Legacy/Access-list gas-price fallback; and renders Blob Base/Max
  settings in both Overview and Blob. Focused API/query tests and 55 frontend
  tests pass, as do the complete 29-file/305-test frontend suite, TypeScript
  lint, production build, `make generate-check`, `make plan-check`, the
  host-authorized 16-flow embedded Chromium suite, and the full host-authorized
  `make check` gate.

- P50-T46 aligns the fail-closed home snapshot parser with the generated Block
  and Transaction contracts for withdrawals, blob fee/hash fields, and access
  lists while retaining strict nested validation. The focused five-test suite,
  all 29 frontend test files (287 tests), TypeScript lint, production build,
  and `make plan-check` pass. `make recreate-preview` rebuilt the production
  image while retaining PostgreSQL and Geth; a fresh HTTPS browser load showed
  indexed/head block 90, finalized block 64, caught-up state, and six recent
  blocks without console warnings or errors.

- P50-T39: Transaction tabs now derive visibility from typed transaction type,
  with access-list/blob/authorization panels and lazy unrelated-resource
  loading. Block detail is an extensible URL-driven Overview/Transactions/
  Withdrawals tab page, with the withdrawal tab shown only when the block
  exposes the capability, plus keyboard navigation, cursor pagination, and
  orphan-hash support. Address summaries hide Name while block-origin
  funded-by evidence links only to the exact block.
- P50-T39 verification: generated-client check, TypeScript lint, production
  build, 28-file/265-test Vitest suite, and the 12-flow embedded Playwright
  suite pass.

- The verified OpenZeppelin proxy browser regression now follows the compact
  ABI form contract: target addresses and removed management-scope copy are
  not asserted inside function cards, the Copy calldata action is present,
  and the Clone Code tab reopens its Proxy identity disclosure before the
  bilingual responsive checks. The documented bundled single-process
  diagnostic run `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make
  test-e2e` passes all 10 embedded browser flows; the ordinary host-access
  retry was unavailable because its permission review timed out.
- P50-T18 replaces CodeMirror's dynamically styled default highlighter with
  Lezer's stable semantic class highlighter, allowing the existing bundled
  `tok-*` theme rules to work under the production CSP for Solidity and Yul.
  The focused six-test artifact suite, TypeScript lint, production build, npm
  audit, generation, and plan checks pass. Preview image
  `sha256:528bfac37637434d5048e5ebe3fa0f551ab70f185276bd09b9ccd844d361bbfa`
  was rebuilt without replacing PostgreSQL or Reth. Live verification at
  `0x5FbDB2315678afecb367f032d93F642f64180aa3` observes distinct computed
  colors for `tok-keyword`, `tok-comment`, and `tok-string` while retaining a
  `0px` first-line gutter delta. Two canonical `make test-e2e` launch requests
  did not reach the test process because sandbox permission review timed out;
  they produced no test failure.
- P50-T17 moves CodeMirror's required editor, scroller, content, gutter, layer,
  and search-panel structure into the bundled stylesheet because the accepted
  production CSP rejects the library's runtime-injected inline stylesheet. An
  embedded-browser regression measures the first source line against its line
  number and requires sub-pixel alignment. The focused six-test artifact suite,
  TypeScript lint, production build, and complete 10-flow `make test-e2e` gate
  pass. Preview image
  `sha256:4321f56f1bd78d16cc32d4627265bf3a81d540e94485d2035795414c72f1804e`
  was rebuilt without replacing PostgreSQL or Reth; live verification at
  `0x5FbDB2315678afecb367f032d93F642f64180aa3` measures a `0px` first-line
  gutter delta with the scroller restored to flex layout.
- P50-T16 replaces raw verified-contract JSON with a verification summary,
  fail-closed inline-source parsing, main-file-first navigation, and a locally
  bundled CodeMirror 6 workspace. Solidity uses the maintained Replit grammar,
  Yul uses bounded local highlighting, and unknown languages remain selectable
  plain text. Search, wrapping, copy feedback, fullscreen, keyboard selection,
  and both CodeMirror read-only fences are covered by focused tests.
- P50-T16 presents optimizer, runs, EVM version, via-IR, metadata, remappings,
  source and library counts without guessing compiler defaults. Complex
  optimizer details, output selection, model checker, linked libraries, the
  full settings object, ABI, constructor arguments, declared transformations,
  and compilation/code artifacts remain copyable disclosures. The existing
  clipboard fallback is shared by the new and existing copy controls.
- P50-T16 verification passes 14 focused artifact/contract tests, the complete
  27-file/224-test Vitest suite, TypeScript lint, and the production build.
  `make test-e2e` passes all 10 embedded Chromium flows, including multi-file
  switching, strict read-only content, settings disclosure, bilingual themes,
  narrow layout, keyboard behavior, and Axe coverage. Browser acceptance also
  caught and closed a production-only CodeMirror chunk cycle by keeping the
  CodeMirror, Lezer, and Solidity grammar graph in one deterministic vendor
  chunk.
- Final `make generate-check`, `make license-check`, `make plan-check`,
  `git diff --check`, and `make check` pass. The web dependency audit reports
  zero vulnerabilities, the production license scan accepts all new packages,
  and the common gate passes generation, lint, ordinary/race tests, security
  scans, Dockerfile/Compose validation, and Helm lint/render checks. The
  independent Hardhat tree retains only its pre-existing low-severity
  `elliptic` advisory below the enforced threshold.
- P50-T15 fixes the ABI result formatter's incorrect assumption that every
  Solidity integer decodes to `bigint`. Viem returns `number` for signed and
  unsigned widths up to 48 bits and `bigint` above 48 bits; the formatter now
  enforces that exact type split and the declared integer range recursively
  through scalar, array, tuple, multi-output, and revert values. An ERC-20
  `decimals() returns (uint8)` form now decodes and renders `18`. ABI `function`
  values are rejected at the verified-ABI boundary because the selected Viem
  codec cannot encode or decode that type, rather than rendering a form that
  can only fail at submission.
- P50-T15 focused verification passes 25 ABI and contract-form tests. The full
  frontend suite passes all 26 files and 219 tests, TypeScript lint and the
  production build pass, and `make test` passes the complete ordinary Go suite
  plus the frontend suite. `GOCACHE=/tmp/etherview-abi-go-cache make
  generate-check`, `make plan-check`, and `git diff --check` pass.
- P50-T15 live Preview acceptance rebuilt image
  `sha256:5889501300a320596a88be76d240cad6a95d357fc557233e00dfc71581d3a53f`
  with `make recreate-preview`; PostgreSQL and Reth were retained and all six
  application roles became healthy. The verified artifact for
  `0x5FbDB2315678afecb367f032d93F642f64180aa3` contains
  `decimals() returns (uint8)`, and an exact `eth_call` with selector
  `0x313ce567` returns the 32-byte encoding of 18.
- P50-T15's dependency closure updates the locked transitive `nanoid` from
  3.3.16 to 3.3.18. `npm --prefix web audit --audit-level=high` reports zero
  vulnerabilities, and the complete `make security-check` passes the
  production build, Go vulnerability scan, working-tree and history secret
  scans, all four npm high-severity audit gates, and the security-focused Go
  tests. The independent Hardhat tree reports only its existing low-severity
  `elliptic` advisory, which remains below the repository's enforced threshold.

- P50-T14 replaces the API-key, ABI-JSON, calldata, and value workbench with
  anonymous verified-artifact-driven function panels. Direct contract and
  implementation pages, implementation calls through a proxy, exact
  `ProxyAdmin` and `UpgradeableBeacon` management targets, overloads, tuples,
  nested arrays, bounded integers, bytes, payable value, multiple returns, and
  decoded reverts share the injected-wallet boundary. Every implementation or
  management submission refreshes and compares binding, chain, account, and
  transaction target; high-risk upgrades expose their exact target and Beacon
  impact, and an unmatchable submission remains an unknown outcome.
- P50-T14 browser acceptance against the rebuilt seven-role Preview covers the
  OpenZeppelin 5.6.1 UUPS proxy at
  `0xDc64a140Aa3E981100a9becA4E685f962f0cF6C9`, Transparent/ProxyAdmin,
  two proxies sharing one UpgradeableBeacon, standard and immutable-args
  Clones, direct-only UUPS `proxiableUUID()`, current implementation links,
  complete upgrade and initialization timelines, and the absence of the old
  manual workbench. Generic or incomplete evidence stays read-only; Clones are
  explicitly immutable and have no upgrade controls.
- P50-T14 Preview replay pins one canonical 0--387 snapshot and completes
  `proxy@2` followed by `abi@2` at 388/388. The final UUPS binding is
  `d3f21003-7683-4f81-b0b7-f12c2128a3c2`; upgrade reindexing invalidates the
  prior binding, shared Beacon history stores one observation and resolves to
  both linked proxies, runtime immutable admin/beacon values remain
  authoritative over compatibility slots, and initialization versions 1 and 2
  retain their event-time implementation linkage. Anonymous artifact and
  proxy/history APIs return the verified artifacts and complete canonical
  histories without an API key; the temporary acceptance key was revoked.
- P50-T14 frontend verification passes TypeScript lint, all 26 Vitest files
  with 217 tests, the production build, and the 10-flow embedded browser E2E
  including bilingual, responsive, keyboard, and accessibility coverage. The
  raw-signing helper used by Reth passes 3 focused Node tests and syntax checks.
  `make generate-check`, `make test-e2e`, `make plan-check`, and
  `git diff --check` pass. The complete `make check` passes generation,
  ordinary/race tests, security and license scans, Dockerfile/Compose
  validation, and Helm lint/render checks.
- P50-T14's final `make test-hardhat3-e2e` uses the pinned real
  `@openzeppelin/contracts@5.6.1` fixtures and passes in 456.74 seconds:
  monolith in 223.80 seconds and the complete distributed topology in 232.20
  seconds. It covers verified and unverified management, runtime immutable
  authority, durable bindings, upgrade invalidation/rebinding, anonymous
  artifacts, and the native proxy APIs. Preview remains running for manual
  review; no commit or pull request was created.
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
- P50-T13: the footer add-network control and its separate floating picker are
  removed. The existing wallet popover now owns one configured network section
  that prefers the active wallet, uses a sole discovered provider directly,
  keeps multi-provider choice inline, disables cleanly without a provider, and
  resets local chooser and result state when the menu closes. Account
  connection, SIWE, public configuration, and the bounded wallet RPC contract
  remain unchanged.
- P50-T13 verification: the focused component suite passes 5 tests and the
  complete frontend suite passes 20 files with 149 tests.
  `npm --prefix web run lint`, `npm --prefix web run build`, and
  `make generate-check` pass. The embedded browser fallback
  `PLAYWRIGHT_USE_BUNDLED=1 PLAYWRIGHT_SINGLE_PROCESS=1 make test-e2e` passes
  all 9 flows, including exact account-independent add-network RPC, 320-pixel
  containment, bilingual dark/light rendering, keyboard behavior, and WCAG
  2.1 A/AA scans. `make check` also passes generation, lint, ordinary/race
  tests, security and license scans, Dockerfile/Compose validation, and Helm
  lint/render checks; sandboxed cache/config reads emitted warnings without
  changing their successful results.
- P50-T19 changes ABI scalar and payable inputs to a stacked heading/type and
  control layout, adds compact themed Query/Write and Copy calldata actions,
  centralizes clipboard fallback reuse, and shows localized wallet connection
  or chain mismatch guidance while keeping calldata copying wallet-independent.
  Focused ABI coverage passes 18 tests, the full frontend suite passes 27 files
  with 229 tests, and `npm --prefix web run lint`, `npm --prefix web run build`,
  `make test`, `make plan-check`, and `git diff --check` pass.
- P50-T20 adds a localized Self shortcut to every address ABI input, including
  nested tuple and array fields. It fills the connected wallet account without
  touching the wallet RPC boundary and stays disabled when no account is
  active. The focused ABI suite passes 19 tests, the complete frontend suite
  passes 27 files with 230 tests, and lint, production build,
  `make generate-check`, `make plan-check`, and `git diff --check` pass.
- P50-T21 reduces the Self control to a compact borderless inset action inside
  the address input, with theme-aware hover/focus/disabled states. The focused
  ABI and stylesheet suites pass 20 tests, and frontend lint, production build,
  and `git diff --check` pass.
- P50-T22 wraps disabled Read/Write actions in hoverable guidance containers so
  browser tooltips remain available even when the buttons cannot receive hover
  events. The focused ABI and stylesheet suites pass 20 tests, frontend lint
  and production build pass, and the wallet guidance title is regression-tested.
- P50-T23 removes the `Actual call target` explanatory label while retaining the
  exact transaction target address for safety-sensitive review. The focused ABI
  suite passes 20 tests, frontend lint and production build pass, and the
  absence of the label is regression-tested.
- P50-T24 replaces the unreliable native hover title with an explicit tooltip
  that opens on hover/focus and dismisses on pointer interaction outside the
  action. The focused ABI and stylesheet suites pass 21 tests, frontend lint
  and production build pass, and `make plan-check` plus `git diff --check` pass.
- P50-T25 changes the wallet guidance from a full-width action-row block to a
  compact floating bubble above the disabled action, with content-sized width,
  theme-aware contrast, shadow, and a short fade/slide transition. The focused
  ABI and stylesheet suites pass 21 tests, and frontend lint, production build,
  `make plan-check`, and `git diff --check` pass.
- P50-T26 removes the rendered transaction target address and its obsolete
  management-impact presentation from ABI forms while preserving the internal
  transaction target used for calls. The focused ABI suite passes 20 tests,
  frontend lint and production build pass, and the absence of the address is
  regression-tested.
- P50-T27 closes the native wallet `details` popover on pointer input outside
  its menu while preserving internal wallet-option interactions. The focused
  WalletMenu suite passes 2 tests, frontend type checking and production build
  pass, and `make plan-check` plus `git diff --check` pass. The complete web
  suite has 230 passing tests and 3 pre-existing failures in ContractPage tests
  that still expect the transaction target removed by P50-T26.
- P50-T28 renders Proxy identity inside the Code tab only when the API returns
  an actual proxy identity, and exposes neither proxy history tab nor history
  request for non-proxy contracts. The focused ContractPage suite passes 10
  tests, the complete frontend suite passes 28 files with 234 tests, and
  frontend lint, production build, `make plan-check`, and `git diff --check`
  pass. Existing ContractPage assertions were aligned with P50-T26's removal
  of rendered transaction targets.
- P50-T29 makes Proxy identity a native details disclosure that is collapsed by
  default, while keeping its title and status visible and preserving all
  identity/evidence content when expanded. The ContractPage suite passes 10
  tests, the complete frontend suite passes 28 files with 234 tests, and
  frontend lint and production build pass.
- P50-T30 removes the duplicate inline unavailable-wallet and chain-mismatch
  notices from ABI forms and keeps the full wallet guidance in the action
  tooltip. The tooltip is anchored within the action area so its text remains
  visible at narrow widths. The focused and complete frontend suites pass (20
  and 234 tests), frontend lint and production build pass, and
  `make plan-check` plus `git diff --check` pass.
- P50-T31 decodes canonical verified constructor arguments with Viem, formats
  named and positional values through the existing bounded ABI formatter, and
  retains a copyable raw-hex section. Missing, ambiguous, oversized, malformed,
  or non-canonical data fails closed to the raw encoding with bilingual guidance.
  Focused ABI and artifact tests pass (20 tests), the complete frontend suite
  passes 28 files with 239 tests, frontend lint and production build pass,
  `GOCACHE=/tmp/etherview-constructor-go-cache make test`,
  `GOCACHE=/tmp/etherview-constructor-go-cache make generate-check`,
  `GOCACHE=/tmp/etherview-constructor-go-cache make plan-check`, and
  `git diff --check` pass. The host-authorized `make test-e2e` passes all 10
  embedded Chromium flows.
- P50-T32 embeds the contract workspace under `/address/:address`, removes the
  legacy `/contract/:address` route, and maps every contract subtab to a
  browser-history-preserving hash. Recognized hashes override activity query
  state, activity links clear hashes, contract lookup/search/artifact/proxy
  links use `#code`, and unresolved EOA/unknown addresses replace an invalid
  contract hash only after address classification. Contract requests remain
  lazy and preserve a requested hash while dynamic tabs load; unavailable
  hashes normalize to `#code`. The complete frontend suite passes 28 files and
  252 tests; frontend lint and production build pass; `make generate-check`,
  `make plan-check`, `git diff --check`, and the host-authorized `make check`
  pass. The embedded Playwright suite passes all 10 flows with the bundled
  single-process browser configuration, including deep links, keyboard/history,
  responsive, bilingual, and WCAG coverage.
- P50-T33 replaces the flat verified-source file list and narrow-layout select
  with one bilingual, keyboard-accessible expandable directory tree. The pure
  tree model preserves complete source paths, places directories before files,
  and keeps malformed entries fail-closed with the existing raw manifest
  disclosure. Focused artifact tests pass all 10 tests; the complete frontend
  suite passes 28 files with 259 tests; frontend lint and production build
  pass. The host-authorized `make test-e2e` passes all 12 embedded Chromium
  flows, including the verified-source, narrow viewport, and WCAG scenarios.
  `GOCACHE=/tmp/etherview-tree-go-cache make generate-check`, `make plan-check`,
  and `git diff --check` pass; the initial generate-check attempt was retried
  with the documented writable cache after the default macOS Go cache denied
  access.
- P50-T34 compacts consecutive directory-only chains into one tree label such
  as `src/contracts/interfaces`, while retaining the exact final directory ID,
  source path, file selection, and editor behavior. The focused artifact suite
  passes 11 tests; the complete frontend suite passes 28 files with 260 tests;
  frontend lint, production build, `make plan-check`, and `git diff --check`
  pass. The host-authorized `make test-e2e` passes all 12 embedded Chromium
  flows, including verified-source, narrow viewport, keyboard, and WCAG
  scenarios.
- P50-T35 removes the address Contract link's special entry styling so it stays
  left-aligned and uses the ordinary activity-tab states while preserving its
  `#code` deep link. A verification 404 now renders explicit bilingual
  unverified-contract guidance instead of the generic not-found notice. The
  focused address/contract suites pass 45 tests; the complete frontend suite
  passes 28 files with 261 tests; frontend lint, production build, and
  `GOCACHE=/tmp/etherview-contract-tab-go-cache make generate-check` pass. The
  host-authorized `make test-e2e` passes all 12 embedded Chromium flows.
- P50-T36 applies the same ordinary activity-tab styling to Delegation, removes
  the unused special entry CSS, and verifies that inactive entries stay neutral
  while the active entry uses the shared green tab background. The focused
  CorePages suite passes 22 tests; the complete frontend suite passes 28 files
  with 261 tests; `make generate-check`, `make plan-check`, `make test-e2e`
  (12 flows), and `make check` pass.
- P50-T37 makes ordinary address tabs explicitly transparent and reserves the
  shared green background for the active state. The focused CorePages/styles
  suite passes 24 tests; the complete frontend suite passes 28 files with 262
  tests; frontend lint/build, `make generate-check`, `make plan-check`, and
  `make check` pass.
- P50-T38 makes all address tab classes JS-controlled from the route-derived
  `activeTab`, disables Router class injection, and keeps the outer Contract
  tab active for nested hashes such as `#read-contract`; the embedded E2E
  regression also verifies inactive Contract styling matches ordinary tabs.
  The focused CorePages/styles suite passes 25 tests; the complete frontend
  suite passes 28 files with 263 tests; frontend lint/build, `make check`,
  `make plan-check`, and `make test-e2e` (12 flows) pass.
- P50-T40 renders transaction logs as compact decoded-first cards with the log
  index in the header, copyable decoded values, per-topic Hex/Address/Text/
  Number conversion, and a collapsed raw/provenance disclosure. Topic text
  decoding is strict UTF-8 and Number conversion uses bigint string output;
  malformed conversions fail closed without changing the raw value.
- P50-T40 verification passes the focused 33-test log/core suite, the complete
  frontend suite with 29 files and 277 tests, TypeScript lint, production
  build, `make plan-check`, `make generate-check`, `git diff --check`, and the
  full host-authorized `make check` gate. The embedded browser suite passes all
  12 flows, including the updated disclosure assertion that opens collapsed
  details before checking exact provenance.
- P50-T41 replaces the event-log argument list with a copyable recursive
  Name/Type/Indexed/Data table. Tuple, struct, and array children use bounded
  jq-style paths without a leading dot, nested Indexed cells show `—`, and
  composite values copy their complete JSON representation. The default
  collapsed More details panel now groups ABI source and execution provenance;
  raw topics/data remain in a separate disclosure with copy controls.
- P50-T41 verification passes the focused log-format/CorePages suite (38
  tests), the complete frontend suite (29 files, 282 tests), frontend lint and
  production build, `make generate-check`, `make plan-check`, `git diff
  --check`, host-authorized `make check`, and `make test-e2e` (12/12 flows).
- P50-T42 removes the duplicate top-level Topics rendering. Topic values and
  Hex/Address/Text/Number conversion controls now live only under the expanded
  More details → Raw topics and data disclosure; topic #0 remains raw event
  signature Hex and converted values remain copyable.
- P50-T42 verification passes focused log/CorePages tests (38), the complete
  frontend suite (29 files, 282 tests), production build, and embedded E2E
  (12/12 flows).
- P50-T43 removes the nested Raw topics and data disclosure and its duplicate
  raw-topic list. More details remains collapsed by default and directly shows
  ABI provenance, convertible topics, and copyable data when expanded. A
  successfully decoded event whose topic count equals its indexed argument
  count is anonymous, so topic zero receives the same Hex/Address/Text/Number
  conversion controls; unknown, malformed, and inconsistent results fail
  closed with topic zero reserved as Hex.
- P50-T43 verification passes the focused log-format/CorePages suite (41
  tests), the complete frontend suite (29 files, 285 tests), frontend lint and
  production build, `make generate-check`, `make plan-check`, `git diff
  --check`, host-authorized `make test-e2e` (12/12 flows), and the full
  host-authorized `make check` gate.
- P50-T44 replaces the generic two-column data definition list with the same
  heading-and-value rhythm as Topics. Its value row reserves the exact topic
  index column and gap, keeping Data aligned with topic zero on desktop and
  narrow layouts while preserving wrapping and copy controls.
- P50-T44 verification passes the focused CorePages/styles suite (30 tests),
  the complete frontend suite (29 files, 285 tests), frontend lint and
  production build, `make generate-check`, `make plan-check`, `git diff
  --check`, and host-authorized `make test-e2e` (12/12 flows). The embedded
  browser asserts the Data and topic-zero value starts differ by at most one
  pixel at both desktop and 390px viewport widths.
- P50-T45 separates decoded and raw transaction calldata into distinct
  sections, keeps the decoded function signature in one heading, and presents
  every matching target or implementation ABI as compact, linked, copyable
  evidence. Raw bytes remain independently copyable from a read-only textarea;
  a low-emphasis text action switches valid UTF-8 in place and leaves invalid
  input in Hex with an explicit status.
- P50-T45 verification passes the focused CorePages suite (27 tests), the
  complete frontend suite (29 files, 285 tests), frontend lint and production
  build, `make generate-check`, `make plan-check`, and `git diff --check`.
  Host-authorized `make test-e2e` passes all 13 flows, including the new
  no-parameter implementation-ABI transaction in English, Chinese, desktop,
  390px, and automated accessibility coverage.
- P50-T47 changes the read-only raw calldata textarea from no-wrap to native
  soft wrapping, backed by `pre-wrap` and long-token breaking so continuous
  Hex stays within the available width without changing its copied value.
  Focused CorePages/styles tests pass (31 tests), frontend lint and production
  build pass, `git diff --check` passes, and host-authorized `make test-e2e`
  passes all 13 flows with the real browser asserting the wrap attribute and
  computed white-space behavior.
- P50-T48 renders only trustworthy submitted contract-write transaction hashes
  as internal links to the transaction Overview route. Unknown outcomes retain
  the existing fail-closed error state without a hash link. The focused ABI
  form suite passes all 21 tests, the complete frontend suite passes all 29
  files and 288 tests, frontend lint and production build pass, `make
  generate-check` passes, and host-authorized `make test-e2e` passes all 13
  flows with the exact transaction-link target and stale-wallet suppression.
- P50-T49 adds an independently paginated Internal Transactions/内部交易 panel
  to the transaction Overview. It shows only successful non-root positive-value
  Trace frames, preserves exact transaction identity fencing and complete-empty
  versus degraded states, and leaves the full Trace tab lazy. ERC-20 quantities
  in transaction, address, and token event tables use exact string-based
  decimal expansion from block-correct metadata; NFTs remain integer-valued and
  missing decimals fall back to the raw integer.
- P50-T49 verification passes the focused frontend suite (4 files, 59 tests),
  the complete frontend suite (29 files, 296 tests), TypeScript lint,
  production build, `make generate-check`, `make test-integration`, and
  `make test-race`. Host-authorized `make test-e2e` passes all 14 browser flows,
  including bilingual/responsive/accessibility coverage and a no-eager-Trace
  assertion; the full host-authorized `make check` gate and `git diff --check`
  pass.
- P50-T50 removes internal ETH transfers from Overview and exposes them only
  through the deep-linkable `internal-transactions` tab immediately before
  Token transfers. The resource request remains lazy, cursor pagination and
  identity fencing are unchanged, and the tab name is localized as Internal
  Transactions/内部交易.
- P50-T50 verification passes the focused frontend suite (3 files, 57 tests),
  the complete frontend suite (29 files, 296 tests), TypeScript lint,
  production build, `make generate-check`, `make test`, and `git diff --check`.
  Both the documented bundled single-process browser diagnostic and the
  host-authorized canonical `make test-e2e` pass all 14 flows, covering exact
  tab order, deep linking, lazy loading, and Trace isolation.
- P50-T51 removes the primary Contracts navigation and standalone `/contracts`
  route, makes that path render the SPA 404, and adds a bilingual verification
  action only for definitively unverified contract artifacts. The action passes
  the validated current address through `/verify` without placing API keys in
  the URL; disabled deployments retain their existing unavailable explanation.
- P50-T51 verification passes the focused frontend suite (3 files, 50 tests),
  the complete frontend suite (29 files, 299 tests), TypeScript lint,
  production build, `make generate-check`, `make plan-check`, and `git diff
  --check`. The host-authorized canonical `make test-e2e` passes all 14 flows,
  including the removed route, address-prefilled action, disabled capability,
  bilingual 390px layout, automated accessibility, and overflow checks.
- P50-T52 replaces the 5,678-line global stylesheet with an ordered import
  manifest and eight responsibility-based modules for foundations, explorer
  views, wallet controls, account surfaces, analytics, verification, verified
  artifacts, and shared responsive behavior. The original rule order and
  declarations are preserved, module ownership and cascade guidance are
  documented, and the stylesheet regression now fences the import order.
- P50-T52 verification passes the focused stylesheet suite (5 tests), the
  complete frontend suite (29 files, 301 tests), TypeScript lint, production
  build, `make generate-check`, `make plan-check`, and `git diff --check`.
