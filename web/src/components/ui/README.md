# Shared UI primitives

Etherview owns the adapted shadcn/ui source in this directory. `web/components.json`
configures the existing Vite/Tailwind 4 project, aliases and registry style.
Upstream sources are the official `https://ui.shadcn.com/r/styles/new-york-v4/`
registry entries (alert-dialog, button, input, textarea, card, badge, table, skeleton and tabs).
The MIT notice is retained in `licenses/shadcn-ui-MIT.txt`.

Import individual modules. Keep business data, translations, API calls and
wallet/session ownership outside these primitives. Use `cn` for utility overrides
and CVA variants for shared treatments. `foundation.css` owns brand values;
`ui-theme.css` maps shadcn semantic utility colors to them. Existing dark mode
uses `data-theme`, system fonts and no Preflight reset.

## Local adaptations

- Cards accept `asChild` to preserve section/article/form landmarks and leave
  spacing and layout with the feature. They have no default decorative shadow.
- Buttons preserve native `type` and form behavior. Links use `asChild`; always
  set `type="button"` for non-submit controls inside a form. Disabled actions,
  focus rings and destructive colors work in both themes. UIIcon owns SVG sizes.
  Caller-owned pressed, selected and success states use explicit utility variants
  so shared component utilities do not override state colors from legacy layers.
- NativeSelect preserves native `size`, `multiple`, refs and forced colors, and
  uses DropdownChevron. Checkbox and Radio are native adapters retaining validity,
  FormData and label activation. Label supports compound field wrappers.
- Table does not add a second scrolling wrapper. Keep the existing focusable,
  labelled container and feature-specific minimum widths. Numeric cells retain
  right alignment and exact strings; rows and headings remain semantic HTML.
- Local Tabs use Radix controlled values and roving keyboard focus. Existing
  panels retain IDs, translated names and conditional mounting; pair each trigger
  with its explicit `aria-controls`. NavigationTabs/NavigationTab share styling
  while preserving router links, URL state and existing keyboard behavior.
- Alert does not impose a live region. The caller chooses status/alert/note or
  ordinary content semantics. Message retains its caller-scoped timers, deduping,
  native-dialog hosting and focus restoration. Non-confirmation dialog and search-selector behavior
  stays with the owning component. No floating-layer or toast runtime is added.
- AlertDialog adapts the shadcn Alert Dialog layout to a native modal `dialog`
  with `alertdialog` semantics. It owns cancellation focus, Tab containment,
  Escape dismissal and focus restoration. Callers own translations, target/session
  validity and mutations; confirmation closes before the action runs. External
  navigation remains an `asChild` anchor with caller-owned privacy attributes.
- Theme and status colors update immediately without color interpolation, preserving
  contrast during theme changes and synchronous status feedback. Skeleton motion
  respects reduced motion. No component generates runtime style
  tags or consumes the editor's CSP nonce. No external font or animation library.

Updates are reviewed source changes: retrieve the relevant upstream component,
compare it with this version, preserve these adaptations, then run frontend and
embedded-browser gates from `docs/testing.md`. Do not run CLI overwrite across
this directory or let init replace the application stylesheet/theme.
