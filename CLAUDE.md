# aispend — working notes for Claude

Local, explainable AI-coding spend. Scans Claude Code / Codex session logs, prices
each turn against a pinned table, stores an evidence ledger. Zero-dependency,
provably offline (`aispend doctor --network`). Go + stdlib `flag` (no cobra).

## Design docs

Full design record in `design-documents/` — start at `00-index.md`. For surface /
UX work, read the concept captures on demand: `design-documents/07-ui-concept.md`
(web) and `design-documents/08-cli-tui-concept.md` (CLI/TUI).

## Build & test

Pure-Go, vendored, no network needed.

```
go build ./cmd/aispend
go test ./...                 # keep green
go test ./internal/... -cover # 85–90% min per package
gofmt -l internal/ && go vet ./...
```

(Sandbox only: `go` isn't on PATH — extract the bundled `go1.25.*.tar.gz` to
`/tmp/go`, then `export PATH=/tmp/go/bin:$PATH GOFLAGS=-mod=vendor GOCACHE=/tmp/gocache`.
`go test -race` can't finish under the 45s cap; non-race + vet are the gate.)

## Conventions (non-negotiable)

- **t-wada-style TDD**: write the failing test first (confirm RED), minimal code to
  GREEN, then refactor. Every change lands with tests.
- **Coverage 85–90% minimum** per package.
- **YAGNI by default**: the laziest solution that works — stdlib/native before custom code or a new
  dependency, no abstraction with a single caller. The vendored **`ponytail`** skill
  (`.claude/skills/ponytail/`) is the dev-time reflex; **`ponytail-review`** backs the
  `/yagni-review` gate. Mark deliberate simplifications with a `ponytail:` comment (and name the
  ceiling + upgrade path for a shortcut).
- **Reviews**: run all three repo review commands on the change-set before done —
  `/review` (code), `/security-review`, `/yagni-review`. `/checkin-review` runs all three and
  prints one verdict; the `pre-push` hook calls it.

## Review & checkin automation

The review gates live in the repo as version-controlled Claude Code commands under
`.claude/commands/` (`review`, `security-review`, `yagni-review`, `checkin-review`), so every
contributor and every run shares the same definitions. They are invoked **consistently at
checkin** by git hooks in `.githooks/` (enable once with `scripts/setup-hooks.sh`):

- `pre-commit` — fast: `gofmt` on staged Go files (keeps "commit on green" cheap).
- `pre-push` — full gate: `gofmt` · `vet` · `go test` + the **85% coverage floor**
  (`scripts/coverage-gate.sh`, no exemptions) · both build SKUs · offline net-free assertion —
  then `/checkin-review` via your **local** Claude Code (no API key anywhere; advisory unless
  `AISPEND_AI_REVIEW_BLOCKING=1`).

GitHub Actions (`.github/workflows/ci.yml`) runs the deterministic, secret-free half on every
push/PR (coverage floor, both SKUs, offline egress assertion). The AI reviews stay **out** of CI
by design — rationale and the optional server-side upgrade path are in `docs/review-automation.md`.

**Security review is layered by depth:** `/security-review` (the fast per-diff checkin gate, driven
by Anthropic's **Security Guidance** plugin) runs every push; `/security-audit` (the **vendored
Cloudflare** six-phase, multi-agent skill in `.claude/skills/security-audit/`) is the deep whole-repo
audit, reserved for **pre-release** (`RELEASE_CHECKLIST.md` §3b) and on-demand — too heavy for every
push. See `docs/review-automation.md` § "Security review — two layers".

## CLI surface

One spend command, calendar-only windows (no rolling window):

```
aispend                        # DEFAULT CHANNEL → interactive TUI; falls back to `today` off a TTY / in the offline build
aispend tui [--period P] [--watch]   # interactive explorer: day-grouped session list (live badge + legend) → ↵ receipt (branch·SHA + cost+churn heatmap) → ↵ file → ↵ turn evidence; one ↑/↓ cursor flows files → top turns (tab jumps between them). --watch live-refreshes in place (periodic re-scan + clock advance). Visual times render in LOCAL tz; the backend/ledger stays UTC.
aispend report --period <today|yesterday|week|month|"last week"|"last month"|
                         quarter|"this year"|"N days"|"since YYYY-MM-DD"|
                         YYYY-MM-DD..YYYY-MM-DD|all> [--by G] [--view V] [--json]
                         # --by: model|repo|provider|cost_tag|session|branch|commit|file
aispend today                  # arbitrage-first daily glance: ROI, cache savings, hourly spike bar
aispend budget [set <amt>|clear] [--json] [--strict]
                                # monthly api-equivalent ceiling (off by default). Bare = month-to-date
                                # PACE (spent + run-rate projection vs ceiling, via the shared budgetPace);
                                # `set`/`clear` write `budget_usd` to ~/.aispend/config.toml (CLI surface
                                # for config.SetBudget). Informational only — never enforced. --json emits a
                                # stable pace object; --strict exits non-zero when projected > ceiling (CI gate).
aispend scan [--full] | doctor | plans
                                # scan: incremental import (watermark-gated); --full re-reads every
                                # session, ignoring the checkpoint, then resets it to the latest.
                                # NOTE: the `explain` command was removed — per-turn/session evidence
                                # now lives in the TUI drill (receipt → file → turn). Offline/non-TTY
                                # builds have no receipt surface.
aispend daemon [--interval D] [--once] [--verbose]
                                # background scan loop: scans once immediately (catch-up), then every
                                # D from the same per-provider checkpoint as scan-on-launch. D defaults
                                # to `scan_interval` in config.toml, else 15m; --interval overrides.
                                # --once runs a single cycle and exits (cron / launchd / systemd-timer
                                # entrypoint). Stops cleanly on Ctrl-C / SIGTERM. Offline-safe: local
                                # reads only, no price refresh. All output on stderr.
aispend pricing [refresh]       # show the active rate source; `refresh` pulls live LiteLLM rates
```

The **TUI is the default channel**: a bare `aispend` opens the interactive explorer
(`cmdDefault`); off a TTY or in the `offline` build (where the TUI is compiled out,
`tuiBuilt=false`) it falls back to the static `today` glance, which carries the same
numbers. `aispend help` still prints usage. Per-turn and per-session evidence lives
**only in the TUI** now (the CLI `explain` command was removed): drill session →
receipt → file → turn, each ↵ one level deeper. The receipt is one continuous ↑/↓
cursor (`recCursor`) over the file heatmap followed by the top turns — it flows from
the files straight into the turns, with `tab` as an accelerator that jumps between the
two sections (top of files ↔ first turn), and ↵ opens whatever is highlighted: a file
(→ file view) or a turn (→ evidence). The heatmap keeps at least the five priciest
files visible (or all, when fewer) so it never collapses on a short terminal. The
receipt carries the VCS linkage (branch · SHA + per-file cost+churn heatmap).
Offline/non-TTY builds (no TUI) therefore have no receipt surface.

**Scan-on-launch.** The read commands (`today`, `report`, `top`, `tui`, and the bare
default) run a **watermark-gated incremental scan before reading the ledger**, so
`aispend` works without a remembered `aispend scan` (install → run → value). It's quiet:
a one-line `scanned N new turn(s)` on **stderr** only when something was imported (so
stdout / `--json` stays pipe-clean), and silent on a fresh ledger. Opt out per-command
with `--no-scan`, globally with `AISPEND_NO_SCAN=1` (explicit truthy only — `1/true/yes/on`),
or persistently with `scan_on_launch = false` in `~/.aispend/config.toml`. `aispend scan`
remains the explicit import. Shared seam: `App.scanOnLaunch` → `App.incrementalScan` (also
the trailer hook's live refresh); offline-safe (local files only, no `net/*`). The opt-in
session-end **hook** (the "milestone" trigger) is still pending — see
`design-documents/12-surfaces-ingestion-roadmap.md` Item 7.

**Background daemon.** `aispend daemon` keeps the ledger current without any read
command: a `time.Ticker` loop (`daemonLoop`/`App.runDaemon`) that scans once
immediately, then every `scan_interval` (config.toml, default
`config.DefaultScanInterval` = 15m; `--interval` overrides; `--once` runs a single
cycle for an external scheduler). It reuses the **same** `App.incrementalScan` and the
**same** per-provider watermark as scan-on-launch — no separate checkpoint — so the
daemon, a manual `scan`, and a read-command launch all advance one pointer (idempotent
upserts collapse any overlap). Cadence resolves via `App.resolveScanInterval` (positive
`--interval` > `scan_interval` > 15m; non-positive rejected so `NewTicker` never panics).
It shuts down on the first Ctrl-C / SIGTERM (`signal.NotifyContext`) and is offline-safe
— local reads only, **no** price refresh (unlike the read-command launch). All output is
on **stderr** (startup banner, `[hh:mm:ss] scanned N new turn(s)` per non-empty cycle,
`--verbose` heartbeats idle cycles, shutdown line). The in-process loop is the chosen
mechanism; wrapping `--once` in launchd/systemd for reboot-survival is left to the user.

Rich static surfaces are **hand-rolled, zero-dependency ANSI** (no Bubble Tea /
lipgloss / x/term — keeps the offline-build + `doctor --network` promise). They
degrade to plain ASCII off a TTY, under `NO_COLOR`, or with `TERM=dumb`, and never
bleed an escape code into a pipe. `today` + the TUI receipt share the web
color language (cache-read blue, cache-write amber, output teal, input purple) and
the `pricing.WithoutCache` primitive for the `without cache ≈ $X · saved Y%` line.
The interactive TUI (`tui`/`watch`) stays deferred — see `08-cli-tui-concept.md`.

Pricing is **offline-first**: `scan`/`report` price against a fresh
(≤24h) LiteLLM cache at `~/.aispend/pricing/litellm.json` when present, else the
embedded table. Only `aispend pricing refresh` touches the network (one inbound GET
of a public file — `doctor --network` discloses it; the `offline` build compiles out
all `net/*`). LiteLLM rates overlay the embedded table, which remains the floor for
any model LiteLLM doesn't list. `ParseLiteLLM` canonicalizes upstream ids
(`canonicalizeModelID`: lowercase, strip `vendor/` prefix + `-YYYYMMDD` snapshot,
then the extensible `modelAliases` map for dotted versions) so overlay keys land on
the same ids the engine prices by; zero-priced LiteLLM stubs are excluded.

`report --json` (token-priced views) and the TUI's turn-level explain view both
surface a per-token-class cost breakdown: input / output / cache-read / cache-write / cache-write-1h.

## VCS linkage (sessions ↔ code)

Events carry git provenance so spend ties to shipped work. `event.GitBranch` is read
straight off the Claude Code line (it logs a branch per turn). `event.GitSHA` is the
commit that was HEAD at the turn's timestamp — the log has no SHA, so it's
reconstructed **best-effort** at scan time from the repo's reflog (`.git/logs/HEAD`)
by `internal/vcs.HeadAt`, **pure-Go, no git binary, no network** (the `offline` build
and `doctor --network` are untouched). `event.SessionChurn` is per-file line churn
(`git diff --numstat` between the session's first and last commit), captured once per
session via `vcs.Numstat` — the **one** git-binary dependency, isolated behind a hook
and still a local read. All three degrade to empty rather than guess: no repo, a
timestamp before the reflog, reflog expiry, or no git → the field is simply absent.
Because the ledger hashes paths (`CWDHash`, `SourcePathHash`), the real repo location
isn't recoverable later, so SHA + churn must be resolved at scan and frozen in the
event — never lazily at `explain` time.

Enrichment runs in the scan pipeline as `normalize.EnrichVCS` (after attribution,
before pricing; pricing is pure so order changes no number). New report facets group
on this: `--by branch`, `--by commit` (1:1, reconcile to the by-model total), and
`--by file` (fan-out — a turn's cost splits equally across the files it touched, so
file rows still sum to the total; fileless turns bucket as `(no files)`). The session
receipt adds a `branch · SHA` line and a per-file **cost+churn heatmap** (cost-shaded
bar + `+adds/-dels`), each row a real file that drills to evidence — not a vanity grid
(09-session-view.md reverses the earlier streak-heatmap cut on those grounds).

## Cache pricing (the subtle part)

Costs are dominated by cache on high-cache-hit workloads, so the cache rates matter
most. See `design-documents/05-llm-pricing.md`.

- **Anthropic**: two cache-write TTLs — 5-minute (default) = **1.25× input**, 1-hour
  (extended) = **2× input**; cache-read = **0.10× input**. TTL refreshes on each read.
  The normalizer reads `cache_creation.ephemeral_5m/1h_input_tokens`;
  `event.Tokens.CacheWrite1h` holds the 1-hour subset; the engine prices it at
  `2× input` (`oneHourCacheInputMultiple` in `internal/pricing/pricing.go`). The
  1-hour rate is derived in code, not a table column.
- **OpenAI / Codex**: **no cache-write charge** (`cache_write_per_mtok: 0`), cache-read
  ≈ **0.5× input** (the cached-input discount — NOT the 10% Anthropic heuristic),
  automatic TTL (no user tier) — the 1-hour multiplier never applies.
- **Gemini** (not yet ingested): billed by cache *storage time* (per-token-hour),
  a different cost dimension — needs its own model when added.

Reference tool for reconciliation: **CodeBurn** (TypeScript) splits the same TTL tiers
(`calculateCost`, `ONE_HOUR_CACHE_WRITE_MULTIPLIER_FROM_FIVE_MINUTE_RATE = 1.6`, i.e.
1.25 × 1.6 = 2.0× input) and pulls live rates from LiteLLM.
