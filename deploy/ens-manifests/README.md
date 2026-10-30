# Reviewed ENS deployment manifests

Mount reviewed JSON manifests in `/etc/etherview/ens`. The default deployment
keeps ENS disabled. Enabling it requires `ens.policy_digest` and one current-chain
`ens.manifest` path. Manifests contain
public deployment identities and no RPC URLs or credentials.

Each manifest binds the identical target and source chain IDs, genesis, policy,
start block/hash, coin type, registry, Universal Resolver, and reviewed
contract deployments. ENSv2 additionally specifies trusted roots and factory
implementation/code identities. See ADR-0058 and the operations guide. Mainnet
activation requires P30-T151 evidence; local fixtures do not establish it.
