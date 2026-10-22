# P79 — Optional Consensus and Blob Archives

Status: `done`

## Outcome

Optional Beacon REST enrichment, consensus-first slot display, durable consensus
overview, and independently enabled, immutable S3 blob archives. Execution
canonicality, finality and core readiness remain execution-owned.
Local Preview enables both capabilities on a real Lighthouse PoS network.

## References

- [ADR-0055](../decisions/ADR-0055-consensus-and-blob-archives.md)
- [Architecture](../architecture/overview.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P79-T01 | done | P10, P20 | Beacon identity, bounded ingestion, durable overview and consensus stage | protocol, PostgreSQL and lease/reorg regressions |
| P79-T02 | done | P79-T01 | KZG-authenticated immutable blob archive and replay | object-store, corruption and crash regressions |
| P79-T03 | done | P79-T01, P79-T02 | Generated API, consensus-first slots and bilingual browsing | generation and browser tests |
| P79-T04 | done | P79-T03 | Role-scoped deployment, documentation and acceptance | common, integration/race, RustFS, schema and runtime gates |
| P79-T05 | done | P79-T04 | Real Lighthouse PoS Preview and durable blob archive deployment | Preview consensus/metadata, restart, permission and role-parity gates |

## Acceptance

- [x] Disabled features issue no upstream requests.
- [x] Consensus slots take precedence with explicit execution-header fallback.
- [x] All published observations bind exact execution and Beacon identities.
- [x] Blob bytes live only in immutable, independently read-back-verified objects.
- [x] Generation and lease fencing prevents stale publication.
- [x] Monolith and split roles preserve core readiness during upstream outages.
- [x] Applicable common and production gates pass.

- [x] Real Lighthouse Preview passes both roles, blob readback, recovery and finality.

## Current Blockers

Preview storage-enforced conditional-write IAM permissions are deferred by
operator decision until RustFS 1.0.4 is evaluated. RustFS 1.0.1 and 1.0.3 lack
the required policy key; application conditional writes and API read-only access
remain active. Clearing this requires a fixed release containing upstream
[PR #8615](https://github.com/rustfs/rustfs/pull/8615) and real-container rejection
of unconditional writes and overwrites.
P70/P73 release blockers remain independent.

## Evidence

- P79-T01: protocol and PostgreSQL regressions authenticate Bellatrix through
  Gloas bodies against pinned roots, including private presets and independent
  geth root checks; reject forged execution associations and network changes;
  preserve unavailable worker outcomes, slot precedence, leases, bounded initial
  discovery and generation-fenced catch-up after disabled-period reorgs.
- P79-T02: archive regressions cover duplicate references, conditional writes,
  independent readback, upload-before-publication failures, retained replay with
  upstream history pruned, generation changes during readback, and missing-object
  repair. Replayed observations still enforce the fixed network identity.
- P79-T03: generated contracts, HTTP/query and frontend tests pass; all 54 browser
  scenarios pass, including consensus slots, archive status, on-demand preview
  and download, mobile layout and freshness.
- P79-T04: after the review fixes, `make check`, `make source-check`,
  `make test-integration-race`, `make test-e2e`, `make test-rustfs`,
  `make test-schema-e2e` and `make test-runtime-e2e` pass. Production-image
  monolith and split roles preserve core readiness during optional endpoint
  outages and pass restart and dependency-recovery checks.
- P79-T05: Geth v1.17.8 / Lighthouse v8.3.0 pass real PoS and finalized-epoch
  advancement, locally signed blob transactions, independent Beacon/S3/API
  byte and KZG/hash checks, read-only denial, outages, retained network/object
  identity and split/all-role publication. `make test-preview-metadata`,
  `make test-preview-consensus`, `make test-rustfs`, `make test-runtime-e2e`
  and `make check` pass locally. The local license scanner requires an explicit
  current `GOROOT`; no gate or license rule was relaxed.

These are local protocol-fixture and container results, including real private
Beacon-node acceptance in T05. Public-network historical retention and production
backup/restore are not claimed; operators must verify those procedures.
P70/P73 blockers remain unchanged.
