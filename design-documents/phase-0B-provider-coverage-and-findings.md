# Phase 0B — Provider Coverage + Fact-Based Findings

_Last updated: 2026-06-14 · **Status: Planned.** Sharpens as 0A nears done._
_Companion to: PRD v1.3 §8.1 (deferred list), §8.4 (fixtures), UC-2/UC-4, §15._

---

## Goal

> **Let a developer compare their supported agents side by side with no silent
> estimates — and surface the first cost drivers as plain facts, never judgments.**

0A earned trust on one provider. 0B widens coverage to Codex and Cursor *without
spending that trust*: every estimate is flagged with its basis and confidence,
and the first findings are metadata-only facts ("you pay for Cursor *and*
Copilot *and* Claude"; "Opus was 70% of spend this week") — not claims about
whether the user did something wrong.

**Success signal (PRD §15):** users compare supported tools with no silent
estimates, and trust the findings.

**This goal guides the build:** a finding that requires *inferring intent* (a
"looping session," "wasted" spend) is out — it can be wrong, and a wrong judgment
burns the trust 0A bought. Facts first; intent never, in 0B.

## Where it sits

- **Assumes:** the 0A `Provider`/`Normalizer`/`PricingEngine`/`Store` seams and the golden-fixture pattern.
- **Unlocks:** cross-tool comparison (the seed of the two-surface story) and the data findings reason over later.

## In scope

- **Codex** and **Cursor** providers (each = one `Provider` implementation + fixtures + golden).
- Parser fixtures per provider *and version*; pricing-table versioning surfaced to the user.
- **The LLM pricing module grows up** ([05-llm-pricing.md](05-llm-pricing.md)): the richer rate schema (cache-write TTLs, batch, context tiers, web-search), multi-provider tables, and the **ship-embedded + daily-refresh client** (`internal/pricing/refresh` → our S3 endpoint, offline-first, opt-out). Requires the server-side pricing endpoint to exist.
- Unsupported-source detection made visible (counts + which source/format).
- **Fact-based cost-driver findings** in neutral language (metadata only).
- A clean **TUI** reading the same `Store` (table-stakes parity with the trackers).

## Out of scope

Two-surface correlation (Phase 2) · CloudYali · budgets/enforcement · any finding
that infers intent or calls spend "waste" (marketing word, not a product claim) ·
OTel (1A).

## Design spec

Each new provider is a single file behind the existing interface — the pattern
0A proves. The standing approach (review-log §10) holds: **reference the
ccusage / CodeBurn parsers rather than reverse-engineering.** The notes below are
verified against their source. Provider-specific honesty problems drive the design:

- **Cursor doesn't just mask the model — it fabricates the tokens. [verified]**
  CodeBurn's cursor provider hardcodes `CURSOR_COST_MODEL = 'claude-sonnet-4-5'`
  and `CHARS_PER_TOKEN = 4` with a comment that Cursor v3 stores **zero token
  counts**, so it estimates `tokens = ceil(text_length / 4)` and prices the assumed
  model with zero cache. So the local store gives us neither real tokens nor the
  model. The event must be `inferred` with a reason like
  `"cursor: tokens estimated from text length; model assumed"` and a **low
  confidence (~0.5)** — *both* axes are estimates, not just the model. Real numbers
  live in Cursor's dashboard/admin API (a 1A/2 reconciliation source), never the
  SQLite store.
  - **Storage map [verified].** Cursor has no single session log. Read **both**
    `globalStorage/state.vscdb` (table `cursorDiskKV`, keys `composerData:<id>` and
    `bubbleId:<composerId>:<bubbleId>`) **and** each `workspaceStorage/<hash>/state.vscdb`
    (table `ItemTable`); a sibling `workspace.json` maps the hash to the real project
    path. Three serialization formats have existed, so the parser needs fallbacks.
    Open every DB **read-only and immutable** (`mode=ro`, `immutable=1`) so we never
    fight the running app or corrupt its WAL.
- **Codex token accounting [verified].** Prefer the per-turn delta
  `info.last_token_usage` when present; only fall back to **diffing** consecutive
  cumulative `total_token_usage` values when it's absent — **never sum** the
  cumulative totals (that inflates wildly). Accept the field-name drift across
  Codex generations: `input_tokens | prompt_tokens | input`,
  `output_tokens | completion_tokens | output`,
  `cached_input_tokens | cache_read_input_tokens | cached_tokens`,
  `reasoning_output_tokens | reasoning_tokens`. Sessions with no model metadata are
  **not skipped** — fall back to a `gpt-5`-family model (ccusage ships a date-based
  `codex-auto-review-fallbacks.json`) so the tokens still count, flagged
  `model_assumed`. There is also a second **headless** shape (`codex exec --json`)
  with usage under `result`/`data`/`response` — detect and parse it separately.
  - **Paths [verified].** Honor `CODEX_HOME` (comma-separated) and scan **both**
    `sessions/` and `archived_sessions/` under each home, letting the active
    `sessions/` copy win when the same session appears in both (dedupe by relative
    path). See [04-platform-and-paths.md](04-platform-and-paths.md).
- **Billing-model shifts.** Codex moved to token-credits (April 2026); Copilot to
  usage-based (June 2026). Pricing tables gain `credit_consumption` population for
  these; pollers stay behind the `Provider` adapter so a billing-model change is a
  table/adapter edit, not a rewrite.

0B is also where the **pricing module grows from seed to real** — multi-provider
tables, cache-write TTL tiers, batch multipliers, and context tiers all land
here. The full design and the target rate schema are in
[05-llm-pricing.md](05-llm-pricing.md); pricing breadth and provider breadth are
the same cycle.

The **findings engine** consumes `AgentEvent`s from the `Store` and emits typed,
fact-only findings with the evidence that supports each (so a finding is itself
`explain`-able). It produces no recommendations that require intent in 0B.

## Demonstratable output

```console
$ aispend week
AI-coding spend · last 7 days · 3 providers
  claude_code   $27.08   (api-equivalent, conf 0.95)
  codex         $ 9.40   (credit_consumption → $, conf 0.90)
  cursor        $ 6.12   (inferred — tokens estimated from text, conf 0.50)  ⚠ Cursor stores no real tokens locally

$ aispend findings
• You are paying for Cursor, Copilot, and Claude Code on overlapping work.   [fact]
• Opus was 70% of this week's Claude Code spend.                              [fact]
  (run `aispend explain <id>` on any line behind these)
```

## Acceptance criteria

- [ ] A developer compares ≥3 supported tools in one view with **no silent estimates** — every estimate carries basis + confidence.
- [ ] Golden fixtures exist for each provider at each supported version.
- [ ] Findings are **facts**, each backed by `explain`-able evidence; none infer intent.
- [ ] TUI totals match CLI totals exactly (same `Store`, same numbers).

## Test & quality plan

Golden fixtures per provider/version; findings unit-tested against fixed inputs
(a finding must cite the events that justify it); TUI snapshot tests; the
`estimated`/low-confidence paths get explicit coverage. Code + security review as
in [03-engineering-process.md](03-engineering-process.md).

## Risks

Cursor stores **no real token counts locally** (CodeBurn fabricates them from
character length) and its API is young, so any Cursor cash number is an estimate
of an estimate — flag it hard and point users at the dashboard for truth.
Copilot/Codex billing models shifted in 2026 — keep pollers behind adapters and
flag every estimate (PRD §17.4). The temptation to ship "waste" findings early is
the trap; hold the line at facts.
