#!/bin/sh
set -eu
# Public fixture state only. Never reinitialize an existing payment chain.
seed=${RETH_SEED_FILE:-/config/reth-genesis.json}
root=${RETH_STATE_DIR:-/data}
data=$root/chain
identity=$root/genesis.sha256
expected=$(sha256sum "$seed" | cut -d ' ' -f 1)
if [ -f "$identity" ]; then
  [ "$(cat "$identity")" = "$expected" ] || { echo 'reth_genesis_mismatch' >&2; exit 1; }
  [ -s "$data/db/mdbx.dat" ] || { echo 'reth_state_missing' >&2; exit 1; }
else
  [ ! -e "$data" ] || { echo 'reth_identity_missing' >&2; exit 1; }
  "${RETH:-reth}" init --chain "$seed" --datadir "$data"
  printf '%s\n' "$expected" > "$identity"
fi
exec "${RETH:-reth}" node --chain "$seed" --datadir "$data" \
  --dev --dev.block-time 3s --disable-discovery --port 0 \
  --authrpc.addr 127.0.0.1 --authrpc.port 0 --ipcdisable \
  --http --http.addr 0.0.0.0 --http.port "${RETH_PORT:-8545}" \
  --http.api eth,net,web3 --http.corsdomain '*' \
  --ws --ws.addr 0.0.0.0 --ws.port "${RETH_WS_PORT:-8546}" \
  --ws.api eth,net,web3 --ws.origins '*'
