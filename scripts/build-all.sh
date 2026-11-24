#!/usr/bin/env bash
# Universal cross-compile script (runs on macOS, Linux, and Windows Git Bash).
# Builds Windows (amd64) and macOS (arm64 + amd64) binaries into dist/.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DIST_DIR="$ROOT_DIR/dist"
mkdir -p "$DIST_DIR"

cd "$ROOT_DIR"

echo "Building Windows (amd64)..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$DIST_DIR/moonlight-windows-amd64.exe" ./cmd/moonlight

echo "Building macOS Apple Silicon (arm64)..."
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o "$DIST_DIR/moonlight-darwin-arm64" ./cmd/moonlight

echo "Building macOS Intel (amd64)..."
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o "$DIST_DIR/moonlight-darwin-amd64" ./cmd/moonlight

echo ""
echo "Done! Artifacts in $DIST_DIR:"
ls -lh "$DIST_DIR"
