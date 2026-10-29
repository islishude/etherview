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
P30-T147 also executes this real Universal Resolver through the isolated RPC
in the production all/split runtime suite. The fixture neither
contacts Mainnet nor closes P30-T148's deployment, activation or coverage gate.
