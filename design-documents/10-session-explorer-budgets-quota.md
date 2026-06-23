# 10 — Session explorer, budgets, and the quota window

Status: **Largely implemented (as of 2026-06-20)** — sections A–D shipped under t-wada
TDD (session spine, prompt-chain view, API-equivalent budgets, the Codex+Claude quota
window). Residue tracked in *Acceptance criteria* / *Phased build* below: subagent
nesting, scoped budgets, cross-session `←/→` travel, budget alerts, and the deliberately
held online `quota refresh` (D.1). · owner: Nishant · companion to `07-ui-concept.md`,
`08-cli-tui-concept.md`, `09-session-view.md`

> The session receipt (09) answered *what did this sitting cost?* This doc takes the
> next three steps the founder asked for: make the **session** the navigational spine
> (including the one that's still running), let a person **travel the prompt chain**
> turn-by-turn watching the meter, and add **budgets** (API-equivalent only, off by
> default) plus a **plan quota window** — the weekly wall Claude Code / Codex put up.
> All additive, all on-wedge (explainability + subscription-arbitrage), all degrading
> to "unknown" rather than guessing.

## The premise that turned out to be wrong (in our favor)

The working assumption was: "the weekly window that disables work — that info isn't
local." It mostly isn't. Except it increasingly **is**, and that reframes the whole
quota feature from impossible to cheap.

- **Codex** writes a `rate_limits` block on its `token_count` events in every rollout
  log under `~/.codex/sessions/…`. Two children: `primary` (the 5-hour window) and
  `secondary` (the weekly window, `window_minutes` ≈ 10080), each carrying
  `used_percent`, `window_minutes`, and `resets_in_seconds`. **We already read those
  lines** — the normalizer just drops the field today (the fixture shows
  `"rate_limits":null` and `{"limit_id":"codex"}`). Honest caveat: Codex **exec** mode
  emits `rate_limits: null`; only interactive runs populate it. So: render the gauge
  when a non-null sample exists, else "unknown — no quota sample in logs."
- **Claude Code** is catching up. Recent versions expose session + weekly usage metrics
  (percent used, tokens used/limit, `resets_in_seconds`, reset timestamp), and there's a
  local cache at `~/.claude/usage-exact.json` the statusline ecosystem already reads.
  It's a **snapshot** Claude Code refreshes via its own API — not a per-turn priced
  fact — so we read the freshest local sample and stamp it with *as-of*.

The principle that keeps this honest and on-brand: **the ledger is computed from
evidence; the quota window is a reported snapshot.** Don't blend them. They answer
different questions — *am I overspending?* (ledger/budget, in $) vs *am I about to get
cut off?* (quota, in % + reset). Two gauges, never one number.

## Where the quota sample lives (a new type, not a new event field)

`AgentEvent` is "the normalized, priced, provenance-carrying unit of **spend**." A
rate-limit reading is none of those — it's a point-in-time meter, not a turn that cost
money. Putting `used_percent` on a priced event would be a category error (which event
"owns" 78%? none of them). So the sample gets its own small type, captured during scan
alongside events and reduced to the freshest-per-(provider, window):

```go
// quota.Sample is a point-in-time plan-limit reading lifted verbatim from a
// provider's local logs/cache. It is NOT priced and NOT part of the ledger; it is
// a reported snapshot, rendered as its own gauge and always shown with its as-of.
type Sample struct {
    Provider     string    // "openai" | "anthropic"
    Window       string    // "5h" | "weekly" | "weekly_opus"
    UsedPercent  float64   // 0..100, as reported
    WindowMinutes int      // 300, 10080, …
    ResetsAt     time.Time // observed_at + resets_in_seconds
    ObservedAt   time.Time // when the line/cache was written
    Source       string    // "codex:rate_limits.secondary" | "claude:usage-exact.json"
}
```

Keep it on the scan result / store as `latest map[providerWindow]Sample` (keep-max
`ObservedAt`). Offline-safe: it's read from files we already open, no `net/*`. A `nil`
/ absent sample renders "unknown," never `0%`.

## A. Sessions as the spine (live + historical)

> **Shipped (2026-06-19, t-wada TDD).** The TUI session list is now day-grouped and
> period-scoped end-to-end: rows group under **last-active-day** headers
> (Today/Yesterday/`Mon Jun 15`), newest day first; within a day the **live** session
> leads with a `●`/`live` badge, then priciest-first; each row shows its span. A new
> `Model.WithNow` clock drives the relative labels + liveness (zero ⇒ absolute dates,
> no live badge — deterministic for tests), and the cli passes the scan's `now`. Day
> headers are height-budgeted so a multi-day window never overflows the viewport.
> Liveness is recency-only for now (`liveWindow`); ANDing in file mtime is the next
> refinement. A **live legend** (`● live — active in the last 10m`) renders only when
> something is live, and **`aispend tui --watch`** turns on a periodic re-scan +
> rebuild that refreshes in place and advances the clock, so liveness decays live
> (Bubble Tea `tea.Tick` → reload, fully unit-tested via `tickMsg`).
>
> **Session naming + subagent rollup shipped (2026-06-19).** The receipt now leads with
> the session's human title — Claude Code's `summary` line, else the first prompt —
> recovered lazily on drill-in by a name resolver mirroring the prompt resolver (content
> read live, never stored). Claude Code `subagents/agent-*.jsonl` turns roll up under
> their parent session: the parent id is resolved from the path at scan time
> (`subagentParent`, since the ledger hashes paths) into the new additive
> `event.SubagentID`, so subagent spend folds into the parent's total (still reconciles
> to the by-model total) and the row + receipt show a `⋮N sub` marker. The in-row
> active-time remains the next slice.
>
> **Time zones (2026-06-19).** Per the product rule, **all visual times render in the
> user's local zone while everything in the backend/ledger stays UTC**. The display
> layer threads a `*time.Location` (default `time.Local`, injected as a fixed zone in
> tests for determinism); day bucketing (Today/Yesterday) uses the *local* day so the
> headers match the wall clock, while event timestamps, period windows, dedupe, and
> pricing remain UTC. **Shipped (2026-06-19):** the discrete timestamps localize —
> list-row clocks + spans, day-group headers/grouping, the receipt/file/explain
> windows — via `fmtTimeIn`/`clockTime(loc)`/`dayKey/dayLabel(loc)`; `fmtTime`
> defaults to `time.Local`. **Correction (2026-06-23): the period-span label is the
> exception — it renders in UTC, not local.** Periods are UTC calendar windows whose
> inclusive end is `23:59:59 UTC`; localizing that boundary crossed midnight east of
> UTC (an IST user saw the Jun 15–21 week as `Jun 15–Jun 22`), so `periodDates` /
> `periodSpanLabel` format the span + zone tag in UTC. **Bucketed axes done (2026-06-19):** the spend-over-time duration bar
> now truncates, indexes, and labels in the display zone (`bucketSpend`/`truncateTo`/
> `bucketIndex` take a `*time.Location`), and `today`'s hourly spike already bucketed in
> `now.Location()`. So every visual time — discrete *and* bucketed — is local (the
> period-span *window* label excepted — UTC, per the 2026-06-23 correction above); the
> backend (instants, windows, dedupe, pricing) stays UTC.

