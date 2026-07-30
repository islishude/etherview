# ADR-0030: API-Integrated Verifier and Self-Contained Compiler Runner

Status: superseded by ADR-0031

## Context

ADR-0029 removed the application-controlled container daemon, but retained a
dedicated verification application role. That role downloads every compiler
and sends the binary to a networkless runner. The resulting deployment still
has three verification processes, gives an application worker a compiler
cache and broad catalog egress, and transfers a large executable on every
compile request.

The public API already owns verification submission and status reads, while
PostgreSQL leases make worker execution safe across API replicas. Solc remains
hostile executable input and must not run in the public API process.

## Decision

- The `api` role owns verification submission, status reads, catalog
  provenance persistence, job leases, and result publication. There is no
  production `verify` role. `roles=all` and the six-role split topology build
  the same verification components exactly once.
- Public Solidity, Yul, and Vyper execution remains in a separately deployed,
  digest-pinned compiler-runner. The runner owns official catalog discovery,
  origin and redirect validation, compiler download, SHA-256 verification,
  disposable caching, platform validation, and bounded compiler execution.
  It receives no database, RPC, API-key, session, billing, or application
  credential.
- Runner protocol v4 exposes info, resolve, and compile operations. Resolve
  returns one exact compiler identity from one validated catalog generation.
  Compile accepts that exact identity and one or two Standard JSON inputs; it
  never accepts an executable payload from the API.
- A compile submission fixes language, normalized compiler version, request
  content, and the configured runner image digest. Its catalog generation,
  platform, and compiler digest are bound once by the leased API worker after
  resolve. PostgreSQL accepts only the transition from entirely unbound
  compiler provenance to one complete identity and rejects every later
  mutation.
- API readiness is independent of runner availability. A coordinator performs
  bounded background protocol/platform checks. Unleased compiler jobs remain
  queued while the runner is unavailable; proxy and Sourcify jobs may continue.
  A transient outage after a compiler job is leased is retried under lease with
  bounded backoff. No unavailable or incompatible runner can produce a
  publishable result.
- `verification.worker_count` independently bounds verification workers per API
  replica and defaults to one. PostgreSQL leases remain the cross-replica
  concurrency authority.
- Explicit `--roles=verify` configuration fails with stable migration guidance.
  It is neither retained as a hidden deployment shape nor mapped to a public
  API listener.
- The runner uses an ephemeral cache and has DNS plus HTTPS egress for approved
  catalog and artifact origins. It is isolated from PostgreSQL, RPC, and
  application networks. RuntimeClass, non-root identity, read-only root,
  dropped capabilities, no-new-privileges, seccomp, resource bounds, and
  whole-process-group termination remain runner-only controls.
- The configured digest-pinned runner OCI reference remains the source of
  `runner_digest`; protocol metadata is not remote image attestation.
- Private development may still select the process compiler. Public
  verification continues to require the remote runner.

This decision supersedes ADR-0029 where it assigns catalog/cache ownership to
the application, transmits compiler executables, defines runner deny-all
egress, makes runner readiness process-fatal, or retains a split `verify` role.
ADR-0024, ADR-0028, and ADR-0029 remain authoritative for compiler provenance,
hostile-input bounds, canonical publication, proxy verification, and
daemonless isolation.

## Consequences

Deployments have two verification services: an API process containing the
durable worker and a credential-free compiler-runner. API availability no
longer follows compiler availability, and compiler executables neither enter
the API image/cache nor cross the internal protocol. Unsupported compiler
versions become asynchronous verification failures because provenance is
resolved after GUID creation.
