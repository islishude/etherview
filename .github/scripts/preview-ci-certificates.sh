#!/usr/bin/env bash
set -euo pipefail

make preview-cert
# Linux bind mounts retain host ownership. Permit only the API
# container's group to read its ephemeral key; keep gateway key 0600.
sudo chgrp 65532 .local/preview-tls/tls.key
chmod 0640 .local/preview-tls/tls.key
