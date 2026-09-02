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
| P76-T01 | done | P10, P20, P40, P50, P60, P61, P68, P75 | Governance, bounded EntryPoint configuration, `userop@1`, PostgreSQL persistence and coverage, generated native APIs, search, bilingual Web browsing, rollout, and runtime parity | decoder, configuration, PostgreSQL, reorg, generation, Web, browser, real-Anvil, monolith/split, race, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

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

## Current Blockers

None.

## Evidence

- P76-T01 delivers the v0.6 unpacked and v0.7-v0.9 packed decoders for direct
  and aggregated bundles, canonical ABI re-encoding, EIP-7702 init markers,
  v0.9 paymaster signatures, versioned lifecycle events, malformed envelopes,
  contradictory identities, duplicate hashes, reverted outer transactions,
  and exact outcome log indices. Configuration tests cover explicit enablement,
  unknown versions, zero/invalid addresses, overlapping ranges, bounded JSON
  environment input, order-independent digests, and the 16-entry limit.
- Migration `0063` adds partitioned operation/event/participant facts,
  configuration-digest-scoped covered blocks and maximal continuous ranges,
  global canonical `userOpHash` enforcement, detach cleanup, and exact
  publication witnesses. The managed PostgreSQL regression drives the real
  outbox/worker/API path, all four reads, search and status, then verifies
  detach, retained orphan children, replayed reattach, and restored canonical
  children. `make test-integration` and `make test-integration-race` pass; their
  integration-package long tails complete in 188.873 and 202.216 seconds.
- `api/openapi.yaml` owns the generated Go and TypeScript contracts for global,
  detail, transaction, and address resources. Search and list cursors bind the
  normalized registry and continuous UserOperation snapshot. `make
  generate-check`, `make lint`, and the final `make check` pass; the Web suite
  passes 39 files/368 tests and the initial asset graph remains within budget
  at 940,459 raw and 291,997 gzip bytes.
- `make test-e2e` passes all 25 Chromium cases in 1.4 minutes, including the
  feature-gated global/list/detail/transaction/address flow, localized roles,
  raw request bytes, decoded failure reason, keyboard navigation, 390px layout,
  and WCAG checks in both themes and locales.
- `make test-schema-e2e` passes a fresh production-image migration and status
  through `0063`. `make deployment-check` passes Dockerfile, Compose, Helm
  schema/lint/render, and topology checks. The final `make test-runtime-e2e`
  passes the transaction-deployed packed-v0.9 Anvil fixture in monolith and
  six-role split mode in 84.34 seconds, including exact operation/event/API and
  normalized durable/public parity; the unchanged prepaid x402 monolith/split
  suite also passes in 35.21 seconds.
- The enablement runbook records the official singleton addresses as address
  examples but deliberately requires operator-supplied chain deployment
  ranges. Historical activation remains an explicit bounded `reindex --stage
  userop`; pending Bundler RPC, submission, and simulation remain outside P76.
