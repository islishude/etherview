# Pinned ENSv2 contract fixture

The fixture executes real creation bytecode using the repository's pinned Geth
EVM. The ABI and bytecode in `testdata` come from the official MIT-licensed
[ENSv2 deployment artifacts](https://github.com/ensdomains/contracts-v2/tree/4f47cbcc11462fb413196e6b5c0f8090247a6c08/contracts/deployments/mainnet),
revision `4f47cbcc11462fb413196e6b5c0f8090247a6c08`. Each reduced artifact retains
the upstream JSON's SHA-256 and exact revision alongside ABI and creation code;
`testdata/LICENSE` retains the upstream license. It contains no RPC credentials. The real Universal Resolver, ContractNamer and
DNSSEC gateway-provider artifacts use the same pinned revision and provenance.

`New` deploys a label store, permissioned registry, user-registry implementation,
permissioned-resolver implementation and verifiable factory, then asks the real
factory to create and initialize both proxies. Tests call the real registration,
role, linking and record methods and consume EVM-generated logs. The all-roles
fixture uses the contract's nybble-packed role bitmap, not an arbitrary uint256
mask. Implementation constructor events are intentionally present so discovery
must distinguish implementations from usable instances.

Run `go test ./internal/enssync/...` and the maintained PostgreSQL runner with
`-run '^TestENSV2'`. These are isolated local contract/collector regressions;
P30-T150 also executes this real Universal Resolver on the same local core-indexed Anvil chain
in the production all/split runtime suite. The fixture neither
contacts Mainnet nor closes P30-T151's representative publication gate or proves historical coverage.

The v1 migration fixture uses ENS contracts revision
`9b034936a42f462fc04bc0a929a419ede5e18d59`. Registry and base registrar artifacts
come from its Sepolia deployment directory; NameWrapper, resolver, reverse
registrars and metadata service come from its Mainnet directory. The contracts
run only inside the local EVM. `LICENSE-v1` retains their license. `Migration`
executes premigration reservation, all three actual token transfer paths,
WrapperRegistry factory discovery and v1 reverse mirroring. These fixtures do
not assert that future v2 deployments are active on any live chain.

The production test replays recorded deployments and calls as signed transactions.
Its additional `ens_ccip_fixture.sol` custom resolver is test-owned (solc 0.8.30,
optimizer 200, Prague, metadata hash disabled), not an official ENS artifact. It
exercises real Universal Resolver CCIP failure propagation with an unavailable
gateway; the production network policy remains enabled.
