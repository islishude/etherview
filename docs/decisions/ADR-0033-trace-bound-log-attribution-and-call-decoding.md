# ADR-0033: Trace-Bound Log Attribution and Call Decoding

Status: accepted

## Context

A receipt log names its emitter and storage context, not necessarily the
runtime code that defined the event. In `DELEGATECALL` and `CALLCODE`, decoding
only by the emitter can therefore select a proxy or diamond ABI instead of the
executed implementation or facet. Normalized Trace also retained raw calldata
and output without distinguishing a frame's own failure from rollback inherited
from an ancestor, so successful return data and revert payloads could not be
projected reliably.

## Decision

- `trace@2` requests `callTracer` with `withLog=true` from one block-pinned
  endpoint. Every captured log must form a complete one-to-one match with the
  persisted receipt logs by global index, emitter, topics, and data. Exact
  matches persist transaction hash, log index, trace path, call type, and
  execution code address in `trace_log_attributions`.
- The receipt emitter remains the public log address. For `DELEGATECALL` and
  `CALLCODE`, frame `to` is the execution/ABI address; the same rule records
  ordinary calls, roots, and construction frames. `STATICCALL` cannot own a
  log. Partial, duplicate, impossible, or contradictory results are permanent
  provider-data failures.
- Only JSON-RPC `-32602` for the log-enabled tracer config downgrades to ordinary
  `callTracer`. A same-endpoint `trace_transaction` fallback remains available.
  Either fallback may publish its complete call tree, but logs without exact
  attribution decode conservatively from the emitter, published historical
  proxy evidence, or a same-code verified artifact.
- `normalized_traces.direct_reverted` records the current frame's failure;
  `reverted` also includes ancestor rollback. A locally successful child keeps
  successful output semantics even if an ancestor later fails. Direct failures
  use an independent custom/builtin revert projection.
- One selected ABI candidate binds function inputs and successful outputs.
  Candidate provenance includes source address and code hash. Missing output
  declarations, explicit empty output lists, malformed output, ambiguity,
  budget exhaustion, and non-applicability are distinct states. No constructor
  argument inference or proxy identity/authority inference is added.
- ABI `receive` and `fallback` entries are selectorless callable identities,
  not unknown function selectors. Empty calldata selects a declared `receive`
  entry first and otherwise a declared `fallback`; incomplete or unmatched
  selectors select `fallback` only when the exact historical ABI declares it.
  The same exact address/code-hash provenance and bounded candidate rules apply.
- A call-like frame whose exact execution resolution is `empty` has no ABI
  function identity. Its decoding status is `not_applicable`, including an
  ordinary native transfer to an EOA, and it does not trigger ABI lookup or an
  unknown-selector warning.
- Public Trace reads attach persisted-first, bounded PostgreSQL-only ABI
  projection inside one repeatable-read snapshot. The schema-v2 S3 object
  contains only normalized raw call frames; it never contains ABI projection.
  Thus late verification changes the read projection without mutating the old
  trace generation or invalidating object storage.

## Consequences

ADR-0034 supersedes the frame-`to` convention for `trace@3`: public `to` is the
call/storage context and a separate execution identity names loaded code. Its
EIP-7702 and constructor rules otherwise preserve this ADR's attribution,
revert, bounded fallback, and cache boundaries.

Log decoding can name both the immutable receipt emitter and the exact executed
code when evidence exists. A provider that omits supported log detail degrades
availability rather than fabricating ownership, while hostile partial data
cannot poison durable attribution. `trace@1` history is not interpreted as
`trace@2`; operators rebuild bounded ranges with `reindex --stage trace`, after
which the existing proxy and ABI replay chain refreshes dependent projections.
