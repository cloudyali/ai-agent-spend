# Phase 0A — The Trusted, Explainable Local Ledger

_Last updated: 2026-06-15 · **Status: In progress** (core contract under TDD; Claude Code keep-max dedup + reported-cost landed, verified against ccusage/CodeBurn)._
_Companion to: PRD v1.3 §8.1, §9 (UC-1/UC-3), §11; `agentspend-phase0a-build-spec.md`._

---

## Goal

> **Make Claude Code spend on one developer's machine a number they trust and can
> open up — and prove the tool never phones home.**

Not a dashboard. Not breadth. One thing done exceptionally well: a narrow,
accurate, **explainable** local ledger for Claude Code, where every number can be
opened to its evidence and the default binary is provably offline. For a
trust-sensitive product, a number that's wrong — or that can't be explained —
kills credibility, so 0A goes *deep on fidelity, not wide on surfaces*.

**The hero is `aispend explain`, not a report.** ccusage and CodeBurn give you a
number; we give you a number you can defend to finance. That is the wedge the
dashboards don't have, and it is what 0A exists to prove.

**Success signals (from PRD §15):** 10–20 early users say the numbers are
understandable, trustworthy, and "replaced my scripts." Time-to-first-insight
< 60s. Zero trust incidents.

**This goal guides the build:** if a proposed task doesn't make the Claude Code
number *more trustworthy* or *more explainable*, or doesn't *prove the offline
promise*, it belongs to a later phase. That single test is what keeps 0A from
sprawling.

## Where it sits

- **Assumes:** nothing. This is the foundation.
- **Unlocks:** 0B (more providers slot into the same `Provider` interface and
  golden-fixture pattern), and everything above it (the `AgentEvent` schema,
  hashed identity, and `CostTag` are the seams the fleet/FinOps tiers snap onto).

---

## In scope

- `aispend scan` — detect Claude Code, read new sessions, normalize, price, store. Locally, no network.
- `aispend report --period <window>` — spend by model/repo/cost_tag over a **calendar** window (`today`, `week`, `last month`, `quarter`, `"90 days"`, `since …`, a `YYYY-MM-DD..YYYY-MM-DD` range, `all`), with a confidence indicator and a labeled cost view. Always calendar time, never a rolling window.
- `aispend explain <event-id>` — **the hero**: open any number to its full evidence.
- `aispend privacy report` and `aispend doctor --network` — make the trust promise auditable.
- `aispend export --format json|csv --local-only` — local dump with full evidence.
- `.aispend.toml` repo-level attribution (nearest-ancestor resolution).
- Embedded, **versioned** pricing tables; multi-view cost engine; evidence ledger.
- Parser fixtures + golden tests for Claude Code as the regression net.

## Out of scope (resisting these is the point)

Other providers (Codex/Cursor → 0B) · cost-driver/optimization findings (0B) ·
TUI/menubar/widget (0B) · two-surface correlation (CloudYali, PRD §12) ·
CloudYali sink/sync (Phase 2) · budgets/alerts · OTel ingestion (1A) · team
aggregation (1B) · the `Activity` classifier (stays empty in 0A).

---

## Design spec

### Module layout

```
cmd/aispend/main.go              # cobra root + command wiring
internal/event/                  # AgentEvent + Money + schema version (the contract)
internal/platform/               # OS-aware path discovery (macOS/Linux/Windows) + app home
internal/provider/               # Provider interface + RawRecord/Source
internal/provider/claudecode/    # the only provider in 0A
internal/normalize/              # RawRecord → AgentEvent
internal/pricing/                # PricingEngine + Plan
internal/pricing/tables/         # versioned pricing tables (go:embed)
internal/store/                  # Store + LocalSink (SQLite)
internal/config/                 # .aispend.toml + ~/.aispend/config.toml loader
internal/privacy/                # privacy report + doctor
internal/cli/                    # command implementations
internal/sink/sink_cloud.go      # //go:build cloudyali — absent from the default build
testdata/providers/claude_code/v1/   # sanitized .jsonl fixtures
testdata/golden/                     # expected AgentEvent JSON
```

### Interfaces (the contract everything plugs into)

