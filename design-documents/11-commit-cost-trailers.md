# 11 — Commit cost trailers: spend that ships with the code

Status: **concept / proposal** · 2026-06-20 · owner: Nishant · companion to `09-session-view.md` (VCS linkage), `10-session-explorer-budgets-quota.md` · prior art: [`mooracle/claude-budget`](https://github.com/mooracle/claude-budget) (MIT), itself the Claude Code counterpart to Copilot Budget

> **The proposal in one line:** today aispend *reads* git provenance off each turn and
> attributes spend **to** commits (`--by commit`, the receipt's `branch · SHA` line). This
> adds the other direction — *writing* the cost back **into** the commit as a git trailer
> (`AI-Cost: 0.42`) at commit time, so spend ships with the code and is readable with a
> plain `git log`, on GitHub, in a PR review — with no tool installed on the other end.
>
> **The net-new surface is small.** aispend already owns the entire read side claude-budget
> needs — the transcript reader, the `(message.id, requestId)` keep-max dedup, the
> cache-tier pricing engine, and `internal/vcs` branch attribution. The only genuinely new
> machinery is the **write path**: two git hooks, a per-branch watermark, and a trailer
> formatter. We reuse the rest untouched.

**Implementation status (2026-06-20).** Built under t-wada TDD in two increments, both
green (`go test ./...`), reviews passed, offline / `doctor --network` intact:

- **Increment 1 — the installer.** `aispend git install|uninstall|status` in
  `internal/githook`: `core.hooksPath` honored first, refuse-to-clobber (atomic),
  Husky / Lefthook / pre-commit detection with paste-ready wiring, and a visible
  `status` wiring indicator. 94.5% coverage.
- **Increment 2 — the engine.** `aispend trailer` / `consume` (hidden, hook-invoked) in
  `internal/trailer`: trailer formatting (rename + per-model + tokens + interactions),
  idempotent apply, squash-fold, source-hint routing (merge/commit skip, squash fold),
  detached-HEAD skip, and the per-branch watermark — stage-on-`trailer`,
  promote-on-`consume`, rebase-aware no-op — i.e. the deferred-truncation guarantee.
  Writes are newline-sanitized. 90.4% coverage. (The watermark lives in
  `internal/trailer/state.go` rather than a separate `internal/gitstate` — cohesion over
  the two-package sketch below.)

- **Increment 3 — config.** `[trailers]` / `[trailers.rename]` in `.aispend.toml` is
  parsed by `config.LoadTrailers` (a section-aware reader added alongside the existing
  flat parser, which stays backward-compatible — `LoadRepo` is unaffected): `enabled`
  (the repo-wide off switch), `cost` / `cost_models` / `tokens` / `interactions`,
  `precision` (clamped 0–8), and `rename.cost`. The CLI maps it to the engine config
  and skips entirely when `enabled = false`. Config-sourced names ride the same
  newline-sanitized write path. 90.5% coverage.

- **Increment 4 — surfacing (`today`).** `aispend today` prints a read-only **pending
  commit** line — the api-equivalent spend the next commit on the current branch would
  be stamped with (the watermark made visible) — via `trailer.Preview`, which reuses
  the hook's exact computation so the preview equals what `git commit` will actually
  stamp. It prints only when something is uncommitted, and stays silent outside a repo
  or on a detached HEAD, so it never adds noise.

- **Increment 5 — live-scan at commit time.** The hook's `pendingUsageLive` runs a
  silent **incremental** scan before pricing, so a commit stamps turns logged since the
  last `aispend scan` — no manual scan needed. `today`'s preview deliberately stays
  store-only (no scan) to remain fast. Best-effort / fail-open: a scan error falls back
  to the existing ledger, never blocking the commit.

- **Increment 6 — TUI receipt badge.** The session receipt renders a trailer line under
  `branch · SHA`: `✓ trailer $0.42 in git · ledger $0.41 · Δ +$0.01`, reading the commit's
  cost trailer via `trailer.ReadCommitCost` (cached on drill-in — no per-render git) and
  reconciling it against the ledger's api-equivalent for that commit. Because the ledger
  hashes repo paths, this resolves only for commits in the current repo (cwd); a session
  from another repo simply shows no badge — honest, not a guess.

- **Increment 7 — TUI trailers editor.** The `t` key in the explorer opens a toggle form
  for the `[trailers]` config (enabled / cost / per-model / tokens / interactions, plus
  precision), persisted to the repo's `.aispend.toml` via `config.SetTrailers` (a
  section writer that preserves all other content). So the install hint "tune them in
  .aispend.toml" no longer means hand-editing TOML.

**Build-out complete** for the local path: installer, engine (trailer/consume/watermark),
`.aispend.toml [trailers]` config, the `today` pending preview, live-scan-at-commit-time,
and the receipt badge — all shipped under t-wada TDD with code + security review, the
offline build and `doctor --network` PASS intact throughout. What remains are the
cross-cutting **open questions** below (multi-repo watermark scoping; trailer-as-ground-
truth in `report --by commit`), not new surface.

## Where this sits

`09` linked sessions to code with three best-effort signals (branch, reconstructed SHA,
churn) and ends on an honest limitation: `event.GitSHA` is **reconstructed at scan time**
from the reflog (`internal/vcs.HeadAt`) and goes empty when the repo is gone, the turn
predates the reflog, or the reflog rotated (90-day default). The commit trailer closes
exactly that gap from the other side — see *The accuracy dividend* below. So this isn't a
detour off the wedge; it's the write-back complement to the read-back linkage `09` already
shipped. claude-budget proves the whole loop is buildable as a single static Go binary with
no runtime deps — the same constraint envelope aispend lives in.

## What it is

A committed-config opt-in. In a repo where you run Claude Code (or Codex), `aispend git
install` drops two thin hooks into `.git/hooks`:

```
git commit
  └─ prepare-commit-msg → aispend trailer "$1" --source "$2"
       · scan the session logs for activity on this branch since the branch watermark
       · dedup, price via the existing engine, sum per model
       · append the configured trailer(s); stage the new watermark
  └─ post-commit        → aispend consume
       · promote the staged watermark so the next commit only counts newer turns
```

Result, in your history:

```
Add the churn heatmap to the receipt

AI-Cost: 0.42
```

There is no daemon and no new data source — it reads the same `~/.claude/projects/*.jsonl`
(and the Codex logs) the scanner already reads, prices them with the same module, and writes
the number into the one place that travels with the work: the commit itself. The shims are
deliberately thin (forward git's message file + `$2` source hint); the binary owns all
routing, exactly as it owns everything else.

## Why it earns a place on the wedge

The wedge is **explainability + arbitrage**, and the moat is *evidence over assertion*. A
trailer is the most portable evidence primitive we have:

- **It survives without the tool.** `--by commit` lives in aispend's ledger; you need
  aispend to see it. A trailer is in the commit message — a teammate reviewing the PR, a
  `git log` six months later, a GitHub blame view all show it with nothing installed. Same
  number, radically wider distribution.
- **It's per-feature cost where decisions happen.** The number shows up in code review,
  next to the diff that incurred it — the moment someone could actually act on "this
  refactor cost $9 in cache-writes."
- **It's still drillable.** The trailer is the headline; aispend stays the receipt. The
  commit SHA is the join key back to the full evidence ledger (the `explain`-grade per-turn
  breakdown now living in the TUI receipt).

This is the same move `08`/`09` made with the opaque event id: a stable, shareable primitive
in the open, the rich receipt one drill away.

## The accuracy dividend (the non-obvious win)

Writing at commit time doesn't just add an output — it **upgrades aispend's own VCS linkage
from best-effort to exact**:

- **Exact SHA, for free.** `09`'s `HeadAt` *guesses* which commit was HEAD at a turn's
  timestamp. The `post-commit` hook doesn't guess — it knows the SHA it just created. For
  any repo with hooks installed, commit↔spend becomes ground truth, not reflog
  reconstruction.
- **Frozen at the source.** Because the ledger hashes paths (`CWDHash`, `SourcePathHash`),
  `09` already insists SHA/churn be frozen at scan time, never recomputed lazily. The trailer
  is the strongest version of that principle: the cost is frozen *in the commit*, immune to
  reflog rotation or a moved repo.
- **A reconciliation anchor.** `report --by commit` can read a present trailer back as
  ground truth and fall back to reflog reconstruction only when it's absent — and the
  `compare` pane (08) gains a third, self-certifying column.

So the feature pays for itself twice: a new portable surface, *and* a correctness upgrade to
linkage we already ship.

## What we reuse vs. what we build

| Layer | Source | Status |
|---|---|---|
| Transcript reader, `(message.id, requestId)` keep-max dedup | aispend `scan` / `normalize` | **Reuse as-is** |
| Cache-tier pricing (5 buckets, 1h tier, LiteLLM overlay + embedded floor) | aispend `internal/pricing` | **Reuse** — fresher & more accurate than claude-budget's embedded card |
| Branch attribution | aispend `internal/vcs` | **Reuse** |
| Per-branch watermark state machine | claude-budget (MIT) | **Port the logic** |
| Hook installer + thin shims (refuse-to-clobber) | claude-budget (MIT) | **Port** |
| Trailer formatter + rebase/squash/amend routing | claude-budget (MIT) | **Port** |

The watermark + rewrite routing is the only hard, stateful, edge-case-ridden part — rebase,
squash, `--amend`, cherry-pick, and detached HEAD each need correct handling or you
double-count or lose usage. claude-budget already solved these *with a real-`git` e2e suite*.
It's MIT: **port the logic, don't re-derive the edge cases** — that's where the bugs live.

## CLI surface (additive)

Per `08`, the TUI is the default channel, but trailers are headless and git-driven — this is
a CLI/hook feature that *surfaces back* into the TUI.

- `aispend git install` / `aispend git uninstall` — install/remove the hook pair in the
  current clone. Reads `core.hooksPath` **first** (Husky v9+ / Lefthook often repoint it away
  from `.git/hooks`, where a naive write would never fire), refuses to clobber a non-aispend
  `prepare-commit-msg` / `post-commit`, and when a hook manager owns the path prints the
  paste-ready wiring for it rather than failing silently — see *Hook managers* below.
- `aispend git status` — report the hook state: `installed` / `managed by <Husky | Lefthook |
  pre-commit> — trailer wiring: detected | NOT detected` / `not installed`. The wired-or-not
  question becomes a visible check, never a silent no-op; `doctor` echoes the same line.
- `aispend trailer <msgfile> --source <s>` / `aispend consume` — **hidden**, hook-invoked
  only; never run by hand. Both **fail-open**: any internal error logs to stderr and the
  commit still succeeds (just without a trailer).
- **Pending view** — extend `aispend today` (or a small `aispend pending`) with an
  *"uncommitted on this branch: $X · N turns"* line: the read-only preview of what the next
  commit will stamp. This is the watermark made visible — the analog of claude-budget's
  `status`.
- **TUI integration** — the session receipt already renders `branch · SHA`. Add a
  `✓ trailer $0.42` badge and a **trailer-vs-ledger reconciliation line**; the receipt is the
  natural home for "this commit's cost is now in git."

## Hook managers & `core.hooksPath`

Native git hooks don't compose — `.git/hooks/prepare-commit-msg` is a single executable, one
owner per event. That's exactly why Husky / Lefthook / pre-commit exist, and it forces three
rules on `install`:

- **Check `core.hooksPath` before writing anything.** Husky v9+ and Lefthook commonly set it
  to a committed dir (`.husky/`), and when it's set git ignores `.git/hooks` outright. A naive
  write there is the worst outcome of all — the hook never fires and `install` *thinks it
  succeeded*, so trailers silently never appear. Read it first; if it points elsewhere, we're
  not even in the right directory.
- **Refuse to clobber — but hand over a snippet, not homework.** Never overwrite a
  `prepare-commit-msg` / `post-commit` we didn't write (doing so would kill the team's
  lint/format checks — a far worse sin than one line of setup). Instead, detect the manager
  (`.husky/`, `lefthook.yml`, `.pre-commit-config.yaml`) and print the **paste-ready** wiring
  for that tool: `aispend trailer "$1" --source "${2:-}"` at `prepare-commit-msg`, `aispend
  consume` at `post-commit`.
- **For Husky it isn't even a clobber.** Husky's own model is "edit the committed `.husky/*`
  scripts," so appending our two lines there (with consent) is the *sanctioned* path, not a
  workaround. Same spirit via Lefthook's `lefthook.yml` and pre-commit's config.

The reframe worth keeping: for a managed repo, wiring into the manager is the **more correct**
integration, not a fallback — a Husky shop wants every hook declared in one place, not one
hiding in `.git/hooks`. The only real failure mode to design out is the *silent* one, which
`aispend git status` / `doctor` close by reporting wiring state explicitly.

## Configuration (one file, not two)

claude-budget ships a separate `.claude-budget.toml`. We already have `.aispend.toml`
(02-data-model) — **fold the trailer config into it**, don't make users manage a second file:

```toml
[trailers]
enabled       = true     # opt-in; OFF by default (mutating commit messages is opinionated)
cost          = true     # AI-Cost:           total USD-equivalent
costModels    = false    # AI-Cost-Models:    per-model
tokens        = false    # AI-Tokens:         per-bucket breakdown (input/output/cache_read/cache_write/cache_write_1h)
interactions  = false    # AI-Interactions:   deduped request count
precision     = 2        # decimals on cost (clamped 0–8)

[trailers.rename]
cost = "AI-Cost"         # cross-provider name (Claude + Codex), not "Claude-Cost"
```

The config is committed and reviewable, so a whole team produces identical trailers; the
hooks (in `.git/hooks`, never committed) are per-clone — `install` once per clone, exactly
like claude-budget's config-vs-hooks split.

## One honesty call: name it for what it is

aispend's whole arbitrage story is that you're on a **subscription** and the API number is
what you'd *otherwise* have paid. A trailer reading `Claude-Cost: 0.42` going into permanent
history risks being read as cash out the door. Two guards:

- **Name it `AI-Cost` / `AI-Cost-Equiv`**, not `Claude-Cost`. Because aispend is
  multi-provider, an `AI-` prefix is also simply more honest than claude-budget's Claude-only
  `Claude-` — a single commit can carry Codex *and* Claude turns.
- **Document the framing once** (config comment + README): the trailer is the
  **api-equivalent** cost view from our six (02-data-model), not billed / credit-consumption.
  A `nil` / absent trailer means "no attributable usage," never an asserted `$0`.

## Two cursors that must not touch

aispend reports on **calendar windows** (`--period`); the trailer path tracks a moving
**per-branch high-water mark** ("since last commit"). They coexist cleanly **only if kept
separate**: the watermark is its own cursor under `.git/` (`.git/aispend-trailer.pending` →
`.git/aispend-trailer`), read and written *only* by `trailer` / `consume`. It must never leak
into the report/period engine, and the report engine must never advance it. Different
questions ("what did this commit cost" vs "what did June cost"), different state, no shared
mutation.

## Security posture (a gate, not an afterthought)

This is the first feature that **installs executable shims, writes into `.git/`, and edits
the commit message file** — a real footgun surface. Copy claude-budget's safe defaults and
add ours:

- **Fail-open, always.** A trailer bug must never block a commit.
- **Refuse-to-clobber — and never silently no-op.** Never overwrite a `prepare-commit-msg` /
  `post-commit` we didn't write. Read `core.hooksPath` first (a manager may repoint it away
  from `.git/hooks`), and when a manager owns the path, print paste-ready wiring and report the
  wired/not-wired state in `git status` / `doctor` rather than appearing to succeed while
  attaching nothing — see *Hook managers & `core.hooksPath`*.
