# P70 — Release

Status: `blocked`

## Outcome

Etherview v1.0.0 has conformance, migration, security, performance, deployment,
and user/operator evidence sufficient for a production public release.

## References

- [Architecture](../architecture/overview.md)
- [ADR-0018](../decisions/ADR-0018-api-read-replica-routing.md)
- [ADR-0019: Authenticated genesis state import](../decisions/ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0020: SIWE user sessions](../decisions/ADR-0020-siwe-user-sessions.md)
- [ADR-0044: prepaid API billing and x402 top-ups](../decisions/ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [ADR-0022: Go-ethereum type and raw RPC ownership](../decisions/ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0025: Historical execution analytics](../decisions/ADR-0025-historical-execution-analytics.md)
- [ADR-0026: Current capability status and numeric canonical tips](../decisions/ADR-0026-current-capability-status-and-numeric-canonical-tips.md)
- [ADR-0027: Process-native API TLS](../decisions/ADR-0027-process-native-api-tls.md)
- [ADR-0028: Durable proxy verification and real Hardhat E2E](../decisions/ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [ADR-0031: API-owned architecture-neutral solc-js executor](../decisions/ADR-0031-api-owned-solc-js-executor.md)
- [ADR-0037: Rebuildable persistent solc-js artifact cache](../decisions/ADR-0037-persistent-solcjs-artifact-cache.md)
- [ADR-0040: SEA-packaged solc-js executor](../decisions/ADR-0040-sea-packaged-solcjs-executor.md)
- [ADR-0046: authoritative ERC-20 holder reconciliation](../decisions/ADR-0046-authoritative-erc20-holder-reconciliation.md)
- [Testing](../testing.md)

## Work Items

| ID | Status | Depends on | Deliverable | Verification |
|---|---|---|---|---|
| P70-T01 | blocked | P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77, P78 | Execution/API/token/holder/proxy/verification/authentication/billing conformance matrix | conformance suite |
| P70-T02 | blocked | P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77, P78 | Threat model, security audit, dependency, compiler, session, holder reconciliation, and payment supply-chain review | security gates |
| P70-T03 | blocked | P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77, P78 | Monolith/split E2E, migration/rollback, outage, reorg, holder, payment, and soak suite | release CI |
| P70-T04 | blocked | P30, P75, P77 | 500 RPS reference capacity report including holder reads and concurrent reconciliation, plus tuning guide | load report |
| P70-T05 | blocked | P00, P10, P20, P30, P40, P50, P64, P65, P67, P68, P73, P74, P75, P77, P78 | User/operator/API/authentication/billing/holder/runbook/upgrade documentation | doc review and link check |
| P70-T06 | todo | P70-T01, P70-T02, P70-T03, P70-T04, P70-T05, P70-T08, P70-T09 | SBOM, checksums, signed multi-arch artifacts and v1.0.0 release | release verification |
| P70-T07 | done | P30 | Database read/write pool split configuration, deployment wiring, and capacity guidance | helm config/schema tests |
| P70-T08 | done | P10, P20, P40, P50, P30 | Authenticated local/remote genesis account state, predeploy enrichment, native API, and block-zero UI | root, persistence, API, browser, security, and split-role tests |
| P70-T09 | done | P10, P20, P40, P50, P30 | Replace duplicative Ethereum RPC/domain types and codecs with reviewed go-ethereum equivalents while retaining explicit hostile-input, persistence, and public-contract adapters | focused compatibility, integration, generation, security, license, and common gates |
| P70-T10 | done | P30 | Configurable process log level and JSON/text output across file, environment, CLI, and deployment surfaces | config, CLI, observability, Compose, and Helm tests |
| P70-T14 | done | P10, P30 | Add reporter-fenced rate-limited sync progress and durable worker outcome logs | focused logging, race, deployment, and Preview tests |
| P70-T16 | done | P20, P40, P50, P30 | Etherscan-inspired execution analytics with `stats@3`, reorg-safe hourly rollups, native history APIs, and overview/detail charts | stage, migration, API, browser, reorg, load, and production Compose E2E |
| P70-T17 | done | P20, P40, P50, P30 | Report current Trace and historical-state capability accurately and select exact state/ABI observations by numeric block height | PostgreSQL, API, browser, ABI, reorg, and Preview tests |
| P70-T20 | done | P30 | Optional native TLS for API listeners with Preview-local Compose and Helm certificate delivery | config, HTTPS service, Preview Compose, Helm, security, and common gates |
| P70-T21 | done | P30-T11, P40-T06 | Hardhat 3 Etherscan-provider source-verification submission and GET status-polling compatibility | handler goldens, pinned Hardhat 3 provider test, security, documentation, and common gates |
| P70-T27 | done | P20-T03, P30-T11, P40-T06, P30-T35, P70-T21 | Durable proxy-verification compatibility plus real Hardhat 3 production-path E2E | handler, PostgreSQL, migration, monolith/split Compose, and real compiler gates |
| P70-T29 | done | P30-T14, P30 | Replace the platform-bound compiler-runner and Vyper surface with an API-owned architecture-neutral solc-js executor | ADR, migration, OpenAPI, subprocess, Compose, Helm, Preview, and multi-architecture compiler gates |
| P70-T31 | done | P30-T11, P40-T06, P30-T35, P70-T29, P30, P70-T27 | Add an independent Foundry production-path source-verification E2E without replacing the Hardhat 3 proxy gate | offline compiler, strict Etherscan V2, monolith/split provenance parity, and native AMD64/ARM64 CI gates |
| P70-T34 | done | P30-T33, P30-T35, P50, P30 | Make Docker Compose `--wait` the sole Preview health owner through an application-native healthcheck command and remove the redundant custom Preview checker | CLI, Compose-render, repository-surface, and live Preview lifecycle regressions |
| P70-T36 | done | P70-T08, P40-T10, P50-T12 | Mark authenticated genesis allocation addresses as the explicit address-origin source | OpenAPI, generated contracts, query, API, frontend, and common gates |
| P70-T38 | done | P30, P70-T29 | Persist checksum-addressed solc-js artifacts across application replacement without changing compiler trust or catalog-freshness semantics | cache concurrency, Compose/Helm, image, real compiler restart, and common gates |
| P70-T39 | done | P70-T38 | Serialize shared solc-js cache installation with writer PostgreSQL advisory locks and stable file snapshots | cache concurrency, PostgreSQL integration, persistence, and common gates |
| P70-T40 | done | P70-T29, P70-T38, P70-T39 | Package the trusted solc-js protocol as one Node SEA with generated target-rootfs ELF closure and one relocatable executor path | ADR, manifest, subprocess, config, image, cache persistence, and real compiler gates |
| P70-T47 | done | P70-T09, P50-T66 | Expose exact optional execution-header slot number in block API and bilingual block overview | query boundary, frontend, generation, browser and common gates |
| P70-T48 | done | P70-T09 | Amsterdam header, gas accounting, protocol log and contract-size compatibility with versioned enrichment | protocol, PostgreSQL, race and common gates |
| P70-T49 | done | P70-T09 | Lease-fenced EIP-7708 native transfer index with snapshot-paginated transaction/address APIs and bilingual browsing | publication, reorg, API, browser and runtime parity |
| P70-T50 | done | P70-T09 | Restore Amsterdam Preview and prove production topology and deployment acceptance | Preview, schema, runtime, Hardhat, Foundry and common gates |
| P70-T52 | done | P70-T49 | Decode EIP-7708 transaction logs from published native transfers and label the system address throughout the Web | protocol, PostgreSQL, API, bilingual browser and common gates |

## Acceptance

- [ ] Every required feature plan and root release gate is complete with evidence.
- [ ] Clean deployment, upgrade, rollback, backup/restore and repair are reproducible.
- [ ] Security review has no unresolved critical/high finding.
- [ ] Reference capacity satisfies the documented dataset, hardware and load thresholds.
- [ ] Reproducible signed artifacts include checksums and an SBOM.
- [x] Authenticated Genesis, analytics, TLS and compiler production paths have feature acceptance.
- [x] Amsterdam headers, gas accounting, native transfers and protocol logs have local acceptance.

## Current Blockers

The superseded accountless request-payment path is replaced by P73. P73-T08
still needs operator-provided testnet funding, payer credentials, a compatible staging facilitator and top-up
surface, the matching writer and independent RPC endpoint, and the deployed
image/build digest. Preserved EIP-3009 and Permit2 reports must cover SIWE,
top-up settlement, account credit, API-key debit, and writer/chain
reconciliation.

P70-T01, P70-T02, P70-T03, and P70-T05 are blocked by P73. P77's holder
implementation, conformance, security, topology, documentation, license, and
common-gate evidence is complete. These P70 items become claimable after the
P73-T08 live reports are recorded and P73 is `done`; local Anvil evidence does
not substitute for the live-payment gate.

P70-T04 is blocked on an operator-provisioned reference environment for the
final clean revision and image digest: at least two failure domains with room
for the documented 9-to-18-pod topology, HA PostgreSQL sized for its connection
budget, a named representative chain snapshot with cardinalities, healthy
purpose-specific RPC behavior, and independent timestamp-aligned resource
monitoring. Running the exact 500 RPS/30-minute target there must include
representative high-cardinality native holder pages, `tokenholderlist`,
`tokenholdercount`, and concurrent incremental holder reconciliation while
preserving the existing latency, error-rate, and lag thresholds. Preserving
the load report, resource peaks, monitoring data, and tuning guide clears the
blocker.

P70-T06 remains dependency-gated until P70-T01 through P70-T05, P70-T08
and P70-T09 are complete. No release is authorized by documentation cleanup.

## Evidence

Retained completed milestones: P70-T07, P70-T08, P70-T09, P70-T10, P70-T14, P70-T16,
P70-T17, P70-T20, P70-T21, P70-T27, P70-T29, P70-T31, P70-T34, P70-T36, P70-T38,
P70-T39, P70-T40, P70-T47, P70-T48, P70-T49, P70-T50, P70-T52.

Completed capability milestones passed their focused tests and applicable
integration/browser/production gates at the delivery revisions. They cover
read replicas, authenticated Genesis, Ethereum type boundaries, logging,
analytics, process TLS, proxy/source verification, persistent compiler cache
and the SEA runtime. These results do not close the remaining release gates.

Amsterdam header/gas/native-transfer support passed Go unit/race,
PostgreSQL integration/race, generated contracts, embedded browser, schema,
runtime/x402, Hardhat, Foundry and real-Geth Preview gates locally. The Preview
gate exercised exact native transfer amounts in both deployment layouts.
Protocol-log projection additionally passed focused publication/corruption/
reorg race cases, 391 Web tests and 49 browser scenarios at its revision.
Local host workarounds and exact commands remain in the historical record;
no new remote, cross-architecture release or production deployment is claimed.

[Detailed completion record](https://github.com/islishude/etherview/blob/e665a0b9b5dcee02e6d90d3a2224a76031cbda3d/docs/plans/P70-release.md#evidence).
These are historical results, not tests rerun by this plan cleanup.
