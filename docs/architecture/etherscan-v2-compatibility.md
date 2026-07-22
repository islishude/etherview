# Etherscan V2 Compatibility Matrix

Etherview exposes an explicit Etherscan-compatible subset at the exact
`/v2/api` route. The allowlist below is the complete compatibility contract;
Etherview does not proxy arbitrary Etherscan or JSON-RPC methods. Registration
does not imply that an action can always succeed: optional or intentionally
unavailable capabilities remain addressable so callers receive a stable
Etherscan envelope instead of a misleading empty result.

The source of truth for dispatch is `internal/etherscan/handler.go`; production
capability wiring and execution live in `internal/app/serve.go` and
`internal/etherscan/postgres.go`. A change to any of those surfaces must update
this matrix and the compatibility golden tests in the same change.

## Common Request Contract

- Every request supplies `chainid`, `module`, and `action`. `chainid` is a
  base-10 integer and must equal the deployment's one configured chain ID.
  Module, action, and parameter names are case-sensitive; notably the logs
  action is `getLogs`.
- Only `GET` and `POST` reach the compatibility handler. A `POST` may carry
  parameters in the query or in a bounded
  `application/x-www-form-urlencoded` body. The per-action table further
  restricts the four verification actions.
- An address is exactly 20 hexadecimal bytes and a transaction, block, or
  topic hash is exactly 32 hexadecimal bytes, each with a `0x` prefix.
- In the tables, **list controls** means optional `page` (default `1`, positive
  integer), `offset` (default `100`, range `1..1000`), and `sort` (default
  `asc`, either `asc` or `desc`).
- **Block range** means optional canonical uint256 decimal `startblock`
  (default `0`) and `endblock` (default current canonical tip), with
  `endblock >= startblock`. **Log range** is the equivalent `fromBlock` and
  `toBlock` pair. Tags other than omitted or `latest` are not accepted.
- Unless a row says **required**, an API key is optional. A valid optional key
  selects its keyed quota; omitting it selects the anonymous quota. Supplying
  an invalid key never falls back to anonymous access.

## Capability Terms

| Term | Required production fact or service |
|---|---|
| Core | Canonical PostgreSQL block, transaction, receipt, log, and withdrawal facts. A Core-backed list additionally requires one continuous durable Core coverage range over its tip-clamped request range. |
| State | A state-purpose RPC endpoint, one exact canonical EIP-1898 block-hash observation, and a successful post-call canonicality recheck. Event-derived balances do not qualify. |
| Trace | After the Core coverage proof, a `complete` published Trace stage result for every canonical block in the selected range. |
| Token | After the Core coverage proof, a `complete` published Token stage result for every canonical block in the selected range. |
| Verified | The newest canonical code observation for the address and a durable verified artifact for that exact code hash whose validity range covers the canonical tip. |
| Price | An enabled pricing adapter with a fresh, valid USD/BTC observation. |
| Public verification | Verification is enabled, `security.public_verification` is enabled, and the durable public verification service is usable. |
| Supply provider | An explicit authoritative execution-currency supply provider. Ethereum JSON-RPC and Etherview's core index do not provide this fact. |

An unavailable prerequisite produces `status: "0"` with a controlled
capability message. Before a Core-backed list reads rows, it clamps the
requested end to the canonical tip and proves that one durable Core coverage
range covers the result. A range wholly above the tip reports
`No records found`; a missing tip, gap, or non-covering range reports
`core coverage unavailable`. Trace- and Token-backed lists perform that Core
proof first and report `No records found` only after every canonical block in
the resulting range has a `complete` published stage result. An absent or
failed stage is not an empty success.

## Supported Actions

### `account`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `balance` | `GET`, `POST` | `address` | `tag` | Optional | State |
| `balancemulti` | `GET`, `POST` | `address` as a comma-separated list of 1 to 20 addresses | `tag` | Optional | State |
| `txlist` | `GET`, `POST` | `address` | Block range; list controls | Optional | Core |
| `txlistinternal` | `GET`, `POST` | One selector mode described below | `address`, `txhash`, block range; list controls | Optional | Trace for the resolved range |
| `tokentx` | `GET`, `POST` | `address` | `contractaddress`; block range; list controls | Optional | Token for the selected range, plus Core transaction and receipt facts |
| `tokennfttx` | `GET`, `POST` | `address` | `contractaddress`; block range; list controls | Optional | Token for the selected range, plus Core transaction and receipt facts |
| `token1155tx` | `GET`, `POST` | `address` | `contractaddress`; block range; list controls | Optional | Token for the selected range, plus Core transaction and receipt facts |
| `tokenbalance` | `GET`, `POST` | `contractaddress`, `address` | `tag` | Optional | State; the contract is read with ERC-20 `balanceOf` |
| `getminedblocks` | `GET`, `POST` | `address` | `blocktype` (`blocks` by default); list controls | Optional | Core for `blocks`; `uncles` is intentionally unavailable |

