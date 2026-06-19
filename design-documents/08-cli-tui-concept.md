# 08 — CLI / TUI concept: rich on home turf

Status: concept / brainstorm capture · 2026-06-17 · owner: Nishant · companion to `07-ui-concept.md`

agentspend is CLI-first and "zero-dependency, provably offline." That ethos should shape the TUI — not get steamrolled by it.

## Two surfaces, very different risk

- **Rich static output** — the daily glance: run it, read it, move on. Low-risk, zero-dep, highest leverage. Most usage lives here.
- **Interactive TUI** (`aispend tui` / `watch`) — sexier, but a dependency + portability tax. Build it deliberately, justified only by drill and watch.

## Rich static output

- **Composition stripes** in `report` — each group's bar colored by token class (blue cache-read, amber cache-write-1h, teal output, purple input), so cache dominance is visible without a drill.
- **Arbitrage headline + a `cache saved ~$11K (84%)` footer** — the default view leads with value, not just a total.
- **`explain` as an ANSI cost-waterfall** — the moat on home turf: `tokens × rate` per class, a proportion bar, the total, a per-turn `without cache ≈ $X · saved Y%` line, then an evidence block (`file:line`, the dedup decision, the pricing rule, `local_only · offline`). Same receipt as the web slide-over.

## Reaching `explain` — you never type the id

The opaque `e7c41a9b` is a *reproducibility primitive, not the primary UX*. A human gets to a receipt three ways:

- **TUI drill** — arrow to any row, press ↵; the id is resolved for you. The main path, and the real reason to go interactive.
- **Selectors & flags** — `aispend explain --top` (priciest in the window), `--last` (most recent), or human selectors: `explain today:max`, `explain opus:biggest`, `explain session:3f9c`. You name the turn; agentspend resolves the hash.
- **Surfaced in output** — `report` and a new `aispend top` print ids beside notable turns, rendered as OSC-8 hyperlinks in modern terminals (click to explain). Copy-paste or click.

The raw hash then earns its own keep as *provenance*: it's stable (derived from `message.id + requestId`), so `explain e7c41a9b` is reproducible and shareable — paste it to a teammate and they see the same itemized turn. Discovery is the job of `top`/the TUI; the id is for re-running and sharing.

`aispend top` becomes a first-class command — the bridge from "my spend is high" to "explain why."

## Interactive TUI (`tui` / `watch`)

Navigable, not just pretty — the only reason to go interactive is drill (↵ → receipt). Plus: `p` cycles the pivot (model → family → provider → token → project); `m/t/c` toggle facets live; `◂ ▸` scrub the period; `watch` tails logs as you code with a live arbitrage gauge; a `compare` pane (agentspend vs CodeBurn/ccusage, window-aligned, deltas lit) productizes the reconciliation.

## Craft

One color language across CLI + web; braille sparklines (`⣀⣤⣶⣿`); OSC-8 hyperlinks for ids/paths; graceful degradation — auto-detect non-TTY / `NO_COLOR` / narrow width → plain ASCII, and never bleed color into a pipe.

## The zero-dependency call

A full Bubble Tea TUI drags in a dependency tree that cuts against "zero-dependency, vendored, provably offline" (and against `doctor --network` / the offline build). Order by ethos-fit: (a) hand-rolled ANSI for static `report` + `explain` — zero deps, ships now; (b) lipgloss-only for styling without the event loop; (c) Bubble Tea only when interactive drill + `watch` genuinely earn it.

## Phased build

- **Now** — Hand-rolled, zero-dep ANSI shipped (2026-06-17): the **session receipt**
  (`explain session:<id|max|last>`), **`aispend today`** (arbitrage-first glance +
  hourly spike bar), `report --by session`, and the color/sparkline/degradation
  layer (`NO_COLOR` / non-TTY / `TERM=dumb` → plain ASCII). Still pending here:
  composition stripes inside the `report` table and `aispend top`.
- **Next** — `tui` with drill-to-receipt, the pivot key, facet bar, period scrub (lipgloss; reach for Bubble Tea only if needed).
  - **Update (2026-06-18): shipped, and promoted to the _default channel_.** The
    interactive explorer (`internal/tui`, Bubble Tea, behind `!offline`) ships with
    period scrub, the `v` view lens, the in-explorer plan picker, and drill-to-receipt.
    A bare `aispend` now opens it (`cmdDefault`); off a TTY or in the offline build
    (`tuiBuilt=false`) it falls back to the static `today` glance. The TUI receipt
    carries the same VCS linkage as the static one — branch · SHA + per-file cost+churn
    heatmap. The earlier zero-dependency caution still holds: Bubble Tea pulls
    `net/url`+`net/netip`, so it stays compiled out of the air-gapped `offline` build.
- **Later** — `watch` live meter, `compare` pane, OSC-8 links, shareable report card.

Decided (2026-06-17): **primary user = the individual founder** → the default
glance (`today`) leads **arbitrage-first** (plan ROI + cache savings), not
facet-first. Selector grammar for `explain` landed as `session:<id|max|last>`;
`top` and the broader `today:max` / `opus:biggest` human selectors are still open.
