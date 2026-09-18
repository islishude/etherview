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
| P67-T04 | done | P67-T01–P67-T03 | Role-scoped deployment configuration, maintenance, observability, docs, and monolith/split production acceptance | security/license/schema/runtime/common gates |
| P67-T05 | done | P67-T04 | Keep independent Hardhat and Foundry verification E2E topologies isolated from the ENS-only Mainnet RPC fixture | Compose render regressions and rebuilt Hardhat/Foundry monolith/split E2E |
| P67-T06 | done | P67-T05 | Migrate hashing and wire encoding to ensdomains/go-ens/v4 with ENSIP-15 Unicode regressions | ENS unit/race, Go unit/race/lint, security/license, docs/plan gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

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

None.

## Evidence

- P67-T01: pinned `github.com/adraffy/go-ens-normalize` provides Go ENSIP-15
  normalization, `github.com/wealdtech/go-ens/v3` provides hashing and wire
  encoding, and Viem owns browser ENSIP-15 normalization;
  modern Universal Resolver forward/reverse calls, explicit
  forward verification, endpoint/block pinning, Mainnet identity checks,
  custom deployment checks, and allowlisted CCIP callbacks are implemented.
  `go test ./internal/ens ./internal/ethrpc ./internal/metadata
  ./internal/config -count=1` passed on 2026-08-18.
- P67-T02: migration `0049` replaces the generic name table with immutable
  official/custom resolution generations, observations, failures, and page
  snapshots; dotted search binds an exact observation and the generated
  `/address-names` API returns ordered partial results. Repository-owned
  PostgreSQL 18 migration/status plus the complete integration package passed
  through `make test-integration` on 2026-08-18.
- P67-T03: the SPA normalizes dotted input with Viem, batches semantic address
  identities through one retained snapshot, renders official/custom names with
  exact address disclosure, and falls back silently per item. TypeScript lint
  and all 346 Vitest cases passed; the ENS snapshot/Unicode/390px/WCAG
  Playwright case plus the complete 23-case browser suite passed on 2026-08-18.
- P67-T04: ENS RPC secrets are restricted to `api`/`all`, configuration remains
  explicitly disabled by default, expired generations/snapshots participate in
  catalog maintenance, and bounded source/direction/outcome metrics cover every
  resolution. `make test-integration-race` passed against an owned PostgreSQL
  18 database (`internal/integration` 159.655s); `make test-schema-e2e` passed
  with the final production image; and `make test-runtime-e2e` passed monolith
  (29.96s) plus all six split applications (40.69s), including ENSIP-23's exact
  `resolve(bytes,bytes)`/`reverse(bytes,uint256)` ABI, exact custom
  forward/reverse verification, search publication, restart, reorg, and durable
  parity. `make check` passed on 2026-08-18, including generation, lint,
  ordinary/race, security, license, Buildx, Compose, and Helm gates.
- P67-T05: the shared runtime Compose layer had enabled ENS for verification
  suites whose isolated Anvil is not Ethereum Mainnet, causing API/all startup
  validation to fail before either client ran. Hardhat and Foundry overlays now
  disable ENS on all seven application roles, force the API/all ENS RPC input
  empty, and their Compose checks enforce both invariants. Rebuilt
  `make test-hardhat3-e2e` passed monolith (100.62s) and distributed (100.31s);
  rebuilt `make test-foundry-e2e` passed monolith (45.91s) and distributed
  (48.53s); `make test-hardhat3-provider-compat` also passed; the final
  `make check` passed on 2026-08-18.
- P67-T06: migrated hashing and DNS wire encoding to
  `github.com/ensdomains/go-ens/v4 v4.0.0`; the old ENS module is absent
  from the resolved module graph. Existing normalization, bounded encoding,
  resolver, endpoint/block pinning, and CCIP transport contracts are retained.
  Unicode namehash vectors were independently cross-checked with the SPA's
  Viem implementation; regressions cover emoji, Arabic-Indic digits,
  ENSIP-15 rejection, invalid UTF-8, and byte-length boundaries. Required
  transitive updates include go-base36 v0.2.0: its upstream license SHA-256
  matches the checked-in notice, and the reviewed version/module checksum
  and third-party notice were updated without relaxing the license gate.
  On 2026-09-18, `go test ./internal/ens -count=1`,
  `go test -race ./internal/ens -count=1`, `make test-go`, `make test-race`,
  `make lint-go` (0 issues), `make security-check`, `make license-check`,
  `make docs-check`, `make plan-check`, and `git diff --check` passed locally.
  The first security run encountered npm ECONNRESET; the unchanged security
  target passed on retry. Production-container and live-RPC tests were not
  rerun for this dependency migration.
