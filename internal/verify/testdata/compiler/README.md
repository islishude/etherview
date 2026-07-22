# Etherview P30-T03 compiler fixtures

These files were generated on 2026-07-22 from version-pinned official compiler
packages in an isolated temporary workspace, then copied here without compiler
caches or installed dependencies.

## Compiler acquisition and versions

Solidity was acquired from the official `solc` npm package:

```sh
env npm_config_cache=/tmp/etherview-t03-fixtures/npm-cache npm pack solc@0.8.30
env npm_config_cache=/tmp/etherview-t03-fixtures/npm-cache npm install --prefix /tmp/etherview-t03-fixtures/tooling/npm --ignore-scripts --no-audit --no-fund solc@0.8.30
node /tmp/etherview-t03-fixtures/tooling/compile-solidity.js INPUT OUTPUT
```

- Reported version: `0.8.30+commit.73712a01.Emscripten.clang`
- `solc-0.8.30.tgz` SHA-256:
  `99fc3d72fadb57f7be4be1d416a4cc6f839bef4c9ab6e7517770ac8f8e92fb9b`

Vyper was acquired from the official PyPI packages through `uvx`:

```sh
env UV_CACHE_DIR=/tmp/etherview-t03-fixtures/uv-cache \
  UV_TOOL_DIR=/tmp/etherview-t03-fixtures/uv-tools \
  UV_TOOL_BIN_DIR=/tmp/etherview-t03-fixtures/uv-bin \
  UV_PYTHON_INSTALL_DIR=/tmp/etherview-t03-fixtures/uv-python \
  uvx --python 3.12 --from vyper==VERSION vyper --version
```

Reported versions:

- `0.4.3+commit.bff19ea2`
- `0.4.1+commit.8a93dd27`
- `0.4.0+commit.e9db8d9`
- `0.3.10+commit.9136169`
- `0.3.9+commit.66b9670`
- `0.3.4+commit.f31f0ec`

Modern Vyper outputs were generated with:

```sh
env UV_CACHE_DIR=/tmp/etherview-t03-fixtures/uv-cache \
  UV_TOOL_DIR=/tmp/etherview-t03-fixtures/uv-tools \
  UV_TOOL_BIN_DIR=/tmp/etherview-t03-fixtures/uv-bin \
  UV_PYTHON_INSTALL_DIR=/tmp/etherview-t03-fixtures/uv-python \
  uvx --python 3.12 --from vyper==0.4.3 vyper --standard-json \
  --pretty-json -o OUTPUT INPUT
```

Legacy outputs were generated with the corresponding version in this command:

```sh
env UV_CACHE_DIR=/tmp/etherview-t03-fixtures/uv-cache \
  UV_TOOL_DIR=/tmp/etherview-t03-fixtures/uv-tools \
  UV_TOOL_BIN_DIR=/tmp/etherview-t03-fixtures/uv-bin \
  UV_PYTHON_INSTALL_DIR=/tmp/etherview-t03-fixtures/uv-python \
  uvx --python 3.12 --from vyper==VERSION vyper \
  -f bytecode,bytecode_runtime -o OUTPUT SOURCE
```

## Solidity fixture facts

`solidity/input.linked.ipfs.json` is a three-source Standard JSON input. The
target imports an external library and an inlined library and declares two
immutables. `settings.libraries` binds the external library to
`0x1111111111111111111111111111111111111111`.

The fully linked official output has empty creation and runtime
`linkReferences`. The otherwise identical unlinked output retains one 20-byte
reference at creation offset 533 and runtime offset 366 and contains an
unresolved `__$...$__` placeholder. This supports rejecting every non-empty
reference map after Standard JSON preparation has required complete linking.

The runtime immutable references are:

- AST ID 37 (`owner`): `{start:130,length:32}` and
  `{start:214,length:32}`.
- AST ID 39 (`seed`): `{start:72,length:32}` and
  `{start:308,length:32}`.

