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
| P61-T05 | done | P61-T04 | Restore transaction Authorizations tab query-parameter routing and add a click regression | focused CorePages frontend tests, lint, build, generation, and plan checks |
| P61-T06 | done | P61-T05 | Expose exact transaction-time calldata decoding and render EIP-7702 delegation, redelegation, and clearing semantics in the transaction Overview | catalog, API, generation, Web, integration, browser, runtime, and common gates |
| P61-T07 | done | P61-T06 | Preserve EIP-7702 log execution-address attribution when the delegate code hash is unavailable from prestateTracer, then resolve ABI only through the exact historical code identity | trace/ABI/catalog regressions, live Preview replay, and common gates |
| P61-T08 | done | P61-T07 | Decode ABI `receive` and `fallback` entry points for trace and transaction calldata instead of reporting empty or selectorless calls as unknown functions | ABI/Catalog/API/Web regressions, live Preview verification, and common gates |
| P61-T09 | done | P61-T08 | Report calls with exact empty execution code, including ordinary native transfers to EOAs, as ABI decoding not applicable instead of an unknown function selector | Catalog/API/Web regressions, live Preview verification, and common gates |
| P61-T10 | done | P61-T09 | Keep canonical delegation history discoverable from an address after its current EIP-7702 delegation is cleared | state/query, generated API, Web, browser, integration, and common gates |

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
- [x] Transaction calldata binds canonical inclusion and raw input to the
      published transaction-time execution identity, including redelegation,
      clearing, ordinary delegated calls, and fail-closed unavailable evidence.
- [x] Trace-bound EIP-7702 logs retain an exact authorization-applied delegate
      address when prestate evidence omits delegate code, while ABI decoding
      still requires an independently resolved historical code identity.
- [x] Empty and selectorless calls use exact historical ABI `receive` and
      `fallback` entries rather than reporting an unknown function selector.
- [x] Trace frames with exact empty execution code report ABI decoding as not
      applicable, including ordinary native transfers to EOAs.
- [x] Clearing the current delegation preserves a writer-authoritative,
      canonical-history address entry that opens History directly without
      eagerly loading current binding or artifact data.

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
- P61-T05 adds `authorizations` to the transaction route's validated tab
  values. Its CorePages regression proves that clicking Authorizations selects
  the tab, requests the generated authorization subresource, and renders an
  applied EIP-7702 tuple. The focused CorePages suite passes 23/23; frontend
  lint and production build pass; `make generate-check` passes.
- P61-T06 adds the billable generated
  `GET /api/v1/transactions/{hash}/calldata` contract. Its repeatable-read
  Catalog projection binds canonical inclusion and raw input to published
  `state_diff@2` execution-code rows, reuses exact published `abi@3` calldata
  facts, and performs only block/address/code-hash-bounded ABI fallback reads.
  Empty execution returns `not_applicable`; absent or unavailable exact
  execution evidence returns `unavailable` without consulting current
  delegation, height, `latest`, an old delegate, or a full trace.
- P61-T06 Catalog/API regressions cover type-4 final redelegation, clearing,
  later type-2 delegated calls, direct execution, published and read-time ABI
  decoding, decoded/ambiguous/unknown/malformed/unavailable states, corrupt
  identity handling, and missing `state_diff@2`. CorePages passes 30/30 and
  the full Web suite passes 29 files/291 tests, including exact provenance,
  clearing, raw calldata, no current verification/proxy/delegation requests,
  and one-retry identity fencing.
- `make generate-check` and `make plan-check` pass. `make test-integration`
  passes against a runner-owned fresh PostgreSQL 18 database; the integration
  package completes in 131.524s and the project/volume are removed.
- `make test-e2e` passes 13/13 Chromium tests. `make test-runtime-e2e` passes
  monolith (36.80s) and the complete six-role distributed topology (45.93s),
  including exact calldata unavailable semantics after a reverted call and
  durable/public parity. The final writable-cache `make check` passes Go/Web
  generation, vet, lint, ordinary and race tests, security/license checks,
  Buildx/Compose validation, and Helm lint/render.
- P61-T07 retains an exact trace path and EIP-7702 delegate execution address
  when `prestateTracer` omits the delegate account and therefore cannot supply
  its code hash. `abi@3` and the read-time Catalog path still require a
  canonical block-scoped code observation and range-valid ABI for that exact
  delegate; ordinary direct frames without a code identity remain on the
  conservative address fallback.
