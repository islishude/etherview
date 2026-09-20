#!/bin/sh
set -eu

# Runs before each daemon start, including against an existing Preview repo.
ipfs config --json Gateway.NoFetch "${IPFS_GATEWAY_NO_FETCH:-false}"
ipfs config --json Gateway.NoDNSLink true
ipfs config --json Gateway.PublicGateways '{"ipfs.preview.test":{"Paths":["/ipfs"],"UseSubdomains":false,"NoDNSLink":true}}'

expected=bafybeibnsoufr2renqzsh347nrx54wcubt5lgkeivez63xvivplfwhtpym
test "$(wc -c </fixtures/metadata.json | tr -d ' ')" = 205
echo 'a87d3d327d1a2c7f839000c080e07cd152b49ddf653f1a5afa5144eeec103d8d  /fixtures/metadata.json' | sha256sum -c -
actual=$(ipfs add --offline --quieter --cid-version=1 --hash=sha2-256 \
  --raw-leaves=true --chunker=size-262144 --pin=true --wrap-with-directory /fixtures/metadata.json)
test "$actual" = "$expected" || { echo 'Preview IPFS fixture CID mismatch' >&2; exit 1; }
