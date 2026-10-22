#!/bin/bash
set -euo pipefail
# The host has already rendered the exact EL template and selected genesis time.
export PRESET_BASE=mainnet CHAIN_ID=48815 NUMBER_OF_VALIDATORS=64
export SLOT_DURATION_MS=6000 SLOT_DURATION_IN_SECONDS=6
export GENESIS_DELAY=0 GLOAS_FORK_EPOCH=0
export BPO_1_EPOCH=18446744073709551615 BPO_2_EPOCH=18446744073709551615
export GENESIS_GASLIMIT=60000000
export BEACON_STATIC_ENR=''
export EL_AND_CL_MNEMONIC='test test test test test test test test test test test junk'
export GENESIS_TIMESTAMP
GENESIS_TIMESTAMP=$(jq -r '.timestamp' /data/metadata/genesis.json)
GENESIS_TIMESTAMP=$((GENESIS_TIMESTAMP))
/work/entrypoint.sh cl
# Never advertise the generator's public bootnode default.
printf '[]\n' > /data/metadata/boot_enr.yaml
rm -f /data/metadata/bootstrap_nodes.txt
eth2-val-tools keystores --insecure --source-min=0 --source-max=64 \
  --source-mnemonic="$EL_AND_CL_MNEMONIC" --out-loc=/data/validator
# The generator's mnemonic and decoded state are not runtime inputs.
rm -rf /data/parsed /data/metadata/mnemonics.yaml
