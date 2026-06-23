# 13 — Spend → effectiveness: from "what did it cost" to "what did it waste"

Status: **concept / brainstorm capture** · 2026-06-23 · owner: Nishant · companion to
`07-ui-concept.md` (the wedge), `09-session-view.md` (churn + the receipt),
`10-session-explorer-budgets-quota.md` (the chain view + its loop marker),
`11-commit-cost-trailers.md`, and `phase-3-optimization-and-control-handoff.md`
(right-sizing as the bridge to recommend-to).

> **The reframe in one line:** every number aispend prints today is a *numerator* —
> dollars — with no denominator. "$84 this session" means nothing until you ask *to ship
> what?* The next surface supplies the denominator — but as **waste detected**, not
> "productivity scored." Same evidence ledger, a bigger question.

## The bet

Arbitrage answers *is my subscription worth it?* The next question the data can answer is
*is this spend producing anything, or leaking?* In 2026 the dollar cost of a coding agent
is rounding error next to engineer salaries; what a team actually wants to know is whether
AI is making them faster and **where it's wasted**. Cost is the trojan horse. The prize is
AI-engineering effectiveness — measured honestly enough that an engineering org will let it
through the door.

## Measure waste, not productivity (the honest fork)

There are two ways to add a denominator, and only one fits this tool.

**Productivity** (output ÷ cost) has a denominator — "value shipped" — that is unknowable
from logs and instantly gameable. Lines of code is the classic lie; reward it and people
write more, worse code. Build this and you become GitClear-for-AI: a vanity score engineers
distrust and game, and it violates the `nil`-is-not-`zero` discipline — you cannot *assert*
productivity with evidence.

**Waste** (spend that produced nothing, or produced rework) is the inverse: defensible,
evidence-backed, drillable, and blameless — a property of the *system* ("the model looped"),
not the person ("the dev is slow").

The analogy: today aispend is an **odometer** — how far the money went. Productivity scoring
would be a bogus "miles of value" gauge. Waste detection is the **idling light** — honest,
actionable, and we already have the sensor data on disk.

## The denominator you already have — and the one you don't

On disk today (`02-data-model.md`): `SessionChurn` (+/− per file), `Files`,
`GitBranch`/`GitSHA`, `ActiveMS`, `PromptID` (the *attempt* unit), `Tokens` (5 classes),
`Tools`/`MCPServers`, `CostViews.APIEquivalent`. Plus the **`Activity` classifier deferred
since 0A** (empty today) — reviving it (edit / test / debug / explore) is the quiet
prerequisite for most of what follows.

What's **not** on disk: the real outcome — did the PR merge, the issue close, the code
survive. That lives in git history (partly ours already) and the dev stack
(GitHub / Linear / Jira) — which is the honest reason to reach beyond the laptop, and a
Phase-2 expansion hook rather than a guess.

## What to build — waste signals, ranked

Ranked by *(computable-now × clearly-waste × actionable)*:

1. **Rework-loop detection (the elbow).** `10`'s chain view already prints
   "⟲ loop starts here" — formalize it. A `PromptID` cluster (or repeated edits to the same
   file/lines, a test→fix→test oscillation) that re-touches the same files N times, tagged
   with its dollars. #1 because it's computable now, unambiguously waste, and immediately
   actionable: *"this loop cost $38; the model thrashed on auth.go."*
2. **Model right-sizing (the counterfactual).** Re-price the same tokens on cheaper models:
   *"$84 on Opus; same tokens ≈ $17 Sonnet / ≈ $3 Haiku,"* and flag the turns that were
   trivial yet ran premium. This is the **actionable-savings number arbitrage lacks**, and a
   straight line into Phase 3's recommend-to, pulled years forward.
3. **Dead spend / abandonment.** Real spend, ~zero churn, no commit = paid to produce
   nothing. Often legitimate (a spike), so don't accuse — **surface and let the human label**
   "spike vs spin." Honesty intact.
4. **Cache leakage.** Cache-read dominates cost; a session with a low cache-read ratio is
   paying full input price on repeat. *"Cache hit 41% vs your 84% norm — ≈ $X leaked."*
   Reuses the `pricing.WithoutCache` primitive.
