---
description: Security review of the current change-set — aispend's trust checklist (no egress, hashed paths, no secret leakage, dependency purity) plus generic SAST. The fast per-diff checkin gate.
argument-hint: "[optional: a git ref to diff against, e.g. main]"
allowed-tools: Bash(git status:*), Bash(git diff:*), Bash(git log:*), Bash(go list:*), Bash(go build:*), Read, Grep, Glob
---

You are performing the **security-review gate** from `design-documents/03-engineering-process.md` §3.
The product's entire promise is trust, so this checklist is stricter than a generic pass.

## Tooling (layered)

- **This command is the fast, per-diff checkin gate** — it runs on every push (pre-push hook).
  Drive it with Anthropic's **Security Guidance** plugin (`/security-review`) when installed; it
  is diff-scoped and quick. Otherwise apply the checklist below directly.
- **For a deep, whole-repo audit, use `/security-audit`** — the vendored Cloudflare multi-phase
  skill (`.claude/skills/security-audit/`). That one is heavy (parallel agents, six phases) and is
  reserved for **pre-release / on-demand**, not every push. Don't run it here.

Review only the diff (scoping as in `/review`).

## aispend standing checklist (must hold)

1. **No egress in the offline build.** The `//go:build offline` artifact imports no `net` at all.
   Verify: `go list -deps -tags offline ./cmd/aispend` contains no line matching `^net(/|$)`.
   In the default build, `net/*` is reachable **only** through the pricing-refresh package and is
   inbound-only (one GET of a public price file). Flag any *new* importer of `net/*`, any outbound
   POST/PUT, or anything that uploads user data anywhere outside `//go:build cloudyali`.
2. **No raw paths.** No filesystem path is persisted, logged, exported, or rendered — only
   `*_path_hash` (`CWDHash`, `SourcePathHash`). Flag any new field, log line, or JSON key carrying
   a real path.
3. **No secret leakage.** Credentials, tokens, API keys never touch the events DB and never appear
   in logs, errors, or rendered output. Flag anything read from the environment or a config that
   could be echoed.
4. **Terminal-injection safe.** Session-derived strings rendered to a TTY pass through
   `internal/termtext` (CWE-150 escape-injection). Flag any new TTY surface that prints
   session/file/branch strings raw.
5. **Dependency purity.** New dependencies are justified, pinned, vendored, and pure-Go where it
   preserves the single-static-binary promise. Flag anything pulling cgo or a transitive `net`
   transport into the default build.

## Generic SAST (on the diff)

- Injection: command exec (`os/exec`), SQL string-building (prefer parameterized / sqlc), path
  traversal on user-influenced paths, unsafe archive extraction (zip-slip).
- Unsafe deserialization, integer/slice bounds, `unsafe` usage, TOCTOU on file ops.
- Resource handling: files/conns closed, no unbounded reads of untrusted input.
- Permissions: files created with least privilege (no world-writable `0666`/`0777`).

## Output

```
## Security review — <branch>
### Findings           (severity · CWE if known · path:line · why it matters · fix)
###   Critical / High / Medium / Low
### Standing checklist  (each of the 5 items: pass / fail / n-a, one line)
```

End with exactly one line:

`SECURITY_REVIEW_VERDICT: PASS`  — no Critical/High findings and the 5 standing items hold, or
`SECURITY_REVIEW_VERDICT: BLOCK` — otherwise.
