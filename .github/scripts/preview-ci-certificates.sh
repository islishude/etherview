#!/usr/bin/env bash
set -euo pipefail

make preview-cert
# Only the root-running Caddy container receives the owner-only gateway key.
