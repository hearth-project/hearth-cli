#!/usr/bin/env bash
# Build Hearth.app (universal: arm64 + x86_64) and zip it for the server's
# dist directory:  desktop/macos/scripts/build-app.sh [out-dir] [version]
# Needs macOS with Xcode command line tools (Swift 5.9+).
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=${1:-dist}
VERSION=${2:-0.1.0}
mkdir -p "$OUT"
# `--arch a --arch b` needs full Xcode (xcbuild). With only the command line
# tools, build each architecture on its own and join them with lipo.
if [ "$(xcode-select -p 2>/dev/null)" != /Library/Developer/CommandLineTools ]; then
  swift build -c release --arch arm64 --arch x86_64
  BIN=$(swift build -c release --arch arm64 --arch x86_64 --show-bin-path)/Hearth
else
  for a in arm64 x86_64; do
    swift build -c release --triple "$a-apple-macosx13.0"
  done
  BIN=.build/Hearth-universal
  lipo -create -output "$BIN" \
    "$(swift build -c release --triple arm64-apple-macosx13.0 --show-bin-path)/Hearth" \
    "$(swift build -c release --triple x86_64-apple-macosx13.0 --show-bin-path)/Hearth"
fi
APP="$OUT/Hearth.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/Hearth"
sed "s/@VERSION@/$VERSION/g" Resources/Info.plist > "$APP/Contents/Info.plist"
# The icon is drawn by the app itself (Snapshot.icon), then packed as .icns.
ICONSET=$(mktemp -d)/AppIcon.iconset
mkdir -p "$ICONSET"
"$BIN" --icon "$ICONSET/full.png" >/dev/null
for s in 16 32 128 256 512; do
  sips -z $s $s "$ICONSET/full.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$ICONSET/full.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
rm "$ICONSET/full.png"
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"
codesign --force --deep --sign - "$APP"          # ad hoc: no Developer ID needed
(cd "$OUT" && rm -f hearth-menubar-darwin.zip && ditto -c -k --keepParent Hearth.app hearth-menubar-darwin.zip)
echo "$OUT/hearth-menubar-darwin.zip"
