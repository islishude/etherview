# Stylesheet organization

`../styles.css` is the only application entry point. It imports these modules
in cascade order:

1. `foundation.css` — theme tokens, element defaults, accessibility, header,
   and navigation.
2. `explorer.css` — shared explorer views, tables, entity details, and
   transaction pages.
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
tab primitives share the underline rules in `explorer.css`. Selected tabs must
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
