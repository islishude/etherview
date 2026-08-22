#!/bin/sh

set -eu

GO_BIN="${GO:-go}"
GO_LICENSES_BIN="${GO_LICENSES:-go-licenses}"
EXPECTED_README_LICENSE_SECTION_SHA256="c8bbbd0dfe29433f72d99b6d2f8a9db3286016fa4ed0b831420b632cf8d907b2"
EXPECTED_LGPL_SHA256="da7eabb7bafdf7d3ae5e9f223aa5bdc1eece45ac569dc21b3b037520b4464768"
EXPECTED_BN256_LICENSE_SHA256="9af6283f0c25b7eab8bc4a9d61fbc8b9dc31c82251612369e3472c53ffef55ca"
EXPECTED_KECCAK_LICENSE_SHA256="911f8f5782931320f5b8d1160a76365b83aea6447ee6c04fa6d5591467db9dad"
EXPECTED_SECP256K1_LICENSE_SHA256="d9e86fe404a67168af493583a2ad300a20447997b2974f026f71872cd4696ca6"
EXPECTED_LIBSECP256K1_LICENSE_SHA256="a735999c7e5649df6fcda6fb06ab97435851c392b1b93494ae8725f37441632f"
EXPECTED_METRICS_LICENSE_SHA256="d2571186acad91c8a3121fb31f1aa5963e82ccd08608d00cef3eb3f3a6c8ad38"
EXPECTED_LIBRARY_LICENSE_HEADER_SHA256="f51c38bc07a6bd41262ce3b0a65a9bef3d8efff92e60f8fb2ccc1d35742be5b6"
EXPECTED_BLOOMFILTER_VERSION="v2.0.3"
EXPECTED_BLOOMFILTER_LICENSE_SHA256="f9e93a3c8d61e448bffac8f9c1d45b6418494ab2b683b13425158b9826dab048"
BLOOMFILTER_LICENSE_PATH="licenses/holiman-bloomfilter-MIT.txt"
EXPECTED_BASE36_VERSION="v0.1.0"
EXPECTED_BASE36_SUM="h1:JR6TyF7JjGd3m6FbLU2cOxhC0Li8z8dLNGQ89tUg4F4="
EXPECTED_BASE36_LICENSE_SHA256="b5f1f21b1eea5d68df829130c545b431e9c277dd73f68a777e641e44c7fa2f0e"
BASE36_LICENSE_PATH="licenses/multiformats-go-base36-Apache-2.0-OR-MIT.md"
EXPECTED_GEAS_VERSION="v0.3.3"
EXPECTED_GEAS_SUM="h1:CtVkRXysF+1gf1L0MgisG4vcr/Zv/uf8ukq/uqiUEUs="
EXPECTED_GEAS_LICENSE_SHA256="da7eabb7bafdf7d3ae5e9f223aa5bdc1eece45ac569dc21b3b037520b4464768"
EXPECTED_SYS_ASM_LICENSE_SHA256="23f18e03dc49df91622fe2a76176497404e46ced8a715d9d2b67a7446571cca3"
SYS_ASM_LICENSE_PATH="internal/verify/testdata/geas/sys-asm-eip7002/LICENSE"

if [ "$#" -eq 0 ]; then
  set -- ./...
fi

sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{print $1}'
    return
  fi
  echo "license-check: sha256sum or shasum is required" >&2
  exit 1
}

sha256_file() {
  sha256_stream <"$1"
}

verify_sha256_value() {
  actual="$1"
  expected="$2"
  label="$3"
  if [ "$actual" != "$expected" ]; then
    echo "license-check: $label changed for go-ethereum $actual_version" >&2
    exit 1
  fi
}

verify_sha256() {
  path="$1"
  expected="$2"
  label="$3"
  actual="$(sha256_file "$path")"
  verify_sha256_value "$actual" "$expected" "$label"
}

