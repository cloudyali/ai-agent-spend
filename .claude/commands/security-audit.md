---
description: Deep, multi-phase security audit of the whole codebase using the vendored Cloudflare security-audit skill (recon → hunt → validate → report → structured output → independent verification). Heavy — run pre-release or on demand, NOT on every push.
argument-hint: "[optional: a path/subsystem to focus, e.g. internal/pricing/refresh]"
allowed-tools: Task, Read, Grep, Glob, Bash(node:*), Bash(ls:*), Bash(git log:*), Bash(git diff:*), Bash(go list:*)
---

Run a **deep security audit** using the vendored Cloudflare `security-audit` skill at
`.claude/skills/security-audit/SKILL.md`. Read that SKILL.md first and follow all six phases
exactly (recon → hunt → validate → report → structured output → independent verification),
launching the parallel sub-agents it describes via the Task tool.

This is the heavy, whole-repo audit — distinct from the fast per-diff `/security-review` checkin
gate. Run it **pre-release** (see `RELEASE_CHECKLIST.md`) or **on demand**. It is intentionally NOT
wired into the per-push hook.

Target scope: `$ARGUMENTS` (default: the whole repo at the current working directory).

Make sure the audit explicitly covers aispend's trust-critical promises (the product IS trust):
- **No egress** — `net/*` is reachable only through `internal/pricing/refresh`; the `//go:build
  offline` artifact imports no `net` at all. Flag any other importer or any outbound write.
- **Path privacy** — only `*_path_hash` (`CWDHash`, `SourcePathHash`) is ever persisted, logged,
  exported, or rendered — never a raw filesystem path.
- **Terminal-injection** — session-derived strings rendered to a TTY pass through
  `internal/termtext` (CWE-150). Flag any raw TTY surface.
- **Dependency purity** — pure-Go, vendored, single static binary; no cgo or transitive net
  transport leaking into the default build.

Write all artifacts under `~/security-audit-skill/ai-agent-spend/run-<N>/` (`REPORT.md`,
`FINDINGS-DETAIL.md`, `findings.json`) and validate the JSON with:

```
node .claude/skills/security-audit/validate-findings.cjs ~/security-audit-skill/ai-agent-spend/run-<N>/findings.json
```

End with a short summary: each confirmed finding as `severity · title · one line`, or an honest
"no exploitable vulnerabilities found".
