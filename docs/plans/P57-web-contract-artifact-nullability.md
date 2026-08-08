# P57 — Web Contract Artifact Nullability

Status: `done`

## Outcome

Preview contract pages render legacy verification artifacts whose match
transformation list is JSON `null`, while current API responses restore the
OpenAPI-required empty-array representation.

## References

- [P56: Contract Artifact Reuse and ABI UX](P56-contract-artifact-reuse-and-abi-ux.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P57-T01 | done | P56-T01 | Null-safe contract artifact rendering and Preview regression coverage | focused Vitest, production build, Preview/browser check, plan check |

## Acceptance

- [x] `/contract/:address` renders when legacy verification match transformations are JSON `null`.
- [x] Existing exact and same-code artifact presentations remain unchanged.
- [x] Focused Web tests, production build, live Preview check, and governance checks pass.

## Current Blockers

None.

## Evidence

- P57-T01: `go test ./internal/verify ./internal/httpapi` passes with stored-match and
  public JSON regressions proving legacy `null` transformations normalize to
  the OpenAPI-required empty array.
- The focused contract artifact, proxy, and contract-page Vitest suite passes
  20 tests, and `npm --prefix web run build` passes.
- `make recreate-preview` rebuilt all six application roles while preserving
  PostgreSQL and Reth. The reported contract API returns
  `creation_match.transformations: []`; a fresh embedded-browser visit renders
  `MyToken` and its creation match without the error boundary or console errors.
