# P63 — Geas Contract Verification

Status: `done`

## Outcome

Verify multi-file Geas v0.3.3 address submissions, including the relative
`#include` and nested `assemble()` forms used by ethereum/sys-asm, through the
existing asynchronous PostgreSQL verification workflow and bilingual Web form.
Runtime bytecode is mandatory and exact; optional creation bytecode is exact.
Geas never inherits Solidity metadata transformations, dynamic compiler
downloads, standalone verifier routes, or Etherscan submission compatibility.

## References

- [ADR-0024: Verifier v2 workflow](../decisions/ADR-0024-verifier-v2-workflow.md)
- [ADR-0031: API-owned solc-js executor](../decisions/ADR-0031-api-owned-solc-js-executor.md)
- [ADR-0037: Persistent solc-js artifact cache](../decisions/ADR-0037-persistent-solcjs-artifact-cache.md)
- [ADR-0039: Pinned Geas verification executor](../decisions/ADR-0039-pinned-geas-verification-executor.md)
- [Geas](https://github.com/fjl/geas)
- [ethereum/sys-asm](https://github.com/ethereum/sys-asm)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P63-T01 | done | P30, P40, P50, P60 | Accepted ADR, generated public contract, migration `0044`, and typed Geas request/provenance model | OpenAPI generation, migration and request-model tests, plan check |
| P63-T02 | done | P63-T01 | Pinned v0.3.3 helper subprocess, virtual source filesystem, deterministic exact compilation, runtime identity, and compiler routing | compiler unit/security/process tests, license and image checks |
| P63-T03 | done | P63-T02 | Language-aware queue claiming, lease-bound Geas provenance, exact runtime/optional creation matching, canonical publication, and per-family availability | worker, repository, PostgreSQL integration and race tests |
| P63-T04 | done | P63-T03 | Native address API, fixed compiler listing, Etherscan reads, bilingual Web submission and plain-text source workspace | generated clients, Go/Web/API/browser tests |
| P63-T05 | done | P63-T04 | Pinned sys-asm fixture and monolith/six-role production verification acceptance, documentation, and common-gate closure | Foundry production E2E, schema/runtime E2E, common gates |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] `language=geas` accepts only compiler `0.3.3`, inline multi-file sources,
      a required runtime entrypoint, and an optional creation entrypoint.
- [x] Source paths are normalized relative slash paths; absolute, traversing,
      duplicate, oversized, missing, or externally resolved files fail before
      a job can compile.
- [x] Each entrypoint compiles twice with stack checking in fresh bounded
      subprocesses; bytecode and the transitive set of read sources agree.
- [x] Runtime and optional creation input match byte-for-byte, publish `full`
      with no transformations, and recheck the exact canonical target before
      atomic publication.
- [x] Compiler and executor identity bind once under the lease without a solc
      catalog generation; retries cannot switch module or helper identity.
- [x] Solc catalog loss does not starve runnable Geas, proxy, or Sourcify jobs,
      and availability is observable separately for `solcjs` and `geas`.
- [x] Native API, Etherscan reads, generated clients, Web form, source display,
      and empty ABI semantics are consistent and bilingual.
- [x] A pinned sys-asm EIP-7002 fixture verifies in both production topologies
      using its official runtime and constructor bytecode.

## Current Blockers

None.

## Evidence

- P63-T01: `make generate-check`, focused `go test` for request, HTTP, store,
  config, app, Etherscan, and observability packages, and PostgreSQL 18
  migration/application through focused `make test-integration` pass.
- P63-T02: helper identity, process, virtual-filesystem, determinism, pinned
  sys-asm unit tests, `make license-check`, `make docker-build`, and
  `make docker-image-check` pass; the arm64 image reports the hardened Geas v0.3.3
  self-test and read-only executable boundary.
- P63-T03: `make test-integration` and focused `make test-integration-race`
  pass against owned PostgreSQL 18 volumes, including compiler-family FIFO,
  immutable provenance, malformed-result rejection, and exact publication.
- P63-T04: `make generate-check`, `make test` (325 Web tests), `make lint`,
  `make security-check`, and `make test-e2e` (18 Chromium tests) pass for the
  native API, generated clients, Etherscan reads, bilingual form, and inert
  source presentation.
- P63-T05: `make deployment-check`, `make test-schema-e2e`, and
  `make test-runtime-e2e-prebuilt` pass, and the final unified `make check`
  gate is green. `make test-foundry-e2e-prebuilt` against the final hardened
  image
  deploys and exactly verifies the pinned sys-asm EIP-7002 constructor/runtime
  in monolith (46.97s) and six-role distributed (48.90s) production topologies.
