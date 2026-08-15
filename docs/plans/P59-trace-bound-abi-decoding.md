# P59 — Trace-bound ABI Decoding

Status: `done`

## Outcome

Transaction logs can use exact call-frame execution identity when the RPC
trace proves it, and transaction Trace frames expose bounded, provenance-aware
function input, successful return, and direct-revert decoding without hiding
the raw bytes or weakening operation on trace providers without embedded logs.
Canonical failed transactions also expose exact root-failure decoding and a
separate bounded Overview presentation.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0033: Trace-bound log attribution and call decoding](../decisions/ADR-0033-trace-bound-log-attribution-and-call-decoding.md)
- [ADR-0034: EIP-7702 execution identity and exact constructor decoding](../decisions/ADR-0034-eip7702-execution-identity-and-constructor-decoding.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P59-T01 | done | P20, P40, P50, P56 | `trace@2` log attribution, complete call ABI projection, generated API, compact Web disclosure, migration, rollout, and regression closure | codec, trace, PostgreSQL, generation, browser, runtime, Hardhat, and common gates |
| P59-T02 | done | P59-T01 | Decode creation-block logs after later same-address verification by exact runtime-code reuse without extending address verification provenance | codec, PostgreSQL, Preview transaction, plan, and common gates |
| P59-T03 | done | P30-T17, P40-T19, P59-T01 | Decode direct Trace calls from exact verified address-range selectors when completed state-diff evidence omits an unchanged target | focused catalog, PostgreSQL, Preview transaction, browser, and common gates |
| P59-T04 | done | P59-T01, P61-T06 | Expose canonical transaction failure decoding and render custom plus Solidity builtin errors in the transaction Overview | codec, catalog, generated API, Web, PostgreSQL, browser, runtime, and common gates |
| P59-T05 | done | P59-T04 | Render Solidity builtin `Error(string)` and `Panic(uint256)` as concise error text without the structured ABI argument table | focused Web, browser, plan, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Exact callTracer logs bind each receipt log to one normalized frame and execution address; unsupported log capture preserves the call tree and uses conservative address fallback.
- [x] CALL, STATICCALL, DELEGATECALL, and CALLCODE expose input, successful output, and direct-revert ABI decoding with exact provenance and bounded hostile-input handling.
- [x] Log emitter identity remains distinct from execution identity, and trace evidence never becomes proxy-detection or management authorization.
- [x] Reorg, replay, partition, cache, monolith, and split-role semantics remain generation- and block-hash-fenced.
- [x] Generated API clients and bilingual responsive Web views retain all raw log and trace bytes.
- [x] Creation-block logs reuse later same-address verification only through
      exact runtime-code equality with `code_hash`/`high` provenance.
- [x] Completed state-diff evidence may use only exact-address, range-valid
      verified selectors for direct `CALL`/`STATICCALL` frames whose execution
      identity is unavailable; indirect execution and incomplete evidence stay
      fail-closed.
- [x] Canonical failed transactions expose one root-failure resource whose
      custom-error ABI provenance matches exact transaction-time execution and
      whose builtin `Error(string)` and `Panic(uint256)` decoding does not
      require a contract ABI.
- [x] Transaction Overview renders decoded failure arguments as bounded
      `Name / Type / Data` leaf rows with jq-style paths and no leading dot,
      while custom failures retain raw revert data and calldata presentation
      remains unchanged.
- [x] Solidity builtin `Error(string)` and `Panic(uint256)` render only their
      decoded error text; their ABI signature, argument table, and raw revert
      data disclosure are omitted.

## Current Blockers

None.

## Evidence

- P59-T05 renders builtin `Error(string)` and `Panic(uint256)` as one concise,
  copyable error value. Their signature, argument names/values, structured
  table, and raw revert-data disclosure are absent; custom errors retain the
  `Name / Type / Data` table and raw revert data. Focused tests report 57/57,
  the complete Web suite reports 31 files and 342 tests, Chromium reports
  22/22, and the final `make check`, `make plan-check`, and `git diff --check`
  pass. One preceding full-gate attempt reached the security phase and was
  interrupted only by a transient npm registry `ECONNRESET`; the unchanged
  full gate passed on retry.
- P59-T04 adds the generated, billable
  `GET /transactions/{hash}/failure` contract. One repeatable-read snapshot
  fences the canonical inclusion, completed `trace@3` result, failed receipt,
  root trace frame, and exact execution ABI. Verified/code-hash/proxy/facet
  ABI sources decode custom errors; signature-database guesses stay unknown.
  Solidity `Error(string)` and `Panic(uint256)` decode without a contract ABI,
  with the reason produced by geth `abi.UnpackRevert`.
- Transaction Overview displays the failure immediately after Status in an
  independent bilingual `Name / Type / Data` layout. Recursive values flatten
  to bounded leaf-only jq-style paths without a leading dot, custom-error raw
  revert bytes remain available, and the existing calldata disclosure is
  unchanged.
- Focused catalog/HTTP and 56 Web tests pass. `make test-integration` passes all
  six packages against a fresh PostgreSQL 18 database (`internal/integration`
  141.875s). `make test-e2e` passes 21/21 Chromium cases, including Chinese
  390px and automated accessibility coverage. `make test-runtime-e2e` passes
  monolith (32.44s) and complete six-role distributed (42.54s) topologies.
  `make generate-check`, `make web-test` (31 files, 341 tests), `make check`,
  `make plan-check`, and `git diff --check` pass. During the first full gate,
  the OpenAPI billing-route maximum was corrected from 33 to 34; a separate
  existing solc-js relocated-runtime race self-test passed on exact rerun and
  the complete subsequent `make check` also passed.
- P59-T03 shares the exact verified-address selector loader with transaction
  calldata and adds a bounded Trace-only fallback after canonical
  `state_diff@2` completion. Exact calldata round trips, overlapping code
  identities, candidate overflow, `DELEGATECALL`/`CALLCODE`, and known
  unresolved EIP-7702 execution addresses remain fail-closed. Focused catalog
  tests cover the reported failed call plus builtin `Panic(uint256)` decoding
  plus declared successful `STATICCALL` output decoding and the
  indirect/incomplete-evidence boundaries. The PostgreSQL integration suite
  covers the exact `output_status=unavailable` contract for a successful call
  whose verified ABI omits outputs while its execution row and root-frame
  identity are unavailable.
- `go test ./internal/catalog`, `make test-integration`, `make test-e2e` (19/19),
  `make check`, `make plan-check`, and `git diff --check`: pass. `make
  recreate-preview` preserves the existing chain/database and rebuilds all six
  application roles successfully. Preview transaction
  `0xfd92879a1c383080a2f2cb1ff1477899a624842d45c53dedf1c19a4de2d949a5`
  now returns and renders `triggerDivisionByZero()` with exact-address verified
  provenance while retaining `execution.resolution=unavailable`; its direct
  revert renders builtin `Panic(uint256)` with code `18`. The rebuilt page has
  no browser console warnings or errors.

- P59-T01 completes the trace-bound attribution, ABI projection, public API,
  Web disclosure, rollout, and regression scope described above.
- `go test ./...`, `make web-lint`, and `make web-test`: pass; the Web suite
  reports 28 files and 252 tests.
- `make generate-check` and `make plan-check`: pass after generated OpenAPI,
  SQL, and governance updates.
- `make test-integration`: pass against a runner-owned fresh PostgreSQL 18
  database with migration `0039`; `internal/integration` completes in
  125.255s and all six integration packages pass.
- `make test-e2e`: pass, 11/11 Chromium tests including bilingual narrow-view
  trace/log disclosures, keyboard operation, raw bytes, and exact execution
  provenance.
- `make test-runtime-e2e`: pass in monolith (36.15s) and complete six-role
  distributed (45.86s) production topologies.
- `make test-hardhat3-e2e`: pass in monolith (225.74s) and distributed
  (227.92s) topologies with real compilation, verification, proxy upgrade,
  invalidation, and rebinding.
- `make check`: pass, including generation, Go/Web lint, ordinary and race
  tests, vulnerability/secret/license checks, Buildx, Compose, and Helm gates.
- The in-app browser also confirmed English and Chinese responsive views,
  successful output and direct-revert decoding, exact log execution identity,
  retained raw bytes, and an empty console warning/error set.
- P59-T02 closes the late-verification creation-block gap in both `abi@2`
  materialization and persisted-first read projection. Exact runtime equality
  can reuse a later same-address artifact only as `code_hash`/`high`; it never
  extends the artifact's address-bound `verified` range. `go test ./...`,
  `make test-integration`, `make check`, and `make plan-check` pass. The rebuilt
  split-role Preview API decodes all four logs of transaction
  `0xd9f0ab26aaca5eb1d3ab989ac40a263cc914ef2a8250ac0f34ad26185492938b`
  as `Upgraded`, `Initialized`, `OwnershipTransferred`, and `AdminChanged`
  while retaining exact trace attribution and source provenance.
