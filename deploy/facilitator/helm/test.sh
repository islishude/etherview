#!/bin/sh
set -eu
chart=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
helm=${HELM:-helm}
output=$(mktemp)
conflict=$(mktemp)
trap 'rm -f "$output" "$conflict"' EXIT INT TERM
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
if "$helm" template facilitator "$chart" -f "$chart/test-values.yaml" --set config.server.allow_insecure_http=true > /dev/null 2>&1; then
  echo 'plaintext production deployment was accepted' >&2; exit 1
fi

HELM_RENDERED_CONFIG="$output" go test -count=1 ./internal/facilitator -run '^TestRenderedHelmConfig$'
awk '{print} /url_file: \/run\/secrets\/rpc-url/ {print "        url: https://example.invalid"}' "$chart/test-values.yaml" > "$conflict"
if "$helm" template facilitator "$chart" -f "$conflict" > /dev/null 2>&1; then
  echo 'conflicting RPC sources were accepted' >&2; exit 1
fi
if "$helm" template facilitator "$chart" -f "$chart/test-values.yaml" --set config.server.listen=127.0.0.1:9000 > /dev/null 2>&1; then
  echo 'listener incompatible with probes was accepted' >&2; exit 1
fi

awk '/^  (database|auth):$/ {skip=1;next} skip && /^    / {next} {skip=0;print}' "$chart/test-values.yaml" > "$conflict"
"$helm" template facilitator "$chart" -f "$conflict" \
  --set secretEnv.X402_DATABASE_URL.name=env-secrets --set secretEnv.X402_DATABASE_URL.key=X402_DATABASE_URL \
  --set secretEnv.X402_BEARER_TOKEN.name=env-secrets --set secretEnv.X402_BEARER_TOKEN.key=X402_BEARER_TOKEN > "$output"
HELM_RENDERED_CONFIG="$output" HELM_EXPECT_SECRET_ENV=true go test -count=1 ./internal/facilitator -run '^TestRenderedHelmConfig$'
if "$helm" template facilitator "$chart" -f "$chart/test-values.yaml" --set-string secretEnv.X402_DATABASE_URL=plaintext > /dev/null 2>&1; then
  echo 'plaintext credential value was accepted' >&2; exit 1
fi
if "$helm" template facilitator "$chart" -f "$chart/test-values.yaml" --set secretEnv.X402_DATABASE_URL.name=env-secrets > /dev/null 2>&1; then
  echo 'incomplete Secret selector was accepted' >&2; exit 1
fi
if "$helm" template facilitator "$chart" -f "$chart/test-values.yaml" --set secretEnv.X402_RPC_URL.name=env-secrets --set secretEnv.X402_RPC_URL.key=rpc > /dev/null 2>&1; then
  echo 'unsupported credential environment was accepted' >&2; exit 1
fi
