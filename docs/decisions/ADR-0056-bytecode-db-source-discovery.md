# ADR-0056: Bytecode Database Source Discovery

Status: accepted

## Decision

An explicitly enabled API-role worker searches the configured eth-bytecode-db
HTTPS service for public bytecode. It never uploads source or requests remote
verification. External source, compiler settings, match labels and artifacts
are hostile candidate input, never publication evidence. Solidity, Yul and
application-supported Vyper candidates pass the existing local compiler,
matcher, canonical-target and lease-fenced publication boundary of ADR-0024.
Ordinary targets require local creation facts; only authenticated Genesis
predeploys retain the runtime-only exception.

PostgreSQL owns per-chain discovery cursors, source lookup caches, cooldowns, global
request admission, task leases and candidate/job provenance. Repeated bounded
address sweeps cover late facts, reorgs and disabled intervals. Cache identity
includes the provider and exact bytecode type/digest; verification additionally
binds chain, address, code hash and block hash. Source reuse never transfers
creation provenance. Deterministic candidate failures are retained. Automatic
jobs are lower priority than submitted work and at most one may compile at a
time. Existing verified publications are not replaced by automatic work.

The integration is disabled by default outside Preview and requires local
verification. Preview explicitly enables both in its shared configuration.
The API and all-role modes use the same components. Remote unavailability does
not affect core readiness. Remote calls hold no database snapshot, are bounded
in time and bytes, and use the existing redirect-free transport with public-network
checks by default. The independent `bytecode_db.unsafe_allow_private_networks`
override permits Fake-IP/private-network development. It defaults off outside
Preview; Preview explicitly enables it in its shared configuration. It only relaxes the search client's IP classification; HTTPS,
TLS certificate verification, checked-IP dialing, disabled environment proxies,
redirect rejection and local source-verification rules remain mandatory.
Only locally successful publications expose `eth_bytecode_db` source origin;
public callers cannot supply this origin. ABI and matching evidence always
come from local compilation.

## Consequences

The fresh-database baseline owns the new durable contracts. No legacy migration
adapter or startup data backfill is added. Operators outside Preview explicitly
enable scans; Preview operators may disable them using the environment override.
Operators may reset lookup cooldowns using the documented administrative procedure.
A healthy remote endpoint does not establish source coverage or successful
local compilation; production and public-service acceptance are separate.