Promote "session" from a `--by` facet (shipped in 09) to the **default backbone** of
the explorer. The model already carries everything: `TSStart/TSEnd` (span), `ActiveMS`
(real active time), `Tools` / `MCPServers`, `PromptID`, `GitBranch` / `GitSHA`,
`SessionChurn`. Group the list by **calendar day** (matches our calendar-only-period
rule), pin **live** sessions on top, and let a cross-midnight session show a "continued"
marker rather than splitting in two.

```
aispend                                            week · Jun 15–19  ◂ ▸
──────────────────────────────────────────────────────────────────────
api-equiv $312.40   ·  plan $46/day · 6.8× ROI  ·  cache saved $1.1K (83%)

budget $500/mo  ███████████░░░░░░░░  $214 (43%) · 63% of month → on track
Claude weekly   ████████████████░░░  78% · resets Thu 09:00 (2d 4h)  ⚠ wall ≈ Wed
Codex  weekly   ███░░░░░░░░░░░░░░░░░  6%  · resets in 3d   (5h window 0%)
──────────────────────────────────────────────────────────────────────
 SESSIONS                                       ↑↓ move · ↵ open · / filter
 Today
 ● live  rajgad · feat/heatmap   2h12m span · 38m active   $84.10  opus
         payments · main         46m · 12m active          $11.20  sonnet
 Yesterday
         rajgad · feat/heatmap   resumed · 3h08m           $63.40  opus
 Mon Jun 15
         rajgad · spike/cache    crossed midnight →        $54.90  opus  ⋮2 sub
──────────────────────────────────────────────────────────────────────
 tab pivot day/repo/model · b set budget · q quota detail
```