```go
// provider — one implementation per agent.
type Source struct {
    PathHash string // hashed; the only form that is ever stored/exported
    RawPath  string // in-memory only, for reading; never persisted
    Kind     string // "session_jsonl"
}
type RawRecord struct {
    Provider string
    Source   Source
    Line     int
    Raw      []byte // the raw JSONL line
}
type Provider interface {
    Name() string                              // "claude_code"
    Detect() (present bool, err error)         // is this agent on the machine?
    Sources() ([]Source, error)                // enables unsupported-source *reporting*
    Read(since time.Time) ([]RawRecord, error) // only records newer than last scan
}

// normalize — pricing is applied separately, so a re-price never forces a re-read.
type Normalizer interface {
    Normalize(RawRecord) (event.AgentEvent, error)
}

// pricing — fills CostViews + the pricing half of Evidence.
type Plan struct {
    Provider   string
    Kind       string       // "api" | "subscription"
    MonthlyFee *event.Money // for subscription_amortized
    Included   *Quota       // included tokens/credits, for marginal cost
}
type PricingEngine interface {
    Price(e *event.AgentEvent, plan Plan) error
    TableVersion() string
}

// store — idempotent on EventID.
type Filter struct {
    Since, Until time.Time
    Provider, Repo string
    GroupBy string // "model" | "repo" | "provider" | "cost_tag"
}
type Store interface {
    Upsert(events []event.AgentEvent) error
    Query(Filter) ([]event.AgentEvent, error)
    Get(eventID string) (event.AgentEvent, error)
    LastScan(provider string) (time.Time, error)
    SetLastScan(provider string, t time.Time) error
}

// sink — the single egress seam. Default build: only the SQLite LocalSink.
type Sink interface {
    Write(events []event.AgentEvent) error
}
```

### Claude Code provider

- **Detect:** ask `internal/platform` for OS-aware roots —
  `ExistingRoots("claude_code")` returns `~/.claude/projects` on macOS/Linux,
  `%USERPROFILE%\.claude\projects` on Windows, honoring `CLAUDE_CONFIG_DIR`. Present if any root exists. (See [04-platform-and-paths.md](04-platform-and-paths.md).)
- **Read:** glob `<root>/**/*.jsonl` (via `path/filepath`, OS-correct separators);
  each line is one record. Track per-file offset/mtime in `scan_state` so
  `Read(since)` returns only new lines (idempotent re-scan).
- **Map** raw JSONL → `AgentEvent`:
  - `EventID` = stable hash of the **dedupe key** `(message.id | requestId)` — the
    semantic identity of one API response, *not* the source line (see the
    deduplication note below). When a line carries no `message.id`, fall back to a
    per-line key `(source_path_hash | line | session_id)` so unrelated turns never
    merge. `Evidence.DedupeKey` records the key itself.
  - `Model`, `Tokens{Input,Output,CacheRead,CacheWrite}` from the message usage fields.
  - `CostViews.Reported` from a `costUSD` on the line, when present and `> 0` (see
    "reported cost" under Pricing). Absent/zero → left `nil`, never a fake `$0`.
  - `Repo`/`Project` from the project path; `CWDHash` = hash of cwd; `CostTag`
    from the nearest-ancestor `.aispend.toml`.
  - `Tools`, `MCPServers` from tool-use entries, names normalized to a standard set so 0B providers align.
  - `Evidence.SourceType="local_file"`, `ParserName="claude_code"`, `ParserVersion="v1"`.
- **Unsupported records:** a line that can't be parsed is *counted and surfaced*
  in `scan` output ("3 records skipped: unrecognized format"), never silently
  dropped (PRD G1).

#### Deduplication — the streaming-placeholder undercount (verified against ccusage)

This is the single most important fidelity fix in 0A, and the rest of the trust
thesis rests on it. Claude Code writes a response to disk *as it streams*, so one
API request becomes **2–10 JSONL lines that share one `(message.id, requestId)`**,
and roughly **75% of them carry `input_tokens` of 0 or 1** (placeholders written
before the real usage settles). The cache fields are written correctly from the
first line, so the same object is simultaneously trustworthy (cache) and wrong
(base in/out). A parser that emits one event per line and sums them overcounts
base input by ~100×. For "a number you can defend to finance," that is fatal.

