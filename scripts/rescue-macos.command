#!/usr/bin/env bash
# Emergency rescue script for macOS: pauses monlight daemon and restores safe brightness & neutral RGB.
set -euo pipefail

export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

if command -v monlight >/dev/null 2>&1; then
  monlight reset
elif [ -x "/usr/local/bin/monlight" ]; then
  /usr/local/bin/monlight reset
else
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
  "$SCRIPT_DIR/../dist/monlight-darwin-arm64" reset
fi
