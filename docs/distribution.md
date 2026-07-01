# Distribution & release plan — three channels, one binary family

aispend ships **three forms**, and we keep all three:

- **CLI** — `aispend` (`report`/`today`/`scan`/`serve`/…). Already distributed: goreleaser → GitHub
  Releases + `brew install cloudyali/tap/aispend` + `install.sh`. Static (`CGO_ENABLED=0`),
  **unsigned** (the cask strips the quarantine bit on install).
- **TUI** — the *default channel* of that same `aispend` binary (bare `aispend` / `aispend tui`).
  No separate artifact.
- **Menu bar** — `aispend-bar`, a macOS `.app` (menuet → cgo, darwin-only). New artifact; this doc
  is mostly about shipping it.

## 1. Architecture: self-contained menu bar (done)

The bar was briefly an HTTP client of `aispend serve` — a non-starter for a shipped app (port
collisions, a second process to babysit; both hit in testing). It now **links the engine and reads
the ledger directly — no server, no port.**

Implemented:

- `cli.App.RefreshSnapshots(now) []lines.Snapshot` — a bounded, offline incremental scan plus the
  usage snapshot — called by `cmd/aispend-bar`; `internal/menubar` is the pure renderer.
- The old HTTP path (`aispend serve`, `internal/localapi`, the menubar HTTP client) had **zero
  consumers** once the bar went self-contained, so it was **removed** — no speculative "kept for
  external integrations" (YAGNI). If a real external consumer ever appears, it's ~200 lines to
  resurrect from git history.
- `caseymrm/menuet` stays out of `cmd/aispend`'s dep graph; the CLI stays cross-platform + offline.

## 2. Packaging the `.app` (macOS, cgo)

menuet is cgo + AppKit, so — unlike the static CLI — the bar can't cross-compile from Linux and needs
a **macOS runner** (this is why OpenUsage's release job is `runs-on: macos-*`).

1. Build `cmd/aispend-bar` for `darwin/arm64` + `darwin/amd64` (`CGO_ENABLED=1`).
2. `lipo -create` → **universal binary**.
3. Bundle → `aispend-bar.app` (Info.plist, `LSUIElement=1`) — productionize `cmd/aispend-bar/build-app.sh`.
4. Sign → (notarize) → package `.dmg`.

## 3. Signing: two phases (you're already on this path for the CLI)

Your cask ships the CLI **unsigned** with an `xattr -dr com.apple.quarantine` hook and the note
"remove once notarization is wired up." Same call for the bar:

- **Phase 1 — unsigned (~$0, ship now).** Ad-hoc-sign the `.app`, distribute the `.dmg`, document
  first-run (right-click → Open, or `xattr -dr com.apple.quarantine aispend-bar.app`). A cask can
  strip quarantine on install like the CLI one does. Rougher first-run than OpenUsage, but free and
  fine for an OSS v0.
- **Phase 2 — Developer ID + notarize (~$99/yr, OpenUsage-grade).** Exactly what OpenUsage does:
  import a Developer ID Application cert, `codesign` with hardened runtime, `notarytool submit --wait`
  + `stapler staple`, ship the signed DMG. Clean first-run, zero Gatekeeper friction. Add when
  traction justifies the $99.

**Recommendation: Phase 1 to launch, Phase 2 when it earns its keep.** Nothing in Phase 1 blocks Phase 2.

## 4. Auto-update (Phase 2)

OpenUsage uses **Sparkle** (EdDSA-signed appcast on GitHub Pages, stable + beta channels), which needs
the app signed (Phase 2). For Phase 1, a lightweight in-app "newer release on GitHub?" check that opens
the releases page works **unsigned** and is much simpler — good enough for v0. Graduate to Sparkle with
Phase 2 if desired.

## 5. Release pipeline (implemented)

- The goreleaser job (Linux runner) still ships the **CLI** — both SKUs, archives, checksums, brew
  cask — unchanged.
- A companion **`menubar` job** (`macos-latest`) in `.github/workflows/release.yml` ships the bar:
  `go get menuet` → `scripts/release-bar.sh` (universal `lipo` → `.app` → `.dmg`; ad-hoc-signed in
  Phase 1, Developer-ID-signed + notarized when the `APPLE_*` secrets are set) → attaches the `.dmg`
  to the tag's GitHub Release → `scripts/update-bar-cask.sh` pushes the `aispend-bar` cask to the tap
  (`brew install --cask cloudyali/tap/aispend-bar`), with the Phase-1 quarantine-strip hook.
- goreleaser OSS can't cgo-cross-compile or build `.app`/`.dmg`, so the bar deliberately lives outside
  goreleaser (noted at the top of `.goreleaser.yaml`) — the same call OpenUsage makes with its
  hand-rolled `script/release.sh`.
- **Phase 2 = flip signing on**: add repo secrets `APPLE_SIGN_IDENTITY`, `APPLE_ID`, `APPLE_PASSWORD`,
  `APPLE_TEAM_ID`; `release-bar.sh` then signs + notarizes automatically, and the cask's quarantine
  hook can be dropped.
- **Validate before relying on it**: the scripts pass `bash -n` and the workflow YAML is well-formed,
  but the macOS build/sign/dmg path can only be exercised on a macOS runner — cut a throwaway
  pre-release tag (e.g. `v0.0.0-rc1`) to smoke-test end to end.

## 6. Website (last mile, after artifacts exist)

`site/` needs:

- A **download hero**: menu-bar `.dmg` button (macOS) + CLI install (`brew` / curl).
- The menu-bar **screenshot** — "Claude $X · ROI 4.9× · cache saved 77%" — the wedge, front and center.
- A first-run note for Phase 1 (right-click → Open) until notarized.
- Positioning copy: *the accurate one* — today's real $ and ROI, versus quota-only trackers.

## 7. Codex plan (per-provider — already wired)

`codex_plan = "chatgpt-<tier>"` in `~/.aispend/config.toml` (or `aispend plans`) prices Codex against
its own subscription; otherwise it inherits the Claude default and mis-reports ROI. **Optional
enhancement:** Codex's `rate_limits.plan_type` field is present in the rollout data but currently
dropped by the parser — wire `plan_type` → catalog plan for auto-detection, with config as the fallback.

## Sequence

1. **Self-contained bar refactor** (`internal/usage` + bar local provider). ← next code step
2. Productionize `.app` bundling + universal build + `.dmg` (`script/release-bar.sh`).
3. Extend the release workflow (macOS job) + `aispend-bar` cask. Phase 1 unsigned.
4. Update the website (download + screenshot + copy).
5. (Later) Developer ID + notarize + auto-update = Phase 2.
