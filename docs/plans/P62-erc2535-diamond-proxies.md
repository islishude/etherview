# P62 — ERC-2535 Diamond Proxies

Status: `done`

## Outcome

Recognize ERC-2535 Diamonds as selector-routed multi-target proxies without
projecting one facet as a singular implementation. Persist exact-block Loupe
snapshots and ordered DiamondCut history, resolve historical function ABIs by
selector and transaction position, and expose bounded bilingual read and
interaction surfaces while retaining explicit partial, inconsistent, and
unknown states.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0032: Evidence-based proxy detection](../decisions/ADR-0032-evidence-based-proxy-detection.md)
- [ADR-0033: Trace-bound log attribution and call decoding](../decisions/ADR-0033-trace-bound-log-attribution-and-call-decoding.md)
- [ADR-0038: Selector-scoped ERC-2535 Diamond identity](../decisions/ADR-0038-selector-scoped-erc2535-diamond-identity.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P62-T01 | done | P20, P30, P40, P50, P58-T03, P59 | Replace the singular implementation assumption with additive selector-scoped Diamond targets in persistence, query, OpenAPI, generated clients, and compatibility projections | migration, query, OpenAPI generation, model unit and integration tests |
| P62-T02 | done | P62-T01 | Fixed-block bounded ERC-2535 detector with Loupe fallback, cross-validation, immutable functions, facet code checks, candidate gates, and independent status/completeness/validation | detector unit/fuzz tests, hostile-return limits, exact-block RPC fixtures |
| P62-T03 | done | P62-T02 | Raw DiamondCut indexing, ordered selector intervals, reorg retention, replay, snapshot reconciliation, and standard DiamondCut presence | PostgreSQL integration/race, add/replace/remove, reorg and same-transaction ordering tests |
| P62-T04 | done | P62-T03 | Historical selector-bound ABI decoding, selector-filtered Diamond interaction, collision handling, facet/current/history API, bilingual UI, and monolith/split acceptance | generated clients, Vitest, Playwright, runtime E2E, common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] No Diamond facet is published through the singular legacy
      `implementation` field; compatibility exposes only the distinct external
      `implementation_addresses` set.
- [x] Every selector is exactly four bytes, belongs to at most one facet at an
      exact chain/block identity, and may resolve to the Diamond itself as an
      immutable function.
- [x] Detector RPC uses one endpoint and exact block hash, distinguishes revert,
      malformed data, limits, transport failure, and missing historical state,
      and never silently truncates a complete result.
- [x] `facets()`, `facetAddresses()`, `facetFunctionSelectors(address)`, and
      `facetAddress(bytes4)` evidence is cross-checked fully or by deterministic
      bounded sampling with explicit completeness and validation.
- [x] DiamondCut Add, Replace, and Remove facts retain transaction/log order,
      orphan history, `_init` exclusion, and canonical selector intervals.
- [x] Historical transaction decoding uses the facet active at the transaction
      position; exact trace execution identity wins for same-transaction cuts.
- [x] Facet ABI functions are selector-filtered and called at the Diamond;
      selector collisions and ambiguous events/errors remain explicit.
- [x] ERC-1967-over-Diamond composition retains both layers and is not treated
      as a detector conflict solely because both families match.
- [x] Resource limits, malformed ABI offsets, zero/no-code facets, duplicate
      selectors, reorgs, and monolith/split parity have regression coverage.

## Current Blockers

None.

## Evidence

- P62-T01 adds migration `0043`, generic selector-scoped proxy targets, and an
  ERC-2535 public document with exact facets/routes, distinct external
  `implementation_addresses`, completeness, validation, standard-cut state,
  and explicit truncation. No Diamond path selects a first facet or populates
  the legacy singular implementation.
- P62-T02 implements exact-block, one-endpoint bounded Loupe enumeration with
  `facets()` fallback, required-interface and absent-selector validation,
  deterministic sampling, immutable functions, external code identity,
  candidate gates, and distinct revert/limit/malformed/transport outcomes.
  Focused detector ordinary/race tests and both fuzz targets pass.
- P62-T03 persists raw ordered cuts and selector changes, replays strict Add,
  Replace, and Remove transitions, retains orphan facts, publishes canonical
  intervals, excludes `_init` from facets, and reconciles complete snapshots.
  `make test-integration` passes against runner-owned PostgreSQL 18 in
  121.369s; `make test-integration-race` passes in 138.587s, with both projects
  and volumes removed afterward.
- P62-T04 resolves historical function ABI by selector and transaction
  position, filters facet functions, retains collision provenance, and exposes
  bounded generated APIs plus bilingual facets, cut history, and Diamond-target
  interaction. `make test-e2e` passes 18/18 Chromium tests.
- `make test-hardhat3-e2e` passes a real multi-facet, immutable-Loupe,
  no-standard-cut Diamond in production images: monolith 227.50s and complete
  six-role distributed 219.91s (447.94s total). Both topologies publish three
  targets and eight selectors, retain the constructor cut, and publish no
  singular proxy observation.
- Final `make check` passes generation, plan checks, Go/Web lint, 323 Web tests,
  ordinary and race suites, vulnerability/secret/license scans, Buildx and
  Compose validation, and Helm lint/render. `git diff --check` passes.
