# Current-chain ENS acceptance

This report retains separate acceptance records for current-chain ENS publication
and its later registration-profile extension under ADR-0058. P20-T27, P40-T27
and P50-T81 own the capability; P30-T150/P30-T151 own local and representative
Mainnet acceptance. Recorded results apply to their exact images and scopes, not
to subsequent changes. Retired independent-role ENS evidence remains in Git.

## Local acceptance

Recorded 2026-10-29 UTC (2026-10-30 Asia/Shanghai). The following passed locally
after the rewrite:

- ENS unit tests and the combined PostgreSQL ENS race regressions.
- `make check`, including generation, Go race, lint, source ownership,
  dependency/security checks, documentation, plans and deployment rendering.
- `make test-integration-race` across the application, billing, genesis,
  integration, authentication and verifier packages; additional ENS race
  regressions cover the final numeric-identity correction.
- `make test-schema-e2e` against the fresh production-image baseline.
- `make test-runtime-e2e`: production all/split topologies, real signed v1/v2
  deployments, migration, factory discovery, forward/primary publication,
  reorg, restart, PostgreSQL/RPC recovery, CCIP failure isolation, SSE and load
  checks; the separate local x402 production topologies also passed.
- `make test-preview-contracts`: real Preview contracts and autonomous name
  publication in split/all layouts, including restart.
- `make test-e2e`: 155 browser tests, including published names with incomplete
  coverage, observed identities, expiry, one SSE connection and bilingual mobile
  display. No frontend changes followed that passing run.

Production-image gates were invoked through their documented `-prebuilt`
variants after `make docker-build`, so each group used the same locally built
image. The final runtime, schema and Preview runs used the image recorded below.
This is local development acceptance; it does not claim remote CI or a release
capacity/security/soak result.

Regressions cover attachment-aware cursor rewind, invalidation-watch rollback,
queue latency versus bounded execution/retry deadlines, and exact numeric
chain/coin identities through import, publication and reads.

## Representative Mainnet publication

[Machine-readable evidence](ens-current-chain-mainnet.json) contains the exact
manifest, image, core/log digest, publications, observation expiry, API checks,
independent comparisons, configuration boundaries and coverage state.

- Chain: Ethereum Mainnet, chain ID `1`, authenticated genesis in the report.
- Core block: `26299443`, hash
  `0x7f6690481dbaed5e43b99deb7ee931f2ecbd52adb9abe2876f30a2e0cf0ab3bf`.
- Production image: Linux arm64,
  `sha256:0ea856f949645f05d92708193bf2a2b9cadb1ce4b27baa4e9fa327221df29dec`.
- Protocol artifacts: v1 revision
  `9b034936a42f462fc04bc0a929a419ede5e18d59`, v2 revision
  `4f47cbcc11462fb413196e6b5c0f8090247a6c08`.

A fresh PostgreSQL database was migrated by that image. Its normal `sync` role
stored the real block and receipts/logs through the current-chain RPC pool;
sync then stopped to keep the sampled target fixed. Its `enrich` role registered
the reviewed manifest, discovered core addresses, consumed a bounded operator
import and published results before any name HTTP lookup. API ran separately,
without an RPC credential mount. The independent PublicNode endpoint matched
the stored canonical header and all **766 logs**, as well as the reviewed code
identities at the observation block.

| Sample                  | Publication checked                                                       |
| ----------------------- | ------------------------------------------------------------------------- |
| `vitalik.eth`           | Forward resolution and independent v1 registry/resolver address           |
| Vitalik's address       | Primary name `vitalik.eth`, including reverse/forward agreement           |
| `dataspace.eth`         | Forward resolution and independent v2 ETHRegistry resolver route          |
| `caimpl.eth`            | Additional v2 forward resolution                                          |
| `nick.eth`, `ens.eth`   | Additional forward publications                                           |
| `test.offchaindemo.eth` | CCIP forward publication; independent resolution made one gateway request |

