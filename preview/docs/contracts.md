# Preview Contracts

[Documentation index](README.md). Run shell commands from the repository root.

## Preview ERC-4337 and local ENS

A fresh `make start-preview` includes the v0.9 EntryPoint, SimpleAccount factory,
Rundler Bundler, and `.test` registrar/resolver suite. Bundler RPC is
`https://bundler.etherview.localhost`; the generated manifest in
`preview/contracts/manifest.json` lists addresses, code hashes, ABIs and
public development identities. These identities are confined to disposable
Preview networks. No Mainnet RPC or external CCIP gateway is required for ENS.
The ENS owner is development mnemonic account 19; Bundler executor uses
account 9, separate from x402 and the normal CLI owner (account 0).

Genesis updates require a fresh database and network. Preserve anything needed
before explicitly running `make stop-preview` followed by `make start-preview`;
stop removes Preview volumes. `make recreate-preview` retains state and rejects
changed network fingerprints. Never replace one genesis file inside an existing
bundle. Local USD and its initial owner balance are part of the same genesis.

### Every genesis allocation

The inventory below covers all **28 addresses** in
[`preview/genesis.json`](../genesis.json): 24 contracts and
four externally owned accounts. Addresses are for chain ID **48815** only.
The generated [application manifest](../contracts/manifest.json)
provides application ABIs, code hashes and development account indices;
[`allocation.json`](../contracts/allocation.json) also includes
constructor-created children. The genesis file is authoritative for every
account's code, nonce, balance and storage. EVM precompiles and the protocol's
system caller are not extra allocations in these tables.

#### Protocol and shared utility contracts

All nine contracts below start with nonce 1 and zero ETH. The seven Geth system
allocations must match the pinned client's `core.SystemContractAllocs()`;
ERC-1820 and Multicall3 are additional shared utilities. Protocol queues and
history contracts are driven by the execution/consensus protocol, not the
Preview management CLI.

| Address                                      | Contract             | Purpose and caller                                                                                                 |
| -------------------------------------------- | -------------------- | ------------------------------------------------------------------------------------------------------------------ |
| `0x00000961ef480eb55e80d19ad83579a64c007002` | WithdrawalQueue      | EIP-7002 execution-triggered validator withdrawal requests; Geth drains the queue into execution requests.         |
| `0x0000bbddc7ce488642fb579f8b00f3a590007251` | ConsolidationQueue   | EIP-7251 validator consolidation requests, processed with the consensus layer.                                     |
| `0x0000bff46984e3725691fa540a8c7589300d8282` | BuilderDeposit       | EIP-8282 builder deposit requests for the Amsterdam/Gloas Preview.                                                 |
| `0x000064d678505ad48f8ccb093bc65613800e8282` | BuilderExit          | EIP-8282 builder exit requests.                                                                                    |
| `0x000f3df6d732807ef1319fb7b8bb8522d0beac02` | BeaconRoots          | EIP-4788 beacon-root history, updated by protocol system calls and readable by EVM callers.                        |
| `0x0000f90827f1c53a10cb7a02335b175320002935` | HistoryStorage       | EIP-2935 execution block-hash history, maintained by protocol system calls.                                        |
| `0x4e59b44847b379578588920ca78fbf26c0b4956c` | DeterministicFactory | EIP-7997 keyless CREATE2 factory; the artifact generator uses it for canonical v0.9 EntryPoint/factory deployment. |
| `0x1820a4b7618bde71dce8cdc73aab6c95905fad24` | ERC1820Registry      | Shared account/interface-implementer registry for ERC-1820 integrations. It is independent of ENS.                 |
| `0xca11bde05977b3631167028862be2a173976ca11` | Multicall3           | Shared batching helper for contract calls; available to RPC clients and integrations.                              |

#### ERC-4337 contracts

These four contracts start with zero ETH. EntryPoint and SimpleAccountFactory
each have nonce 2 because their constructors created a child; the other two
have nonce 1. Rundler uses RPC state overrides for simulation without extra
simulation predeploys. Only the canonical EntryPoint is configured
for indexing or exposed by `eth_supportedEntryPoints`.

