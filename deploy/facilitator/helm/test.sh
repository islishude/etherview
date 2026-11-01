#!/bin/sh
set -eu
chart=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
helm=${HELM:-helm}
output=$(mktemp)
trap 'rm -f "$output"' EXIT INT TERM
"$helm" lint "$chart" -f "$chart/test-values.yaml"
"$helm" template facilitator "$chart" -f "$chart/test-values.yaml" > "$output"
grep -Fq 'image: "etherview:local"' "$output"
grep -Fq 'command: ["/x402facilitator"]' "$output"
grep -Fq 'args: [--config=/etc/etherview/facilitator.yaml]' "$output"
grep -q 'replicas: 1' "$output"
grep -q 'type: Recreate' "$output"
grep -q 'publishNotReadyAddresses: true' "$output"
grep -q 'name: secrets' "$output"
if "$helm" template facilitator "$chart" > /dev/null 2>&1; then
  echo 'missing secret/egress was accepted' >&2; exit 1
fi
if "$helm" template facilitator "$chart" -f "$chart/test-values.yaml" --set config.unsafe_local_http=true > /dev/null 2>&1; then
  echo 'plaintext production deployment was accepted' >&2; exit 1
fi