All seven publications match independent RPC resolution at their exact stored
block hash. The public Universal Resolver's active managed implementation and
root also match the configured reviewed implementation/root. The v1 adapter
resolver returned by Universal Resolver is distinct from the legacy registry's
resolver; its final address was separately checked against the legacy resolver.

After stopping enrich, search and batch address-name reads returned the matching
publication IDs, block identities and expiry. An unknown address returned
`not_indexed`. Candidate, job, observation and publication counts stayed
unchanged across these reads. Restarting API and enrich retained all seven
publications and allowed the original immutable API snapshot to be reused.
Public DNS addresses for the CCIP gateway were
pinned through container host configuration; TLS and the production public-IP
policy remained enabled, with no private-network override. RPC credentials were
kept only in a temporary role-scoped runtime secret.

## Coverage and remaining gates

All **seven selected live samples were operator imports**. Event-discovered
names in this live sample: **zero**. The database also independently discovered
429 address candidates from the core block, and recorded one reverse-result
origin. These origins are reported separately and imports do not manufacture
registration events or close history gaps.

The source declares history starting at `22764821`; the bounded database lacks
that history. Coverage therefore remained `not_started` with `coverage_gap`,
while `last_published_block` was `26299443` and the captured resolution backlog
was `324`. The last publication height is not a coverage checkpoint. The
manifest covers the reviewed entrypoints used in this sample; it is not an
exhaustive Mainnet deployment inventory. Neither this report nor local fixtures
prove full historical coverage or arbitrary offchain/wildcard-name enumeration.

The feature and representative-publication gates are separate from deployment
catch-up. P70's security, reference-capacity, release conformance, recovery/soak,
documentation and signed-artifact gates remain open. Independent live-payment
acceptance belongs to P60-T17 and is not evidence supplied by this report. In particular, this bounded run does not prove the 500 RPS reference
capacity target or clear its infrastructure requirement.

## Registration-profile local acceptance

Recorded 2026-10-30. This extension adds independent registration profiles,
canonical known-name search and database-only name details. Unresolved/expired
names remain searchable; two names resolving to one address retain distinct
identities. Profile and resolution expiry/publication are independent. Canonical
withdrawal, immutable snapshots and lease/generation/configuration fences remain.

Pinned v1/v2 fixtures cover registrar/registry ownership, Wrapper ownership and
expiry, authoritative parent/WrapperRegistry paths, premigration/migration,
renewal, regeneration, grace and reregistration. Unreviewed implementations or
permission models never invent a controller. Focused regressions also preserve
retryable proxy-storage failures and exact-job-block premigration fallback.
Bilingual mobile/keyboard tests cover name details, links, copying and expiry.

Targeted ENS/API/query and PostgreSQL race tests, `make test-integration-race`,
generation, Go/Web lint and documentation gates passed. Frontend acceptance
comprised 760 unit and 157 browser cases, with both new mobile cases additionally
checked through keyboard activation. Initial assets stayed within their budgets.

`make test-schema-e2e`, `make test-runtime-e2e` and `make test-preview-contracts`
passed, including independent profile publication followed by public name reads,
restart/recovery and all/split parity. Final prebuilt runs followed
`make docker-build` using Linux arm64 image
`sha256:6d6b6b53162f67801b508ab94388c1e3abee7218f3d550e89d0f8cccec8cf246`.
Review fixes then passed `make check`, the full owned PostgreSQL race runner,
four bilingual ENS browser cases, schema, all/split runtime/local x402 and Preview
with image `sha256:053532cc3d2561b91bc2fa2941831e6fb2e2d458876fbf64f002a04e7f97aaae`.
The subsequent SQL-only cleanup passed API/query race, PostgreSQL ENS/search race,
generation and Go lint checks; it did not rerun those image-bound suites.

These are local development results, not new Mainnet registration-field or
operation-history evidence, remote CI, full history or release acceptance.
The earlier Mainnet samples above do not validate the later profile fields.
