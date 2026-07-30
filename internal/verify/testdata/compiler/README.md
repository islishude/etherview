# Solidity compiler fixtures

These fixtures were generated from the official `solc@0.8.30` npm package.
The compiler reported `0.8.30+commit.73712a01.Emscripten.clang`; the packed
package SHA-256 was
`99fc3d72fadb57f7be4be1d416a4cc6f839bef4c9ab6e7517770ac8f8e92fb9b`.

`solidity/input.linked.ipfs.json` is a three-source Standard JSON input with
one external library, one inline library, and two immutables. The fully linked
output has empty creation and runtime `linkReferences`; the unlinked variant
retains an unresolved placeholder and is a negative fixture.

The runtime immutable references are:

- AST ID 37 (`owner`): `{start:130,length:32}` and
  `{start:214,length:32}`.
- AST ID 39 (`seed`): `{start:72,length:32}` and
  `{start:308,length:32}`.

The `runtime.onchain.*.hex` files start from official compiler output and fill
only those declared immutable ranges. The IPFS/source-comment pair has
identical executable creation and runtime cores with different valid terminal
CBOR metadata. The IPFS/bzzr1 runtime pair also shares an executable core, but
its creation programs differ because the metadata footer lengths differ.

`input.no-cbor.json` and `output.no-cbor.json` were compiled with
`metadata.appendCBOR=false`; matcher regressions use them to ensure arbitrary
valid-looking CBOR suffixes are not stripped.

`COMMIT_SHA256SUMS` covers every committed fixture and this report.