The fix, lifted directly from ccusage's Rust adapter (`should_replace_deduped_entry`):
collapse all lines sharing a dedupe key and **keep the single entry with the
largest token total** (`input + output + cache_read + cache_write`). The 0/1
placeholders always lose to the full response. Because the same `message.id` can
also reappear across files during branch/resume, the key is global to a scan, not
per file. Concretely:

- The `EventID` is derived from the dedupe key, so streaming lines of one response
  collapse to one stable id — and the idempotent `Upsert` and the keep-max dedup
  agree on identity.
- A per-adapter `Deduper` seam runs in the scan pipeline **before pricing** (so we
  never price or store a turn twice). Claude Code implements it; providers without
  one pass through unchanged. Keeping the rule per-adapter is deliberate — every
  agent double-counts differently (PRD/§1.5), so there is no global rule.
- `scan` reports the collapse (`… · N duplicates collapsed`) — transparency, not a
  silent rewrite.

> **Scoped honestly:** ccusage also handles *sidechain replay* (a subagent
> re-emitting a parent's usage under a new request id), where the non-sidechain
> parent must win the tiebreak. 0A keeps the token keep-max (which fixes the live
> placeholder bug) and defers sidechain/subagent dedup to 0B, where subagent
> attribution lands. Recorded so the gap is a known, flagged decision — not a
> silent omission.

### Pricing (multi-view, with provenance and confidence)

> This is the **seed** of the LLM pricing module — the full subsystem (rich rate
> schema with cache-write TTLs, batch, context tiers; multi-provider coverage;
> build-time freshness with no runtime egress) is designed in
> [05-llm-pricing.md](05-llm-pricing.md) and grows in 0B. 0A ships the minimum
> slice that prices Claude Code honestly.

Embedded versioned tables (`internal/pricing/tables/anthropic-2026-06.json`, …).
`PricedAt` and `PricingTableVersion` recorded on every event. Per view, for
Claude Code in 0A:

- **API-equivalent** — always computable: tokens × table rate, cache-read at the
  reduced rate. `CostMethod="token_priced"`, confidence ~0.95.
- **Reported** — a cost the tool itself wrote to disk (Claude Code's `costUSD`,
  when present and `> 0`). When set, it is **authoritative**: the engine stamps
  `CostMethod="reported"` (confidence ~0.98) while still computing API-equivalent
  alongside it for comparison. This is ccusage's "Auto" rule (reported-else-computed)
  expressed as a first-class, provenance-tagged view rather than a hidden override —
  so the ledger shows both "what the tool said" and "what we'd compute," and `explain`
  makes the basis explicit. Newer Claude Code often omits `costUSD`, so this view is
  opportunistic; API-equivalent remains the always-on number and the cross-provider
  default. (OpenCode/Pi also write a `cost`; the same view will carry them in later phases.)
- **Estimated** — equals API-equivalent when no plan info; flagged as an estimate.
- **Effective-allocated** — only with `Plan.Kind=="subscription"` + `MonthlyFee`:
  amortize the monthly fee across the period's observed usage.
  `CostMethod="subscription_amortized"`, lower confidence, reason "allocation, not a metered price."
- **Marginal** — only with `Plan.Included`: zero within quota, table rate beyond.
- **Billed** — `nil` in 0A (no invoice source). Non-nil only with reconciliation (Phase 2).
- **Credit-consumption** — `nil` for Claude Code; structure exists for 0B.

**`nil` means "not computable here," never "zero."** `explain` shows which views
are `nil` and why (`KnownMissingFields`). Plan config lives in `.aispend.toml` or
`~/.aispend/config.toml` (e.g. `plan = "max"` → subscription with a known fee).

### Commands (the CLI contract)

| Command | Behavior |
|---|---|
| `aispend scan` | Detect → `Read(lastScan)` → `Normalize` → `Price` → `LocalSink.Write` → `SetLastScan`. Prints events imported, period, skipped-record count. No network. |
| `aispend report --period P` | `Query` + aggregate over a single **calendar** window chosen entirely by `--period` (default `week`). In-progress windows end at now — `today` = midnight→now, `week` = Monday→now, `month` = 1st→now, `quarter`, `this year`, `since YYYY-MM-DD`, `"N days"` (last N calendar days incl. today, day-aligned), `all`. Completed windows span their full inclusive range — `yesterday`, `last week`, `last month`, and an explicit `YYYY-MM-DD..YYYY-MM-DD`. **No rolling window**: every span snaps to a calendar boundary, so the same `--period` on the same day is reproducible and reconcilable against an invoice. Flags `--by model\|repo\|provider\|cost_tag`, `--view api_equivalent\|reported\|effective_allocated\|...`, `--json` (machine-readable output for metered views — exact `cost_micros` + convenience `cost_usd`, the same aggregation as the table; for token-priced views each group and the total also carry a `cost_components` split into input/output/cache-read/cache-write; `effective_allocated` not yet covered). Each total shows a confidence indicator. When nothing renders, the message distinguishes three states rather than collapsing them: an empty store ("run `aispend scan`"), data outside the window ("N stored; widen with `--period all` or `--period \"90 days\"`"), and — the subtle one — a window that *does* hold events but none carry a cost in the requested `--view` ("none of the N event(s) in P have a `reported` cost — try --view api_equivalent"). It never tells a user with stored events to re-scan, nor tells a user with a full window to widen it. |
| grouping by repo | `aispend report --by repo` resolves `cost_tag` from `.aispend.toml`. (Replaces the former `by-repo` verb.) |
| `aispend explain <id>` | **The hero.** `Get(id)` then render full evidence (format below). |
| `aispend privacy report` | Paths read (hashed), fields stored, hashing behavior, sync-eligibility (none), telemetry (off). |
| `aispend doctor --network` | Static + runtime assertion that no network path is active; non-zero exit if any net-capable sink is present. |
| `aispend export` | `--format json\|csv`, `--local-only` (default and only mode). Full evidence included. |

### Trust requirements (testable, non-negotiable)

- Default build has **no cloud code**: `internal/sink/sink_cloud.go` is
  `//go:build cloudyali`; `go build ./cmd/aispend` excludes it.
- **CI no-egress test** fails if any `net` transport is reachable from the default
  build's import graph — "no phone-home" as a compile-time property. (0A's
  `cmd/aispend` is **embedded-only → provably net-free**, verified by `go list -deps`.
  The refresh build-tag seam `internal/pricing/refresh` is already built + verified —
  the default build isolates `net/*` to that one package, and `go build -tags offline`
  is net-free — and is wired into the daily path in 0B. The **locked default posture**
  is refresh-on / opt-out; the property *narrows* in 0B, never loosens — see
  [05-llm-pricing.md](05-llm-pricing.md) §4 and [01-architecture.md](01-architecture.md).)
