# P58 OpenZeppelin Proxy Detection Baseline

Date: 2026-08-08

Scope: the production `proxy@2` stage and its current OpenZeppelin 5.6.1,
ERC-1167, ERC-1967, Transparent, UUPS, and Beacon behavior. This report records
the pre-P58 contract. It does not authorize a behavior change.

## Call graph and consumers

```text
durable proxy@2 job
  -> PostgresProxyProcessor.Process
     -> loadCandidates
        -> receipts, transactions, trace/state-diff output, lifecycle events,
           genesis observations, verification replay targets
     -> acquire one PurposeState endpoint for the immutable block
     -> probe shared verified UUPS implementation targets
     -> rpcProxyDetector.detectBlock
        -> eth_getCode(proxy, blockHash)
        -> exact ERC-1167 runtime parser
           -> optional CREATE/CREATE2 trace authentication for trailing args
           -> eth_getCode(implementation, blockHash)
           -> return immediately
        -> eth_getStorageAt(implementation slot, blockHash)
        -> eth_getStorageAt(beacon slot, blockHash)
        -> eth_getStorageAt(admin slot, blockHash)
        -> load source-authenticated OZ artifact, when published
        -> resolve implementation or beacon path
           -> eth_getCode(beacon, blockHash)
           -> eth_call(beacon.implementation(), blockHash)
           -> eth_getCode(implementation, blockHash)
           -> optional eth_getCode(admin, blockHash)
     -> rpcProxyDetector.detectBeaconBlock for known Beacon candidates
     -> lease-fenced transaction
        -> proxy_observations and generation witness
        -> beacon/UUPS observations and generation witnesses
        -> proxy_detection_evidence
        -> proxy_artifact_resolutions
        -> stage result, journal, and publication

published proxy@2 generation
  -> abi@2 exact dependency and implementation ABI binding
  -> writer-authoritative native proxy detail/history queries
  -> source-authenticated interaction binding and write fencing
  -> Etherscan getsourcecode/getabi proxy projection
  -> generated same-origin client
  -> contract page proxy summary, implementation-as-proxy forms, and management UI
```

Proxy observations are persisted. They are correctness-sensitive inputs to ABI
publication, verification, and authenticated write targets, not presentation-only
metadata. P58 therefore requires an additive observation contract and shadow
comparison before any V2 result becomes authoritative.

## Current fixed-block regression inventory

The focused suite uses immutable `Job.BlockHash` selectors and fixed block
numbers. Its maintained cases include:

| Case | Current expected behavior | Regression location |
|---|---|---|
| Standard ERC-1167 | exact clone; implementation bytes 10 through 29 | `internal/enrich/proxy_test.go` |
| ERC-1167 trailing bytes | rejected until exact OZ initcode/trace evidence exists | `internal/enrich/proxy_test.go`, `proxy_processor_test.go` |
| Oversized immutable args | bounded rejection | `internal/enrich/proxy_test.go`, `proxy_processor_test.go` |
| Canonical clone with empty implementation code | exact clone with empty implementation code hash | `proxy_processor_test.go` block 313 |
| ERC-1967 implementation | generic high-confidence relation when target has code | `proxy_processor_test.go` block 400 |
| Beacon proxy | partial proxy -> beacon -> implementation relation | `proxy_processor_test.go` block 400 |
| Implementation and beacon slots both set | `ambiguous_slots` rejection unless exact artifact evidence can retain a generic shell | `proxy_processor_test.go` |
| Dirty high bytes in a storage word | invalid address rejection | `proxy_processor_test.go` |
| Implementation or beacon with no code | explicit negative evidence unless an exact artifact proves only the shell | `proxy_processor_test.go` |
| Admin slot on an unverified runtime | compatibility evidence only; pattern remains unknown | `proxy_processor_test.go` |
| OZ 5.6.1 Transparent | authenticated runtime immutable is admin authority; slot disagreement is evidence | `proxy_processor_test.go` block 451 |
| OZ 5.6.1 BeaconProxy | authenticated runtime immutable is beacon authority; slot disagreement is evidence | `proxy_processor_test.go` block 452 |
| UUPS implementation | direct fixed-block implementation probes require exact UUID and 5.0.0 interface response | `proxy_test.go`, `proxy_processor_test.go`, `uups_probe_test.go` |
| Malformed/reverting beacon or UUPS calls | strict rejection or negative compatibility evidence; transport failures fail the stage | `proxy_processor_test.go`, `uups_probe_test.go` |
| Changed implementation over history | append-only block-range transition | `proxy_test.go` |
| Reorg/replay/generation fencing | orphan output retained but cannot shadow the published canonical generation | `internal/integration/proxy_*_test.go` |

Baseline command on 2026-08-08:

```text
go test ./internal/enrich ./internal/query ./internal/httpapi \
  -run 'Proxy|EIP1967|EIP1167|UUPS|Beacon' -count=1
```

Result: pass.

## Review findings

