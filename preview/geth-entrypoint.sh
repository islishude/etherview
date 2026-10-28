#!/bin/sh
set -eu

geth --datadir=/gethdata init /config/genesis.json
exec geth --datadir=/gethdata "$@"
