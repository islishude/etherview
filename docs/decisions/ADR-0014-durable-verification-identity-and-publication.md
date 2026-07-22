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
- Before that digest is computed, the service prepares one reproducible
  Standard JSON document, and the repository repeats that preparation as a
  defense-in-depth boundary for direct callers. Duplicate JSON keys are
  rejected before map decoding. The language must agree with the requested
  compiler, every source is an inline `content` object (never a compiler URL),
  the exact `source:name` target must exist, and a Vyper target must be a `.vy`
  source whose name equals its filename stem. Every Vyper source, interface,
  override target, and override-file name is a clean relative POSIX path, so a
  compiler-normalized alias cannot change the exact target identity. Vyper
  interfaces are inline and collectively bounded. Before 0.4.0, `.vy` content,
  `.json` ABIs, and EthPM v3 `contractTypes` are accepted, with ignored manifest
  fields removed and bare ABI arrays normalized to the object form. Vyper 0.4.0
  and newer instead accepts `.vy`/`.vyi` content and `.json` ABIs, but not its
  unsupported EthPM form. Storage-layout overrides are accepted only where
  Vyper 0.4.1 or newer consumes them: 0.4.1 takes the layout directly, while
  0.4.2 and newer require exactly one clean inline override-file path.
- Caller code-generation settings and supported top-level inputs are
  preserved, but `outputSelection` is server-owned and replaced rather than
  unioned. Solidity requests only the exact target ABI, metadata, creation and
  runtime bytecode, link references, and immutable references. Vyper requests
  ABI and both bytecodes before 0.3.10, adds metadata through 0.4.0, and adds
  layout from 0.4.1. Vyper before 0.4.0 requires a non-empty selection for
  every source, so unrelated sources receive only `userdoc`; newer versions
  select only the target. This removes wildcard/group expansion, selector-order
  ambiguity, and non-target output amplification. Vyper 0.4.0 and newer use
  the fixed in-memory search path `["."]`; no caller-selected filesystem path
  is accepted.
- The prepared bytes, not the pre-normalized API payload, are persisted,
  digested, and sent to the compiler. Semantically identical service and direct
  repository submissions therefore share an identity. Workers never silently
  rewrite a leased input.
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
- An append-only migration that strengthens job/result/projection guards takes
  transaction-scoped `SHARE ROW EXCLUSIVE` locks before replacing any function
  or trigger or validating existing rows. It follows the production
  RowExclusive acquisition order—`verification_results`, `verified_contracts`,
  then the terminal `verification_jobs` update—so pre-guard DML either commits
  before validation or waits until the new guards commit; it cannot cross the
  migration boundary.
- Mismatch results are durable successful results but contain no ABI, sources,
  or settings. Failed jobs contain only an allowlisted stable error code. Lease,
  result, error, size, JSON-shape, attempt, identity, and provenance coherence
  are enforced by PostgreSQL constraints as well as repository validation.
- A compiler output is eligible even for a durable `mismatch` only after its
  complete target artifact has been validated. The requested source and
  contract are selected without fallback; ABI, language-specific metadata,
  required bytecode objects, reference maps, and version-required Vyper
  metadata/layout must have their documented shapes. Compiler diagnostics,
  malformed or duplicate-key JSON, unresolved library
  placeholders, non-empty link-reference maps, missing outputs, and invalid or
  excessive ranges are compiler-output failures, not bytecode mismatches.
- Matching ignores only compiler-declared deployment-time values. Solidity
  deployed-bytecode immutable references are byte ranges; every range must be
  positive, bounded, non-overlapping, and outside enabled compiler metadata.
  With CBOR disabled, footer-shaped executable bytes do not create a boundary.
  The same
  immutable identifier must have the same deployed value at every occurrence.
  Libraries must already be linked through Standard JSON `settings.libraries`;
  link slots are never wildcards. For Vyper 0.4.1 and newer, immutable data is
  accepted only as the exact suffix length declared by both creation auxdata
  and `layout.code_layout`; the declarations must agree. Vyper 0.3.10 through
  0.4.0 use the creation tuple's authenticated immutable size because those
  official Standard JSON formatters omit layout. Earlier formats, plus Vyper
  0.3.10 through 0.4.0 with metadata disabled, have no such declaration, so
  only a zero-length suffix can match. A missing or malformed declaration is
  never replaced by a length guess.
- `exact` means that bytecode is identical after only those declared immutable
  values are normalized. `metadata_only` requires different, completely
  decoded language-specific metadata with identical normalized executable
  bytes. Solidity `settings.metadata.appendCBOR`, when present, must be boolean;
  setting it to `false` disables `metadata_only` because the compiler output has
  no authenticated footer boundary. Otherwise Solidity metadata is one bounded
  CBOR map located by the final two-byte payload length. Vyper metadata uses its
  versioned format: a legacy exact signature, an exclusive-length runtime map,
  or an inclusive-length four/five-element creation auxdata tuple. Four-element
  creation tuples match only exactly. For a five-element tuple, only the
  integrity value may differ; runtime/data/immutable sizes and the compiler
  identity must agree. CBOR with duplicate keys, indefinite values, excessive
  nesting or collection sizes, the wrong top-level type, invalid lengths, or
  trailing data is not metadata. Bytecode without a recognized valid footer
  can match only exactly.
- `creation_bytecode` is the compiler creation program, excluding externally
  supplied constructor arguments. Native and compatibility handlers resolve
  the canonical creation input from PostgreSQL, then must prove and strip the
  exact caller-declared argument suffix before submission. Neither HTTP
  boundary accepts a caller-selected creation program. Constructor arguments
  remain durable provenance but are not appended a second time by the matcher.
  A creation or runtime mismatch makes the aggregate result a mismatch;
  otherwise any metadata-only side makes the aggregate result metadata-only.

## Consequences

Idempotency no longer conflates distinct source or compiler requests for one
deployed contract. Public jobs cannot be consumed by a process compiler even
when API and worker configuration differ. Crashed and reorged attempts reach a
bounded, auditable terminal state, and published artifacts remain traceable to
their exact request and compiler digest.

This keeps `metadata_only` narrow enough to be publication evidence without
confusing compiler placeholders or deployment values with source metadata.
Compiler fixture tests retain the Solidity multi-file/link/immutable cases and
the Vyper auxdata/layout format boundaries so later compiler allowlist changes
cannot silently widen the matcher.

The allowlisted compiler manifest and cache, sandbox resource implementation,
Sourcify interoperability, and public HTTP compatibility remain the
responsibilities of P30-T02, P30-T04, and P40.
