# Mining AI Coding Agents' Local Session Data: Metadata, Cost, and Parser Design

*A critical research note for building cost/usage tracking on top of local agent telemetry.*

Last updated: June 2026. Treat every path and schema here as a moving target. These formats are undocumented, version-specific, and change without notice. The single most important lesson from the field is: **trust the bytes on disk, not the docs or the source code comments.** Someone reverse-engineering Codex modelled their parser on `protocol.rs` and got exactly one event per session because the wire format and the internal data structures don't match.

> **Verification status.** The claims here were cross-checked against the source of two production trackers: **ccusage** (now a Rust per-agent adapter implementation) and **codeburn** (TypeScript, 25 tools, prices via LiteLLM). Where the code confirmed, refined, or corrected an earlier claim, it's marked inline as **[verified]**, **[refined]**, or **[corrected]**. The headline corrections from that pass: Copilot's good token data comes from an opt-in OpenTelemetry export, not the session log; Pi is richer than first assumed; Claude Code's path discovery and `costUSD` handling needed nuance; and Codex now emits a per-turn token field so you don't always have to diff cumulative counters.

---

## 0. The uncomfortable truth (read this first)

There's a seductive idea here: every agent dumps a rich transcript on your disk, so you can build a free, offline, privacy-preserving cost tracker just by reading files. Half of that is true. The trap is assuming the local data is a *billing-grade ledger*. For several of these tools it is not. It's a usage signal with holes in it.

Here is the honest reliability verdict before any of the detail:

| Agent | Local data exists | Tokens local | Cost local | Cost reliability verdict |
|---|---|---|---|---|
| Claude Code / Cowork | Yes, rich JSONL | Yes, but **input/output undercounted** | No (compute it) | Cache tokens trustworthy; base input/output is **broken in the raw log** (often 0 or 1). Reconstruct carefully. |
| Codex CLI | Yes, rich JSONL | Yes, **per-turn or cumulative** | No (compute it) | Good. Newer Codex emits a per-turn delta directly; older formats give cumulative counters you must diff. Pick the right format generation. |
| Cursor | Yes, but SQLite + replay-oriented | Mostly **no** | No | Local store is for conversation replay, not accounting. Trackers estimate tokens from character counts and assume a model. Real numbers live server-side. Don't try to bill from it. |
| Gemini CLI | Yes, JSONL + optional OTEL | Yes (native) | No (compute it) | Cleanest token data of the bunch. Free-tier/Code-Assist quotas muddy *cash* cost. |
| GitHub Copilot CLI | Session log: **output only**; OTEL export: full | No in session log | No | Session `events.jsonl` carries only output tokens. Full input/output/cache/reasoning split exists **only via the opt-in OTEL file exporter**. And Copilot bills in **premium requests**, not tokens. |
| OpenCode | Yes, JSON files **and** SQLite | Yes | Field exists but **often 0** | Recompute from tokens. Read both the JSON files and the SQLite db and dedupe across them. |
| Aider | Yes, but **Markdown** | Only if analytics enabled | Only if analytics enabled | Primary artifacts are prose Markdown. Both ccusage and codeburn skip Aider entirely. Structured cost is opt-in and ephemeral. |
| Pi | Yes, JSONL (optional SQLite) | Yes, **full cache-aware split** | Yes (`cost.total`) | **[verified]** Richer than first assumed: input/output/cacheRead/cacheWrite/total plus a real cost field. Smaller ecosystem, but the schema is clean. |

If you remember one row, remember Claude Code's: the most popular agent writes a beautiful transcript whose headline token numbers are wrong by 10x to 700x in the raw form. That is the whole ballgame for a cost product.

---

## 1. The critical debate

Before the reference tables, the arguments worth having. This is where most cost-tracking products quietly go wrong.

### 1.1 Is local session data even a trustworthy cost source?

**The case for it.** It's free. It's granular down to the tool call. It works offline, with no API rate limits, no dashboard scraping, no waiting on a vendor's usage API to settle. It captures things the vendor dashboards don't surface well: per-project attribution, per-tool breakdowns, subagent fan-out, which files got touched. For showback and chargeback inside a team, that granularity is gold.

**The case against it.** Four of the eight tools here do not give you a clean cash number locally, and two actively mislead a naive parser:

- **Claude Code's streaming placeholders.** Because the CLI writes partial entries as a response streams, a single API request produces 2-10 JSONL lines sharing one `requestId`, and roughly 75% of entries carry `input_tokens` of 0 or 1. Sum them naively and your input tokens are off by 100x-174x, output by 10x-17x. The cache fields (`cache_read_input_tokens`, `cache_creation_input_tokens`) are written correctly from the initial response, so they reconcile at ~1x. So the log is simultaneously trustworthy (cache) and untrustworthy (base in/out) in the same object. A parser that doesn't know this ships confidently wrong dashboards.
- **Cursor** stores conversation for replay, not accounting. The bill is computed on Cursor's servers. The local SQLite is the wrong instrument for cost.
- **Copilot** logs only *total* tokens locally and bills in *premium requests* (quota units), so even perfect token parsing doesn't reproduce the invoice.
- **OpenCode** frequently writes `cost: 0` (subscription/OAuth providers), so the field is present but empty.

