# 05 — The LLM Pricing Module

_Last updated: 2026-06-14 · Stable design, phased build. The engine behind every
cost view — so its fidelity is the product's fidelity._

Pricing is not a helper in AgentSpend; it is the machine that turns raw tokens
into a defensible number. Every `cost_view` and every confidence score is its
output, and "the tool whose numbers you can trust" lives or dies here. This doc
designs the module as a first-class subsystem. The Phase 0A code (`internal/pricing`)
is the **minimum viable slice** of it; this is the target it grows into.

---

## 1. What "a pricing module" actually has to handle

LLM billing is deceptively spiky. A flat `tokens × rate` covers maybe 60% of
reality. The rest is where wrong numbers come from:

- **Many providers and models**, renamed and re-snapshotted constantly
  (`claude-opus-4-20250514`, `gpt-5-2026-04-01`, …). Needs canonicalization + aliases.
- **Cache pricing with structure.** Anthropic prompt caching has a *cache-read*
  rate (0.1× input) **and two cache-write rates** by TTL: **1.25× input** for the
  5-minute (default) tier and **2× input** for the 1-hour (extended) tier, with the
  TTL refreshed on every cache read. **[implemented 2026-06-16]** The Claude
  normalizer reads the per-TTL split (`cache_creation.ephemeral_5m/1h_input_tokens`),
  `event.Tokens.CacheWrite1h` carries the 1-hour subset, and the engine prices it at
  `2× input` (`oneHourCacheInputMultiple`). Other providers differ and are handled
  accordingly: **OpenAI** charges *nothing* for cache writes (read ≈ 0.5× input, TTL
  automatic 5–10 min, no user tier) — `cache_write_per_mtok: 0` for codex, so the
  1-hour multiplier never applies; **Gemini** (not yet ingested) bills cache by
  *storage time* (per-token-hour, default 1h TTL), a different cost dimension. See §6.
- **Batch / off-peak discounts** (e.g. Anthropic/OpenAI batch ≈ 50% off).
- **Context-length tiers.** Some models charge more above a prompt-token threshold
  (long-context surcharge). One model can have two rate regimes. **[verified]**
  ccusage encodes this concretely as a **200k tier** (`input_above_200k` and
  matching cache rates) that kicks in once input + cache-read crosses 200k tokens —
  exactly the regime long agent sessions live in, so ignoring it mis-prices the
  bulk of real spend.
- **Speed/"fast" tier.** **[verified]** ccusage prices a fast variant by suffixing
  the model id with `-fast` and applying a higher multiplier. If the source marks a
  turn as fast and we price it at the base rate, we under-report it.
- **Reasoning tokens** (o-series / thinking) billed as output; sometimes separately surfaced.
- **Billing kinds beyond metered API:** flat subscriptions (Claude Max/Team),
  token-credits (Codex), usage-based credits (Copilot), included quotas with overage.
- **Currency, taxes, enterprise discounts, regional pricing** — mostly a Phase 2
  reconciliation concern, but the schema must not preclude them.

> The numbers above are *structural ratios*, stated illustratively. Exact
> per-token prices live only in versioned data files (§4) and must be verified
> against each vendor's live price list before a release — never hard-coded into
> prose or logic.

## 2. Pricing data model (the target schema)

Today's `rate` is the flat subset of this. The target, additive, schema:

```go
// A Model's price, in micro-USD per 1,000,000 tokens unless noted.
type Rate struct {
    InputPerMTok        int64
    OutputPerMTok       int64
    CacheReadPerMTok    int64
    CacheWrite5mPerMTok int64   // Anthropic 5-minute TTL
    CacheWrite1hPerMTok int64   // Anthropic 1-hour TTL
    BatchMultiplier     float64 // 0.5 = 50% off; 0 → treat as 1.0
    ContextTiers        []ContextTier // optional rate regimes past a prompt-size threshold
    WebSearchPer1KCalls int64         // server-side tool use (e.g. web search) billed separately — CodeBurn prices this
}

type ContextTier struct {
    MinPromptTokens int64 // applies when input+cache_read tokens exceed this
    Rate            Rate  // overriding rates (ContextTiers ignored within a tier)
}

type Model struct {
    ID      string   // canonical, e.g. "claude-opus-4"
    Aliases []string // explicit renames; snapshot dates handled by regex
    Kind    string   // "chat" | "reasoning"
    Rate    Rate
}

type Table struct {
    Provider  string            // "anthropic" | "openai" | "cursor" | "github_copilot" | ...
    Version   string            // "anthropic-2026-05"
    Currency  string            // "USD"
    ValidFrom string            // ISO date the prices took effect
    ValidTo   string            // "" = current
    Source    string            // URL/citation the rates were taken from
    Models    map[string]Model
}
```

