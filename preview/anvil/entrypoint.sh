#!/bin/sh
set -eu
# Public fixture state only. Never overwrite a used chain with a new seed.
seed=${ANVIL_SEED_FILE:-/config/anvil-state.json}
state=${ANVIL_STATE_DIR:-/data}/state.json
identity=${ANVIL_STATE_DIR:-/data}/genesis.sha256
expected=$(sha256sum "$seed" | cut -d ' ' -f 1)
if [ -f "$identity" ]; then
  [ "$(cat "$identity")" = "$expected" ] || { echo 'anvil_genesis_mismatch' >&2; exit 1; }
  [ -s "$state" ] || { echo 'anvil_state_missing' >&2; exit 1; }
else
  [ ! -e "$state" ] || { echo 'anvil_identity_missing' >&2; exit 1; }
  cp "$seed" "$state"
  printf '%s\n' "$expected" > "$identity"
fi
# No generated accounts: loading a chain must never refill development funds.
ready=${ANVIL_STATE_DIR:-/data}/ready
rm -f "$ready"
rpc=http://127.0.0.1:${ANVIL_PORT:-8545}
"${ANVIL:-anvil}" --host 0.0.0.0 --port "${ANVIL_PORT:-8545}" --chain-id 31337 --hardfork prague \
  --accounts 0 --block-time 1 --state "$state" --state-interval 1 --preserve-historical-states &
node_pid=$!
cleanup() {
  rm -f "$ready"
  kill -INT "$node_pid" 2>/dev/null || true
  wait "$node_pid" || true
}
trap cleanup EXIT
trap 'exit 0' INT TERM
attempt=0
until "${CAST:-cast}" chain-id --rpc-url "$rpc" >/dev/null 2>&1; do
  kill -0 "$node_pid" 2>/dev/null || exit 1
  attempt=$((attempt + 1))
  [ "$attempt" -lt 100 ] || exit 1
  sleep 0.1
done
# Native snapshots preserve old block time. Advance normally before accepting
# payment authorizations whose validity window is expressed in wall-clock time.
current=$("${CAST:-cast}" block latest --field timestamp --rpc-url "$rpc")
target=$(date +%s)
if [ "$current" -ge "$target" ]; then target=$((current + 1)); fi
"${CAST:-cast}" rpc --rpc-url "$rpc" evm_setNextBlockTimestamp "$target" >/dev/null
"${CAST:-cast}" rpc --rpc-url "$rpc" evm_mine >/dev/null
touch "$ready"
wait "$node_pid"
