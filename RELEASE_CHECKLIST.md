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

- [ ] `cloudyali/homebrew-tap` repository exists and is writable by the token above.
- [ ] After a release, `brew install cloudyali/tap/aispend` works on a clean machine.
- [ ] **Known gap:** binaries are **unsigned/un-notarized**, so Gatekeeper quarantines
      browser-downloaded copies. The cask strips the quarantine bit as a stopgap
      (see `.goreleaser.yaml`); wire up Apple notarization when a Developer account is
      available, then remove the stopgap.

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
