# ADR-0009: Block-Bound ABI Provenance

Status: accepted

## Context

An ABI selector is not a globally unique contract identity. Four-byte function
and error selectors collide, proxies change implementations over time, runtime
code changes across block ranges, and two forks may contain different facts at
the same height. A process-wide selector map can therefore decode a call with
an ABI learned from another chain, address, code version, range, or fork.

Signature databases are useful fallback evidence, but a matching selector or
topic does not prove deployed bytecode. Allowing a caller to assign confidence
independently of source would let guessed material be published as verified.

## Decision

- Every durable ABI binding names the target chain, address, runtime code hash,
  context block number and hash, and inclusive validity range. The context
  block must fall inside that range. In-memory registries use the same complete
  target identity as their lookup key.
- Direct ABI material comes only from a verified artifact for the same target
  address and code hash whose range covers the context block. It has source
  `verified` and confidence `verified`.
- A historical proxy binding consumes an already persisted canonical proxy
  observation; the ABI comes from a verified artifact for that observation's
  implementation address and code hash. Its valid range is the intersection of
  target code, proxy implementation, and verified-artifact ranges. It has
  source `proxy_implementation` and confidence `high`. ABI processing does not
  discover proxies.
- Signature candidates are selected only for identifiers actually observed at
  the target. The worker parses each stored ABI entry, reconstructs its
  canonical signature, and verifies its selector/topic before use. Such
  material has source `signature_database` and confidence `guess`.
- PostgreSQL fixes the source-to-confidence mapping with a check constraint;
  callers never persist confidence independently. Direct and signature sources
  must also repeat the target source address and code hash. Built-in Solidity
  `Error(string)` and `Panic(uint256)` entries are decoder-local and can appear
  in decoding output, but are never durable contract ABI bindings.
- Candidate preference is direct verified ABI, historical proxy implementation
  ABI, then signature guess. Equal-confidence selector collisions remain
  `ambiguous` and retain all candidate signatures rather than selecting a
  pretend winner.
- `abi@1` writes bindings, decoded transaction/log/available normalized-trace
  observations, its stage result, and its canonicality journal in one
  transaction. Trace absence never delays or fails ABI processing; explicit
  ABI reindex after Trace may add those optional decodings.
- Production `abi@1` is claim- and processor-gated on `proxy@1` for the exact
  block hash. Complete proxy facts permit decoding; explicit proxy
  unavailability makes ABI unavailable rather than persisting `unbound` or a
  lower-priority guess. Late proxy or Trace facts safely reset only terminal
  output under [ADR-0010](ADR-0010-block-pinned-proxy-stage-and-abi-dependency.md).
- ABI decoding rows are block-hash facts in fixed one-million-block partitions.
  Detach retains bindings and decodings with `canonical=false`; reattach or
  exact replay restores the same identities idempotently.

## Consequences

- ABI material cannot leak between forks, code versions, contracts, or chains,
  even when selectors collide.
- A signature service can improve display coverage but can never create a
  verified fact in Go or PostgreSQL.
- Proxy ABI quality depends on P20-T03 producing canonical historical proxy
  observations; missing observations degrade to signature guesses or unknown,
  not a global ABI lookup.
- ABI reindex may be useful after Trace completes, but core ingestion and other
  enrichment stages never wait for it.
