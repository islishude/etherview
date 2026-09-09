# P30-T100: wazero compiler feasibility probe

This is an isolated experiment, not a production executor. Its nested Go module
keeps wazero out of the application dependency graph. ADR-0031, ADR-0040 and
ADR-0047 still own production execution. No API, schema, job provenance,
container, or role behavior changes here.

## Reproduce

Requires the repository's existing compiler npm installs and the pinned Vyper
venv created by `make compiler-install`, plus the Hardhat fixture's solc install.
Use the repository root for these commands:

```sh
python3 compiler/wazero-probe/prepare.py /tmp/etherview-wazero-probe
(cd compiler/wazero-probe && go build -o /tmp/etherview-wazero-probe/probe .)
python3 compiler/wazero-probe/check.py /tmp/etherview-wazero-probe/probe /tmp/etherview-wazero-probe "$PWD"
python3 compiler/wazero-probe/boundaries.py /tmp/etherview-wazero-probe/probe /tmp/etherview-wazero-probe "$PWD"
```

`prepare.py` downloads the pinned CPython 3.13.15/WASI SDK 24 build from
[brettcannon/cpython-wasi-build](https://github.com/brettcannon/cpython-wasi-build/releases/tag/v3.13.15).
Its recorded archive hash pins this experiment's bytes; it is not an independent
reproducible-build attestation. Python packages come from the installed pinned
venv; `inputs.json` records copied source hashes. Native `.so` files are excluded.
The official Solidity 0.8.30 and 0.8.36 artifacts are checked against the
[official WASM catalog](https://binaries.soliditylang.org/emscripten-wasm32/list.json).
Existing downloaded files are reused for offline replay. Delete only the
experiment's catalog file to request a fresh catalog snapshot.

`extract.cjs` executes trusted soljson JavaScript once in Node during preparation,
extracting its unmodified WASM bytes and compressed import/export name map.
The Node VM context is not a sandbox. Never give it arbitrary/untrusted JavaScript.
Compilation then runs in Go/wazero without Node. The matrix also runs Node as an
independent reference for newly created Solidity/Yul cases. A Node-free extractor
and support for historical soljson packaging are outside this experiment.

`check.py` writes `report.json` and `results/` under the chosen work directory.
It deliberately records unsupported cases as failures: a successful script exit
means the diagnostic matrix completed, not that every compiler path passed.
`boundaries.py` asserts the observed filesystem, memory, cancellation and hash
checks, writing `boundaries.json`. Detailed measured results belong to the
[P30 evidence](../../docs/plans/P30-contract-verification.md#p30-t100--wazero-feasibility).

## Deliberate prototype boundaries

- Solidity host adapters cover the exercised successful compilation paths,
  including indirect calls and legalized i64 calls. Unknown host calls panic;
  Emscripten C++ exception handling is not implemented. A null compiler callback
  rejects absent imports but has different diagnostic text from `solc/wrapper`.
  This is not a compatible replacement for the production executor yet.
- CPython WASI cannot import the unchanged PyCryptodome dependency through
  `_ctypes`. `vyper_probe.py` explicitly substitutes `keccak_probe.py`, a small
  diagnostic Keccak-256 implementation. Vyper sources remain unchanged.
  `immutables` and `cbor2` use their own upstream pure-Python fallbacks.
  The hash replacement is not an audited or selected production dependency.
- WASI exposes only the Python distribution and probe package tree read-only,
  explicit Python path, standard streams, clock and entropy. It receives no
  inherited host environment or host root filesystem mapping. This does not
  constitute a complete malicious-code sandbox audit.
- The runner limits WASM linear memory to 512 MiB and closes modules on context
  timeout. This does not cap total process RSS. The outer diagnostic driver has
  a subprocess timeout; production-grade bounded streaming, error redaction,
  process-group handling and worker integration remain absent.
- Compiled WASM code is cached on disk outside guest mounts; each invocation
  still instantiates a fresh module. Recorded wall times include subprocess,
  module loading, compilation/cache lookup and execution; they are not a
  controlled performance benchmark. No speed or RSS improvement is claimed.
- Testing is on the local macOS ARM64 host. No native Linux AMD64/ARM64 image,
  production topology, workload, full compiler-version matrix, or publication
  acceptance is claimed.
