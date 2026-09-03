# Third-party notices

Etherview includes software developed by third parties. The dependency lock
files remain the authoritative inventory; this notice calls out dependencies
whose redistribution terms require material beyond Etherview's Apache-2.0
license.

## Node SEA and solc-js runtime

- Runtime: Node.js 26.8.1 Single Executable Application
- Bundled protocol dependency: `solc@0.8.36`
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
as an offline verification fixture. The exact upstream license is stored beside
the fixture at
`internal/verify/testdata/geas/sys-asm-eip7002/LICENSE`.

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
- Reviewed version: `v0.1.0`
- Copyright: Protocol Labs
- License: Apache-2.0 OR MIT
- Upstream: <https://github.com/multiformats/go-base36>

The module's permissive-license-stack notice is not classified by the license
scanner. The automated gate pins the exact module checksum, verifies the
upstream and checked-in notice hashes, and includes the reviewed text in the
production image at
`/licenses/multiformats-go-base36-Apache-2.0-OR-MIT.md`.
