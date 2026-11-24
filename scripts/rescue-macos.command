#!/usr/bin/env bash
# Emergency rescue script for macOS: pauses moonlight daemon and restores safe brightness & neutral RGB.
set -euo pipefail

export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

if command -v moonlight >/dev/null 2>&1; then
  moonlight reset
elif [ -x "/usr/local/bin/moonlight" ]; then
  /usr/local/bin/moonlight reset
else
  SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
  "$SCRIPT_DIR/../dist/moonlight-darwin-arm64" reset
fi
