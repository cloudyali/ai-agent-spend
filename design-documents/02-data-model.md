# 02 — Data Model

_Last updated: 2026-06-14 · Stable. A change here is a versioned-contract change:
bump `SchemaVersion` and update the affected phase docs in the same change-set._

`AgentEvent` is the core asset. Every surface reads it; nothing reads a raw file.
It is stamped with a `SchemaVersion` so a future CloudYali server can ingest
today's and older shapes. This doc is the single source of truth for that
contract — the Go code in `internal/event` must match it exactly, and the golden
fixtures (`testdata/golden/`) enforce that it does.

---

## 1. `AgentEvent`

```go
package event

const SchemaVersion = 1

type AgentEvent struct {
    SchemaVersion int       `json:"schema_version"`
    EventID       string    `json:"event_id"`   // stable hash of the dedupe key — collapses streaming duplicates, idempotent re-scan
    SessionID     string    `json:"session_id"`
    PromptID      string    `json:"prompt_id,omitempty"`
    Provider      string    `json:"provider"`   // "claude_code"
    Surface       string    `json:"surface"`    // "coding_agent"
    IdentityHash  string    `json:"identity_hash"`
    Project       string    `json:"project,omitempty"`
    Repo          string    `json:"repo,omitempty"`
    CWDHash       string    `json:"cwd_hash,omitempty"`
    GitBranch     string    `json:"git_branch,omitempty"` // branch off the session line (§9)
    GitSHA        string    `json:"git_sha,omitempty"`    // HEAD at the turn, reflog-reconstructed best-effort (§9)
    CostTag       string    `json:"cost_tag,omitempty"`   // from .aispend.toml
    Model         string    `json:"model"`
    Mode          string    `json:"mode,omitempty"`     // agent | chat
    Tokens        Tokens    `json:"tokens"`
    CostViews     CostViews `json:"cost_views"`
    Evidence      Evidence  `json:"evidence"`
    Tools         []string  `json:"tools,omitempty"`
    MCPServers    []string  `json:"mcp_servers,omitempty"`
    Files         []string  `json:"files,omitempty"`         // repo-relative paths the turn operated on
    SessionChurn  []FileChurn `json:"session_churn,omitempty"` // per-file line churn, once per session (§9)
    Activity      string    `json:"activity,omitempty"` // classifier deferred to 0B; empty in 0A
    TSStart       time.Time `json:"ts_start"`
    TSEnd         time.Time `json:"ts_end"`
    ActiveMS      int64     `json:"active_ms,omitempty"`
}

// FileChurn is the net line delta a session made to one repo-relative file.
type FileChurn struct {
    Path    string `json:"path"`
    Added   int    `json:"added"`
    Removed int    `json:"removed"`
}

type Tokens struct {
    Input, Output, CacheRead, CacheWrite int64
}
```

A few of these fields earn their keep specifically because they are the seam the
later fleet/FinOps work snaps onto, at zero cost today: `IdentityHash` (the
server resolves to people later, only via consented mapping), `CostTag` (the
attribution primitive, §4), `SchemaVersion` (forward-compatible ingestion), and
`Surface` (the two-surface tag — `coding_agent` now, `server_api` at the
CloudYali tier).

The VCS fields — `GitBranch`, `GitSHA`, `Files`, `SessionChurn` — link a turn to the
code it changed (the spend → shipped-work seam). All four are **additive** (no
`SchemaVersion` bump): `GitBranch`/`Files` come from the normalizer, `GitSHA` and
`SessionChurn` from the best-effort scan-time `EnrichVCS` pass (reflog + `git diff`).
They are empty when unresolvable — never guessed. The mechanics, the offline/zero-dep
boundary, and the report/receipt surfaces live in [09-session-view.md](09-session-view.md)
§"Linking sessions to code".

## 2. `Money` — integer micro-units, never a float

Floating-point money is a bug waiting for a finance argument. Money is an integer
count of millionths of a currency unit:

```go
// 1 USD = 1_000_000 micros. $0.42 = 420_000 micros.
type Money struct {
    Micros   int64  `json:"micros"`
    Currency string `json:"currency"`
}
```

Micros (not cents) because token pricing is sub-cent: a cache-read at
`$0.30 / 1M tokens` is `0.30` micro-dollars per token — representable exactly in
micros, lossy in cents. All arithmetic is integer; rounding happens once, at
render time, in the CLI.

## 3. Cost views — no single "true cost"

Flat plans, pooled credits, amortized subscriptions, included usage, enterprise
discounts, and API-equivalent replacement values are *different cost concepts*.
Present one as "the truth" and you invite an argument you lose. We model each
lens and let the user pick:

```go
// A nil pointer means "not computable from available evidence" — NEVER zero.
type CostViews struct {
    Billed             *Money `json:"billed,omitempty"`
    Reported           *Money `json:"reported,omitempty"`
    EffectiveAllocated *Money `json:"effective_allocated,omitempty"`
    Marginal           *Money `json:"marginal,omitempty"`
    APIEquivalent      *Money `json:"api_equivalent,omitempty"`
    CreditConsumption  *int64 `json:"credit_consumption,omitempty"`
    Estimated          *Money `json:"estimated,omitempty"`
}
```

