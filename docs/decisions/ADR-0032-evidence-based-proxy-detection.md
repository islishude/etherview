# ADR-0032: Evidence-based proxy detection

- Status: Accepted
- Date: 2026-08-08

## Context

The `proxy@2` stage has one OpenZeppelin-oriented recognizer whose first
accepted branch becomes the durable public result. That contract cannot
represent simultaneous matches, distinguish an unavailable historical RPC
from a negative result, or add Safe proxy recognition without increasing the
cost of every indexed contract.

Safe proxy identity also has two independent parts: the runtime proxy shell
and the current slot-0 singleton. A canonical shell may legitimately point to
a custom singleton, and a contract that merely exposes `masterCopy()` is not a
canonical Safe proxy.

## Decision

Proxy recognition V2 is an additive, feature-gated observation owned by the
existing `proxy@2` stage.

- Every detector receives a shared context bound to one chain, address, block
  number, and exact block hash. The context owns and memoizes all RPC access.
- Detectors return structured outcomes with evidence, warnings, confidence,
  and one of `confirmed`, `candidate`, `inconsistent`, `not-detected`, or
  `unknown`. RPC transport or historical-state failure produces `unknown`.
- A resolver retains all outcomes and explicit conflicts. No detector wins by
  registration order or short-circuits the remaining detectors.
- The existing OpenZeppelin 5.6.1 result remains the only source for legacy
  proxy observations, verified bindings, and write authorization until a
  separate accepted decision replaces that contract.
- Safe bulk detection first compares the already-fetched runtime code hash
  against a generated, source-pinned manifest. A miss performs no Safe-specific
  RPC. A hit validates slot 0 and singleton code at the same exact block.
- Canonical shell identity and official singleton identity are independent.
  Deep-mode interface similarity without a known runtime is at most a
  `safe-compatible-proxy` candidate.
- V2 resolutions are stored as generation-fenced JSON evidence. Shadow
  collection and public API exposure use separate flags; both default off and
  public exposure requires shadow collection.
- Safe detection cannot authorize implementation interaction. EIP-7702 account
  detection and generic delegatecall inference remain out of scope.

## Consequences

New proxy families add one detector and resolver tests rather than modifying
the legacy branch tree. Exact-block cache keys prevent state from leaking
between heights. The manifest and public schema become generated contracts.
Operators can collect and compare results before exposure, disable the detector
without reverting a deployment, and replay fixed blocks idempotently.

The additive evidence row is intentionally not a replacement for existing
`proxy_observations`; downstream security-sensitive behavior remains stable
during shadow rollout.
