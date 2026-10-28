#!/bin/sh
# Public local-development accounts only. Never point this at an external RPC.
set -eu
export ETH_RPC_URL="${RPC_URL:?Preview RPC is required}"
export ETH_RPC_TIMEOUT=15
export ETH_TIMEOUT=90
# Empty private chains suggest tiny tips below Geth's block-builder minimum.
export ETH_GAS_PRICE=2gwei
export ETH_PRIORITY_GAS_PRICE=1gwei
test "$(cast chain-id)" = 48815 || { echo 'x402-init: wrong chain' >&2; exit 1; }

owner=0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266
owner_key=ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80
deployer=0x70997970C51812dc3A010C7d01b50e0d17dc79C8
deployer_key=59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d
token=0x8464135c8F25Da09e49BC8782676a84730C318bC
expected="$(forge inspect LocalBillingToken deployedBytecode --offline)"
actual="$(cast code "$token")"
if [ "$actual" = 0x ]; then
  # The dedicated deployer's first CREATE has a stable address regardless of
  # transactions already sent by the interactive wallet. Never reset a nonce.
  test "$(cast nonce "$deployer" --block pending)" = 0 || {
    echo 'x402-init: deployer already used; refusing a different token address' >&2; exit 1;
  }
  if [ "$(cast balance "$deployer")" = 0 ]; then
    cast send --private-key "$owner_key" "$deployer" --value 10ether >/dev/null
  fi
  bytecode="$(forge inspect LocalBillingToken bytecode --offline)"
  arguments="$(cast abi-encode 'constructor(string,string,address,uint256)' 'Local USD' '1' "$owner" 1000000000000000000)"
  cast send --private-key "$deployer_key" --nonce 0 --create "${bytecode}${arguments#0x}" >/dev/null
  actual="$(cast code "$token")"
fi
test "$actual" = "$expected" || {
  echo 'x402-init: token bytecode differs; refusing to reuse or replace it' >&2; exit 1;
}
# Existing balances and spent authorizations survive every restart. No remint.
echo "x402-init: Local USD ready at $token on eip155:48815"
