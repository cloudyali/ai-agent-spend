# 14 — Spend ↔ outcome: our cost trailers vs CodeBurn's `yield`

Status: **concept / comparison capture** · 2026-06-24 · owner: Nishant · companion to
`11-commit-cost-trailers.md` (the write path), `13-spend-to-effectiveness.md` (the
denominator), `09-session-view.md` (VCS linkage + churn) · prior art compared:
[CodeBurn `yield`](https://codeburn.app/docs/yield) (`getagentseal/codeburn`, MIT)

> **The comparison in one line:** our trailers *write* the cost **into** the commit — a
> numerator, frozen in history, portable. CodeBurn's `yield` *reads* git **back** to judge
> whether the spend produced anything that survived — a denominator (productive / reverted /
> abandoned). They point in **opposite directions and compose**: `yield` is almost exactly
> doc 13's unbuilt waste denominator, and our frozen `AI-Cost` trailer is the cost source it
> would want to read.

This doc captures a head-to-head asked for on 2026-06-24, **verified against the code on both
sides** (our `internal/trailer` + `internal/vcs`; CodeBurn's published `yield` docs). The
conclusion isn't "they beat us here" — it's that the two features answer different halves of
the same sentence, and seeing them side by side sharpens what doc 13 should build and what
doc 11 already does better.

## The two directions

aispend already attributes spend **to** commits (read side, `09`) and writes spend **into**
commits (write side, `11`). CodeBurn `yield` adds a third motion we don't have: reading git
to grade the spend. Laid flat:

| | **Our trailers (`11`)** | **CodeBurn `yield`** |
|---|---|---|
| Motion | **Write** cost into the commit message at commit time | **Read** git after the fact, no writes |
| Question | "What did this commit cost?" | "Did that spend survive?" |
| Output | `AI-Cost: 0.42` in `git log` / PR / GitHub, tool-free | `productive / reverted / abandoned` split for a period |
| Join key | The exact SHA the `post-commit` hook just created | Session↔commit **correlation by timestamp** |
| State | Per-branch watermark in `.git/` (deferred-truncation) | None — recomputed live each run |
| Scope | The commit you're making, any branch | Current branch, needs local git history |
| Reliability cost | High (mutates messages; rebase/squash/amend edge cases) | Low (read-only) |

The odometer analogy from `13` holds: trailers stamp **how much fuel this leg burned**, onto
the leg itself. `yield` is the **trip report** that asks how many of those legs actually got
you somewhere. One is provenance; the other is outcome.

## What CodeBurn `yield` actually does (verified)

From the published docs: `codeburn yield -p <today|7days|30days|month>` correlates AI sessions
with git commits **by timestamp** — "commits made during or shortly after each session" — and
buckets the spend three ways:

- **Productive** — commits from this session landed in `main`.
- **Reverted** — commits were later reverted.
- **Abandoned** — no commits near the session, or commits never merged.

It must run inside a git repo and needs history on the current branch. It writes nothing. It's
read-only analysis layered on the same on-disk session logs CodeBurn already parses — the same
shape as its `optimize` and `compare` features. There is no watermark, no commit mutation, no
hook: every run re-derives the verdict from live git.

## What ours actually does (verified)

The write path is the careful, stateful one. `prepare-commit-msg → aispend trailer`
(`internal/trailer/state.go: Trailer`) resolves the branch, reads the per-branch watermark,
asks the ledger for priced+deduped usage **strictly after** that mark, formats the configured
lines (`trailer.go: FormatTrailers` → `AI-Cost`, optional per-model/tokens/interactions),
applies them idempotently, and **stages** (not promotes) the new watermark. `post-commit →
aispend consume` promotes it. Staging-not-promoting is the deferred-truncation guarantee — a
cancelled commit carries its usage forward. Squash folds duplicate cost lines (`foldCost`);
merge / `commit` / `--amend` skip; detached HEAD skips; mid-rebase `consume` is a no-op. Writes
are newline-sanitized (`oneLine`). Pure-local, fail-open, no `net/*` — the offline build and
`doctor --network` are untouched.

The crucial asymmetry: our `post-commit` hook **knows the SHA it just created**, so for any
repo with hooks installed, commit↔spend is ground truth — not the reflog reconstruction
(`vcs.HeadAt`) we fall back to elsewhere. That's the "accuracy dividend" of `11`.

## The realization: `yield` ≈ doc 13, and the two compose

`yield` is not a competitor to our trailers — it's a shipped, minimal version of **doc 13's
denominator**, specifically signal #3 (dead spend / abandonment) and the surviving-churn idea.
We have that on paper and nothing shipped; CodeBurn shipped a timestamp-correlation cut of it.

And the two features feed each other:

- A `yield`-style view over **our** data reads cost from the frozen `AI-Cost` trailer (the
  "reconciliation anchor" already named in `11`), instead of re-pricing live. Frozen cost meets
  observed outcome — **a combined story neither tool has alone**: CodeBurn grades spend it
  re-derives each run; we'd grade spend that's notarized in the commit.
- Conversely, an outcome label makes the trailer *worth reading* — cost is only interesting
  next to "and it survived / and it got reverted."

## What to borrow (UX)

**Ship the outcome view — but route it through our frozen SHA, not a re-correlation.** The
clean first cut is **an outcome column on the commit view we already have** (the `c` key /
`report --by commit`): `merged · reverted · abandoned`, or better, a *surviving-churn %*. Small
surface, high value, and it lands doc 13's leak story where decisions already happen. `yield`
validates both the demand and a minimal shape (three buckets, one `--period`, which maps
straight onto our calendar-window engine).

**Borrow the verdict, not its tone.** `yield`'s flat "Abandoned" will wrongly accuse
legitimate WIP, experiments, and long-running branches. Doc 13 already has the better posture —
*surface waste, drill to evidence, let the human label "spike vs spin"* — and the surveillance
landmine ("measure the work, not the worker," k-anonymity from `1B`). Borrow `yield`'s idea
through that lens; a self-certifying receipt under every claim is the part CodeBurn structurally
can't copy.

## Where we're already ahead — surface it, don't rebuild it

- **Attribution reliability.** `yield` correlates by timestamp ("during or shortly after"),
  which is fragile against late commits, many-sessions-to-one-commit, squashes, and rebases. We
  freeze `event.GitSHA` at scan via `HeadAt`, and with hooks installed get the exact SHA from
  `post-commit`. If we ship an outcome view, attributing through the frozen SHA is **strictly
  more accurate** than a timestamp re-correlation — we already paid for this.
- **The Cowork/`HEAD` case we already fixed.** CodeBurn supports Claude Desktop, whose sessions
  log `gitBranch:"HEAD"`. Timestamp correlation almost certainly misattributes those — the exact
  failure our increments 10–11 close (`cli.branchMatches` folds placeholder `HEAD`/`""` onto the
  committed branch; `vcs.CurrentBranch` rewrites `HEAD`→ real branch at scan). We're more robust
  to precisely the provider `yield` is weakest on.
- **Offline / repo-gone / cross-repo.** `yield` is branch-local and re-reads git live. Our
  commit grouping is over **stored events** (`11`, increment 9: git-independent, works even if
  the repo is deleted). So an outcome view for us is mostly *relabeling data we already group* —
  see the performance note.
- **Drill-to-evidence.** Every aispend row opens to the receipt. `yield` shows the bucket; it
  can't show the turns.

## The reliability cost that's ours alone

Being a write path, our trailers carry stateful risk `yield` never touches — watermark
truncation, squash-fold, rebase no-op, fail-open, commit-message mutation. That's the price of
the portability moat, and worth paying, but it's the honest reason the **security-review gate
matters more for us than it ever would for a read-only command**. A `yield`-style outcome view
should be built on the *read* side, where it inherits none of that risk.

## The missing primitives (small, and they fit the existing seam)

Building the outcome label dependency-free needs only:

- **"Did the SHA land in `main`?"** — `git merge-base --is-ancestor <sha> <main>` (exit-code
  read), behind the same git-binary seam as `vcs.Numstat`. Best-effort, degrade-to-unknown like
  the rest of `internal/vcs`.
- **Revert detection** — git's own `Revert "<subject>"` + `This reverts commit <sha>.` body; a
  message scan we can do from `trailer.ReadCommitMessage`, almost git-light.
- **Surviving churn (the honest denominator, `13`)** — is line *L* from SHA still in HEAD *N*
  days on? One extra git pass; more robust than revert-detection and anti-vanity (deleted code
  scores *worse*). The heavier, later option.

All computed **once at scan and frozen on the event**, never lazily at render — the same
discipline `09`/`11` already enforce because the ledger hashes paths and can't re-resolve later.

## Converge

- **Now** — an outcome column on the existing commit view (`c` / `--by commit`): `merged ·
  reverted` via `merge-base --is-ancestor` + message-scan revert detection, attributed through
  the frozen SHA. Reuse the `Numstat` git seam; freeze at scan. A `today` one-liner
  (*"abandoned ≈ $X this week"*) mirrors `13`'s "recoverable" line.
- **Next** — fold into `report --view yield` (or `--view waste`, per `13`), reading the frozen
  `AI-Cost` trailer as ground-truth cost where present; the TUI leaks panel.
- **Later** — surviving-churn (second git pass, N-day horizon); the k-anonymous team rollup as
  the CFO-legible line (rides `1B`); dev-stack outcome (PR merged / issue closed) for the *full*
  denominator beyond the laptop.

## Open questions

- **Where does the outcome label live — column, view, or both?** Lean: an outcome column on the
  commit view first (cheap, drillable), a `report --view` once the signal proves material.
- **What's "main"?** `main`/`master`/`HEAD`'s upstream? Config, with a sane default and a
  degrade-to-unknown when ambiguous — never a wrong verdict.
- **Trailer vs. live re-price on conflict** (carried from `11`): when a SHA carries an `AI-Cost`
  trailer *and* the logs still exist, the frozen trailer is canonical for that SHA; a rescan only
  fills gaps. An outcome view should honor the same rule.
- **Does this dilute the wedge?** Same answer as `13`: it's not a new product, it's the same
  ledger answering a bigger question. Lead with spend; earn the outcome story. Flip the order and
  we sound like every other AI-productivity vendor.
- **Material waste?** The riskiest assumption stays `13`'s: run the abandonment + surviving-churn
  pass over our own ledger and a few design partners' before investing — ~5% recoverable is a
  vitamin, 20–30% is a company.
