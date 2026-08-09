# P59 — Trace-bound ABI Decoding

Status: `done`

## Outcome

Transaction logs can use exact call-frame execution identity when the RPC
trace proves it, and transaction Trace frames expose bounded, provenance-aware
function input, successful return, and direct-revert decoding without hiding
the raw bytes or weakening operation on trace providers without embedded logs.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0007: Block-scoped derived canonicality journals](../decisions/ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0015: Disposable runtime accelerators](../decisions/ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0033: Trace-bound log attribution and call decoding](../decisions/ADR-0033-trace-bound-log-attribution-and-call-decoding.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P59-T01 | done | P20, P40, P50, P56 | `trace@2` log attribution, complete call ABI projection, generated API, compact Web disclosure, migration, rollout, and regression closure | codec, trace, PostgreSQL, generation, browser, runtime, Hardhat, and common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Exact callTracer logs bind each receipt log to one normalized frame and execution address; unsupported log capture preserves the call tree and uses conservative address fallback.
- [x] CALL, STATICCALL, DELEGATECALL, and CALLCODE expose input, successful output, and direct-revert ABI decoding with exact provenance and bounded hostile-input handling.
- [x] Log emitter identity remains distinct from execution identity, and trace evidence never becomes proxy-detection or management authorization.
- [x] Reorg, replay, partition, cache, monolith, and split-role semantics remain generation- and block-hash-fenced.
- [x] Generated API clients and bilingual responsive Web views retain all raw log and trace bytes.

## Current Blockers

None.

## Evidence

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
