#!/bin/bash
set -eu
# RPC readiness must not wait for Lighthouse to start. Chain progress and BAL
# readiness are checked separately by ready.mjs after the entire graph starts.
body='{"jsonrpc":"2.0","id":1,"method":"eth_chainId","params":[]}'
exec 3<>/dev/tcp/127.0.0.1/8545
printf 'POST / HTTP/1.0\r\nHost: localhost\r\nContent-Type: application/json\r\nContent-Length: %s\r\nConnection: close\r\n\r\n%s' "${#body}" "$body" >&3
response=''
IFS= read -r -d '' -t 2 response <&3 || [ -n "$response" ]
exec 3>&-
[[ $response == *'"result":"0xbeaf"'* ]]
