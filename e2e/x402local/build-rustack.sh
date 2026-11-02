#!/bin/sh
set -eu
# Build only the pinned source archive, excluding upstream's unrelated vendor
# submodules. The upstream Dockerfile owns its Rust toolchain and locked build.
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
revision=ffc10378c64280a78bf6b73fff51612bbba0eb66
workspace=$(mktemp -d "${TMPDIR:-/tmp}/etherview-rustack.XXXXXX")
trap 'rm -rf "$workspace"' EXIT HUP INT TERM
curl --fail --location --retry 3 --output "$workspace/source.tar.gz" \
  "https://api.github.com/repos/tyrchen/rustack/tarball/$revision"
mkdir "$workspace/source"
tar -xzf "$workspace/source.tar.gz" --strip-components=1 -C "$workspace/source"
"$root/.github/scripts/buildx.sh" build --load \
  --label "org.opencontainers.image.revision=$revision" \
  --tag "etherview-rustack:$revision" "$workspace/source"
