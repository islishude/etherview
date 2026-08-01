# Third-party notices

Etherview includes software developed by third parties. The dependency lock
files remain the authoritative inventory; this notice calls out dependencies
whose redistribution terms require material beyond Etherview's Apache-2.0
license.

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

## bloomfilter

- Module: `github.com/holiman/bloomfilter/v2`
- Reviewed version: `v2.0.3`
- Copyright: 2014, 2015 Barry Allard
- License: MIT
- Upstream: <https://github.com/holiman/bloomfilter>

The nested v2 module archive does not include the repository-root license file.
The exact reviewed text is checked into this repository and included in the
production image at `/licenses/holiman-bloomfilter-MIT.txt`.