- **`--no-network`** flag hard-disables any net path (belt-and-suspenders).
- **No raw paths** persisted or exported — only `*_path_hash`.
- Signed releases + SBOM in the release pipeline.

---

## Demonstratable output

**Real captured session (2026-06-14)** — `aispend` built from this repo, run
against a `~/.claude` fixture (a user turn, an Opus turn with tools, a Sonnet
turn, a summary, and a deliberately corrupt line):

```console
$ aispend scan
Claude Code detected · 1 source(s)
Imported 2 events · 2026-06-14 → 2026-06-14 · 1 skipped (unrecognized format)
Stored in ~/.aispend/events.json · no network calls made

$ aispend report --period week --by model
AI-coding spend · this week · by model · view: api-equivalent (token_priced, confidence 0.95)
  claude-opus-4           $0.43  ▓▓▓▓▓▓▓▓▓▓  96%
  claude-sonnet-4         $0.02  ··········   4%
  total                   $0.45  (2 events)

$ aispend explain evt_6d5c9430b55bd36b
  $0.02  Claude Code · claude-sonnet-4 · 2,000 in / 500 out / 0 cache-read
  source   2781438a8d17…#L3  (path hashed in storage)
  parser   claude_code v1
  priced   anthropic-2026-06 table, priced_at 2026-06-14
  method   token_priced     confidence 0.95
  cost     input $0.01 · output $0.01 · cache-read $0.00 · cache-write $0.00
  views    api-equivalent $0.02 · estimated $0.02 · billed n/a · effective-allocated n/a
  missing  none

$ aispend doctor --network
default build: no network-capable sink in import graph  ✓
RESULT: PASS — this binary cannot phone home
```