**"All info about a session"** is a header block on the receipt that answers *what was
this sitting?* before you drill: span + active time (`ActiveMS` summed), repo · branch,
model mix (the composition stripe), turn count, files touched, `Tools` / `MCPServers`
used, and the arbitrage line. It's `explain session` with a fuller identity line — not a
new surface.

**Live / ongoing sessions** are the new capability. A session is *live* when its last
`TSEnd` is within N minutes **and** its source file's mtime is fresh. The receipt goes
live: running total, **burn rate** (`$/hr` over the last K turns), and — using the 5-hour
`primary` sample — "throttle in ~52m at this pace." This is finally the concrete job for
the deferred `watch` mode: poll mtime, re-scan the tail, refresh. Honesty: the streaming
tail turn can be mid-flight, so mark it `partial` and exclude it from the confident total
(this is the known streaming-dedup event-count gap; don't let live mode pretend it's
gone).

**Subagents.** Claude Code stores subagent transcripts nested under the parent session
(`…/<session-id>/subagents/agent-*.jsonl`). Ingested flat, they fragment the picture.
Nest them as indented child rows that **roll up** into the parent's total (linked by the
parent session id / directory), so a session's number is whole and the children are still
drillable. This also seeds the chain view's branch points (below).

## B. Prompt-chaining: the GPS replay of the conversation

The 09 receipt is the **itemized bill** — top-N costly turns, ranked. What the founder
asked for is the **GPS replay of the drive**: walk the conversation in order and watch
the meter climb. `PromptID` already groups assistant/tool turns under the user prompt
that triggered them — that *is* the chain, for free. Add a **chain view** as a sibling
drill from the receipt (toggle `c`).

> **Shipped (2026-06-19).** `c` on the receipt opens `modeChain`: `chain.Build(sel.evs)`
> rendered as the turns in time order — columns **WHEN · MODEL · COST** (per-turn) **·
> CUM** (the running gutter). `↑/↓` walks the turns, `↵` opens the turn's evidence (esc
> back), `~` flags a not-priced turn, and long chains page via `fileWindow`.
> (Per-prompt `p<N>` group markers were tried and removed as noise; cross-session `←/→`
> travel stays deferred.)

```
CHAIN  rajgad · feat/heatmap › session bd34e22a   ↑↓ turn · tab prompt · ↵ evidence
                                                  ← prev session · → next (same branch)
 cum$     Δt     turn
 $0.0  ┌▸ 10:02  PROMPT  "add a churn column to the heatmap"
 $4.2  │  +3s    ├ asst  plan + read tui.go      ██   $4.2  18k→3k · opus
 $9.6  │  +22s   ├ tool  grep/read ×6            ███  $5.4  ↑cache-read
 $14.0 └◂        └ asst  edit tui.go             ██   $4.4  +120/−20
 $14.0 ┌▸ 10:31  PROMPT  "tests are red, fix"
 $20.4 │  +5s    ├ asst  read test + edit        ███  $6.4  ⟲ loop starts here
 $84.1 └  ...    └ 30 more turns                      $63.7 faint = partial (live)
──────────────────────────────────────────────────────────────────────
 ↵ on a turn → evidence (tokens · files · branch·SHA · confidence)
```