**Synthesis.** Local data is an excellent *usage and attribution* source and a *poor invoice-reconciliation* source for most of these tools. Build for the former, and treat any cash figure as an estimate with explicit provenance. The moment you blur "tokens we observed" with "dollars we owe," you've built a liability, not a feature.

### 1.2 Token-derived cost vs vendor-reported cost

Two camps. Camp A multiplies observed tokens by a pricing table (the ccusage / LiteLLM approach). Camp B trusts only a `costUSD` field when the tool writes one.

Camp A breaks on pricing drift: cache-write tiers (5-minute vs 1-hour cache have different prices), model aliases (`gemini-3-pro-high` really means `gemini-3-pro-preview`), batch/tiered discounts, and the big one for anyone doing real FinOps, **negotiated discounts** (committed-use, enterprise agreements). List-price times tokens is not what an enterprise on an EDP-style discount actually pays. That gap *is* the FinOps problem, not a rounding error.

Camp B breaks because `costUSD` is usually absent or zero in local files — though **[refined]** not always: Claude Code transcripts have historically carried a `costUSD` field (newer versions often omit it), and OpenCode/Pi write a `cost` field that's sometimes real and sometimes zero. So "trust the reported cost" is a viable *primary* path as long as you have a fallback.

The only defensible answer is a hybrid with **provenance tagging**: prefer a reported cost when present and non-zero, fall back to computed, and stamp every number with the method that produced it (`reported` | `computed_list` | `computed_discounted`). Silently mixing the two across a fleet produces numbers nobody can defend in a finance review.

**[verified]** This is exactly what ccusage's three cost modes encode in production: `Display` uses only the logged `costUSD`; `Calculate` always computes from tokens; and the default `Auto` uses `costUSD` if present, else computes. That default *is* the hybrid-with-fallback. codeburn does the same per provider (`if cost > 0 use it, else recompute`).

### 1.3 The subscription elephant

Most of these agents run on flat-rate plans: Claude Max, a Copilot seat, Cursor Pro. Under those plans the marginal cash cost of a token is zero. So "cost" computed from tokens is *shadow cost*, the imputed API-equivalent value, not money that left a bank account.

This forces a product decision you cannot dodge: are you reporting **what it would cost at API rates** (great for ROI, showback, "is this seat worth it") or **what was actually billed** (a fixed seat fee, zero marginal)? They're different products. For internal chargeback, the shadow number is arguably *more* useful than the seat fee, because it tells you where the value and the waste are. But you have to label it as imputed, loudly, or someone will add it to the seat fee and double-count.

**[verified] The plan itself is not in the local data.** A natural instinct is to read the subscription envelope — plan name, tier, seat count, billing-period start/end, renewal date — from the session files and bound the shadow cost against it. You can't. None of these agents write plan/tier/start/end into their transcripts; that's an account-level fact living server-side in the vendor's billing system. The only plan-adjacent signals on disk are weak: Claude logs a `service_tier` on `message.usage` (standard/priority/batch — a *model* tier, not your subscription) and a usage-limit reset timestamp that only appears *after* you hit a rate limit (a quota-window edge, not a billing cycle); Codex reads a `service_tier` from its own config for pricing speed. That's it. Tellingly, the one tracker that nets out subscription cost (codeburn) can't auto-detect any of this — it makes the **user declare** their plan in the tool's own config, and even then the declared object is thin: a plan id, a flat `monthlyUsd`, an optional monthly `resetDay`, and a `setAt` timestamp (when the user typed it in, *not* a subscription start date). No vendor-sourced dates, no tier, no seats. So if your product needs the subscription envelope, you have two honest options — ask the user, or pull it from the vendor's admin/billing API — and neither comes free from disk.

### 1.4 Schema drift and the "trust the wire" rule

Every format here is undocumented and unstable. Codex has at least three on-disk generations. OpenCode is mid-migration from JSON files to SQLite. Cursor has used three serialization formats. Claude Code's fields have shifted (cache creation now sometimes splits into 5m/1h sub-buckets). A parser built against one version silently rots.

Defensive consequences: version-detect before parsing, treat unknown record types as skippable noise rather than fatal errors, and never assume a fixed event ordering. Pin your parser's behavior to observed sample files per version, not to a vendor's protocol description.

### 1.5 Double-counting is the number-one bug class

Each tool double-counts differently, so there is no single dedup strategy:

