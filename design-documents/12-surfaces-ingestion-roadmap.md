# 12 — Surfaces, ingestion, and scale: the post-teardown roadmap

Status: **Planning / proposal capture · 2026-06-23 · owner: Nishant.** Threads into
`06-provider-coverage-backlog.md` (coverage), `07-ui-concept.md` (web UI),
`10-session-explorer-budgets-quota.md` (quota/budgets), and
`phase-2-cloudyali-reconciliation.md` (reconciliation). This doc sequences nine items
that came out of the AIMeter / TokenTracker teardown into a build order; full
phase-style specs get split out per item when each one is picked up.

**Decisions already taken (this doc builds on them, doesn't re-litigate them):**

- **Storage stays pure-Go, benchmark-gated.** `events.json` remains canonical; SQLite is
  evaluated as a *spike*, adopted only if it's pure-Go (no CGO) **and** a benchmark proves
  a real bottleneck the stdlib can't close. (Item 8.)
- **Web UI: static HTML now, served dashboard next phase.** `report --html` ships a
  self-contained offline file now; a served localhost dashboard is a separate, build-tagged
  SKU in the next phase — never in the default or offline build. (Items 1, 9.)
- **Coverage: clean-token first (Gemini CLI, OpenCode).** Cursor is deferred — its tokens
  are fabricated from char counts (doc 06) and it's SQLite-only. (Item 3.)

## The bet (why these, in this order)

The teardown's one-line finding: **aispend is ahead on substance, behind on surface.** It
already ships quota windows, budgets, watch-mode, cache-tier pricing, and VCS linkage that
neither competitor has — but TokenTracker has 104 stars to our 0, because a dashboard
screenshot travels and a TUI doesn't. So this plan borrows *reach mechanics*, not *trust
compromises*. Every line below passes one test:

> **Does it survive `aispend doctor --network`, and does every number stay drillable?**
> If yes, take it. If no, find the on-ethos adaptation, or it waits.

That test does almost all the sorting: it's why the menu bar is a plugin and not a Swift
app, why the web dashboard is a build-tagged SKU and not the default, why hooks are opt-in
and only trigger the same local scan, and why we ingest a gateway's data instead of becoming
one.

## Non-negotiables this plan preserves (from 00-index §Non-negotiables)

These are invariants, not aspirations — each risky item below shows its work against them:

- **Local by default, truthfully.** The default `go build` stays net-free; anything that can
  reach the network lives behind a build tag and is disclosed by `doctor --network`.
- **Evidence over assertion.** Every number a new surface renders still drills to its
  `file:line` + pricing rule. A prettier surface that can't be opened is a regression.
- **No single "true cost."** New providers and surfaces respect the cost-view model; a value
  that can't be computed reads "not computable," never `$0`.
- **Money is never a float.** Integer micros end-to-end, including in HTML/JSON emission.
- **Additive, never subtractive.** No item here removes a local feature or bolts an account
  onto the local tool.
- **Process:** t-wada TDD (RED → GREEN → refactor), **85–90% coverage per package**, and the
  **code-review skill + Security-Guidance security review** gate before any item is "done."

## The waves at a glance

Sequenced so people can *get in* (A), then have something to *share* (B), then have it *see
more* (C), before we *scale the surface* (Next). Marketing-relevant waves are front-loaded,
per the current priority.

| Wave | Items | One-line goal | Gating test it must pass |
|---|---|---|---|
| **A — Onboarding & reach** | 7/4 scan cadence · 2 menu-bar glance | Install → run → value, with an always-on glance | No background daemon by default; glance is local-only |
| **B — The shareable surface** | 1 `report --html` (+TUI export) · 6 quota in report/HTML | One offline file you'd screenshot into a FinOps thread | HTML emits zero `net/*`; every number still drillable |
| **C — Ingestion depth & scale** | 3 Gemini + OpenCode · 6 quota breadth · 8 SQLite spike | See more agents, honestly; prove storage scales | New tokens are clean or flagged; storage stays pure-Go |
| **Next / Phase 2** | 9 served web UI · 5 gateway ingest | A real dashboard + close the direct-API blind spot | Served UI is a tagged SKU; we ingest, never intercept |

---

# Wave A — Onboarding & reach

## Item 7 + 4 — Scan cadence: scan-on-launch, milestone-based refresh, opt-in hook

> **Status (2026-06-23): scan-on-launch shipped under t-wada TDD.** `today`/`report`/`top`/`tui`
> (and the bare default) run a watermark-gated incremental scan before reading the ledger; a
> one-line `scanned N new turn(s)` lands on **stderr** only when something imported (stdout/`--json`
> stays clean), silent on a fresh ledger. Opt-outs: `--no-scan`, `AISPEND_NO_SCAN=1` (explicit truthy
> only), `scan_on_launch=false` (`config.LoadScanOnLaunch`). Seam: `App.scanOnLaunch` →
> `App.incrementalScan` (shared with the trailer hook's live refresh); offline-safe, no `net/*`.
> Coverage: config 91%, the new cli funcs ~100% (defensive provider-error branch aside); offline
> build + `doctor --network` verified unchanged. **Remaining:** the opt-in session-end **hook** (the
> "milestone" trigger) and a stricter mtime pre-gate (skip the stat-walk when nothing changed).

> **Status (2026-06-24): opt-in `aispend daemon` shipped under t-wada TDD.** A wall-clock
> background loop (`daemonLoop` → `App.runDaemon`, `internal/cli/daemon.go`) that scans once
> immediately, then every `scan_interval` (config.toml, default `config.DefaultScanInterval` =
> 15m; `--interval` overrides; `--once` = one cycle for an external scheduler). It reuses
> `App.incrementalScan` and the **same** per-provider watermark as scan-on-launch — one
> checkpoint, idempotent upserts collapse overlap. Stops on Ctrl-C/SIGTERM
> (`signal.NotifyContext`); offline-safe (local reads only, **no** price refresh); all output on
> stderr. **This refines the "not a wall-clock daemon" stance below, it doesn't break it:** the
> daemon is **never auto-started** — it's an explicit command the user runs, so the default
> posture still owns no background process. Running `aispend daemon` (or wrapping `--once` in
> launchd/systemd) is the *user's* chosen schedule, the same principle as "the schedule is the
> user's, not ours" (Item 2). Coverage: config `LoadScanInterval` table-tested; `daemonLoop`,
> `runDaemon`, `resolveScanInterval`, and the `--once` path covered. The signal-wiring sliver in
> `cmdDaemon` is the only intentionally-untested line (real SIGTERM would kill the test process).

> **Status (2026-06-27): the TUI runs an in-process periodic sync by default, plus a last-sync
> header stamp (t-wada TDD).** The interactive explorer's background re-scan is now ON without
> `--watch`: `cmdTui` resolves the cadence via `App.tuiSyncInterval` — the daemon cadence
> (`config.scan_interval`, else `DefaultScanInterval` = 15m) by default, the 3s live tick under
> `--watch` — and drives the existing `WithWatch` → `reloadCmd` loop, which already reuses
> `App.incrementalScan` and the one per-provider watermark (idempotent upserts; the tui serializes
> reloads, so no overlapping writers). The header gains a `synced Nm ago` segment
> (`tui.WithSyncStatus` ← `App.lastSyncTime`, the newest provider watermark), re-read each refresh
> so the in-process sync's advance is visible. **This refines the "never auto-started" stance — it
> doesn't break it:** the *OS* `aispend daemon` process is still never spawned on the user's behalf;
> the periodic sync here lives **inside the foreground TUI process and dies with it** — no separate
> process we own, no reboot-survival, no push anywhere. One-shot surfaces (`today`/`report`/`top`,
> and the non-TTY/offline fallback) keep their single in-process scan-on-launch — they render and
> exit, so there's no loop to host. Coverage: `tuiSyncInterval` (default vs `--watch`) and
> `lastSyncTime` (newest watermark; zero when unscanned) unit-tested; the header `synced` segment
> covered in `internal/tui`.
>
> **Follow-up (2026-06-27): the freshness stamp ages via a cheap clock heartbeat.** The relative
> stamp is `relAge(now − watermark)`, but `now` only advanced on the reload tick — which, on the
> 15m sync cadence, ALSO reset the watermark — so it sat frozen at "synced just now". Fix:
> `tui.WithClockTick` (a separate `clockMsg` heartbeat, `tuiClockInterval` = 30s, wired only when
> NOT `--watch`) advances the model clock and repaints **without** re-scanning or touching the
> store — the reload tick owns data, the heartbeat owns time. The stamp now grows 0→15m between
> syncs and snaps back to "just now" only when a real scan moves the watermark; "live" badges decay
> on the same beat. No new I/O (so it's not the "battery-burning timer" the milestone bullet warns
> against — pure clock + terminal diff, foreground-only). Covered by `TestModel_ClockTickAgesSyncStamp`.

> **Follow-up (2026-06-27): on-demand sync — a `sync` command and a TUI `s` key, both single-flight
> ("if a sync is already running, do nothing").** Two surfaces, one capability: a way to force a
> sync *now* without waiting out the cadence. (a) **CLI `aispend sync`** (`internal/cli/sync.go`):
> a bounded price refresh-if-stale + an incremental ledger scan, concise summary on stdout — the
> explicit verb that pairs with the `synced …` stamp, reusing `refreshIfStale` + `incrementalScan`
> (no new ingestion path). (b) **TUI `s`** (`Model.syncCmd` → `syncDoneMsg`): the same `WithWatch`
> reload, off the UI loop, folded back **without** re-arming the watch tick (a one-shot, not a
> cadence beat). The single-flight guard differs by surface, matching where "already running" is
> real: the CLI takes a **cross-process advisory lock** (`acquireSyncLock`/`guardedScan`,
> `internal/cli/synclock.go` — `~/.aispend/sync.lock`, pure-mtime TTL of 10m, **fail-open**, no
> PID/liveness syscalls so it's cross-platform + offline-safe) that **also gates the daemon's
> cycles**, so `sync`, a second `sync`, and the daemon never double-scan; the TUI uses an
> **in-process** `Model.reloading` flag (an `s` or a watch tick while a reload is in flight is a
> no-op). **Stays on-ethos:** no net (`sync`'s only network is the same disclosed price GET behind
> the refresh opt-outs, compiled out of the offline build), every number still drills, and the
> lock is advisory + self-healing (a crashed holder is stolen after the TTL), never a wedge. This
> refines the cadence story; it doesn't add a process we own — `sync` runs and exits, the lock is
> released on return. Coverage: `synclock_test.go` (acquire/steal/guard), `sync_cli_test.go`
> (import / idempotent / no-op-when-held / stdout), and `on_demand_sync_test.go` (the TUI key,
> guard, and no-tick-rearm) — all t-wada RED→GREEN.
>
> **Follow-up (2026-06-27): the `s` keypress announces itself — an in-progress `syncing…`
> stamp, then back to `synced …`.** Pressing `s` used to mark the sync in flight silently
> (the screen looked frozen until the reload landed). Now a `Model.syncing` flag — set ONLY
> by the manual `s` path, never by a background watch tick, so the periodic sync stays quiet —
> swaps the header's freshness segment to `syncing…` the frame after the keypress (Bubble Tea
> paints the returned model before running the off-loop reload cmd), and the segment resumes
> the stamp, snapped to `synced just now`, when `syncDoneMsg` folds the result back. The
> segment only renders when sync-status is wired (`syncFn != nil`, which the cli always pairs
> with the reload), so callers without it are unchanged. Covered by
> `TestModel_OnDemandSync_ShowsSyncingThenSynced` (pre-stamp → `syncing…` → `synced just now`).

**Goal.** Collapse "install → `scan` → `today`" into "install → run." A bare `aispend`,
`today`, `report`, or the TUI brings its own data current; the manual `scan` step stops being
a prerequisite. Keep `scan` as an explicit command — we add a trigger, we don't remove the
control.

**Extends / phase:** 0A polish · touches `internal/scan`, `internal/cli`, new
`internal/githook`-style agent-hook installer.

**In scope.**
- **Scan-on-launch, watermark-gated.** Commands that read the ledger first run an incremental
  scan *iff* the watermark is stale (the watermark already exists; incremental scan is cheap).
  Print one line (`scanned 3 new sessions since 09:14`) or stay silent when nothing's new. A
  `--no-scan` escape hatch and a `scan_on_launch = false` config key for people who want the
  old explicit flow.
- **Milestone-based refresh, not a wall-clock daemon *by default*.** "Periodic" = triggered by
  milestones, not a battery-burning timer: (a) on launch, (b) on the TUI's in-process periodic
  sync (default-on at the 15m daemon cadence; `--watch` = the 3s live tick — see the 2026-06-27
  status note above), (c) optionally on agent **session-end** via an opt-in hook (below). No
  always-running background process *that we own outside a foreground surface*: the TUI's loop is
  bounded by the TUI's own lifetime and dies with it. *(An explicit, opt-in OS-level wall-clock
  loop also exists as `aispend daemon` for users who want one that survives the foreground — see
  the 2026-06-24 status note above. That process is never auto-started.)*
- **Opt-in `aispend hooks install`.** Mirrors `aispend git install` exactly: a safe,
  disclosed, uninstallable hook in the agent's config (Claude Code `SessionEnd`, Codex TOML
  `notify`) that runs `aispend scan` when a session ends — near-real-time without us polling.
  It triggers the **same local scan**; it introduces **no new data path** and sends nothing.

