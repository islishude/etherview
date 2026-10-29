# Third-party notices

Etherview includes software developed by third parties. The dependency lock
files remain the authoritative inventory; this notice calls out dependencies
whose redistribution terms require material beyond Etherview's Apache-2.0
license.

All maintained license texts and this notice live in `licenses/`. Images copy
that directory directly; builds only verify the reviewed texts against pinned
dependencies and never collect or download notices. The root `LICENSE` symlink
keeps repository license discovery working. Dependency updates must update the
corresponding checked-in notices in the same change.

## Node SEA and solc-js runtime

- Runtime: Node.js 26.12.1 Single Executable Application
- Bundled protocol dependency: `solc@0.8.37`
- Build-only bundler: `esbuild@0.28.2`
- Upstreams: <https://nodejs.org/>, <https://github.com/ethereum/solc-js>, and
  <https://github.com/evanw/esbuild>

The production image includes the Node SEA license at
`/licenses/solcjs-runtime/node-LICENSE.txt`, the bundled npm packages' license
texts, and Debian copyright records for every private ELF library that the
build discovers outside the final distroless base rootfs. The canonical runtime
manifest records each transitive SONAME, provider, package version, resolution
path, and license digest. Base-provided libraries are bound by the final OCI
image and its license inventory; `esbuild` is used only while constructing the
SEA and is not copied into production.

## go-ethereum library

- Module: `github.com/ethereum/go-ethereum`
- Copyright: The go-ethereum Authors
- License for library code outside `cmd/`: GNU Lesser General Public License,
  version 3 or later
- Upstream: <https://github.com/ethereum/go-ethereum>

Etherview imports library packages only. It does not import the separately
GPL-licensed `github.com/ethereum/go-ethereum/cmd` tree. The production image
includes the upstream `COPYING.LESSER` as
`/licenses/go-ethereum-LGPL-3.0-or-later.txt`.

The dependency lock selects the included version. The automated license gate
binds scanner attribution to that resolved version, verifies the exact upstream
license materials, README license section, and representative library source
header by SHA-256, and rejects any `cmd/` dependency before applying a narrow
scanner exception.

The imported dependency graph also contains four directories with their own
permissive license attribution:

- `crypto/bn256`: BSD-3-Clause, Copyright 2012 The Go Authors and 2018 Péter
  Szilágyi; license at
  `/licenses/go-ethereum-crypto-bn256-BSD-3-Clause.txt`.
- `crypto/keccak`: BSD-3-Clause, Copyright 2009 The Go Authors; license at
  `/licenses/go-ethereum-crypto-keccak-BSD-3-Clause.txt`.
- `crypto/secp256k1`: BSD-3-Clause, Copyright The Go Authors, ThePiachu,
  Jeffrey Wilcke, Felix Lange, and Gustav Simonsson; license at
  `/licenses/go-ethereum-crypto-secp256k1-BSD-3-Clause.txt`.
- `metrics`: BSD-2-Clause-FreeBSD, Copyright 2012 Richard Crowley; license at
  `/licenses/go-ethereum-metrics-BSD-2-Clause-FreeBSD.txt`.

The `crypto/secp256k1` source includes libsecp256k1 under the MIT license,
Copyright 2013 Pieter Wuille. Its text is included at
`/licenses/libsecp256k1-MIT.txt`.

## Geas assembler

- Module: `github.com/fjl/geas`
- Reviewed version: `v0.3.3`
- Copyright: The go-ethereum Authors and Geas contributors
- License: GNU Lesser General Public License, version 3
- Upstream: <https://github.com/fjl/geas>

Etherview links the Geas assembler library into its isolated compiler helper.
The automated license gate pins the exact tagged module, forbids replacements,
verifies its module checksum and license text, and includes that text in the
production image at `/licenses/geas-LGPL-3.0.txt`.

## ethereum/sys-asm EIP-7002 test fixture

- Repository: `github.com/ethereum/sys-asm`
- Commit: `83f9801245ff56878a450b5625801101b9a225a1`
- Copyright: 2024 The go-ethereum Authors
- License: MIT
- Upstream: <https://github.com/ethereum/sys-asm>

Etherview retains the withdrawals contract sources and official bytecode only
as an offline verification fixture. The exact upstream license is retained at
`licenses/ethereum-sys-asm-MIT.txt`.

## bloomfilter

- Module: `github.com/holiman/bloomfilter/v2`
- Reviewed version: `v2.0.3`
- Copyright: 2014, 2015 Barry Allard
- License: MIT
- Upstream: <https://github.com/holiman/bloomfilter>

The nested v2 module archive does not include the repository-root license file.
The exact reviewed text is checked into this repository and included in the
production image at `/licenses/holiman-bloomfilter-MIT.txt`.

## go-base36

- Module: `github.com/multiformats/go-base36`
- Reviewed version: `v0.2.0`
- Copyright: Protocol Labs
- License: Apache-2.0 OR MIT
- Upstream: <https://github.com/multiformats/go-base36>