| Address                                      | Contract                     | Purpose and operation path                                                                                                                                                     |
| -------------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `0x433709009b8330fda32311df1c2afa402ed8d009` | EntryPoint v0.9              | Validates and executes UserOperations and manages deposits/nonces. Rundler submits bundles here; use `aa estimate`, `aa send` and `aa receipt`.                                |
| `0x0a630a99df908a81115a3022927be82f9299987e` | SenderCreator                | EntryPoint's real constructor-created helper for account creation. It is called through EntryPoint, not directly by CLI users.                                                 |
| `0xf4a7018fdbf22804526ec4b77dce4f9b0d27a395` | SimpleAccountFactory         | Computes and deploys owner/salt-specific account proxies. `aa address` computes the address; the first successful `aa send` supplies factory calldata and deploys the account. |
| `0x9ef4c86838638daea46d5517442f5c23fe1dd34c` | SimpleAccount implementation | Shared implementation for factory-created account proxies; it is not a user's wallet address. `aa fund` funds the computed proxy address.                                      |

#### Local ENS contracts

All ten contracts start with nonce 1 and zero ETH. `.test` is owned by the
BaseRegistrar; the configured controller can register and renew names. ENS
administration belongs to development account 19. Registration gives the
selected CLI account ownership of its name, not administration of the system.

| Address                                      | Contract                    | Purpose and operation path                                                                                                                                                          |
| -------------------------------------------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `0xc469e7ae4ad962c30c7111dc580b4adbc7e914dd` | ENSRegistry                 | Authoritative node owner/resolver/TTL registry; holds the initialized `.test`, `reverse`, `addr.reverse` and `default.reverse` relationships.                                       |
| `0x43ca3d2c94be00692d207c6a1e60d8b325c6f12f` | BaseRegistrarImplementation | ERC-721 ownership and expiry of `.test` labels. `ens transfer` transfers the registrar token; `ens reclaim` updates the Registry owner.                                             |
| `0xa7c8b0d74b68ef10511f27e97c379fb1651e1ed2` | PreviewRegistrarController  | Upstream controller with the TLD/node changed to `.test`; handles `ens quote`, `ens commit`, `ens register` and `ens renew`.                                                        |
| `0xe9061f92ba9a3d9ef3f4eb8456ac9e552b3ff5c8` | PreviewPriceOracle          | Fixed rent of 1 gwei per second, zero premium, no exchange-rate feed. Read through `ens quote`.                                                                                     |
| `0xc92b72ecf468d2642992b195bea99f9b9bb4a838` | PublicResolver              | Stores forward address records and supports the controller's registration-time record setup. `ens set-address` writes the Preview coin-type record.                                 |
| `0x446c29fbfef829f81e236a2376191f648dbef995` | UniversalResolver           | Local forward/reverse resolution entrypoint used by Etherview and `ens resolve`; never points at Mainnet.                                                                           |
| `0xabb12158488d9c9bd52c14b9ae4c835ece4a6e13` | GatewayProvider             | UniversalResolver's gateway configuration, initialized to an empty list. Preview makes no external CCIP gateway requests.                                                           |
| `0xb09da8a5b236fe0295a345035287e80bb0008290` | ReverseRegistrar            | Owns the standard `addr.reverse` branch and supports controller-authorized reverse records. The Preview CLI uses default reverse for its primary name.                              |
| `0x6d925938edb8a16b3035a4cf34faa090f490202a` | DefaultReverseRegistrar     | Stores account default reverse names; `ens set-primary` updates the calling account's name. The controller can set it during registration.                                          |
| `0xed8cab8a931a4c0489ad3e3fb5bdea84f74fd23e` | DefaultReverseResolver      | Resolves default reverse records through the local reverse hierarchy, including coin type 2147532463; the application verifies the forward record before displaying a primary name. |

#### Funded accounts

These addresses have no EVM code. Mnemonic indices use the public development
mnemonic `test test test test test test test test test test test junk` and
`m/44'/60'/0'/0/index`. It must never hold real assets.

