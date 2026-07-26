#!/bin/sh

set -eu

GO_BIN="${GO:-go}"
GO_LICENSES_BIN="${GO_LICENSES:-go-licenses}"
EXPECTED_VERSION="v1.17.2"
EXPECTED_README_SHA256="61564be196f43db4f4cfe38a6149db60b0f3154062102eb31c2c1c000b8d8737"
EXPECTED_LGPL_SHA256="da7eabb7bafdf7d3ae5e9f223aa5bdc1eece45ac569dc21b3b037520b4464768"
EXPECTED_KECCAK_LICENSE_SHA256="911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad"
EXPECTED_SECP256K1_LICENSE_SHA256="d9e86fe404a67168af493583a2ad300a20447997b2974f026f71872cd4696ca6"
EXPECTED_LIBSECP256K1_LICENSE_SHA256="a735999c7e5649df6fcda6fb06ab97435851c392b1b93494ae8725f37441632f"
EXPECTED_METRICS_LICENSE_SHA256="d2571186acad91c8a3121fb31f1aa5963e82ccd08608d00cef3eb3f3a6c8ad38"
EXPECTED_LIBRARY_HEADER_SHA256="bde834b6926204c664328452c4318a500c856753031f4f17aceb645ce2242117"

if [ "$#" -eq 0 ]; then
  set -- ./...
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  echo "license-check: sha256sum or shasum is required" >&2
  exit 1
}

verify_sha256() {
  path="$1"
  expected="$2"
  label="$3"
  actual="$(sha256_file "$path")"
  if [ "$actual" != "$expected" ]; then
    echo "license-check: $label changed for go-ethereum $EXPECTED_VERSION" >&2
    exit 1
  fi
}

actual_version="$("$GO_BIN" list -m -f '{{.Version}}' github.com/ethereum/go-ethereum)"
module_dir="$("$GO_BIN" list -m -f '{{.Dir}}' github.com/ethereum/go-ethereum)"
if [ "$actual_version" != "$EXPECTED_VERSION" ]; then
  echo "license-check: go-ethereum license review covers $EXPECTED_VERSION, found $actual_version" >&2
  exit 1
fi
if [ -z "$module_dir" ]; then
  echo "license-check: go-ethereum module directory is unavailable" >&2
  exit 1
fi

verify_sha256 "$module_dir/README.md" "$EXPECTED_README_SHA256" "upstream README"
verify_sha256 "$module_dir/COPYING.LESSER" "$EXPECTED_LGPL_SHA256" "upstream LGPL text"
verify_sha256 "$module_dir/crypto/keccak/LICENSE" "$EXPECTED_KECCAK_LICENSE_SHA256" "crypto/keccak license"
verify_sha256 "$module_dir/crypto/secp256k1/LICENSE" "$EXPECTED_SECP256K1_LICENSE_SHA256" "crypto/secp256k1 license"
verify_sha256 "$module_dir/crypto/secp256k1/libsecp256k1/COPYING" "$EXPECTED_LIBSECP256K1_LICENSE_SHA256" "bundled libsecp256k1 license"
verify_sha256 "$module_dir/metrics/LICENSE" "$EXPECTED_METRICS_LICENSE_SHA256" "metrics license"
verify_sha256 "$module_dir/interfaces.go" "$EXPECTED_LIBRARY_HEADER_SHA256" "library license header"

if ! grep -Fq 'all code outside of the `cmd` directory) is licensed under the' "$module_dir/README.md"; then
  echo "license-check: upstream library-license statement is missing" >&2
  exit 1
fi
if ! grep -Fq 'GNU Lesser General Public License' "$module_dir/interfaces.go"; then
  echo "license-check: go-ethereum library source is missing its LGPL header" >&2
  exit 1
fi

dependencies="$("$GO_BIN" list -deps "$@")"
if ! printf '%s\n' "$dependencies" | grep -qx 'github.com/ethereum/go-ethereum'; then
  echo "license-check: the reviewed go-ethereum exception is no longer used" >&2
  exit 1
fi
if printf '%s\n' "$dependencies" | grep -Eq '^github\.com/ethereum/go-ethereum/cmd(/|$)'; then
	echo "license-check: GPL-licensed go-ethereum cmd packages are forbidden" >&2
	exit 1
fi

reported_geth_licenses="$("$GO_LICENSES_BIN" report "$@" 2>/dev/null |
	awk -F, '$1 ~ /^github\.com\/ethereum\/go-ethereum(\/|$)/ {print}' |
	LC_ALL=C sort)"
expected_geth_licenses="$(printf '%s\n' \
	'github.com/ethereum/go-ethereum,https://github.com/ethereum/go-ethereum/blob/v1.17.2/COPYING,GPL-3.0' \
	'github.com/ethereum/go-ethereum/crypto/keccak,https://github.com/ethereum/go-ethereum/blob/v1.17.2/crypto/keccak/LICENSE,BSD-3-Clause' \
	'github.com/ethereum/go-ethereum/crypto/secp256k1,https://github.com/ethereum/go-ethereum/blob/v1.17.2/crypto/secp256k1/LICENSE,BSD-3-Clause' \
	'github.com/ethereum/go-ethereum/metrics,https://github.com/ethereum/go-ethereum/blob/v1.17.2/metrics/LICENSE,BSD-2-Clause-FreeBSD' |
	LC_ALL=C sort)"
if [ "$reported_geth_licenses" != "$expected_geth_licenses" ]; then
	echo "license-check: go-ethereum scanner attribution changed" >&2
	exit 1
fi

# go-licenses attributes the module-root COPYING file to every package even
# though upstream explicitly licenses library packages outside cmd under LGPL.
# The checks above fence this exception to the reviewed module, version,
# upstream texts, independent subpackage licenses, scanner output, source
# header, and non-cmd dependency graph. Everything else remains subject to the
# repository's normal permissive-license allowlist.
exec "$GO_LICENSES_BIN" check "$@" \
  --ignore github.com/ethereum/go-ethereum \
  --allowed_licenses=0BSD,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0
