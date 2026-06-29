# Review & checkin automation

How aispend keeps its three non-negotiables — **t-wada TDD**, the **85–90% coverage floor**, and
**code + security + YAGNI review** — enforced consistently, without putting a Claude API key
anywhere in CI.

## The shape

Two layers, split by one rule: *anything that needs a model runs locally; anything deterministic
runs in CI.*

```
                         ┌────────────────────────────── you, at checkin ──────────────────────────────┐
   git commit  ─────────▶│  .githooks/pre-commit   gofmt (staged Go files)                              │
                         │                                                                              │
   git push    ─────────▶│  .githooks/pre-push                                                          │
                         │     deterministic gate:  gofmt · vet · go test + coverage floor ·            │
                         │                          build both SKUs · offline net-free                  │
                         │     AI review (local Claude Code, no key):  /checkin-review                  │
                         │            = /review  +  /security-review  +  /yagni-review                  │
                         └──────────────────────────────────────────────────────────────────────────────┘
                                              │ push
                                              ▼
   GitHub Actions ───────▶  .github/workflows/ci.yml   (secret-free, every push/PR)
                              gofmt · vet · go test + coverage floor (85%, no exemptions) ·
                              both SKUs build · offline build is net-free · shellcheck
```

The review *logic* is version-controlled (`.claude/commands/*.md`), so it is identical whether you
run it by hand (`/review`), the hook runs it (`/checkin-review`), or a teammate runs it on their
machine. That is the "skills available in the repository, invoked consistently" part.

## The pieces

| File | Role |
|---|---|
| `.claude/commands/review.md` | Code review — Go correctness, determinism, trust-specific rules. |
| `.claude/commands/security-review.md` | Security review — egress, hashed paths, secrets, termtext, deps, SAST. |
| `.claude/commands/yagni-review.md` | YAGNI — speculation, premature abstraction (respects deliberate seams). |
| `.claude/commands/checkin-review.md` | Orchestrator — runs all three, one `CHECKIN_VERDICT:` line. |
| `.claude/settings.json` | Read-only tool allow-list so headless review runs don't hang on prompts. |
| `scripts/coverage-gate.sh` | The 85% per-package floor, no exemptions. Used by the hook *and* CI. |
| `scripts/coverage-gate_test.sh` | Pins the gate's own behavior (t-wada). Run: `bash scripts/coverage-gate_test.sh`. |
| `.githooks/pre-commit`, `.githooks/pre-push` | Invoke the gates at checkin. Enable: `bash scripts/setup-hooks.sh`. |
| `.github/workflows/ci.yml` | The deterministic backstop on every push/PR. |
| `.claude/commands/security-audit.md` | The deep, whole-repo audit command (pre-release / on-demand). |
| `.claude/skills/security-audit/` | Vendored Cloudflare multi-phase security-audit skill (MIT). |
| `.claude/skills/ponytail-review/` | Vendored ponytail-review skill (MIT) — backs `/yagni-review`. |
| `.claude/skills/ponytail/` | Vendored ponytail skill (MIT) — the dev-time YAGNI reflex. |

## YAGNI review — backed by ponytail

`/yagni-review` runs the vendored **`ponytail-review`** skill (`.claude/skills/ponytail-review/`):
over-engineering only, one terse line per cut (`delete:` / `stdlib:` / `native:` / `yagni:` /
`shrink:`), ending in `net: -<N> lines possible`. It runs in the `pre-push` hook with the code and
security reviews, and respects aispend's deliberate seams (the `Store` interface, the injected
fetch/VCS hooks, the two build SKUs) so they're never flagged. The command maps the findings to the
`YAGNI_REVIEW_VERDICT:` line the checkin gate reads.

Its sibling **`ponytail`** (`.claude/skills/ponytail/`) is the *write-time* reflex — the "laziest
solution that works" ladder (stdlib → native → existing dep → one line). Reach for it while coding;
`ponytail-review` then checks the result.

## Security review — two layers

Security gets two distinct tools, by depth:

1. **Fast checkin gate — every push.** `/security-review` (this repo's command), driven by
   Anthropic's **Security Guidance** plugin. Diff-scoped, quick, runs in the `pre-push` hook
   alongside the code and YAGNI reviews. It enforces aispend's standing checklist (no egress,
   hashed paths, no secret leakage, termtext, dependency purity) plus generic SAST on the diff.
2. **Deep audit — pre-release / on-demand.** `/security-audit`, backed by the **vendored
   Cloudflare `security-audit` skill** (`.claude/skills/security-audit/`). A six-phase, multi-agent
   audit of the *whole* repo (recon → hunt → validate → report → structured output → independent
   verification) that emits machine-readable `findings.json`, validated by the skill's
   zero-dependency `validate-findings.cjs`. **Not** a per-push gate — it's heavy. Wired into
   `RELEASE_CHECKLIST.md` (§3b) and runnable any time.