- **Claude Code:** **[refined]** dedupe key is the hash of `(message.id, request_id)`, not `requestId` alone. On a collision, keep the entry with the **larger token total** (keep-max), not first or last — that rule is quietly what defends against the streaming-placeholder undercount, since the 0/1-token partials lose to the full entry for the same message. Also handle the same message UUID appearing across files during branch/resume, and sidechain logs that *replay* a parent message under a new request ID (ccusage keeps the non-sidechain parent's real count).
- **Codex:** **[refined]** `token_count` events are cumulative, *but* newer Codex also emits a per-turn `info.last_token_usage`. Prefer that delta when present; only fall back to subtracting consecutive `total_token_usage` values when it's missing. Summing the cumulative totals inflates wildly either way.
- **Subagents:** Claude Code writes each subagent to its own session file (or a `subagents/` subdir); a team run spreads tokens across many files. Codex marks subagent sessions with a `thread_spawn` record and has explicit replay-dedup so a subagent's replay of the parent's `token_count` isn't double-counted. Sum files blindly and you either double-count the parent context or miss the children.
- **Active vs archived:** Codex and OpenCode can hold the same session in `sessions/` and `archived_sessions/` (Codex) or JSON files and a SQLite db (OpenCode). Active/live copy wins; never count both. **[verified]** ccusage dedupes by relative path so the active copy beats the archived one.

A robust ingester needs a per-agent dedup key baked into each adapter, not one global rule.

### 1.6 The privacy surface nobody mentions

These files contain full prompts, pasted secrets, file contents, tool outputs, working directories, git branches, and sometimes environment details. A SaaS that ingests them is ingesting your customers' source code and IP. That is a compliance landmine and, flipped around, a positioning advantage: a **local-first or hash-on-ingest** design (compute usage locally, ship only aggregates) is both safer and a genuine differentiator. Given the SOC 2 / ISO 27001 posture most buyers in this space expect, "your code never leaves the machine" is a sentence worth being able to say.

---

## 2. Per-agent reference

Each entry: where it lives, the format, the records that matter, what's usable for cost, and the traps.

### 2.1 Claude Code (and Claude Cowork)

**Location.** `~/.claude/projects/<url-encoded-abs-project-path>/<session-uuid>.jsonl`. The project's absolute path is URL-encoded into the folder name (`/Users/you/code/app` becomes `-Users-you-code-app`). Windows: `%USERPROFILE%\.claude\projects\`. The filename is the session UUID.

> **[corrected] Path discovery is wider than `~/.claude`.** ccusage checks, in order: the `CLAUDE_CONFIG_DIR` env var (comma-separated, multiple roots, can point at the dir *or* its `projects/` child), then `$XDG_CONFIG_HOME/claude` (i.e. `~/.config/claude`), then `~/.claude`. Any root must contain a `projects/` dir to count. So a parser that only globs `~/.claude` will miss XDG installs and custom configs. Honor `CLAUDE_CONFIG_DIR` and the XDG path too.

> **[refined] Layout: flat is common, but nested and `subagents/` are real.** The modern common case is flat `projects/<proj>/<session>.jsonl`, but ccusage explicitly handles `projects/<proj>/<session>/chat.jsonl` (nested) and `projects/<proj>/<session>/subagents/<worker>.jsonl`. So "the `sessions/` subdir is a tutorial myth" was too strong — recurse the `projects/` tree for `*.jsonl` and derive the session id from the path shape rather than assuming one layout.

**Format.** Append-only JSONL, one JSON object per line, not pretty-printed. Lines are written as the session progresses, so the file is also a live tail source.

**Records.** Each line has top-level fields including `type` (`user` | `assistant` | `system` | `summary` | `file-history-snapshot`), `uuid`, `parentUuid` (the message chain), `sessionId`, `timestamp`, `cwd`, `version` (CLI version, useful for schema branching), `gitBranch`, and `requestId`. The `message` object holds `role`, `model`, `content` (array of `text` / `thinking` / `tool_use` blocks), `stop_reason`, and crucially `usage`.

`message.usage` fields: `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `service_tier`. Newer versions may split cache creation into 5-minute and 1-hour ephemeral buckets, which carry different prices.

Subagent / team-mode linkage rides on `parentToolUseId`, `agentId`, `agentType`, `teamName`. `file-history-snapshot` records correlate with file-write tool calls to reconstruct what changed.

**Cost.** **[refined]** Often computed, but `costUSD` *can* be on the line. ccusage reads a `cost_usd` field directly (its `Display`/`Auto` modes use it), and historically Claude Code wrote it; newer versions frequently omit it. So treat it as "prefer if present and non-zero, else compute," not "never there." (The Agent SDK separately exposes `total_cost_usd` and `modelUsage[].costUSD` at the result level if you drive Claude through the SDK rather than reading transcripts after the fact.)

**Cost computation detail [verified].** ccusage's pricing applies the splits this doc warned about: 5-minute vs 1-hour cache-write tiers (1h write = 2x base input rate), a 200k-token context tier (`input_above_200k`, etc.), and a "fast" speed tier whose multiplier suffixes the model name with `-fast`. If your math ignores the cache-write duration split or the 200k tier, it's wrong for long-context agent sessions.

**Traps.**
- Base `input_tokens` / `output_tokens` are unreliable in the raw log (streaming placeholders, ~75% are 0/1). **[refined]** Dedupe by `(message.id, request_id)` and on collision **keep the entry with the larger token total** — that's how the real values win over the 0/1 partials. Only lines containing a `"usage":{` object are parsed at all; entries with a non-semver `version` or empty ids are rejected, and lines with `null` in critical fields (model, costUSD, cache fields, ids) are skipped.
- `output_tokens` in the log **excludes extended-thinking tokens**, while the in-app status bar includes them. Two "truths," different denominators.
- Old session files are **auto-deleted** on a retention schedule. If you want history, archive on ingest (people are shipping SQLite-archiver tools precisely because sessions vanish).
- **Cowork [verified 2026-06-15, on a live Cowork session].** Cowork writes Claude-Code-format JSONL into the **same `~/.claude/projects/` store** — same fields (`requestId`, `isSidechain`, `message.id`, `message.usage` with input/output/cache, `service_tier`, `speed`), model `claude-opus-4-8`. The `claude_code` normalizer parses it unchanged, so the new `(message.id, requestId)` dedup applies directly. Two real nuances: (1) **no `costUSD`** on the line (the computed api-equivalent path runs; reported stays nil), and (2) the project-folder name and every line's **`cwd` point at Cowork's own session dir** (`~/Library/Application Support/Claude/local-agent-mode-sessions/<id>/…/outputs`), **not the user's repo** — so repo/cost_tag attribution is opaque for Cowork events unless we special-case it. See [phase-0B](phase-0B-provider-coverage-and-findings.md) (attribution) and review-log §13.

### 2.2 OpenAI Codex CLI

**Location.** `~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`, plus `~/.codex/archived_sessions/`. **[verified]** `CODEX_HOME` (comma-separated) overrides the base dir; ccusage scans both `sessions/` and `archived_sessions/` under each home. Session IDs are backend-generated and stored inside the file; you can rename files but `/resume` uses the internal ID.

**Format.** JSONL. Each line is `{timestamp, type, payload}`. The `type` is the outer envelope; the real discriminator is often `payload.type`.

**Records (validated against v0.130.0).** Outer `type` values include `session_meta`, `turn_context`, `event_msg`, `response_item`.
- `session_meta.payload`: `model_provider`, `cli_version`, plus session context.
- `turn_context.payload.model`: the active model for that turn (your per-turn model attribution).
- `event_msg` with `payload.type == "token_count"`: token totals under `payload.info`. **[refined]** Two fields live here: `total_token_usage` (**cumulative**) and `last_token_usage` (**per-turn delta**). Newer Codex populates `last_token_usage`, so you can read it directly; fall back to diffing `total_token_usage` only when it's absent. Interspersed everywhere, with no fixed cadence.
- `response_item` with `payload.type == "function_call"`: tool invocation (`name`, `arguments` as a JSON *string*, `call_id`).
- `response_item` with `payload.type == "function_call_output"`: result, paired by `call_id`.
- `event_msg` / `agent_message`: reasoning text. `response_item` / `message` (role assistant): the user-facing reply.

**Cost.** Not stored; compute from the cumulative token deltas times a pricing table.

**Traps.**
- **Prefer the per-turn field, diff only as fallback.** **[refined]** Read `info.last_token_usage` when present; otherwise subtract consecutive `total_token_usage` values (saturating). Never sum cumulative totals.
- **Field names drift across generations.** **[verified]** ccusage accepts `input_tokens` OR `prompt_tokens` OR `input`; `output_tokens` OR `completion_tokens` OR `output`; `cached_input_tokens` OR `cache_read_input_tokens` OR `cached_tokens`; `reasoning_output_tokens` OR `reasoning_tokens`. Match any of them.
- **Tool calls are flat, paired by `call_id`, not nested.** Buffer pending calls and match on arrival; there can be many lines between a call and its output.
- **No causal parent field.** Causality is inferred from ordering. Fine for ~80% of cases; you cannot always prove which output triggered which next call.
- **Three format generations** exist (the new metadata format, a mid format, and the oldest ~Aug 2025). **[corrected]** Sessions lacking model metadata are **not** simply skipped by current ccusage — it falls back to `gpt-5` (or a date-based auto-review model table) so the tokens still count. Older TS ccusage did skip them; behavior changed.
- **Second "headless" shape.** **[verified]** `codex exec --json` output is a different, non-cumulative shape with usage under `result` / `data` / `response` `.usage`. If you support headless runs, detect and parse it separately.
- **Active vs archived** can hold the same relative path; let the active `sessions/` copy win.

### 2.3 Cursor

**Location (the messy one).** Cursor does not have a single session-log folder. Expect:
- `globalStorage/state.vscdb` (SQLite). Table `cursorDiskKV`, keyed rows: `composerData:<composerId>` (session header/metadata) and `bubbleId:<composerId>:<bubbleId>` (individual messages). Also `aiService.generations` and `aiService.prompts`.
- `workspaceStorage/<hash>/state.vscdb` (SQLite). Table `ItemTable` (key/value, value is JSON-as-text), keys like `composer.composerData` and legacy `workbench.panel.aichat.view.aichat.chatdata`. A sibling `workspace.json` maps the hash back to the real project path.
- Newer surfaces: `~/.cursor/chats/`, `~/.cursor/projects/.../agent-transcripts/*.jsonl`, `~/.cursor/ai-tracking/ai-code-tracking.db`, `~/.cursor/prompt_history.json`.

Platform roots: macOS `~/Library/Application Support/Cursor/User/`, Linux `~/.config/Cursor/User/`, Windows `%APPDATA%\Cursor\User\`. Resolution order people use: explicit custom path, then `CURSOR_DATA_PATH`, then platform default.

**Format.** SQLite key-value, inherited from VS Code. Bubbles carry user text, assistant responses, timestamps, tool calls, diffs, and optional thinking blocks. Three serialization formats have existed over time, so a parser needs fallbacks (legacy chat key first; then scan values for `conversations/messages/assistant/role` structures; last resort, synthesize from `aiService.generations`).

**Cost.** Effectively **not available locally** in a reliable form — and **[verified]** it's worse than "unreliable." codeburn's cursor provider hardcodes a cost model (`CURSOR_COST_MODEL = 'claude-sonnet-4-5'`) and a `CHARS_PER_TOKEN = 4` constant, with a comment stating plainly that Cursor v3 stores zero token counts so it estimates them from text length (`ceil(textLen / 4)`), pricing against the assumed model with zero cache tokens. In other words the tooling *fabricates* the token counts because the local store has null/zero values. Usage and billing are computed server-side; for real numbers use Cursor's dashboard / admin usage API, not the SQLite store.

**Traps.** Chats are tied to the folder path and "disappear" when you move a project. The global DB can balloon to multiple GB; deleting it (common disk-cleanup advice) bricks history into an infinite "Loading Chat." Query *both* the global `cursorDiskKV` and per-workspace `ItemTable`. Open the DB read-only and WAL-safe (`immutable=1` / `mode=ro`) so you don't fight the running app.

### 2.4 Gemini CLI

**Location.**
- Session transcripts: `~/.gemini/tmp/<project_hash>/chats/` (the `chatRecordingService`), JSONL, project-scoped.
- Checkpoints: `~/.gemini/tmp/<project_hash>/checkpoints/` (JSON conversation + tool-call snapshots; feature off by default, enabled via `settings.json`). Shadow git snapshots: `~/.gemini/history/<project_hash>`. Shell history: `~/.gemini/tmp/<project_hash>/shell_history`.
- Telemetry (opt-in, OpenTelemetry): local file `.gemini/telemetry.log` or an OTLP collector, configured in `~/.gemini/settings.json` under `telemetry`.

**Format.** Session recording is JSONL with messages, tool calls, per-turn token metadata (prompt / candidate / cached / total tokens), and assistant "thoughts"/reasoning summaries. Telemetry follows GenAI OTEL semantic conventions: metric `gemini_cli.token.usage` (by token type, including `thought`), plus `gen_ai.client.token.usage`, and structured log events (`gemini_cli.api_response`, `gemini_cli.tool_call`, file-operation counters with user-added/removed lines).

**Cost.** Token usage is **native and clean** in the recording, the nicest of the bunch. Cash cost still has to be computed, and is complicated by Gemini's free tier and Code Assist quota plans, where the marginal cash cost may be zero.

**Traps.** Telemetry is off by default, so don't rely on `.gemini/telemetry.log` existing. The `tmp/` location hints at possible cleanup; archive if you need durability. Everything is keyed by `project_hash`, so you need the project root to resolve which sessions belong where. A `surface` tag in the User-Agent (and `GEMINI_CLI_SURFACE`) lets you distinguish IDE vs terminal traffic.

### 2.5 GitHub Copilot CLI

**Location.** Two distinct data sources, and which one you read changes everything:
- **Session log:** `~/.copilot/session-state/<session-id>/` containing `events.jsonl` (full session history), `workspace.yaml` (metadata), `plan.md`, `checkpoints/`, `files/`. Legacy: `~/.copilot/history-session-state/` (pre-v0.0.342). A JetBrains variant lives under `~/.copilot/jb`. Config in `~/.copilot/config.json`, etc. The base dir honors `XDG_CONFIG_HOME`.
- **OpenTelemetry file export [corrected — this is the good one]:** `~/.copilot/otel/*.jsonl`, plus an explicit path via `COPILOT_OTEL_FILE_EXPORTER_PATH`. This is what ccusage actually reads, and it carries the **full token breakdown** under GenAI semantic-convention attributes: `gen_ai.usage.input_tokens`, `gen_ai.usage.output_tokens`, `gen_ai.usage.cache_read.input_tokens`, `gen_ai.usage.cache_write.input_tokens`, `gen_ai.usage.reasoning.output_tokens`. But the user must **enable the exporter** before/while running Copilot — ccusage's empty-state message literally tells them to.

The VS Code Copilot Chat extension is separate again, writing `.json` transcripts under the VS Code user dirs.

**Format.** JSONL in both cases. The OTEL file is OTEL records (spans + log records) keyed by `trace_id` / `response_id`; ccusage dedupes across four record sources (ChatSpan, InferenceLog, AgentTurnLog, AgentSummarySpan) so the same inference logged in multiple places isn't counted twice.

**Cost.** Weak for accounting on two counts, but the nuance matters:
- **The session log is output-only [corrected].** codeburn's `events.jsonl` parser reads `assistant.message.outputTokens` and hardcodes input to 0 with no cache — so "total tokens only" was generous; the session log gives you *output* tokens and nothing else. Its VS Code transcript path estimates tokens from content length (`/4`) when they're missing. The full input/output/cache/reasoning split exists **only** through the OTEL exporter above.
- **Premium-request billing.** Copilot bills in **premium requests** (quota units with per-model multipliers), not tokens. `/usage` shows "premium requests est." So even a perfect token breakdown won't reproduce the invoice. To track Copilot *cost*, model premium-request consumption against the plan; the token cost both tools compute is a *shadow* number.

**Traps.** Two sources with wildly different fidelity; the good one is opt-in; output-only session log; premium-request billing; default cloud sync (privacy/compliance); the session-state path moved at v0.0.342+, so handle both layouts.

### 2.6 OpenCode (sst / anomalyco)

**Location.** `~/.local/share/opencode/storage/` (XDG data dir; override with `OPENCODE_DATA_DIR`, which accepts a comma-separated list of roots). File-based JSON layout:
- `session/<projectID>/<sessionID>.json` (metadata: `id`, `version`, `projectID`, `parentID` for subagents)
- `message/<sessionID>/<messageID>.json` (assistant messages carry `tokens{}`, `cost`, completion status, typed errors like `ContextOverflowError`)
- `part/<messageID>/<partID>.json` (tool calls, text, file snapshots; discriminated union by `type`)
- `session_diff/`, `share/`, `project/<projectID>.json`, `migration`

**Format shift to watch.** **[verified]** Recent OpenCode is mid-migration from these JSON files into a **SQLite** database. ccusage reads **both** — it opens the db (`load_entries_from_database`) *and* globs `storage/message/*.json`, deduping by entry id across the two. So don't pick one; read both and dedupe, exactly as the production tracker does.

**Cost.** Tokens are present per assistant message (`tokens.input/output/cache.write/cache.read/total`). **[verified]** The `cost` field exists but is **frequently 0** — ccusage has a test literally named `calculates_cost_when_opencode_stores_zero_cost` that proves it recomputes from tokens when cost is 0. Model aliases need resolving first (e.g. `gemini-3-pro-high` → `gemini-3-pro-preview`, `k2p6` → `kimi-k2.6`), and ccusage also tries provider-prefixed candidates like `github_copilot/claude-sonnet-4-5` plus dotted→dashed normalization (`claude-sonnet-4.5` → `claude-sonnet-4-5`).

**Traps.** Storage grows unbounded (no built-in prune; ~500 MB/month per active project is reported), so plan for large reads. Mixed-version sessions (v0.13.x next to v0.14.x) have broken loaders before; your parser should tolerate version skew. Subagents link via `parentID`.

### 2.7 Aider

**Location.** Per-repo, in the project root (not centralized):
- `.aider.chat.history.md` (Markdown transcript of prompts and responses)
- `.aider.input.history` (raw input lines)
- `.aider.llm.history` (full LLM conversation log, opt-in via `--llm-history-file`)

Opt-in analytics: `~/.aider/analytics.json` (uuid + opt-in state) and an analytics event log when run with `--analytics`. Aider prices via LiteLLM internally and prints per-message cost and tokens to the terminal.

**Format.** The primary artifact is **Markdown**, not structured JSON. That's the headline: Aider is built for human-readable history, not machine accounting. **[verified]** Tellingly, *neither* ccusage nor codeburn ships an Aider adapter at all — the Markdown history isn't worth parsing for structured cost, which is the strongest possible vote of no-confidence in this data source.

**Cost.** Token and cost figures are computed by Aider and shown in the terminal, but **not persisted** unless analytics is enabled. With `--analytics`, you get structured events you can parse. Without it, cost is ephemeral.

**Traps.** Parsing Markdown for accounting is brittle (you'd be regexing prose). Files are per-repo, so fleet aggregation means crawling many repos. If you need Aider cost, push users to `--analytics` or capture `.aider.llm.history` and re-price yourself.

### 2.8 Pi (earendil-works / `@mariozechner/pi-coding-agent`)

**Location.** JSONL session files. **[verified]** ccusage resolves: an explicit custom path, then the `PI_AGENT_DIR` env var, then `~/.pi/agent/sessions/`. (Pi's own runtime also honors `--session-dir` / `PI_CODING_AGENT_SESSION_DIR` / `sessionDir` in `settings.json` for where it *writes*; resolve the chain to find files.) Folder-trust decisions are stored under `~/.pi/agent/`.

**Format.** JSONL. Full history is retained in the JSONL even after lossy `/compact` (so the file is the source of truth; `/tree` and `/fork` navigate it). RPC mode uses strict LF-delimited JSONL framing. The Rust reimplementation (`pi_agent_rust`) adds an optional **SQLite** session backend (`sqlite-sessions` feature) alongside JSONL.

**Cost.** **[verified]** Richer than first assumed. Pi lines with `type == "message"` and `message.role == "assistant"` carry `message.usage` with `input`, `output`, `cacheRead`, `cacheWrite`, and `totalTokens`, plus a real `cost.total`. So it's a full cache-aware token breakdown *and* a reported cost — verify on a sample, but the schema is clean and parseable.

**Traps.** Configurable session dir means you must resolve the precedence chain to find files. JSONL vs optional SQLite by build. Smallest ecosystem, so fewest reference parsers; budget time to reverse-engineer from sample files.

---

## 3. Cross-agent comparison matrix

| Dimension | Claude Code | Codex | Cursor | Gemini CLI | Copilot CLI | OpenCode | Aider | Pi |
|---|---|---|---|---|---|---|---|---|
| Container | JSONL | JSONL | SQLite | JSONL | JSONL | JSON files / SQLite | Markdown | JSONL / SQLite |
| Root | `~/.claude/projects/` (+ XDG, CLAUDE_CONFIG_DIR) | `~/.codex/sessions/` (+ CODEX_HOME) | VS Code user dirs + `~/.cursor/` | `~/.gemini/tmp/` | `~/.copilot/session-state/` + `~/.copilot/otel/` | `~/.local/share/opencode/` | repo root | `~/.pi/agent/sessions/` |
| Project scoping | encoded path folder | date folders | workspace hash | project hash | session-id folder | projectID | per-repo | per-project |
| Token granularity | in/out/cache (in/out buggy) | per-turn or cumulative in/cache/out/reasoning | none locally (estimated from chars) | prompt/candidate/cache/total | session log: output only; OTEL: full | full per-message | terminal only | full incl. cache + cost |
| Counting model | per-message, dedupe (msg.id,req.id) keep-max | prefer last_token_usage, else diff | n/a | per-turn | OTEL spans deduped by trace/response id | per-message | n/a | per-message |
| Cost field on disk | costUSD sometimes present | no | no (estimated) | no | no (premium requests) | yes but often 0 | analytics only | yes (cost.total) |
| Subagents | separate files / subagents dir + agentId | thread_spawn + replay dedup | n/a | structured subagent records | inline | parentID sessions | n/a | fork/tree |
| Biggest trap | streaming undercount | cumulative vs per-turn + 3 formats | no local cost (faked tokens) | telemetry off by default | OTEL export is opt-in | JSON + SQLite dual read | Markdown not JSON (skipped) | small ecosystem |
| Auto-deletion | yes (retention) | archived dir | grows huge | tmp cleanup | sync to cloud | unbounded growth | persists | persists |

---

## 4. A parser architecture that survives contact with reality

Since you live in Go, here's the shape I'd build.

**Canonical event model.** Normalize every agent into one neutral event so the rest of the system never sees vendor quirks:

```
type Tokens struct {
    InputFresh   int64
    Output       int64
    CacheRead    int64
    CacheWrite5m int64
    CacheWrite1h int64
    Reasoning    int64
    Total        int64
}

type Event struct {
    Agent        string    // "claude-code", "codex", ...
    AgentVersion string
    SessionID    string
    ProjectPath  string
    Timestamp    time.Time
    Provider     string
    Model        string
    Role         string    // user | assistant | tool | system
    EventType    string    // message | tool_call | tool_result | token_count | snapshot
    Tokens       Tokens
    CostReported *float64  // nil unless the tool wrote a real, non-zero number
    CostComputed *float64
    CostMethod   string    // reported | computed_list | computed_discounted
    RequestID    string    // for dedup
    ParentUUID   string
    ToolName     string
    SubAgentID   string
}
```

**Adapter pattern.** One adapter per agent, each owning discovery, version-detection, parsing, and its own dedup key:

```
type Adapter interface {
    Name() string
    Discover() ([]SessionRef, error)        // walk the agent's store
    DetectVersion(SessionRef) (string, error)
    Parse(SessionRef) ([]Event, error)       // emit canonical events
}
```

**Dedup layer, per agent.** Claude Code dedupes by `(message.id, request_id)` and keeps the max-token entry. Codex prefers `last_token_usage` and only diffs cumulative `total_token_usage` as a fallback. OpenCode dedupes by id across its JSON files *and* its SQLite db, and lets active sessions beat archived. Don't try to unify this; isolate it inside each adapter. **[verified]** This per-adapter shape is exactly how ccusage is now organized — one `adapter/<name>/` module each, with its own paths/parser/dedup.

**Cost engine with provenance.** Prefer `CostReported` when present and non-zero. Otherwise compute `tokens x priceTable[model][tier]` and stamp the method. **[verified]** ccusage encodes this as three modes worth copying: `Display` (reported only), `Calculate` (computed only), and `Auto` (reported-else-computed, the sane default). Keep the price table external and refreshable: the **LiteLLM** dataset (`BerriAI/litellm/.../model_prices_and_context_window.json`) is the de facto primary source both tools use, with **models.dev** as a fallback; both embed a build-time snapshot for offline use. The price table needs per-tier fields (5m/1h cache write, 200k context tier, fast multiplier), not a flat input/output pair. Crucially, support a **discount overlay** (committed-use / enterprise rates) so the computed number can reflect a real contract, not list price. Tag those as `computed_discounted` so finance can tell them apart.

**Incremental ingest.** JSONL stores are append-only, so track a byte offset per file and tail with `fsnotify`. SQLite stores (Cursor, new OpenCode) must be opened **read-only and immutable** so you don't collide with the live app. Archive on ingest because Claude Code deletes old sessions and Gemini uses a `tmp/` dir.

**Fail soft.** Unknown record types are skipped, not fatal. Unknown versions fall back to a best-effort generic reader and a logged warning. A cost tracker that crashes on a new CLI release is worse than one that under-reports for a day.

---

## 5. Cost-derivation strategy (the part finance will audit)

1. **Separate observed usage from imputed cost.** Store tokens as fact. Store cost as a derived, labeled estimate.
2. **Never report a single blended number across subscription and API.** Split "shadow cost at API rates" from "actual cash billed." Show both; never add them.
3. **Carry the discount.** List-price math is the wrong answer for any enterprise. Make the discount a first-class input.
4. **Reconcile, don't assume.** Where a vendor usage API exists (Cursor admin, OpenAI usage, Anthropic console), periodically reconcile local-derived totals against it and surface the drift. That drift number is itself a feature: it tells customers how much to trust the local estimate.
5. **Cache pricing is not a footnote.** With agents re-sending large context every turn, cache-read and cache-write tiers dominate the bill. Get the 5m vs 1h write tiers right or your numbers are fiction.
6. **Source the plan envelope; don't expect it on disk.** Plan, tier, seats, and billing-period dates are not in the session files (see 1.3). To frame shadow cost against a real subscription — "$X of API-equivalent usage against a $20/mo seat," or quota burn against a renewal date — get the plan from the vendor's billing/admin API or have the user declare it, and store it as a separate, labeled input. The only native local signal in this direction is Claude's usage-limit reset timestamp, and it only fires once a limit is hit, so treat it as a hint, not a billing cycle.

---

## 6. Privacy and trust posture

These files are full of source code, secrets, and prompts. If you ingest them into a SaaS, you've taken on your customer's IP. Two defensible designs:

- **Local-first agent.** Parse on the machine, ship only aggregates (tokens, costs, model, project hash) upstream. Raw transcripts never leave.
- **Hash-on-ingest.** If you must centralize, hash or strip prompt/file content at the edge and keep only the accounting skeleton.

For a buyer who cares about SOC 2 / ISO 27001, "your code and prompts never leave your machine" is a concrete, checkable claim and a real wedge against any competitor that uploads transcripts.

---

## 7. Verify-on-disk checklist (don't trust this doc blindly)

Run these against a live install before writing a parser line. Paths and fields *will* have drifted.

```
# Claude Code  (also check $XDG_CONFIG_HOME/claude and $CLAUDE_CONFIG_DIR)
ls ~/.claude/projects/*/ | head; head -3 ~/.claude/projects/*/*.jsonl

# Codex
ls ~/.codex/sessions/*/*/*/ ; head -5 ~/.codex/sessions/*/*/*/rollout-*.jsonl
grep -m1 token_count ~/.codex/sessions/*/*/*/rollout-*.jsonl

# Cursor (macOS path shown)
sqlite3 "~/Library/Application Support/Cursor/User/globalStorage/state.vscdb" \
  "SELECT key FROM cursorDiskKV WHERE key LIKE 'composerData:%' LIMIT 5;"

# Gemini CLI
ls ~/.gemini/tmp/*/chats/ ; head -3 ~/.gemini/tmp/*/chats/*.jsonl

# Copilot CLI — session log (output-only) AND the OTEL export (full breakdown)
ls ~/.copilot/session-state/*/ ; head -3 ~/.copilot/session-state/*/events.jsonl
ls ~/.copilot/otel/ 2>/dev/null ; head -3 ~/.copilot/otel/*.jsonl 2>/dev/null
# (if otel/ is empty, the exporter isn't enabled — the good token data won't exist)

# OpenCode — check BOTH the JSON files and a SQLite db
ls ~/.local/share/opencode/storage/message/*/ | head
ls ~/.local/share/opencode/*.db ~/.local/share/opencode/**/*.sqlite* 2>/dev/null

# Aider
head -20 .aider.chat.history.md

# Pi
echo "$PI_AGENT_DIR"; ls ~/.pi/agent/sessions/ 2>/dev/null; head -3 ~/.pi/agent/sessions/*.jsonl 2>/dev/null
```

For each tool, confirm three things before trusting it for cost: (1) which field carries fresh-input vs cache tokens, (2) whether token counts are per-event or cumulative, and (3) whether any on-disk cost field is real or a zero placeholder. Those three questions are where the money is, and where the bugs hide.

---

## 8. Open items to confirm

Several earlier open items were closed by reading ccusage and codeburn (Pi schema, OpenCode SQLite dual-read, Codex per-turn field, Copilot's real data source). What's left:

- **Claude Code base-token correction:** the keep-max dedup on `(message.id, request_id)` is the field-tested mitigation, but decide whether you additionally cross-reference status-line totals for display. Both are imperfect.
- **Cowork schema:** ~~assumed identical to Claude Code; verify on a real Cowork install.~~ **RESOLVED 2026-06-15** — verified identical on a live session (same store + schema; no `costUSD`). New open item instead: **Cowork attribution** — its `cwd` is the session dir, not the user's repo, so events bucket under a meaningless project. Track a fix (derive the real project, or special-case Cowork).
- **Copilot OTEL enablement:** the full breakdown only exists if the user turned on the file exporter. Decide product behavior when `~/.copilot/otel/` is empty — fall back to output-only session-log parsing, or prompt the user to enable it.
- **Copilot premium-request accounting:** still need the plan/quota model, since neither tokens nor the OTEL breakdown reproduce the premium-request bill.
- **OpenCode SQLite schema:** capture the concrete table/column shape once the migration lands widely, so the db reader doesn't rely on ccusage's internal mapping.
