#!/usr/bin/env bash
set -euo pipefail

BIN_PATH="${1:-/usr/local/bin/moonlight}"
PLIST_DIR="$HOME/Library/LaunchAgents"
PLIST_PATH="$PLIST_DIR/com.moonlight.daemon.plist"
LOG_DIR="$HOME/Library/Logs/moonlight"

if [ ! -x "$BIN_PATH" ]; then
  echo "Error: executable not found at $BIN_PATH"
  echo "Copy the built binary there first, e.g.:"
  echo "  sudo cp dist/moonlight-darwin-arm64 /usr/local/bin/moonlight && sudo chmod +x /usr/local/bin/moonlight"
  exit 1
fi

mkdir -p "$PLIST_DIR" "$LOG_DIR"

cat > "$PLIST_PATH" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.moonlight.daemon</string>
  <key>ProgramArguments</key>
  <array>
    <string>${BIN_PATH}</string>
    <string>daemon</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key>
    <string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${LOG_DIR}/stdout.log</string>
  <key>StandardErrorPath</key>
  <string>${LOG_DIR}/stderr.log</string>
</dict>
</plist>
EOF

launchctl unload "$PLIST_PATH" 2>/dev/null || true
launchctl load -w "$PLIST_PATH"

echo "Installed and started LaunchAgent: $PLIST_PATH"
echo "Logs: $LOG_DIR/stdout.log"