It reuses every interaction we already taught: **one `↑/↓` cursor** flows the spine
(same model as the receipt's files→turns cursor), **`tab`** jumps prompt-to-prompt
(skipping the assistant/tool sub-turns — the same accelerator idea), **`↵`** opens the
existing per-turn evidence view. The genuinely new pieces:

- A **cumulative-cost gutter** (`cum$`) so you *see* where the curve bends — that elbow
  is almost always the runaway loop or the moment a giant context got pinned. The bill
  tells you the total; the gutter tells you *when* it happened.
- **Cross-session travel** with `←/→`: step to the previous/next session on the **same
  repo·branch**, because a feature's real arc spans resumes and subagent branches, not
  one sitting. Breadcrumb: `repo › branch › session › prompt`.
- **Honesty rendering**: turns with `Evidence.KnownMissingFields` show cost faint with a
  `~` and a one-line confidence note — same ethos as the rest of the ledger.

## C. Budgets (API-equivalent only, off by default)

> **Shipped (2026-06-19, t-wada TDD).** `internal/budget` computes a monthly **pace**
> (`ComputePace`): period-to-date api-equivalent spend projected to month end at the
> current run rate, with a verdict — "on track", "under", or "N.N× over pace" — plus
> `UsedFraction`/`ElapsedFraction` for the bar. A `budget_usd` key in config.toml turns
> it on (off by default; `config.LoadBudget`), and `aispend today` renders the line —
> `budget $500/mo  [####----]  $214 used (43%) · 63% of month · on track` — against the
> local calendar month, excluding + disclosing providers with no api-equivalent.
> Informational only, never enforced (a budget is your $ ceiling, not the provider's
> hard wall). It also renders in the **TUI list header** (`WithBudget`, above the quota
> gauges; re-paced on each watch tick). Scoped budgets (per repo/provider/cost_tag) are
> the next slice.

This maps onto what already exists. Budgets measure against `CostViews.APIEquivalent`
(the view is already on every event), scope by the **same group keys the faceter uses**
(`global` / `provider` / `repo` / `cost_tag` — budgets ride the existing machinery), and
align to **calendar** periods (month default; no rolling window). Config is a new optional
block beside `plans.json`; default is *no budget* (the gauge simply doesn't render).

The bar is the easy half — reuse the `cache saved Y%` percentage primitive and the
palette (teal → amber ≥80% → red ≥100%, ASCII `[#####-----] 43%` off a TTY). The useful
half is **pace, not level** — a fuel gauge with range-to-empty: *"43% spent, 63% through
the month → on track"* beats a naked 43%; *"43% spent, 20% through the month → trending
2.1× over"* is the line that changes behavior.

aispend observes, it can't enforce — so budgets are **informational** (and the natural
hook for the later alert / scheduled-task story). Be explicit in copy that a budget is
*your* ceiling in dollars, **not** the provider's hard limit (that's the quota window).
Any provider with no computable API-equivalent (`nil`) is excluded from budget math and
disclosed in the footer — the same pattern as `today`'s "not in the ROI" note.

## D. The quota window (dollarize the wall)

