#!/usr/bin/env bash
set -euo pipefail

if [[ ! "${IMAGE_DIGEST:-}" =~ ^sha256:[a-f0-9]{64}$ ]]; then
  echo 'Invalid image digest' >&2
  exit 1
fi
mkdir -p "$RUNNER_TEMP/digests"
touch "$RUNNER_TEMP/digests/${IMAGE_DIGEST#sha256:}"
