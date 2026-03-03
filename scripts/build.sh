#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="$ROOT_DIR/dist"

mkdir -p "$OUT_DIR"

platforms=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
)

for item in "${platforms[@]}"; do
  os="${item%% *}"
  arch="${item##* }"
  out="$OUT_DIR/knock-${os}-${arch}"
  echo "Building $out"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -o "$out" "$ROOT_DIR"
done

echo "Build complete: $OUT_DIR"
