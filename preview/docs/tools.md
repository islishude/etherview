# Preview manual tools

[Documentation index](README.md). Run commands from the repository root after
`pnpm install --frozen-lockfile` and `make start-preview`. The tools operate on
that already-running disposable chain; they never start, stop or reset it.
Local IPFS metadata normally uses Preview's dedicated metadata worker.
[ADR-0005](../../docs/decisions/ADR-0005-safe-nft-metadata-and-media-boundary.md)
forbids the private-network exception in `all`; an all-role deployment needs an
HTTPS gateway accepted by the strict address policy. The CLI does not change
that policy or the deployment layout.

## Quick start

```sh
make preview-tool ARGS='doctor'
make preview-tool ARGS='list'
make preview-tool ARGS='run blob'
make preview-tool ARGS='run aa'
make preview-tool ARGS='run proxy.uups'
make preview-tool ARGS='act --run RUN_ID upgrade'
make preview-tool ARGS='run eip7702'
make preview-tool ARGS='act --run RUN_ID redelegate'
make preview-tool ARGS='act --run RUN_ID clear'
make preview-tool ARGS='wallet prepare --to 0xYOUR_WALLET'
make preview-tool ARGS='run all --to 0xYOUR_WALLET'
make preview-tool ARGS='check --run RUN_ID'
make preview-tool ARGS='report --run RUN_ID'
```

Replace `RUN_ID` and the example address with actual values. Each invocation
prints its run ID, inspection URLs and a Markdown report path. Open those links
in your normal browser. After an action, use the explorer's **Refresh** button:
ordinary explorer pages intentionally do not replace their snapshot automatically.

`run all` without `--to` prepares browser assets for the documented public
Preview account 0. An explicit wallet address is recommended. The CLI never
requests that wallet's private key or changes its EIP-7702 delegation. Scenario
owners and EIP-7702 authorities are separate generated disposable accounts,
funded by public development account 0. Only use these tools on Preview.

## Scenario catalog

| Scenario            | Initial data                                                                                          | Explicit actions                                                                 |
| ------------------- | ----------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `transactions`      | Legacy, access-list and EIP-1559 transfers; creation, internal transfer, state/log changes and revert | `activity`                                                                       |
| `blob`              | Real one-blob and two-blob transactions, cell proofs, archive readback                                | None                                                                             |
| `aa`                | Funded SimpleAccount deployed through Rundler v0.9                                                    | `execute`, `revert`, `bad-signature`, `insufficient-prefund`                     |
| `tokens`            | ERC-20, ERC-721 and ERC-1155; large integer IDs and local IPFS metadata                               | `transfer`, `approve`, `burn`, `metadata-update`, `metadata-invalid`, `activity` |
| `ens`               | A unique `.test` registration and matching primary name                                               | `renew`, `transfer`, `reclaim`, `set-address`, `set-primary`, `mismatch`         |
| `contracts`         | Exact verified, same-code and unverified examples; source upload files                                | `verify`                                                                         |
| `proxy.erc1967`     | ERC-1967 proxy and implementation                                                                     | `write`, `upgrade`                                                               |
| `proxy.transparent` | Transparent proxy and its immutable ProxyAdmin                                                        | `write`, `upgrade`                                                               |
| `proxy.uups`        | UUPS proxy plus valid and invalid upgrade targets                                                     | `write`, `bad-upgrade`, `upgrade`                                                |
| `proxy.beacon`      | Two independently initialized proxies sharing one Beacon                                              | `write`, `upgrade`                                                               |
| `proxy.erc1167`     | Factory-created standard clone                                                                        | `write`                                                                          |
| `proxy.immutable`   | OpenZeppelin immutable-args clone                                                                     | `write`                                                                          |
| `proxy.cwia`        | Exact Solady legacy CWIA with large integer and bytes arguments                                       | `write`, `unauthorized`                                                          |
| `proxy.safe`        | Official Safe 1.4.1 singleton and factory-created proxy                                               | None; inspect recognition, not management authorization                          |
| `proxy.diamond`     | Diamond and two facets                                                                                | `write`, `add`, `replace`, `remove`                                              |
| `eip7702`           | A separately funded authority delegated to verified code                                              | `execute`, `redelegate`, `skip`, `revert`, `clear`                               |
| `wallet`            | ETH, Local USD, ERC-20, ERC-721 and ERC-1155 for the specified browser wallet                         | `activity`                                                                       |

