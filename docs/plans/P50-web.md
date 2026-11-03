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

- [ADR-0058](../decisions/ADR-0058-background-ens-synchronization-and-database-only-reads.md)

- [ADR-0052](../decisions/ADR-0052-amsterdam-explorer-support.md)
- [ADR-0025](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0019](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0005](../decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
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

| ID      | Status | Depends on                         | Deliverable                                                                                                                                                                                            | Verification                                                                                                        |
| ------- | ------ | ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| P50-T01 | done   | P00                                | Route-lazy React/Vite, on-demand CodeMirror, pinned generated same-origin Fetch SDK, i18n and accessible responsive primitives                                                                         | Type/runtime/client generation, asset budgets, build/deep-link, frontend and browser accessibility tests            |
| P50-T02 | done   | P40                                | Home, block/orphan, transaction/address/search pages with exact addresses                                                                                                                              | core Playwright, responsive and accessibility tests                                                                 |
| P50-T03 | done   | P20, P10, P40                      | Token/NFT, contract, verify, charts, pending and sync-status pages, inert canonical NFT metadata/traits, stale warnings and per-open media-link confirmation                                           | capability, ERC-721/ERC-1155, generated client, bilingual keyboard/security, embedded browser and Preview tests     |
| P50-T04 | done   | P10, P20, P40                      | EIP-6963 discovery and wallet-only contract read/write forms                                                                                                                                           | provider/mismatch tests                                                                                             |
| P50-T05 | done   | P50-T01                            | Precompressed immutable assets, deep-link fallback, cache headers and per-shell nonce-authorized editor styles under CSP                                                                               | cache/encoding/ETag/HEAD/fallback, CSP nonce, binary/runtime, asset and accessibility tests                         |
| P50-T06 | done   | P40-T07, P40-T12, P40-T15, P30-T43 | Tabbed transaction/block protocol details and separate native-transfer panels, structured log ABI provenance, decoded/raw calldata, exact-decimal transfers and automatic pending/replaced transitions | generated client, ABI/failure/identity, bilingual responsive/accessibility, embedded browser and runtime tests      |
| P50-T10 | done   | P40-T08, P40-T10, P40-T17          | Address activity/assets/withdrawals/protocol transfers, origins/QR/copy, contract entry, native labels/add-network flow, and compact Method/signature disclosure                                       | snapshot, exact Ether/decimals, wallet provider, generation, bilingual responsive/accessibility and browser tests   |
| P50-T11 | done   | P40-T09                            | One recoverable EventSource, atomic version-fenced home state and page-scoped manual browsing refresh with explicit invalidation notices                                                               | Refresh races/failures, reconnect/reorg, version floors, wallet-operation independence and bilingual browser tests  |
| P50-T14 | done   | P40-T11, P50-T10                   | Etherscan-style verified ABI read/write forms for contracts, implementations through proxies, exact management targets, and proxy histories, with real OpenZeppelin 5.6.1 Preview acceptance           | Vitest, generated client, embedded browser, responsive, accessibility, Hardhat 3, monolith/split, and Preview tests |
| P50-T16 | done   | P50-T14                            | Verified source/artifact workspace, compiler settings, decoded constructor arguments with raw hex, contract deep links and accessible source directory tree                                            | ABI/tree-model, generated client, security/license, bilingual responsive/accessibility and embedded browser tests   |
| P50-T72 | done   | P40-T20, P50-T02, P50-T03          | Bilingual UserOperation search/list/detail, transaction Bundle activity, and address participant-role activity                                                                                         | generated-client, Vitest, responsive/accessibility, browser, and runtime tests                                      |
| P50-T73 | done   | P40-T21, P50-T03                   | Authoritative ERC-20 holder list/count on the bilingual Token page                                                                                                                                     | generated-client, availability, accessibility, 390px browser, and compatibility tests                               |
| P50-T74 | done   | P60-T20, P40-T23, P60-T05          | Bilingual private watch/notification workspace and bounded CSV download with account-switch cache isolation                                                                                            | browser/session isolation, generated-client, responsive/accessibility, and production parity tests                  |
| P50-T75 | done   | P40-T22, P50-T06, P60-T05          | Consensus/slot/archive views and deliberate session-only blob preview; download complete cached bytes from expanded details                                                                            | Capability/freshness, single-fetch save, cancellation/identity cleanup and bilingual desktop/mobile browser tests   |
| P50-T77 | done   | P40-T24                            | Bilingual Requests tab with five request types, filtering, details and pagination                                                                                                                      | Generated-client, five-type/filter/cursor browser regressions and bilingual 390px accessibility                     |
| P50-T78 | done   | P50-T03, P60-T03, P60-T05          | Session-authenticated browser verification with two-step submission/results and recoverable result links; API-key protocol isolation                                                                   | authentication matrix, frontend/browser, PostgreSQL, production all/split and common gates                          |
| P50-T79 | done   | P50-T74, P50-T78                   | Optional runtime Turnstile guards for browser verification and CSV; compact address actions                                                                                                            | transport/auth/CSP, bilingual browser, deployment and all/split regressions                                         |
| P50-T81 | done   | P40-T27                            | Bilingual name search/detail/links and expandable ENS history; atomic explicit reads, expiry hiding and incomplete-directory notices                                                                   | Keyboard/mobile/theme, inert values, pagination, local expiry, single SSE and production topology regressions       |
| P50-T86 | done   | P40-T33, P50-T10                   | Bilingual contract event log tab with mutually exclusive historical-name and positioned-topic filters                                                                                                  | Generated client, bilingual URL/filter/page, keyboard/mobile and production runtime checks                          |
| P50-T87 | done   | P40-T34, P50-T86                   | Compact event browsing with searchable dropdown, draft range filters and expandable logs                                                                                                               | 992 frontend tests, 235 browser tests, keyboard/IME and real Preview range checks                                   |

## Acceptance

- [x] Bilingual responsive explorer, account and contract surfaces use only the
      generated same-origin client; wallet RPC/signing stays provider/chain fenced.
- [x] Routes/deep links, exact-address disclosure and verified source/ABI/decoded
      evidence retain provenance and accessible keyboard/mobile interactions.
- [x] Route-lazy/editor loading and precompressed assets meet budgets and preserve
      cache/CSP/ETag/HEAD/fallback semantics.
- [x] One EventSource refreshes only home/status; asynchronous jobs retain terminal
      polling. Ordinary browsing uses page-scoped explicit refresh and notices.
      Expiry/invalidation withdraws stale ENS fields without background reads.
- [x] UserOperation, holder, consensus, request and native-transfer views retain
      completeness/provenance, precise amounts and bilingual capability states.
- [x] ENS name detail/search/history preserves independent field validity,
      canonical pagination, inert record values and bounded lazy expansion.
- [x] NFT pages render inert metadata, exact ERC-721 ownership and no invented
      ERC-1155 owner; external media requires confirmation for each navigation.
- [x] Blob preview requires session and deliberate action; expanded details save
      complete cached bytes once, with cancellation and identity-change cleanup.
- [x] Account changes cancel private requests and clear state. Session-only
      verification, private tools and browser push retain independent identity;
      CSV uses bounded generated-client downloads and releases object URLs.
- [x] Optional Turnstile protects verification/CSV with server validation,
      session-fenced widgets and narrow CSP; native key clients are unchanged.

- [x] Contract-only event logs retain mutually exclusive signature/topic filters,
      URL restoration, explicit refresh, bounded pagination and visible coverage;
      shared log cards preserve raw data and decoding provenance.

## Current Blockers

The current-chain ENS feature and representative-publication milestones are
complete with [new acceptance evidence](../reviews/ens-current-chain-acceptance.md).
Full directory coverage remains separately reported deployment progress; P70
retains its release security, capacity, recovery and other existing gates.

## Evidence

P50-T02/P50-T03/P50-T04/P50-T06/P50-T10/P50-T14/P50-T16/P50-T72/
P50-T73/P50-T74/P50-T77 passed frontend unit, generated-client, embedded
Chromium and responsive/accessibility checks at their feature revisions.
Shared production topology evidence remains in P30-T35. Coverage includes bilingual explorer/account/contract pages, exact-address and ABI
provenance, wallet fences, protocol/native-transfer views, UserOperations,
holders, NFT metadata/stale states and per-open external-link confirmation.

P50-T01/P50-T05 passed deterministic generated SDK, route/editor lazy loading,
asset budgets, compressed delivery, cache/encoding/ETag/HEAD/fallback and CSP
nonce checks. Ordinary builds consume committed generated output.
P50-T11's manual-refresh acceptance passed 780 frontend tests, 39 tooling tests
and 170 browser scenarios, including all Address tabs, races/failures, ENS
withdrawal, one-SSE recovery and keyboard/mobile controls. Later wallet tests
covered real simulated read/write calls despite unrelated browsing invalidation.

P50-T78/P50-T79 passed SIWE/CSRF verification, recoverable job links,
account-switch cancellation, Siteverify transport, CSP/secret isolation and
all/split authentication tests. Native key clients remain separate. Local mock
widgets do not establish Cloudflare domain/secret correctness or public Sourcify
availability. P60-T20 retains independent notification-provider evidence.

P50-T81 passed name display/detail/search and lazy expandable history in both
languages/themes and 390px/1440px layouts, with keyboard access, inert values,
expiry, pagination and production runtime/Preview checks. See the
[profile extension report](../reviews/ens-current-chain-acceptance.md#registration-profile-local-acceptance); local
history/profile fixtures do not extend representative Mainnet acceptance.

P50-T75's session-only preview shows the first 1024 bytes and saves complete
cached content without another request. Identity change/unmount cancels reads
and discards bytes. Bilingual desktop/mobile checks passed in the recorded
225-test browser suite; shared commands/image/environment are in
[P20 archive acceptance](P20-enrichment.md#blob-access-and-archive-update).
All counts describe historical revisions, not tests rerun by documentation
cleanup. Local browser fixtures do not establish live-chain or release acceptance.

P50-T86 passed the generated-client and frontend lint gates, all 983 frontend
tests and 235 embedded Chromium scenarios, including bilingual mobile/keyboard
controls, URL restoration, filter switching, page reset, validation and EOA
exclusion. Production all/split checks compare nonempty raw logs, positioned
topic results and published signature results. Shared database/query acceptance
is recorded under P40-T33; local browser fixtures do not extend live coverage
or close P70 release gates.

P50-T87 passed compact expandable logs, draft/submit/reset ranges, searchable
paged dropdown, keyboard/IME/focus recovery, stale responses and precise URL
restoration. Frontend (992), full browser (235), focused final browser and
production all/split gates passed. Real Preview checked 36 event APIs and
English desktop/Chinese 390px light/dark range views; reports and screenshots
remain in the Preview-tool run.
