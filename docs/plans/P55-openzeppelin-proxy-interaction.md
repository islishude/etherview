# P55 — OpenZeppelin Proxy Interaction

Status: `superseded`

## Outcome

Verified contracts expose ABI-driven injected-wallet reads and writes, while
OpenZeppelin 5.x proxy, implementation, management, upgrade, and initialization
identities remain exact, canonical, and reorg-safe.

## References

- [ADR-0003: Spec-first API and canonical public identifiers](../decisions/ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0021: x402 request billing](../decisions/ADR-0021-x402-request-billing.md)
- [ADR-0028: Durable proxy verification and real Hardhat E2E](../decisions/ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [OpenZeppelin Contracts 5.x Proxy API](https://docs.openzeppelin.com/contracts/5.x/api/proxy)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P55-T01 | superseded | P20, P30 | OpenZeppelin 5.x-aware `proxy@2` and dependent `abi@2` observations, histories, and exact verification bindings | proxy/ABI unit, PostgreSQL, reorg, and binding tests |
| P55-T02 | superseded | P55-T01, P40 | Public proxy detail, canonical upgrade and initialization history, and anonymous verified-artifact reads | OpenAPI, handler, cursor, PostgreSQL, auth, and x402 tests |
| P55-T03 | superseded | P55-T02, P50 | ABI-driven contract, as-proxy, management, upgrade-history, and initialization UI | Vitest, embedded browser, responsive, and accessibility tests |
| P55-T04 | superseded | P55-T03, P60 | Real OpenZeppelin 5.6.1 Hardhat fixtures and Preview seven-role acceptance | Hardhat 3, runtime parity, generation, plan, and common gates |

## Acceptance

- [ ] Transparent, UUPS, beacon, clone, and initialization facts are
      block-pinned, reorg-safe, and distinguish exact identities from partial
      or generic proxy evidence.
- [ ] Proxy interaction bindings include the current implementation,
      recognized pattern, and any verified management-contract identity.
- [ ] Native APIs expose canonical snapshot-stable proxy histories and free
      anonymous verified artifacts without adding a browser RPC endpoint.
- [ ] Verified contracts render typed ABI read/write forms without manual API
      keys or calldata and fence every as-proxy or management operation against
      the current binding.
- [ ] Real OpenZeppelin 5.6.1 fixtures pass monolith, split-role, upgrade,
      rebind, browser, and Preview acceptance.

## Current Blockers

None.

## Evidence

- P55 is superseded by the user-requested cross-phase dependency chain so each
  ownership boundary is implemented and verified in its maintained phase.
- P55-T01 is superseded by P20-T13.
- P55-T02 is superseded by P30-T15 and P40-T11.
- P55-T03 is superseded by P50-T14.
- P55-T04 is superseded by the final acceptance work recorded under P50-T14
  after P20-T13, P30-T15, and P40-T11 are complete.