Subscriptions/credits stay on `pricing.Plan` (already present): `Kind`
(`api|subscription|credit`), `MonthlyFee`, `Included` quota, and a future
`CreditRate` for credit→USD conversion.

## 3. Engine responsibilities

The engine is a pure function of `(AgentEvent, Plan, Table, clock)` →
filled `CostViews` + pricing `Evidence`. Steps:

1. **Resolve the model** — raw id → canonical via alias map + snapshot-date strip.
2. **Select the rate** — by `priced_at` ∈ `[ValidFrom,ValidTo)` and by context
   tier (prompt size vs `MinPromptTokens`).
3. **Compute each view** — api-equivalent (always), estimated (flagged),
   marginal (vs `Included`), effective-allocated (subscription amortization, at
   the aggregation step), credit-consumption (credit kinds), and **reported**
   when the source carried its own cost. The **reported-else-computed precedence**
   is ccusage's three modes made concrete: `Display` (reported only), `Calculate`
   (always compute), `Auto` (reported if present and non-zero, else compute — the
   sane default). We keep both numbers — reported *and* api-equivalent — each
   provenance-tagged, and let `cost_method=reported` mark which is authoritative,
   rather than silently blending them. CodeBurn does the same per provider
   (`if cost > 0 use it, else recompute`).
4. **Stamp provenance** — `PricingTableVersion`, `PricedAt`, `CostMethod`,
   `ConfidenceScore`, `ConfidenceReason`, `KnownMissingFields` — even when the
   model is unknown (price nothing, explain why). This is already how 0A behaves.

Integer micro-USD throughout; round once at render (see [02-data-model.md](02-data-model.md)).

## 4. Freshness & sourcing — ship embedded, refresh from our endpoint

Prices change often, and the trackers show the two failure modes to avoid:
*embed-only* goes stale (ccusage repeatedly ships fixes for missing new model
snapshots, e.g. `claude-opus-4-6`/`-4-7`), while *fetch-only-from-a-third-party*
is fragile (the LiteLLM "invalid cost map on main" incident broke everyone's
costs at once). So AgentSpend does **both**, in a strict precedence that **always
degrades to an embedded floor** and never blocks on the network:

