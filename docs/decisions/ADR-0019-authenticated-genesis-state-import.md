# ADR-0019: Authenticated Genesis State Import

Status: accepted

## Context

Execution block zero authenticates a state root but does not enumerate the
accounts committed by that root. Ordinary JSON-RPC block ingestion therefore
retains block-zero header and transaction facts while omitting prefunded EOAs,
predeploy code, account nonces, and storage roots. Treating that omission as an
empty allocation would publish false explorer state. Querying `latest` cannot
repair it because the result would no longer describe genesis.

## Decision

- An operator may supply a standard Genesis JSON document with either the
  server-only `chain.genesis_file` or `chain.genesis_url` setting. The settings
  are mutually exclusive and are accepted only when `chain.start_block` is
  zero.
- A remote source is a one-time bootstrap boundary. It permits only public
  HTTPS on the default port, without credentials, query, fragment, redirect,
  environment proxy, path traversal, or private/special-use destination. The
  response must use identity encoding, is capped at 64 MiB, and must contain
  valid JSON. Accepted media types are JSON, vendor `+json`,
  `application/octet-stream`, and `text/plain`.
- `chain.genesis_sha256` is optional and valid only with a remote source. It is
  an explicit non-zero lowercase SHA-256 digest of the exact response bytes and
  must match before parsing or importing. No HTTP header or sidecar checksum is
  trusted implicitly.
- Import computes the Ethereum allocation trie and requires its root to equal
  the exact stored canonical block-zero `state_root`. It also requires the
  document's resulting block identity to match the configured and stored
  genesis hash. A mismatch or malformed account is a startup/import failure and
  publishes no partial state.
- Remote import first waits for canonical block zero. Sync replicas coordinate
  with a per-chain PostgreSQL session advisory lock and recheck durable
  completion after acquiring it, so only one replica fetches. The HTTP request,
  checksum, and parsing run outside the atomic import transaction.
- A complete durable import removes the runtime dependency on its remote
  source. Restarts do not fetch again; when a checksum is configured, it must
  match the persisted document digest. Transport and temporary HTTP failures
  publish a stable redacted `unavailable` state, while checksum, content, and
  canonical-identity failures publish a stable redacted `failed` state.
- PostgreSQL stores one immutable import identity and one immutable observation
  per allocated address: exact block hash, balance, nonce, code hash, code,
  and storage root. Raw allocation storage keys and values are used only to
  authenticate the trie and are not retained.
- A missing source is an explicit `unavailable` genesis-state capability,
  never a successful empty allocation. A valid document with an empty
  allocation is a successful empty allocation and remains distinguishable.
- Non-empty predeploy code is also inserted into the existing exact
  `contract_code_observations` relation at block zero. Genesis-origin code is
  immutable exact state and does not require a state RPC call.
- A completed or late import feeds the block-zero proxy stage through a
  source-deduplicated replay. Imported code makes predeploys exact proxy
  candidates; proxy storage reads retain the existing one-endpoint EIP-1898
  boundary, then ABI follows its existing dependency. Genesis accounts are not
  token-classified merely because code exists; token discovery still requires
  its normal evidence.
- The native API exposes a cursor-stable `/api/v1/genesis/accounts` collection.
  Balances and nonces use decimal strings. Public reads require the canonical
  block-zero mapping and authenticated successful import; unavailable input
  returns the shared typed capability error.

## Consequences

- Default block-zero indexing is unchanged and remains useful without a
  Genesis JSON source, but account enumeration is explicitly unavailable.
- Operators must provide the chain's authoritative genesis document to display
  prefunded EOAs and no-transaction predeploys.
- The explorer can prove that displayed genesis accounts are covered by the
  same state root and block identity as its indexed chain without persisting
  raw storage slots.
- Adding the remote bootstrap source and checksum does not alter the persistent
  schema, native API, or embedded SPA protocol.
- Persistent schema, public API, deployment configuration, proxy replay, and
  embedded SPA changes are reviewed together under P70-T08.