> **Shipped (2026-06-19, t-wada TDD).** Claude's weekly wall is live end-to-end.
> `quota.ParseClaudeRateLimits` reads the documented `rate_limits` shape (`five_hour` /
> `seven_day` / `seven_day_opus`), handling its quirks — `utilization` vs
> `used_percentage`, epoch-vs-string `resets_at`, and the known Claude Code
> epoch-as-percentage bug (out-of-range → skipped, never a bogus 100%). A best-effort
> cli reader (`claudeQuotaSamples`) loads `~/.claude/usage-exact.json` via
> `Resolver.ClaudeUsagePath` (honoring `CLAUDE_CONFIG_DIR`), stamped with the file's
> mtime as its as-of, and the gauge renders on the `today` glance and the TUI list
> header (`Model.WithQuota`), refreshing on each watch tick. When the snapshot is
> absent or stale-past-reset it degrades to an explicit `Claude weekly — unknown (no
> local usage snapshot)` line (shown when there's Claude activity in the window) rather
> than vanishing silently — a "not computable" the tool explains, never a guess. It
> stays a reported snapshot —
> never folded into the ledger.
>
> **Codex shipped (2026-06-19).** `codexQuotaSamples` reads the freshest `rate_limits`
> (5h + weekly) straight from the rollout logs — newest file first (reusing
> `quota.ParseCodex` + the Tracker, capped at the 8 most-recent sessions, stopping at
> the first populated window) — and renders on the same `today` + TUI gauge, degrading
> to nothing on exec-mode `rate_limits:null`. Both providers now light the one gauge:
> Claude from its usage snapshot, Codex from the logs we already scan.
>
> Validated against a real `~/.codex` session, which corrected the parser: Codex writes
> an **absolute `resets_at` epoch** (not the docs' `resets_in_seconds`) and can carry
> the **weekly** window in the `primary` slot with `secondary: null` — so `ParseCodex`
> classifies the window by `window_minutes` (≈10080 → weekly) and reads `resets_at`,
> rather than trusting the slot name or a relative reset. Real local data beat the docs.

This is the new value the reframe unlocks, and it's the closing chapter of the
arbitrage wedge. aispend already says *"you'd have paid $Y on API — 6.8× ROI on your
subscription."* With the quota gauge it can also say:

> *"You've burned 78% of your weekly Claude window with 2 days left. At this pace you'll
> hit the wall Wednesday afternoon — keeping going costs ≈ $X in API credits, or upgrade
> from Max 5x to 20x for $100/mo."*

That turns an abstract weekly limit into a **dated, dollarized forecast**. The pace line
is a projection from observed `used_percent` vs elapsed window time, labelled *projected*.
The 5-hour `primary` sample powers the live-session "throttle in ~52m" nudge in section A.

Render rules (provability first):
- It's a **separate gauge**, never folded into a ledger total.
- Always show the **source and as-of** (`from provider · as of 11:42, 2m ago`).
- If the freshest sample is older than its own reset, **drop it** (show "unknown") rather
  than show a stale window.
- **Plan identity** (Max 5x vs 20x, Plus vs Pro) is configured by the user (we already
  have `plans.json`); the **numeric ceilings** are dynamic/banded and unpublished, so we
  never hardcode them — we lean only on the self-describing `used_percent` /
  `resets_in_seconds` the logs report.

## D.1 — Opt-in online `quota refresh` (scoping, 2026-06-19)

**Why.** On setups where Claude Code writes no local usage snapshot — no `usage-exact.json`,
no `rate_limits` in the session logs, and `stats-cache.json` carrying only daily activity
counts (`messageCount`/`sessionCount`/`toolCallCount`) — the weekly wall is simply not on
disk (confirmed on a real machine, 2026-06-19). The authoritative subscription window then
exists only online.

**Feasibility — one source, and it's undocumented.** The only endpoint that returns the
Pro/Max subscription windows is the **undocumented** `GET https://api.anthropic.com/api/oauth/usage`
— the same call behind Claude Code's `/usage` (utilization %, reset time, weekly + 5h + Opus
windows). The *official* Rate Limits API (platform.claude.com) is the **Admin API for API-key
orgs** and does **not** expose subscription quotas, so it cannot serve this.

**Auth.** An OAuth access token (`sk-ant-oat01-…`), resolved as Claude Code does:
`CLAUDE_CODE_OAUTH_TOKEN` env → macOS Keychain (`security find-generic-password -s
'Claude Code-credentials' -w`) → Linux `~/.claude/.credentials.json` (mode 0600). The token
expires; we would **not** implement OAuth refresh — expired → tell the user to re-auth in
Claude Code.

**Architecture (mirror the pricing-refresh seam).**
- A new `quota` network unit behind `//go:build !offline`; the `offline` build compiles out
  all net + credential code, so the air-gapped binary stays provably net-free *and*
  credential-free.
- A strictly opt-in `aispend quota refresh` command — **never** invoked by `scan`/`today`/`tui`,
  which read only the local cache.
- Cache the parsed windows to `~/.aispend/quota/anthropic.json` (our own cache, mirroring
  `~/.aispend/pricing/litellm.json`); the existing gauge reads it offline via `quota.Sample`
  — no new rendering.
- `doctor --network` discloses it.

**How it differs from `pricing refresh` — the crux.** Pricing refresh is a GET of a **public,
identity-free** file. Quota refresh sends the user's **identity and a secret** (OAuth token) to
an **undocumented** endpoint. That is a categorically stronger network action, so the
disclosure and guards must be stronger — this is not "just another disclosed GET."

**Risks + required guards.**
- *Offline-trust boundary* — stays behind `!offline`, opt-in, never automatic; `doctor
  --network` must disclose it as an *authenticated, identifying* outbound, distinct from the
  identity-free price fetch. Default surfaces read cache only.
- *Aggressive 429s* — `/api/oauth/usage` rate-limits hard (anthropics/claude-code #31637);
  reportedly safe only at ≥180s intervals and with the right User-Agent. So: manual refresh
  only (never poll), a hard min-interval guard, backoff, and degrade to cache/"unknown" on 429.
  Spoofing Claude Code's User-Agent to dodge 429s is itself dubious — flag it, don't hide it.
- *Credential hygiene* — the token is a secret: read just-in-time, never log, never write to
  the cache/ledger/export, prefer the env var, accept the keychain prompt. The cache holds
  only windows (percent + reset), never the token.
- *ToS / stability* — an undocumented endpoint + OAuth token + possible UA-spoofing may run
  against Anthropic's terms, and the endpoint can change without notice. (Not legal advice —
  a judgment call for the founder.) Treat as unofficial/best-effort, clearly labeled.

**Recommendation.** Feasible and on-pattern, but it crosses from "provably offline,
identity-free" into "authenticated call to an undocumented endpoint with your credential." Ship
it only as an explicitly **experimental, off-by-default, fully-disclosed** `quota refresh` (with
the min-interval + credential hygiene + an "unofficial endpoint" notice), **or** hold for an
official subscription usage API. The decision is a trust/ToS trade, not a technical blocker —
the founder's call. **Decided (2026-06-19): hold the online refresh; ship the
zero-risk honest state instead** — the explicit `Claude weekly — unknown (no local
usage snapshot)` line now renders on `today` and the TUI header whenever there's
Claude activity but no snapshot, so the gauge always explains its blank.

## Periods: how each surface relates to the window scrub

The explorer keeps the **implemented** period model unchanged: the CLI hands the TUI a
set of pre-windowed `Period`s (`Label`, `Events`, `Since`/`Until`, `Amortized` plan fee),
scrubbed with `◂ ▸`, and the selection **persists across drill** (already true for
receipt → file → explain via `TestModel_PeriodScrub` / `TestModel_PeriodPersistsAcrossDrill`).
The new surfaces slot into that — but three of them relate to the window differently, on
purpose:

- **Session list, receipt, and chain view — period-scoped, exactly as today.** They read
  the selected `Period.Events`, so they answer "in *this* window." A session straddling a
  boundary shows its **in-window** turns and in-window total (the events are already
  windowed), so it reconciles to the period total like `report --by session`; the chain's
  cumulative gutter runs over the in-window turns. Seeing a conversation *across* a
  boundary is the job of cross-session `←/→` travel (which deliberately steps outside the
  window) or an explicit, opt-in "full session" toggle — never the default, so the numbers
  stay reconcilable.
- **Budgets track their own calendar period, not the scrub.** A budget is a `$/month` (or
  `$/week`) commitment; scrub to "today" and the gauge still reads "$214 of $500 *this
  month*." This mirrors the existing `Amortized` field, which already prorates the plan fee
  to a window independent of the per-event views. The view period and the budget period are
  two different clocks, and conflating them would mislead.
- **Quota is a point-in-time snapshot, not a window aggregation.** The gauge shows the
  freshest `quota.Sample` ("now · as of T") regardless of the selected period; on a
  historical period it stays labelled as current (or is hidden), never recomputed as "last
  month's quota." `quota.Tracker` has no period concept by design — it reduces to the latest
  reading, full stop.

## Edge cases worth deciding now (the brainstorm residue)

- **Codex exec-mode null** → no weekly sample from headless runs; gauge reads "unknown."
  Interactive sessions backfill it. Acceptable; just never imply we have it when we don't.
- **Claude snapshot staleness** → `usage-exact.json` is API-refreshed; treat as snapshot,
  stamp as-of, expire past reset. Reading it is local; we do **not** trigger the refresh
  (that would breach the offline default).
- **Partial live turn** → the tail turn of a live session may be mid-stream; mark
  `partial`, keep it out of the confident total (ties to the ~3.8% streaming-dedup gap).
- **Cross-midnight grouping** → group a session under its **last-active day** (so a live
  or just-touched session surfaces under Today, not stranded under an older start day),
  show the span, and mark one whose first turn landed earlier as "continued from <day>";
  never double-list it or merge two sittings' durations. (As-built chose last-active day
  over the earlier start-day idea on exactly this live-surfacing ground.)
