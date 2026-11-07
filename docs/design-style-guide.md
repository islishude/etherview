# Design Style Guide

Etherview's interface must remain simple, modern, information-first, and consistent
across the application. This guide defines the visual and interaction rules for
new pages and subsequent style changes, based on the current explorer design.
It is required reading before changing UI presentation or shared components.

The [stylesheet guide](../web/src/styles/README.md) owns CSS module responsibilities
and cascade order. The [development guide](development.md) owns change workflow;
the [testing guide](testing.md) and [Makefile](../Makefile) own verification commands
and their scope. The accepted [browser boundary ADR](decisions/ADR-0013-embedded-spa-serving-and-browser-security.md)
continues to govern accessibility, asset loading, and browser security.

## Design principles

- **Simple:** show the information and actions needed for the current task. Use
  clear grouping and progressive disclosure for secondary detail without hiding
  essential facts, status, or recovery actions.
- **Modern:** use restrained neutral surfaces, clear typography, fine borders,
  and purposeful feedback. Avoid decorative gradients, heavy shadows, excessive
  accent colors, unnecessary nested cards, and animation without a functional role.
- **Information-first:** make dense chain data easy to scan and compare. Maintain
  precise values, readable labels, and useful context; visual simplification must
  not remove information or weaken accessibility.
- **Consistent:** reuse shared components and theme tokens. The same action or
  state should look and behave the same across routes, themes, and languages.

## Color, typography, and layout

The explorer uses neutral surfaces with a blue brand accent in both themes.
[`foundation.css`](../web/src/styles/foundation.css) owns all shared color, typography, radius and shadow tokens:
brand colors identify interactive elements, while `--success`, `--warning`
and `--danger` communicate outcomes independently of the brand. Use the system
sans-serif stack for UI and monospace for hashes, addresses and source. Numeric
values use tabular figures. Panels use 8–12px corners and fine borders; reserve
`--shadow` for popovers and dialogs rather than ordinary content cards.

The shared shell has a 1280px maximum content width. Below 1024px, explorer
navigation becomes an expandable link list; below 768px, search occupies its
own row and summary grids stack. Wide data tables scroll within their own
focusable container. Language, theme, wallet and account controls
remain reachable without expanding explorer navigation.

`NavigationDisclosure` contains ordinary links with native Tab order, not ARIA
menuitems. Escape returns focus to its trigger, and outside pointer/focus and
link activation close it. Keep wallet connection and SIWE account identity
separate, preserve capability-gated links, and never add initial imports of
route-only dependencies for shell decoration.

Keep spacing compact but readable. Reuse the spacing and alignment of neighboring
shared components, with clear separation between sections and related controls.
Avoid oversized empty areas or tighter layouts that impair scanning or interaction.
Preserve existing responsive breakpoints and their feature-specific meanings as
documented in the stylesheet guide.

Long addresses and hashes may be shortened visually only when the complete value
remains accessible through the existing detail, disclosure, or copy interaction.
Never replace the underlying value with a truncated string. Keep exact numeric
values available and distinguish unavailable data from an authoritative empty or
zero result. Loading, empty, error, and retry states belong with the affected data.

## Shared components

Use the repository-owned shadcn/ui primitives in `web/src/components/ui` for
buttons, fields, cards, badges, notices, tables, pagination and tabs.
`DesignPrimitives` retains the application frame and a semantic section adapter
for Card. Import components directly from their owning module; no global barrel.
Use Button variants (`default`, `outline`, `secondary`, `ghost`, `link`,
`destructive`) and sizes instead of adding another action style. Links use
`asChild` so hrefs and modified-click navigation remain native. Table containers
remain focusable and owned by the calling view.

shadcn semantic colors in `styles/ui-theme.css` map to the foundation tokens.
The existing `--muted` foreground and radius lengths must not be overwritten
with shadcn defaults. Shared styles use the components cascade layer so explicit
component utilities win; route-specific rules retain their own lazy imports.
No Tailwind Preflight reset or external runtime styling is introduced. Keep
caller-owned selection and feedback states in explicit utility variants when
shared utilities otherwise override their legacy cascade layer.

Native checkbox/radio and select adapters preserve browser form behavior;
Radix owns local tab selection and keyboard navigation. Route tabs retain links
and router-controlled state with the same shared underline treatment. Navigation
disclosures, searchable business selectors, native dialogs and Message retain
their established lifecycle, focus and CSP boundaries.
Do not introduce an alternative treatment for the same kind of action.

Shared page titles use `components/PageHeading`; domain pages retain
their own identifiers and summary content. Manual refresh occupies the heading action
slot; its route-loaded styles live in `api/browsingRefresh.css`. Keep stale status
compact, preserve its accessible description on mobile, and expose the hint on
keyboard focus with Escape dismissal. The account, contract and transaction
tab primitives share the underline treatment in `components/ui/tabs.tsx`. Selected tabs must
retain visible text and keyboard focus rather than relying on a filled surface.
Use `numeric` for right-aligned quantities without rounding source strings.