| View | Meaning | Computable in 0A? |
|---|---|---|
| **Billed** | What appears on the provider invoice / admin export | No — needs reconciliation (Phase 2). Stays `nil`. |
| **Reported** | A cost the *tool itself* wrote to disk (Claude Code `costUSD`; OpenCode/Pi `cost`) | Yes, when present and `> 0`. Authoritative: `cost_method=reported`, confidence ~0.98, with api-equivalent still computed beside it. This is ccusage's "Auto" (reported-else-computed) as a first-class, labeled view — not a blended override. Distinct from **Billed**, which is the *vendor's invoice*, not a tool's own estimate. |
| **Effective-allocated** | Subscription/seat/credit spread across observed usage | Only if a subscription plan with a monthly fee is configured. `subscription_amortized`, lower confidence. |
| **Marginal** | Incremental overage beyond included quota | Only if the plan declares included quota. |
| **API-equivalent** | What the usage would cost at public API rates | **Yes, always** — tokens × table rate, cache-read at the reduced rate. `token_priced`, confidence ~0.95. |
| **Credit-consumption** | Usage converted into platform credits | `nil` for Claude Code; structure exists for 0B (Copilot/Codex). |
| **Estimated** | Inferred when nothing authoritative exists | Equals API-equivalent when no plan info — and is *flagged* as an estimate. |

**The rule that makes this trustworthy: `nil` means "not computable here," never
"zero."** `aispend explain` shows which views are `nil` and why (via
`KnownMissingFields`).

## 4. The evidence ledger — provenance as a product feature

Every `AgentEvent` carries the answer to "why is this number what it is." This is
not an internal field; it is what `explain` renders, and it is the moat.

```go
type Evidence struct {
    SourceType           string    `json:"source_type"`      // "local_file"
    SourceRecordID       string    `json:"source_record_id"`
    SourcePathHash       string    `json:"source_path_hash"` // hashed — never the raw path
    SourceLine           int       `json:"source_line,omitempty"`
    ParserName           string    `json:"parser_name"`
    ParserVersion        string    `json:"parser_version"`
    PricingTableVersion  string    `json:"pricing_table_version"`
    PricedAt             time.Time `json:"priced_at"`
    Currency             string    `json:"currency"`
    DiscountBasis        string    `json:"discount_basis,omitempty"`
    CostMethod           string    `json:"cost_method"`      // token_priced | subscription_amortized | inferred
    ConfidenceScore      float64   `json:"confidence_score"` // 0..1
    ConfidenceReason     string    `json:"confidence_reason"`
    KnownMissingFields   []string  `json:"known_missing_fields,omitempty"`
    DedupeKey            string    `json:"dedupe_key"`
    ReconciliationStatus string    `json:"reconciliation_status"` // "local_only" in 0A
    InvoiceReference     string    `json:"invoice_reference,omitempty"`
}
```

| Field group | Answers | Filled in 0A by |
|---|---|---|
| Source provenance | "Which file/parser produced this?" | Normalizer |
| Pricing provenance | "Which price list, when, what discount?" | Pricing engine |
| Cost method | "How was the cost derived?" | Pricing engine |
| Confidence | "How much to trust it, what's missing?" | Pricing engine |
| Reconciliation | "Deduped? Matched to an invoice?" | `local_only` until Phase 2 |

