# 07 — UI concept: receipts, arbitrage, and a spend prism

Status: concept / brainstorm capture · 2026-06-17 · owner: Nishant

## The bet

ccusage hands you the credit-card total. CodeBurn draws it as a pretty dashboard. aispend should hand you the **itemized receipt** — and answer the one question neither tool touches: *is my subscription actually worth it?*

So we do **not** try to out-dashboard CodeBurn. The wedge is two things only we can do, because only we keep an evidence ledger:

1. **Explainability / trust** — every number traces to `file:line` and the exact pricing rule that produced it.
2. **Subscription arbitrage** — api-equivalent vs amortized, made visible. On the current workload that's ~47× plan ROI, with prompt caching covering ~84% (~$11K/wk) of what a no-cache bill would be.

Lead with the one chart and the one drill-down nobody else has. Everything else is table stakes we can borrow later.

## Three hero views

- **Arbitrage & cache chart.** Daily api-equivalent cost, stacked by token class (cache-read dominates), superimposed on a flat amortized plan line. Two bands tell the whole story: the gap to the plan line = subscription arbitrage; the cache-read band = what caching saved. The analogy: your flat plan is the lease payment, api-equivalent is the metered taxi fare — the gap is how badly your lease is beating the meter.
- **Explain receipt (the moat).** Click any bar or number → a slide-over with the cost itemized `tokens × rate` per class (input / output / cache-read / cache-write-5m / cache-write-1h), plus a "how we know" panel: source `file:line`, the dedup decision (keep-max, N streaming placeholders dropped), and the pricing rule applied. This is the itemized receipt no competitor can print.
- **Faceted spend explorer (the "spend prism").** A left rail of facets — provider, model family/name, token type, cache hit/miss (plus project, activity) — cross-filtering a pivotable, group-by stacked chart + table. Treating cache hit/miss as a *filter* lets you ask "show me only what I paid on cache misses" — a FinOps question no other tool supports.

## CLI / TUI — don't abandon home turf

aispend is CLI-first; out-explain, don't out-pretty. Arbitrage sparkline inside `report`; `explain` rendered as an ANSI cost-waterfall; an `aispend cache` savings view; faceted `--by` / `--where` / `--pivot`; **`aispend compare`** (window-aligned reconciliation vs ccusage / CodeBurn / the invoice); and an `aispend watch` live TUI with the arbitrage chart pinned.

## Why it wins (and the proof)

We reconciled to CodeBurn within ~0.2% once windows were aligned — the scary "gap" was a window-misalignment artifact plus a streaming-dedup decision, not a pricing error. Both of those become *features*: the window pain becomes `compare`, and the dedup decision becomes a line in the receipt's evidence panel. Trust is the differentiator, and we can show our work.

## Risks / open questions

- **Who's the primary user?** The arbitrage/ROI story sings for the individual founder; faceting and cost-allocation sing for a team lead. They want different defaults — pick one or we ship everyone's second choice.
- **Does explainability earn attention, or is it founder-correctness?** Cheapest test: ship the receipt + the arbitrage card and watch which gets screenshotted into a FinOps thread.
- **Superimpose discipline.** One insight per view. The hero is the arbitrage line; cache is a toggle, not a fifth axis crammed in.

## Phased build

- **Now** — `explain` receipt (CLI ANSI + web slide-over) and the arbitrage/cache card. The wedge.
- **Next** — faceted explorer (provider / family / token type / cache) on web, with the amortized line superimposed inside every facet view.
- **Later** — `compare`/reconcile view, shareable "report card" PNG (marketing flywheel), project/activity facets, `watch` TUI.

Open: ground the web mockups in the Figma design system + sidebar nav (not yet pulled); confirm the primary user before locking the default view.
