#!/usr/bin/env bash
set -euo pipefail

pnpm --filter ./compiler install --frozen-lockfile
node compiler/vyper/wasm/prepare.mjs
pnpm --dir compiler run build:sea "$PWD/.local/vyper-wasm/sea/etherview-solcjs"
