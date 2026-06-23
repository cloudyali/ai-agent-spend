# Pre-release checklist (preview)

A practical run-list for cutting the first public **preview** release. Tick top-to-
bottom; items already done in this OSS-prep pass are marked ✅.

## 1. Branding & consistency

- [x] ✅ `AgentSpend` → `aispend` swept from Go doc comments and the `LICENSE` copyright line.
- [x] ✅ `design-documents/` verified clean — 0 occurrences of the old name remain.
- [ ] **`git add` the untracked files before tagging** — `LICENSE`, `docs/` (screenshots),
      `.aispend.toml`, and the new `CONTRIBUTING.md` / `SECURITY.md` / `RELEASE_CHECKLIST.md`
      / `.github/` templates are not yet tracked. A release without a committed `LICENSE`
      is a non-starter, and GoReleaser archives reference `LICENSE*`.
- [ ] Skim the README once more for stale `$`/number examples.

## 2. Version & tagging

- [ ] Pick the first preview tag (e.g. `v0.1.0` — the repo has **no tags yet**).
- [ ] Confirm version stamping still works: CI's "version stamping is wired" step
      builds with `-ldflags -X …/internal/cli.Version=…` and greps for it. (`Version`
      must stay a `var`, not a `const`.)
- [ ] `git tag vX.Y.Z && git push origin vX.Y.Z` triggers the release workflow.

## 3. CI is green

- [ ] `ci.yml` passes: `gofmt`, `go vet`, `go test` (incl. the non-UTC timezone re-run),
      `shellcheck`, and `install.sh` unit tests.
