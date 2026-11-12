#!/bin/sh
set -eu
# Primary PoS chain: Lighthouse owns block production. Never use --dev here.
seed=/config/genesis.json
data=/data/chain
identity=/data/genesis.sha256
expected=$(sha256sum "$seed" | cut -d ' ' -f 1)
if [ -f "$identity" ]; then
  [ "$(cat "$identity")" = "$expected" ] || { echo execution_genesis_mismatch >&2; exit 1; }
  [ -s "$data/db/mdbx.dat" ] || { echo execution_state_missing >&2; exit 1; }
else
  [ ! -e "$data" ] || { echo execution_identity_missing >&2; exit 1; }
  reth init --chain "$seed" --datadir "$data"
  printf '%s\n' "$expected" > "$identity"
fi
# Archive is Reth's default; no pruning or full/minimal-storage flags are used.
# The reviewed custom genesis has baseFeePerGas=1, below Reth's default pool floor.
exec reth node --chain "$seed" --datadir "$data" \
  --disable-discovery --max-outbound-peers 0 --max-inbound-peers 0 --port 0 \
  --authrpc.addr 0.0.0.0 --authrpc.port 8551 --authrpc.jwtsecret /run/engine/jwt.hex \
  --ipcdisable --rpc-cache.cache-computed-bals --txpool.minimal-protocol-fee 1 \
  --http --http.addr 0.0.0.0 --http.port 8545 --http.api eth,net,web3,debug,txpool \
  --http.corsdomain '*' \
  --ws --ws.addr 0.0.0.0 --ws.port 8546 --ws.api eth,net,web3,debug,txpool --ws.origins '*'
