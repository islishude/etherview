# ADR-0057: Preview Rundler

Status: accepted

## Context

Preview needs a real ERC-4337 v0.9 submission and safe-validation path on its
private Geth/Lighthouse chain. The previous Alto integration required a local
simulation-result decoding patch and additional simulation predeploys.

## Decision

Use the pinned official Rundler release as the independent Preview Bundler.
Review both Linux architecture archive checksums in the release manifest.
Keep canonical EntryPoint v0.9 and SimpleAccount identities; remove Alto-only
simulation predeploys and the unused utility account. Rundler supplies its
simulation code through RPC state overrides, not deployment transactions.

Before starting Rundler, verify chain ID and exact EntryPoint code at block zero
against the generated allocation. Enable only v0.9 and the standard eth RPC
namespace, without debug/admin methods, unsafe validation or unsafe fallback.
Pass only the public development executor key to the Bundler process. It has no
public port outside the existing HTTPS gateway and does not gate core readiness.
Preserve canonical PostgreSQL indexing independently of the Bundler mempool.

Generated allocation and Bundler inputs participate in network fingerprint
validation. Changed genesis requires explicit fresh Preview initialization;
startup never rewrites existing chain state. The canonical CREATE2 construction
inputs retain their pinned source provenance independently of the Bundler image.

## Acceptance

Reproduce genesis and verify code/storage at block zero. Run real Geth/Lighthouse
submission, gas estimation, deployment, execution/revert, signature/prefund
rejection, duplicate submission, standard RPC and disabled debug methods.
Preserve ENS, API/browser, restart and split/all-role parity, and rerun the
Preview metadata, consensus and x402 gates. Local acceptance does not establish
public-chain support or close the live-payment release gates.