5. **Context bloat.** Huge input/context tokens for tiny output or churn — a giant context
   pinned to change three lines.

Every one is a row that **drills to evidence**. That's the keystone no competitor can copy:
aispend can accuse a session of waste *and print the receipt*.

## The denominator done honestly: surviving churn

The best git-computable denominator is **survival**: did the expensive code **still exist N
days later**? Cost per *surviving* line nets out rework and reverts — did you pour a
foundation that's still standing, or one you jackhammered out the next week? It's novel
(nobody does it), honest (survival is observed, not asserted), and **anti-vanity** — writing
more deleted code scores *worse*, which is exactly the property that made `09` cut the streak
grid. It needs one extra git pass (is line L from SHA still in HEAD M days on?), not a new
data source. The *full* outcome denominator (PR merged, issue resolved) is the dev-stack
integration story above.

## The landmine: one decision from surveillance

Effectiveness analytics on developers is a per-engineer scoreboard waiting to happen — the
precise thing `phase-1B`'s k-anonymity exists to prevent, and the fastest way to get banned
by the eng org you're selling to. The rule that saves it: **measure the work, not the
worker.** "The model looped $38" is blameless; "engineer X wasted $38" is brand poison.
Aggregate, opt-in, waste-framed. And the constraint is itself the moat: a tool that
quantifies AI waste *without ranking engineers* is deployable exactly where the
GitClear / Copilot-impact-style tools get thrown out. The privacy posture we already have
(local-first, hash-on-ingest, PRD §6) becomes the permission slip.

## Does it dilute the wedge? (the objection, steelmanned)

*"This is scope creep — you'll drift from a sharp spend tool into vague AI analytics."* Real
risk. The discipline: it is **not a new product, it's the same ledger answering a bigger
question.** Explainability stays the foundation (every waste claim drills to evidence);
arbitrage's *reassurance* becomes waste's *action*. And keep the sequence honest — **cost is
the wedge that gets you in the door; effectiveness is the expansion that makes you a
painkiller for the buyer.** Lead marketing with spend, earn the effectiveness story. Flip
that order and you sound like every other AI-productivity vendor.

## Converge

- **Build first:** a "leaks" view — loops (#1) + right-sizing (#2). Both computable today,
  both drillable, both actionable. Surface as `report --view waste`, a TUI "leaks" panel, and
  a `today` line: *"recoverable ≈ $X."*
- **Riskiest assumption:** that there's *material* recoverable waste. ~5% = a vitamin;
  20–30% = a company.
- **Cheapest test (this week):** run the loop-detector + counterfactual over your own
  `~/.claude` ledger and 3 design partners'. The test data already exists on disk — the
  number it returns tells you whether "effectiveness" is a feature or the thesis.

## Risks / open questions

- The deferred **`Activity` classifier** is a prerequisite for clean edit/test/debug/explore
  attribution — how much can the loop + right-sizing heuristics do *before* it lands?
- The right-sizing counterfactual needs a defensible *"was this turn trivial?"* signal (token
  size + churn + activity) — guard against false *"should've used Haiku"* calls on turns that
  genuinely needed Opus.
- Surviving-churn needs a second git pass and an N-day horizon choice; decide how to attribute
  survival when files are later refactored vs reverted.
- Where does the "leaks" view live first — `today`, the TUI, or `report`? (Lean: a `today`
  one-liner + a TUI panel, mirroring budgets/quota in `10`.)
- **Out of scope (load-bearing):** any per-engineer productivity score, leaderboards,
  "AI wrote X% of your code" vanity metrics, or anything that reads code *content*.

## Phased build

- **Now** — rework-loop detection + model right-sizing over the *existing* ledger (no new
  data); the `today` "recoverable" line + a TUI leaks panel; run the demand test on real
  ledgers.
- **Next** — revive the `Activity` classifier; surviving-churn (second git pass); fold the
  waste signals into `report --view waste`.
- **Later** — a k-anonymous **team waste rollup** as the CFO-legible savings line (rides
  `phase-1B`); dev-stack outcome integration (PR/issue) for the real denominator; right-sizing
  emitted as Phase-3 recommend-to artifacts.
