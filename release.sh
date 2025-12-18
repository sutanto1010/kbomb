#!/usr/bin/env bash
set -euo pipefail

APP_NAME="kbomb"
DIST_DIR="dist"
mkdir -p "$DIST_DIR"

GIT_SHA="$(git rev-parse --short HEAD 2>/dev/null || echo dev)"

if ! command -v go >/dev/null; then
  echo "go not found"
  exit 1
fi

if ! command -v zip >/dev/null; then
  echo "zip not found"
  exit 1
fi

targets=("linux:amd64" "linux:arm64" "darwin:amd64" "darwin:arm64" "windows:amd64" "windows:arm64")

for t in "${targets[@]}"; do
  IFS=":" read -r GOOS GOARCH <<<"$t"
  EXT=""
  if [ "$GOOS" = "windows" ]; then EXT=".exe"; fi
  OUT_BIN="${DIST_DIR}/${APP_NAME}-${GOOS}-${GOARCH}${EXT}"
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -ldflags "-s -w -X main.version=${GIT_SHA}" -o "$OUT_BIN" .
  ZIP_FILE="${DIST_DIR}/${APP_NAME}-${GOOS}-${GOARCH}.zip"
  zip -j -q "$ZIP_FILE" "$OUT_BIN"
done

echo "release artifacts in $DIST_DIR"