Finding ID: P58-OZ-001  
Severity: high  
Location: `internal/enrich/proxy_processor.go`, `proxyDetection` and
`rpcProxyDetector.detect`  
Observed behavior: one private result combines a selected proxy resolution, one
authenticated resolution, or a rejection string. The function returns early for
clones and selects one ERC-1967 path.  
Expected behavior: independent detectors return structured outcomes and a
resolver preserves simultaneous, weaker, and conflicting evidence.  
Impact: a new family requires editing the central branch, and the persisted
generation cannot explain all applicable recognizers.  
Reproduction: inspect the exact-clone return before the three ERC-1967 reads and
the single `proxy` pointer on `proxyDetection`.  
Suggested fix: add a block-scoped detection context, detector interface, outcome
list, and deterministic resolver while adapting the current OZ logic unchanged.  
Required regression test: one address producing multiple detector outcomes must
retain all outcomes and resolve independently of detector registration order.

Finding ID: P58-OZ-002  
Severity: high  
Location: `internal/enrich/proxy_processor.go`, stage error handling and
`internal/query/proxy.go` public projection  
Observed behavior: a transport/state RPC failure aborts the whole proxy stage;
the API can expose stage-level unavailable/failed, while successful per-address
negative evidence has no V2 `unknown`, `inconsistent`, or `candidate` status.  
Expected behavior: transport inability is never converted to not-detected, and
each detector outcome distinguishes unknown, inconsistent, candidate, confirmed,
and not-detected.  
Impact: the current model cannot faithfully expose a broken canonical shell or
an address-local indeterminate result in a successful batch.  
Reproduction: make `eth_getStorageAt` return a transport error; `detectBlock`
returns the error instead of a structured address result.  
Suggested fix: keep fatal endpoint/canonicality failures stage-level, but add
typed detector errors and address-local V2 outcomes for bounded probe failures.  
Required regression test: timeout/revert/malformed results map to their specified
typed outcomes and never to not-detected.

Finding ID: P58-OZ-003  
Severity: medium  
Location: `internal/enrich/proxy_processor.go`, exact ERC-1167 branch  
Observed behavior: an exact ERC-1167 runtime remains exact even when its current
implementation has no code. This behavior is intentionally asserted at block
313.  
Expected behavior: the shell evidence and implementation liveness are distinct;
the result should retain a confirmed shell and a warning or inconsistent state
without pretending the implementation is healthy.  
Impact: UI and downstream callers can see an exact proxy relation without an
explicit warning that delegation cannot currently reach code.  
Reproduction: `TestRPCProxyDetectorRecognizesCanonicalCloneWithoutImplementationCode`.  
Suggested fix: preserve exact shell recognition in the legacy projection during
shadow mode and express implementation liveness in V2 evidence/status.  
Required regression test: exact shell remains identified, implementation code is
empty, V2 is inconsistent, and legacy output is unchanged until cutover.

Finding ID: P58-OZ-004  
Severity: medium  
Location: `internal/enrich/proxy_processor.go`, pre-artifact ERC-1967 reads  
Observed behavior: every non-clone candidate pays three storage reads before the
result is known; recognizers cannot declare a zero-additional-RPC fingerprint
miss.  
Expected behavior: shared runtime code is evaluated by local exact-bytecode
detectors first, and family-specific RPC is conditional on applicability.  
Impact: appending Safe logic directly would either increase every candidate's RPC
cost or create more central branching.  
Reproduction: a normal non-clone candidate issues code plus implementation,
beacon, and admin slot reads.  
Suggested fix: expose memoized context reads and run the Safe detector only after
a local runtime fingerprint hit in bulk mode.  
Required regression test: a non-Safe runtime causes zero Safe-owned storage,
call, log, or trace requests.

Finding ID: P58-OZ-005  
Severity: medium  
Location: `api/openapi.yaml`, proxy enums and `ProxyDetails`  
Observed behavior: the public schema models OZ mechanisms and calls every final
target `implementation`; it has no family, detector version, singleton role,
warning list, or canonical-shell/official-singleton separation.  
Expected behavior: an additive V2 contract carries detector, family, status,
confidence, implementation role/path, evidence, warnings, chain, block, and
detector version.  
Impact: Safe cannot be represented accurately without overloading OZ fields.  
Reproduction: inspect `ProxyMechanism`, `ProxyPattern`, and `ProxyDetails`.  
Suggested fix: add a V2 projection during shadow mode; do not widen management or
interaction authorization based on Safe recognition.  
Required regression test: generated clients preserve legacy fields and decode a
Safe singleton independently from implementation/admin/beacon identities.

## Confirmed invariants

- One state endpoint is acquired per immutable block.
- Code, storage, beacon calls, and UUPS calls use the same EIP-1898 block hash.
- Implementation/beacon slot conflicts are rejected unless exact artifact
  evidence preserves only a generic authenticated shell.
- Storage addresses reject non-zero high 12 bytes.
- Generic admin-slot evidence does not confirm Transparent.
- Source-authenticated OZ 5.6.1 Transparent and Beacon runtime immutables override
  compatibility slots and record disagreement.
- UUPS probes target the implementation, never the proxy, and strictly validate
  the UUID. Revert is negative compatibility evidence; transport failure is not
  silently treated as a non-proxy.
- Exact ERC-1167 parsing is bounded and does not search for delegatecall.
- Published output is lease/generation fenced and reorg-safe.

## PR-2 compatibility boundary

The first framework adapter must reproduce the current legacy rows and API
projection byte-for-byte for the fixed inventory above. New V2 outcomes may be
persisted in shadow form, but they must not authorize ABI publication,
implementation-as-proxy writes, ProxyAdmin writes, or Beacon management until a
separate accepted contract change defines those fences.
