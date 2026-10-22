#!/usr/bin/env bash
set -euo pipefail

npm ci --workspace compiler
node compiler/vyper/wasm/prepare.mjs
node compiler/vyper/wasm/packages.mjs
node compiler/vyper/wasm/build-release.mjs --output .local/vyper-releases
