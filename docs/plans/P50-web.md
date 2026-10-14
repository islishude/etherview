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

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P50-T01 | done | P00 | React/Vite routing, generated API client, i18n, theme, design primitives | frontend unit/build tests |
| P50-T02 | done | P40 | Home, blocks/orphans, transactions, addresses, and search pages | Playwright core flows |
| P50-T03 | done | P20, P10, P40 | Token/NFT, contract, verify, charts, pending, and sync-status pages | capability UI tests |
| P50-T04 | done | P10, P20, P40 | EIP-6963 discovery and wallet-only contract read/write forms | provider/mismatch tests |
| P50-T05 | done | P50-T01 | Embedded assets, deep-link fallback, cache headers, CSP, accessibility | binary E2E and a11y tests |
| P50-T06 | done | P40-T07 | Etherscan-inspired tabbed transaction detail system | frontend, embedded E2E, responsive and a11y tests |
| P50-T10 | done | P40-T08 | Address activity tabs, lazy assets, and contract-address entry | frontend, embedded E2E, and generation checks |
| P50-T11 | done | P40-T09 | Atomic home snapshot EventSource with no REST polling fallback | frontend, embedded E2E, and generation checks |
| P50-T12 | done | P40-T10 | Address origins, QR/copy header, ERC-20 assets, configurable native labels, and add-network wallet flow | frontend unit, build, and generation checks |
| P50-T14 | done | P40-T11, P50-T12 | Etherscan-style verified ABI read/write forms for contracts, implementations through proxies, exact management targets, and proxy histories, with real OpenZeppelin 5.6.1 Preview acceptance | Vitest, generated client, embedded browser, responsive, accessibility, Hardhat 3, monolith/split, and seven-role Preview tests |
| P50-T16 | done | P50-T14 | Etherscan-inspired verified source workspace, structured compiler settings, and summarized contract artifacts | focused frontend, embedded browser, responsive, accessibility, generation, security, and license gates |
| P50-T31 | done | P50-T16 | Decode verified constructor arguments into readable ABI parameter rows while retaining raw hex | focused ABI, component, and embedded browser regressions |
| P50-T32 | done | P50-T16 | Embed Contract beside address activity tabs and support hash-driven contract subpage deep links | focused frontend, embedded browser, accessibility, and generation regressions |
| P50-T33 | done | P50-T32 | Replace the verified-source flat file list with an accessible expandable directory tree on desktop and narrow layouts | focused tree-model, component, accessibility, responsive, and common frontend gates |
| P50-T39 | done | P40-T12, P50-T33 | Protocol detail tabs for transactions and blocks, withdrawals, typed transaction fields, and address origin evidence | focused frontend, generated-client, responsive, accessibility, and embedded browser regressions |
| P50-T41 | done | P50-T39 | Structured ABI provenance and recursive Name/Type/Indexed/Data event log decoding | focused frontend, responsive, accessibility, and common frontend gates |
| P50-T45 | done | P50-T41 | Separate decoded and raw transaction calldata with compact ABI evidence and read-only raw conversion | focused frontend, bilingual, responsive, accessibility, and embedded browser gates |
| P50-T49 | done | P40-T13, P50-T45 | Transaction-overview internal ETH transfers and exact-decimal ERC-20 transfer quantities | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |
| P50-T55 | done | P40-T15, P40-T14, P50-T49, P30-T48 | Preview pending and replaced transaction details with automatic transitions and iconized transaction statuses | focused frontend, bilingual, responsive, accessibility, embedded browser, and runtime E2E gates |
| P50-T56 | done | P40-T16, P50-T55 | Lazy address withdrawal history and exact Ether display for address and block withdrawals | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |
| P50-T57 | done | P40-T17, P50-T56 | Compact transaction-list Method column with full-signature disclosure and exact fallback labels | focused frontend, bilingual, responsive, accessibility, embedded browser, and common gates |
| P50-T62 | done | P50-T16, P40-T18, P30-T15, P50-T57, P70-T29 | Authorize CodeMirror runtime styles with a per-SPA-shell CSP nonce | Go handler, frontend, embedded Chromium, and common gates |
| P50-T64 | done | P50-T62 | Neutral light/dark design tokens, blue brand and accessible grouped responsive navigation | focused navigation/theme tests, frontend and embedded browser gates |
| P50-T65 | done | P50-T64 | Unified home, explorer lists and entity detail hierarchy | core page, deep-link, responsive and accessibility regressions |
| P50-T66 | done | P50-T65 | Consistent contract, analytics, account, notification and administration surfaces | capability/account/wallet regressions and frontend gates |

## Acceptance

- [x] Bilingual core, token, contract and account surfaces use the generated same-origin client.
- [x] Explorer routes, deep links and canonical/snapshot identities remain stable.
- [x] Wallet-only RPC and signing enforce provider, chain and exact target fences.
- [x] Verified code, ABI, protocol fields and decoded/raw data retain explicit provenance.
- [x] One public EventSource drives invalidation and pending/replaced transitions.
- [x] Embedded assets, CSP, theme, responsive layout and accessibility pass browser coverage.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P50-T01, P50-T02, P50-T03, P50-T04, P50-T05, P50-T06,
P50-T10, P50-T11, P50-T12, P50-T14, P50-T16, P50-T31, P50-T32, P50-T33, P50-T39,
P50-T41, P50-T45, P50-T49, P50-T55, P50-T56, P50-T57, P50-T62, P50-T64, P50-T65,
P50-T66.

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

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P50-web.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
