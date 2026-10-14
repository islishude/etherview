# P76 — ERC-4337 UserOperation Browsing

Status: `done`

## Outcome

Configured EntryPoint v0.6, v0.7, v0.8, and v0.9 deployments produce one
canonical, reorg-safe UserOperation index from exact stored bundle facts.
Users can browse snapshot-stable global, transaction, and address activity,
open a complete UserOperation detail, and search by userOpHash in the bilingual
embedded SPA.

Pending Bundler mempools, UserOperation submission or simulation, wallet-
specific call/signature interpretation, automatic EntryPoint discovery, and
nested EntryPoint calls are outside this plan.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0007](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0022](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0034](../decisions/ADR-0034-eip7702-execution-identity-and-constructor-decoding.md)
- [ADR-0045](../decisions/ADR-0045-erc4337-useroperation-index.md)
- [ERC-4337](https://eips.ethereum.org/EIPS/eip-4337)
- [ERC-7769](https://eips.ethereum.org/EIPS/eip-7769)
- [Official EntryPoint releases](https://github.com/eth-infinitism/account-abstraction/releases)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P76-T01 | done | P10, P20, P30, P40, P50, P68, P75 | Governance, bounded EntryPoint configuration, `userop@1`, PostgreSQL persistence and coverage, generated native APIs, search, bilingual Web browsing, rollout, and runtime parity | decoder, configuration, PostgreSQL, reorg, generation, Web, browser, real-Anvil, monolith/split, race, and common gates |

## Acceptance

- [x] Explicit, configuration-digest-bound EntryPoint ranges cover v0.6-v0.9 without address or version autodiscovery.
- [x] `userop@1` accepts only successful direct `handleOps` and `handleAggregatedOps` calls whose calldata and EntryPoint events agree exactly.
- [x] A malformed or contradictory bundle publishes no partial UserOperation rows.
- [x] Canonical UserOperations and their lifecycle, failure, and participant facts survive replay, detach, reattach, and orphan retention.
- [x] Global and address pagination use one continuous published coverage snapshot; absent detail is never reported authoritatively across a coverage gap.
- [x] Public APIs preserve raw operation bytes, decimal-string quantities, stable typed errors, and generated Go/TypeScript contracts.
- [x] Search, global list, detail, transaction Bundle activity, and address role activity are complete in both locales and responsive layouts.
- [x] Disabled or mismatched configuration fails explicitly and never exposes output from another configuration digest.
- [x] Existing databases use explicit bounded `reindex --stage userop`; migrations never enqueue unbounded history.
- [x] Monolith and split roles produce the same PostgreSQL and public results from real-Anvil EntryPoint wire fixtures.
- [x] Runtime-only UserOperation fixtures opt in explicitly; unrelated Hardhat and Foundry verification overlays remain feature-off and start successfully.
- [x] The full PostgreSQL integration package has an explicit bounded timeout above observed cold-CI duration without skipping or weakening tests.
- [x] The testing guide owns the shared-overlay default-off, explicit opt-in, feature-derived readiness, cross-suite command sequence, timeout, and failure-signature rules.
- [x] A fresh GitHub Actions run on the remediation revision passes PostgreSQL integration and both architectures of the Hardhat and Foundry matrices.

## Current Blockers

None. P70/P73 retain independent release blockers.

## Evidence

Retained completed milestones: P76-T01.

EntryPoint v0.6-v0.9 decoder/configuration tests, PostgreSQL normal/race,
generation, `make check` and all 25 then-current Chromium cases passed.
Fresh-schema and production runtime acceptance exercised a real Anvil packed
v0.9 transaction through monolith and split workers, APIs and durable parity.
Feature-off verification fixtures retain separate Hardhat/Foundry coverage.
Pending Bundler RPC, submission and simulation remain out of scope.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P76-erc4337-user-operations.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