| Address                                      | Initial ETH / nonce                              | Purpose                                                                                                                                            |
| -------------------------------------------- | ------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266` | 340282366920938463463.374607431768211446 ETH / 0 | Mnemonic index 0: default interactive CLI owner, validator fee recipient and x402 token owner/funding source. Its genesis nonce remains unchanged. |
| `0xa0ee7a142d267c1f36714e4a8f75612f20a79720` | 1000 ETH / 0                                     | Index 9: dedicated Bundler executor, signing bundle transactions. Its development key is supplied only to the Bundler service.                     |
| `0x8626f6940e2eb28930efb4cef49b2d1f2c9c1199` | 1000 ETH / 22                                    | Index 19: ENS administrator and deterministic contract-construction account. Nonce 22 is the exported post-construction nonce; do not reset it.    |

The x402 Facilitator account `0x70997970c51812dc3a010c7d01b50e0d17dc79c8`
(index 1) has 10 ETH and nonce 1 in genesis. Local USD at
`0x8464135c8f25da09e49bc8782676a84730c318bc` is also a genesis allocation,
including constructor storage and 1000000000000000000 atomic units owned by
index 0. The pinned contract generator constructs and exports this state;
startup performs no token deployment, funding or reminting.
Existing bundles with the old genesis fail fingerprint validation and require
explicit network reinitialization using the Preview lifecycle procedure.
User-specific SimpleAccount proxies and subsequently registered names arise
after block zero. No NameWrapper, Paymaster or extra EntryPoint version is installed.

### Contract CLI reference

The CLI uses the same fixed genesis identities and checks chain ID plus exact
block-zero code hashes before operating. Add `--rpc` and `--bundler` for custom
local endpoints, `--index` to select a public development account, or
`--key-file` for a server-side signing key. Node clients must trust the Preview
CA, for example with `NODE_EXTRA_CA_CERTS=.local/preview-tls/rootCA.pem`.

Run commands from the repository root after `pnpm install --frozen-lockfile`
and a fresh Preview startup. This shell helper only shortens the executable path:

```sh
export NODE_EXTRA_CA_CERTS="$PWD/.local/preview-tls/rootCA.pem"
preview_contracts() { node preview/contracts/cli.mjs "$@"; }
preview_contracts addresses
```

| Command                                                                           | Result and chain effect                                                                                                                                                                                                                                     |
| --------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `addresses`                                                                       | Prints the checked-in application manifest without contacting RPC. For the full genesis inventory, use the tables above or read `preview/genesis.json`.                                                                                                     |
| `ens quote NAME [--duration SECONDS]`                                             | Reads availability and base/premium rent in wei; does not reserve the name.                                                                                                                                                                                 |
| `ens commit NAME --state FILE [--duration SECONDS]`                               | Creates a fresh 0600 state file with the commitment secret, signs and mines a commitment transaction. Fails if FILE already exists.                                                                                                                         |
| `ens register --state FILE`                                                       | Loads the saved registration, enforces commitment age, pays current quoted rent in ETH, sets the Preview forward address and default reverse name. Reusing a state file with a saved registration hash checks that transaction instead of submitting again. |
| `ens renew NAME [--duration SECONDS]`                                             | Pays to extend registration by the supplied duration; does not change ownership or records.                                                                                                                                                                 |
| `ens transfer NAME --to ADDRESS`                                                  | Transfers the registrar ERC-721 token from the selected owner. Registry ownership and records must be updated separately.                                                                                                                                   |
| `ens reclaim NAME`                                                                | Sets the Registry owner to the selected account using its registrar-token authority. Run as the new token owner after transfer.                                                                                                                             |
| `ens set-address NAME [--to ADDRESS]`                                             | Sets the ENSIP-11 address record for coin type 2147532463; omitted --to uses the signing account. Requires resolver authorization.                                                                                                                          |
| `ens set-primary NAME`                                                            | Sets the signing account's default reverse name. Display requires the same name's forward record to match this account.                                                                                                                                     |
| `ens resolve NAME`                                                                | Reads the local UniversalResolver response, including ABI-encoded address-result bytes; it is not a plain-address formatter.                                                                                                                                |
| `aa address [--salt INTEGER]`                                                     | Reads the counterfactual account address for owner/salt; does not deploy it.                                                                                                                                                                                |
| `aa fund [--salt INTEGER] [--value ETH]`                                          | Sends ETH from the signing EOA to that account address, including before deployment. Default funding is 1 ETH.                                                                                                                                              |
| `aa estimate [--salt INTEGER] [--to ADDRESS] [--value ETH] [--data HEX]`          | Simulates, estimates gas, adds the CLI margin and signs a proposed operation; prints operation/hash without submitting or persisting it.                                                                                                                    |
| `aa send --state FILE [--salt INTEGER] [--to ADDRESS] [--value ETH] [--data HEX]` | Estimates/signs a new UserOperation, saves its exact hash and signed payload before sending once to Rundler. The first successful operation deploys the account through the factory.                                                                        |
| `aa receipt --state FILE`                                                         | Queries the saved UserOperation hash. A null result means no receipt is available yet; query again without generating another operation.                                                                                                                    |

Common options are `--rpc URL` (default Geth HTTPS origin), `--bundler URL`
(default Bundler HTTPS origin), `--index INTEGER` (default 0), and `--key-file
PATH` (a file containing a 0x-prefixed private key; overrides --index). Keep key
files private with mode 0600. The Bundler endpoint is only contacted by AA
estimation/submission/receipt operations. Saved state binds the owner, chain ID
and genesis hash, so use the same identity and network when resuming.

`--duration` is an integer number of seconds and applies only to quote, commit
and renew. Its default is 31536000 seconds (one year). Registration costs
0.031536 ETH per default year plus transaction gas; the minimum is 2419200
seconds (28 days), with a 90-day grace period. Commitments require 3 seconds and
expire after 86400 seconds (24 hours). `--value` uses decimal ETH; `--data` uses
hex calldata; `--salt` is a nonnegative integer, default 0. AA execute defaults
to the selected EOA, zero ETH and empty calldata. Keep owner/salt consistent
across address, fund and send; receipt instead reads the saved state.

A complete registration and account-deployment sequence:

```sh
preview_contracts ens quote alice.test
preview_contracts ens commit alice.test --state .local/alice-registration.json
# Wait at least three seconds and another real PoS block.
preview_contracts ens register --state .local/alice-registration.json
preview_contracts ens resolve alice.test
preview_contracts ens renew alice.test --duration 31536000
preview_contracts ens set-primary alice.test

