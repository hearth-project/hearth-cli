#!/usr/bin/env bash
# Build Hearth.app (universal: arm64 + x86_64) and zip it for the server's
# dist directory:  desktop/macos/scripts/build-app.sh [out-dir] [version]
# Needs macOS with Xcode command line tools (Swift 5.9+).
set -euo pipefail
cd "$(dirname "$0")/.."
OUT=${1:-dist}
VERSION=${2:-0.1.0}
mkdir -p "$OUT"
swift build -c release --arch arm64 --arch x86_64
BIN=$(swift build -c release --arch arm64 --arch x86_64 --show-bin-path)/Hearth
APP="$OUT/Hearth.app"
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$BIN" "$APP/Contents/MacOS/Hearth"
sed "s/@VERSION@/$VERSION/g" Resources/Info.plist > "$APP/Contents/Info.plist"
if [ -f Resources/AppIcon.icns ]; then cp Resources/AppIcon.icns "$APP/Contents/Resources/"; fi
codesign --force --deep --sign - "$APP"          # ad hoc: no Developer ID needed
(cd "$OUT" && rm -f hearth-menubar-darwin.zip && ditto -c -k --keepParent Hearth.app hearth-menubar-darwin.zip)
echo "$OUT/hearth-menubar-darwin.zip"