- [ ] **Decision (Windows framing):** CI runs on `ubuntu-latest` only today, which is
      why the README marks Windows 🧪 experimental and macOS "not yet in the matrix."
      To upgrade either claim, add `macos-latest` / `windows-latest` to the test job
      matrix first, then update the [Supported OS](README.md#supported-os) table.

## 4. Release build (GoReleaser)

- [ ] `goreleaser check` (needs GoReleaser ≥ v2.10 for `homebrew_casks`).
- [ ] Dry run: `goreleaser release --snapshot --clean` — confirm it produces the
      standard **and** `aispend-offline` archives for linux/darwin/windows × amd64/arm64,
      plus `checksums.txt`.
- [ ] Secret `HOMEBREW_TAP_TOKEN` is set (PAT with `contents:write` on the tap repo).
- [ ] `release.yml` and `pricing-sync.yml` workflows reviewed for the right triggers/secrets.

## 5. Homebrew tap (macOS)

How it works: on a `v*` tag, GoReleaser builds the archives + `checksums.txt`, cuts
the GitHub Release in this repo, then generates `Casks/aispend.rb` and commits it to a
**separate** tap repo. Users then `brew install cloudyali/tap/aispend` (the `homebrew-`
prefix is dropped). The `homebrew_casks` block and `release.yml` token wiring are
already done — these two manual steps are what's left:

- [ ] **Create the tap repo.** Make `cloudyali/homebrew-tap` on GitHub — **public**, may
      be empty (a bare README is fine). GoReleaser creates `Casks/` and `aispend.rb`
      itself, but it will **not** create the repo. (Repo name must keep the `homebrew-`
      prefix; `homebrew-tap` → users install `cloudyali/tap/aispend`.)
- [ ] **Provision `HOMEBREW_TAP_TOKEN`.** The default `GITHUB_TOKEN` cannot push to a
      second repo ("resource not accessible by integration"), so a fine-grained PAT is
      required. Steps:
    1. GitHub → **Settings → Developer settings → Personal access tokens → Fine-grained
       tokens → Generate new token**.
    2. **Token name:** `HOMEBREW_TAP_TOKEN`. **Expiration:** set a date and calendar the
       renewal (an expired PAT silently breaks the next release's tap push).
    3. **Resource owner:** the owner of `homebrew-tap` — the `cloudyali` **org** if it's
       org-owned, *not* your personal account. The org must have fine-grained PATs
       enabled; if it requires approval, an org owner approves it (auto if you're one).
    4. **Repository access:** Only select repositories → **`homebrew-tap`** (the
       destination, not `ai-agent-spend`).
    5. **Permissions → Repository → Contents: Read and write** (Metadata: Read is added
       automatically; nothing else is needed).
    6. Generate, copy the token, then add it under **`cloudyali/ai-agent-spend` → Settings
       → Secrets and variables → Actions → New repository secret**, named exactly
       `HOMEBREW_TAP_TOKEN`.
    - Prefill shortcut (still pick the repo + expiration in the UI):
      `https://github.com/settings/personal-access-tokens/new?name=HOMEBREW_TAP_TOKEN&target_name=cloudyali&contents=write`
    - Fallback: a classic PAT with the `repo` scope also works.
- [x] ✅ `release.yml` already passes `GITHUB_TOKEN` (this repo's Release) **and**
      `HOMEBREW_TAP_TOKEN` (cross-repo tap push) to GoReleaser.
- [ ] Dry-run the cask locally before trusting CI: `goreleaser release --snapshot --clean`,
      then `brew install --cask ./dist/.../aispend.rb` (or inspect the generated `.rb`).
- [ ] After a real release, `brew install cloudyali/tap/aispend` works on a clean machine.
- [ ] **Known gap (unsigned):** binaries are **unsigned/un-notarized**, so Gatekeeper
      quarantines browser-downloaded copies. The cask strips the quarantine bit as a
      stopgap (see `.goreleaser.yaml`); wire up Apple notarization (§5b) when a Developer
      account is available, then remove the `xattr` hook.

## 5b. Code signing (when ready — optional for preview)

- [ ] **macOS notarization** — biggest UX win, runs from the current Linux CI via
      GoReleaser's cross-platform `quill` path. Needs an Apple Developer account
      ($99/yr), a **Developer ID Application** cert (`.p12`), and an App Store Connect
      API key (`.p8`). Add a `notarize.macos` block + the base64 secrets, then drop the
      `xattr` cask hook. Docs: <https://goreleaser.com/customization/sign/notarize/>.
- [ ] **Checksums signing** — free supply-chain signal; add a `signs`/cosign block to
      sign `checksums.txt`.
- [ ] **Windows signing** — optional; an unsigned `.exe` only shows a SmartScreen prompt.
      Skip EV certs (they no longer bypass SmartScreen since 2024). Cheapest modern path
      is Azure Trusted Signing (~$9.99/mo, signable from CI via `jsign`) — but check
      eligibility (US/Canada individuals, or US/Canada/EU/UK orgs) before relying on it.

## 6. Screenshots (currently placeholders)

- [ ] Replace the placeholders in `docs/screenshots/` with **real** captures:
      `today.png`, `tui.png`, `receipt.png`. The README still labels them placeholders —
      remove that note once they're real. (See `docs/screenshots/README.md`.)

## 7. Docs & community files

- [x] ✅ README: added **Use cases** and **Supported OS**, with Windows as experimental.
- [x] ✅ `CONTRIBUTING.md` (TDD, coverage floor, reviews, both-build gate, Conventional Commits).
- [x] ✅ `SECURITY.md` (private reporting, offline trust model, supported versions).
- [x] ✅ Issue templates (`bug_report.yml`, `feature_request.yml`, `config.yml`) + `PULL_REQUEST_TEMPLATE.md`.
- [ ] Optional: add a `CODE_OF_CONDUCT.md` (the [Contributor Covenant](https://www.contributor-covenant.org/)
      is the usual choice). CONTRIBUTING currently carries a short conduct note instead.
- [ ] Optional: `CHANGELOG.md` — or rely on GoReleaser's generated GitHub release notes
      (the `changelog` block already groups by `feat:` / `fix:`).

## 8. GitHub repo settings

- [ ] **Enable Discussions** — the issue-template `config.yml` links to it.
- [ ] **Enable private vulnerability reporting** (Settings → Security) — `SECURITY.md`
      and the issue `config.yml` both point at Security Advisories.
- [ ] Set the repo **description** and **topics** (e.g. `ai`, `claude-code`, `codex`,
      `finops`, `cli`, `golang`, `cost-tracking`).
- [ ] Confirm Issues are enabled and the templates render (Issues → New issue).
- [ ] Add a repo **About** link to `aispend.cloudyali.io`.

## 9. Final smoke test

- [ ] Fresh `git clone` → `go build ./cmd/aispend` works with no network.
- [ ] `go install github.com/cloudyali/ai-agent-spend/cmd/aispend@vX.Y.Z` works post-tag.
- [ ] `install.sh` installs the tagged release and verifies the checksum.
- [ ] `aispend doctor --network` passes on both the default and `offline` builds.
- [ ] `aispend today` / `report` / `top` run against a real `~/.claude` / `~/.codex`.