- **Subagent rollup vs double-count** → children roll into the parent total exactly once;
  the parent row shows `⋮N sub`, the children are indented and drillable. Reconcile to the
  by-model total like every other facet.
- **Two weekly windows on Claude** (overall + Opus-specific) → model `Window` as an enum
  (`weekly`, `weekly_opus`); show whichever is closer to its wall.

## Demonstratable output

- `aispend` (TUI default) opens the **day-grouped session list** with a live session
  pinned, a budget bar (if configured), and the Codex/Claude quota gauges (if a sample
  exists) — each degrading to "unknown"/absent honestly.
- `↵` on a session → receipt with the identity header; `c` → the **chain view**; `↵` on a
  turn → existing evidence; `←/→` travels sessions on the same branch.
- `aispend today` gains the budget pace line and the quota gauge in its static glance.
- `aispend budget set --period month --api-equiv 500` (name TBD) writes the optional
  budget; with none set, nothing budget-related renders.

## Acceptance criteria

- [x] Codex `rate_limits.{primary,secondary}` retained into a `quota.Sample`;
      absent/`null`/exec-mode → no sample (never `0%`) (2026-06-19). Validated against a
      real `~/.codex`: window classified by `window_minutes`, absolute `resets_at` epoch.
- [x] Quota gauge renders `used_percent` + reset countdown with source + as-of; expires a
      sample older than its reset; degrades to "unknown" off-sample and to ASCII off-TTY
      (2026-06-19).
