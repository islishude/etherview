# P30 — Contract Verification

Status: `planned`

## Outcome

Users can submit asynchronous Solidity and Vyper verification jobs whose
compiler inputs are reproducible, sandboxed, code-hash-versioned, and optionally
interoperable with Sourcify v2. External metadata access is SSRF-safe.

## References

- [Architecture](../architecture/overview.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P30-T01 | todo | P10, P20 | Verification job/source/compiler/result schema and service boundary | repository tests |
| P30-T02 | todo | P30-T01 | Allowlisted compiler manifests, checksum cache, resource-limited sandbox | tamper/limit tests |
| P30-T03 | todo | P30-T02 | Solidity/Vyper Standard JSON and multi-file exact/metadata-only matching | compiler fixture tests |
| P30-T04 | todo | P30-T01 | Sourcify v2 lookup/import and consent-gated submission | mocked API tests |
| P30-T05 | todo | P20 | Safe HTTPS/IPFS NFT metadata and media proxy | SSRF/content tests |
| P30-T06 | todo | P20 | Configurable name-service resolver and operator labels | resolver/CLI tests |

## Acceptance

- [ ] Verification binds to chain, address, code hash, and applicable block range.
- [ ] Public compilation refuses to start when a production sandbox is not
      enforceable.
- [ ] Compiler downloads are allowlisted and checksum verified; executions have
      no network and bounded resources.
- [ ] Sourcify submission is never implicit.
- [ ] Redirects, DNS rebinding, private networks, oversized content, SVG/HTML,
      and unsafe MIME types are handled without SSRF or XSS.

## Current Blockers

P20 is not complete. Verification work that depends only on P10 may proceed,
but the plan cannot complete until its P20 integration boundary is done.

## Evidence

None yet.