- **No new egress.** `trailer` / `consume` are pure-local reads plus a local write; they must
  stay out of any `net/*`-touching path so the `offline` build and `doctor --network` are
  untouched (`vcs.Numstat`-style isolation behind a hook).
- **Sanitize the write.** Strip newlines from rename values and bound the trailer block; a
  stray newline must not corrupt a commit message (claude-budget already guards this).
- Through the **security-review gate** before merge (03-engineering-process — non-negotiable).

## Acceptance criteria

- [ ] `aispend git install` installs both hooks and refuses to clobber foreign hooks;
      `uninstall` removes only ours, leaving `.aispend.toml` and existing trailers intact.
- [ ] In a repo with `core.hooksPath` set or a hook manager present (Husky / Lefthook /
      pre-commit), `install` writes **no** dead `.git/hooks` file: it prints paste-ready
      wiring, and `aispend git status` / `doctor` report `trailer wiring: detected | NOT
      detected` — no silent no-op.
- [ ] A `git commit` after measurable branch activity appends the configured trailer; a
      commit with no attributable usage gets **no** trailer block (not `AI-Cost: 0.00`).
- [ ] The post-commit watermark advances so the next commit counts only newer turns; a
      cancelled commit carries its usage forward (deferred-truncation).
- [ ] rebase / squash / `--amend` / cherry-pick / detached-HEAD each behave correctly: no
      double-count, no loss, no duplicate block (covered by a real-`git` e2e suite).