- [x] Session list groups by calendar day; live sessions pinned with span (2026-06-19).
      As-built deviations from the wording above: grouped under **last-active** day (not
      start day) so a live session surfaces under Today; liveness is **recency-only** for
      now — AND-ing in file mtime is still pending.
- [x] Subagent transcripts roll up under the parent session exactly once (2026-06-19)
      — reconciles to the by-model total; indented *drillable* children is a later refinement.
- [x] Chain view orders turns by time, groups by `PromptID`, shows a cumulative-cost
      gutter, reuses the `↑/↓` + `tab` + `↵` model (2026-06-19); cross-session `←/→`
      travel is deferred.
- [x] Budgets measure `CostViews.APIEquivalent` only, default off, calendar-aligned;
      show pace not just level; never claim enforcement; exclude + disclose `nil`-cost
      providers (2026-06-19). **Global scope only** — per-repo/provider/cost_tag scoping
      still pending.
- [x] Money stays integer micros; `nil` reads as "not computable," never `$0`.
- [~] New code clears the ≥ 85% coverage floor (quota 93%, chain 100%, budget 93%,
      tui 90%) and the offline build still compiles out `net/*` with `doctor --network`
      unchanged (verified 2026-06-20). **Still to run for these features: the code-review
      + Security-Guidance gates.**

## Phased build

- **Now — done (2026-06-19).** `quota.Sample` + Codex `rate_limits` retention + the weekly
  gauge on `today` and the TUI header.
- **Next — done (2026-06-19).** The day-grouped live session list + identity header + the
  chain view (reuses 09's cursor grammar); budgets (config + pace gauge).
- **Later — partially done.** ✅ Claude `usage-exact.json` snapshot reader (2026-06-19);
  ✅ the live `watch` loop (2026-06-19). ⏳ Remaining: subagent nesting (drillable
  children), scoped budgets, cross-session `←/→` travel, and budget alerts via scheduled
  tasks.

## Open questions

- **Live cadence** — how fresh is "live," and how often to re-scan without burning the
  battery? (mtime-gated re-scan of just the tail file is probably enough.)
- **Chain gist** — what's the one-line summary per turn: first N chars of the user prompt,
  or the assistant's first tool/action? (Lean to the prompt for prompts, the action for
  asst/tool rows.)
- **Budget scope precedence** — if global *and* per-repo budgets are set, which gauge
  leads on the home screen? (Probably the tightest-pace one, like the quota rule.)
- **Codex weekly reset drift** — upstream has reported inconsistent weekly-reset windows;
  treat `resets_in_seconds` as authoritative per-sample and don't try to reconcile across
  samples.
