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