- Focused `go test ./internal/enrich ./internal/catalog ./internal/httpapi
  ./internal/apiops`, writable-cache `make generate-check`, `make plan-check`,
  and `git diff --check` pass. The final writable-cache `make check` passes all
  Go ordinary/race and Web 291/291 tests plus generation, vet, lint,
  vulnerability, secret, license, Buildx, Compose, and Helm gates.
  `make test-integration` passes against a runner-owned PostgreSQL 18 database;
  `internal/integration` completes in 134.086s and the project and volume are
  removed.
- Preview transaction
  `0xe077f2f1688b96206ed609cb589508001134abf02187c7ca8d3789f38d7d722c`
  was replayed from an exact archive reconstruction of the retained chain
  after the live path-state node could no longer serve block 33. Its public log
  now reports `exact_trace`, execution address
  `0x610178dA211FEF7D417bC0e6FeD39F05609AD788`, verified historical code hash
  `0x0fc2f604e56003f124b958dda95b91ed9e54c8a6ef454639deab54506e0f76c2`,
  and decoded `Received(address,uint256)` arguments. The recovery containers
  were removed and the regular Preview `sync` and `trace` roles are healthy.
- P61-T08 recognizes ABI `receive` and `fallback` entries as selectorless call
  identities. Empty calldata selects `receive` before `fallback`; incomplete or
  unmatched selectors select only a declared `fallback`. Trace ingestion now
  persists empty-call observations, and Catalog transaction/trace projection
  keeps the exact historical address, code hash, ABI source, and confidence.
- Focused `go test ./internal/enrich ./internal/catalog ./internal/httpapi
  ./internal/apiops` and the focused `CorePages` Web suite (30/30) pass.
  Writable-cache `make check` passes all ordinary/race, Web 291/291,
  generation, vet, lint, security/license, Buildx/Compose, and Helm gates.
  `make test-integration` passes against runner-owned PostgreSQL 18;
  `internal/integration` completes in 131.605s and the disposable project and
  volume are removed.
- After rebuilding the already-running Preview, transaction
  `0xe077f2f1688b96206ed609cb589508001134abf02187c7ca8d3789f38d7d722c`
  returns trace decoding status `decoded`, signature `receive()`, empty inputs
  and outputs, verified confidence, delegate execution address
  `0x610178dA211FEF7D417bC0e6FeD39F05609AD788`, and historical code hash
  `0x0fc2f604e56003f124b958dda95b91ed9e54c8a6ef454639deab54506e0f76c2`.
- P61-T09 maps exact `empty` call execution to ABI decoding
  `not_applicable`, so ordinary EOA transfers and other calls with no
  executable code never report an unknown selector or query ABI material.
  The generated Trace contract includes the new status, Web renders
  “No executable code” / “无可执行代码”, and required empty candidate arrays
  serialize as `[]` rather than `null`.
- Focused Catalog/HTTP/API/ABI Go tests pass, and the focused CorePages suite
  passes 31/31. Writable-cache `make generate-check`, `make plan-check`, and
  the final `make check` pass; the full Web suite is 292/292 and all ordinary,
  race, static-analysis, security/license, Buildx/Compose, and Helm gates pass.
- Rebuilt Preview transaction
  `0x1d2099298b948843f9149fd7c410a879656e0cfc78cdc6cd3678ac2b9a2ce847`
  is a successful type-2 value transfer with `input=0x`. Its public Trace root
  reports `execution.resolution=empty`, `decoding.status=not_applicable`,
  `output_status=not_applicable`, and `candidates=[]`; every Preview service is
  healthy.
- P61-T10 adds required `AddressSummary.has_delegation_history` from a
  writer-authoritative repeatable-read query at the exact canonical state
  reference. Applied clearing remains discoverable; skipped, orphan, later,
  detached, and unavailable evidence cannot silently produce a visible false
  result. Cleared addresses open `#history` directly and keep current binding
  and verified-artifact reads lazy; current delegated and ordinary EOA routing
  remain unchanged.
- Focused query/state/app tests and CorePages 32/32 pass. `make test` passes all
  ordinary Go packages and Web 293/293; `make test-race`, `make generate-check`,
  `make lint`, and `make plan-check` pass. `make test-integration` passes against
  a runner-owned PostgreSQL 18 database (`internal/integration` 135.631s) and
  removes the disposable project and volume. Host-authorized `make test-e2e`
  passes 14/14 Chromium tests, including cleared-history lazy loading and narrow
  accessibility. The final host-authorized writable-cache `make check` passes
  generation, vet/lint, ordinary/race, security/license, Buildx/Compose, and
  Helm gates.
