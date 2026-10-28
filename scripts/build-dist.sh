#!/usr/bin/env bash
# Cross-compile the client/server for every platform /install.sh serves:
#   scripts/build-dist.sh [out-dir] [version]
# Put the output in <server data>/dist (or pass --dist-dir) and new
# machines of any listed platform can install with one curl command.
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=${1:-dist}
VERSION=${2:-$(git describe --tags --always 2>/dev/null || echo 0.1.0-dev)}
mkdir -p "$OUT"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  os=${target%/*} arch=${target#*/}
  CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
    -o "$OUT/hearth-$os-$arch" ./cmd/hearth
  echo "$OUT/hearth-$os-$arch"
done
echo "$VERSION" > "$OUT/VERSION"
(cd "$OUT" && { sha256sum hearth-* 2>/dev/null || shasum -a 256 hearth-*; } > SHA256SUMS)
