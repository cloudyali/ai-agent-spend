#!/usr/bin/env bash
# build-app.sh — package aispend-bar into a minimal macOS .app bundle.
#
# menuet initializes UNUserNotificationCenter at startup, and macOS THROWS
# (NSInternalInconsistencyException: bundleProxyForCurrentProcess is nil) when that
# runs from a loose executable. A menu-bar app therefore MUST run from inside a .app
# bundle — this script builds one. LSUIElement=1 makes it an agent (menu bar only,
# no Dock icon).
#
# Usage (from anywhere in the repo):
#   cmd/aispend-bar/build-app.sh [OUTPUT_DIR]   # default OUTPUT_DIR = current dir
#   open ./aispend-bar.app                       # or run the inner binary to see logs
set -euo pipefail

APP_NAME="aispend-bar"
BUNDLE_ID="io.cloudyali.aispend-bar"
OUT_DIR="${1:-.}"
APP="$OUT_DIR/$APP_NAME.app"
MACOS="$APP/Contents/MacOS"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

mkdir -p "$MACOS"
( cd "$ROOT" && go build -o "$MACOS/$APP_NAME" ./cmd/aispend-bar )

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>aispend-bar</string>
  <key>CFBundleDisplayName</key><string>aispend</string>
  <key>CFBundleExecutable</key><string>${APP_NAME}</string>
  <key>CFBundleIdentifier</key><string>${BUNDLE_ID}</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# Ad-hoc sign so recent macOS is happy launching it (no Developer ID needed for
# local use; notifications — which this app doesn't use — would need real signing).
codesign --force --sign - "$APP" >/dev/null 2>&1 || echo "note: codesign skipped (ad-hoc signing unavailable)"

echo "Built $APP"
echo "Run:  open \"$APP\"    (or, to see logs:  \"$MACOS/$APP_NAME\")"