actual_version="$("$GO_BIN" list -m -f '{{.Version}}' github.com/ethereum/go-ethereum)"
module_dir="$("$GO_BIN" list -m -f '{{.Dir}}' github.com/ethereum/go-ethereum)"
module_replacement="$("$GO_BIN" list -m -f '{{if .Replace}}{{.Replace.Path}}{{end}}' github.com/ethereum/go-ethereum)"
if [ -n "$module_replacement" ]; then
  echo "license-check: go-ethereum module replacements are forbidden" >&2
  exit 1
fi
if ! printf '%s\n' "$actual_version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "license-check: go-ethereum must resolve to an official tagged release" >&2
  exit 1
fi
if [ -z "$module_dir" ] || [ ! -d "$module_dir" ]; then
  echo "license-check: go-ethereum module directory is unavailable" >&2
  exit 1
fi

readme_license_section_sha256="$(
  awk '
    $0 == "## License" { capture = 1 }
    capture && $0 ~ /^## / && $0 != "## License" { exit }
    capture { print }
  ' "$module_dir/README.md" | sha256_stream
)"
verify_sha256_value "$readme_license_section_sha256" "$EXPECTED_README_LICENSE_SECTION_SHA256" "upstream README license section"

library_license_header_sha256="$(awk 'NR == 1, /^$/ { print }' "$module_dir/interfaces.go" | sha256_stream)"
verify_sha256_value "$library_license_header_sha256" "$EXPECTED_LIBRARY_LICENSE_HEADER_SHA256" "library license header"

verify_sha256 "$module_dir/COPYING.LESSER" "$EXPECTED_LGPL_SHA256" "upstream LGPL text"
verify_sha256 "$module_dir/crypto/bn256/LICENSE" "$EXPECTED_BN256_LICENSE_SHA256" "crypto/bn256 license"
verify_sha256 "$module_dir/crypto/keccak/LICENSE" "$EXPECTED_KECCAK_LICENSE_SHA256" "crypto/keccak license"
verify_sha256 "$module_dir/crypto/secp256k1/LICENSE" "$EXPECTED_SECP256K1_LICENSE_SHA256" "crypto/secp256k1 license"
verify_sha256 "$module_dir/crypto/secp256k1/libsecp256k1/COPYING" "$EXPECTED_LIBSECP256K1_LICENSE_SHA256" "bundled libsecp256k1 license"
verify_sha256 "$module_dir/metrics/LICENSE" "$EXPECTED_METRICS_LICENSE_SHA256" "metrics license"

bloomfilter_version="$("$GO_BIN" list -m -f '{{.Version}}' github.com/holiman/bloomfilter/v2)"
if [ "$bloomfilter_version" != "$EXPECTED_BLOOMFILTER_VERSION" ]; then
  echo "license-check: bloomfilter license review covers $EXPECTED_BLOOMFILTER_VERSION, found $bloomfilter_version" >&2
  exit 1
fi
if [ ! -f "$BLOOMFILTER_LICENSE_PATH" ]; then
  echo "license-check: checked-in bloomfilter license is missing" >&2
  exit 1
fi
actual_bloomfilter_license_sha256="$(sha256_file "$BLOOMFILTER_LICENSE_PATH")"
if [ "$actual_bloomfilter_license_sha256" != "$EXPECTED_BLOOMFILTER_LICENSE_SHA256" ]; then
  echo "license-check: bloomfilter license changed for $EXPECTED_BLOOMFILTER_VERSION" >&2
  exit 1
fi

base36_version="$("$GO_BIN" list -m -f '{{.Version}}' github.com/multiformats/go-base36)"
base36_sum="$("$GO_BIN" list -m -f '{{.Sum}}' github.com/multiformats/go-base36)"
base36_module_dir="$("$GO_BIN" list -m -f '{{.Dir}}' github.com/multiformats/go-base36)"
base36_replacement="$("$GO_BIN" list -m -f '{{if .Replace}}{{.Replace.Path}}{{end}}' github.com/multiformats/go-base36)"
if [ "$base36_version" != "$EXPECTED_BASE36_VERSION" ] || [ "$base36_sum" != "$EXPECTED_BASE36_SUM" ]; then
  echo "license-check: go-base36 review covers $EXPECTED_BASE36_VERSION at its exact module sum" >&2
  exit 1
