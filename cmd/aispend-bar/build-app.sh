#!/usr/bin/env bash
# build-app.sh — package AiSpend into a minimal macOS .app bundle for local use.
#
# The app is a status-bar agent (NSStatusItem + a WKWebView popover, via cgo to the
# system Cocoa/WebKit frameworks — no external Go deps). Status-bar apps behave best from
# inside a .app bundle; LSUIElement=1 makes it a menu-bar agent (no Dock icon).
#
# Usage:  cmd/aispend-bar/build-app.sh [OUTPUT_DIR]   # default OUTPUT_DIR = current dir
#   open ./AiSpend.app            # (first run: xattr -dr com.apple.quarantine ./AiSpend.app)
set -euo pipefail

APP_NAME="AiSpend"
BUNDLE_ID="io.cloudyali.aispend-bar"
OUT_DIR="${1:-.}"
APP="$OUT_DIR/$APP_NAME.app"
MACOS="$APP/Contents/MacOS"
RES="$APP/Contents/Resources"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"

rm -rf "$APP"
mkdir -p "$MACOS" "$RES"
( cd "$ROOT" && go build -o "$MACOS/$APP_NAME" ./cmd/aispend-bar )

# Icon: build AiSpend.icns from the committed 1024 master via macOS sips + iconutil.
ICON_SRC="$ROOT/cmd/aispend-bar/AiSpend.png"
if command -v iconutil >/dev/null 2>&1 && [ -f "$ICON_SRC" ]; then
  ICONSET="$(mktemp -d)/AiSpend.iconset"; mkdir -p "$ICONSET"
  for s in 16 32 128 256 512; do
    sips -z "$s" "$s" "$ICON_SRC" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
    d=$((s * 2)); sips -z "$d" "$d" "$ICON_SRC" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
  done
  iconutil -c icns "$ICONSET" -o "$RES/AiSpend.icns"
  rm -rf "$(dirname "$ICONSET")"
fi

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>AiSpend</string>
  <key>CFBundleDisplayName</key><string>AiSpend</string>
  <key>CFBundleExecutable</key><string>${APP_NAME}</string>
  <key>CFBundleIdentifier</key><string>${BUNDLE_ID}</string>
  <key>CFBundleIconFile</key><string>AiSpend</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

codesign --force --sign - "$APP" >/dev/null 2>&1 || echo "note: ad-hoc codesign skipped"

echo "Built $APP"
echo "Run:  open \"$APP\"    (first run may need:  xattr -dr com.apple.quarantine \"$APP\")"
