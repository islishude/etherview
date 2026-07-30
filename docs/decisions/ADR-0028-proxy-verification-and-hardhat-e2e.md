# ADR-0028: Durable Proxy Verification and Real Hardhat E2E

Status: accepted

## Context

The Hardhat 3 compatibility regression introduced by P70-T21 drives the
official Etherscan provider against the real HTTP compatibility handler, but
uses an in-memory API-key repository and a stateful fake compatibility backend.
It proves the provider wire contract, not deployment, indexing, persistence,
compiler execution, or publication.

The registered `contract.verifyproxycontract` and
`contract.checkproxyverification` actions also report a permanent unavailable
capability even though the block-pinned proxy stage already persists exact
canonical EIP-1167, EIP-1967, and beacon observations.

## Decision

- Keep the provider regression as a fast component-level compatibility test
  and name it accordingly. A separate production-path E2E uses the pinned
  Hardhat CLI, Anvil, PostgreSQL, production images, the official compiler
  catalog, and the digest-pinned networkless compiler runner.
- Run the production-path E2E in both `roles=all` and the complete split
  topology. The production application image sends verified compiler payloads
  to the same digest-pinned remote runner used by deployments. Hardhat runs
  from an independent dependency-locked client image; neither image receives a
  Docker CLI or daemon socket. ADR-0029 defines this superseding execution
  boundary.
- Proxy verification uses the existing durable verification queue. A proxy job
  records the exact proxy code hash, observation block hash, proxy kind,
  implementation address, and implementation code hash. The optional expected
  implementation is normalized to that exact resolved implementation, so
  equivalent submissions share one request digest.
- Submission reads only PostgreSQL writer facts. It accepts complete,
  high-confidence canonical EIP-1167, EIP-1967, or beacon observations and
  requires exact verified-source publications for both proxy and
  implementation. It performs no RPC or compiler call.
- The verify worker rechecks the exact canonical observation and both source
  publications under the leased completion transaction. Success creates an
  immutable result and proxy publication. A reorganization before completion
  fails closed with `target_not_canonical`.
- Historical results and bindings are retained. Public reads expose a proxy
  binding only while the exact observation remains canonical and current.
  Upgrades and reorgs therefore hide stale bindings without rewriting history.
- `getsourcecode` returns `Proxy: "1"` and the checksummed implementation only
  for a current verified proxy binding. `getabi` continues to return the
  queried address's own verified ABI.
- Successful E2E output is phase-bounded. Failure artifacts retain redacted
  Hardhat output, Compose state and logs, and bounded durable/public snapshots.
  API keys never enter commands, URLs recorded as artifacts, or logs.

## Consequences

Proxy verification becomes a real asynchronous compatibility capability rather
than an alias for automatic discovery. The extra immutable schema and exact
source prerequisites make the word “verified” stronger than a chain-only proxy
guess. The full E2E intentionally depends on official compiler availability
and fails instead of silently substituting a fixture compiler.