fi
if [ -n "$base36_replacement" ] || [ -z "$base36_module_dir" ] || [ ! -d "$base36_module_dir" ]; then
  echo "license-check: go-base36 module replacement or missing module directory" >&2
  exit 1
fi
if [ "$(sha256_file "$base36_module_dir/LICENSE.md")" != "$EXPECTED_BASE36_LICENSE_SHA256" ]; then
  echo "license-check: upstream go-base36 license changed for $EXPECTED_BASE36_VERSION" >&2
  exit 1
fi
if [ ! -f "$BASE36_LICENSE_PATH" ] ||
   [ "$(sha256_file "$BASE36_LICENSE_PATH")" != "$EXPECTED_BASE36_LICENSE_SHA256" ]; then
  echo "license-check: checked-in go-base36 license changed for $EXPECTED_BASE36_VERSION" >&2
  exit 1
fi

geas_version="$("$GO_BIN" list -m -f '{{.Version}}' github.com/fjl/geas)"
geas_sum="$("$GO_BIN" list -m -f '{{.Sum}}' github.com/fjl/geas)"
geas_module_dir="$("$GO_BIN" list -m -f '{{.Dir}}' github.com/fjl/geas)"
geas_replacement="$("$GO_BIN" list -m -f '{{if .Replace}}{{.Replace.Path}}{{end}}' github.com/fjl/geas)"
if [ "$geas_version" != "$EXPECTED_GEAS_VERSION" ] || [ "$geas_sum" != "$EXPECTED_GEAS_SUM" ]; then
  echo "license-check: Geas review covers $EXPECTED_GEAS_VERSION at its exact module sum" >&2
  exit 1
fi
if [ -n "$geas_replacement" ] || [ -z "$geas_module_dir" ] || [ ! -d "$geas_module_dir" ]; then
  echo "license-check: Geas module replacement or missing module directory" >&2
  exit 1
fi
if [ "$(sha256_file "$geas_module_dir/LICENSE")" != "$EXPECTED_GEAS_LICENSE_SHA256" ]; then
  echo "license-check: Geas license changed for $EXPECTED_GEAS_VERSION" >&2
  exit 1
fi
if [ ! -f "$SYS_ASM_LICENSE_PATH" ] ||
   [ "$(sha256_file "$SYS_ASM_LICENSE_PATH")" != "$EXPECTED_SYS_ASM_LICENSE_SHA256" ]; then
  echo "license-check: pinned ethereum/sys-asm fixture license changed" >&2
  exit 1
fi

dependencies="$("$GO_BIN" list -deps "$@")"
if ! printf '%s\n' "$dependencies" | grep -qx 'github.com/ethereum/go-ethereum'; then
  echo "license-check: the reviewed go-ethereum exception is no longer used" >&2
  exit 1
fi
if ! printf '%s\n' "$dependencies" | grep -qx 'github.com/holiman/bloomfilter/v2'; then
  echo "license-check: the reviewed bloomfilter exception is no longer used" >&2
  exit 1
fi
if ! printf '%s\n' "$dependencies" | grep -qx 'github.com/multiformats/go-base36'; then
  echo "license-check: the reviewed go-base36 exception is no longer used" >&2
  exit 1
fi
if ! printf '%s\n' "$dependencies" | grep -qx 'github.com/fjl/geas/asm'; then
  echo "license-check: the reviewed Geas exception is no longer used" >&2
  exit 1
fi
if printf '%s\n' "$dependencies" | grep -Eq '^github\.com/ethereum/go-ethereum/cmd(/|$)'; then
	echo "license-check: GPL-licensed go-ethereum cmd packages are forbidden" >&2
	exit 1
fi

