#!/usr/bin/env bash

set -eu

GO_BIN="${GO:-go}"
NODE_BIN="${NODE:-node}"
EXPECTED_GO="${EXPECTED_GO_VERSION:-go1.26.5}"
EXPECTED_NODE="${EXPECTED_NODE_VERSION:-v24.18.0}"

normalize_version() {
  local version="$1"

  version="${version#go}"
  version="${version#v}"
  version="$(printf '%s' "$version" | tr -d '[:space:]')"
  version="${version%%[^0-9.]*}"

  printf '%s\n' "$version"
}

version_at_least() {
  local current="$1"
  local minimum="$2"
  local current_major current_minor current_patch
  local minimum_major minimum_minor minimum_patch

  IFS='.' read -r current_major current_minor current_patch <<< "$current"
  IFS='.' read -r minimum_major minimum_minor minimum_patch <<< "$minimum"

  current_major="${current_major:-0}"
  current_minor="${current_minor:-0}"
  current_patch="${current_patch:-0}"
  minimum_major="${minimum_major:-0}"
  minimum_minor="${minimum_minor:-0}"
  minimum_patch="${minimum_patch:-0}"

  if [[ ! "$current_major" =~ ^[0-9]+$ ]] || [[ ! "$current_minor" =~ ^[0-9]+$ ]] || [[ ! "$current_patch" =~ ^[0-9]+$ ]]; then
    return 2
  fi
  if [[ ! "$minimum_major" =~ ^[0-9]+$ ]] || [[ ! "$minimum_minor" =~ ^[0-9]+$ ]] || [[ ! "$minimum_patch" =~ ^[0-9]+$ ]]; then
    return 2
  fi

  if (( current_major != minimum_major )); then
    ((current_major > minimum_major)) && return 0 || return 1
  fi

  if (( current_minor != minimum_minor )); then
    ((current_minor > minimum_minor)) && return 0 || return 1
  fi

  if (( current_patch < minimum_patch )); then
    return 1
  fi

  return 0
}

check_tool() {
  local label="$1"
  local executable="$2"
  local minimum="$3"
  local raw current minimum_clean status

  if ! command -v "$executable" >/dev/null 2>&1; then
    echo "toolchain-check: $label executable not found: $executable"
    return 1
  fi

  case "$label" in
    Go) raw="$("$executable" env GOVERSION 2>/dev/null || true)" ;;
    Node) raw="$("$executable" --version 2>/dev/null || true)" ;;
    *)
      echo "toolchain-check: unsupported tool label $label"
      return 1
      ;;
  esac

  if [ -z "$raw" ]; then
    echo "toolchain-check: failed to read current $label version"
    return 1
  fi

  current="$(normalize_version "$raw")"
  minimum_clean="$(normalize_version "$minimum")"
  if [ -z "$current" ] || [ -z "$minimum_clean" ]; then
    echo "toolchain-check: unsupported version format for $label (current=$raw, minimum=$minimum)"
    return 1
  fi

  if ! version_at_least "$current" "$minimum_clean"; then
    status=$?
    if [ "$status" -eq 2 ]; then
      echo "toolchain-check: unsupported version format for $label (current=$raw, minimum=$minimum)"
      return 1
    fi
    echo "toolchain-check: $label current version $current is below minimum $minimum_clean"
    return 1
  fi
}

check_tool "Go" "$GO_BIN" "$EXPECTED_GO"
check_tool "Node" "$NODE_BIN" "$EXPECTED_NODE"
