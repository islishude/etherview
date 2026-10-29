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

Atomic replacement alone is insufficient for a shared persistent directory.
On filesystems where `rename` replaces an existing destination, two replicas
can both install the same authenticated digest while one validator is between
`Lstat` and `Open`. The path remains safe, but the validator observes different
inodes and spuriously rejects the installation.

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
  catalog. Validation rechecks path identity after hashing. An inode change
  retries the complete validation at most eight times; stable type, mode, size,
  read, or digest failures fail immediately.
- Downloads continue to stream into a same-directory private temporary file,
  authenticate, sync, and close before distributed coordination. One process
  serializes its own cold misses, while independent replicas may download the
  same missing digest concurrently. Final destination recheck, atomic rename,
  and installed-file validation run under one digest-scoped writer PostgreSQL
  session advisory lock. A loser revalidates and reuses the winner instead of
  replacing it.
- Advisory-lock waiters use `pg_try_advisory_lock`, return a contended writer
  connection to the pool before waiting, and obey the compiler context. Only
  the winning session remains reserved for the bounded local installation; no
  database transaction, snapshot, or connection crosses the external HTTP
  download. An uncertain acquisition or release discards the connection, and
  there is no unlocked fallback.
- Base Compose mounts one project-scoped named volume at the compiler cache
  boundary. Preview instead bind-mounts `.local/preview-compilers` from the
  repository. Daily Preview and its E2E suites share one explicit bind-cache
  lifecycle: the host creates an empty parent, and a one-shot management
  container sets that directory and its `cache` child to UID/GID 65532 and
  mode 0750 before the API starts. Existing nonempty caches must already have
  that ownership and mode; initialization never recursively repairs their
  permissions. One lifecycle script invokes `docker run --rm` directly, outside
  the Compose service topology. The helper reuses Preview's pinned Node tooling image, has no
  network, a read-only root filesystem, no application secrets or Docker
  socket, and only the cache bind and read-only management script. It runs as
  root with only CHOWN, FOWNER and DAC_OVERRIDE capabilities. Application
  containers retain UID/GID 65532 and gain no capabilities. Among application
  roles, only `all` or `api` receives the cache. Ordinary restart,
  forced recreation, and image replacement retain it; an explicit Compose
  teardown with volume removal deletes the Base Compose volume but retains
  the Preview host directory. `make clean-preview-compiler-cache` is a separate
  explicit operator action: host orchestration rejects any running container
  mounting the directory, then the helper removes only its cache contents,
  without following symlinks, and the host removes the empty parent. E2E suites
  use canonical test-owned paths through `PREVIEW_COMPILER_CACHE_DIR`, stop
  their projects before this same cleanup, and treat cleanup failure as a test
  failure. They never use the daily cache or world-writable parents.
- Helm keeps its memory-backed `emptyDir` default. Operators may instead set
  one existing PersistentVolumeClaim shared by every `all` or `api` replica;
  the chart neither creates nor deletes that claim. Multi-node replicas require
  storage supporting their concurrent read/write access, atomic same-directory
  rename, consistent reads, Unix ownership and modes, and the configured
  capacity. RWX is the usual access mode for this topology. Every process that
  mounts one shared cache must use the same writer PostgreSQL database so the
  advisory key has one lock domain; sharing a volume across separate databases
  is unsupported.
- Cache growth is bounded by the host filesystem, Docker volume or PVC capacity and operator
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
chart cannot assume an RWX storage class. A cold install now also depends
briefly on writer PostgreSQL availability, while a completely validated cache
hit does not acquire an advisory lock.
