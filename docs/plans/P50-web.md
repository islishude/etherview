# P50 — Embedded Web

Status: `done`

## Outcome

A responsive Chinese/English React SPA covers the core explorer workflows,
uses versioned same-origin explorer APIs, is embedded in the Go binary, and uses an
injected EIP-1193 wallet for all contract reads and writes.

The generated-client explorer includes UserOperation global/detail and bundle
activity, authoritative Token holder pages, and optional consensus/blob views.
The account workspace adds private watches, browser push, and CSV downloads
with session-isolated browser state. Verify uses session-authenticated
submission and independent, recoverable result steps. Optional Turnstile guards
protect browser verification and CSV downloads; address actions use a compact toolbar.

## References

- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0025](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0019](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0005](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [ADR-0041](../decisions/ADR-0041-snapshot-stable-ens-primary-names.md)
- [Architecture](../architecture/overview.md)
- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0013: Embedded SPA serving and browser security](../decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [ADR-0036: Endpoint-scoped mempool replacement observations](../decisions/ADR-0036-endpoint-scoped-mempool-replacements.md)
- [EIP-1193: Ethereum Provider JavaScript API](https://eips.ethereum.org/EIPS/eip-1193)
- [EIP-6963: Multi Injected Provider Discovery](https://eips.ethereum.org/EIPS/eip-6963)
- [Tailwind CSS with Vite](https://tailwindcss.com/docs/installation/using-vite)
- [Tailwind Preflight](https://tailwindcss.com/docs/preflight)
- [ADR-0045](../decisions/ADR-0045-erc4337-useroperation-index.md)
- [ADR-0046](../decisions/ADR-0046-authoritative-erc20-holder-reconciliation.md)
- [ADR-0051](../decisions/ADR-0051-watchlist-notifications-csv.md)
- [ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md)
- [Testing](../testing.md)

## Work Items

| ID      | Status | Depends on                         | Deliverable                                                                                                                                                                                            | Verification                                                                                                                   |
| ------- | ------ | ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------ |
| P50-T01 | done   | P00                                | Route-lazy React/Vite, on-demand CodeMirror, generated client, i18n, accessible light/dark primitives and consistent responsive navigation/page hierarchy                                              | frontend unit/build, asset budgets, core/capability/account/wallet, deep-link and responsive accessibility tests               |
| P50-T02 | done   | P40                                | Home, block/orphan, transaction/address/search pages and shared snapshot-stable bilingual ENS display with exact addresses                                                                             | core Playwright, normalization, snapshot reuse, custom-name distinction, responsive and accessibility tests                    |
| P50-T03 | done   | P20, P10, P40                      | Token/NFT, contract, verify, charts, pending and sync-status pages, inert canonical NFT metadata/traits, stale warnings and per-open media-link confirmation                                           | capability, ERC-721/ERC-1155, generated client, bilingual keyboard/security, embedded browser and Preview tests                |
| P50-T04 | done   | P10, P20, P40                      | EIP-6963 discovery and wallet-only contract read/write forms                                                                                                                                           | provider/mismatch tests                                                                                                        |
| P50-T05 | done   | P50-T01                            | Precompressed immutable assets, deep-link fallback, cache headers and per-shell nonce-authorized editor styles under CSP                                                                               | cache/encoding/ETag/HEAD/fallback, CSP nonce, binary/runtime, asset and accessibility tests                                    |
| P50-T06 | done   | P40-T07, P40-T12, P40-T15, P30-T43 | Tabbed transaction/block protocol details and separate native-transfer panels, structured log ABI provenance, decoded/raw calldata, exact-decimal transfers and automatic pending/replaced transitions | generated client, ABI/failure/identity, bilingual responsive/accessibility, embedded browser and runtime tests                 |
| P50-T10 | done   | P40-T08, P40-T10, P40-T17          | Address activity/assets/withdrawals/protocol transfers, origins/QR/copy, contract entry, native labels/add-network flow, and compact Method/signature disclosure                                       | snapshot, exact Ether/decimals, wallet provider, generation, bilingual responsive/accessibility and browser tests              |
| P50-T11 | done   | P40-T09                            | Atomic version-fenced home state, query-local event policies and one recoverable EventSource including UserOperation reorg refresh without list polling                                                | version-floor, reconnect/reorg, frontend, embedded browser, runtime and generation checks                                      |
| P50-T14 | done   | P40-T11, P50-T10                   | Etherscan-style verified ABI read/write forms for contracts, implementations through proxies, exact management targets, and proxy histories, with real OpenZeppelin 5.6.1 Preview acceptance           | Vitest, generated client, embedded browser, responsive, accessibility, Hardhat 3, monolith/split, and seven-role Preview tests |
| P50-T16 | done   | P50-T14                            | Verified source/artifact workspace, compiler settings, decoded constructor arguments with raw hex, contract deep links and accessible source directory tree                                            | ABI/tree-model, generated client, security/license, bilingual responsive/accessibility and embedded browser tests              |
| P50-T72 | done   | P40-T20, P50-T02, P50-T03          | Bilingual UserOperation search/list/detail, transaction Bundle activity, and address participant-role activity                                                                                         | generated-client, Vitest, responsive/accessibility, browser, and runtime tests                                                 |
| P50-T73 | done   | P40-T21, P50-T03                   | Authoritative ERC-20 holder list/count on the bilingual Token page                                                                                                                                     | generated-client, availability, accessibility, 390px browser, and compatibility tests                                          |
| P50-T74 | done   | P60-T19, P40-T23, P60-T05          | Bilingual private watch/notification workspace and bounded CSV download with account-switch cache isolation                                                                                            | browser/session isolation, generated-client, responsive/accessibility, and production parity tests                             |
| P50-T75 | done   | P40-T22, P50-T06                   | Consensus overview, slot provenance, blob archive status, on-demand preview, and exact-hash downloads                                                                                                  | generated-client, disabled/unavailable/freshness, mobile, browser, and production parity tests                                 |
| P50-T76 | done   | P50-T01, P40                       | Pinned Hey API Fetch SDK with explicit same-origin client, exact types, error/stream preservation and explicit generation ([#169](https://github.com/islishude/etherview/issues/169))                  | type/runtime regressions, deterministic directory generation, Web/build/browser and common gates                               |
| P50-T77 | done   | P40-T24                            | Bilingual Requests tab with five request types, filtering, details and pagination                                                                                                                      | 545 frontend tests and 105 embedded browser scenarios; bilingual 390px accessibility                                           |
| P50-T78 | done   | P50-T03, P60-T03, P60-T05          | Session-authenticated browser verification with two-step submission/results and recoverable result links; API-key protocol isolation                                                                   | authentication matrix, frontend/browser, PostgreSQL, production all/split and common gates                                     |
| P50-T79 | done   | P50-T74, P50-T78                   | Optional runtime Turnstile guards for browser verification and CSV; compact address actions                                                                                                            | transport/auth/CSP, bilingual browser, deployment and all/split regressions                                                    |

## Acceptance

- [x] Independent default-off Turnstile switches protect browser verification
      (including all Sourcify paths) and CSV exports with server-side validation,
      session-bound widget lifecycle and narrowly scoped shell CSP. Native
      API-key clients are unchanged; address actions are compact, bilingual,
      keyboard accessible and responsive in both themes.

- [x] Verify lives under More and preserves address-prefilled entries and file
      uploads. SIWE-authenticated submission opens a separate result step;
      saved job links restore redacted results after login. Native/Etherscan
      APIs remain key-scoped. Terminal/error polling stops; account changes,
      expiration and cancelled requests cannot retain or revive private results.

- [x] The Requests tab loads by exact execution hash and supports five types,
      precise amounts, exit labels, filters, cursor reset, expandable/copyable
      full values, bilingual mobile layout and keyboard access.
- [x] Bilingual core, token, contract and account surfaces use the generated same-origin client.
- [x] Hey API operations require the configured client; ordinary builds use committed
      SDK output while explicit generation and CI enforce complete-directory freshness.
- [x] Explorer routes, deep links and canonical/snapshot identities remain stable.
- [x] Wallet-only RPC and signing enforce provider, chain and exact target fences.
- [x] Verified code, ABI, protocol fields and decoded/raw data retain explicit provenance.
- [x] One durable public EventSource drives live-list invalidation and
      pending/replaced transitions without periodic list polling.
- [x] Embedded assets, CSP, theme, responsive layout and accessibility pass browser coverage.
- [x] The initial asset graph excludes feature pages and CodeMirror; raw and
      compressed asset budgets protect route-lazy delivery.
- [x] Embedded delivery negotiates prebuilt compressed assets using precomputed
      metadata while preserving CSP, ETags, immutable caching, HEAD, and fallback.
- [x] UserOperation search, global/detail, transaction Bundle, and address-role
      views cover both locales and responsive layouts without Bundler RPC.
- [x] Holder list/count is bilingual, accessible, generated-client-only, and
      responsive at 390px; incomplete state is explicitly unavailable.
- [x] Account changes cancel private requests and clear cached data. One public
      EventSource remains. Account tabs put Watchlist after Overview; watches and
      API keys show their lists until the user opens a creation form. P60-T20
      replaces private notification polling/history
      with session-bound browser push and an embedded Service Worker.
      CSV downloads use the generated client and release their object URLs.
- [x] Consensus/blob views distinguish disabled, pending, unavailable, and ready
      states, show source/freshness, and load preview/download only on demand.
- [x] ERC-721 retains exact owner state while ERC-1155 never invents a unique
      owner, and both standards share the same metadata detail route.
- [x] The SPA renders no NFT image and performs no NFT media or NFT-related external request
      before the user confirms each navigation.
- [x] Every external navigation is bilingual, keyboard accessible, opener-free,
      and no-referrer, with the target host visible before confirmation.
- [x] Browser forward input is normalized with Viem; displayed primary names
      are reverse-plus-forward verified and reuse the page's name snapshot.
- [x] Every semantic address display retains an inspectable/copyable exact address and
      visually distinguishes custom ENS names.
- [x] Pending/failed NFT refresh keeps prior canonical content with a bilingual
      stale warning and explicit observation identities; reorg restores the
      prior version and clears the warning.
- [x] ENS is a current presentation identity; historical names, registration,
      avatars/text records and implicit ENS in write forms remain out of scope.
- [x] Query-local event policy, bounded version floors and one recoverable SSE
      connection preserve reorg-aware lists and UserOperation views.
- [x] Global/address Method columns disclose full signatures and retain exact
      fallback labels. Decoded constructor/calldata views preserve raw hex;
      source trees and contract subpages remain keyboard accessible and deep-linkable.

- [x] Block-zero allocation and address origins disclose authenticated Genesis
      provenance. Historical chart overview/detail routes preserve exact tables
      and bounded API snapshots. Transaction/address protocol-transfer panels
      stay separate from traces and top-level value; EIP-7708 log decoding and
      bilingual System Address labels preserve exact address inspection/copying.

## Current Blockers

None. P60/P70 retain independent release blockers.

## Evidence

P50-T79 passed local `make check`, 712 frontend tests, all 134 embedded Chromium
scenarios, `make test-integration-race` and `make test-runtime-e2e`. Tests cover
bounded Siteverify transport, authentication ordering, secret role isolation,
CSP, expiry/retry/account changes, CSV failures and bilingual toolbar screenshots.
Production all/split tests enable both guards after real SIWE, reject missing
tokens on all five protected POSTs and still accept native API-key submissions.
Mock widgets and transports do not establish live Cloudflare domain/secret
correctness or remote CI acceptance.

P50-T78 passed local `make check`, 699 frontend tests, all 131 embedded Chromium
scenarios, `make test-integration-race` and `make test-runtime-e2e`. Production
all/split tests exercise real SIWE, Cookie/CSRF submission, result reads and
native API-key isolation using Geas creation and runtime entrypoints. Local
fixtures do not establish public Sourcify availability or remote CI acceptance.

P50-T77 passed 545 frontend tests and all 105 embedded browser scenarios,
including the new bilingual 390px Requests deep links, five-type details,
exact quantities, filtering, pagination reset, capability states and accessibility.
Generated same-origin client and asset-budget checks also passed; actual
Withdrawals retain their existing semantics.

P50-T76 passed local `make check`, 535 frontend tests, all 102 embedded Chromium
cases, deterministic SDK generation and the Docker Web builder stage. Build-only
workflow changes then passed command-graph regressions, generation, documentation
and deployment checks. Existing asset budgets remain unchanged. This does not
claim remote CI or full production-image/runtime acceptance.

P50-T02/P50-T03/P50-T06/P50-T10 retain Genesis/block-zero, analytics and
Amsterdam native-transfer/protocol-log browser acceptance, including bilingual
system-address labels and exact amounts. These are historical local frontend,
generation, browser and Preview results, not a new browser run.

P50-T02/P50-T03 retain ENS snapshot reuse and exact-address disclosure, bilingual
NFT detail/traits, per-open media confirmation and stale-warning browser,
responsive and accessibility acceptance. Generated contracts and production
all/split flows passed at delivery revisions. P50-T11 includes version-floor,
expired-cursor recovery, single-connection reconnect and UserOperation reorg tests.

P50-T72 passed the feature revision's 25 Chromium cases alongside generated
contracts and common gates. P50-T73 passed frontend and all 26 then-current
Chromium cases, including bilingual accessible 390px Token browsing.
P50-T74 passed local browser/session isolation and real SIWE notification/export
flows in both production role layouts on 2026-09-26.
P50-T75 passed the feature revision's 54 browser scenarios, including consensus
slots, archive status, on-demand preview/download, mobile layout, and freshness.
These historical suite counts describe their individual revisions, not a new
combined browser run. Aggregate/runtime evidence is retained in P30-T35 and
P30-T138.

Retained completed milestones: P50-T01, P50-T02, P50-T03, P50-T04, P50-T05, P50-T06,
P50-T10, P50-T11, P50-T14, P50-T16, P50-T72, P50-T73, P50-T74, P50-T75, P50-T76,
P50-T77, P50-T78, P50-T79.

Frontend unit, generated-client, embedded Chromium, responsive and
accessibility checks passed at the feature revisions. Coverage includes
bilingual explorer routes and deep links, one public EventSource, wallet
provider/identity fences, verified source and ABI forms, transaction decoding,
protocol fields, pending/replaced transitions and nonce-authorized editor styles.

The redesign and subsequent theme regressions passed 380 frontend tests and
44 embedded browser cases, plus
Web lint/build, generation and documentation gates. Those counts describe that
revision, not today's suite. Subsequent small layout, styling and test fixes
are maintained in tests and Git history without separate roadmap items.

Route-lazy delivery, on-demand CodeMirror, and precompressed assets passed
asset-graph budgets, reproducible Web asset baselines, handler cache/encoding/CSP,
Vitest, Playwright, and runtime E2E checks. Shared event-driven invalidation
passed browser coverage without periodic live-list polling.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P50-web.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
