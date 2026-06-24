---
description: The checkin gate — runs the code, security, and YAGNI reviews over the current change-set and prints one consolidated, deduplicated report with a single verdict. Used by the pre-push hook.
argument-hint: "[optional: a git ref to diff against, e.g. main]"
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git log:*), Bash(go vet:*), Bash(go test:*), Bash(go list:*), Bash(go build:*), Bash(gofmt:*), Read, Grep, Glob
---

Run the three review gates over the **current change-set** and produce ONE consolidated report.
This is the gate invoked at checkin (see `.githooks/pre-push`). Scope the diff exactly as `/review`
does (working tree + branch delta vs `origin/main`, or `$ARGUMENTS` if given).

Perform all three passes:

1. **Code review** — the criteria in `/review` (Go correctness, determinism, trust-specific rules).
2. **Security review** — the checklist in `/security-review` (egress, hashed paths, secrets,
   termtext, dependency purity, SAST).
3. **YAGNI review** — the criteria in `/yagni-review` (speculation, premature abstraction).

Then merge the results: deduplicate overlapping findings, and sort by severity so the most
important thing is first.

## Output (keep it tight — this is read in a terminal)

```
## Checkin review — <branch> · <n> files · <commit range>

### Must fix before push        (blockers from any gate)
- [code|sec|yagni] path:line — problem → fix

### Should fix                  (non-blocking but real)
- ...

### Notes                       (nits, things done well — 1–5 lines max)

### Gate verdicts
- code:     PASS | BLOCK
- security: PASS | BLOCK
- yagni:    PASS | BLOCK
```

End with exactly one machine-readable line (the hook greps for it):

`CHECKIN_VERDICT: PASS`  — if all three gates PASS, or
`CHECKIN_VERDICT: BLOCK` — if any gate is BLOCK.
