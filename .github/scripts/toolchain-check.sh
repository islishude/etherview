#!/usr/bin/env bash

set -eu

GO_BIN="${GO:-go}"
NODE_BIN="${NODE:-node}"
NPM_BIN="${NPM:-npm}"
MINIMUM_GO="go1.26.5"
MINIMUM_NODE="v24.18.0"
MINIMUM_NPM="11.16.0"

normalize_version() {
  local raw="$1"
  local prefix="$2"
  local version

  if [ -n "$prefix" ]; then
    case "$raw" in
      "$prefix"*) version="${raw#"$prefix"}" ;;
      *) return 1 ;;
    esac
  else
    version="$raw"
  fi

  if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
    return 1
  fi

  printf '%s\n' "$version"
}

version_at_least() {
  local current="$1"
  local minimum="$2"
  local current_major current_minor current_patch
  local minimum_major minimum_minor minimum_patch

  IFS='.' read -r current_major current_minor current_patch <<< "$current"
  IFS='.' read -r minimum_major minimum_minor minimum_patch <<< "$minimum"

  if (( 10#$current_major != 10#$minimum_major )); then
    (( 10#$current_major > 10#$minimum_major )) && return 0 || return 1
  fi
  if (( 10#$current_minor != 10#$minimum_minor )); then
    (( 10#$current_minor > 10#$minimum_minor )) && return 0 || return 1
  fi
  if (( 10#$current_patch < 10#$minimum_patch )); then
    return 1
  fi

  return 0
}

check_tool() {
  local label="$1"
  local executable="$2"
  local minimum="$3"
  local prefix="$4"
  local raw current minimum_clean
  shift 4

  if ! command -v "$executable" >/dev/null 2>&1; then
    echo "toolchain-check: $label executable not found: $executable"
    return 1
  fi

  if ! raw="$("$executable" "$@" 2>/dev/null)"; then
    echo "toolchain-check: failed to read current $label version"
    return 1
  fi

  if ! current="$(normalize_version "$raw" "$prefix")"; then
    echo "toolchain-check: unsupported $label version format"
    return 1
  fi
  if ! minimum_clean="$(normalize_version "$minimum" "$prefix")"; then
    echo "toolchain-check: invalid configured $label minimum"
    return 1
  fi

  if ! version_at_least "$current" "$minimum_clean"; then
    echo "toolchain-check: $label version $current is below minimum $minimum_clean"
    return 1
  fi
}

check_tool "Go" "$GO_BIN" "$MINIMUM_GO" "go" env GOVERSION
check_tool "Node" "$NODE_BIN" "$MINIMUM_NODE" "v" --version
check_tool "npm" "$NPM_BIN" "$MINIMUM_NPM" "" --version
