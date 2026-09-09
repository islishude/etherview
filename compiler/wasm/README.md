# Unified WASM compiler migration

[ADR-0048](../../docs/decisions/ADR-0048-wazero-compiler-executor.md) owns the
approved architecture. P30-T101–P30-T106 own staged implementation and final
cutover. This branch builds the unified candidate image; deployment cutover and
removal of the retained development reference executors require T106 acceptance.

## Solidity baseline

`solc-baseline.json` is the unmodified official emscripten-wasm32 catalog
snapshot selected for migration: 105 builds from 0.3.6 through 0.8.36, including
prereleases. All artifacts must match their recorded SHA-256 before extraction
or reference execution. Never substitute npm bytes that differ from the catalog.

`reference.cjs` uses the exact repository solc wrapper and is development-only.
It records reference compiler failures and unexpected stdout separately, so
historically broken execution is not mistaken for supported compiler JSON.
A version whose runtime identity disagrees with the catalog must remain rejected,
including the legacy 0.3.6 version-string mismatch.

Fetch or validate the fixed baseline with `python3 compiler/wasm/fetch_baseline.py
/absolute/artifacts`. An explicit `--base-url` can select a clean HTTPS mirror
with the same directory layout; there is no automatic trust/source fallback.

With the authenticated artifact files in a directory, run from the repository:

```sh
SOLC_BASELINE_DIR=/absolute/artifacts go test ./internal/wasmcompiler -run 'TestExtractCatalog|TestCatalogHostCoverage' -count=1
SOLC_BASELINE_DIR=/absolute/artifacts go test ./internal/wasmcompiler -run TestSolcCatalogDifferential -count=1 -timeout=30m
SOLC_TEST_CASE=invalid SOLC_BASELINE_DIR=/absolute/artifacts go test ./internal/wasmcompiler -run TestSolcCatalogDifferential -count=1 -timeout=30m
SOLC_TEST_CASE=missing SOLC_BASELINE_DIR=/absolute/artifacts go test ./internal/wasmcompiler -run TestSolcCatalogDifferential -count=1 -timeout=30m
SOLC_TEST_CASE=yul SOLC_BASELINE_DIR=/absolute/artifacts go test ./internal/wasmcompiler -run TestSolcCatalogDifferential -count=1 -timeout=30m
```

The outer test timeout covers the complete catalog, not a relaxed per-compilation
timeout. Each differential case retains a one-minute context. A missing artifact,
checksum mismatch, unsupported ABI, mismatched output or unexpected success is a
failure, not a skipped version. Tests without the explicit artifact directory
skip the external full-catalog gate and are not full-catalog acceptance evidence.

## Provenance of adapters

The Emscripten host and legacy Standard JSON adapters follow the glue shipped
inside the checksum-authenticated official compiler artifacts and the MIT-licensed
`solc@0.8.36` wrapper/translation code. `solc-js-LICENSE.txt` preserves its license.
The Go extractor handles data-URI WASM and block-LZ4-packed WASM without executing
JavaScript; derived helper thunks do not modify compiler WASM bytes.

Indirect calls use a separate WASM module importing the original function table.
Legacy exported dynCall trampolines remain authoritative where present, including
for C++ destructors. Newer callbacks are installed in the guest function table;
no table entries are guessed and no wazero internal APIs are imported.

Code generation uses at most four wazero compilation workers, capped by the
process Go CPU concurrency. Verification worker count and fresh-process/guest
isolation remain unchanged; no native-code cache is introduced. The exported
experimental compilation-worker API is pinned to wazero 1.12.0.

## Local performance evidence

`benchmark.py` compares three sequential fresh-process samples per backend and
six inputs with two workers. It uses the same inputs and 120-second, 5 MiB input,
64 MiB output limits for each backend, verifies complete JSON equality, and
records end-to-end invocation time and per-child peak RSS with macOS `time -l`.
Invocation time includes extraction, JIT or interpreter startup and compilation;
it does not isolate pure compiler CPU time or measure total process-tree RSS.
The original SEA and native Vyper executors are development references only.
This local comparison does not close production capacity/native architecture
acceptance, and its results must not be presented as an assumed speedup.
