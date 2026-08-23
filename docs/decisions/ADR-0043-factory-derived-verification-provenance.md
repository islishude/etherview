# ADR-0043: Factory-derived Verification Provenance

Status: accepted

## Context

The verifier compiles and validates every bounded non-abstract contract in a
submitted Standard JSON unit, but successful address publication currently
retains only the selected contract. Canonical normalized traces already retain
CREATE and CREATE2 init code and deployment identity, while canonical contract
code observations retain the resulting runtime identity. Recompiling or
retracing would add a second trust and availability boundary, and global
runtime similarity would not prove source provenance.

## Decision

- Only a successful canonical address-verification job may authenticate a
  compilation unit. Its immutable request digest, compiler and executor
  provenance, normalized Standard JSON, and complete bounded candidate set are
  persisted transactionally with verification completion. Public callers
  cannot create or mutate authenticated units.
- A derived attempt starts from a persisted canonical, non-reverted CREATE or
  CREATE2 trace whose creator resolves to an exact address-bound verified code
  epoch at that block. It consumes the trace init code and the created
  address's canonical code observation at the same block identity; the normal
  path performs no RPC or compilation.
- Candidate creation and runtime matching uses the same transformation-aware
  matcher as submitted verification, including constructor arguments, library
  links, immutables, and compiler auxdata. Publication requires exactly one
  fully qualified candidate to pass both matches. No match, ambiguity, missing
  runtime, and stale evidence are durable non-publication outcomes.
- Every attempt is identified by chain, immutable block hash, transaction hash,
  trace path, and compilation unit. Its publication transaction rechecks the
  canonical block, trace, parent code epoch, and child runtime observation.
  Detach retains audit evidence as stale and prevents it from resolving as a
  current verified address; reattach or retry remains idempotent.
- Derived success uses an internal job and the same verified-contract,
  selector, and proxy-replay publication helper as submitted address
  verification. Its origin and creation provenance are additive facts; it
  does not overwrite an existing exact verification and never inherits a
  user's Sourcify submission consent.
- Historical scans are enqueued after compilation-unit publication. Forward
  work is an immutable event keyed by the exact successful `trace@3` or
  `proxy@2` durable publication job and generation. Trace generations request
  fork-aware rescan floors for CREATE/CREATE2 work; proxy generations wake
  pending-runtime attempts only after exact code publication. A newly derived
  child may enqueue its own scan, making transitive propagation durable rather
  than recursive.
- Successful scan pages reset the consecutive-failure budget. A forward event
  racing a running scan merges a durable earliest rescan block that the old
  lease must consume before it can advance again; replacement forks and
  same-hash new generations therefore cannot be hidden behind an old cursor.
- Candidate hydration, transformation matching, uniqueness classification, and
  result construction occur once outside a database transaction under a
  heartbeat-renewed scan lease. The final short transaction locks and rechecks
  only the exact canonical evidence and prepared digests before publication.
- Creation provenance and verification origin are independent additive facts.
  A directly submitted exact-address artifact remains `submitted` when a later
  factory match records its canonical creation. Parent identity and child lists
  bind the exact compilation and creator code epoch; code-hash reuse never
  inherits target-address creation provenance.
- PostgreSQL remains authoritative. Configuration bounds candidates, bytecode,
  traces per scan, attempts, and workers; feature publication is disabled by
  default for staged rollout. Metrics and the public API expose only stable,
  bounded labels and provenance fields.

## Consequences

Factory-derived verification is an auditable extension of the existing
verification trust chain rather than a second verifier. It can backfill and
follow future deployments without archive/debug RPC, while ambiguous or
non-canonical evidence fails closed. Persisted compilation input and candidate
bytecode increase storage use, and the API role gains a separately bounded
derived-work loop, but neither ingestion correctness nor readiness depends on
that loop.

Schema migration `0057` changes the derived queue protocol. Environments with
derived workers enabled replace every `all`/`api` replica together; the
migration preserves existing scan and attempt state but does not silently
requeue failed or pending historical work. Recovery remains an audited explicit
backfill.