`txlistinternal` accepts exactly one of these selector modes:

1. `address`, with an optional block range;
2. `txhash`, with no address or block range; or
3. an explicit `startblock` and `endblock` range, with neither address nor
   transaction hash.

### `contract`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `getabi` | `GET`, `POST` | `address` | None | Optional | Verified, with a non-null ABI |
| `getsourcecode` | `GET`, `POST` | `address` | None | Optional | Verified |
| `getcontractcreation` | `GET`, `POST` | `contractaddresses`, a comma-separated list of 1 to 5 unique addresses | None | Optional | Core; factory `CREATE`/`CREATE2` rows need Trace. A definitive no-match additionally needs indexing from genesis, continuous Core coverage through the tip, and Trace through the tip. |
| `verifysourcecode` | `POST` | `contractaddress` and the [source-verification form](#source-verification-form) | Form-specific fields below | Required | Public verification plus an exact current canonical code observation and canonical top-level or traced creation input |
| `checkverifystatus` | `POST` | `guid`, a durable verification-job UUID | None | Required | Public verification |
| `verifyproxycontract` | `POST` | `address` | `expectedimplementation` | Required | Intentionally unavailable |
| `checkproxyverification` | `GET` | `guid` | None | Required | Intentionally unavailable |

### `transaction`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `getstatus` | `GET`, `POST` | `txhash` | None | Optional | A canonical Core receipt with a status field |
| `gettxreceiptstatus` | `GET`, `POST` | `txhash` | None | Optional | A canonical Core receipt with a status field |

### `logs`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `getLogs` | `GET`, `POST` | None | Log range; `address`; `topic0` through `topic3`; topic operators; list controls | Optional | Core |

Each supplied topic is one 32-byte hash. Operators are named
`topicN_M_opr`, contain `and` or `or`, and may connect only adjacent supplied
filters in increasing topic-index order. Missing operators default to `and`.
Tags such as `latest` are not accepted for `fromBlock` or `toBlock`; those
inputs are canonical uint256 decimal values.

### `block`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `getblocknobytime` | `GET`, `POST` | `timestamp` as a canonical uint256 decimal; `closest` as `before` or `after` | None | Optional | Core, with one continuous durable coverage range from genesis through the tip |
| `getblockcountdown` | `GET`, `POST` | `blockno` as a canonical uint256 decimal | None | Optional | The Core interval containing the canonical tip, with a future target and a positive continuous time/block span over at most its latest 128 blocks |

### `stats`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `ethsupply` | `GET`, `POST` | None | None | Optional | Supply provider; the shipped runtime does not wire one |
| `ethprice` | `GET`, `POST` | None | None | Optional | Price |
| `tokensupply` | `GET`, `POST` | `contractaddress` | None | Optional | State; the contract is read with ERC-20 `totalSupply` |

### `token`

| Action | Methods | Required parameters | Optional parameters | API key | Capability prerequisite |
|---|---|---|---|---|---|
| `tokensupply` | `GET`, `POST` | `contractaddress` | None | Optional | State; alias of `stats.tokensupply` |
| `tokenbalance` | `GET`, `POST` | `contractaddress`, `address` | `tag` | Optional | State; alias of `account.tokenbalance` |
| `tokeninfo` | `GET`, `POST` | `contractaddress` | None | Optional | Token through the canonical tip and a current canonical token observation. Exact State is optional; without it, ERC-20 `totalSupply` is omitted. |
| `tokenholderlist` | `GET`, `POST` | `contractaddress` | List controls | Optional | Intentionally unavailable |

## Source-Verification Form

`contract.verifysourcecode` requires exactly one value for every supplied
parameter and rejects unknown parameters. In addition to the common selectors
and `contractaddress`, these fields are accepted:

| Parameter | Rule |
|---|---|
| `sourceCode` | Required, non-empty, and within the configured verification input limit. It is plain source for `solidity-single-file` and an inline-source Standard JSON object for the JSON formats. Duplicate JSON keys and external source URLs are rejected. |
| `codeformat` | Required: `solidity-single-file`, `solidity-standard-json-input`, or `vyper-json`. |
| `contractname` | Required. A single Solidity file may use a bare contract name; Standard JSON uses `source:name`. Vyper's name must match the source filename. |
| `compilerversion` | Required. The optional `vyper:` prefix is removed for `vyper-json`; the resulting version must be allowlisted by the verification runtime. |
| `optimizationUsed` | Optional `0` or `1`; it must not conflict with Standard JSON settings. |
| `runs` | Optional canonical integer `0..1000000`, Solidity only; it must not conflict with Standard JSON settings. |
| `constructorArguments` / `constructorArguements` | Optional even-length hexadecimal suffix, with or without `0x`. Both spellings are accepted only when they do not conflict. The suffix must exactly match the server's canonical creation input. |
| `evmVersion` / `evmversion` | Optional compiler EVM version; `default` means omitted. Both spellings are accepted only when they do not conflict. |
| `licenseType` | Optional canonical integer `1..14`; defaults to `1`. It is publication metadata, not a compiler setting. |
| `libraryname1..10`, `libraryaddress1..10` | Optional paired Solidity library bindings. Multi-file names are source-qualified and every address is validated. |

The server replaces caller output selection with the bounded exact-target
artifact set, derives chain/address/code hash/block hash/runtime bytecode and
creation input from canonical PostgreSQL facts, and never grants Sourcify
upload consent through this compatibility form. The returned GUID is the
durable local verification-job UUID.

## API-Key and Error Boundary

- `/v2/api` accepts a key through `X-API-Key`, the `apikey` query parameter,
  or, for URL-encoded `POST`, the bounded `apikey` form field. Equal values
  across sources are accepted; conflicting sources or repeated non-empty
  values within one source are rejected. Credential material is removed before
  action validation and is never forwarded to the backend.
- `verifysourcecode`, `checkverifystatus`, `verifyproxycontract`, and
  `checkproxyverification` require an authenticated key. Authentication and
  rate limiting happen before action dispatch, including for the intentionally
  unavailable proxy operations.
- Compatibility successes use `{ "status": "1", "message": "OK",
  "result": ... }`. Ordinary validation, not-found, pending, and capability
  failures normally retain HTTP 200 with `status: "0"`. Request parsing can
  return 400, non-GET/POST methods return 405, authentication returns
  400/401/413 as applicable, rate limiting returns 429, and unexpected backend
  failures return 500. Every one of those boundary responses still uses the
  Etherscan `{status,message,result}` envelope.

## Registered but Intentionally Unavailable

These are stable negative capabilities, not empty datasets:

- `contract.verifyproxycontract` and `contract.checkproxyverification` always
  report `proxy verification workflow unavailable`. Proxy/code discovery is
  not a durable GUID-addressable source-verification workflow.
- `token.tokenholderlist` always reports `state capability unavailable`.
  Standard JSON-RPC cannot enumerate all current ERC-20 holders, and
  event-derived deltas are not authoritative current state.
- `stats.ethsupply` has an authoritative-provider extension point, but no
  production runtime adapter or configuration wires it. The shipped runtime
  therefore reports `supply capability unavailable` rather than inventing an
  issuance total.
- `account.getminedblocks` supports `blocktype=blocks`; its
  `blocktype=uncles` mode reports `uncle index capability unavailable`.
- The `blockReward` field is omitted from mined-block rows because the Core
  schema has neither consensus issuance nor a complete execution reward.

Every module or action absent from the tables is unsupported, including the
Etherscan `proxy` JSON-RPC module. Unknown modules and actions are rejected as
such; they are not forwarded upstream.

## Intentional Wire Differences

- Etherview is one-chain-per-deployment. `chainid` is mandatory and cannot
  select another chain behind the same endpoint.
- Read actions accept both `GET` and `POST`. Source submission and source
  status are POST-only; proxy submission is POST-only and proxy status is
  GET-only. In particular, `checkverifystatus` is intentionally not a GET.
- Account, token, block, and statistics quantities are decimal strings. The
  `getLogs` result instead uses lowercase RPC-style hexadecimal strings for
  `blockNumber`, `timeStamp`, `gasPrice`, `gasUsed`, `logIndex`, and
  `transactionIndex`. Hashes are lowercase hexadecimal and response addresses
  are checksummed.
- An unavailable optional capability is an explicit `NOTOK` result, never an
  authoritative empty success. Core-backed empty lists require one continuous
  durable coverage range over the tip-clamped request; Trace- and Token-backed
  lists then require published completeness over that same canonical range.
- `getsourcecode.SourceCode` is the compact canonical stored sources object,
  not a reconstruction of the caller's original submission wrapper.
  `CompilerType` is `solc` or `vyper`; `ContractFileName` is present but empty;
  and `MatchKind` (`exact` or `metadata_only`) is an Etherview extension.
  `Proxy` remains `"0"`, while `Implementation`, `SwarmSource`, and
  `SimilarMatch` remain empty until authoritative corresponding facts exist.
- Transaction and token rows do not guess a function signature:
  `functionName` is empty and `methodId` is only the lowercase four-byte
  selector when the input contains one. Failed `getstatus` responses use the
  controlled description `execution failed` rather than upstream revert text.
- Source verification binds to server-derived canonical target facts and
  returns a durable UUID. It is stricter than permissive compatibility
  parsers: repeated or unknown form fields, duplicate JSON keys, external
  source indirection, and conflicting form/Standard-JSON settings are rejected.
