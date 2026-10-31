# Preview documentation

Preview is the disposable local Geth/Lighthouse environment started by the
repository-root `compose.yaml`. Run all commands from the repository root.

- [Setup](setup.md): prerequisites, endpoints, images and persistent data.
- [Operations](operations.md): lifecycle, TLS gateway, x402, IPFS, browser push,
  configuration layout and archive IAM limitations.
- [Manual tools](tools.md): scenario CLI, proxy and EIP-7702 lifecycle controls,
  browser checklists and recovery.
- [Contracts](contracts.md): genesis allocations, funded accounts, ERC-4337 and
  the local ENS management CLI.
- [Architecture](architecture.md): genesis provenance and Bundler boundaries.
- [Testing](testing.md): isolated metadata, consensus, browser, contracts and
  payment acceptance gates.

Start with `make preview-cert` and `make start-preview`.
`make recreate-preview` preserves data; **`make stop-preview` deletes Preview
volumes and its default generated network bundle**.

Repository-wide [development rules](../../docs/development.md),
[testing rules](../../docs/testing.md), [accepted ADRs](../../docs/decisions/index.md)
and [milestone status](../../docs/plans/index.md) retain their existing ownership.
Use the [deployment guide](../../deploy/README.md) for external-chain deployments.
