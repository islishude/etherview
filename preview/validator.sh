#!/bin/sh
set -eu
# Keep keys and slashing protection together in one persistent volume.
if [ ! -f /validators/.initialized ]; then
  if [ -n "$(ls -A /validators)" ]; then
    echo 'Incomplete validator initialization; explicitly rebuild Preview' >&2
    exit 1
  fi
  cp -R /initial/keys/. /validators/
  touch /validators/.initialized
  set -- --init-slashing-protection "$@"
fi
exec lighthouse --testnet-dir=/network vc --validators-dir=/validators \
  --secrets-dir=/initial/secrets --beacon-nodes=http://lighthouse:5052 \
  --suggested-fee-recipient=0xf39fd6e51aad88f6f4ce6ab8827279cfffb92266 \
  --graffiti=Etherview-Preview "$@"
