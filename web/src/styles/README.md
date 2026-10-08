# Stylesheet organization

`../styles.css` is the only application entry point. It imports these modules
in cascade order:

1. `foundation.css` — theme tokens, element defaults, accessibility, header,
   and navigation.
2. `explorer.css` — import-only manifest for the explorer modules listed below.
3. `wallet.css` — contract workspace, wallet controls, actions, and notices.
4. `account.css` — account, API key, administration, and billing pages.
5. `analytics.css` — shared form controls and chart pages.
6. `verification.css` — verification, proxy history, and ABI forms.
7. `artifacts.css` — verified source browsing, CodeMirror, and compiler data.
8. `responsive.css` — footer/network-picker additions, animations, and shared
   responsive overrides.

Keep feature rules in their owning module. Put a component's narrow-layout
rule beside the component when it is self-contained; reserve `responsive.css`
for overrides that coordinate several modules. Add reusable colors, radii,
shadows, and font families to `foundation.css` instead of duplicating literal
values. Preserve the import order unless the intended cascade change is tested.

## Explorer ownership and responsive rules

`explorer.css` imports these modules in order:

1. `explorer/shared.css` — page layout, panels, status badges, and notices.
2. `explorer/home.css` — home metrics and recent activity.
3. `explorer/tables.css` — tables, address identity cells, and list notes.
4. `explorer/chain.css` — coverage and reorg context.
5. `explorer/pending.css` — pending snapshots, fees, and pagination.
6. `explorer/entities.css` — entity details, shared tabs, copy controls,
   dialogs, NFT metadata, and address activity.
7. `explorer/transaction.css` — transaction overview, fees, and status overrides.
8. `explorer/calldata.css` — decoded/raw calldata and failure details.
9. `explorer/logs.css` — shared transaction panels, logs, CWIA argument tables,
   and state changes.
10. `explorer/traces.css` — trace frames and values.

Keep combined selectors intact when several pages share a rule. Transaction
status overrides intentionally follow the shared badges; log and trace rules
follow shared entity and transaction presentation. Do not deduplicate these
rules or change their specificity as part of moving files.

Feature-only responsive rules live at the end of their owning module, retaining
media conditions and relative order. `responsive.css` keeps shell/footer/network
rules, animations, reduced motion, generic form controls, and selector lists
spanning multiple modules (for example, transaction detail/log/state rows and
home/status/contract/account layouts). The mobile `.detail-item` override also
stays here: transaction rows carry that class and need its final 0.3rem gap
after the shared transaction/log/state row rule. These rules retain their final position
so feature declarations cannot override them accidentally. Existing 700, 767,
980 and 1023px feature breakpoints retain their exact meanings; do not normalize
them to a different breakpoint scale while reorganizing styles.

Use existing Tailwind utilities and `DesignPrimitives` for shared layout,
feature-owned semantic classes for complex presentation, and foundation tokens
for theme values. Adding a stylesheet does not require a preprocessor, another
layer, or a new runtime dependency. This organization does not require rewriting
existing components into utility classes.

## Visual system

The explorer uses neutral surfaces with a blue brand accent in both themes.
`foundation.css` owns all shared color, typography, radius and shadow tokens:
brand colors identify interactive elements, while `--success`, `--warning`
and `--danger` communicate outcomes independently of the brand. Use the system
sans-serif stack for UI and monospace for hashes, addresses and source. Numeric
values use tabular figures. Panels use 8–12px corners and fine borders; reserve
`--shadow` for popovers and dialogs rather than ordinary content cards.

The shared shell has a 1280px maximum content width. Below 1024px, explorer
navigation becomes an expandable link list; below 768px, search occupies its
own row and summary grids stack. Wide data tables scroll within their own
focusable container. Language, theme, wallet, account and notification controls
remain reachable without expanding explorer navigation.

`NavigationDisclosure` contains ordinary links with native Tab order, not ARIA
menuitems. Escape returns focus to its trigger, and outside pointer/focus and
link activation close it. Keep wallet connection and SIWE account identity
separate, preserve capability-gated links, and never add initial imports of
route-only dependencies for shell decoration.

Shared page titles use `PageHeading` from `DesignPrimitives`; domain pages retain
their own identifiers and summary content. The account, contract and transaction
tab primitives share the underline rules in `explorer/entities.css`. Selected tabs must
retain visible text and keyboard focus rather than relying on a filled surface.
Use `numeric` for right-aligned quantities without rounding source strings.

## Visual acceptance

`make test-e2e` serves the production SPA through the Go embedded browser fixture.
`e2e/redesign.spec.ts` checks seven representative routes at 390, 768 and 1440px,
in English and Chinese, in both themes. Each route checks WCAG 2.1 AA with axe,
page overflow, CSP/console failures and unexpected external requests, then saves
a named screenshot in `web/test-results/` and attaches it to the Playwright
report. Inspect these artifacts after structural or token changes. The account
and Watchlist browser flows also save authenticated account, billing-admin and
notification views. Navigation tests cover keyboard order, Escape, focus
restoration, mobile disclosure and capability-gated entries.
