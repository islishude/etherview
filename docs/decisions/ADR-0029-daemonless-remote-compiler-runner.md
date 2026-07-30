# ADR-0029: Daemonless Remote Compiler Runner

Status: superseded by ADR-0031

## Context

ADR-0024 requires public compiler executions to run behind a digest-pinned,
networkless, resource-bounded isolation boundary. Its first implementation
made the verification worker invoke Docker or Podman for every compilation.
That couples an ordinary application process to a container daemon and makes
Kubernetes deployment require a privileged nested daemon, a copied runtime
client, image loading, and a preflight service.

The compiler catalog, artifact checksum, platform identity, dual compilation,
lease fencing, and immutable result provenance do not depend on that
orchestration mechanism.

## Decision

- Public verification uses a remote compiler-runner service. The verification
  worker owns catalog refresh, artifact download, cache, origin validation, and
  SHA-256 validation, then sends the exact compiler and one or two bounded
  Standard JSON inputs over the internal runner protocol.
- The runner exposes only versioned health/info and compile endpoints. It
  rechecks the compiler digest, executable platform, and version before
  sequential execution. It never downloads artifacts or receives database,
  RPC, API-key, or application configuration.
- Each runner replica accepts one compilation request at a time. The process
  uses a sanitized environment and a separate process group. Timeout or
  cancellation kills the complete group; uncertain cleanup terminates the
  runner and never produces a publishable result.
- The runner image is non-root and digest-pinned, with a read-only root,
  executable tmpfs, dropped capabilities, no-new-privileges, bounded
  resources, and no egress. Kubernetes may additionally select a sandboxed
  RuntimeClass for runner Pods.
- `runner_digest` remains the configured OCI image digest and immutable job
  provenance. Deployment rendering binds the runner workload and verification
  worker configuration to the same exact image. Runtime readiness verifies the
  protocol and platform; it is not presented as remote image attestation.
- `security.compiler_sandbox` accepts `disabled`, `process`, and `remote`.
  Process mode remains private-only. The old `container` value fails with
  migration guidance and never falls back.
- Monolith and split roles use the same remote endpoint. Neither application
  shape receives a Docker/Podman client, daemon socket, container-runtime
  credentials, or Kubernetes API permission.
- The compiler-runner is an internal service, not a public API. The Helm chart
  requires NetworkPolicy when public verification is enabled, admits runner
  traffic only from `all` or `verify`, and denies runner egress.

This decision supersedes ADR-0024 and ADR-0028 only where they require the
application to create a compiler container. Their compiler provenance,
hostile-input, canonical publication, and real-E2E requirements remain in
force.

## Consequences

Docker Compose and Kubernetes share one execution protocol without nested
container orchestration. Resource ownership moves from application
configuration to the runner workload, and a runner outage fails verification
readiness or compilation closed. Compiler binaries still cross an internal
boundary for every request, so the pair-compilation protocol transfers the
artifact once for both deterministic executions.
