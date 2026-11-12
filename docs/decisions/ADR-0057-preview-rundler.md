# ADR-0057: Preview Rundler

Status: accepted

## Context

Preview needs a real ERC-4337 v0.9 submission and safe-validation path on its
private Reth/Lighthouse chain. The previous Alto integration required a local
simulation-result decoding patch and additional simulation predeploys.

## Decision

Use the official Rundler image with an explicit version tag directly for the
independent Preview Bundler. Docker selects the matching Linux architecture;
Rundler is the container main process and receives shutdown signals directly.
Keep canonical EntryPoint v0.9 and SimpleAccount identities; remove Alto-only
simulation predeploys and the unused utility account. Rundler supplies its
simulation code through RPC state overrides, not deployment transactions.

Compose waits for Reth health before starting Rundler. Genesis identity and
exact EntryPoint code at block zero are verified by acceptance tests, not by a
startup wrapper. No Bundler JSON-RPC container healthcheck is required.
Enable only v0.9 and the standard eth RPC
namespace, without debug/admin methods, unsafe validation or unsafe fallback.
Declare the public development executor key and logging level in Compose. It has no
public port outside the existing HTTPS gateway and does not gate core readiness.
Preserve canonical PostgreSQL indexing independently of the Bundler mempool.
Match the private Reth chain's zero priority-fee recommendation in the Bundler
fee policy. The execution base fee and all safe-validation checks still apply.

Generated allocation and Bundler inputs participate in network fingerprint
validation. Changed genesis requires explicit fresh Preview initialization;
startup never rewrites existing chain state. The canonical CREATE2 construction
inputs retain their pinned source provenance independently of the Bundler image.

## Acceptance

Reproduce genesis and verify code/storage at block zero. Run real Reth/Lighthouse
submission, gas estimation, deployment, execution/revert, signature/prefund
rejection, duplicate submission, standard RPC and disabled debug methods.
Preserve ENS, API/browser, restart and split/all-role parity, and rerun the
Preview metadata, consensus and x402 gates. Local acceptance does not establish
public-chain support or close the live-payment release gates.
