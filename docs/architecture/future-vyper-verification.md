# Vyper verification

The maintained Vyper boundary is [ADR-0047](../decisions/ADR-0047-pinned-vyper-executor.md).
Implementation and acceptance evidence belong to
[P30-T95–P30-T99](../plans/P30-contract-verification.md).

## Fixed compiler and executor

Only official Vyper 0.4.3 is supported. Its Python wheel is identical across
architectures; the CPython 3.13.15/PyInstaller 6.22.2 helper, native dependencies
and complete runtime manifest are built for the host architecture. The API owns
execution and publication without a remote compiler service, Pyodide, runtime
package installation or catalog downloads. The deployed executor identity is
immutable for every leased job, so bound work must drain before an upgrade.

## Inputs and matching

Native Standard JSON and multipart submissions require `target_file`. Submitted
modules and interfaces resolve through JSONInputBundle, never the host source
filesystem. Optimization uses `none`, `gas`, or `codesize`; stable compiler
settings and server-selected outputs are bounded. Experimental backends,
debug, storage-layout overrides, Solidity libraries and optimization runs are
rejected. Etherscan `vyper-json` maps its contract name to the same exact target.

The verifier compiles original and whitespace-perturbed sources independently.
Vyper 0.4.3 creation metadata is a five-field CBOR tuple whose terminal length
includes its own two bytes. Runtime metadata is absent; runtime equality alone
therefore remains partial. Immutable data is an exact compiler-declared suffix,
with contiguous, non-overlapping layout ranges and no undeclared wildcard.
Constructor arguments retain canonical ABI encoding. Address publication uses
canonical code observations, exact block identity and the existing transaction
and lease fences. Factory-derived and batch Vyper verification are excluded.

See [testing](../testing.md) for production topology gates and
[operations](../operations.md) for runtime upgrades and identity failures.
