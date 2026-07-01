#!/usr/bin/env bash
# release-bar.sh — build the AiSpend macOS menu-bar app into a distributable .dmg.
#
# Runs on macOS only (cgo + AppKit; goreleaser can't cross-compile this from Linux, and
# OSS goreleaser has no .app/.dmg builder — hence this companion script, à la OpenUsage's
# script/release.sh). The CLI still ships via goreleaser on Linux; this handles just the bar.
#
# Phase 1 (default): ad-hoc signed, NOT notarized — first run needs the quarantine bit
# cleared (the Homebrew cask does it automatically). Phase 2 (Developer ID + notarize) turns
# on automatically when the APPLE_* env vars are present.
#
# Usage:  scripts/release-bar.sh [VERSION]     # VERSION defaults to $VERSION or 0.0.0-dev
set -euo pipefail

VERSION="${1:-${VERSION:-0.0.0-dev}}"
VERSION="${VERSION#v}" # tolerate a leading v (e.g. from a git tag)
APP_NAME="AiSpend"
BUNDLE_ID="io.cloudyali.aispend-bar"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"
APP="$DIST/$APP_NAME.app"
MACOS="$APP/Contents/MacOS"
RES="$APP/Contents/Resources"
DMG="$DIST/aispend-bar-$VERSION.dmg" # stable release-asset name; the .app inside is AiSpend.app

command -v lipo >/dev/null || { echo "release-bar: needs macOS (lipo not found)" >&2; exit 1; }

rm -rf "$APP" "$DMG"
mkdir -p "$MACOS" "$RES"

# 1. Universal binary (Apple Silicon + Intel) via lipo. The macOS-only menuet dep is in
#    go.mod (CI runs `go get github.com/caseymrm/menuet` first as a safety net).
for arch in arm64 amd64; do
  ( cd "$ROOT" && CGO_ENABLED=1 GOOS=darwin GOARCH="$arch" \
      go build -trimpath -ldflags "-s -w" -o "$DIST/$APP_NAME-$arch" ./cmd/aispend-bar )
done
lipo -create -output "$MACOS/$APP_NAME" "$DIST/$APP_NAME-arm64" "$DIST/$APP_NAME-amd64"
rm -f "$DIST/$APP_NAME-arm64" "$DIST/$APP_NAME-amd64"

# 2. Icon: AiSpend.icns from the committed 1024 master (sips + iconutil).
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

# 3. Info.plist — LSUIElement=1 = menu-bar agent (no Dock icon).
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
  <key>CFBundleShortVersionString</key><string>${VERSION}</string>
  <key>CFBundleVersion</key><string>${VERSION}</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>12.0</string>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

# 4. Sign. Developer ID + hardened runtime when APPLE_SIGN_IDENTITY is set (Phase 2);
#    otherwise ad-hoc (Phase 1 — Gatekeeper still quarantines; the cask clears it).
if [ -n "${APPLE_SIGN_IDENTITY:-}" ]; then
  codesign --force --deep --options runtime --timestamp --sign "$APPLE_SIGN_IDENTITY" "$APP"
else
  codesign --force --deep --sign - "$APP"
fi

# 5. Package a drag-to-Applications .dmg.
STAGE="$DIST/dmg-root"
rm -rf "$STAGE"; mkdir -p "$STAGE"
cp -R "$APP" "$STAGE/"
ln -sf /Applications "$STAGE/Applications"
hdiutil create -volname "AiSpend" -srcfolder "$STAGE" -ov -format UDZO "$DMG"
rm -rf "$STAGE"

# 6. Notarize + staple (Phase 2) — only when the notary credentials are present.
if [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_PASSWORD:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ]; then
  echo "release-bar: notarizing $DMG ..."
  xcrun notarytool submit "$DMG" --apple-id "$APPLE_ID" --password "$APPLE_PASSWORD" --team-id "$APPLE_TEAM_ID" --wait
  xcrun stapler staple "$DMG"
else
  echo "release-bar: APPLE_* not set — shipping unsigned/un-notarized (Phase 1)." >&2
fi

# 7. Checksum for the Homebrew cask.
shasum -a 256 "$DMG" | awk '{print $1}' > "$DMG.sha256"
echo "Built $DMG"
echo "sha256: $(cat "$DMG.sha256")"
