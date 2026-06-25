# 09 — Session view: the work-session receipt

Status: **Now-phase shipped (2026-06-17, t-wada TDD)** · owner: Nishant · companion to `07-ui-concept.md`, `08-cli-tui-concept.md`

> **Shipped:** `report --by session` (reconciles to the by-model total), and the
> session receipt via `explain session:<id|max|last>` — window+span, total,
> per-token-class composition, the `without cache ≈ $X · saved Y%` arbitrage line
> (new `pricing.WithoutCache` primitive), and the top costly turns as drillable
> event ids. The hourly **spike bar** landed early, inside `aispend today`.
> **Deferred:** the denormalized `session_id` SQLite column (the active store is
> the in-memory/JSON `FileStore`, which already holds `SessionID` — the column is
> a SQLite-path optimization, not a correctness need, so it waits until SQLite is
> the live store); session ids OSC-8-linked in `report`/`top`; the cross-session
> day grid. Session "duration" renders as **wall-clock span** (first→last turn),
> labelled as span — active-time is still open (below).

## The unit we're missing

Today's facets — model, provider, repo, cost_tag — all answer *what kind* of spend.
None answer the question a person actually asks at the end of the day: *what did
**this sitting** cost me?* The session is the natural unit of an AI-coding work
session — you sat down, worked, stopped. It's the meal; `explain <event>` is the
line item for one dish. So the session view is just **`explain` one level up**: a
receipt for the whole sitting. That keeps it dead-on the wedge (explainability),
not off in generic activity-tracking.

## Two ideas, pulled apart

The original pitch bundled a **session view** with a **GitHub-style check-in
heatmap**. We split them, and we keep only the first. The decision and the why,
for the record:

- **Keep — the session view.** On-wedge, action-driving (it surfaces the runaway
  turn), and cheap (the data's already in the ledger; see below).
- **Cut — the streak heatmap, as framed.** GitHub's contribution graph has one
  job: make you feel good about consistency so you don't break the streak. It
  gamifies doing *more*. Our promise is spending *less*. A grid that glows green
  when you spend more is a mood ring rewarding the exact behavior we're helping
  people rein in. It also fights the data shape (AI-coding spend is bursty — a few
  hot days in a year of empty cells) and the brand (a colored cell is the least
  explainable primitive there is — you can't click it to its evidence). ccusage
  already prints the pretty blocks; copying them is parity, not moat.

  > **Update (2026-06-18).** This cut still stands for the *calendar streak grid*. A
  > different primitive — a **per-file cost+churn heatmap on the receipt** — does ship
  > (see "Linking sessions to code" below). It dodges every objection above: it's
  > scoped to one sitting (not a year of cells), each row is a real file that drills
  > to its turns/evidence (not an opaque cell), and the bar encodes *cost* (what we
  > want reined in), with git line-churn beside it — not a streak that rewards more.

The temporal view survives only if we **reframe it from streak to spike** (below).

## The session receipt (the hero)

One session, rendered as a receipt: **window** (first turn → last turn, duration),
**total**, **composition** by token class (the same color language as `report` —
blue cache-read, amber cache-write-1h, teal output, purple input), the per-session
**arbitrage line** (`without cache ≈ $X · saved Y%`), and the **top N costly
turns**, each an id that drills to its own `explain`. On web it's the spend
prism's receipt at session scope; in the CLI it's the ANSI waterfall from 08, one
level up. Reaching it never requires typing a hash — `08` already named the
selector: `explain session:3f9c`.

## CLI surface (additive)

- `report --by session` — one new grouping dimension alongside `model` (default) /
  `repo` / `provider` / `cost_tag`. One line in `groupKey`.
- `explain session:<id>` — the session receipt; prefix-matches the sessionId, like
  the other human selectors in 08. `explain session:max` = priciest session in the
  window.
- Session ids surfaced beside notable rows in `report` / `top`, OSC-8 hyperlinked
  where the terminal supports it (click → explain), per 08's craft rules.

## Linking sessions to code (2026-06-18, t-wada TDD)

The receipt answers "what did this sitting cost"; linking it to code answers "what did
it cost *to ship this*." Three signals, all **additive** to the schema (no
`SchemaVersion` bump), all **best-effort** — absent rather than guessed:

- **Branch** — `event.GitBranch`, read straight off the Claude Code line (it logs a
  branch per turn). Durable.
- **Commit SHA** — `event.GitSHA`. The log carries no SHA, so it's reconstructed at
  scan time from the repo's reflog (`.git/logs/HEAD`): the commit that was HEAD at the
  turn's timestamp. `internal/vcs.HeadAt` is **pure-Go, no git binary, no network**, so
  the `offline` build and `doctor --network` are untouched. Empty when the repo is
  gone, the turn predates the reflog, or the reflog rotated (90-day default).
- **Churn** — `event.SessionChurn` (`[]FileChurn`), per-file `+added/−removed` from
  `git diff --numstat` between the session's **first and last commit**, captured once
  per session via `vcs.Numstat`. This is the **one** git-binary dependency, isolated
  behind a hook and still a local read. Honesty note: it counts only churn *committed
  during the session*; a sitting whose work wasn't committed mid-session shows no churn
  (the heatmap degrades to cost-only) rather than over-attributing uncommitted edits.

Because the ledger hashes paths (`CWDHash`, `SourcePathHash`), the real repo location
can't be recovered after the fact — so SHA and churn are resolved at **scan** time
(`normalize.EnrichVCS`, after attribution, before pricing) and frozen into the event,
never computed lazily at `explain`.

**Surfaces.** New report facets: `--by branch` and `--by commit` (1:1 groupings that
reconcile to the by-model total — cost per feature / per PR), and `--by file` (fan-out:
a turn's cost splits equally across the files it touched, so file rows still sum to the
grand total; fileless turns bucket as `(no files)`). The receipt gains a `branch · SHA`
line and the per-file **cost+churn heatmap**: a cost-shaded intensity bar + `+adds/−dels`,
top files first, each a real path. Plain-ASCII / `NO_COLOR` / non-TTY degradation holds
(no escape into a pipe), per the craft rules.

**Receipt navigation — one cursor, tab as an accelerator.** The heatmap and the
top-turns table are a single ↑/↓ list: the cursor (`recCursor`) walks every file, then
flows straight into the turns, and ↵ opens whatever's highlighted — a file (→ file
view) or a turn (→ its evidence). The earlier model made the two a pair of
`tab`-switched *focus panes*, which read worse — the turns below the heatmap looked
inert because ↑/↓ drove only the focused files and reaching the turns took a
non-obvious `tab`. So ↑/↓ now flows across both, and `tab` is kept purely as an
**accelerator**: a one-key jump between the top of the files and the first turn, so a
long heatmap isn't in the way (no-op when only one section exists). The heatmap also
keeps **at least the five priciest files** visible (or all, when fewer) even on a
short terminal — the signal never collapses to a row or two — clamping the window to
the last file once the cursor crosses into the turns so the rows just above them stay
in view.

