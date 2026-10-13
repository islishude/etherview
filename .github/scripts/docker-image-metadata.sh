#!/usr/bin/env bash
set -euo pipefail

COMMIT="${GITHUB_SHA:-HEAD}"
if VERSION="$(git describe --tags --exact-match --match 'v*' "$COMMIT" 2>/dev/null || true)"; then
  VERSION="${VERSION:-}"
fi
if [ -z "${VERSION}" ]; then
  VERSION="$(git rev-parse --short "$COMMIT")"
fi
REVISION="$(git rev-parse "$COMMIT")"
CREATED="$(git show -s --format=%cI "$COMMIT" 2>/dev/null || date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "version=$VERSION" >> "$GITHUB_OUTPUT"
echo "revision=$REVISION" >> "$GITHUB_OUTPUT"
echo "created=$CREATED" >> "$GITHUB_OUTPUT"