The corrupt line is **reported, not dropped** (`1 skipped`); every number is
`explain`-able to its hashed source, parser version, pricing table, method, and
confidence; and the default binary is provably offline. (`report --period today`,
`report --by repo|provider|cost_tag --view ...`, and `doctor --paths` work too.)

**Attribution + plan-aware cost** — with a `.aispend.toml` (`cost_tag =
"team-payments"`) in the repo and `~/.aispend/config.toml` (`plan = "max"`,
`monthly_fee_usd = 200`):

```console
$ aispend report --period "7 days" --by cost_tag
AI-coding spend · last 7 days · by cost_tag · view: api-equivalent (token_priced, confidence 0.95)
  team-payments           $0.45  ▓▓▓▓▓▓▓▓▓▓ 100%
  total                   $0.45  (2 events)

$ aispend report --period "7 days" --view effective_allocated
AI-coding spend · last 7 days · by model · view: effective-allocated (subscription_amortized, confidence 0.70)
  claude-opus-4          $45.25  ▓▓▓▓▓▓▓▓▓▓  97%
  claude-sonnet-4         $1.41  ··········   3%
  total                  $46.67  (allocation, not a metered price)
```

The `effective_allocated` view amortizes the $200/mo subscription across the
window ($200 × 7/30 ≈ $46.67) and splits it by usage — the showback lens, flagged
as an allocation with lower confidence, never presented as a metered price.

**Dedup + reported cost (verified 2026-06-15)** — a session whose response was
written as three streaming lines (`input_tokens` 1, 0, then the real 12,400), plus
a turn carrying a `costUSD`:

```console
$ aispend scan
claude_code · 2 source(s) · imported 2 · 2026-06-14 → 2026-06-14 · 2 duplicates collapsed
Imported 2 events total · stored in ~/.aispend/events.json · no network calls made

$ aispend explain evt_e1a5c2f3165cf77b
  $0.43  Claude Code · claude-opus-4 · 12,400 in / 3,100 out / 8,900 cache-read
  source   664a9e3cc68c…#L1  (path hashed in storage)
  parser   claude_code v1
  priced   pricing-2026-06 table, priced_at 2026-06-15
  method   reported     confidence 0.98
  views    api-equivalent $0.43 · reported $0.43 · estimated $0.43 · billed n/a · effective-allocated n/a
  missing  none
```

The three streaming placeholders collapse to one event at the true 12,400 input
tokens (`2 duplicates collapsed`), not summed; and where the tool wrote its own
`costUSD`, `explain` shows `method reported` with the reported view *beside* the
computed api-equivalent — both numbers, each labeled, never blended.

---

## Acceptance criteria

- [x] `aispend scan && aispend report` shows Claude Code spend by model/repo with a confidence indicator and a labeled cost view.
- [x] `aispend explain <id>` renders full provenance for any event (source, parser version, pricing table, method, confidence, missing fields).
- [x] Re-running `scan` is idempotent (no duplicate events). *(tested)*
- [x] Streaming placeholders for one response collapse to a single event at the true (max) token total — base input is not inflated by the 0/1-token partials. *(tested: normalize keep-max + scan + golden fixture)*
- [x] A tool-written `costUSD` is surfaced as the `reported` view with `cost_method=reported`, beside the computed api-equivalent. *(tested: normalize + pricing + golden)*
- [x] `aispend doctor --network` passes; the default binary provably makes no network calls (`go list -deps ./cmd/aispend` is net-free). *(Wiring that assertion into CI is the next cycle.)*
- [x] No raw filesystem paths in storage — only hashed (`source_path_hash`, `cwd_hash`). *(The `export` command itself is pending.)*
- [x] Golden fixtures pass for the bundled Claude Code samples.
- [ ] 10–20 early users report the numbers are understandable, trustworthy, "replaced my scripts." *(external validation — pending launch.)*

---

## Test & quality plan

Built under [03-engineering-process.md](03-engineering-process.md): t-wada TDD,
≥85% coverage on the core, code + security review before "done."

