# Phase 3 — Optimization & Control Handoff

_Last updated: 2026-06-14 · **Status: Planned.** The endgame._
_Companion to: PRD v1.3 §13, §14, §15 (Phase 3), §17.7._

---

## Goal

> **Turn diagnosis into a fix in whatever enforcement stack the team already runs
> — be the neutral FinOps brain, never the plumbing — so CloudYali becomes the
> system of record for AI-engineering spend.**

We own the *brain* of control (detection, budgets, advisory guidance) and
deliberately not the *plumbing*. We never build a proxy, gateway, or harness;
we hand the fix to whatever the team already uses to act. This neutrality —
"Switzerland" — is itself a moat: no pure tracker does the handoff, and no
enforcement layer does the cross-tool FinOps brain.

**Success signal (PRD §15):** CloudYali becomes the system of record for AI engineering spend.

## Where it sits

- **Assumes:** the Phase 2 reconciliation, cost-center allocation, and anomaly detection.
- **Unlocks:** the closed loop — detect → recommend → (team enforces) → re-measure.

## In scope

- **Recommend-to integrations:** emit the fix in the target's own language —
  a LiteLLM routing rule, a Claude Code hook, an OpenRouter/Helicone config, or a policy-engine rule.
- **Showback / chargeback**, finalized.
- **Broader cloud-bill reconciliation** (the wider FinOps surface, where incumbents are strong — entered last, from strength).

## Out of scope

Building any in-path component — proxy, gateway, harness — **ever** (PRD N6, §13).

## Design spec

A detected cost driver ("Opus on trivial turns cost $340 last month") flows into
a **recommendation emitter** that renders an *apply-ready artifact* per
integration target — config or policy the team applies themselves. We never sit
in the request path; the artifact is the handoff. Emitters live behind adapters
(like the ingestion pollers) because integration surfaces evolve (PRD §17.7).

The advisory-vs-enforced boundary stays honest: for a solo dev with no gateway,
control is alerts and recommendations; real enforcement appears only when a team
runs an enforcement layer we integrate with — which is exactly where CloudYali
monetizes, so the boundary aligns with the business model instead of fighting it.

## Demonstratable output

```console
# From a detected driver, emit the fix in the team's language:
$ cloudyali recommend export --driver opus-on-trivial --target litellm
# → litellm router rule: route turns < 2k tokens off opus to haiku
$ cloudyali recommend export --driver opus-on-trivial --target claude-code-hook
# → a PreToolUse hook that warns when opus is selected for a trivial turn
```

Each artifact is the *diagnosis made actionable* — and is itself traceable back
to the `explain`-able events that justified it.

## Acceptance criteria

- [ ] A cost driver produces a valid, apply-ready artifact for ≥2 enforcement targets.
- [ ] We ship **no** in-path component; every fix is a handoff the team applies.
- [ ] Chargeback exports reconcile against the Phase 2 ledger.
- [ ] Design partners reference CloudYali as their system of record for AI-eng spend.

## Test & quality plan

Each emitter is tested by asserting the generated artifact is valid for its target
(schema/lint of the rule/hook/policy) and that it traces back to the events that
produced the recommendation. Adapters get contract tests so a target's format
change is a localized failure. Per [03-engineering-process.md](03-engineering-process.md).

## Risks

Integration surfaces (gateways, harnesses, policy engines) evolve — keep
recommend-to behind adapters (PRD §17.7). The discipline risk is scope creep
*into* the path under pressure to "just enforce it"; the answer is always to
recommend, never to proxy.
