# 06 — Provider Coverage Backlog

_Last updated: 2026-06-15 · Reference. The prioritized map of *which agents to
support next* and what each one's local data is worth. Sourced from
[research-local-session-data.md](research-local-session-data.md) ("Mining AI
Coding Agents' Local Session Data") and verified against the ccusage and CodeBurn
source._

The roadmap builds providers in trust order — Claude Code (0A), then Codex +
Cursor (0B). But the trackers we benchmark against cover a much wider field:
**ccusage ships ~15 per-agent adapters and CodeBurn ~32.** This doc keeps that
intelligence so adding the next provider is a triage decision, not a research
project. The rule of thumb, learned the hard way in the research: **trust the
bytes on disk, not the docs** — every entry here is "verify on a real install
before writing a parser line."

Think of it like fuel quality, not just fuel availability: several agents leave a
rich transcript on disk, but only some of it is billing-grade. We sort by how
clean the local data is, because a clean token stream is a cheap adapter and a
fabricated one is a liability.

## The adoption tiers

| Tier | Agent | Local data | Token quality | Cost on disk | Biggest trap | When |
|---|---|---|---|---|---|---|
| **Shipping** | Claude Code | rich JSONL | in/out/cache (raw in/out undercounted) | `costUSD` sometimes | streaming-placeholder undercount → keep-max dedup | **0A (done)** |
| **Next** | Codex | JSONL (+archived) | per-turn or cumulative, full split | no | cumulative vs per-turn; 3 formats | **0B** |
| **Next** | Cursor | SQLite (replay store) | **none — fabricated from char counts** | no | local store isn't for accounting | **0B** |
| **High value** | Gemini CLI | JSONL (+opt OTEL) | **native & clean** (prompt/candidate/cache/total) | no | telemetry off by default; `tmp/` cleanup | 1A+ |
| **High value** | Pi | JSONL (+opt SQLite) | **full cache-aware split** | **yes (`cost.total`)** | smallest ecosystem; resolve `PI_AGENT_DIR` chain | 1A+ |
| **High value** | OpenCode | JSON files **and** SQLite | full per-message | field exists, **often 0** | dual-read + dedupe; unbounded growth | 1A+ |
| **Caveated** | GitHub Copilot | session log + **opt-in OTEL** | session log **output-only**; OTEL full | no (premium requests) | good data is opt-in; bills in premium requests | 1A |
| **Skip** | Aider | **Markdown** | terminal only | analytics-only | prose, not JSON — both trackers skip it | deprioritized |

Detail for the eight above lives in the research note's per-agent reference
([§2](research-local-session-data.md)). The two we build next, Codex and Cursor, are specified in
[phase-0B](phase-0B-provider-coverage-and-findings.md); Copilot's OTEL path is in
[phase-1A](phase-1A-durable-ingestion.md).

## The long tail (present in the trackers, not yet triaged)

Beyond the eight, the trackers carry adapters for a crowd of newer agents —
evidence the field is exploding, and a ready-made reference when a user asks for
one: **goose, amp, qwen, kimi, droid, codebuff, openclaw, hermes, kilo** (ccusage)
and additionally **antigravity, cline, crush, devin, forge, kiro, mistral-vibe,
mux, roo-code, warp, vercel-gateway, cursor-agent, ibm-bob** (CodeBurn). We don't
spec these until there's pull, but the adapter pattern means each is a single
file plus fixtures when the time comes — and CodeBurn/ccusage already did the
reverse-engineering we'd otherwise repeat.

## How a new provider gets added

The point of the 0A architecture is that this list is cheap to work through:

1. **Triage from this table** — clean-token agents (Gemini, Pi, OpenCode) are
   high-value, low-effort; fabricated-token ones (Cursor) are low-confidence by
   construction; Markdown-only ones (Aider) wait.
2. **Verify on disk** — run the research note's §7 checklist against a real
   install; confirm (a) which field is fresh-input vs cache, (b) per-event vs
   cumulative counts, (c) whether any cost field is real or a zero placeholder.
3. **Reference ccusage / CodeBurn** for that agent rather than reverse-engineering
   (the standing approach, review-log §10).
4. **Implement** one `Provider` + `Normalizer` (+ its own `Deduper` if it
   double-counts), fixtures, and a golden — exactly the Claude Code shape.
5. **Flag every estimate** with basis + confidence; never fold a fabricated number
   into a "true" total.

## Two cross-cutting facts the whole list shares

- **The subscription envelope is never on disk. [verified]** Plan, tier, seats,
  and billing-period dates live in the vendor's billing system, not the session
  files — CodeBurn confirms it by making the user *declare* their plan (a thin
  object: id, flat `monthlyUsd`, optional `resetDay`, a `setAt` stamp). So for any
  agent, "frame shadow cost against the real subscription" means *ask the user or
  hit the admin API*, never parse it from disk (PRD §1.3; we already do this via
  `~/.aispend/config.toml`).
- **These files are full of source code, secrets, and prompts.** Every adapter we
  add widens the privacy surface, which is exactly why the local-first / hash-on-
  ingest posture (PRD §6, [01-architecture.md](01-architecture.md)) is a feature,
  not overhead.