**[implemented 2026-06-16 — bootstrap; endpoint moved to our host 2026-06-17]**
Steps 1 and 3 are wired: pricing is offline-first via `internal/pricing/refresh`
(`ReadFreshCache`/`WriteCache`, `pricing.ParseLiteLLM`, `pricing.NewEngineWithRates`
overlay) and the CLI command `aispend pricing [refresh]`. `refresh` targets the
**AgentSpend-hosted price mirror** at `refresh.LiteLLMURL` —
`https://agentspend.cloudyali.io/pricing/litellm.json`, a host we control and
**host-pin** (the client refuses any cross-host redirect), serving a copy of the
upstream LiteLLM JSON schema. This already satisfies the "the laptop only ever talks
to a host we control" posture; what remains versus the full step-2 vision below is
the signed `index.json` + per-table checksums and the per-provider path layout.
`scan`/`report`/`explain` price through the cache overlay; `doctor --network`
discloses the one inbound fetch; the `offline` build compiles out all `net/*`.
LiteLLM omissions (e.g. a model's cache rates) map to 0 rather than a fabricated
heuristic.

**Resolution order (freshest valid wins):**
1. **Local cache** — `~/.aispend/pricing/litellm.json` (one table today; a
   per-provider `<provider>.json` layout later), used if fresh (≤ 24h).
   (CodeBurn caches the same way at `~/.cache/codeburn/`.)
2. **Remote refresh** — at most daily, from the **AgentSpend pricing endpoint** on
   our own subdomain: `https://agentspend.cloudyali.io/pricing/litellm.json` today,
   growing to per-provider tables plus a signed `index.json` (versions + checksums).
   Curated and kept current by our **server-side process**, which tracks vendor pages
   / LiteLLM / models.dev, validates against the schema, versions + signs, and
   publishes with cache headers.
3. **Embedded table** (`go:embed`) — the always-present floor; the tool prices
   fully offline with it, exactly like ccusage's bundled snapshot and CodeBurn's
   hardcoded Claude/GPT fallbacks.

Every fetched table is **validated before use** (schema + checksum/signature);
anything malformed or unsigned is rejected and we fall back — so a bad upstream
push can't poison local costs (the lesson of the LiteLLM incident). `priced_at`
+ `PricingTableVersion` on each event record exactly which table priced it.

**Why our own endpoint, not LiteLLM directly:** control and reliability. We
curate and validate before publishing, we don't inherit a third party's outages
or bad pushes, and we serve a coherent multi-provider set. We may *source* from
LiteLLM/models.dev server-side, but the laptop only ever talks to our validated
bucket (and `pricing.endpoint` lets a user point at a self-hosted mirror).

### 4.1 The trust reconciliation (this evolves the egress stance — read it)

A daily refresh puts an **outbound network call** in the default build — a real
change from the original "no `net/*` at all" property. It is done so it doesn't
betray the trust thesis, and the line we hold is precise:

- **No user data ever leaves.** The refresh is a plain `GET` of a public, static,
  non-identifying pricing file. It sends **no spend, no identity, no telemetry, no
  cookies** — at most a `?v=<version>` cache hint. It is *inbound reference data*,
  the same category as a package manager fetching an index. ccusage and CodeBurn —
  both developer-trusted — ship exactly this.
- **Offline-first.** Pricing never blocks on the network; on any failure it falls
  back cache → embedded, silently.
- **Controllable + auditable.** `--offline` / `--no-network` and a config toggle
  (`pricing.refresh = false`) disable it; `aispend doctor --network` reports the
  *one* outbound, its exact URL, and that it carries no identifiers.
- **A pure-offline artifact remains.** A `//go:build offline` target embeds prices
  and contains **no network code at all** — the strict original guarantee,
  preserved as a mode for air-gapped / enterprise users.

So the honest property becomes: **the default build never *uploads* anything about
you; its only network capability is an opt-out, auditable, inbound price refresh —
and a zero-network build exists for those who want it.**

> **Locked default posture (decided 2026-06-14):** the shipped binary carries the
> embedded tables **and** the refresh mechanism **enabled** (opt-out). The embedded
> table is always present as the offline floor; `--offline` / `pricing.refresh =
> false` disables fetching; the `//go:build offline` artifact is embedded-only and
> contains no network code.
>
> **Seam built and verified (`internal/pricing/refresh`):** the default build
> isolates every `net/*` import to that one package, and `go build -tags offline`
> produces a binary where even the pricing package imports **zero** network code
> (`go list -deps -tags offline ./cmd/aispend` is net-free — zero `net/*`). The
> **default** `cmd/aispend` now wires the refresher in (via `aispend pricing refresh`
> / `doctor --network`), so it contains `net/http` by design — the opt-out inbound
> price refresh described above, and nothing else; `doctor --network` discloses that
> one fetch. See the egress
> notes in [01-architecture.md](01-architecture.md) and
> [03-engineering-process.md](03-engineering-process.md).

Every table is also covered by a **schema-validation test** and **golden cost
tests**, and — like parser fixtures (PRD §8.4) — pricing tables are a
**community-maintainable asset** with a contribution guide.

## 5. Provenance & confidence (ties to the evidence ledger)

The pricing module is the main author of an event's confidence story:

| Situation | `cost_method` | confidence | reason |
|---|---|---|---|
| Tool wrote its own cost (`costUSD`/`cost` > 0) | `reported` | ~0.98 | "cost reported by the tool on disk" |
| Model + tokens known, public rate | `token_priced` | ~0.95 | "tokens × public API rate" |
| Cache-write TTL split present (`ephemeral_5m`/`1h`) | `token_priced` | ~0.95 | "1-hour tier priced at 2× input" |
| Cache-write TTL unknown (only a flat count) | `token_priced` | ~0.85 | "no TTL split; priced at the 5m rate (1.25× input)" |
| Cursor Auto-mode, tokens estimated from chars | `inferred` | ~0.5 | "auto-mode: tokens estimated from text length" |
| Subscription amortization | `subscription_amortized` | ~0.7 | "allocation, not a metered price" |
| Model not in table | `inferred` | 0 | "model not in pricing table <v>" |

## 6. Honesty gaps we model explicitly (don't paper over)

- **Cache-write TTL.** **[resolved 2026-06-16]** Recent Claude Code records carry
  the per-TTL split (`cache_creation.ephemeral_5m_input_tokens` /
  `ephemeral_1h_input_tokens`); the normalizer keeps the 1-hour subset in
  `CacheWrite1h` and the engine prices it at 2× input (the 5-minute portion at
  1.25×). Older records with only the flat `cache_creation_input_tokens` fall back
  to the 5-minute rate at lower confidence — the flagged estimate the evidence
  ledger exists for.
- **Batch vs interactive.** If we can't tell, assume interactive (the higher,
  conservative number) and note it.
