# Reviewed ENS event profiles

These files are event-only ABI subsets. The collector selects a profile only
through a reviewed manifest that also pins deployment block/hash, runtime code
hash and (for RPC-verified proxies) implementation address/code hash. They do
not enable an arbitrary emitter merely because its topics match.

- `registry`, `registrar`, `controller_legacy`, and the address/name portion of
  `resolver` use the ABI definitions in the pinned `github.com/ensdomains/go-ens/v4`
  v4.0.0 dependency (`contracts/{registry,baseregistrar,ethcontroller,resolver}`).
- `controller_v3` follows the official
  [ENS contracts v1.1.4 controller](https://github.com/ensdomains/ens-contracts/blob/v1.1.4/contracts/ethregistrar/ETHRegistrarController.sol).
- `controller`, `wrapper`, `reverse`, `default_reverse`, and resolver version
  events follow the repository's pinned ENS v1.7.0 sources at
  `preview/contracts/upstream/ens-sources.json`, revision
  `9b034936a42f462fc04bc0a929a419ede5e18d59`.
- Wrapper transfers use the standard ERC-1155 single/batch event layouts.

The manifest digest includes each profile's SHA-256 digest. These subsets
intentionally omit unrelated approvals/text/metadata events; matching emitter log identities reference core storage. Deployment addresses and historical activation are
operator-reviewed input, not inferred from this ABI catalog. Public network
activation and completeness remain the independent P30-T151 gate.

The v2 registry, resolver and factory profiles are event subsets of the official
[deployment artifacts](https://github.com/ensdomains/contracts-v2/tree/4f47cbcc11462fb413196e6b5c0f8090247a6c08/contracts/deployments/mainnet),
revision `4f47cbcc11462fb413196e6b5c0f8090247a6c08`. They retain registry lifecycle,
role/resource/token identity, proxy creation, name links and address/primary
record updates. Text/content/ABI records are outside this presentation feature.
The same revision supplies the real EVM fixture below; this does not assert
Mainnet v2 activation.

The `universal` profile pins the resolver entrypoint deployment without
claiming standard record event semantics. Background workers retain the reviewed
Universal Resolver call and CCIP validation in `internal/ens`.