With a multi-scenario run, select the scenario explicitly:

```sh
make preview-tool ARGS='act --run RUN_ID --scenario proxy.beacon upgrade'
make preview-tool ARGS='act --run RUN_ID --scenario ens transfer'
make preview-tool ARGS='act --run RUN_ID --scenario ens reclaim'
make preview-tool ARGS='act --run RUN_ID --scenario ens set-address'
make preview-tool ARGS='act --run RUN_ID --scenario ens set-primary'
```

ENS transfer and reclaim deliberately remain separate. The new generated name
owner receives gas; a transferred registrar token does not automatically change
the Registry controller. `mismatch` changes the forward record to another
account so that the primary-name match can be checked.

Use `run SCENARIO --lifecycle` or `resume --run RUN_ID --lifecycle` for the
catalog's ordered full flow. The default preserves the initial state. Lifecycle
execution ends at the final state (including cleared delegation and invalid
metadata), so inspect the initial state first if you need a visual comparison.
Actions are real transactions. For example, repeating a transfer of the same
ERC-721 after ownership has changed can revert. Reports retain that outcome.

## Reports and recovery

Runs live in `.local/preview-tools/runs/RUN_ID/`. `report.json` and `report.md`
contain addresses, hashes, expected API values, links and unchecked manual
items. `verification/` contains the exact standard-JSON submissions for the
fixture compiler and its imports. These are suitable for the browser Verify
flow; the CLI also performs native API verification by default. Safe uses its
locked upstream bytecode instead of recompiling the 0.7.6 artifact with 0.8.30.
Its new local singleton address is outside Safe’s chain-aware deployment manifest:
expect `canonical_proxy_shell=true` and `official_singleton=false`.

`state.json` and `private/` are private execution state, including signed
payloads, ENS commitment material and disposable keys. Do not share them.
Reports intentionally omit this material. Large integers remain strings.
`blobs/*.bin` contains the original unsigned 131072-byte blob bodies for manual
download comparisons; the report records their SHA-256 and versioned hashes.

The lifecycle distinguishes prepared/submitted transactions, mined receipts,
API publication and manual inspection. Proxy verification can reject a stale
publication while indexing advances; the CLI records the failed job and makes
fresh verification submissions within the configured timeout (at most 30),
with staggered delays. This never resends a transaction. API rate limits use
bounded `Retry-After` backoff; multi-blob checks retain downloads already verified
during that check, so a later rate limit cannot starve the remaining downloads.
A successful API check means data is ready for inspection; it never marks the
browser checklist completed. Negative
AA admission tests require the expected RPC rejection code; they do not create
an explorer transaction. An execution revert is instead a real included result.

```sh
make preview-tool ARGS='check --run RUN_ID'
make preview-tool ARGS='resume --run RUN_ID'
make preview-tool ARGS='report --run RUN_ID --json'
```

`check` queries receipts and public APIs without signing or sending operations.
It also reconciles the funding transaction shown separately in the report.
It cannot mark unfinished setup or actions complete; those still require `resume`.
`resume` reuses the recorded identities and continues only after previous
transactions are confirmed. A submitted or prepared operation with no known
receipt remains uncertain: the tool does not replace it or allocate a new nonce.
Investigate the recorded hash. Start a new run only after deciding that new
chain activity is intended. An expired ENS commitment likewise requires an
explicit new run, not a silently replaced secret.

Writers lock the run and shared funding account. Funding locks stay in the
repository’s `.local/preview-tools/locks/` even with a custom `--dir`, so separate
state directories cannot race the same developer nonce. An uncertain shared
payment deliberately retains its lock even after a normal process exit. New
scenario transactions also stop until earlier uncertain operations reconcile.
Before removing a stale lock, verify that its PID is no longer running and
reconcile the saved payment hash with `check`. A dead PID alone does not prove
that a payment was not sent. Keep the private run intact; do not release an
unresolved payment merely to allow another run to spend the same nonce.
Changing the Preview genesis invalidates old runs; use a new run on the new
network. Recreating containers without changing the network retains validity.

## Options and browser checklist