- [ ] Trailer cost reconciles to the same turns' `--by commit` figure for that SHA.
- [ ] Money stays integer micros; the trailer renders the **api-equivalent** view, framing
      documented.
- [ ] `trailer` / `consume` fail-open (commit always succeeds) and add **no** network egress;
      `offline` build + `doctor --network` unchanged.
- [ ] New code ≥ 85% coverage; t-wada TDD; code-review + security-review gates pass.

## Phased build

- **Now** — `internal/trailer` (formatter, `.aispend.toml [trailers]` parse) +
  `internal/gitstate` (per-branch watermark, `.git/` read/write); the hidden `trailer` /
  `consume` subcommands; the hook installer (incl. the `core.hooksPath` / manager-detection
  path). Write the failing real-`git` e2e tests **first**
  (RED) for the rewrite cases, then the minimal GREEN. Reuse `internal/pricing` +
  `internal/vcs` untouched (pricing stays pure → order moves no number).
- **Next** — the pending line in `today`; the TUI receipt badge + reconciliation line;
  `report --by commit` prefers a present trailer as ground truth.
- **Later** — the `compare` pane's self-certifying column; a `git log`-style aggregate
  (`report --by commit --source trailer`) that reads cost *purely* from trailers with no
  rescan — the "it works on a fresh clone with no logs" proof.

## Open questions

- **Default off, surely?** Mutating commit messages is opinionated — the proposal is opt-in
  via `[trailers] enabled = true`. Confirm we never write a trailer absent explicit config,
  even after `git install`.
- **Trailers as ground truth in `report`?** When a SHA carries a trailer *and* the logs still
  exist, which wins on conflict — the frozen trailer or a fresh rescan? (Lean: the trailer is
  canonical for that SHA; rescan only fills gaps.)
- **Cross-provider naming.** `AI-Cost` spanning Claude + Codex in one commit is good — but do
  we ever want per-provider lines? Probably config, off by default.
- **Worktrees & monorepos.** One `.git` shared across worktrees, or several repos touched in
  one sitting — does the per-branch watermark hold? (claude-budget assumes one repo per clone;
  verify before porting.)
- **Primary user.** The trailer sings for a **team in code review**; the individual founder
  (08's decided primary user) may not want history noise. Is this really a team-beta feature
  (1B), surfaced now only as the read-only *pending* preview?
