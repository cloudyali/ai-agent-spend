# Phase 1A — Durable Ingestion

_Last updated: 2026-06-14 · **Status: Planned.**_
_Companion to: PRD v1.3 §8.2, §8.4, §15 (1A), §17.4._

---

## Goal

> **Make ingestion trustworthy in someone else's environment — not just on the
> author's laptop.**

File parsing is the wedge, but it is also the most fragile source. 1A adds the
*durable* paths — first-class OTel and official/admin APIs where they exist — and
formalizes the fixture/contribution process, so a design partner can trust the
collector inside their own internal environment.

**Success signal (PRD §15):** design partners trust the collector in internal environments.

## Where it sits

- **Assumes:** the 0A/0B `Provider` adapter layer, the `AgentEvent` schema, and the OS-aware path layer ([04-platform-and-paths.md](04-platform-and-paths.md)).
- **Unlocks:** the self-host/team path (1B), because durable ingestion is what makes a multi-machine rollup believable.

## In scope

- **Claude Code OTel** ingestion, first-class (same `AgentEvent` out as the file parser).
- **Copilot OTel** ingestion — and this is where Copilot's *real* token data lives.
  **[verified]** Copilot's session log (`~/.copilot/session-state/<id>/events.jsonl`)
  is **output-only** (CodeBurn reads `assistant.message.outputTokens` and hardcodes
  input to 0); the full input/output/cache/reasoning split exists **only** via the
  opt-in **OTEL file exporter** (`~/.copilot/otel/*.jsonl`, path overridable with
  `COPILOT_OTEL_FILE_EXPORTER_PATH`), which ccusage reads under GenAI
  semantic-convention attributes and dedupes across its four record sources. So:
  prefer the OTEL export; fall back to the output-only session log (clearly flagged)
  when the exporter is off; and decide product behavior when `~/.copilot/otel/` is
  empty (prompt the user to enable it, or accept output-only).
- **Copilot bills in premium requests, not tokens. [verified]** Even a perfect
  token breakdown won't reproduce the invoice — Copilot meters **premium requests**
  (quota units with per-model multipliers). Model that as a `credit_consumption` /
  quota view; the token cost is a *shadow* number, labeled as such.
- **Official/admin APIs** where available, behind the `Provider` adapter.
- **Source reconciliation:** when file, OTel, and API describe the same event, pick via `dedupe_key` + confidence rather than double-counting.
- **Fixture suite + parser-contribution guide** so external contributors can add a provider.

## Out of scope

Team aggregation (1B) · CloudYali reconciliation (2) · enforcement.

## Design spec

The OTel path is an *ingestion adapter* that emits the same `AgentEvent`, so
every downstream surface is unchanged — only the source provenance differs
(`source_type="otel"` vs `"local_file"`). Admin-API pollers are `Provider`
implementations that happen to read a network API instead of a file; they are the
first network-capable code in the tree and therefore live **only** behind their
own build consideration and are never in the default offline binary's required
path (the offline guarantee from 0A is preserved — a poller is opt-in
configuration, and `doctor --network` reports it honestly).

Because two sources can describe one event, the reconciliation rule is explicit:
equal `dedupe_key` ⇒ keep the higher-confidence/`cost_method` precedence record,
record the other as corroboration. This is the local rehearsal for the Phase 2
server-side dedup.

## Demonstratable output

```console
$ aispend scan --source otel
Claude Code OTel endpoint detected · 1,204 spans → 1,204 events
Cross-checked against local files: 1,198 matched (dedup_key), 6 OTel-only kept
0 double-counted · confidence picks logged

$ aispend sources
claude_code: local_file (parser v1, conf 0.95) · otel (conf 0.97, preferred)
```

## Acceptance criteria

- [ ] The OTel path yields events matching the file path within a stated tolerance on the same sessions.
- [ ] Admin-API pollers sit behind the adapter; the default offline binary is unaffected and `doctor --network` reports any configured poller.
- [ ] No event is double-counted when two sources describe it (dedup verified by test).
- [ ] An external developer can add a provider using the contribution guide + fixtures.

## Test & quality plan

OTel ingestion tested against recorded span fixtures → golden `AgentEvent`s; a
dedup test asserts equal `dedupe_key` collapses to one event with the correct
confidence precedence; the contribution guide ships with a runnable example
provider + fixtures. Reviews per [03-engineering-process.md](03-engineering-process.md).

## Risks

Admin APIs vary by vendor and are young (PRD §17.4); OTel semantic conventions
for GenAI are still evolving — keep both behind adapters so a change is a
localized edit. Introducing the first network code is the moment to re-verify the
offline guarantee holds for the *default* build.
