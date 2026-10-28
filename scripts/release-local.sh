#!/usr/bin/env bash
# Rebuild the server binary and every client build from this checkout and
# restart a supervised server on them, so `install.sh` and `hearth
# self-update` hand out the new version.
#   scripts/release-local.sh [server-dir]   (default ../hearth-server: bin/hearth, data/)
set -euo pipefail
export PATH="$PATH:/usr/local/go/bin${HEARTH_GO_BIN:+:$HEARTH_GO_BIN}"
cd "$(dirname "$0")/.."
S=$(cd "${1:-../hearth-server}" && pwd)
V=0.1.0-$(date +%Y%m%d.%H%M)
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$V" -o "$S/bin/hearth.new" ./cmd/hearth
mv "$S/bin/hearth.new" "$S/bin/hearth"
scripts/build-dist.sh "$S/data/dist" "$V" >/dev/null
"$S/bin/hearth" server restart --data "$S/data"
echo "released $V; clients update with: hearth self-update"
