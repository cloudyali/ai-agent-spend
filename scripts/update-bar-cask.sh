#!/usr/bin/env bash
# update-bar-cask.sh — generate the aispend-bar Homebrew cask and push it to the tap.
# Runs after release-bar.sh has produced dist/aispend-bar-<version>.dmg(.sha256).
#
# Phase 1: the app is unsigned, so the cask clears the quarantine bit on install (mirrors
# the CLI cask in .goreleaser.yaml). Remove the postflight once the app is notarized.
#
# Usage:  scripts/update-bar-cask.sh [VERSION]   (needs HOMEBREW_TAP_TOKEN in the env)
set -euo pipefail

VERSION="${1:-${VERSION:-}}"; VERSION="${VERSION#v}"
[ -n "$VERSION" ] || { echo "update-bar-cask: VERSION required" >&2; exit 1; }
[ -n "${HOMEBREW_TAP_TOKEN:-}" ] || { echo "update-bar-cask: HOMEBREW_TAP_TOKEN required" >&2; exit 1; }

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SHA="$(cat "$ROOT/dist/aispend-bar-$VERSION.dmg.sha256")"
TAP_DIR="$(mktemp -d)"
trap 'rm -rf "$TAP_DIR"' EXIT

git clone --depth 1 "https://x-access-token:${HOMEBREW_TAP_TOKEN}@github.com/cloudyali/homebrew-tap.git" "$TAP_DIR"
mkdir -p "$TAP_DIR/Casks"

# The url uses Ruby's #{version} interpolation; $VERSION / $SHA are shell-expanded here.
cat > "$TAP_DIR/Casks/aispend-bar.rb" <<CASK
cask "aispend-bar" do
  version "$VERSION"
  sha256 "$SHA"

  url "https://github.com/cloudyali/ai-agent-spend/releases/download/v#{version}/aispend-bar-#{version}.dmg"
  name "aispend"
  desc "Menu-bar view of your AI-coding spend, ROI, and cache savings"
  homepage "https://github.com/cloudyali/ai-agent-spend"

  depends_on macos: ">= :monterey"
  app "AiSpend.app"

  # Phase 1 (unsigned): strip the quarantine bit so Gatekeeper opens it. Remove once the
  # app is Developer-ID-signed + notarized.
  postflight do
    system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{appdir}/AiSpend.app"]
  end
end
CASK

cd "$TAP_DIR"
git config user.name goreleaserbot
git config user.email bot@cloudyali.io
git add Casks/aispend-bar.rb
git commit -m "aispend-bar $VERSION" || { echo "no cask change"; exit 0; }
git push
echo "pushed cask aispend-bar $VERSION → cloudyali/homebrew-tap"
