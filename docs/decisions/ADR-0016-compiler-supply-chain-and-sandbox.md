# ADR-0016: Compiler Supply Chain and Sandbox

Status: accepted

## Context

Verification executes compiler inputs supplied through a hostile public
boundary. A version label, executable path, or container tag is not an
artifact identity. A checksum taken before execution is also insufficient when
the cache can be replaced through a symbolic link or when a timed-out runtime
leaves the compiler container alive. Compiler downloads must not become an
SSRF path, and a configured container backend must prove its daemon and every
digest-pinned image are locally available before the verify role becomes
ready.

## Decision

- Process-mode compiler manifests contain only Solidity or Vyper versions that
  satisfy the verification version grammar. Each entry has one absolute HTTPS
  URL, a canonical non-zero lowercase SHA-256 digest, and a bounded byte limit;
  an omitted limit uses the fixed 200 MiB default. Downloads bypass environment
  proxies, reject redirects, and refuse DNS answers containing loopback,
  private, link-local, documentation,
  carrier, benchmark, transition, or other special-purpose addresses. HTTP and
  private-network access exist only as explicit test hooks and are never wired
  by production assembly.
- The process cache root is absolute, private from group/world writes, and not
  a symbolic link. A reusable compiler is a regular non-symlink file with mode
  `0500` and the exact manifest digest. Downloads stream through the byte and
  checksum limits into a same-directory temporary file, sync and chmod it,
  then atomically replace the cache entry. A mismatch or unsafe existing entry
  is never executed.
- Container manifests use an immutable image reference with exactly one
  `@sha256:` suffix and a canonical non-zero digest. Startup resolves only the
  allowlisted `docker` or `podman` executable, checks daemon reachability, and
  inspects every configured digest without pulling it. Compilation uses that
  resolved executable with `--pull=never`, no network, a read-only root,
  dropped capabilities, no-new-privileges, a non-root identity, bounded CPU,
  memory, PIDs and file descriptors, and a small no-exec temporary filesystem.
- Each compiler container has a random bounded name and does not use runtime
  auto-removal. Every invocation performs a bounded forced removal by that
  exact name before its success, failure, cancellation, or timeout is accepted.
  The cleanup is registered before crossing the runtime boundary, so a panic
  after the container starts follows the same path. A failed or hung
  force-removal takes precedence over every other outcome; cleanup failure and
  a compiler-runtime invariant failure are fatal worker conditions that leave
  the job leased for explicit expiry/operator handling. Neither is terminalized
  as an ordinary compiler failure, and compiler/runtime diagnostics or panic
  values never cross the stable worker or log boundary.
- Process execution remains private-only and is never represented as hard
  isolation. Public verification persists its hard-isolation requirement and
  can be claimed only by a validated container compiler with matching durable
  provenance.

## Consequences

Operators must pre-pull configured compiler images by digest before starting a
container verify role. A process cache on a group/world-writable or symlinked
path is rejected rather than repaired implicitly. These restrictions make
compiler availability more explicit while keeping compilation identity,
network access, resource use, and failure cleanup reviewable and reproducible.