- `--dir PATH`: isolated state/report directory.
- `--rpc ORIGIN`, `--bundler ORIGIN`, `--app ORIGIN`: local endpoints; otherwise
  the standard HTTPS hosts and `PREVIEW_HTTPS_PORT` are used. TLS verification
  stays enabled. Make supplies the Preview CA through `NODE_EXTRA_CA_CERTS`.
- `--timeout SECONDS`: bounded per-operation/publication wait, default 180,
  maximum 1800. Failure retains evidence and a nonzero exit status.
- `--project NAME`, `--compose-files FILE,...`: select the Compose project used
  for temporary verification credentials and local IPFS fixture uploads. Defaults
  to the daily Preview.
- `--no-verify`: leave source verification and binding preparation manual;
  this does not establish source/ABI/write-interface acceptance.
- `--json`: machine-readable public result; operation failures return nonzero.

For browser acceptance, check Chinese and English, light/dark themes, desktop
and 390px layouts, exact integer display, pagination, link navigation and
explicit refresh. Inspect proxy source/read/write/management tabs separately;
Safe detection and same-code artifacts do not authorize arbitrary writes.
EIP-7702 checks distinguish the authority's storage context from its execution
code address and retain authorization history after clearing.

For private features, prepare your wallet, add Preview, sign in and create a
user API key through the normal UI. Check both local Billing methods, approval
and signature prompts, usage and balances using the
[x402 procedure](operations.md#preview-x402-billing). An uncertain payment must
be reconciled rather than repaid. Watch an address, grant notification permission,
then invoke `activity`; export its CSV. API-key creation/revocation, wallet
signatures and notification permissions remain explicit browser actions.

Home, status, genesis, search and charts use the generated activity. Pending
transactions may disappear quickly on the three-second-slot chain. Consensus
and execution requests expose actual network data, including empty request
sets. Admin permissions, external source services and real browser-provider
push delivery require their own prerequisites and remain unverified until
checked. This tool does not perform outages, reorg injection, clock changes or
public-chain acceptance.

## Acceptance

`make test-preview-tools` runs journal, input, artifact, privacy and blob-helper
regressions.

`make test-preview-tools-e2e` is the default CLI smoke gate. It creates one
isolated split-role Geth/Lighthouse Compose project and runs only `transactions`
with `--no-verify`: real transactions, receipts and public API checks, report
creation, lifecycle continuation, completed-resume transaction identity and
read-only `check` stability. It does not build the blob helper or launch a
browser. The test-process deadline is 15 minutes. This still requires the real
Preview stack; it is not a unit test.

`make test-preview-tools-e2e-full` explicitly runs the full catalog and lifecycle
in independent all/split projects, with initial/final English desktop and Chinese
mobile browser assertions and screenshots. Its test-process deadline is 60
minutes. Run it when changing scenario lifecycles, report-to-browser integration,
or tool behavior across deployment layouts. It is opt-in and does not run in
ordinary PR, push or scheduled CI. Browser prerequisites are described in
[Preview testing](testing.md). The full Go test also requires
`PREVIEW_TOOLS_FULL=1`, supplied by this Make target, so a broad tagged Go test
cannot accidentally opt in.

Both E2E targets have `-prebuilt` variants that use already-built images:
`make test-preview-tools-e2e-prebuilt` and
`make test-preview-tools-e2e-full-prebuilt`. CI runs the smoke variant without
installing Chromium, shares its CI job with gateway/genesis and x402 checks,
and retains CLI reports and diagnostics. Business acceptance
remains in the metadata, contracts/Bundler/ENS, consensus/blob and runtime gates;
UI coverage remains in the frontend/browser gates.

The isolated E2E config uses a 250ms runtime poll interval for the large catalog on three-second
slots; source discovery services stay disabled. Exact canonical binding checks
remain enabled. The all-role subtest retains strict metadata network policy and
uses a test-owned HTTPS gateway on an internal Docker subnet with addresses
accepted by that policy, following the runtime source-provider fixture pattern.
It trusts the fixture CA and uses offline local Kubo; it never contacts a public
IPFS gateway. The split subtest exercises the normal metadata-only exception.
With slower operator polling, binding may time out while new heads advance;
the run preserves its transactions for `check` and `resume`. Test cleanup owns
only its projects, never the daily Preview. These gates establish local
behavior, not external payment, public-source availability or remote CI results.
