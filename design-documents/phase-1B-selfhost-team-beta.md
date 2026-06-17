# Phase 1B — Self-Host / Team Beta

_Last updated: 2026-06-14 · **Status: Planned.**_
_Companion to: PRD v1.3 §4 (the bridge persona), §6.4, UC-5, §15 (1B), §17.1._

---

## Goal

> **Let a platform/DevEx engineer aggregate a team's AI spend without starting a
> privacy fight — hashed identity, k-anonymity, no per-person scoreboard.**

This is the bridge from solo-developer OSS adoption to the commercial buyer. The
platform/staff engineer already treats team AI spend as a governance problem and
has the credibility to bring it to finance. 1B gives them a self-hostable rollup
that a security reviewer will actually approve.

**Success signal (PRD §15):** platform/DevEx teams deploy without privacy/security blockers.

## Where it sits

- **Assumes:** durable ingestion (1A) and the open, documented `Sink` interface.
- **Unlocks:** the commercial CloudYali reconciliation (Phase 2) — same data shape, managed.

## In scope

- **Self-host collector** that receives `AgentEvent`s from multiple machines (via the open sink interface).
- **Basic team aggregation** by repo / model / tool / `cost_tag`.
- **k-anonymity suppression** (default **N=5**): any group smaller than N is suppressed, not shown.
- **Hashed identity resolution** only via *consented* mapping — never an implicit de-anonymization.
- **Org privacy docs** a security/privacy reviewer can sign off on.

## Out of scope

The managed CloudYali service, invoice/API reconciliation, RBAC/SSO (all Phase 2)
· in-path enforcement · per-person productivity views (forever a non-goal, PRD N1).

## Design spec

The collector is the *same schema* on the receiving end: machines write
`AgentEvent`s through the open `Sink` interface to a self-hosted endpoint instead
of (or in addition to) the local SQLite sink. Aggregation enforces k-anonymity at
query time — a per-repo or per-model rollup suppresses any cohort with fewer than
N contributors, so "team spend" never collapses into "this one person's spend."
Identity stays `IdentityHash` unless an admin and the member both opt into a
mapping. The control hierarchy's **org tier** appears here only as *advisory*
budgets/alerts (PRD §13) — never enforcement.

This phase is gated on a **validation question, not a build question** (PRD
§17.1): confirm whether FinOps/finance or platform/DevEx actually owns
coding-agent spend before over-investing in the rollup. The spec stays modest
until that answer is in.

## Demonstratable output

```console
$ aispend-collector serve --k-anon 5
listening · 9 machines reporting · identity: hashed

# team view (suppressing cohorts < 5)
team-payments   $612/wk   opus 58% · sonnet 31% · cursor 11%
team-search     $403/wk   …
team-infra      —         (suppressed: 3 contributors < k=5)
```

## Acceptance criteria

- [ ] A team rollup shows cost drivers by repo/model/tool with cohorts < N suppressed.
- [ ] No per-person view is possible without explicit consented mapping.
- [ ] A platform team deploys the collector without tripping privacy/security blockers (org privacy docs complete).
- [ ] Identity is hashed end-to-end by default; de-anonymization requires logged consent.

## Test & quality plan

k-anonymity suppression unit-tested at boundaries (N-1 suppressed, N shown);
consent-gated mapping tested to fail closed; the collector ingestion path reuses
the 1A dedup tests. Security review focuses on the multi-machine trust boundary
and the suppression logic. Per [03-engineering-process.md](03-engineering-process.md).

## Risks

Ownership ambiguity (PRD §17.1) — validate before building wide. Surveillance
perception is the killer; lead with the enablement framing and make k-anonymity
visibly the default, not a setting.
