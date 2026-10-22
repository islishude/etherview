#!/usr/bin/env bash
set -euo pipefail

npm ci --workspace compiler
node compiler/vyper/wasm/prepare.mjs
npm --workspace compiler run build:sea -- "$PWD/.local/vyper-wasm/sea/etherview-solcjs"