reported_licenses="$("$GO_LICENSES_BIN" report "$@" 2>/dev/null)"
reported_geth_licenses="$(printf '%s\n' "$reported_licenses" |
	awk -F, '$1 ~ /^github\.com\/ethereum\/go-ethereum(\/|$)/ {print}' |
	LC_ALL=C sort)"
expected_geth_licenses="$(printf '%s\n' \
	"github.com/ethereum/go-ethereum,https://github.com/ethereum/go-ethereum/blob/$actual_version/COPYING,GPL-3.0" \
	"github.com/ethereum/go-ethereum/crypto/bn256,https://github.com/ethereum/go-ethereum/blob/$actual_version/crypto/bn256/LICENSE,BSD-3-Clause" \
	"github.com/ethereum/go-ethereum/crypto/keccak,https://github.com/ethereum/go-ethereum/blob/$actual_version/crypto/keccak/LICENSE,BSD-3-Clause" \
	"github.com/ethereum/go-ethereum/crypto/secp256k1,https://github.com/ethereum/go-ethereum/blob/$actual_version/crypto/secp256k1/LICENSE,BSD-3-Clause" \
	"github.com/ethereum/go-ethereum/metrics,https://github.com/ethereum/go-ethereum/blob/$actual_version/metrics/LICENSE,BSD-2-Clause" |
	LC_ALL=C sort)"
if [ "$reported_geth_licenses" != "$expected_geth_licenses" ]; then
	echo "license-check: go-ethereum scanner attribution changed" >&2
	exit 1
fi

reported_bloomfilter_license="$(printf '%s\n' "$reported_licenses" |
	awk -F, '$1 == "github.com/holiman/bloomfilter/v2" {print}')"
expected_bloomfilter_license='github.com/holiman/bloomfilter/v2,Unknown,Unknown'
if [ "$reported_bloomfilter_license" != "$expected_bloomfilter_license" ]; then
	echo "license-check: bloomfilter scanner attribution changed" >&2
	exit 1
fi

reported_base36_license="$(printf '%s\n' "$reported_licenses" |
	awk -F, '$1 == "github.com/multiformats/go-base36" {print}')"
expected_base36_license='github.com/multiformats/go-base36,Unknown,Unknown'
if [ "$reported_base36_license" != "$expected_base36_license" ]; then
	echo "license-check: go-base36 scanner attribution changed" >&2
	exit 1
fi

reported_geas_license="$(printf '%s\n' "$reported_licenses" |
	awk -F, '$1 == "github.com/fjl/geas" {print}')"
expected_geas_license="github.com/fjl/geas,https://github.com/fjl/geas/blob/$EXPECTED_GEAS_VERSION/LICENSE,LGPL-3.0"
if [ "$reported_geas_license" != "$expected_geas_license" ]; then
	echo "license-check: Geas scanner attribution changed" >&2
	exit 1
fi

# go-licenses attributes the module-root COPYING file to every package even
# though upstream explicitly licenses library packages outside cmd under LGPL.
# The checks above bind this exception to the canonical tagged module selected
# by Go, exact upstream license surfaces, version-specific scanner output, and
# the non-cmd dependency graph. bloomfilter/v2 omits its repository-root MIT
# license from the nested v2 module archive, so its exception is separately
# fenced to the reviewed version, exact checked-in license text, dependency
# graph, and scanner result. go-base36 ships an exact Apache-2.0 OR MIT notice
# that the scanner does not classify; its exception is fenced to one module
# version and checksum, both upstream and checked-in notice hashes, dependency
# graph, and scanner result. Geas is similarly fenced to one module version,
# exact module checksum and LGPL text, dependency graph, and scanner result.
# Everything else remains subject to the repository's normal permissive-license
# allowlist.
exec "$GO_LICENSES_BIN" check "$@" \
  --ignore github.com/ethereum/go-ethereum \
  --ignore github.com/holiman/bloomfilter/v2 \
  --ignore github.com/multiformats/go-base36 \
  --ignore github.com/fjl/geas \
  --allowed_licenses=0BSD,Apache-2.0,BSD-2-Clause,BSD-3-Clause,ISC,MIT,MPL-2.0