**Test TODO list (worked top-to-bottom, one red at a time):**

- `platform` — `AppHome` honors `AISPEND_HOME` else `<home>/.aispend`; `ProviderRoots("claude_code")` is OS-correct for darwin/linux/windows (injected `GOOS`); `CLAUDE_CONFIG_DIR` override ranks first; `ExistingRoots` filters to real dirs; `HashPath` is stable across separator/case variants and collision-free.
- `event.Money` — zero value renders `$0.000000`; `Add` sums micros; mixing currencies errors; JSON round-trips exactly (integer, no float drift).
- `event.AgentEvent` — JSON round-trips; `SchemaVersion` stamped; `nil` cost views omitted from JSON (`omitempty`); required fields present.
- `pricing.Engine` — api-equivalent = tokens × rate with cache-read reduced; estimated mirrors api-equivalent and is flagged; evidence fields (`PricingTableVersion`, `PricedAt`, `CostMethod`, `ConfidenceScore`) filled; subscription plan produces `effective_allocated` with lower confidence; `nil` views where not computable.
- `store` — `Upsert` is idempotent on `EventID` (re-scan adds no duplicates); `Query` filters by period/repo and groups; `Get` round-trips; `LastScan`/`SetLastScan` persist. In-memory implementation first; SQLite `LocalSink` then satisfies the same suite.
- `normalize` (claude_code) — golden: fixtures in `testdata/providers/claude_code/v1/*.jsonl` normalize to `testdata/golden/*.json`; unparseable line is counted, not dropped; no raw path in output; the dedupe key is `(message.id|requestId)` and streaming lines share an `EventID`; `Dedupe` keeps the max-token entry per key; a `costUSD` becomes the `reported` view (zero/absent → nil).
- `scan` (dedup) — streaming placeholders for one response collapse to a single stored event at the true token total, with `Summary.Deduped` counting the collapse; providers without a `Deduper` pass through unchanged.
- `cli` — golden-output tests for `week` and `explain` rendering; `doctor --network` exits 0 on the default build.

**Fixtures as a public asset:** `testdata/providers/claude_code/v1/` holds
sanitized real-shape sessions; `testdata/golden/` holds the expected `AgentEvent`
output (regenerate with `-update`). This turns a Claude Code format change from a
silent-breakage risk into a failing test.

### Progress (2026-06-14, sessions 1–2)

Built bottom-up under t-wada TDD (test list → red → green → refactor, one behavior
at a time). All green, `go vet` clean, `-race` clean, and the no-egress property
already holds.