**Out of scope.** A background daemon/agent by default; any push of data anywhere; modifying
agent config without `hooks install` consent.

**Key decisions + rationale.**
- *Why not adopt TokenTracker's always-installed hooks as the default?* Installing into a
  tool's config is a write and a footprint a security reviewer will ask about. Passive scan +
  `watch` already gets ~all the freshness with zero writes. "We don't modify your tools' config
  unless you ask" is itself a differentiator — keep it the default and make hooks the opt-in.
- *Why watermark-gate launch scans?* Surprise latency on `today` is the failure mode; gating on
  the watermark keeps the common case ~instant and silent.

**Demonstratable output.**
```text
$ aispend today           # no prior `scan`
scanned 5 new sessions · 41 turns (1.2s)
aispend today · Tue Jun 23
  $14.02 api-equivalent · plan $7.33/day · 1.9× ROI
  …

$ aispend hooks install
✓ aispend session-end hooks installed (claude_code, codex)
  a scan runs when a session ends · `aispend hooks status` · `aispend hooks uninstall`
```

**Acceptance criteria.**
- [x] Stale ledger → launch triggers an incremental scan; fresh ledger → no scan, no output. **(2026-06-23)**
- [x] `--no-scan`, `AISPEND_NO_SCAN=1`, or `scan_on_launch=false` fully suppress launch scans;
      the notice prints to **stderr** only when something imported (stdout/`--json` clean). **(2026-06-23)**
