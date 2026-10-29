# P30-T148 Mainnet ENS acceptance

Status: **in progress**. On 2026-10-29, real Mainnet read-only checks passed
deployment/activation, representative resolution, bounded database collection,
independent event reconciliation and restart recovery. Full historical coverage
and reconciliation of database-published names remain open. This report does
not close P30-T148 or the P70 live-source gate.

The [machine-readable evidence](P30-ens-mainnet-acceptance.json) retains exact
identities, artifact hashes, reviewed probe manifests, samples and limitations.
The supplied RPC URL is omitted. No transactions were sent.

## Environment and identities

The test used PostgreSQL 18 and the actual `serve --roles=ens` production image
on local ARM64, with separate v1/v2 sources and persistent cursors. Image:
`sha256:8ac3d10c15d3358f3bd26551ccc323188eab42ed0473bc834ee34e0dd6619001`.
This is not a deployed service or new remote CI result.

Official deployment artifacts were pinned to `ens-contracts`
`9b034936a42f462fc04bc0a929a419ede5e18d59` and `ens-contracts-v2`
`4f47cbcc11462fb413196e6b5c0f8090247a6c08`. The primary archive RPC and
[PublicNode](https://ethereum-rpc.publicnode.com) agreed on Mainnet chain ID,
genesis and finalized block **26297715**,
`0x409c195c73590ef2c622ca5dffa4e944b804e63bc1bd1718c3594450d4df5e24`
(2026-10-29 10:11:35 UTC). Samples below use that exact block hash.

## Results

| Check                           | Evidence                                                                                                                                                                                                                                                                                            |
| ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| v2 deployments                  | 14 pinned deployments matched artifact runtime with immutable locations accounted for; deployment block hashes agreed across RPCs. Primary archive RPC also returned deployment-height code.                                                                                                        |
| v2 activation                   | Public Universal Resolver `0xeEeEEEeE14D718C2B47D9923Deab1335E144EeEe` points through managed proxy `0xcffd2ae680cd110d28739a70922636b5937b022b` to v2 `0x06fdd07f007adac667177b46ab2eb9a75132cfd5`; `isENSv2()` is true and the root is `0x2553c09be6447beffdb86fa622ad41fb8f77a91e` on both RPCs. |
| Live names                      | Both RPCs agree on forward and reverse resolution for `vitalik.eth`, `nick.eth` and `ens.eth`. The address resolved from `ens.eth` has primary name `wallet.ensdao.eth`. v2 reports `nick.eth` registered with a nonzero owner; reserved records are recorded separately.                           |
| Actual registrations            | Five decoded recent `LabelRegistered` samples retain transaction/block/log identities; these establish actual v2 activity separately from deployment.                                                                                                                                               |
| Recent event comparison         | Blocks 26297460–26297715: all 1,607 logs for the declared root/ETH registry/registrar filter match across RPCs, digest `0x695fb4078ee018ceda601f51e94f8a7ebd0b39a222624cfc00ba6e5e52f5122b`. This is an RPC comparison, not evidence those blocks were indexed in the test database.                |
| v1 historical state             | Primary archive RPC resolved `vitalik.eth` at block 26100000; the independent RPC matched its block hash but does not serve the required historical state. Seven v1 contract identities were also sampled.                                                                                          |
| Database v2 collection          | 368 consecutive blocks, 26105299–26105666, nine retained logs, no parent gaps.                                                                                                                                                                                                                      |
| Database v1 collection          | 592 consecutive blocks, 22764821–22765412, two retained logs, no parent gaps.                                                                                                                                                                                                                       |
| Independent database comparison | All headers and filtered logs match in three exact 16-block windows: 26105299–26105314 (zero logs), 26105621–26105636 (five logs) and 22764821–22764836 (two logs). Historical log requests used `blockHash`; range requests were rejected by the independent provider.                             |
| Restart                         | Both production ENS processes restarted against the same database and advanced the same source cursors without gaps. A separate mid-round cancellation committed no partial round; a new collector resumed with the next eight blocks.                                                              |
| Live CCIP                       | The production resolver and HTTP gateway resolved the official `test.offchaindemo.eth` sample through both RPCs, with one gateway request each, to `0x779981590e7ccc0cfae8040ce7151324747cdb97`. See the DNS qualification below.                                                                   |

The database sources were still behind finalized block **26297971**,
`0xca175264dc91e2c977460b5fe4d91174a59f7551b212afff445bc47c43694785`,
when stopped. Their selected bootstrap windows contained no name candidates;
no database-published name reconciliation is claimed. The retained manifests are
fixed-contract collection probes, not complete production manifests.

## Findings and validation

The initial collector inherited the eight-second individual RPC timeout for a
whole multi-block round and committed zero blocks. The repaired default keeps
individual HTTP/resolution requests bounded and gives an eight-block collection
round one minute. The same real source then committed two rounds around a
deliberate cancellation. A delayed local RPC regression protects this boundary.

Whole-block v2 discovery also encountered responses above the default 1 MiB
cap. The test used the existing `ens.max_response_bytes: 8388608` setting and
resumed the stalled cursor. The runbook records this configuration; global
defaults and upper bounds were not relaxed.

The host's DNS returned an address in `198.18.0.0/15` for the CCIP gateway, which
the production client correctly rejected. Cloudflare and Google DNS-over-HTTPS
agreed on public A records `104.21.82.76` and `172.67.198.91`. A test-only
`metadata.Resolver` supplied those records; production TLS verification,
network policy, redirect rejection and body limits remained active. This proves
live CCIP with that DNS setup, not the default host DNS path. No DNS override
was added to the application.

Validation passed:

- Collector PostgreSQL race regressions, including
  `TestENSCollectorRoundOutlivesIndividualRequestBudget`.
- Real Mainnet collector interruption/recovery and the independent comparisons
  retained in the evidence file.
- `make check` and production-image `TestProductionComposeRuntimeE2E` in both
  monolith and distributed layouts.

## Remaining acceptance

Complete reviewed source manifests and contiguous historical coverage through a
pinned finalized target, then independently reconcile the resulting database
publications. A complete, verified baseline would require its own design and
validation; importing a few names or advancing a cursor cannot replace missing
history. These conditions do not require an online deployment, but the current
bounded test does not satisfy them. Do not infer full coverage, publication or
deployment readiness from the successful samples above.

Independent deployment-height state was unavailable (`rpc_error_4444`); retain
that distinction from the primary archive checks and independent current-state,
header and log comparisons. Capacity, long-running catch-up and rollout remain
unmeasured.