The module's permissive-license-stack notice is not classified by the license
scanner. The automated gate pins the exact module checksum, verifies the
upstream and checked-in notice hashes, and includes the reviewed text in the
production image at
`/licenses/multiformats-go-base36-Apache-2.0-OR-MIT.md`.

## Shared Python WASM Vyper executor

The modern runtime contains Pyodide 314.0.7 (MPL-2.0), CPython 3.14.2
(PSF license), Emscripten 5.0.3 runtime components and PyCryptodome 3.23.0.
The legacy runtime contains Pyodide 0.29.5 (MPL-2.0), CPython 3.13.2
(PSF license), Emscripten 4.0.9 runtime components and PyCryptodome 3.21.0.
Authenticated upstream notices and source URLs are installed under
`/licenses/python-wasm`; `compiler/vyper/wasm/licenses.lock.json` pins their
identities. The source URLs identify exact upstream releases or commits.
The notices also cover zlib 1.3.1, the Emscripten bzip2 1.0.6 port, Expat
2.6.4 / 2.7.3, xz/liblzma 5.2.2, zstd 1.5.7, SQLite 3.39.0,
libffi, hiwire, musl 1.2.5, LLVM compiler-rt/libc++/libc++abi,
HACL* and CPython third-party components including libmpdec 2.5.1.
The source identities and notice digests are recorded in the license lock.
The former custom advisory inventory and vulnerability-disposition records
are retired; current integrity and license checks are described in
`compiler/vyper/README.md`.

Each locally assembled compiler package includes official Vyper source,
its pure Python dependencies and their upstream notices, including setuptools
vendored distributions. `compiler/vyper/versions/index.json` and per-version
lock files bind the official artifacts; the package manifest binds the shipped
archive. Native Python and PyInstaller are not shipped in the production image.

Vyper 0.2.0 and its embedded source archive are no longer distributed.
Supported compiler/dependency archives are fetched by application-locked PyPI
digest; no independently hosted compiler distribution or signing key is required.

## Hey API generated Fetch runtime

The generated Web SDK bundles runtime support from `@hey-api/openapi-ts@0.99.0`
(MIT, Copyright (c) Hey API). Its license is retained in
`licenses/hey-api-MIT.txt` and included by the production image's licenses copy.
The generator version and package integrity are pinned in `pnpm-lock.yaml`.

## Preview application contracts and Bundler

The separate Preview Bundler image contains the official Rundler 0.11.0 binary
from https://github.com/alchemyplatform/rundler/releases/tag/v0.11.0.
Its executable is GPL-3.0-or-later and its libraries are LGPL-3.0-or-later.
The matching source is available at that release tag. Both license texts are
retained in `preview-rundler-GPL-3.0.txt` and `preview-rundler-LGPL-3.0.txt`
and shipped under `/app/licenses/` in the image. The binary is copied from the
official `alchemyplatform/rundler` image, whose version tag is set in
`preview/bundler.Dockerfile` for both Linux ARM64 and AMD64.

Preview genesis embeds account-abstraction v0.9.0 contracts (commit
b36a1ed52ae00da6f8a4c8d50181e2877e4fa410, GPL-3.0) using canonical creation
inputs with immutable source and digest provenance in
`preview/contracts/upstream/account-abstraction-v09.json`.
Those construction inputs originated in Alto's deployment constants; Alto and
its simulation helpers are not included in the runtime image or genesis.
Account-abstraction source is available from the official
https://github.com/eth-infinitism/account-abstraction repository at that commit.

ENS contracts 1.7.0 are MIT licensed (`preview-ens-MIT.txt`). Preview vendors the
required Solidity import closure from the official GitHub commit
9b034936a42f462fc04bc0a929a419ede5e18d59 and compiles it with pinned solc and
OpenZeppelin packages. The ENS npm package is not installed because its package
name is covered by GHSA-58x9-4xmp-8mg5. PreviewRegistrarController changes only
the upstream controller name and `.eth` node/suffix to `.test`. The exact source
snapshot and compiler settings are checked in. Genesis verification compiles
these inputs through the ordinary API worker; startup does not fetch sources.

ENS buffer 0.1.3 is BSD-2-Clause; `npm-clarifications.json` pins its reviewed
license-file hash where the scanner otherwise reports only BSD.

### Preview verification source bundles

`preview/verification` retains inline compiler inputs for all genesis contracts.
The account-abstraction and ENS license notices above also cover these source
copies; OpenZeppelin imports (including 5.0.2 for the canonical SimpleAccount
factory) retain their MIT SPDX and copyright comments.
`Multicall3.json` retains the MIT-licensed mds1 Multicall3 source and its full
license text in source comments. `ERC1820Registry.json` retains Jordi Baylina's
ERC-1820 source and its CC0 dedication. Each snapshot records its source URL.
The `sys-asm.json` bundle pins ethereum/sys-asm revision
f808a29f280f0b844d83fec592d3e1c592d1c92a and retains its complete MIT license.
Its BeaconRoots source explicitly uses PUSH3 for the deployed EIP-4788 constant;
this adaptation is recorded alongside the upstream revision.