- **Context tiers.** Apply only when prompt size is known; otherwise note the
  assumption.
- **Extended-thinking tokens.** Claude Code's logged `output_tokens` *excludes*
  extended-thinking tokens, while the in-app status bar *includes* them — two
  "truths" with different denominators. So a token-priced output number computed
  from the log is a floor; if we ever reconcile against the status bar or an admin
  API, expect a gap and label which denominator each number uses.
- **Unknown / unpriced models must be visible, never silent.** Resolution is
  exact-match (after snapshot-date strip); a model with no table entry leaves
  `api_equivalent` nil. Reports therefore *surface* those events on an `unpriced`
  footnote (count + model histogram, e.g. `claude-opus-4-7 (7535), <synthetic> (25)`)
  instead of dropping them from the total. Real case (Session 14): `claude-opus-4-7`
  — the user's primary model — was absent from the table, so 7,535 turns silently
  vanished and the total read ~½ the truth. A missing barcode must beep, not pass
  the item through unscanned. **Why not a family fallback** (`-4-7` → `claude-opus-4`):
  the generic key holds the *legacy* Opus 4.0 rate ($15/$75), 3× the 4.x line
  ($5/$25), so falling back would overcharge. Contemporaneous snapshots get explicit
  entries at their verified rate until an alias/version-aware resolver (§3) exists.

Conservative-but-flagged beats confident-but-wrong, every time.

## 7. Phasing — how the module grows

| Phase | Pricing scope |
|---|---|
| **0A (now)** | Anthropic table; flat input/output/cache-read/cache-write; api-equivalent + estimated. `cmd/aispend` is **embedded-only & provably net-free**. The refresh **build-tag seam is built + verified now** (`internal/pricing/refresh`: default isolates `net/*`; `-tags offline` is net-free) but not yet wired into the binary. The seed. |
| **0B** | Multi-provider tables (OpenAI/Codex credits, Cursor auto-mode estimate, Copilot usage credits); cache-write TTL tiers; batch multiplier; context tiers; web-search pricing; the richer `Rate` schema above; per-table schema + golden tests; contribution guide; **automating** the price refresh (embedded → cache → endpoint, §4) to run daily — the manual `aispend pricing refresh` against our subdomain already ships — plus the signed `index.json` + checksums. **This is where it becomes a real "module."** |
| **1A** | Admin/usage-API rates as an authoritative override of estimates where a vendor exposes them. |
| **2 (CloudYali)** | Billed/invoice reconciliation overrides estimates entirely; enterprise discounts, currency, tax, regional pricing. |

## 8. Where it lives

`internal/pricing` (engine + `Plan`) and `internal/pricing/tables/*.json` (data).
The refresh client lives in an **isolated** `internal/pricing/refresh` package —
the only place `net/*` is imported — so the egress surface is one small, auditable
unit (and absent entirely from the `offline` build). The `PricingEngine` interface
([phase-0A](phase-0A-trusted-explainable-ledger.md)) is unchanged by this growth —
richer tables, the refresh source, and new views all slot in behind it.

## 9. References — how the local trackers do pricing

Worth reading their code directly before building 0B; we are deliberately walking
the path they proved, then improving the sourcing.

- **ccusage** embeds a models.dev/LiteLLM pricing snapshot and fetches via a
  shared pricing fetcher, with an `--offline` mode using bundled data. Its bundled
  snapshots periodically lag new model releases — concrete reason to have a refresh
  path *and* robust model canonicalization/aliases. ([repo](https://github.com/ryoppippi/ccusage), [offline-data issue #844](https://github.com/ryoppippi/ccusage/issues/844), [#948](https://github.com/ryoppippi/ccusage/issues/948), [Codex source guide](https://ccusage.com/guide/codex/))
- **CodeBurn** fetches LiteLLM pricing, **caches 24h at `~/.cache/codeburn/`**, keeps
  **hardcoded Claude/GPT fallbacks to prevent mispricing**, prices web-search tokens,
  and labels Cursor Auto as "Auto (Sonnet est.)". ([repo](https://github.com/getagentseal/codeburn), [models doc](https://codeburn.app/docs/models))
- **LiteLLM model-cost-map incident** — a bad cost map on `main` broke downstream
  costs; the case for our own validated endpoint + checksums + embedded fallback.
  ([incident report](https://docs.litellm.ai/blog/model-cost-map-incident), [local cost map / offline](https://docs.litellm.ai/docs/proxy/custom_pricing))
