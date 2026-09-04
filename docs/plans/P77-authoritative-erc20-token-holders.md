# P77 — Authoritative ERC-20 Token Holders

Status: `done`

## Outcome

ERC-20 contracts with genesis-through-tip canonical Transfer coverage and a
complete exact-state reconciliation expose current holder counts and
address-ordered holder pages through native, Etherscan-compatible, and
bilingual Web surfaces. Missing coverage, inconsistent supply, unsupported
token behavior, RPC loss, replay, or reorganization fails closed.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0003](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0006](../decisions/ADR-0006-durable-canonical-coverage-and-live-priority.md)
- [ADR-0007](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0008](../decisions/ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0012](../decisions/ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0046](../decisions/ADR-0046-authoritative-erc20-holder-reconciliation.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P77-T01 | done | P20, P30, P68, P75 | `holder@1`, exact-state reconciliation, immutable PostgreSQL facts, coverage, replay, and reorg handling | stage, RPC, migration, PostgreSQL, reorg, lease, race, and generation tests |
| P77-T02 | done | P77-T01, P40, P50, P74 | Native holder list/count, Etherscan list/count billing, and bilingual Token-page browsing | OpenAPI, handler, billing, Web, browser, and compatibility tests |
| P77-T03 | done | P77-T02 | Runtime parity, bounded reindex, observability, operations, capacity workload, and v1 release-gate evidence | schema, runtime, monolith/split, deployment, documentation, license, security, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] `holder@1` is always scheduled and uses one exact block-hash state RPC endpoint per bounded reconciliation operation.
- [x] Candidate discovery requires canonical high/verified ERC-20 Transfer facts continuously covered from block zero.
- [x] No holder row is public until every candidate balance and total supply agree at one still-canonical snapshot.
- [x] Forward Transfer participants are reconciled exactly; code/proxy generation changes and detected drift withdraw availability until a full baseline succeeds.
- [x] Reorg, replay, restart, lease expiry, and conflicting exact observations cannot expose partial or stale holder state.
- [x] Native list/count and Etherscan list/count return only current-tip complete PostgreSQL snapshots; empty success is proven, never inferred.
- [x] Native cursors bind chain, token, snapshot, holder epoch, and address boundary; address ordering is stable and balance ranking is absent.
- [x] Both compatibility actions are billable and retain existing pagination, response-size, authentication, and prepaid-credit boundaries.
- [x] The embedded Token page is bilingual, accessible, generated-client-only, and responsive at 390px.
- [x] Existing databases require explicit bounded `reindex --stage holder`; migrations and startup never enqueue unbounded history.
- [x] Monolith and split roles publish identical durable and public results, and the P70 reference workload includes holder reads plus reconciliation.
- [x] Every applicable focused, integration, race, generation, browser, schema, runtime, deployment, documentation, and common gate passes.

## Current Blockers

None.

## Evidence

- P77-T01 adds migration `0064`, always-scheduled `holder@1`, Token/Proxy claim
  dependencies, exact EIP-1898 supply and balance reconciliation in batches of
  at most 200, high/verified candidate selection, full-supply equality,
  first/gap/generation/audit baselines, participant-only forward increments,
  rotating audits, code/proxy-generation rechecks, lease-fenced atomic
  publication, partition management, canonical journals, replay coherence,
  immutable facts, and bounded `reindex --stage holder`. Focused stage/store
  tests, `make test`, `make test-race`, `make test-integration`, and `make
  test-integration-race` pass. The PostgreSQL Holder regression exercises the
  complete durable stage, RPC, native/compatibility read, mutation rejection,
  incremental burn, immutable historical reconstruction, and reorg-withdrawal
  path; its final focused integration-race run also passes.
- P77-T02 adds the generated native list/count contracts, snapshot/epoch-bound
  address cursor, current-tip fail-closed reads, dedicated unavailable and
  not-applicable errors, Etherscan list/count wire shapes, fixed ascending
  order, billable operation identities and Helm/x402 price catalogs. The Token
  page uses only the generated same-origin client. `make generate-check`,
  `make lint`, `make security-check`, and the final 39-file/368-test Web suite
  pass; `make test-e2e` passes all 26 Chromium cases including the bilingual
  390px Holder accessibility case.
- P77-T03 runtime and deployment evidence passes: `make test-schema-e2e`,
  `make test-runtime-e2e-prebuilt`, `make deployment-check`, and the real
  Hardhat monolith/split production gate. Foundry monolith first passed while
  split timed out pending; an unchanged retry failed monolith at the external
  compiler artifact boundary, and the next unchanged retry passed both
  topologies in 114.41 seconds. Retained diagnostics show `holder@1` succeeded
  quickly in the failed runs and the failures were verification queue/compiler
  availability, not Holder. `make docs-check`, `make plan-check`, and `git diff
  --check` pass. After the scanner compatibility repair, the unchanged `make
  license-check` passes under Go 1.27.1, including Go attribution checks and
  both production npm license allowlists; the expected assembly-inspection
  warnings do not fail the gate. P77-T03 and P77 are complete.
