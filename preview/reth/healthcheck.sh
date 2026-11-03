#!/bin/bash
set -eu
# The official Reth image provides Bash but does not require curl, jq or Foundry.
# Loopback connections and response reads are bounded by the Compose timeout.
rpc() {
  local body response
  body='{"jsonrpc":"2.0","id":1,"method":"'"$1"'","params":'"$2"'}'
  exec 3<>/dev/tcp/127.0.0.1/${RETH_PORT:-8545}
  printf 'POST / HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s' "${#body}" "$body" >&3
  response=''
  IFS= read -r -d '' -t 2 response <&3 || [ -n "$response" ]
  exec 3>&-
  printf '%s' "$response"
}
chain=$(rpc eth_chainId '[]')
[[ $chain == *'"result":"0x7a69"'* ]]
block=$(rpc eth_getBlockByNumber '["latest",false]')
[[ $block =~ \"timestamp\":\"(0x[0-9a-f]+)\" ]]
timestamp=$(( ${BASH_REMATCH[1]} ))
now=$(date +%s)
(( timestamp >= now - 30 && timestamp <= now + 30 ))
[[ $block =~ \"number\":\"(0x[0-9a-f]+)\" ]]
(( ${BASH_REMATCH[1]} > 0 ))
[[ $block =~ \"blockAccessListHash\":\"0x[0-9a-f]{64}\" ]]
[[ $block =~ \"slotNumber\":\"0x[0-9a-f]+\" ]]
