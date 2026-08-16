# ADR-0040: SEA-Packaged solc-js Executor

Status: accepted

## Context

ADR-0031 established the API-owned, architecture-neutral solc-js executor and
ADR-0037 made its checksum-addressed compiler-artifact cache persistent. The
production image currently carries a general Node executable, a separate
wrapper and npm dependency tree, and a build-time list of three copied shared
libraries. That layout preserves the accepted trust boundary, but it makes the
runtime surface and operator configuration larger than necessary and allows a
future Node release to add an undeclared ELF dependency.

Node Single Executable Applications can embed the trusted JavaScript protocol
and wrapper in one host-native executable. SEA does not statically link Node or
remove its operating-system dependencies, so the final executable still needs
an exact, generated ELF dependency closure.

## Decision

- The production solc-js executor is a Node 26.7.0 SEA containing one bundled
  CommonJS entrypoint and the exact-lockfile `solc@0.8.36` wrapper. The bundle
  imports `solc/wrapper`, not the package root, and does not contain a default
  soljson compiler. Exact checksum-authenticated official
  `emscripten-wasm32` soljson artifacts remain external in the persistent
  rebuildable cache.
- The SEA exposes only `--self-test` and
  `--compile <absolute-artifact-path> <normalized-version>`. Standard JSON
  remains stdin/stdout, no import callback is supplied, and one fresh process
  handles each input.
- SEA startup embeds the permission model, disabled SIGUSR1 inspector,
  disabled addons and global search paths, and the 384 MiB V8 heap bound. CLI
  execution-argument extension is enabled only so the Go parent can add one
  exact artifact read permission. The parent still supplies a secret-free
  environment, private temporary directory, bounded I/O, timeout, and process
  group cleanup. Node permissions remain defense in depth for trusted code.
- The production runtime is one read-only directory containing
  `etherview-solcjs`, `runtime-manifest.json`, and `lib/`. Operator
  configuration exposes only the absolute clean executor path; manifest and
  private-library paths are derived from its directory. Alternate host or
  custom-image runtimes must provide this complete layout and identity.
- Image assembly recursively analyzes the final SEA with `pax-utils` `lddtree`
  against the exact distroless target rootfs. Dependencies already resolved by
  that rootfs are recorded as base-provided. Missing dependencies are copied,
  dereferenced, from the Node builder into the private `lib/` directory. There
  is no shared-library name allowlist. Unresolved dependencies, conflicting
  SONAMEs, unsafe paths, or missing package and license attribution fail the
  build.
- A canonical `etherview-solcjs-sea-runtime-v1` manifest records the exact
  Node, wrapper, bundle builder and SEA policy, the ELF interpreter and full
  transitive dependency inventory, and hashes for the SEA plus every private
  library. Base-provided library bytes are bound by the final OCI image rather
  than duplicated in `executor_digest`. Go validates the complete runtime tree
  and uses the manifest SHA-256 as the immutable executor digest.
- New work continues to use `executor_kind=node_solcjs_v1` and
  `execution_policy=trusted_subprocess`. Existing terminal records need no
  migration. Operators drain executor-bound work before rolling out a new
  manifest digest to homogeneous API-capable replicas.
- The production image contains no general `node`, npm tooling, wrapper source,
  package metadata, or `node_modules`. Build-stage and production-image tests
  execute the SEA self-test, and the build stage also performs a real compiler
  invocation with the exact installed soljson fixture.

This decision supersedes ADR-0031 only for the Node runtime packaging,
runtime-path configuration, build manifest, dynamic-library assembly, and
subprocess launch details. ADR-0031 remains authoritative for compiler trust,
catalog, compilation, provenance, and security semantics. ADR-0037 remains
authoritative for cache validation, persistence, and coordination.

## Consequences

The image exposes a smaller compiler runtime and future Node dependency changes
are discovered from the actual executable instead of a maintained SONAME list.
SEA remains host-native and dynamically linked, so every supported Linux image
architecture builds and validates its own executable and dependency manifest.
Custom runtimes must be rebuilt as one coherent directory; the removed three
path settings are not compatibility aliases.
