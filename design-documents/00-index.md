# AgentSpend — Design Documents

This folder is the **living design record** for AgentSpend. It sits between the
two source artifacts and the code:

- **`aispend-prd.md` (PRD v1.3)** — *why* we build and *what* the product is.
- **`agentspend-phase0a-build-spec.md`** — the engineering spec for the first phase.
- **`design-documents/` (this folder)** — the synthesis: cross-cutting design,
  plus one elaborated spec per roadmap phase, each with a **Goal that guides the
  build** and a **demonstratable output**. Kept updated as the build proceeds.

> Think of the PRD as the map and these docs as the turn-by-turn directions. The
> map rarely changes; the directions get more detailed the closer we are to the
> turn. Phase 0A is fully detailed because we are driving it now. Later phases
> are sketched at the resolution we can honestly commit to today, and sharpened
> as each one approaches.

_Last updated: 2026-06-20 · UX/UI concept captures (07–11) added — 11 proposes per-commit cost trailers (claude-budget-style write-back into git history, opt-in) · Status: Phase 0A nearly shippable — `scan → report → today → tui → doctor` working end-to-end on a real `~/.claude`, default binary provably offline (the `explain` command was folded into the TUI receipt drill — per-turn/session evidence now lives only in the TUI). **Claude Code `(message.id, requestId)` keep-max dedup + reported-cost (`costUSD`) landed under TDD, verified against ccusage/CodeBurn** (the streaming-placeholder undercount is fixed). **Rich zero-dep ANSI surfaces shipped (t-wada TDD): `aispend today` (arbitrage-first) plus the interactive TUI explorer — day-grouped session list, session receipt, prompt-chain view, API-equivalent budgets, and the Codex+Claude quota window (doc 10), with VCS linkage (`--by branch|commit|file`) and the color/sparkline/degradation layer — see 08/09/10.** Remaining for shippable 0A: `privacy`/`export` commands, release signing + SBOM (CI workflow + goreleaser config have landed)._

---

## Reading order

1. **[01-architecture.md](01-architecture.md)** — the system shape: components,
   the single egress seam, the build-tag trust boundary, the tech stack.
2. **[02-data-model.md](02-data-model.md)** — the contract everything reads:
   `AgentEvent`, `Money`, cost views, the evidence ledger, `.aispend.toml`,
   schema versioning, the SQLite shape.
3. **[03-engineering-process.md](03-engineering-process.md)** — how we build:
   t-wada TDD, the 85–90% coverage floor, the code-review and security-review
   gates, and the CI trust assertions.
4. **[04-platform-and-paths.md](04-platform-and-paths.md)** — OS-awareness: where
   each agent's local files live on macOS / Linux / Windows, and the single
   platform layer every provider goes through to find them.
5. **[05-llm-pricing.md](05-llm-pricing.md)** — the pricing module: the rate
   schema (cache TTLs, batch, context tiers), multi-provider coverage, and how
   prices stay fresh *without* the runtime ever making a network call.
6. **[06-provider-coverage-backlog.md](06-provider-coverage-backlog.md)** — the
   prioritized map of which agents to support next and what each one's local data
   is worth, backed by [research-local-session-data.md](research-local-session-data.md).
7. **The UX/UI concept captures** — [07-ui-concept.md](07-ui-concept.md) (web),
   [08-cli-tui-concept.md](08-cli-tui-concept.md) (CLI/TUI), and
   [09-session-view.md](09-session-view.md) (the work-session receipt), and
   [10-session-explorer-budgets-quota.md](10-session-explorer-budgets-quota.md)
   (session-as-spine, prompt-chain travel, budgets, the plan-quota window): the surface
   design — arbitrage chart, `explain` receipt, faceted explorer, session receipt —
   captured from the 2026-06-17 and 2026-06-19 brainstorms.
   **[11-commit-cost-trailers.md](11-commit-cost-trailers.md)** — writing per-commit
   cost back **into** git history as trailers (the write-back complement to 09's VCS
   linkage), proposal capture from 2026-06-20.
8. **The phase specs**, in roadmap order (below).

## Document map

