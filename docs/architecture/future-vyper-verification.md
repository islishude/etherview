# Vyper verification

[ADR-0049](../decisions/ADR-0049-dynamic-vyper-runtimes.md) owns the signed dynamic
runtime catalog and preserves ADR-0047's API-owned subprocess execution.
[P30-T107–P30-T110](../plans/P30-contract-verification.md) track implementation and
native acceptance. The version build index includes all 26 currently identified
non-withdrawn stable versions, including official-source-only 0.2.0.

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
[operations](../operations.md#signed-vyper-runtime-catalog) for configuration.
