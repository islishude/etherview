# ADR-0014: Durable Verification Identity and Publication

Status: accepted

## Context

Contract verification is asynchronous and may cross an API process, a worker
process, a lease expiry, and a chain reorganization. Binding a job only to its
chain, address, code hash, and block hash is insufficient: two requests for the
same deployed code can select different compiler inputs, contracts, bytecode,
or Sourcify consent. A split-role worker can also have a different compiler
configuration from the API process that accepted the request.

The compiler result is useful only while the exact block-scoped code
observation is canonical. A worker that finishes after a reorganization must
not publish an artifact or leave a running job that is reclaimed forever.
Published artifacts also need an immutable source record so a later submission
cannot silently overwrite the provenance of an earlier result.

## Decision

- A submission stores its exact validated request bytes and a SHA-256 digest
  with a domain separator and the server-derived hard-isolation requirement.
  The digest therefore covers every compiler input and option, the complete
  target identity, Sourcify consent, and whether the public boundary required
  a hard sandbox. Only an active or successful job with the same complete
  digest is idempotent. A failed or cancelled request may be submitted again as
  a new job.
- The API service, not the caller payload, sets the durable
  `requires_hard_isolation` bit. Every worker checks that bit before compiling.
  It binds the leased job to the actual compiler kind, SHA-256 digest, and
  isolation property before execution; a reclaim may reuse only the identical
  provenance. This protects split-role deployments from configuration drift.
- Verification leases have a durable attempt count and maximum. Claiming
  increments the count. An expired job that consumed its budget becomes
  `failed` with the stable `attempts_exhausted` code instead of being reclaimed
  indefinitely.
- Completion locks and revalidates the exact lease, then resolves the request's
  chain, address, runtime code hash, and block hash through an exact canonical
  `contract_code_observations` row. If that identity is no longer canonical,
  the same transaction records the terminal `target_not_canonical` result and
  publishes no artifact.
- Every successful compiler/matcher completion inserts one immutable
  `verification_results` row linked by database constraints to the job,
  request digest, target, and compiler provenance. Verified artifacts are a
  deterministic projection of those immutable rows. Exact matches outrank
  metadata-only matches; equal-quality results use the request digest as the
  stable tie-break. Readers apply the same exact-first, newest-applicable-range
  ordering.
- Mismatch results are durable successful results but contain no ABI, sources,
  or settings. Failed jobs contain only an allowlisted stable error code. Lease,
  result, error, size, JSON-shape, attempt, identity, and provenance coherence
  are enforced by PostgreSQL constraints as well as repository validation.

## Consequences

Idempotency no longer conflates distinct source or compiler requests for one
deployed contract. Public jobs cannot be consumed by a process compiler even
when API and worker configuration differ. Crashed and reorged attempts reach a
bounded, auditable terminal state, and published artifacts remain traceable to
their exact request and compiler digest.

This decision defines the durable boundary only. The allowlisted compiler
manifest and cache, sandbox resource implementation, full compiler/matcher
fixture matrix, Sourcify interoperability, and public HTTP compatibility remain
the responsibilities of P30-T02 through P30-T04 and P40.
