#!/usr/bin/env bash
set -euo pipefail

args=(--version "${VERSION:?}")
if [[ "${MANUAL:-false}" == true ]]; then args+=(--manual); fi
case "${1:-}" in
  inspect)
    action=$(node compiler/vyper/update-pr.mjs --inspect "${args[@]}")
    printf 'action=%s\n' "$action" >> "$GITHUB_OUTPUT"
    ;;
  publish)
    node compiler/vyper/update-pr.mjs --artifact "$RUNNER_TEMP/vyper-update" "${args[@]}"
    ;;
  *) echo 'Usage: vyper-update-pr.sh inspect|publish' >&2; exit 1 ;;
esac
