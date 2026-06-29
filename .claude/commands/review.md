---
description: Code review of the current change-set against the aispend engineering bar (Go error handling, concurrency, interfaces, table-tests, SQL/N+1, coverage gaps).
argument-hint: "[optional: a git ref to diff against, e.g. main — defaults to the working tree + branch delta]"
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git log:*), Bash(go vet:*), Bash(go test:*), Bash(gofmt:*), Read, Grep, Glob
---

You are performing the **code-review gate** described in `docs/review-automation.md`.
It must be clean (or carry a logged, accepted exception) before a change is "done".

If the `technical-code-reviewing` skill is available in this session, use it — it is
Go/React/SQL/Docker aware. Otherwise apply the criteria below directly.

## Scope the diff

Review only what changed in this change-set, not the whole repo:

1. `git status` and `git diff` for uncommitted work.
2. If `$ARGUMENTS` names a ref, also diff `git diff $ARGUMENTS...HEAD`. Otherwise diff the
   branch against `origin/main` (fall back to `main`):
   `git diff "$(git merge-base HEAD origin/main 2>/dev/null || echo main)"...HEAD`.
3. Read the full surrounding context of each changed function — never review a hunk in isolation.

## What to check (this repo)

**Go correctness**
- Error handling: every error checked, wrapped with `%w` where it crosses a boundary, never
  silently swallowed. No `_ =` on a meaningful error.
- Concurrency: goroutine lifetimes bounded, no data races on shared maps/slices, channels closed
  by the owner, `context` honored.
- Interfaces & seams: small interfaces, accept-interfaces / return-structs. The `Store` interface
  (in-memory + SQLite satisfying one suite) is a deliberate seam — preserve it.
- Numbers are the product: any path that produces a cost/price/token figure a user can drill into
  must be exercised by a test. Flag new money/pricing/normalize branches that ship without a
  pinning test.
- Determinism: rendered output is byte-stable (golden fixtures). Flag map-iteration order leaking
  into output, unsorted slices, or `time.Now()`/locale creeping into rendered surfaces (UTC
  end-to-end; local only at the render boundary).
- Table tests: new behavior covered by table-driven cases including the nil-cost-view and
  low-confidence paths.

**Trust-specific (aispend)**
- No raw filesystem paths persisted, logged, or rendered — only `*_path_hash`. Session-derived
  strings rendered to a TTY go through `internal/termtext` (escape-injection / CWE-150).
- The `offline` build must keep compiling; nothing network-adjacent leaks into the default read path.

**Style**
- `gofmt`-clean, `go vet`-clean. Names say the behavior. Comments explain *why*, not *what*.

## Output

Group findings by severity. For each: `path:line` · one-line problem · concrete fix (a diff
sketch where it helps).

```
## Code review — <branch> (<n> files)
### Blockers      (must fix before merge)
### Should-fix    (fix now or file a tracked follow-up)
### Nits          (optional)
### Done well     (1–3 things, briefly)
```

Be specific and kind. If there are no blockers, say so plainly. End with exactly one line:

`CODE_REVIEW_VERDICT: PASS`  — if there are no blockers, or
`CODE_REVIEW_VERDICT: BLOCK` — if there is at least one blocker.