| Package | What it does | Tests | Coverage |
|---|---|---|---|
| `internal/platform` | OS-aware path discovery (macOS/Linux/Windows), app home, path hashing | green | **100%** |
| `internal/event` | `Money` (micro-units), `AgentEvent` + evidence contract, schema version | green | **100%** |
| `internal/pricing` | api-equivalent + estimated + **reported** (Auto: prefer tool-written cost), embedded versioned table, provenance/confidence | green | **96.2%** |
| `internal/store` | `Store`+`Sink` seams: **`FileStore` (default, zero-dep, persistent)** + `MemStore`; idempotent upsert, query, lossless round-trip, persist-across-reopen. `SQLiteStore` (sqlc + vendored modernc) **verified passing** under `-tags sqlite`. | green | **94.3%** |
| `internal/provider` + `…/claudecode` | `Provider` seam; Detect/Sources/Read over OS-aware roots | green | **87.5%** |
| `internal/normalize` | Claude Code JSONL → `AgentEvent` (provenance, tokens, tools/MCP, canonical model); **semantic dedupe key + keep-max `Dedupe`**; `costUSD` → reported view + **golden fixtures** (incl. streaming-placeholder + reported-cost) | green | **94.7%** |
| `internal/pricing/refresh` | the `//go:build offline` egress seam (default isolates `net/*`; offline net-free) — 0B refresh client, scaffolded + verified now | green | **90.0%** |
| `internal/scan` | the pipeline: provider→normalize→**dedupe (per-adapter, before pricing)**→price→store; summary (imported/skipped/not-billable/**deduped**); idempotent | green | **90.7%** |
| `internal/cli` | stdlib-`flag` commands + renderers, incl. **`--by cost_tag`**, the **`effective_allocated`** (subscription-amortized) view, the **`reported`** view + `explain` headline, and the `… duplicates collapsed` scan line; wired into `cmd/aispend` | green | **88.0%** |
| `internal/config` | nearest-ancestor `.aispend.toml` (project/cost_tag/env) + `~/.aispend/config.toml` (plan/monthly_fee); **seeded plan prices** (Claude/ChatGPT) + `aispend plans`; zero-dep parser | green | **92.6%** |

Pricing table is **`anthropic-2026-06`** with current models (Opus 4.8 $5/$25,
Sonnet 4.6 $3/$15, Haiku 4.5 $1/$5) plus retained legacy keys, so real sessions
price. Subscription fees are seeded (`plan = "claude-max-20x"` → $200/mo;
`monthly_fee_usd` overrides).

Total: **87.3%** for the default build — held down only by the generated
`internal/store/sqlcgen` (counted but exercised only under `-tags sqlite`).
**Excluding generated code, the hand-written packages hold ~91%** (each
87.5–100%); the `-tags sqlite` run exercises `sqlcgen` + `SQLiteStore` as well.
All above the 85% floor. The
no-egress check passes today: `go list -deps ./cmd/aispend` contains **no
`net/*` package** — the offline promise is already a property of the build.

The normalize→price pipeline is demonstrable now: the fixture
`testdata/providers/claude_code/v1/basic_session.jsonl` produces a byte-pinned
golden (`testdata/golden/basic_session.json`) — a real session line becomes a
fully-priced, provenance-carrying event (opus turn = 431,850 micros, `token_priced`,
confidence 0.95). A Claude Code format change is now a failing test. Regenerate
with `go test ./internal/normalize -update`.

Persistence is done: the default `FileStore` survives across process runs (so
`scan` then a separate `week` works), and the `-tags sqlite` backend is ready for
scale. (It closes review finding #1: both stores serialize to JSON on write, so
the aliasing concern is gone.)

> **SQLite backend — vendored and verified.** `modernc.org/sqlite` (pure Go) is
> committed under `vendor/` (the proxy zip is blocked in the sandbox, so the dep
> ships via `go mod vendor`). The `-tags sqlite` backend uses **sqlc-generated**
> type-safe queries (`internal/store/sqlcgen`) and **passes the full store
> contract** (`MemStore` + `FileStore` + `SQLiteStore`) plus persistence across
> reopen. A real bug surfaced only by *running* it — reopen DDL needed
> `IF NOT EXISTS` — now fixed.
>
> **Trust finding:** a `-tags sqlite` build pulls Go's `net` package *transitively*
> via `modernc.org/libc`'s socket/netdb shims (storage plumbing, not outbound
> calls). So the zero-dependency **`FileStore` is what carries the net-free
> guarantee** — `go list -deps ./cmd/aispend` (default) has no `net`. That makes
> FileStore-as-default a trust decision, not merely a dependency workaround.

The pipeline and the hero commands are **done and demonstrated** above
(`scan`/`report --period …`/`explain`/`doctor`, wired through
provider→normalize→price→store on a real `~/.claude`).

**Remaining to call 0A shippable:** the `privacy report` + `export`
(`--format json|csv`) commands · a CI workflow wiring the no-egress, no-raw-path,
and golden assertions (plus a `-tags sqlite` job) · signed releases + SBOM.
*(Done now: the `.aispend.toml` / plan loader, `cost_tag` grouping, and the
subscription-amortized `effective_allocated` view. `marginal` follows when an
`included` quota is configured.)*

---

## Risks & open questions

- **Over-narrowing into a me-too** (PRD §17.8). Mitigation: the hero is
  `explain`; depth-on-trust is the differentiator, not surface area.
- **Parser durability** (PRD §17.4). Claude Code's JSONL shape can change.
  Mitigation: the fixture + golden suite, kept as a community asset.
- **Plan-aware cost honesty.** Amortization is an *allocation*, not a metered
  price; it must always carry the lower confidence and the stated reason, or it
  becomes the kind of confident-but-wrong number 0A exists to avoid.
