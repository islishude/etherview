# P61 — EIP-7702 Delegated Accounts and Exact Constructor Decoding

Status: `done`

## Outcome

EIP-7702 type-4 transactions expose exact authorization outcomes and canonical
delegation history, every traced call distinguishes its storage/call context
from the code that actually executes, and verified top-level and nested
creations expose exact constructor decoding. Delegated EOAs may use the
delegate's verified ABI while every call still targets the authority and every
write is fenced by a fresh canonical binding.

Raw transaction authorization tuples, initcode, trace input/output, and log
emitter identity remain intact. Missing transaction-time state evidence is
reported as unavailable; block-end or latest state is never used as an
inference. Authorization signing/revocation UI, arbitrary delegatecall proxy
recognition, opcode/raw trace persistence, EIP-7851, and EIP-8202 are outside
this plan.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0022: go-ethereum type and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0023: Exact transaction state differences](../decisions/ADR-0023-exact-transaction-state-differences.md)
- [ADR-0032: Evidence-based proxy detection](../decisions/ADR-0032-evidence-based-proxy-detection.md)
- [ADR-0033: Trace-bound log attribution and call decoding](../decisions/ADR-0033-trace-bound-log-attribution-and-call-decoding.md)
- [ADR-0034: EIP-7702 execution identity and constructor decoding](../decisions/ADR-0034-eip7702-execution-identity-and-constructor-decoding.md)
- [EIP-7702](https://eips.ethereum.org/EIPS/eip-7702)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P61-T01 | done | P20, P30, P40, P50, P59, P60 | `state_diff@2`, `trace@3`, and `abi@3`; exact EIP-7702 and constructor persistence/projection; generated APIs; delegated-account Web interaction; migration, rollout, ADR, and regression closure | codec, state diff, trace, PostgreSQL, generation, browser, integration, runtime, Hardhat, and common gates |
| P61-T02 | done | P61-T01 | Repair delegated-account Web layout, add delegated-address browser regressions, and close Preview production verification | focused Web tests, production browser, Preview, and common gates |
| P61-T03 | done | P61-T02 | Fix Preview delegation-history SQL and allow delegated reads across canonical-tip advancement while retaining exact write fences | focused Go and Web tests; Preview API/browser verification |
| P61-T04 | done | P61-T03 | Rebuild the delegated-account page as a contract-style hash-tab workbench with lazy history and delegated deep-link regressions | focused Web tests, production browser, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Type-4 tuples are recovered and replayed in order with stable applied,
      skipped, or unavailable outcomes and exact transaction-time evidence.
- [x] Canonical delegation and execution-code facts survive reorg, replay,
      partition lifecycle, and monolith/split execution without latest-state
      inference.
- [x] Root and nested call-like frames expose context and actual execution code;
      trace-bound logs use the same exact code identity while preserving the
      emitter.
- [x] Exact verified CREATE/CREATE2 matches decode constructor parameters and
      retain raw initcode; malformed or inexact matches are explicit.
- [x] Public authorization/delegation APIs, generated clients, and bilingual
      delegated-account interaction use writer-authoritative freshness fences.
- [x] Bounded rollback and rollout rebuild `state_diff@2`, then `trace@3`,
      `proxy@2`, and `abi@3` without migration-time historical enqueue.
- [x] Delegated-account binding, interaction, and history panels reuse the
      responsive Web layout and pagination contracts without changing API or
      EIP-7702 data semantics.

## Current Blockers

None.

## Evidence

- P61-T01 completes migration `0040`, `state_diff@2`, `trace@3`, and `abi@3`,
  exact authorization/delegation and execution-code persistence, exact
  constructor decoding, generated APIs, writer-fenced delegated-account Web
  interaction, rollout documentation, and ADR-0034.
- Focused Go tests for ABI, Trace, StateDiff, Store, and verification pass;
  `make generate-check` and `make plan-check` pass.
- `make test-integration`: pass against a runner-owned fresh PostgreSQL 18
  database with migration `0040`; `internal/integration` completes in
  117.195s and all integration packages pass.
- `make test-e2e`: pass, 11/11 Chromium tests for generated API boundaries,
  bilingual responsive disclosures, raw trace/log data, exact execution
  provenance, wallet isolation, and keyboard accessibility.
- `make test-runtime-e2e`: pass in monolith (36.50s) and complete six-role
  distributed (46.73s) production topologies, including exact-hash reorg,
  dependency outage/recovery, restart, load, and durable/public parity.
- `make test-hardhat3-e2e`: pass in monolith (224.19s) and distributed
  (226.72s) topologies with real compilation, address verification, UUPS,
  Transparent, Beacon, and Clone binding, upgrade invalidation, and rebinding.
- `make check`: pass, including generated-contract consistency, Go/Web lint,
  253 Web tests, ordinary and race tests, vulnerability/secret/license gates,
  Buildx checks, Compose contracts, and Helm lint/render.
- P61-T02 repairs the delegated-account panels by reusing the shared detail
  card/grid, disclosure, pagination, and button styles; adds Vitest and
  production-browser coverage for binding, delegate ABI interaction, history
  paging, bilingual narrow layouts, A11y, overflow, and runtime errors.
- `npm --prefix web test -- --run src/pages/CorePages.test.tsx`: pass, 20/20;
  `make test-e2e`: pass, 12/12 Chromium tests; `make check`: pass.
- `make recreate-preview` rebuilt `etherview:local` while retaining the
  PostgreSQL/Geth volumes. Live Preview verification at
  `0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266` reports the delegated binding,
  delegate `0x5FbDB2315678afecb367f032d93F642f64180aa3`, 24px panel padding,
  grid detail layout, zero horizontal overflow, and no browser warnings/errors.
- P61-T03 fixes the reserved PostgreSQL `authorization` alias and supplies
  numeric zero cursor boundaries for first-page delegation history queries.
  Delegated reads now accept a newer canonical tip when authority, chain,
  delegate, and code hash remain unchanged; writes retain exact block-number
  and block-hash fencing. Focused Go/Web tests, `go test ./...`, Web 255/255,
  `make lint`, `make plan-check`, and host-authorized `make test-e2e` (12/12)
  pass. Rebuilt Preview returns HTTP 200 for the reported `delegations?limit=20`
  request with two canonical history items.
- P61-T04 rebuilds delegated EOA interaction as a contract-style `#code`,
  `#read-contract`, `#write-contract`, and `#history` workbench. Binding and
  verified artifact details stay on Code, read/write forms use the matching
  ABI mode and existing writer fence, and delegation history loads only when
  active. Address routing now accepts `tab=delegation`, separates delegated
  hashes from contract hashes, and preserves the canonical `#code` entry.
  Vitest passes 28 files/255 tests; bundled production browser E2E passes
  12/12 with delegated tab, paging, bilingual narrow layout, and accessibility
  coverage; host-authorized `GOCACHE=/tmp/etherview-go-cache make check` passes.