## The temporal view, reframed: spike, not streak

The question worth answering is *where did the money go, and did any of it run
away?* — not *did I keep my streak?*

- **Today, by hour** — an hour intensity bar over the `today` window, so you catch
  the 2am session that looped and burned $40 in cache-writes. A spike-finder.
- **Cross-session** — a day grid is fine **as navigation** (click a hot day → its
  sessions → explain), never as a vanity counter. Every cell drills to a receipt.
  That drill is the line between a diagnostic and a vanity metric: if a cell
  doesn't lead somewhere actionable, it doesn't ship.

## What it costs to build (almost nothing new)

The data is already captured and persisted — this is exposure, not ingestion:

- **Timestamps** — `event.TSStart` / `TSEnd` (per-second ISO from the raw logs),
  stored as `ts_start_unixnano` with an index (`idx_events_ts`). Time-range and
  hour bucketing are query-time work; no schema change for the today view.
- **Session id** — `event.SessionID` is captured per event, today riding inside
  the lossless `event_json` blob. The one small change worth making: **denormalize
  a `session_id` column** so grouping doesn't deserialize every event. Additive,
  backward-compatible, **no re-scan**.

Built t-wada TDD, 85–90% coverage floor, through the code-review + security-review
gates (see `03-engineering-process.md`).

## Acceptance criteria

- [ ] `report --by session` groups events by sessionId; totals reconcile to the
      same window's `--by model` total (no double-count, no drop).
- [ ] `explain session:<id>` prints a receipt: window, duration, total,
      per-token-class composition, arbitrage line, top-N turns with drillable ids.
- [ ] `explain session:max` resolves to the priciest session in the active period.
- [ ] `report --period today --view <hourly>` (name TBD) shows per-hour intensity;
      the hot hour is identifiable and its turns reachable.
- [ ] Money stays integer micros end-to-end; a session with zero billable turns
      reads as `nil`/"not computable," never `$0` asserted.
- [ ] Plain-ASCII / `NO_COLOR` / non-TTY degradation holds (no color into a pipe).
- [ ] New code ≥ 85% coverage; review + security gates pass.

## Phased build

- **Now** ✓ — `report --by session` + `explain session:<id>` (the receipt). Reuses
  the 08 ANSI waterfall and selector grammar. (Denormalized `session_id` column
  deferred — see Status: the live store is the JSON `FileStore`, no column needed.)
- **Next** — `today` hourly spike bar ✓ (shipped early); `explain session:max`/`:last` ✓;
  session ids surfaced + OSC-8 linked in `report`/`top` (pending).
- **Later** — cross-session day grid as a navigation surface (drill-only), and the
  same session facet on web inside the spend prism.

> **Update (2026-06-25) — multi-day sessions split by UTC day in the list.** The
> day-grouped session list (the TUI default; see `08-cli-tui-concept.md`) resolves the
> *spanning-days* half of the open question below: a resumed session is **split into one
> row per UTC calendar day** (`groupSessions` keys on `(sessionID, UTC-day-of-TSStart)`),
> so each day-group subtotal is the honest spend on that calendar day and stays identical
> across period windows that contain the day — week vs month no longer disagree on
> "Yesterday." Grouping pins to **UTC** (the calculation, matching the period window); the
> per-row clock stays **local** (display). The **receipt** is unchanged: it still renders
> one sitting's **wall-clock span** (first → last turn), so the span-vs-active-time
> question below still stands.

Open: **session boundary & duration** — a resumed Claude Code session can span days
with long idle gaps; does "duration" mean wall-clock span or active time? (Honesty
matters here — a misleading duration is the kind of number this tool exists to
not print.) Also: does the cross-session grid earn a build now, or wait behind the
07/08 hero views? And confirm the primary user — the session receipt sings for the
individual founder; the spike-finder sings for whoever's chasing a runaway bill.