preview_contracts aa address --salt 0
preview_contracts aa fund --salt 0 --value 2
# Fund the future name owner (development index 2) with 1 ETH through the account.
preview_contracts aa estimate --salt 0 --to 0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc --value 1
preview_contracts aa send --salt 0 --to 0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc --value 1 --state .local/user-operation.json
preview_contracts aa receipt --state .local/user-operation.json
# Query the same receipt until mined before continuing with the funded owner.

preview_contracts ens transfer alice.test --to 0x3c44cdddb6a900fa2b585dd299e03d12fa4293bc
preview_contracts ens reclaim alice.test --index 2
preview_contracts ens set-address alice.test --index 2
preview_contracts ens set-primary alice.test --index 2
preview_contracts ens resolve alice.test
```

Names are normalized and limited to one label of at least three characters
under `.test`. The explorer displays `custom_ens` names only after reverse/forward
matching; transfer alone does not update those records. Use an ordinary funded
account for experiments, not the Bundler executor or ENS administrator.

Registration state files contain commitment secrets and are created with 0600
permissions. Keep them private and out of reports. After interruption, rerun
`ens register --state FILE` with the original owner; it checks the on-chain
commitment or saved registration transaction. If no commitment was mined or it
expired, inspect the saved commit hash and chain state before making a new
commitment with a new state file. Do not overwrite the pending file or secret.
The CLI reports reverted transactions rather than automatically replacing them.

For AA, the operation/hash is saved before the RPC send; a timeout never causes
automatic re-signing or resubmission. A repeated `aa send` with the same file is
rejected; `aa receipt` performs read-only reconciliation. A different deliberate
operation needs a different state path. CLI output represents current chain
state; Etherview search and pages become visible after canonical indexing and
ENS generation refresh, not necessarily immediately after a receipt.

For arbitrary allocated addresses, the standard RPC remains available for
read-only inspection. The same commands work for contracts without a dedicated
CLI subcommand (Foundry `cast` must trust the Preview CA):

```sh
export ETH_RPC_URL=https://geth.etherview.localhost
cast code 0x433709009b8330fda32311df1c2afa402ed8d009 --block 0
cast nonce 0x433709009b8330fda32311df1c2afa402ed8d009 --block 0
cast balance 0x8626f6940e2eb28930efb4cef49b2d1f2c9c1199 --block 0
cast call 0x433709009b8330fda32311df1c2afa402ed8d009 'senderCreator()(address)' --block 0
```

`make preview-contracts-generate` explicitly regenerates checked-in allocations;
`make preview-contracts-check` reproduces them and tests the real contracts.
Use Anvil 1.8.6 and the locked pnpm workspace. Do not copy runtime bytecode to a
new address or manually edit storage: constructor immutables, child contracts,
nonces and initializer state must remain together.

ENS sources are pinned to the official v1.7.0 GitHub commit and compiled locally;
the ENS npm package is deliberately absent because of its package-wide malware
advisory. Rundler uses the official release archives pinned by version and
SHA-256 in `preview/rundler-release.json`. `make preview-bundler-image`
downloads the Docker host's Linux architecture and validates cached archives
before extraction. The image includes the reviewed GPL/LGPL notices.
The launcher checks chain ID and EntryPoint code at block zero, enables only
v0.9 and standard eth RPC, and never enables unsafe fallback. See
[ADR-0057](../../docs/decisions/ADR-0057-preview-rundler.md). Ordinary startup uses
checked-in genesis bytecode and storage without compilation or deployment.
