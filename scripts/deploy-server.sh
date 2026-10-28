#!/bin/sh
# One-command Hearth server: installs the binary and the client builds it
# serves, creates a self-signed certificate (unless you bring one), and
# starts the supervised server. Re-running it upgrades and restarts.
#
#   curl -fsSL https://raw.githubusercontent.com/hearth-project/hearth-cli/main/scripts/deploy-server.sh \
#     | sh -s -- --host hearth.example.com
#   scripts/deploy-server.sh --host 10.0.0.5          # from a clone (builds from source when needed)
#
# Options (or the environment variable in brackets):
#   --host NAME          IP or DNS name clients use (required)            [HEARTH_HOST]
#   --port N             listen port (default 8787)                       [HEARTH_PORT]
#   --data DIR           data directory (default /var/lib/hearth as root, else ~/hearth-server) [HEARTH_DATA]
#   --registration MODE  invite (default), open, approval or closed       [HEARTH_REGISTRATION]
#   --version vX.Y.Z     release to install (default: latest)             [HEARTH_VERSION]
#   --tls-cert F --tls-key F   your own certificate instead of a self-signed one
#   --source             build from source (needs Go 1.26+) instead of downloading a release
# Behind a proxy, export https_proxy first (curl uses it).
set -eu

REPO=hearth-project/hearth-cli
HOST=${HEARTH_HOST:-}
PORT=${HEARTH_PORT:-8787}
if [ "$(id -u)" = 0 ]; then DATA=${HEARTH_DATA:-/var/lib/hearth}; else DATA=${HEARTH_DATA:-$HOME/hearth-server}; fi
REG=${HEARTH_REGISTRATION:-invite}
VERSION=${HEARTH_VERSION:-latest}
CERT= KEY= SOURCE=
while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST=$2; shift ;;
    --port) PORT=$2; shift ;;
    --data) DATA=$2; shift ;;
    --registration) REG=$2; shift ;;
    --version) VERSION=$2; shift ;;
    --tls-cert) CERT=$2; shift ;;
    --tls-key) KEY=$2; shift ;;
    --source) SOURCE=1 ;;
    -h|--help) sed -n '2,20p' "$0" 2>/dev/null || true; exit 0 ;;
    *) echo "unknown option $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done
[ -n "$HOST" ] || { echo "pass --host <IP or name clients use to reach this server>" >&2; exit 2; }
say() { printf '\033[1m==> %s\033[0m\n' "$*"; }

os=$(uname -s | tr 'A-Z' 'a-z'); arch=$(uname -m)
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) echo "unsupported CPU $arch" >&2; exit 1 ;; esac
BIN_DIR="$DATA/bin"; DIST="$DATA/dist"
mkdir -p "$BIN_DIR" "$DIST"
chmod 700 "$DATA"

sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

fetch_release() {
  if [ "$VERSION" = latest ]; then base="https://github.com/$REPO/releases/latest/download"
  else base="https://github.com/$REPO/releases/download/$VERSION"; fi
  tmp=$(mktemp -d)
  curl -fsSL --retry 2 -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || { rm -r "$tmp"; return 1; }
  for p in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
    say "downloading hearth-$p"
    curl -fsSL --retry 2 -o "$tmp/hearth-$p" "$base/hearth-$p" || { rm -r "$tmp"; return 1; }
  done
  (cd "$tmp" && sha -c SHA256SUMS) >/dev/null || { echo "checksum mismatch" >&2; rm -r "$tmp"; exit 1; }
  v=$VERSION
  if [ "$v" = latest ]; then  # .../releases/latest redirects to .../releases/tag/vX.Y.Z
    v=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest" | sed -n 's#.*/tag/##p')
  fi
  echo "${v#v}" > "$tmp/VERSION"
  cp "$tmp"/hearth-* "$tmp/SHA256SUMS" "$tmp/VERSION" "$DIST/"
  rm -r "$tmp"
}

build_source() {
  command -v go >/dev/null 2>&1 || { echo "no release to download and no Go toolchain: install Go 1.26+ (https://go.dev/dl/) and re-run" >&2; exit 1; }
  src=$(cd "$(dirname "$0")/.." 2>/dev/null && pwd || true)
  if [ -z "$src" ] || [ ! -f "$src/go.mod" ] || ! grep -q "$REPO" "$src/go.mod"; then
    src=$(mktemp -d)/hearth
    say "cloning $REPO"
    git clone -q --depth 1 "https://github.com/$REPO" "$src"
  fi
  v=$(cd "$src" && git describe --tags --always 2>/dev/null || echo 0.1.0-dev)
  say "building $v from $src (4 platforms)"
  (cd "$src" && scripts/build-dist.sh "$DIST" "$v" >/dev/null)
}

if [ -n "$SOURCE" ]; then build_source
else fetch_release || { say "no GitHub release available; building from source"; build_source; }; fi
install -m 0755 "$DIST/hearth-$os-$arch" "$BIN_DIR/hearth.new" && mv "$BIN_DIR/hearth.new" "$BIN_DIR/hearth"
H="$BIN_DIR/hearth"
say "hearth $(cat "$DIST/VERSION") installed to $H"

TLS=""
if [ -n "$CERT" ]; then
  TLS="--tls-cert $CERT --tls-key $KEY"
elif [ ! -f "$DATA/tls.crt" ]; then
  say "creating a self-signed certificate for $HOST"
  "$H" server gencert --data "$DATA" --host "$HOST"
fi

if "$H" server status --data "$DATA" >/dev/null 2>&1; then
  say "restarting the running server on the new version"
  "$H" server restart --data "$DATA"
else
  say "starting the server (supervised: restarted if it exits; log $DATA/server.log)"
  # shellcheck disable=SC2086
  "$H" server start --data "$DATA" --addr "0.0.0.0:$PORT" --public-url "https://$HOST:$PORT" --registration "$REG" $TLS
fi

cat <<EOS

Hearth server is running at https://$HOST:$PORT  (data: $DATA)
  download page and the client install command:  https://$HOST:$PORT/
  status / logs:     $H server status --data $DATA   ·   tail -f $DATA/server.log
  invite a user:     $H server invite --data $DATA       (registration: $REG)
  make yourself admin after signing up:  $H server admin grant <you@example.com> --data $DATA
  start at boot:     $H server install-service --data $DATA --addr 0.0.0.0:$PORT --public-url https://$HOST:$PORT --registration $REG
EOS