- [ ] `hooks install/status/uninstall` are idempotent, honor existing hook managers, and only
      ever invoke a local `scan` (asserted: no network symbol reachable from the hook path).
      *(opt-in session-end hook — still pending; scan-on-launch landed first)*
- [x] Offline build behavior unchanged; `doctor --network` still PASS. **(verified 2026-06-23)**

**Test & quality.** TDD the watermark-gate decision (fresh/stale boundary), the silent-when-empty
path, and hook install/uninstall idempotency against a temp agent-config fixture. Security review
focuses on the config-write surface (path traversal, clobbering a user's hook). Coverage ≥85%.

**Risks.** Launch-scan latency on huge first scans → show progress + keep it incremental. Hook
writing into agent config → consent-gated, disclosed, reversible.

## Item 2 — Menu-bar glance (plugin, not an app)

**Goal.** Pin today's spend + the tightest quota window to the menu bar / status line, so the
number is glanceable without opening anything — TokenTracker's strongest daily-active hook,
captured for ~1% of the effort.

**Extends / phase:** 0A satellite · new `aispend glance` subcommand + a `contrib/` plugin folder.

**In scope.**
- **`aispend glance [--json]`** — a compact, stable, one-line status payload: today's
  api-equivalent, ROI multiple, the tightest quota gauge (`% used` + reset), and a pending-commit
  hint if trailers are on. JSON for machine consumption; a plain `▲ $14.02 · 1.9× · ⏳ Claude 78%`
  string for direct embedding.
- **`contrib/` plugins** that poll it: **xbar / SwiftBar** (macOS), **waybar / polybar / tmux**
  (Linux). Each is a tiny script calling the binary on the user's chosen interval, plus a
  screenshot for the README.

**Out of scope (for now).** A native Swift menu-bar app and desktop widgets — second codebase,
macOS-only, Gatekeeper "damaged app" friction (TokenTracker's README spends paragraphs on it).
Deferred until there's pull; `glance` is the seam it'd reuse anyway.

**Key decision.** The glance string is local-only and offline-safe; the *plugin's* refresh cadence
is the user's, not ours — so there's still no daemon we own.

**Demonstratable output.**
```text
$ aispend glance
▲ $14.02 today · 1.9× ROI · Claude weekly 78% (resets 2d 4h)

$ aispend glance --json
{"api_equiv_usd":14.02,"roi":1.9,"quota":{"provider":"anthropic","window":"weekly","used_pct":78,"resets_in_s":187200}}
```

**Acceptance criteria.**
- [ ] `glance --json` is schema-stable (documented keys) and degrades each field to `null`/absent
      honestly (no plan → no ROI; no quota sample → no quota block).
- [ ] xbar + waybar scripts render from `glance` with a screenshot in the README.
- [ ] `glance` reachable in the offline build (local-only).

**Test & quality.** Golden JSON + golden string with injected clock/zone; field-absence cases.
Coverage ≥85%. Marketing: the menu-bar screenshot is a launch asset.

---

# Wave B — The shareable surface

## Item 1 — `aispend report --html` (and TUI export)

**Goal.** Emit a **single self-contained HTML file** — the arbitrage/cache chart, the explain
receipt, and the cost+churn heatmap — that you'd screenshot into a FinOps thread or a tweet.
It's a *file write, like `--json`*, not a server: it survives `doctor --network` and works in the
offline build. This is the highest-leverage marketing move on the board and the SSR foundation
the served dashboard (Item 9) reuses.

**Extends / phase:** 0A/0B · new `internal/htmlreport` (name TBD) + `report --html`; TUI export key.

**In scope.**
- **A shared view → renderer seam.** The same view structs that already feed `report` / `today` /
  the receipt feed three renderers: ANSI (today), JSON (`--json`), and **HTML**. One source of
  numbers, three skins — so the HTML can never disagree with the terminal.
- **Self-contained + offline.** Inline CSS; charts rendered **server-side as inline SVG** (reusing
  the sparkline/bar math the ANSI layer already computes) — **no CDN, no JS, no web fonts**. The
  file opens in any browser with the network off. (This is the HTML/SVG analog of our hand-rolled
  zero-dep ANSI layer.)
- **Still drillable (the moat).** Each number carries its evidence in an inline `<details>`
  slide-over: `tokens × rate` per class, the dedup decision, the pricing rule, `file:line`. A
  prettier surface that drops the drill is a regression, not a feature.
- **`--card` slim variant.** A compact "report card" (hero arbitrage number + cache-saved + top
  models + tightest quota) sized for screenshotting — 07's "report card PNG (marketing flywheel),"
  delivered as shareable HTML/SVG.
- **TUI export.** A key (`x`) writes the current period/selection to HTML and prints the path
  (optionally opens via `open`/`xdg-open` — a process exec, not a network call).

**Out of scope.** A served/interactive dashboard (Item 9). PNG rasterization in-process (it'd pull
a headless browser or an image dep) — users screenshot the `--card`; a rasterizer is a later
nicety, not a dependency we take now.

**Key decisions + rationale.**
- *SVG, not Chart.js-from-CDN.* A CDN chart needs the network to render, breaking "opens offline"
  and the provability story. Hand-rolled SVG keeps the same zero-dep promise that makes the ANSI
  layer trustworthy.
- *Ships in default **and** offline builds.* Because it's pure templating with no `net/*`, even the
  air-gapped SKU can emit the shareable report — a selling point, and the CI import-graph assertion
  (the one `doctor` already uses) proves it.

**Demonstratable output.**
```text
$ aispend report --period week --html=week.html
wrote week.html (self-contained · 84 KB · no network) · open in a browser

$ aispend report --period week --card --html=card.html
wrote card.html (report card · screenshot-ready)
```
(In the TUI: `x` on any session/period → `wrote ~/…/aispend-rajgad-week.html`.)

**Acceptance criteria.**
- [ ] `--html` output contains **no** external references (no `http(s)://`, no CDN, no remote
      font) — asserted by a test that greps the emitted file.
- [ ] The HTML renderer imports no `net/*` — asserted via the import-graph check; `--html` works in
      the offline build.
- [ ] Every rendered number has an evidence panel reachable in the file; numbers reconcile
      byte-for-byte with `report --json` (same view structs).
- [ ] Money stays integer micros through emission; `nil` views render "not computable."
- [ ] Golden HTML + golden SVG snapshots are deterministic under injected clock/zone.

**Test & quality.** Golden-file TDD (mirror the ANSI golden approach). The "no external refs" and
"no net import" assertions are first-class tests. Coverage ≥85%. Security review: HTML-escaping of
prompt-derived strings (session titles, file paths) to prevent injection when the file is opened —
this is the main new attack surface and must be reviewed.

**Risks.** SVG chart fidelity vs. effort → reuse existing bar/sparkline math; start with the bar
+ stacked-arbitrage chart, add the faceted view later. HTML injection from log-derived text →
escape on the way in; covered by security review.

## Item 6 — Quota windows: add breadth + surface in `report` and HTML (and TUI)

**Goal.** The quota gauge already lights on `today` and the TUI header for Claude + Codex
(doc 10). Extend it two ways: **more providers** (as they're added in Item 3) and **more
surfaces** (`report` and the HTML report), keeping it a separate gauge that never folds into a
ledger total.

**Extends / phase:** extends `10-session-explorer-budgets-quota.md` · `internal/quota`,
render layer.

**In scope.**
- **Surface in `report`.** A header gauge block above the grouped table (same `Model.WithQuota`
  data), behind nothing — or a `--quota`/`--no-quota` toggle if it crowds piped output.
- **Surface in HTML (Item 1).** The dollarized-wall gauge in the shareable report — this is a
  view competitors don't have, so it belongs on the screenshot.
- **Breadth.** For each new provider (Item 3), parse a local quota/rate-limit sample **if one
  exists on disk** (verify-on-disk per doc 06); otherwise render the honest
  `<provider> weekly — unknown (no local snapshot)` line. No new online calls — the held opt-in
  `quota refresh` (doc 10 §D.1) stays held.

**Out of scope.** Folding quota into spend totals (category error, doc 10). Online quota polling
(decided: hold).

**Key decision.** Reuse `quota.Tracker`'s reduce-to-latest model verbatim; new providers add a
`quota.Parse<Provider>` + a source string, nothing more. Provability rules from doc 10 (source +
as-of, expire-past-reset, "unknown" not `0%`) apply unchanged.

**Demonstratable output.**
```text
$ aispend report --period week
AI-coding spend · this week · by model · view: api-equivalent …
  Claude weekly   ████████████████░░░  78% · resets Thu 09:00 (2d 4h)
  Codex  weekly   ███░░░░░░░░░░░░░░░░░   6% · resets in 3d
  claude-opus-4-8  $8.74  …
  …
```

**Acceptance criteria.**
- [ ] Quota gauge renders in `report` and in the HTML report, sourced + as-of stamped, expiring a
      sample older than its reset.
- [ ] A provider with no local sample degrades to an explicit "unknown" line, never `0%`.
- [ ] Quota never enters a spend total in any surface.

**Test & quality.** Extend quota parser tests per new provider; golden render in `report`/HTML.
Coverage ≥85% (quota pkg already at 93%).

---

# Wave C — Ingestion depth & scale

## Item 3 — Broader coverage: Gemini CLI, then OpenCode (clean-token first)

**Goal.** Widen the addressable tool set without diluting the ledger — add the **high-value,
clean-token** agents first (doc 06), so each new provider *strengthens* the evidence story rather
than importing fabricated numbers.

**Extends / phase:** extends `06-provider-coverage-backlog.md` (0B/1A) · one `Provider` +
`Normalizer` (+ `Deduper`) + fixtures + golden per agent, the Claude Code shape.

**In scope — in order.**
1. **Gemini CLI.** JSONL under `~/.gemini` with a native, clean prompt/candidate/cache/total split.
   Two traps from doc 06: telemetry/usage is **off by default** (detect + tell the user how to
   enable), and the `tmp/` location gets cleaned up (capture before it's gone). **Pricing caveat:**
   Gemini bills cache by *storage-time* (per-token-hour), a different dimension than Anthropic/OpenAI
   per-token cache — so price input/output normally and mark Gemini **cache cost "not computable"**
   until the storage-time model lands (honesty rule), rather than misapplying a per-token cache rate.
2. **OpenCode.** Per-message data in **JSON files and SQLite**. Prefer **JSON-only ingestion** to
   avoid taking a SQLite-read dependency (verify on disk that JSON is complete); its `cost` field is
   often `0`, so ignore it and token-price. Dedupe the dual-source overlap; watch unbounded store
   growth.

**Out of scope.** **Cursor** — fabricated char-count tokens (a liability to the evidence brand) and
SQLite-only; deferred. When demand forces it, ship it with tokens explicitly flagged low-confidence,
never folded into a "true" total — and only after the storage decision (Item 8) settles whether we
have a pure-Go SQLite reader at all.

**Key decisions + rationale.**
- *Clean-token-first is the wedge, not a compromise.* "We won't show you a number we can't stand
  behind" is a sharper story than "we support more tools." Gemini/OpenCode raise the tool count
  while *raising* average trust; Cursor would lower it.
- *JSON-over-SQLite for OpenCode* keeps the zero-dep promise intact and sidesteps the Item-8
  dependency question entirely where possible.

**Demonstratable output.**
```text
$ aispend scan
claude_code · imported 17 · …
codex · imported 6 · …
gemini · 1 source · imported 9 · cache: not computed (storage-time model pending)
opencode · 1 source · imported 14 · reported-cost ignored (token-priced)
Imported 46 events · no network calls made

$ aispend report --period week --by provider     # the 0B "you pay for N tools" finding
```

**Acceptance criteria.**
- [ ] Gemini + OpenCode each: a `Provider`+`Normalizer`(+`Deduper`)+fixtures+golden, verified
      against a real install per doc 06 §"verify on disk."
- [ ] Token classes are sourced from native clean fields; any non-computable class (Gemini cache,
      zero `cost`) is flagged, never fabricated.
- [ ] Cross-tool `report --by provider` reconciles to the by-model total.
- [ ] Each new adapter clears ≥85% coverage with its own golden.

**Test & quality.** Fixture-driven golden tests per agent; dedupe boundary tests for OpenCode's
dual source. Reference ccusage/CodeBurn for each rather than reverse-engineering (review-log §10).
Security review: every new adapter widens the privacy surface (these files hold source + secrets) —
confirm hash-on-ingest holds.

## Item 8 — SQLite evaluation: a benchmark spike, pure-Go-gated

**Goal.** Decide *with data*, not vibes, whether the `events.json` ledger needs a different storage
engine at scale — under a hard constraint: **pure-Go (no CGO), and only if it beats the stdlib.**
The "one binary, no database to run, provably offline" line is marketing-load-bearing, so the bar
to add a dependency is high and explicit.

**Extends / phase:** 0B/1A spike · `internal/store` · a `benchmarks/` harness. **This is an
evaluation task with a decision gate, not a committed migration.**

**The spike, step by step.**
1. **Baseline the current mechanism.** Benchmark `events.json` load / append / windowed-aggregate at
   realistic scale — **10k, 100k, 1M events** — capturing cold-start latency, peak memory, and
   `report` aggregation time. (t-wada discipline: measure before optimizing.)
2. **Define "real bottleneck" up front.** e.g. cold-start > 300 ms or peak memory > ~200 MB at the
   scale a heavy daily user hits in a year. If we don't cross it, **we stop here** and keep JSON.
3. **Evaluate pure-Go candidates against the baseline**, in rough order of ethos-fit:
   (a) a **stdlib index / streaming reader** so a windowed query doesn't load every event (likely the
   cheapest win); (b) an **append-only log + periodic compaction**; (c) **`modernc.org/sqlite`**
   (pure-Go) — but measure the **binary-size hit** (the offline SKU is 4.5 MB today and that number
   is itself a selling point) and the added vendored dependency-audit surface.
4. **Decision gate, recorded in this doc.** Adopt SQLite **only if** it beats (a)/(b) **and** stays
   pure-Go **and** the binary-size/offline story survives. Otherwise ship the stdlib index. Likely
   outcome (hypothesis to disprove): a streaming index closes the gap and we keep JSON canonical —
   but the benchmark decides.

**Out of scope.** `mattn/go-sqlite3` or any **CGO** driver (breaks static cross-compile and the
single-binary promise). A storage rewrite before the benchmark justifies one.

**Demonstratable output.**
```text
$ go test ./benchmarks -run XXX -bench Store
BenchmarkLoad/json/1M-10        … 1420 ms/op   612 MB
BenchmarkLoad/index/1M-10       …  240 ms/op    71 MB
BenchmarkLoad/sqlite/1M-10      …  180 ms/op    48 MB  (binary +3.1 MB)
DECISION: index/streaming adopted; SQLite held (size cost > marginal win)   # recorded in §gate
```

**Acceptance criteria.**
- [ ] A reproducible benchmark harness at 10k/100k/1M with documented thresholds.
- [ ] A recorded decision with the numbers behind it; if SQLite is adopted, proof it's CGO-free and
      the offline-build size delta is acceptable.
- [ ] `doctor --network` and the offline build are unaffected whatever wins.

**Test & quality.** The harness is the artifact; any new store impl reuses the existing store test
suite (same contract, new backend). Coverage ≥85% on whatever ships.

**Risks.** SQLite dep vs the offline-size marketing number — gated explicitly. Scope creep into a
rewrite — the gate exists to prevent it.

---

# Next / Phase 2

## Item 9 — A served web dashboard (next-phase SKU, build-tagged)

**Goal.** The "better web UI beyond TUI": a real, interactive **localhost dashboard** — doc 07's
three hero views (arbitrage/cache chart, explain receipt slide-over, the faceted "spend prism") —
**built on Item 1's renderer**, shipped as a **separate, build-tagged SKU** that is never in the
default or offline binary.

**Extends / phase:** next phase · reuses `internal/htmlreport` view seam + doc 07 · new
`//go:build webui` server + `aispend serve`.

**In scope.**
- **A tagged SKU.** `aispend serve` exists **only** in the `webui` build, mirroring how the cloud
  sink lives behind `//go:build cloudyali`. The default + offline binaries never compile a listener;
  `doctor --network` on them stays PASS. In the `webui` build, `doctor --network` **discloses** the
  local listening socket.
- **Same numbers, richer interaction.** The server hands the same view structs (as JSON) to the
  front end; the static HTML report (Item 1) is the server-rendered fallback. Interactivity adds
  live drill + cross-filtering, not new numbers.

**Out of scope.** Any default-build server; auth/multi-user/hosting (that's the team collector, 1B,
and CloudYali, Phase 2). Bundling the front end into the default binary.

**Open decision (defer to build).** **React-via-`go:embed`** (richer, matches your stack, but adds a
Node build step + bundle) **vs. server-rendered + htmx** (no Node, lighter, closer to the zero-dep
ethos). Lean htmx/SSR for consistency; revisit when the phase starts. Either way the bundle is
embedded only in the `webui` SKU.

**Acceptance criteria (when picked up).**
- [ ] `serve` compiles only under `webui`; default + offline builds have no listener (CI-asserted).
- [ ] `doctor --network` discloses the socket in the `webui` build and still PASSes the default.
- [ ] Views reconcile with `report --json` / `--html`; drill-downs preserve evidence.

**Risks.** SKU drift (a network import leaking into default) → CI assertion is the guardrail.
Front-end build complexity → the SSR fallback keeps it usable without the SPA.

## Item 5 — Gateway / proxy spend: ingest, never intercept (Phase 2)

**Goal.** Close the blind spot that CLI-log scanning has by construction: **direct API usage** (a
script hitting the Anthropic key, OpenRouter traffic) that never writes a coding-agent session log.

**Extends / phase:** `phase-2-cloudyali-reconciliation.md` (+ phase-3 LiteLLM-rule emission).

**The principle (decided): ingest, don't intercept.** aispend does **not** become a proxy — online
infra in the request path is antithetical to the offline, zero-runtime, nothing-to-operate promise.
Instead, Phase 2 reconciliation **reads a gateway's own usage records** (LiteLLM proxy logs,
OpenRouter usage export, an API invoice/admin export) through the **same open `Sink`/`Provider`
interface** that local agents use, and reconciles them against the CLI-log ledger (dedup against
double-counting where an agent *and* the gateway both saw a call).

**In scope (Phase 2).** A gateway-log/export `Provider`; reconciliation + dedup against the local
ledger; surfacing "direct-API spend not attributable to a coding session" as its own line.

**Out of scope (forever, for the local tool).** aispend sitting in the request path; any always-on
proxy we operate.

**Acceptance criteria (Phase 2).**
- [ ] A LiteLLM/OpenRouter usage export ingests via the open interface and reconciles to the ledger
      without double-counting agent-attributable calls.
- [ ] The default local binary remains net-free; ingestion is reading exported records, not routing
      live traffic.

**Risk.** Double-counting agent calls a gateway also logged → the dedup is the core test.

---

## Cross-cutting: how each risky item stays on-ethos (the audit row)

| Item | The temptation | How this plan keeps the promise |
|---|---|---|
| 1 HTML report | CDN charts / JS | Inline SVG, no external refs; ships in offline build; CI greps for `http`/`net` |
| 2 Menu bar | A Swift app + widgets | A `glance` command + `contrib/` plugins; no app, no daemon we own |
| 3 Coverage | Match "8 tools" incl. Cursor | Clean-token first; fabricated tokens flagged, never in a "true" total |
| 4/7 Scan cadence | Always-installed agent hooks | Passive scan + watermark-gated launch; hooks opt-in, local-trigger-only |
| 6 Quota breadth | Online quota polling | Local samples only; held `quota refresh` stays held; "unknown" not `0%` |
| 8 SQLite | Drop in a fast driver | Pure-Go + benchmark-gated; CGO excluded; offline-size is a gate metric |
| 9 Served UI | A default web server | `webui` build tag only; default + offline stay net-free, CI-asserted |
| 5 Gateway | Become a proxy | Ingest exported records via the open interface; never in the request path |

## Open questions to settle as each item starts

- **Scan-on-launch UX** — silent vs. always-print-one-line; default cadence ceiling so a long-idle
  launch doesn't full-scan unexpectedly.
- **`glance` schema** — lock the JSON keys before the `contrib/` plugins depend on them.
- **HTML `--card` dimensions** — what screenshots cleanly at 2:1 for social vs. a full report.
- **Gemini cache** — ship "not computable," or invest in the storage-time model now? (Lean: ship
  honest-blank first.)
- **SQLite gate thresholds** — agree the exact latency/memory numbers that count as "real
  bottleneck" before running the spike, so the decision isn't post-hoc.
- **Served UI front end** — React-via-`go:embed` vs htmx/SSR; decide at phase start, not now.
- **Marketing tie-ins (current priority)** — the comparison table, the shareable `--card`, the
  menu-bar screenshot, and the Homebrew auto-bump tap are the launch assets these items unlock;
  sequence a short "build-in-public" thread per wave.
