# ADR-0031: API-Owned Architecture-Neutral solc-js Executor

Status: accepted

## Context

ADR-0029 and ADR-0030 removed application-controlled container daemons but
retained a separately deployed compiler-runner. That runner still selects a
native solc-bin directory from its container CPU architecture. Preview also
persisted the runner image digest outside Compose, so a previously built
`linux/amd64` image remained active after source-level platform settings were
removed.

Official Solidity catalogs publish checksum-addressed
`emscripten-wasm32` solc-js artifacts. Those artifacts are independent of the
host CPU architecture and can be loaded by the same small, pinned solc-js
wrapper on every supported application image.

Vyper has no equivalent architecture-neutral artifact contract in the current
implementation. Retaining its historical Linux AMD64 catalog would preserve
the platform coupling this decision removes.

## Decision

- Public verification supports Solidity and Yul. Native Vyper submission,
  matching, catalog, UI, and compatibility paths are removed. Migration 0031
  deletes Vyper verification data and publications; the removed behavior is
  recorded only as a possible future design input.
- `api` and `all` own compiler catalog refresh, checksum-addressed artifact
  caching, job leases, compilation, and publication. There is no
  compiler-runner service, runner protocol, runner image, or compiler-specific
  deployment platform setting.
- Solidity catalog discovery uses only the official
  `emscripten-wasm32/list.json` format or an explicitly configured mirror with
  the same platform identity. `emscripten-wasm32` is compiler artifact
  provenance, not an OCI or CPU architecture constraint.
- The production image contains a host-native Node 26.5.0 binary and an
  exact-lockfile `solc@0.8.36` wrapper package. The wrapper loads one exact
  checksum-verified soljson file, confirms its normalized long version, accepts
  one Standard JSON document on stdin, supplies no import callback, and writes
  only compiler JSON to stdout.
- The Node executable, wrapper, and runtime-manifest paths default to their
  production-image locations but are explicit non-secret operator
  configuration. Overrides must be absolute clean paths that identify one
  coherent read-only runtime and pass the same manifest identity, complete-tree
  checksum, and wrapper self-test. This supports host deployments and custom
  images; the standard Compose and Helm surfaces do not mount an alternate
  runtime or weaken the bundled runtime identity.
- Every compile input runs in a new Node process. The parent supplies an empty
  secret-free environment, private temporary directory, absolute runtime and
  artifact paths, a 384 MiB V8 heap, Node's permission model, bounded
  stdin/stdout/stderr, and the existing compile timeout. Cancellation or
  timeout terminates the process group before the lease is released.
- The Node permission model is defense in depth, not a security boundary for
  malicious JavaScript. Etherview trusts only the exact lockfile runtime and
  official catalog artifact whose SHA-256 was verified by Go. Contract source
  and Standard JSON remain hostile bounded data and never select executable
  code, paths, imports, environment, or subprocess arguments.
- A canonical build-time manifest covers the Node binary, wrapper, production
  package tree, and their hashes. `api` and `all` verify the complete manifest
  and a bounded wrapper self-test before accepting compiler work. The manifest
  digest is immutable `executor_digest` provenance. New compiler work uses
  `executor_kind=node_solcjs_v1` and
  `execution_policy=trusted_subprocess`.
- Compiler catalog and artifact download remains proxy-free, redirect-free,
  origin-allowlisted, public-address-only by default, size bounded, and
  checksum verified. Node receives no network permission. A transient catalog
  outage does not fail API readiness; versions become unavailable and compiler
  jobs remain queued until a retained or refreshed generation is usable.
- Compiler provenance is bound atomically by the leased worker: platform,
  catalog generation, compiler digest, executor kind, execution policy, and
  executor digest transition from entirely empty to entirely complete once.
  A retry must use the same complete identity.
- Existing terminal Solidity records retain their native compiler platform and
  legacy executor provenance. Active legacy compiler jobs terminate with
  `executor_migrated`. Vyper deletion is irreversible without restoring the
  operator's pre-migration backup, and old and new application versions are
  not supported concurrently across migration 0031.
- Application images remain multi-architecture. BuildKit may select artifacts
  for its current target architecture while assembling an image, but Compose,
  Preview, Helm, CI service definitions, and operator configuration never pin
  one CPU platform. Replicas within one deployment are assumed homogeneous
  while executor-bound jobs are drained during upgrades. A runtime-path change
  follows the same rule: drain bound jobs, deploy one manifest digest to every
  API-capable replica, and restart those replicas before admitting new work.

This decision supersedes ADR-0029 and ADR-0030. It supersedes ADR-0024 only
where that decision requires Vyper, native executable platform selection,
hard compiler isolation, runner images, or runner provenance. ADR-0024 remains
authoritative for catalog integrity, dual compilation, bounded matching,
canonical publication, stable errors, and hostile-input handling.

## Consequences

Preview and production no longer carry a hidden AMD64 compiler dependency.
The application image is larger because it includes the minimal Node runtime
and solc-js wrapper, and API replicas require bounded HTTPS egress plus a
disposable compiler cache. The trusted-subprocess model is intentionally
narrower than a malicious-code sandbox; expanding compiler sources beyond the
authenticated official solc-js catalog requires a new security decision.
An operator that selects alternate runtime paths is responsible for placing
that complete runtime in the host or custom image; path configuration alone
does not supply files, volumes, or a new trust boundary.
