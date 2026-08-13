# ADR-0037: Rebuildable Persistent solc-js Artifact Cache

Status: accepted

## Context

The API-owned compiler already downloads an official `emscripten-wasm32`
soljson artifact into a private directory, verifies its catalog-bound SHA-256,
installs it atomically under a digest-derived name, and gives the resulting
read-only path to the permission-restricted Node wrapper. The application
therefore has a filesystem cache, not a download-to-memory execution path.

Compose, Preview, and Helm currently mount that directory from tmpfs or a
memory-backed `emptyDir`. Replacing the application container or Pod discards
otherwise reusable authenticated artifacts and forces the next verification
for each compiler version to download the same content again.

## Decision

- The compiler artifact cache is persistent but rebuildable performance data.
  It is not a correctness authority, is excluded from backups, and may be
  deleted while compiler-owning processes are stopped.
- Catalog origin, freshness, platform, version, byte limit, and SHA-256 remain
  the trust source. A persisted artifact never makes a stale catalog usable and
  never changes immutable job provenance.
- Every use revalidates that the digest-derived path is one ordinary,
  non-symlink, read-only file within its byte limit and hashes to the expected
  catalog digest. An invalid entry is replaced only by a newly downloaded and
  authenticated artifact; it is never executed or trusted as an offline
  catalog.
- Downloads continue to stream into a same-directory private temporary file,
  sync, close, and atomically rename into place. One process serializes its own
  cold misses. Independent replicas may download the same missing digest
  concurrently; if another replica wins installation, the loser accepts the
  destination only after complete validation. No distributed lock or storage-
  specific locking contract is introduced.
- Base Compose and Preview mount one project-scoped named volume at the
  compiler cache boundary. Only `all` or `api` receives it. Ordinary restart,
  forced recreation, and image replacement retain it; an explicit Compose
  teardown with volume removal deletes it.
- Helm keeps its memory-backed `emptyDir` default. Operators may instead set
  one existing PersistentVolumeClaim shared by every `all` or `api` replica;
  the chart neither creates nor deletes that claim. Multi-node replicas require
  storage supporting their concurrent read/write access, atomic same-directory
  rename, consistent reads, Unix ownership and modes, and the configured
  capacity. RWX is the usual access mode for this topology.
- Cache growth is bounded by the Docker volume or PVC capacity and operator
  monitoring. There is no automatic LRU or catalog-based deletion because a
  bound retry may still require an older catalog generation. Operators stop
  every mounting compiler owner before clearing the cache.

This decision supersedes ADR-0031 only where it calls the compiler cache
disposable as a deployment requirement. ADR-0031 remains authoritative for
the compiler source, runtime, execution, provenance, and security boundaries.

## Consequences

Repeated verification and application rollout no longer require re-downloading
an already authenticated compiler artifact. Cache loss or corruption degrades
only performance and remote availability: a healthy allowed origin can rebuild
the entry, while an unavailable origin produces the same stable compiler
failure as before. Shared Kubernetes persistence is opt-in because a portable
chart cannot assume an RWX storage class.
