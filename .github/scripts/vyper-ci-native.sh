#!/usr/bin/env bash
set -euo pipefail

npm --prefix compiler ci --ignore-scripts
node compiler/vyper/wasm/prepare.mjs
npm --prefix compiler run build:sea -- "$PWD/.local/vyper-wasm/sea/etherview-solcjs"
