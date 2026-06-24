---
description: YAGNI review of the current change-set — over-engineering only. Backed by the vendored ponytail-review skill — terse, one line per cut, ending in net lines removable. Respects aispend's deliberate seams.
argument-hint: "[optional: a git ref to diff against, e.g. main]"
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git log:*), Read, Grep, Glob
---

Perform the **YAGNI review** ("You Aren't Gonna Need It") on the current change-set. This repo's
TDD rule is *write the minimum code to get to green*; this gate holds new code to that line.

**Engine: the vendored `ponytail-review` skill** (`.claude/skills/ponytail-review/SKILL.md`). Read it
and apply its method exactly — hunt complexity only (correctness, security, and performance belong to
`/review` and `/security-review`, not here). Use its terse format, one line per finding:

`L<line>: <tag> <what>. <replacement>.`  (or `<file>:L<line>: …` across files)

Tags: `delete:` (dead/speculative — nothing replaces it) · `stdlib:` (Go stdlib already ships it) ·
`native:` (the platform/language already does it) · `yagni:` (abstraction with one impl, config nobody
sets, layer with one caller) · `shrink:` (same logic, fewer lines — show the shorter form).

Scope the diff exactly as `/review` does (working tree + branch delta vs `origin/main`, or `$ARGUMENTS`).

## aispend guardrails — do NOT flag these as YAGNI

- **Deliberate test seams.** The `Store` interface has two real implementations (in-memory + SQLite)
  satisfying one suite — an intended seam, not speculation. The injected `fetchPrices`/`priceFetcher`
  seam exists for hermetic tests. The `vcs.HeadAt`/`Numstat` hooks are isolation boundaries.
- **Documented roadmap hooks** a `design-documents/` file explicitly calls for (provider-coverage
  stubs, the pending session-end hook). If the diff cites a design doc, treat it as planned.
- **The two build SKUs** (default / `offline`) and the cache-TTL tiers — core, not extra.
- A single smoke test / `assert`-style self-check is the minimum, never bloat — don't flag it.

## Output

The ponytail-review findings (terse, as above), then the metric it ends on:

`net: -<N> lines possible.`   — or `Lean already. Ship.` if there's nothing to cut.

Then, for the checkin gate, emit exactly one machine-readable line (the hook greps for it):

`YAGNI_REVIEW_VERDICT: PASS`  — nothing in `delete:`/`yagni:` that is a clear, unjustified cut, or
`YAGNI_REVIEW_VERDICT: BLOCK` — at least one clear `delete:`/`yagni:` cut with no design-doc justification.

This command only lists cuts; it does not apply them.
