# Contract verification and compiler execution

[Architecture overview](overview.md). Accepted [ADRs](../decisions/index.md) own the decisions behind these contracts.

This topic owns verification provenance and compiler execution.
See [operator procedures](../operations/verification.md) and
[acceptance requirements](../testing/contracts.md) for execution and validation.

- [Verification task execution](#verification-task-execution)
- [Submission, matching and derived verification](#submission-matching-and-derived-verification)
- [Compiler identity and isolation](#compiler-identity-and-isolation)
- [Sourcify and browser submission](#sourcify-and-browser-submission)
- [Automatic external source discovery](#automatic-external-source-discovery)

## Verification task execution

`verification` owns compiler refresh, source discovery, Sourcify calls, local
compilation and factory-derived workers; `api` admits and reads tasks. Each
instance uses its own worker count. PostgreSQL atomically claims jobs with
leases and fences publication. Optional NATS `jobs` hints wake claimers after
commit; periodic polling remains active and is the sole path without NATS.
Sourcify import returns 202 before source lookup and retains one job ID through
retrieval and local verification. First accepted inputs are immutable and
reused after restart; failed lookups are durable task outcomes. Public Vyper
versions read worker-published PostgreSQL entries matching the application's
recipe lock without installing an API compiler runtime.

## Submission, matching and derived verification

- Etherscan source verification reuses the native durable verification service
  and is gated by both the public-verification safety switch and an API key.
  The server derives code, code hash, block hash, and creation input from
  canonical PostgreSQL facts, verifies the stored runtime hash, then returns
  the durable verification-job UUID as the compatibility GUID. Source submit
  uses POST and source status permits GET or POST. Proxy submission uses POST
  and proxy status uses GET. Proxy jobs pin the exact canonical proxy mapping
  and require source publications for both code identities; a verification worker
  performs no compilation for them and atomically publishes an immutable
  binding only after rechecking canonicality and both sources.

- Native and compatibility verification both resolve the current canonical
  code, block, runtime bytecode, and creation input from PostgreSQL; native
  callers cannot submit those identities. An optional constructor-argument
  suffix is stripped only after exact comparison with the canonical creation
  input. Both boundaries reject empty runtime bytecode or a code hash that
  differs from its Keccak-256 digest. A durable submission
  digest covers the exact request payload and the server-derived public sandbox
  requirement; only the same active or successful digest is idempotent. A
  leased verification worker resolves and binds the job to one exact catalog generation,
  `emscripten-wasm32` artifact identity, compiler digest, and runtime executor
  digest before execution, and expired leases
  stop at their persisted attempt budget. Successful worker output is
  publishable only when the completion transaction finds an exact canonical
  code observation for the request's chain, address, code hash, and block hash.
  A stale target becomes a stable terminal failure, while successful results are
  immutable provenance rows projected deterministically to the verified
  contract read model. The fresh-database baseline creates the final publication
  guards directly. Production writes retain the relation order: immutable
  results, verified projections, then terminal job updates. See
  [ADR-0024](../decisions/ADR-0024-verifier-v2-workflow.md) and
  [ADR-0031](../decisions/ADR-0031-api-owned-solc-js-executor.md).

- Verification prepares duplicate-key-free, inline-source Solidity and Yul
  Standard JSON inputs with bounded server-owned outputs. It compiles the
  original sources and one whitespace-modified copy with the same exact
  compiler, uses their strictly validated differences to locate compiler
  auxdata, and automatically compares every bounded candidate. Matches retain
  Verifier Alliance-style auxdata, library, constructor, and immutable
  transformations and are classified as full or partial. Address publication
  requires a canonical runtime match and the exact immutable job result; a
  creation-only result is never address publication evidence. Every compiled
  variant retains its exact Standard JSON input alongside its full candidate
  set. Selection carries both through compilation-unit authentication and result
  settings, including when an earlier variant failed compilation.

- Successful Solidity address verification also retains one immutable bounded
  compilation unit. Factory-derived scans match its complete candidate set
  against canonical non-reverted CREATE/CREATE2 input and exact child runtime
  observations without RPC or recompilation. Each successful `trace@4` or
  `proxy@2` publication generation creates one durable forward event; events
  merge a fork-aware rescan floor, proxy publication wakes pending-runtime
  attempts, and successful pages reset rather than consume the failure budget.

- The scan lease heartbeats during bounded work. Candidate hydration, matching,
  uniqueness, and result construction occur once without a database snapshot;
  the final short transaction rechecks the canonical block, trace, creator code
  epoch, child runtime, immutable compilation identity, and prepared digests.
  Public creation provenance is exact-address and additive: submitted source
  origin remains submitted, children are scoped to the source compilation/code
  epoch, and code-hash artifact reuse never claims target-address creation.

## Compiler identity and isolation

- The `all` or `verification` process owns official catalog discovery, checksum-addressed
  artifact caching, and execution. Only the published
  `emscripten-wasm32/list.json` catalog is accepted; this is an
  architecture-independent compiler artifact format, not a Docker or CPU
  platform. Resolution validates approved HTTPS origins, redirects, catalog
  size, artifact size, and SHA-256. The API persists the generation, artifact
  identity, compiler digest, executor kind/policy, and executor digest, then
  atomically binds that complete identity under the worker lease.
  An artifact is streamed into a same-directory temporary file, authenticated,
  then installed under its digest while holding a writer PostgreSQL session
  advisory lock only for the final destination recheck, rename, and stable-file
  validation. Replicas may download one cold miss concurrently, but lock
  waiters do not retain a writer connection between attempts and a cache hit
  does not acquire the lock. Compose keeps this rebuildable cache across
  application replacement; Helm may mount one operator-owned shared PVC or
  retain its default memory-backed cache. Every replica sharing one cache must
  use the same writer database lock domain. Cache persistence never overrides
  catalog freshness or provenance. See
  [ADR-0037](../decisions/ADR-0037-persistent-solcjs-artifact-cache.md).

- Vyper execution follows [ADR-0054](../decisions/ADR-0054-vyper-application-lock.md):
  an application-embedded supported-version lock replaces independently signed catalogs
  and hosted compiler packages. Vyper 0.2.0 is retired; exact locked
  upstream archives download from PyPI into an authenticated SHA-256 cache.
  The shared bounded Go assembler produces the locked package before atomic
  installation. There is no runtime index lookup, dependency resolution or pip.
  PostgreSQL retains immutable application snapshot generations, recipe-bound
  requests and compiler/executor bindings; no remote expiry governs Vyper.
  Each process resolves its own application snapshot, not another replica's head.
  Dual Pyodide runtime selection, fresh restricted SEA processes, before/after
  identity validation and native AMD64/ARM64 monolith/split acceptance remain.
  Solidity's independently refreshed compiler catalog remains unchanged.

- Native address verification also accepts a bounded inline Geas v0.3.3 source
  filesystem with a required runtime entrypoint and optional creation
  entrypoint. Each entrypoint is assembled twice with stack checking in fresh
  helper subprocesses; runtime and supplied creation input match exactly, with
  no Solidity transformations or ABI. Only transitively read source files are
  published. The helper's exact Go module checksum and executable digest bind
  once under the job lease without a compiler catalog. See
  [ADR-0039](../decisions/ADR-0039-pinned-geas-verification-executor.md).

- The production image includes one Node 26.13.0 SEA containing the
  `solc@0.8.37` wrapper protocol, plus a canonical read-only runtime manifest
  and any target-rootfs-missing ELF libraries discovered recursively at build
  time. It contains no general Node executable, npm tree, wrapper source, or
  bundled default compiler. Startup verifies every manifest path and digest and
  performs a permission self-test. Each deterministic compilation is a separate subprocess with a
  minimal environment, private temporary directory, 384 MiB V8 heap,
  input/output/time bounds, and whole-process-group termination. The subprocess
  may read only its runtime and selected compiler artifact and receives no
  network, child-process, worker, addon, WASI, FFI, or inspector permission.
  The permission model is defense in depth for trusted checksum-pinned solc-js,
  not a claim that Node isolates malicious JavaScript.

- API readiness is independent of temporary solc catalog availability. When no
  validated catalog generation is available, the version surface reports
  unavailable and Solidity/Yul jobs remain queued rather than being executed
  with an unbound artifact. Geas, proxy, or Sourcify jobs may continue. There is no
  standalone runner, runner network, native compiler fallback, or CPU-platform
  selection. See
  [ADR-0031](../decisions/ADR-0031-api-owned-solc-js-executor.md) and
  [ADR-0040](../decisions/ADR-0040-sea-packaged-solcjs-executor.md).

## Sourcify and browser submission

- Sourcify v2 remains an optional interoperability adapter rather than a local
  trust root. Calling its dedicated verification endpoint is explicit source
  publication consent. Bounded asynchronous polling returns an external result
  only; local address publication still requires the ordinary compiler,
  transformation, runtime, lease, and canonicality checks. See
  [ADR-0024](../decisions/ADR-0024-verifier-v2-workflow.md).
  The Web Verify entry lives under More and uses a two-step form/result flow.
  SIWE login is required to submit or read a task, with no Etherview API-key
  field. Four explicit session endpoints under `/api/v1/verification-jobs`
  share the native verifier service; local and Sourcify import bodies include
  the target address. Cookie, exact Origin and CSRF protect writes; native and
  Etherscan programmatic submissions retain API-key scope checks. A
  `/verify?job=<UUID>` result link restores status after login without restoring
  uploaded files or credentials. Results show progress and outcome first, with
  technical details collapsed. Session changes cancel requests and discard
  private query data; result reads expose target identity and match evidence
  rather than sources or compiler artifacts. Existing source publication
  consent and the optional upstream Etherscan key remain separate.
  The Web verification page uses service cards and defaults the Sourcify workspace
  to importing existing sources into Etherview. The API resolves a canonical
  local target, closes its snapshot and enqueues a `sourcify_import` job. The
  verification worker retrieves bounded sources by the selected source chain
  and same address, persists its input and performs local address verification. Remote source/runtime
  inconsistencies fail closed; local source and ABI publication still require
  compiler matching and canonical/lease fencing. External source publication is
  a separate action with source/metadata and Etherscan modes, both requiring
  explicit consent. Completed external jobs offer local import using their
  submitted chain/address. The source chain is a positive uint64 string and never
  changes the local target's chain. Source files and metadata have independent
  upload controls and share the existing bounded file-map submission contract.
  Local Standard JSON and source inputs also use file selection. Browser-side
  extension checks allow `.sol` for Solidity, `.yul` for Yul, `.vy`/`.vyi` for
  Vyper, `.eas` for Geas, and `.json` for Standard JSON and metadata; Sourcify
  sources accept `.sol`/`.yul`/`.vy`/`.vyi`. Extensions must be lowercase.
  Source paths can be edited independently of file contents, with relative-path
  and duplicate checks. Selection is bounded before reads and complete JSON
  requests are bounded before submission (1024 files and 5 MiB). These Web
  checks do not change API filename rules or Standard JSON source keys.
  All actions use the generated same-origin client. Job views
  identify Sourcify results separately from local verification and show the
  external verification ID without implying local source or ABI publication.
  Forms label Etherview authentication separately from the optional Etherscan
  API key used only for imports. The latter is encrypted before durable enqueue
  with keys derived from the API role's pepper, recovered only for the Sourcify
  request, omitted from all job responses, and cleared from the form and
  completed mutation variables. A keyed fingerprint preserves active-job
  deduplication despite randomized encryption.
  Its HTTPS client checks public-network addresses by default. The independent
  `sourcify.unsafe_allow_private_networks` development override defaults off outside
  Preview. Preview enables it for Fake-IP/private destinations while retaining
  TLS verification, redirect rejection and explicit publication consent.

- Verification reads and submissions are separate runtime capabilities.
  API-key-protected job reads and anonymous, free verified-artifact reads
  remain backed by PostgreSQL when public compilation submission is disabled;
  the public configuration advertises only actually usable submission and
  Sourcify surfaces.

## Automatic external source discovery

The optional eth-bytecode-db adapter is search-only and disabled by default
outside Preview. Preview enables it together with local verification.
Verification/all workers repeatedly scan canonical contract addresses using durable
per-chain cursors, fetch bounded candidates through an HTTPS client with
public-network checks by default,
and enqueue low-priority local verification. PostgreSQL owns provider/bytecode
lookup identity, global request admission, cooldowns, leases and candidate/job
provenance. At most one automatic compilation runs concurrently. Local
compiler identity, dual compilation, exact target and publication fences remain
mandatory; an external FULL label or ABI never grants local verification.
Ordinary contracts wait for creation facts, while authenticated Genesis
predeploys retain their existing exception. Successful source origin is exposed
as `eth_bytecode_db`. See [ADR-0056](../decisions/ADR-0056-bytecode-db-source-discovery.md)
and the [operator procedure](../operations/verification.md#automatic-bytecode-database-source-discovery).
The independent `bytecode_db.unsafe_allow_private_networks` development override
defaults off outside Preview. Preview enables it for Fake-IP/private destinations while
retaining TLS verification, redirect rejection and all source-verification fences.

## Queue capacity and input lifecycle

Admission and terminal transitions use the PostgreSQL capacity contract in
[ADR-0024](../decisions/ADR-0024-verifier-v2-workflow.md#input-persistence-and-admission-capacity).
Small job metadata and source-free status projections remain available after
failed-input retention expires. The source body stores the base compiler input
once, with ordered settings alternatives, and workers reconstruct variants before
compiling. Successful evidence remains durable. `verification-input-cleanup`
runs with the verification role in every layout; it performs bounded hourly work.

Status polling reads a stored outcome summary containing kind, verification ID and
match evidence, alongside target identity and task state. It does not detoast
submitted sources or full successful results. The native status endpoint retains
its existing complete-result contract through a separate evidence read; browser
and compatibility polling never fetch that source-bearing evidence.
