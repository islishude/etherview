# Stylesheet organization

`../styles.css` is the global application entry point. It imports these modules
in cascade order. Shared legacy rules are imported in `layer(components)`,
below Tailwind utilities; feature-owned lazy CSS remains scoped to its consumers.
`ui-theme.css` follows foundation and maps shadcn semantic colors to existing
tokens without overwriting `--muted` or the radius lengths. No Preflight is loaded.
The remaining shared import order is:

1. `foundation.css` — theme token values, element defaults, header and navigation.
2. `ui-theme.css` — Tailwind theme mappings, dark variant and primitive defaults.
3. `explorer.css` — import-only manifest for the explorer modules listed below.
4. `wallet.css` — contract workspace, wallet controls, actions, and notices.
5. `analytics.css` — shared form controls and chart pages.
6. `forms.css` — shared field/form layouts used across routes.
7. `responsive.css` — cross-module responsive overrides.

The declared components sublayers preserve the order foundation, explorer,
wallet, account, analytics, verification, responsive even when a stylesheet
loads later. `account.css` loads with account, billing, Watchlist and export
consumers in `components.account`. `verification.css` loads with verification,
contract and delegation consumers in `components.verification`. These rules
therefore stay below both responsive overrides and primitive utilities.
`pages/PersonalBilling.css` retains its lazy personal-billing layout.

The lazy contract page imports `../pages/ContractPage.css` for its proxy summary
and facts; these rules do not enter the initial shell stylesheet. Shared loading
indicators stay in `explorer/shared.css`.

`ContractArtifactPanel` imports `artifacts.css` for verified source browsing,
file-tree icons, CodeMirror, and compiler data. Its responsive rules remain in
that stylesheet; these feature styles do not enter the initial shell.

Keep feature rules in their owning module. Put a component's narrow-layout
rule beside the component when it is self-contained; reserve `responsive.css`
for overrides that coordinate several modules. Add reusable colors, radii,
shadows, and font families to `foundation.css` instead of duplicating literal
values. Preserve the import order unless the intended cascade change is tested.

## Explorer ownership and responsive rules

`explorer.css` imports these modules in order:

1. `explorer/shared.css` — page layout, domain status colors, and notices.
2. `explorer/home.css` — shared panel headings, consensus metrics and recent activity.
   `pages/HomePage.css` loads home summary and ENS participant layout with the route.
3. `explorer/tables.css` — tables and list notes.
4. `explorer/chain.css` — coverage and reorg context.
5. `explorer/pending.css` — pending snapshots, fees, and pagination.
6. `explorer/entities.css` — entity details, copy controls,
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

Use repository-owned shadcn/ui primitives and Tailwind utilities for shared layout,
feature-owned semantic classes for complex presentation, and foundation tokens
for theme values. Adding a stylesheet does not require a preprocessor, another
layer, or a new runtime dependency. This organization does not require rewriting
existing components into utility classes.

`explorer/requests.css` is scoped to the Requests panel and imported by
`RequestsPanel.tsx` in the lazy block route. Vite loads it with that route so
request-card presentation does not increase the initial shell asset budget.
Keep its selectors feature-specific and its colors on foundation tokens; it
must not override shared page or navigation styles.

Address identity and select-arrow styles live beside `AddressIdentity` and
`ui/native-select` and load with their consumers. `SelectControl` re-exports the
native-select adapter. Shared primitive ownership and local adaptations are in
[`components/ui/README.md`](../components/ui/README.md).

## Design guidance

The [design style guide](../../../docs/design-style-guide.md) owns visual principles,
shared component presentation, icons, interaction feedback, and visual acceptance.
Read it before changing styles; use its review checklist for subsequent UI changes.
