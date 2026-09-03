# Plan Catalog

The root [implementation plan](../../PLAN.md) owns current status and release
dependencies. This catalog groups the child plans by responsibility.

## Core chain and data

- [P00 — Foundation](P00-foundation.md)
- [P10 — Indexing](P10-indexing.md)
- [P20 — Enrichment](P20-enrichment.md)

## Contract platform and runtime

- [P30 — Contract Platform & Runtime Operations](P30-contract-verification.md)
  is the canonical plan for contract verification, contract intelligence, and
  shared runtime/operations responsibilities.

## Public API and Web capabilities

- [P40 — API](P40-api.md)
- [P50 — Embedded Web](P50-web.md)
- [P64 — NFT Metadata Web](P64-nft-metadata-web.md)
- [P67 — ENS Primary Names](P67-ens-primary-names.md)
- [P74 — Etherscan V2 Read Expansion](P74-etherscan-v2-read-expansion.md)
- [P76 — ERC-4337 UserOperation Browsing](P76-erc4337-user-operations.md)

## Identity and billing

- [P65 — User Authentication](P65-user-auth.md)
- [P73 — Prepaid API Billing and x402 Top-ups](P73-prepaid-api-billing.md)

## Hardening and release

- [P68 — Runtime and Architecture Hardening](P68-runtime-architecture-hardening.md)
- [P75 — Runtime and Performance Hardening](P75-runtime-performance-hardening.md)
- [P70 — Release](P70-release.md)

## Retired plans

- P55 — OpenZeppelin Proxy Interaction: superseded by the completed P20/P30/P40/P50
  implementation chain; P55-T01 maps to P20-T13, P55-T02 to P30-T15/P40-T11,
  and P55-T03/P55-T04 to P50-T14. No active work remains.
- P66 — accountless x402 request billing: P66-T01 through P66-T07 are historical
  accountless implementation work replaced by P73/ADR-0044; P66-T08 is
  replaced by P73-T08. Historical request-payment rows remain audit-only and
  never create prepaid credit.

Retired identifiers are not reused. Historical migration, test, and review
labels may continue to use their original identifiers as provenance.