**`DedupeKey` is the semantic identity of a turn, set per-adapter.** For Claude
Code it is `(message.id | requestId)`; the `EventID` is the hash of that key, so
the several JSONL lines one streamed response produces (most with `input_tokens`
of 0 or 1) share an `EventID` and collapse to one event under the adapter's
**keep-max** rule — the entry with the largest token total wins (the fix for the
~100× base-input overcount; see
[phase-0A](phase-0A-trusted-explainable-ledger.md#deduplication--the-streaming-placeholder-undercount-verified-against-ccusage)).
With no `message.id`, the key falls back to `(source_path_hash | line | session_id)`
so unrelated turns never merge. Each adapter owns its key because every agent
double-counts differently — there is no single global dedupe rule.

## 5. `.aispend.toml` — the attribution primitive

Committed at a repo root; invisible to a hobbyist, load-bearing for the rollup:

```toml
project   = "payments-service"
cost_tag  = "team-payments"
env       = "prod"
```

The normalizer resolves the **nearest-ancestor** `.aispend.toml` from an event's
working directory and stamps `Project` / `CostTag`. No local tracker has this; it
is exactly the seam the centralized rollup uses to speak in cost-center terms.

Plan configuration lives in `~/.aispend/config.toml` and drives the
`effective_allocated` view. A default plus **per-provider** plans (one
subscription per agent) is supported, since a developer often pays two flat fees:

```toml
plan             = "claude-max-20x"   # default / Claude Code's plan
plan_start       = "2026-06-12"       # billing anchor — the day-of-month it renews
codex_plan       = "chatgpt-pro"      # Codex billed against a different subscription
codex_plan_start = "2026-06-01"       # each plan can start on its own date
```

Each provider's plan fee is amortized over *only that provider's* usage, so the
`effective_allocated` total is the real sum of both subscriptions. Fees come from
the seeded plans table (`internal/config/plans.json`); `monthly_fee_usd` (or
`<provider>_monthly_fee_usd`) overrides.

`plan_start` (and `<provider>_plan_start`) makes amortization **billing-cycle
aware**: cycles run from the start date's day-of-month to the next month's anchor
(clamped — a 31st anchor lands on Feb 28/29), and each cycle's days are priced at
`monthly_fee ÷ that cycle's actual length`, so a day in a 28-day February costs
slightly more than one in a 31-day March. Days before `plan_start` are never
charged, and a plan whose start falls after the report window is flagged (not
silently shown as `$0`). Without `plan_start` the legacy flat `fee × days / 30`
proration is used, so existing configs are unaffected. Both files are
parsed by a small **zero-dependency** loader (`internal/config`) that supports the
flat subset AgentSpend uses — `key = value`, quotes, `#` comments, `[section]`
skipped — keeping the binary dependency-free (swap in a full TOML library behind
the loader if richer files are ever needed).

## 6. Storage — pluggable backend behind the `Store` interface

Persistence sits behind the `Store`/`Sink` interface, so the backend is swappable
and both implementations run the *same* test contract:

- **`FileStore` — the default in 0A.** A single JSON file at
  `~/.aispend/events.json`, written atomically (temp + rename). **Zero external
  dependencies**, which keeps the binary a pure-Go static artifact and is ample for
  one developer's local ledger. The full `AgentEvent` is stored per record, so
  nothing is lost.
- **`SQLiteStore` — optional, built with `-tags sqlite`.** `modernc.org/sqlite`
  (pure Go, no cgo) for larger/fleet scale and indexed SQL. Same interface, same
  contract suite. It stores the full event as JSON for lossless round-trips, plus
  denormalized scalar columns for indexed filtering. Queries are **type-safe,
  generated by [sqlc](https://sqlc.dev)** from `internal/store/sql/*.sql` into
  `internal/store/sqlcgen` (committed; regenerate per
  `internal/store/sql/README.md`). Schema:

```sql
CREATE TABLE events (
  event_id          TEXT PRIMARY KEY,     -- idempotent upsert (ON CONFLICT)
  schema_version    INTEGER NOT NULL,
  provider          TEXT, surface TEXT,
  repo              TEXT, project TEXT, cost_tag TEXT,
  model             TEXT,
  ts_start_unixnano INTEGER,              -- robust ordering / range filtering
  event_json        TEXT NOT NULL         -- the full AgentEvent, lossless (the explain source)
);
CREATE INDEX idx_events_ts   ON events(ts_start_unixnano);
CREATE INDEX idx_events_repo ON events(repo);

CREATE TABLE scan_meta (                  -- per-provider last scan (Store.LastScan/SetLastScan)
  provider  TEXT PRIMARY KEY,
  last_scan INTEGER
);
```

The full event rides in `event_json`, so `Tools`, `MCPServers`, `CostViews`, and
`Evidence` are preserved exactly; the scalar columns are a denormalized index for
filtering. `FileStore` stores the same data as one JSON document
(`~/.aispend/events.json`); `SQLiteStore` uses the schema above
(`~/.aispend/aispend.db`). Truly incremental `Read(since)` with per-file
offset/mtime (a granular `scan_state`) is a later refinement; 0A filters by file
mtime. **When that refinement lands it must, in the same change, make `Upsert`
keep-max on an `EventID` collision** (keep the larger token total, not
last-write-wins): once a response's streaming lines can split across scan batches,
the per-batch `normalize.Dedupe` keep-max no longer covers them, so the store
boundary has to enforce it (review-log §12, finding 12-1). Credentials and config are **never** stored here and never logged. No raw
paths appear — only `source_path_hash`, computed from an OS-normalized path
(separators unified, case-folded on case-insensitive filesystems) so the same file
hashes identically across platforms. See
[04-platform-and-paths.md](04-platform-and-paths.md).

## 7. FOCUS alignment

`AgentEvent` maps onto the FinOps FOCUS spec so the Phase 2/3 FinOps export is a
projection, not a migration: `Billed`/`EffectiveAllocated` → `BilledCost` /
`EffectiveCost`; `Provider` → `ProviderName`; tokens → `ConsumedQuantity`/`Unit`;
`CostTag` → `Tags`; subscription fee → contract-commitment fields. AI-coding
specifics ride as namespaced `x_*` columns until FOCUS 1.4's token work lands
natively.

## 8. Schema versioning rules

- `SchemaVersion` is a single integer constant in `internal/event`.
- **Additive** changes (new optional field) do **not** bump it.
- **Breaking** changes (rename, type change, semantic change) bump it, and the
  store records the version per row so old rows remain readable.
- Every bump updates this doc, the golden fixtures, and a short note in the
  phase doc that motivated it.
