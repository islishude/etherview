# ADR Catalog

Accepted decisions remain individually authoritative for their own public,
persistent, security, and runtime boundaries. This catalog groups them without
merging distinct invariants.

## Core chain, persistence, and runtime correctness

- [ADR-0001 — Modular Roles and PostgreSQL Truth](ADR-0001-modular-roles-and-postgresql-truth.md)
- [ADR-0002 — Identity-Bound Repair and Explicit Reindex](ADR-0002-identity-bound-repair-and-explicit-reindex.md)
- [ADR-0004 — Durable Runtime Status and Event Replay](ADR-0004-durable-runtime-status-and-events.md)
- [ADR-0006 — Durable Canonical Coverage and Live-Head Priority](ADR-0006-durable-canonical-coverage-and-live-priority.md)
- [ADR-0007 — Block-Scoped Derived Canonicality Journals](ADR-0007-block-scoped-derived-canonicality-journals.md)
- [ADR-0008 — Versioned Token Observations and Exact State Reconciliation](ADR-0008-versioned-token-observations-and-exact-state-reconciliation.md)
- [ADR-0012 — Lease-Fenced Derived Publication](ADR-0012-lease-fenced-derived-publication.md)
- [ADR-0015 — Disposable Runtime Accelerators](ADR-0015-disposable-runtime-accelerators.md)
- [ADR-0018 — API Read-Replica Routing](ADR-0018-api-read-replica-routing.md)
- [ADR-0019 — Authenticated Genesis State Import](ADR-0019-authenticated-genesis-state-import.md)
- [ADR-0022 — Go-Ethereum Type and Raw RPC Ownership](ADR-0022-go-ethereum-type-and-raw-rpc-ownership.md)
- [ADR-0023 — Exact Transaction State Differences](ADR-0023-exact-transaction-state-differences.md)
- [ADR-0025 — Historical Execution Analytics](ADR-0025-historical-execution-analytics.md)
- [ADR-0026 — Current Capability Status and Numeric Canonical Tips](ADR-0026-current-capability-status-and-numeric-canonical-tips.md)
- [ADR-0036 — Endpoint-Scoped Mempool Replacements](ADR-0036-endpoint-scoped-mempool-replacements.md)

## Contract verification and intelligence

- [ADR-0009 — Block-Bound ABI Provenance](ADR-0009-block-bound-abi-provenance.md)
- [ADR-0010 — Block-Pinned Proxy Stage and ABI Dependency](ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md)
- [ADR-0024 — Verifier v2 Workflow](ADR-0024-verifier-v2-workflow.md)
- [ADR-0028 — Proxy Verification and Hardhat E2E](ADR-0028-proxy-verification-and-hardhat-e2e.md)
- [ADR-0031 — API-Owned solc-js Executor](ADR-0031-api-owned-solc-js-executor.md)
- [ADR-0032 — Evidence-Based Proxy Detection](ADR-0032-evidence-based-proxy-detection.md)
- [ADR-0033 — Trace-Bound Log Attribution and Call Decoding](ADR-0033-trace-bound-log-attribution-and-call-decoding.md)
- [ADR-0034 — EIP-7702 Execution Identity and Constructor Decoding](ADR-0034-eip7702-execution-identity-and-constructor-decoding.md)
- [ADR-0037 — Persistent solc-js Artifact Cache](ADR-0037-persistent-solcjs-artifact-cache.md)
- [ADR-0038 — Selector-Scoped ERC-2535 Diamond Identity](ADR-0038-selector-scoped-erc2535-diamond-identity.md)
- [ADR-0039 — Pinned Geas Verification Executor](ADR-0039-pinned-geas-verification-executor.md)
- [ADR-0040 — SEA-Packaged solc-js Executor](ADR-0040-sea-packaged-solcjs-executor.md)
- [ADR-0042 — Solady Legacy CWIA Identity](ADR-0042-solady-legacy-cwia-identity.md)
- [ADR-0043 — Factory-Derived Verification Provenance](ADR-0043-factory-derived-verification-provenance.md)

## API, Web, external boundaries, and identity

- [ADR-0003 — Spec-First API and Canonical Public Identifiers](ADR-0003-spec-first-api-and-canonical-public-identifiers.md)
- [ADR-0005 — Safe NFT Metadata and Media Boundary](ADR-0005-safe-nft-metadata-and-media-boundary.md)
- [ADR-0011 — Snapshot Search, Statistics, and Bounded Adapters](ADR-0011-snapshot-search-stats-and-bounded-adapters.md)
- [ADR-0013 — Embedded SPA Serving and Browser Security](ADR-0013-embedded-spa-serving-and-browser-security.md)
- [ADR-0020 — SIWE User Sessions](ADR-0020-siwe-user-sessions.md)
- [ADR-0027 — Process-Native API TLS](ADR-0027-process-native-api-tls.md)
- [ADR-0035 — User-Owned Scoped API Keys](ADR-0035-user-owned-scoped-api-keys.md)
- [ADR-0041 — Snapshot-Stable ENS Primary Names](ADR-0041-snapshot-stable-ens-primary-names.md)
- [ADR-0044 — Prepaid API Billing with x402 Top-Ups](ADR-0044-prepaid-api-billing-and-x402-topups.md)
- [ADR-0045 — Canonical ERC-4337 UserOperation Index](ADR-0045-erc4337-useroperation-index.md)
- [ADR-0046 — Authoritative ERC-20 Holder Reconciliation](ADR-0046-authoritative-erc20-holder-reconciliation.md)

## Retired decisions

- ADR-0014 — durable verification identity and publication: replaced by the
  maintained verifier-v2, compiler-executor, and publication boundaries in
  ADR-0024 and ADR-0031.
- ADR-0016 — compiler supply chain and sandbox: replaced by ADR-0024, ADR-0031,
  and ADR-0040.
- ADR-0017 — Sourcify interoperability boundary: replaced by ADR-0024.
- ADR-0021 — accountless x402 request billing: replaced by ADR-0044.
- ADR-0029 — daemonless remote compiler runner: replaced by ADR-0031.
- ADR-0030 — API-integrated verifier and self-contained compiler runner:
  replaced by ADR-0031.

Retired ADR identifiers are not reused. The current ADR files remain the
authoritative decisions; this page is only their navigation and replacement
register.
