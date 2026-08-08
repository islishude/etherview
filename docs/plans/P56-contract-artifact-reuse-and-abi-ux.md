# P56 — Contract Artifact Reuse and ABI UX

Status: `done`

## Outcome

Contracts with the same canonical runtime code hash can reuse a clearly
attributed verified artifact, standard proxies can call their implementation
ABI through the proxy without weakening management authorization, and
transaction logs expose bounded ABI decoding while retaining their raw data.

## References

- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0009: Block-bound ABI provenance](../decisions/ADR-0009-block-bound-abi-provenance.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0028: Durable proxy verification and real Hardhat E2E](../decisions/ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P56-T01 | done | P30, P40 | Writer-authoritative exact/same-code artifact resolution across native and Etherscan reads | repository, API, compatibility, migration, and generation tests |
| P56-T02 | done | P56-T01, P50 | Standard proxy implementation interaction with fresh identity fencing and verified-only management | proxy API, wallet, frontend, browser, and Hardhat tests |
| P56-T03 | done | P56-T01, P20, P40, P50 | Persisted-first, bounded read-time ABI decoding for transaction logs | codec, catalog, API, frontend, reorg, and PostgreSQL tests |
| P56-T04 | done | P56-T02, P56-T03 | Architecture, acceptance, common-gate, integration, browser, and production proxy closure | ADR, plan, generation, integration, E2E, Hardhat, and common gates |

## Acceptance

- [x] Same-code artifacts retain their verified source identity and never mark
      the requested address as independently verified.
- [x] Native and Etherscan reads agree on exact versus similar matches while
      verification submission remains address-bound.
- [x] Unambiguous Clone, EIP-1967, and Beacon implementations can be read and
      written through the proxy; every operation refreshes the complete proxy
      identity and unverified management stays unavailable.
- [x] Transaction logs prefer durable decoding, safely improve after late
      verification, use the implementation valid at the transaction block,
      and always retain raw topics and data.
- [x] Bilingual responsive Web flows, generated contracts, PostgreSQL
      integration, embedded-browser, Hardhat, and applicable common gates pass.

## Current Blockers

None.

## Evidence

- P56-T01 adds one repeatable-read writer resolver for current canonical target
  identity and deterministic exact/same-code publication selection. Focused Go
  tests for verification, native HTTP, Etherscan, application wiring, and
  migrations pass; the tagged PostgreSQL same-code fixture compiles and is
  ready for the owned integration gate. TypeScript generation and lint pass,
  and focused artifact/proxy/contract-page tests pass after adapting the
  staged P50-T16 source workspace to explicit target/source identities.
- P56-T02 publishes a separate high-confidence standard implementation
  interaction for Clone, EIP-1967, and Beacon details. The Web loads the
  implementation artifact but fixes calls and ordinary transactions to the
  proxy, refreshes mechanism and all code identities before use, and keeps
  `bindingId` on exact management-only targets. Focused adapter, wallet-fence,
  proxy, artifact, and contract-page tests pass.
- P56-T03 adds structured log decoding to the spec and generated clients,
  reuses the bounded ABI registry for persisted-first repeatable-read fallback,
  resolves same-code and historical proxy implementation artifacts without RPC
  or writes, supports anonymous events and indexed hashes, and renders named
  arguments before an accessible raw disclosure. Focused codec, catalog, HTTP,
  and Web tests pass; the integration fixture covers persisted and historical
  proxy fallback projections.
- P56-T04 closes the API, PostgreSQL, browser, and production topology
  acceptance loop. `make generate-check`, `make plan-check`,
  `make test-integration`, `make test-e2e`, `make test-hardhat3-e2e`, and
  `make check` pass. The Hardhat suite covers both monolith and six-role split
  deployments; its second same-code BeaconProxy uses an explicit forced
  submission after the compatibility preflight reports `SimilarMatch`, proving
  that artifact reads can reuse a source while address verification remains an
  independent operation.