| Doc | Scope | State |
|---|---|---|
| [01-architecture.md](01-architecture.md) | Components, interfaces, egress seam, stack | Stable |
| [02-data-model.md](02-data-model.md) | `AgentEvent` + evidence ledger + storage | Stable |
| [03-engineering-process.md](03-engineering-process.md) | TDD, coverage, review gates, CI | Stable |
| [04-platform-and-paths.md](04-platform-and-paths.md) | OS-aware path discovery (macOS/Linux/Windows) | Stable |
| [05-llm-pricing.md](05-llm-pricing.md) | LLM pricing module: rate schema, coverage, freshness | Stable design, phased build |
| [06-provider-coverage-backlog.md](06-provider-coverage-backlog.md) | Prioritized which-agent-next map; per-agent data quality | Reference |
| [07-ui-concept.md](07-ui-concept.md) | Web UI concept: arbitrage/cache chart, `explain` receipt slide-over, faceted explorer | Concept (brainstorm capture) |
| [08-cli-tui-concept.md](08-cli-tui-concept.md) | CLI/TUI concept: ANSI `explain` receipt, composition-striped `report`, navigable TUI, reaching `explain` without an id | Concept (brainstorm capture) |
| [09-session-view.md](09-session-view.md) | Session as a first-class unit: `report --by session` + the TUI session receipt, spike-not-streak temporal view, cost+churn heatmap | Implemented |
| [10-session-explorer-budgets-quota.md](10-session-explorer-budgets-quota.md) | Session as the explorer spine (live + historical), prompt-chain travel, API-equivalent budgets, and the local plan-quota window (Codex `rate_limits` / Claude usage snapshot) | Largely implemented (A–D shipped; residue: nesting, scoped budgets, ←/→ travel, alerts) |
| [11-commit-cost-trailers.md](11-commit-cost-trailers.md) | Per-commit cost trailers: writing api-equivalent spend back into git history via hooks (claude-budget-style); the write-back complement to 09 | Concept (proposal) |
| [research-local-session-data.md](research-local-session-data.md) | Source note: mining agents' local session data (verified vs ccusage/CodeBurn) | Reference |
| [phase-0A-trusted-explainable-ledger.md](phase-0A-trusted-explainable-ledger.md) | Claude Code local ledger + `explain` | **Detailed — building now** |
| [phase-0B-provider-coverage-and-findings.md](phase-0B-provider-coverage-and-findings.md) | Codex + Cursor, fixtures, cost-driver findings, TUI | Planned |
| [phase-1A-durable-ingestion.md](phase-1A-durable-ingestion.md) | OTel, admin APIs, fixture suite | Planned |
| [phase-1B-selfhost-team-beta.md](phase-1B-selfhost-team-beta.md) | Self-host collector, team aggregation, k-anonymity | Planned |
| [phase-2-cloudyali-reconciliation.md](phase-2-cloudyali-reconciliation.md) | Cloud sync, reconciliation, unified pane | Planned |
| [phase-3-optimization-and-control-handoff.md](phase-3-optimization-and-control-handoff.md) | Recommend-to integrations, chargeback | Planned |

---

## The roadmap at a glance

Each phase exists to make the *next* one credible. The line through all of them
is **explainability**: any number, at any tier, can be opened up to its evidence.

| Phase | One-line goal | Demonstratable output | Success signal |
|---|---|---|---|
| **0A** | A trusted, explainable local ledger for Claude Code | `scan` → `week` → `explain <id>` on a real `~/.claude` session, default binary provably offline | 10–20 early users: numbers are understandable, trustworthy, "replaced my scripts" |
| **0B** | Cover more agents without silent estimates; surface fact-based cost drivers | Compare Claude Code + Codex + Cursor in one table; a neutral "you pay for Cursor *and* Copilot" finding; TUI | Users trust cross-tool comparisons and the findings |
| **1A** | Make ingestion durable enough for someone else's environment | Same numbers via OTel/admin API as via file parsing, on a design partner's machine | Design partners trust the collector internally |
| **1B** | Let a platform team aggregate without a privacy fight | Self-hosted collector rolls up a team with k-anonymity, no per-person scoreboard | Platform/DevEx teams deploy without security blockers |
| **2** | Reconcile coding-agent spend with API/invoice/seat spend in CloudYali | The unified per-seat + API pane, deduped and allocated to cost centers | First paid design partners reconcile the two surfaces |
| **3** | Hand the fix to whatever enforcement stack the team runs | Emit a LiteLLM rule / Claude Code hook / policy from a detected cost driver | CloudYali is the system of record for AI-eng spend |

---

## Conventions every phase doc follows

So the docs stay comparable and reviewable, each phase spec uses the same spine:

1. **Goal** — a single north-star statement plus the few signals that prove it.
   The goal *guides the build*: if a proposed task doesn't serve the goal, it
   waits for a later phase.
2. **Where it sits** — what it assumes from prior phases, what it unlocks next.
3. **In scope / Out of scope** — the out-of-scope list is load-bearing.
   Over-building is the failure mode these docs exist to prevent.
4. **Design spec** — components, interfaces, data, behavior, and the key
   decisions with their rationale.
5. **Demonstratable output** — the exact command(s) and the expected terminal
   output. "Done" means *this demo runs*. Every phase ends in something you can
   show, not just merge.
6. **Acceptance criteria** — a binary checklist.
7. **Test & quality plan** — the TDD cycles, fixtures, coverage target, and the
   review gates (see [03-engineering-process.md](03-engineering-process.md)).
8. **Risks & open questions** — carried forward from PRD §17 where relevant.

## Non-negotiables (apply to every phase)

These come straight from the PRD's product principles and are enforced in code,
not just prose:

- **Local by default, truthfully.** The default `go build` contains *no cloud
  code* — the cloud sink lives behind `//go:build cloudyali`. A security audit of
  the default binary finds nothing that can phone home. CI asserts it.
- **No single "true cost."** We model billed / effective-allocated / marginal /
  api-equivalent / credit-consumption / estimated, each with provenance and a
  confidence marker. A `nil` cost view means "not computable here," never "zero."
- **Evidence over assertion.** Every number carries where it came from, which
  parser and pricing table produced it, and how confident we are. `aispend
  explain` renders that ledger. This is the moat, not an internal detail.
- **Money is never a float.** Integer micro-units (`1 USD = 1_000_000 micros`)
  with an explicit currency. See [02-data-model.md](02-data-model.md).
- **Additive, never subtractive.** The commercial layer only ever *adds*. No OSS
  feature is removed and no account requirement is bolted onto the local tool.

## How these docs get kept updated

- When a phase's design changes during the build, the change lands **in that
  phase's doc in the same change-set as the code** — never after.
- When a phase ships, its status flips to **Done** here and its "Demonstratable
  output" is updated to the *actual* recorded output (e.g. a captured terminal
  session), so the doc doubles as proof.
- Cross-cutting decisions (a new cost view, a schema bump) update
  [02-data-model.md](02-data-model.md) and the affected phase docs together.