## Icons and selectors

All application UI icons use Lucide through `UIIcon`, with a 2px stroke and
14px inline/status, 16px control, 20px activity, or 24px illustration sizes.
Import the chosen Lucide icons in their owning module to retain route splitting;
do not add a global icon registry. `UIIcon` hides decorative SVGs from assistive
technology; buttons and links own translated names, titles and state. Icon-only
controls retain visible keyboard focus, and state changes must also update text
or accessible names. Containers own spacing and color, never SVG size overrides.
Dropdown indicators use `DropdownChevron` with a 14px Lucide chevron and a 1.5px
stroke. Form indicators sit 12px from the right edge with 36px text clearance.
Custom dropdowns rotate from their actual expanded state over 180ms, respecting
reduced motion; native selects keep a downward indicator.
`SelectControl` retains a native select and adds the shared decorative chevron;
forced-colors mode restores the browser arrow. Loading and disclosure icons
respect reduced motion. Brand assets, wallet/provider imagery, QR payloads and
data visualizations are content rather than UI icons and keep their own rendering.
`SourceEditorIcons` adapts the pinned CodeMirror search and go-to-line close
buttons to the same Lucide SVGs while retaining the editor's native commands
and focus handling. Editor upgrades must retain the bilingual keyboard regression.

## Feedback and accessibility

`components/Message` owns transient operation feedback. Messages stack at the
viewport top, use semantic theme colors and Lucide status icons, dismiss after
three seconds, and pause while hovered or focused. They never take initial focus;
manual close returns focus to the previous control. Native modal dialogs own
their message stack so feedback remains in the accessible top layer. Identical
concurrent messages coalesce, and caller teardown removes feedback and timers.
Keep read/loading states, field validation and durable transaction/payment
outcomes in their owning views.

Confirmation actions use the shared `components/ui/alert-dialog` shadcn adaptation
with native modal hosting. Focus cancellation initially, contain keyboard focus,
allow Escape cancellation, ignore backdrop clicks, and restore focus on close.
Only explicit confirmation runs the action; session and target changes discard
pending confirmation. Keep business checks and translations with the caller.

Support both light and dark themes and English and Chinese in every changed view.
Use semantic theme tokens rather than page-local color literals. Leave room for
translated labels and preserve accessible names for icon-only actions. Status
must have text or an accessible name as well as color or an icon.

Keep semantic landmarks, the skip link, visible keyboard focus, and keyboard
access to controls and scrollable tables. Dialogs and disclosures retain their
focus and dismissal behavior. Respect reduced motion for feedback and transitions;
animations must communicate state without becoming necessary to understand it.
Charts retain an exact-value alternative. Do not add external fonts, decorative
runtime dependencies, or eager feature imports to achieve a visual effect.

## Visual acceptance

`make test-e2e` serves the production SPA through the Go embedded browser fixture.
[`appearance.spec.ts`](../web/e2e/appearance.spec.ts) checks seven representative routes at 390, 768 and 1440px,
in English and Chinese, in both themes. Each route checks WCAG 2.1 AA with axe,
page overflow, CSP/console failures and unexpected external requests, then saves
a named screenshot in `web/test-results/` and attaches it to the Playwright
report. Inspect these artifacts after structural or token changes. The account
and Watchlist browser flows also save authenticated account, billing-admin and
browser notification settings. [`navigation.spec.ts`](../web/e2e/navigation.spec.ts) covers keyboard order, Escape, focus
restoration, mobile disclosure and capability-gated entries.

## Change workflow and review checklist

Read this guide before proposing visual changes. Prefer existing components and
foundation tokens; keep feature styling in its owning module. When changing a
global visual rule, update this guide and the affected shared implementation in
the same change, and review all affected consumers. A single page must not create
its own competing visual system. Any exception must be explicitly justified in
the PR and reconciled with this guide; accepted ADR boundaries remain mandatory.

Include the following in the PR description or completion report:

- [ ] Explain the user need and how the change preserves simple, modern,
      information-first presentation; remove decoration without a clear purpose.
- [ ] Identify reused components and tokens, and justify any new shared pattern
      or global rule. Update this guide when the design contract changes.
- [ ] Check both themes and languages, readable hierarchy, precise data, and
      loading, empty, error, and persistent result states where affected.
- [ ] Review narrow, tablet, and desktop layouts, long identifiers, translated
      labels, table scrolling, and access to navigation and account controls.
- [ ] Verify keyboard operation, visible focus, accessible names, non-color status
      cues, reduced motion, and applicable accessibility scans.
- [ ] Record the applicable commands, results, inspected screenshots, and any
      evidence limits. Follow the testing guide for the required gates.

Human review determines whether a change remains simple and modern. Existing
checks verify measurable behavior; a passing scan or a saved screenshot alone
does not establish visual quality. Inspect the screenshots rather than treating
their creation as approval. Documentation-only changes require documentation and
applicable governance checks, not unrelated browser runs; they do not establish
new visual acceptance evidence.
