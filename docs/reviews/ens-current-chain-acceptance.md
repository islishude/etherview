# Current-chain ENS rewrite acceptance

Recorded 2026-10-29 UTC (2026-10-30 Asia/Shanghai). This report belongs to
P30-T150/P30-T151 and ADR-0058. The older
[P30 ENS report](P30-ens-mainnet-acceptance.md) remains historical evidence for
its original architecture and does not close these milestones.

## Local acceptance

The following passed locally after the rewrite:

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

The regressions found and fixed during acceptance include source-cursor rewind
following an orphan sibling repeatedly, rollback of newly learned invalidation
watches, exhaustion of unclaimed work while waiting in the queue, and PostgreSQL
numeric coefficients being mistaken for full chain/coin values. Queue latency
now remains backlog; first claim starts the bounded execution deadline, and
subsequent retries cannot renew it. A coin type of `60` remains `60` through
operator import, publication and reads.

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
catch-up. P70's live-payment, security, reference-capacity, release conformance,
recovery/soak, documentation and signed-artifact gates remain in their owning
plans. In particular, this bounded run does not prove the 500 RPS reference
capacity target or clear its infrastructure requirement.
