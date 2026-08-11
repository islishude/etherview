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
