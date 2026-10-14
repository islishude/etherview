# Vyper verification

[ADR-0054](../decisions/ADR-0054-vyper-application-lock.md) owns application-locked
PyPI assembly and immutable recipe provenance. It replaces the independently
signed runtime catalog. [ADR-0053](../decisions/ADR-0053-vyper-wasm-distribution.md)
owns explicit dual-Pyodide routing and fresh restricted SEA execution.
[P30](../plans/P30-contract-verification.md) retains the delivery milestones and
native acceptance. The application lock defines the supported versions; Vyper
0.2.0 is retired. Historical 26-version reports are not the current version list.

Native REST Standard JSON and multipart, Etherscan `vyper-json`, and the Web use
one version-aware pipeline. The compiler catalog response includes per-version
capabilities. Omitted options retain upstream defaults; unsupported explicit
options fail rather than being silently ignored. Sources and interfaces remain
bounded inline data. Historical format adapters recover compiler-produced
layout output omitted by older JSON formatters; 0.3.1–0.3.3 export exact typed
AST offsets and lengths. No arbitrary bytecode range is inferred or masked.

Original and whitespace-perturbed sources compile independently. Releases from
0.4.1 carry the five-field integrity footer; older version-only metadata does not
create full-source evidence. Runtime without authenticated source metadata
remains partial, and immutable differences are limited to exact declared ranges.
Constructor ABI, canonical code/block provenance, lease fencing and atomic
publication retain the common verification boundary. Batch, derived and Sourcify
Vyper paths remain excluded.

See [testing](../testing.md) for native matrix and production topology gates and
[operations](../operations.md) for configuration and release procedures.