`runtime.onchain.*.hex` is synthetic: it starts from the real compiler runtime
template and fills only those declared ranges with owner `0x22...22` and seed
42. The base IPFS and source-comment-only IPFS outputs form the positive
metadata-only pair: both creation and runtime executable cores are identical
after strict terminal-CBOR stripping, both footers are real compiler output,
and both 51-byte payloads differ. Their creation and runtime lengths remain
815 and 648 bytes respectively.

The IPFS and bzzr1 runtime pair also has an identical executable core and two
different, real compiler-generated CBOR map footers (53 and 52 bytes including
their two-byte length fields). It is intentionally a negative creation case:
the one-byte footer-length shift changes creation-program constants, so strict
terminal-footer stripping leaves different creation cores.

`input.no-cbor.json` and `output.no-cbor.json` are an official solc 0.8.30
compile with `metadata.appendCBOR=false`. The exact creation and runtime
bytecodes contain no compiler footer. Matcher tests append two different,
syntactically valid CBOR-map byte sequences as executable suffixes and prove
that the disabled setting prevents them from being treated as metadata.

## Vyper 0.4.3 fixture facts

The Standard JSON input contains `contracts/Target.vy` and the imported inline
`interfaces/Remote.vyi`. It explicitly uses `settings.search_paths: ["."]`.
Official `JSONInputBundle` resolves only its in-memory source map and never
uses the filesystem. An empty search-path list is invalid because it prevents
even the virtual target from being searched.

The output uses the Vyper shapes: `metadata` is an object, and immutable
declarations are under `layout.code_layout`. The real creation footer is a
55-byte inclusive-length CBOR tuple:

```text
[integrity(32 bytes), 345, [4], 64, {"vyper":[0,4,3]}] || 0x0037
```

The recompiled runtime is 345 bytes and carries no footer. The synthetic
deployed runtime appends the 64 immutable bytes independently declared by both
the creation tuple and `layout.code_layout`. No EVM deployment was run.

The source-comment variant is a real metadata-only creation pair: executable
cores and tuple shape/length are identical, while only the integrity element
changes. Disabling bytecode metadata is not a metadata-only pair because two
constructor-argument-location constants in the creation program also change.

## Vyper Standard JSON version matrix

`vyper-version-matrix/input-VERSION.json` and `output-VERSION.json` are small
official Standard JSON compilations produced with the corresponding pinned
0.3.10, 0.4.0, and 0.4.1 compiler. The source is a single exact target with no
immutables, so its 39-byte runtime can be matched directly while the output
schema and creation auxdata boundaries remain visible:

- 0.3.10 returns an object-valued `metadata`, no `layout`, and the four-element
  creation tuple `[39, [], 0, {"vyper":[0,3,10]}] || 0x0012`.
- 0.4.0 returns an object-valued `metadata`, no `layout`, and the four-element
  creation tuple `[39, [], 0, {"vyper":[0,4,0]}] || 0x0012`.
- 0.4.1 returns an object-valued `metadata`, an empty but present `layout`, and
  the five-element creation tuple
  `[integrity(32 bytes), 39, [], 0, {"vyper":[0,4,1]}] || 0x0034`.

The matcher fixture test selects the exact target from each real output,
decodes those tuple shapes, and performs exact creation and runtime matches.

## Legacy Vyper boundaries

The bytecodes were generated by the exact official compilers above. Their
installed official `vyper/ir/compile_ir.py` source defines these formats:

- 0.3.4 appends a fixed 11-byte CBOR version map and no length field to runtime.
- 0.3.9 appends that map plus a two-byte payload length excluding the field.
- 0.3.10 moves metadata to creation only and appends a four-element tuple plus
  a two-byte total length including the field.

The generated minimal auxdata values are:

- 0.3.4: `0xa165767970657283000304`
- 0.3.9: `0xa165767970657283000309000b`
- 0.3.10: `0x8418278000a16576797065728300030a0012`

`cases.json` in the Solidity, Vyper 0.4.3, and legacy Vyper fixture directories
records machine-readable sizes, schema facts, and which deployed values were
synthetically derived.
`COMMIT_SHA256SUMS` covers every committed input, output, derived case, and this
report; caches, helper scripts, archives, and installed compiler dependencies
are intentionally excluded.
