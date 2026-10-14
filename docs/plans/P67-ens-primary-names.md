# P67 — ENS Primary Names

Status: `done`

## Outcome

Add explicitly enabled, snapshot-stable ENS name recognition. Dotted search
resolves names through the official Ethereum L1 Universal Resolver and may
fall back to a configured custom Universal Resolver on the explored chain only
after a definitive official no-record result. Public address displays use only
primary names whose reverse record resolves forward to the same address.

Historical transaction-time names, ENS registration/management, avatars, text
records, and implicit ENS support in write forms are out of scope.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0011: Snapshot search, statistics, and bounded adapters](../decisions/ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0041: Snapshot-stable ENS primary names](../decisions/ADR-0041-snapshot-stable-ens-primary-names.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P67-T01 | done | P20-T06 | First-class ENS configuration, Go ENSIP-15 normalization, block-pinned Universal Resolver and bounded CCIP-Read core | resolver/config unit and security tests; `make generate-check` |
| P67-T02 | done | P67-T01 | Fresh-schema persistence, resolution generations, official-to-custom fallback, search integration, and generated batch address-name API | PostgreSQL integration/race and API contract tests |
| P67-T03 | done | P67-T02 | Shared bilingual primary-name UI across semantic address surfaces with snapshot reuse and exact address disclosure | web unit, accessibility, responsive, and embedded browser tests |
| P67-T04 | done | P67-T01, P67-T02, P67-T03 | Role-scoped deployment configuration, maintenance, observability, docs, and monolith/split production acceptance | security/license/schema/runtime/common gates |

## Acceptance

- [x] Browser forward input is normalized with Viem, Go canonicalization uses
  `github.com/adraffy/go-ens-normalize`, `github.com/ensdomains/go-ens/v4` owns
  hashing/wire encoding, and every displayed primary name is
  reverse-plus-forward verified.
- [x] Official resolution uses Ethereum Mainnet finality and the configured chain coin type; custom resolution uses one exact local canonical block.
- [x] Custom resolution runs only after an official no-record or unsupported-coin-type result, never after transport, CCIP, normalization, or validation failure.
- [x] Resolution generations, search cursors, and address-name snapshots remain stable across pagination and invalidate correctly on custom-chain reorg.
- [x] Core indexing, ordinary API responses, and readiness do not depend on ENS availability.
- [x] Every semantic address display retains an inspectable/copyable exact address and visually distinguishes custom ENS names.
- [x] Monolith and split-role semantics match.
- [x] Hardhat and Foundry verification E2E explicitly disable ENS and do not
  require or impersonate the ENS Mainnet RPC fixture.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P67-T01, P67-T02, P67-T03, P67-T04.

ENS resolver/configuration/security, Unicode and exact-block tests passed;
PostgreSQL normal/race covered immutable generations and snapshot reuse.
Generated API, bilingual browser/accessibility, fresh-schema and production
monolith/split gates passed. Later hashing/wire-library maintenance retained
ENSIP-15 and boundary regressions. Live upstream availability is not implied
by fixture or historical acceptance.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P67-ens-primary-names.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
