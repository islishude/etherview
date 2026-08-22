# P69 — Solady Legacy CWIA Proxies

Status: `done`

## Outcome

Recognize only the exact Solady legacy `LibCWIA` runtime as an immutable,
single-implementation proxy. Persist its fixed implementation and raw packed
arguments through the existing `proxy@2` authority, consume the implementation
ABI through the established verified binding, and derive typed immutable
arguments only from bounded canonical Solady helper calls in the dual-compiled
Solidity AST. Exact shell observations remain readable through a matching
implementation code artifact; writes require an exact verified binding and a
successfully decoded complete schema.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0010: Block-pinned proxy stage and ABI dependency](../decisions/ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0028: Proxy verification and Hardhat E2E](../decisions/ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [ADR-0032: Evidence-based proxy detection](../decisions/ADR-0032-evidence-based-proxy-detection.md)
- [ADR-0042: Solady legacy CWIA identity and verified immutable arguments](../decisions/ADR-0042-solady-legacy-cwia-identity.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P69-T01 | done | P20-T13, P58-T02 | Exact legacy LibCWIA runtime parser, authoritative `proxy@2` projection, independent V2 detector, and persistent `cwia` mechanism | parser/detector unit and fuzz tests; PostgreSQL constraints; `make source-check` |
| P69-T02 | done | P69-T01 | ABI and proxy-verification binding support plus generated native/Etherscan API contracts for raw CWIA arguments | query, verification, API, generation, integration, and reorg tests |
| P69-T03 | done | P69-T02 | Verified NatSpec schema parsing, packed scalar decoding, bilingual Web presentation, and schema-digest write fence | schema/parser unit tests; Vitest; embedded Chromium E2E |
| P69-T04 | done | P69-T03 | Pinned Solady Hardhat fixture, monolith/six-role parity, operations/testing documentation, and aggregate gates | schema/runtime/Hardhat E2E; security; common gates |
| P69-T05 | done | P69-T04 | Preview regression fix: expose code-hash-authenticated CWIA implementation reads before proxy verification while preserving exact-binding/schema write gates and reason-specific status text | focused target/page tests; embedded Chromium E2E; live Preview inspection |
| P69-T06 | done | P69-T05 | Replace the provisional NatSpec schema with bounded canonical Solady CWIA Solidity-AST derivation, dynamic bytes/array decoding, code-hash read projection, and exact-binding write fencing | verifier/query/API/Web tests; production Hardhat/browser/runtime gates; common gates |
| P69-T07 | done | P69-T06 | Separate exact implementation verification from code-hash artifact availability and replace immutable-argument facts with an accessible Name/Type/Offset/Data table | query/API/write-fence tests; bilingual browser/Preview regressions; common gates |
| P69-T08 | done | P69-T07 | Keep published proxy interaction tabs stable when a fresh operation fence observes a transient latest-stage unavailable snapshot, while failing that operation closed and preserving real target-change refreshes | target/form/page tests; real-Chromium transient-stage regression; live Preview reproduction |
| P69-T09 | done | P69-T08 | Hide the direct verified-artifact submission surface on recognized CWIA shell addresses while retaining implementation artifact source/ABI and proxy interaction | page/unit/browser regressions; live Preview inspection |

Allowed item states are `todo`, `in_progress`, `blocked`, `done`, and `dropped`.

## Acceptance

- [x] Only the exact legacy `LibCWIA` shell is accepted; modern `LibClone` and
      unrelated CWIA runtimes remain unsupported.
- [x] The two encoded lengths, implementation position, fixed opcode bytes,
      argument range, product limit, zero/self targets, and same-block code
      identity are validated without creation-trace dependence.
- [x] The authoritative observation is `mechanism=cwia`, `pattern=clone`, has
      no standard version or management authority, and retains raw arguments
      without the two-byte footer.
- [x] The independent V2 outcome is attributed to the CWIA detector and keeps
      malformed, over-limit, missing-code, RPC-unknown, and composed Diamond
      states distinct.
- [x] A bounded version-2 schema is derived only when reachable helper calls
      resolve to the exact canonical Solady `0.1.26` source; arbitrary same-name
      functions and unsupported AST data flow produce no typed values.
- [x] Fixed, dynamic bytes, and canonical array accesses cover the raw arguments
      exactly and expose checksummed addresses, decimal uints, lowercase bytes,
      and bounded string arrays with stable public status and reasons.
- [x] Reads remain available for an exact high-confidence CWIA shell with a
      matching implementation code artifact; writes require an exact verified
      binding plus a decoded schema and refresh the binding, code identities,
      raw proxy code, and schema digest before submission.
- [x] Reorg, replay, persistence/restart, monolith/split, generated API, Web,
      and real pinned Hardhat behavior pass without enabling public proxy V2.
- [x] Migration and startup do not enqueue, rewrite, or replay historical proxy
      or ABI work; support is guaranteed only for blocks first processed after
      the feature is deployed.
- [x] Canonical Solady `0.1.26` helper calls derive one bounded AST schema from
      dual compilation, cover the raw bytes exactly, and decode fixed, dynamic,
      and array values without NatSpec or source-text guessing.
- [x] Code-hash AST artifacts permit typed reads before proxy verification;
      writes still require the exact current binding and refreshed schema digest.
- [x] Current proxy implementations expose exact-address versus code-hash
      artifact resolution without weakening binding or management authorization.
- [x] Decoded immutable arguments use an accessible Name/Type/Offset/Data table
      with copyable typed values and bounded array element rows.
- [x] A transient unavailable latest proxy stage fails the current interaction
      without replacing the displayed published target or redirecting its tab;
      real binding, code, resolution, or schema changes still refresh closed.
- [x] Recognized CWIA shell addresses do not expose a direct Verified artifact
      section or verification-submission entry; implementation artifact and
      implementation-as-proxy surfaces remain available.

## Current Blockers

None.

## Evidence

- P69-T01 adds the byte-exact 98-byte legacy shell parser, authoritative CWIA
  clone projection, independent `solady-cwia` V2 outcome, Diamond composition,
  and migration `0050` without changing stage versions or scheduling history.
  Focused/full enrichment and store tests plus `make source-check` pass. The
  owned PostgreSQL 18 `make test-integration` gate applies all migrations
  through `0050`, persists exact raw arguments and V2 attribution, passes every
  integration package, and removes its project and volume.
- P69-T02 extends the canonical clone-shaped verification admission and
  immutable binding constraints to `cwia`, preserves raw arguments through the
  native and Etherscan projections, and adds generated `cwia` mechanism/family
  contracts. Focused query, app, verification, Etherscan, observability, and
  API contract tests pass; the PostgreSQL binding regression is compiled for
  the owned integration gate.
- P69-T03 reads the reserved contract-level NatSpec only through the exact
  current implementation verification, strictly normalizes and SHA-256 binds
  version-1 fixed packed scalar schemas, and exposes bounded unavailable,
  invalid, mismatch, and decoded projections while retaining raw bytes. The
  Web renders bilingual typed values and fences CWIA writes on the decoded
  schema digest. Focused Go tests, 52 proxy/Web Vitest cases, Web lint, and the
  real-Chrome CWIA read/write, bilingual, 390px, and accessibility flow pass.
  P69-T06 supersedes this provisional schema source while retaining the write
  fence and raw-byte authority.
- P69-T04 pins `solady@0.1.26` and deploys a real legacy LibCWIA account whose
  52-byte owner/uint256 arguments, large decimal value, getters, owner-gated
  delegated storage write, exact implementation source, `Proxy=1` Etherscan
  fallback, historical ABI, native API, and absence of upgrade history pass in
  production images. `make test-hardhat3-e2e-prebuilt` passes monolith in
  162.14s and the complete six-role topology in 106.54s; normalized persistent
  counts and public results match.
- P69-T05 reproduces the Preview state where the exact high-confidence CWIA
  shell and both code identities are current but no verified proxy binding
  exists. The Web now loads a matching code-hash implementation artifact for
  read-only `Read/Write implementation (as proxy)` tabs, discards any unbound
  schema digest, keeps every state-changing function unavailable, and labels
  `proxy_not_verified` as an unverified binding rather than an unavailable
  verified schema. Focused target/page tests pass 53/53, the full Web suite
  passes 350/350, and `make test-e2e` passes 24/24 real-Chrome flows including
  the new unverified-CWIA fixture. After `make recreate-preview`, live browser
  inspection of `0x9f1ac54BEF0DD2f6f3462EA0fa94fC62300d3a8e`
  confirms `data()`, `number()`, and `owner()` in the read tab, an empty guarded
  write tab, reason-specific status text, and unchanged raw immutable bytes.
- P69-T06 replaces the provisional NatSpec contract with server-owned analysis
  version 1 and public schema version 2. Dual compiler outputs must agree on a
  bounded descriptor whose helper references resolve to the exact
  `bc97b0d0…9cfca3` Solady source. All canonical uint/address/bytes/array
  helpers, constant offsets, single-assignment dynamic lengths, strict full
  coverage, getter naming, exact-address precedence, code-hash consensus, and
  binding-plus-digest write fencing have focused regressions. Schema fuzz runs
  859,704 inputs and AST-walker fuzz runs 609,990 inputs without a panic.
- P69-T06 production gates pass: PostgreSQL integration and race complete in
  159.88s and 173.67s; real Chrome passes 24/24; production Hardhat passes
  monolith in 117.57s and distributed in 145.39s with dynamic
  owner/uint256/uint16/bytes arguments; schema E2E passes; and runtime E2E
  passes monolith in 37.06s and distributed in 46.37s. The rebuilt Preview
  implementation was explicitly re-submitted under the new analysis version;
  live API and browser inspection show code-hash AST resolution, owner,
  number=100, data_length=11, bytes data, proxy read tabs, and the unverified
  binding write fence. The temporary verification-only API key was revoked.
- P69-T07 adds optional public `artifact_resolution` while retaining
  exact-address `verification_state`. Query regressions cover CWIA, EIP-1167,
  EIP-1967, and Beacon current implementations; exact-address wins, absent
  artifacts remain unresolved, and proxy/admin/beacon/management identities
  reject code-hash substitution. Browser fences include the resolution and
  keep code-hash implementation artifacts read-only. The Web renders the
  independent four-column table, complete copyable array parents, 64 bounded
  element rows, per-array omission notices, bilingual labels, keyboard focus,
  and internal 390px horizontal scrolling.
- The rebuilt Preview preserves its existing Genesis and PostgreSQL volumes.
  Live API inspection of CWIA
  `0x5cBc06b3274eFE4e7750d523b6C3fA3655844A61` reports implementation
  `0xCafac3dD18aC6c6e92c921884f9E4176737C052c` as
  `unverified + code_hash`, with decoded AST values at offsets 0, 20, 52, and
  54. Its page shows the English and Chinese Name/Type/Offset/Data table plus
  `owner()`, `number()`, and `data()` read forms while the write tab remains
  disabled. The implementation Code page reports code-hash verification only,
  links verified source `0x5FbDB2315678afecb367f032d93F642f64180aa3`,
  and no longer displays an unverified state beside verified source.
- P69-T07 gates pass: focused Go/API/Web tests, all 355 Vitest cases,
  `make generate-check`, `make source-check`, and `make plan-check`; PostgreSQL
  integration (153.32s) and race (169.30s); 24/24 real-Chromium E2E including
  bilingual narrow-table and Code-page regressions; fresh production-image
  schema E2E; runtime monolith/distributed (37.50s/45.97s); Hardhat provider
  compatibility and real Solady production E2E monolith/distributed
  (139.19s/140.94s); `make security-check`; and `make check`. The locked
  Hardhat dependency tree retains its known eight low-severity transitive
  `elliptic` findings and no high-severity audit failure.
- P69-T08 reproduces the reported Preview failure on
  `0x9f1ac54BEF0DD2f6f3462EA0fa94fC62300d3a8e`: in 120 bounded live proxy
  reads, 45 returned the detected CWIA identity, 51 returned a successful
  `status=unavailable` latest snapshot, and 24 were rate-limited. A getter's
  fresh fence previously converted the unavailable snapshot to
  `TARGET_CHANGED`, invoked the page refetch, replaced its cached target, and
  redirected to Code. `FRESH_PROXY_UNAVAILABLE` now aborts the call before
  wallet RPC without invoking that callback; every retry still performs a new
  fresh read, and true binding/target failures retain the old refresh fence.
- Focused target and ABI-form tests cover unavailable versus failed state,
  absence of `eth_call`, callback suppression, and successful retry. The
  production-browser fixture connects an EIP-6963 wallet, returns two
  consecutive unavailable fresh snapshots, asserts both implementation tabs
  and `#read-implementation` remain stable, then completes the third getter
  attempt. All 357 Vitest cases, 24/24 Chromium cases (44.0s),
  `make security-check`, `make check`, plan/diff checks, and the rebuilt
  volume-preserving Preview pass; the live page again exposes Code,
  Read/Write-as-proxy, and Initialization history. The known eight
  low-severity transitive Hardhat `elliptic` findings remain unchanged.
- P69-T09 suppresses the direct Verified artifact heading and verification
  submission link after mechanism `cwia` is published, without suppressing the
  implementation artifact query or implementation-as-proxy tabs. While proxy
  classification is `unavailable`, the choice remains hidden and the hook
  rechecks every 500 ms; browser coverage holds an initial unavailable response
  open, proves no direct-entry flash, then releases the exact CWIA identity.
  Ordinary Clone, ordinary contract, and implementation Code-page regressions
  retain their direct artifact surface.
- All 358 Vitest cases and 24/24 Chromium cases (44.4s) pass, along with
  `make check`, plan/diff checks, and the volume-preserving Preview rebuild.
  Live inspection of `0x9f1ac54BEF0DD2f6f3462EA0fa94fC62300d3a8e`
  shows no Verified artifact heading or submission link while Code,
  Read/Write-as-proxy, Initialization history, and Proxy identity remain. Its
  implementation `0xCafac3dD18aC6c6e92c921884f9E4176737C052c` still resolves
  the code-hash artifact and verified source after classification completes.
- Final gates pass: the bytecode fuzz target executed 591,964 inputs in five
  seconds; `make test-integration` and `make test-integration-race` pass the
  exact fork/replay/restart and verification-schema regressions against owned
  PostgreSQL 18; `make test-schema-e2e` applies migration `0050`; `make
  test-runtime-e2e` passes monolith (37.68s) and distributed (45.36s); and
  `make test-e2e` passes 24/24 real-Chrome flows. `make check` passes generated
  contracts, source/plan checks, zero-issue Go/Web lint, 351 Web tests, ordinary
  and race suites, vulnerability/secret/license checks, Buildx, Compose, and
  Helm gates. The locked Hardhat tree retains its known eight low-severity
  transitive `elliptic` findings and has no high-severity audit failure.
