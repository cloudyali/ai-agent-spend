# Phase 2 — CloudYali Reconciliation Beta

_Last updated: 2026-06-14 · **Status: Planned.** The commercial turn._
_Companion to: PRD v1.3 §6.4, §11.4, §12, §14, §15 (Phase 2), §17.2._

---

## Goal

> **Reconcile the first-party coding-agent ledger with provider APIs, invoices,
> seats, and credits — deduped and allocated — and answer the question almost no
> org can: what did we buy, what was consumed, who or what caused it, how
> confident are we, and what should change?**

This is where the local ledger becomes a FinOps platform. The two blind pools —
per-seat coding-agent spend and server-side API spend — are joined *here, upstream
and correctly*, with identity resolution and dedup, **not** via a naive local
correlation that could double-count or misattribute (PRD §12).

**Success signal (PRD §15):** first paid design partners reconcile coding-agent + API spend.

## Where it sits

- **Assumes:** durable ingestion (1A), the self-host/team rollup and k-anonymity (1B), the `Sink` seam, and the evidence ledger's reconciliation fields (`dedupe_key`, `reconciliation_status`, `invoice_reference`).
- **Unlocks:** the optimization/control handoff (Phase 3) and CloudYali as system of record.

## In scope

- **CloudYali sync** — the cloud sink, finally real, behind `//go:build cloudyali` (absent from the OSS binary).
- **Consent UX** (PRD §6.4): a concrete first-connect diff of exactly what will and won't sync, before any upload.
- **Ingestion + dedup** server-side (so Bedrock/Vertex usage already in the cloud bill isn't double-counted).
- **Identity resolution** (hashed → people, only via consented mapping) and **attribution** → team/project/cost-center.
- **Invoice / API / local reconciliation**, including the two-surface picture (per-seat coding + server-side API).
- **Unified per-seat + API pane**, budgets, anomaly alerts, **RBAC/SSO**, **FOCUS export**.

## Out of scope

Broader cloud-bill unification (Phase 3) · in-path enforcement (never).

## Design spec

The cloud sink that has been a documented stub since 0A becomes an
implementation. Crucially, this changes **nothing** in the default OSS build —
it is a separate target, and the OSS no-egress guarantee is unaffected and still
CI-asserted. Every upload is gated by the consent UX: the user sees a concrete
diff ("daily spend totals by provider" will sync; "prompts, code, file contents"
will not), confirms, and can disconnect + purge anytime.

Server-side, reconciliation is the product: join the local first-party evidence
with provider usage/cost APIs (Anthropic, OpenAI), in-cloud AI from the cloud bill
(deduped via `dedupe_key`/`reconciliation_status`), seats, and credits — then
allocate to cost centers via `CostTag`. The two-surface unification (PRD §12)
happens at this tier because only here is the identity/dedup context sufficient to
do it without double-counting. We *read* every source; we never proxy.

## Demonstratable output

A mocked-then-real reconciliation pane (validate as a mock first, PRD §17.1):

```text
Acme Eng · AI spend · May 2026 · reconciled
  Coding agents (per-seat + metered)        $ 14,220   ← first-party local evidence
  Server-side API (Anthropic + OpenAI)      $ 31,540   ← usage APIs
  In-cloud AI (Bedrock/Vertex)              $  8,900   ← from cloud bill, DEDUPED (not added twice)
  ----------------------------------------------------
  Total AI-in-engineering                   $ 54,660
  Allocated: team-payments 38% · team-search 24% · …
  Confidence: 0.93 (3 line items flagged for review)  ⚠ 2 inactive paid seats found
```

## Acceptance criteria

- [ ] First paid design partners reconcile coding-agent + API spend in one pane.
- [ ] In-cloud AI already on the cloud bill is **not double-counted** (dedup verified).
- [ ] Every upload is preceded by the consent diff; disconnect + purge works.
- [ ] Allocation to cost centers via `CostTag`; RBAC/SSO enforced; FOCUS export validates.
- [ ] The honesty guardrail holds: zero spend/identifiable data leaves a machine without logged consent.

## Test & quality plan

Dedup/reconciliation is the whole value, so it gets the deepest test investment:
synthetic ledgers where local + API + invoice overlap, asserting no double-count
and correct allocation. Consent-gating tested to fail closed. The cloud build is
tested *separately*; the default build's no-egress test stays green. Security
review is heavyweight here (egress, auth, RBAC, residency). Per
[03-engineering-process.md](03-engineering-process.md).

## Risks

Incumbent descent — Finout/Vantage productizing "FinOps for AI agents" proves
demand and means head-to-head comparison (PRD §17.2); win on developer trust +
the two-surface narrative + reconciliation fidelity. The guardrail metric (zero
un-consented egress) is existential — a single violation evaporates the trust the
OSS wedge spent a year earning.