The Cloudflare skill (Layer 2) is **vendored** (pinned in-repo, MIT) so the deep audit is consistent
for every contributor with no per-machine install — see `.claude/skills/security-audit/VENDOR.md`.

The Security Guidance plugin (Layer 1) is **not vendored — install it once via `/plugins`.** It lives
in `anthropics/claude-code` under **"© Anthropic PBC. All rights reserved."** (Commercial ToS, *not*
an OSS license), so redistributing its files into this repo isn't permitted — unlike the MIT-licensed
Cloudflare/ponytail skills we vendor. It's also a hook-driven plugin (edit-time pattern checks +
commit-time model review through Claude Code's plugin runtime), so copied files wouldn't fire its
hooks anyway. It's free on all plans and actively maintained, so the marketplace install is the
correct, always-fresh path. `/security-review` references it and degrades gracefully when it's absent.

## Day-to-day

```sh
bash scripts/setup-hooks.sh          # once per clone

git commit -m "feat(pricing): ..."   # pre-commit: gofmt
git push                             # pre-push: full gate + /checkin-review

# Run a single gate by hand inside Claude Code:
/review
/security-review
/yagni-review
/checkin-review

# Escape hatches (use sparingly):
AISPEND_SKIP_HOOKS=1 git push            # skip everything
AISPEND_SKIP_AI_REVIEW=1 git push        # deterministic gate only
AISPEND_AI_REVIEW_BLOCKING=1 git push    # make a BLOCK verdict actually stop the push
```

The AI review is **advisory by default**: it always runs and writes its report to
`.git/aispend-checkin-review.md`, but a BLOCK verdict won't stop the push unless you opt in with
`AISPEND_AI_REVIEW_BLOCKING=1`. Model output is non-deterministic; the deterministic gate is what
hard-blocks. If `claude` isn't on `PATH`, the AI step is skipped and the rest still runs.

## Coverage policy (block, no exemptions)

`scripts/coverage-gate.sh` fails if **any** package with statements is below **85%**. Packages with
no test files / no statements are surfaced as warnings (pure scaffolding is exempt "until it gains
logic", per [`CONTRIBUTING.md`](../CONTRIBUTING.md)) — never silently passed.

> **Heads-up:** per the project's own working notes, `internal/cli` sits around **83–84%**
> (TTY/network-bound entrypoints). With *no exemptions*, this gate will fail CI until that package
> reaches 85% — that is the intended "hold the line" behavior you chose. Two honest ways to stage it:
>
> - Raise `internal/cli` coverage to 85% (preferred), or
> - Temporarily run the floor lower for that package via an explicit, commented allow-list. To add
>   one, change the gate's policy block — e.g. keep `COVERAGE_FLOOR=85` but skip a named package with
>   a tracked TODO. (The gate is deliberately exemption-free today; an allow-list is a one-function
>   change if you decide to phase it in.)

Raise the bar repo-wide any time with `COVERAGE_FLOOR=90 bash scripts/coverage-gate.sh`.

## Why the AI reviews are not in CI

A deliberate choice, not a limitation:

1. **No secret in CI.** Running Claude in GitHub Actions means a long-lived `ANTHROPIC_API_KEY` or
   OAuth token sits in repo secrets. For a trust-first, provably-offline tool, the cleanest posture
   is *no model credential in the build system at all.*
2. **Prompt-injection surface.** Anthropic's own security-review action is explicit that it "is not
   hardened against prompt injection and should only be used to review trusted PRs." Keeping AI
   review on the contributor's machine (reviewing their own diff) sidesteps untrusted-PR risk.
3. **Cost control.** Per-PR model spend is opt-in per contributor, not a standing org bill — fitting
   for the product this repo *is*.

## Optional: server-side AI review later

If you ever want AI review to run in CI as well, do it without a raw API key:

- **Claude Code GitHub App** — run `/install-github-app` from Claude Code; it wires the app and uses
  an OAuth token (`CLAUDE_CODE_OAUTH_TOKEN` from `claude setup-token` for Pro/Max), driving the same
  `.claude/commands/*` you already have via `anthropics/claude-code-action@v1`.
- **Dedicated security SAST** — `anthropics/claude-code-security-review@main` posts inline
  vulnerability comments on PR diffs.

Either can reuse the repo commands verbatim, so the local and CI definitions never drift. Until then,
the local hooks are the source of truth and CI holds the deterministic line.

### References

- Claude Code GitHub Actions — setup: <https://github.com/anthropics/claude-code-action/blob/main/docs/setup.md>
- Claude Code GitHub Actions — docs: <https://docs.anthropic.com/en/docs/claude-code/github-actions>
- Security review action: <https://github.com/anthropics/claude-code-security-review>
