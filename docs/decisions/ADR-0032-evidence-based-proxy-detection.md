# ADR-0032: Evidence-based proxy detection

- Status: Accepted
- Date: 2026-08-08
- Updated: 2026-10-19

## Context

Proxy identity needs structured evidence, simultaneous matches and explicit
historical-state failures. Safe shell and singleton identities are independent;
Diamond targets are selector-scoped. A detection priority or confidence cannot
establish a verified binding or grant management authority.

The initial additive rollout has been replaced by one detection framework.
Keeping separate execution and public-exposure switches would preserve a
permanent alternate path and an unnecessarily versioned public contract.

## Decision

- The existing `proxy@2` stage always runs the shared detection framework.
  Standard and exact Solady legacy CWIA recognition are mandatory. Safe and
  Diamond retain independent, default-off family switches.
- Every detector receives one chain/address/exact-block context. Standard code,
  storage and Beacon calls use the same memoized RPC context as optional
  detectors. Standalone Beacon observations share the block memo. UUPS probes
  retain their separate implementation-only authority and seed exact code facts.
- Detectors return structured outcomes with evidence, warnings, confidence,
  and `confirmed`, `candidate`, `inconsistent`, `not-detected`, or `unknown`.
  The resolver retains all outcomes and conflicts independent of registration
  order. Its primary result is presentation, never authorization.
- Standard and CWIA detectors also return their authoritative observations as
  an explicit internal result. Only these facts can feed the existing singular
  proxy observations and exact artifact resolution. Multiple authoritative
  observations are rejected. Safe and Diamond evidence never synthesizes an
  ordinary implementation binding.
- Required code/standard/CWIA RPC failures retain the stage's retryable,
  unavailable and permanent error classifications. Optional detector failures
  remain explicit unknown outcomes; neither path turns failures into negatives.
- Existing exact bindings, immutable admin/beacon authority, historical ABI
  dependencies, canonicality and generation/lease publication fences remain
  unchanged. Recognition does not create source verification or write authority.
- Detection summaries are generation-fenced JSON evidence with candidate kind
  `proxy_resolution`. They are published as `proxy_detection` in the public
  API whenever a current published summary exists. No collection/public gate,
  shadow comparison, alternate old execution path or old wire alias remains.
- Safe bulk detection first checks the already-fetched runtime fingerprint.
  A miss performs no Safe-specific RPC. A hit validates slot 0 and singleton
  code at the exact block. Official/custom singleton identity remains separate;
  deep-only interface similarity is at most a compatible candidate.
- Safe evidence grants no implementation interaction. Diamond calls retain
  selector/code identity fencing and target the Diamond, not an arbitrary facet.
- Keep internal detector and stage versions for identity. Removing a public
  naming suffix does not remove versioned publication or cache identity.
- This development cutover updates the fresh database baseline directly. Old
  configuration keys, public fields and schema discriminators have no aliases,
  startup conversion, backfill or legacy-schema compatibility. Older development
  databases must be recreated under the repository's fresh-database policy.

## Consequences

There is one production detection path and one public evidence contract. The
separate authoritative relationship and detection summary serve different
purposes; neither table layout nor authorization is collapsed into resolver
priority. Standard/CWIA authority remains covered by pre-change observation
fixtures and exact-binding regressions.

Every processed candidate now stores a bounded detection summary. Deployment
acceptance compares RPC, allocations, database writes and latency, checks public
Safe/Diamond boundaries and reviews fixed-block positive/negative evidence.
Use the [deployment review](../reviews/proxy-detection-deployment-review-template.md)
before serving a new revision to users. Validation occurs in an isolated
acceptance deployment because there is no public-hiding flag.

Disabling an optional family stops new work but does not erase published
historical evidence. A bad core revision is withdrawn or replaced as a whole;
there is no old-detector fallback. The schema and binary must be deployed as a
matching fresh-baseline pair. Bounded historical reindex remains explicit.
