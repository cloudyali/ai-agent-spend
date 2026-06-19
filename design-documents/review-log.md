# Review Log

_Code-review and security-review findings per the gates in
[03-engineering-process.md](03-engineering-process.md). A phase ships only when
its findings are resolved or logged here as accepted exceptions._

---

## 2026-06-14 · Session 1 — core contract (`platform`, `event`, `pricing`, `store`)

Scope: the four TDD'd packages. Build clean, `go vet` clean, `-race` clean,
93.0% coverage on implemented packages, no `net/*` in the default import graph.

### Code review (technical-code-reviewing)

**Overall:** Solid foundation. Interfaces are small and consumer-side
(`Provider`, `Store`, `Sink`, `PricingEngine`), errors are wrapped with `%w` and
lowercased, no ignored errors outside tests, table-driven tests, injectable seams
(`platform.Resolver`, the pricing clock). No HIGH/blocker issues.

| # | Sev | Location | Finding | Disposition |
|---|---|---|---|---|
| 1 | MEDIUM | `store.go:72,98,112` | `MemStore` stores/returns `AgentEvent` by shallow copy — nested `Tools`/`MCPServers` slices and `CostViews`/`Evidence` pointers are shared with callers. Not triggerable today (pricing finishes mutating before `Upsert`; 0A query consumers are read-only), but a latent aliasing bug as the pipeline grows. | **Accepted for session 1.** Fix when the SQLite `LocalSink` lands — it serializes to JSON on write/read, which removes the aliasing for the shipped store; add a `clone()` for `MemStore` at the same time. Tracked in phase-0A next cycles. |
| 2 | MEDIUM-LOW | `pricing.go:109` | `micros = tokens * perMTok / 1e6` can overflow `int64` for pathological token counts (safe for realistic ≤~1e8 tokens × 7.5e7 micros = 7.5e15 ≪ 9.2e18). | **Accepted.** Inputs are first-party session files with bounded tokens. Add a `math/bits.Mul64` guard if/when untrusted ingestion (OTel/API, Phase 1A) arrives. |
| 3 | LOW | `event.go:76` | `Money.String()` negation overflows for `math.MinInt64`. | Accepted (unreachable for real money). Guard later. |
| 4 | LOW | `pricing.go:92` | Under a subscription plan, `Estimated` mirrors api-equivalent; revisit when amortization lands so "estimated" reflects allocation basis. | Tracked via `KnownMissingFields="effective_allocated"`. |
| 5 | LOW | `pricing.go:62` | `NewEngine` panics on a corrupt embedded table. | Accepted — build-time asset integrity check (cf. `regexp.MustCompile`); can become `(*Engine, error)` if a loader path ever takes user input. |

### Security review

> Note: the automated `/security-review` command expects to run with the git repo
> as its working directory; in this session the working directory is the
> scratchpad, so the command could not self-run. Review below was performed
> manually against the standing trust checklist; run `/security-review` from the
> repo root for the automated pass before 0A ships.

**Threat model for session 1:** all code is local, offline, first-party-file
sourced. Surface is tiny (path resolution, pure compute, in-memory store).

| Area | Result |
|---|---|
| **Egress** | ✅ No `net/*` package in `go list -deps ./cmd/aispend`. The single `Sink` seam holds; no cloud code present. The offline promise is a build property today. |
| **Secrets** | ✅ None read, stored, or logged. The credentials path (`~/.aispend/credentials`) is not touched by any code this session. |
| **Path / PII leakage** | ✅ `AgentEvent` has **no raw-path field** — only `SourcePathHash`. `provider.Source.RawPath` exists only in the (not-yet-built) read path and is contractually in-memory. `HashPath` enforces hashing. |
| **Injection** | ✅ No SQL yet (in-memory store), no shell-out, no template/`exec`. |
| **Deserialization** | ✅ `json.Unmarshal` runs only on the embedded pricing table (trusted, build-time), not on user input. |
| **Dependencies** | ✅ Zero third-party runtime deps so far (stdlib only). `modernc.org/sqlite` is the only planned add — pure-Go; scan advisories at add time. |

Findings / controls to carry forward:

| # | Sev | Finding | Disposition |
|---|---|---|---|
| S1 | LOW | `HashPath` is unsalted SHA-256 — an attacker who already holds a candidate path *and* the local DB can confirm it. | Accepted for local-only 0A (the DB never leaves the machine). Consider a per-install random salt (stored in config, not the events DB) if path pseudonymity needs strengthening before any sync. |
| S2 | CONTROL | Enforce "no raw paths in DB/export" and "no egress" as tests. | Add (a) a golden/assert test that serialized events contain no path-like raw strings, and (b) the `go list -deps` no-egress grep, both into CI before 0A ships. |
| S3 | CONTROL | When the SQLite `LocalSink` lands, use parameterized queries exclusively. | Tracked for the store cycle; security review will re-run on that change. |

**No HIGH/critical security issues in session-1 code.**

---

## 2026-06-14 · Session 2 — Claude Code parser + normalizer (`provider`, `normalize`, `claudecode`)

Scope: the provider seam, the Claude Code normalizer, and golden fixtures. Build
clean, `vet` clean, `-race` clean, 92.4% total, no `net/*` in the default graph.
Golden output verified to contain **only hashed** cwd/source paths.

| # | Sev | Location | Finding | Disposition |
|---|---|---|---|---|
| 6 | MEDIUM | `normalize.go` `eventID` | Hash was truncated to 12 hex (48 bits) — dedupe collisions plausible over a large ledger. | **Fixed this session** → 16 hex (64 bits). Golden regenerated. |
| 7 | LOW | `normalize.go` `canonicalModel` | Strips a trailing 8-digit group; a model legitimately ending in 8 digits would be mis-canonicalized. | Accepted — no such Anthropic model; revisit if the table grows. |
| 8 | LOW | `claudecode.go` `Read` | Reads whole files into memory and filters by file mtime (coarse). | Accepted for 0A volumes; per-file offset tracking lands with the store integration (`scan_state`). |
| 9 | CONTROL | `provider.RawRecord.Source.RawPath` | Raw path rides in memory through `Read`→`Normalize`; it is never written to `AgentEvent` (verified in golden) but a future logger could leak it. | Add a lint/test asserting `RawPath` never reaches logs/store when the scan pipeline is wired. |

**No HIGH/critical issues in session-2 code.** Security posture unchanged: still
offline, still hashed-paths-only, still zero third-party runtime deps.

---

## 2026-06-14 · Design decision — pricing refresh evolves the egress stance

**Decision (product owner):** the LLM pricing module will *ship embedded **and**
refresh daily* from an AgentSpend S3 endpoint, curated server-side — the
ccusage/CodeBurn pattern, improved by self-hosting (see
[05-llm-pricing.md](05-llm-pricing.md) §4). This introduces the **first outbound
network call** in the default build, from Phase 0B.

**Why it does not break the trust thesis:** the refresh is an *inbound* GET of a
public, static price file — no spend, no identity, no telemetry, no cookies. The
"no **user-data** egress" promise is intact. Controls: offline-first fallback
(cache → embedded), `--offline` / `pricing.refresh=false`, `doctor --network`
discloses the one outbound, and a `//go:build offline` artifact has zero net code.

**Tracked for security review when `internal/pricing/refresh` is built (0B):**

| # | Sev | Item |
|---|---|---|
| D1 | HIGH-watch | The refresh client must send **no identifiers** (no UA fingerprint, no cookies, no query beyond `?v=`); assert in tests. |
| D2 | HIGH-watch | Validate every fetched table (schema + checksum/signature) before use; reject-and-fallback on mismatch (the LiteLLM bad-cost-map lesson). |
| D3 | MED | `net/*` must be importable **only** from `internal/pricing/refresh`; CI asserts it (see [03-engineering-process.md](03-engineering-process.md) §4). |
| D4 | MED | TLS verification on the endpoint; pin the host; no plaintext fallback. |
| D5 | LOW | Cache files in `~/.aispend/pricing/` are non-sensitive (public prices) but should still be written with safe perms and validated on read. |

Through Phase 0A nothing changes: pricing is embedded-only and the build remains
provably net-free.

---

## 2026-06-14 · Session 3 — persistent store (`FileStore`, `SQLiteStore`)

- **Finding #1 (MemStore aliasing) — RESOLVED.** The persistent backends serialize
  the event to JSON on write and deserialize on read, so callers no longer share
  slice/pointer state with stored events. `MemStore` remains an in-memory test/ephemeral aid.
- **Backend decision:** the default 0A store is the zero-dependency `FileStore`
  (pure stdlib JSON, atomic temp+rename) — `modernc.org/sqlite` could not be
  downloaded in the build sandbox (proxy metadata works; the zip redirects to a
  blocked host). `SQLiteStore` is committed behind `-tags sqlite` for scale, same
  contract suite. Net effect: stronger single-static-binary posture by default.
- **Security:** `FileStore` writes `~/.aispend/events.json` with `0600`; both
  backends store only hashed paths (no raw paths); no net in the store import graph
  (`go list -deps ./internal/store` is net-free). `cmd/aispend` stays net-free.
- Both backends + `MemStore` pass the shared contract; `-race` clean; store coverage 94.3%.

**No HIGH/critical issues in session-3 code.**

---

## 2026-06-14 · Session 4 — scan pipeline + CLI (`scan`, `cli`)

The hero path runs end-to-end: provider → normalize → price → store → render, on
a real `~/.claude`. `scan`/`week`/`today`/`by-repo`/`explain`/`doctor` work; 91%
total coverage; `cmd/aispend` verified net-free.

- **CLI on stdlib `flag`, not cobra.** cobra (like `modernc.org/sqlite`) can't be
  fetched in this sandbox, and a stdlib CLI keeps the binary dependency-free. Same
  rationale as the FileStore decision; noted for the user to confirm.
- **Security:** `explain`/storage emit only hashed source + cwd (no raw paths —
  verified in the captured session). `doctor --network` truthfully reports the 0A
  binary as net-free. `scan` reports unparseable lines (`1 skipped`) — never drops.
- **Carry-forward:** the `doctor --network` "no network-capable sink" line is a
  static claim today; once the CI no-egress assertion is wired (next cycle) it
  becomes machine-checked. `--no-network` is currently a no-op (nothing to disable
  in 0A) — wire it when the 0B refresher lands.

**No HIGH/critical issues in session-4 code.**

---

## 2026-06-14 · Session 5 — SQLite verified (sqlc + vendored modernc)

`modernc.org/sqlite` is now vendored (`vendor/`, committed via `go mod vendor`)
and the `-tags sqlite` backend **compiles, links, and passes** the full store
contract (Mem + File + SQLite) plus persistence-across-reopen. Queries are
sqlc-generated (`internal/store/sqlcgen`, committed).

- **Bug caught by running it (fixed):** `OpenSQLite` re-applies the schema on
  every open; the DDL lacked `IF NOT EXISTS`, so reopening an existing DB errored
  ("table events already exists"). Fixed in `schema.sql` (sqlc accepts `IF NOT
  EXISTS`); regenerated. Exactly the kind of defect golden/unit tests miss but a
  real run surfaces.
- **Egress finding (important, not a defect):** a `-tags sqlite` binary pulls Go's
  `net` / `net/netip` / `net/url` **transitively via `modernc.org/libc`** (socket
  and netdb shims the SQLite VFS references — *not* outbound networking). The
  **default `FileStore` build remains net-free** (`go list -deps ./cmd/aispend` has
  no `net`). Reinforces FileStore-as-default for the no-egress guarantee; SQLite is
  an explicit opt-in for scale. Captured in [01-architecture.md](01-architecture.md)
  and [03-engineering-process.md](03-engineering-process.md).
- **Coverage note:** generated `sqlcgen` is 0% in the default build (only called by
  the tagged `SQLiteStore`), pulling the default total to 86.3%; it's exercised
  under `-tags sqlite`. Generated code is conventionally excluded from coverage
  gates — hand-written packages stay 87.5–100%.

**No HIGH/critical issues in session-5 code.** `vendor/` (235 MB) is the standard
offline-build mechanism; it contains only the pinned, `go.sum`-verified driver tree.

---

## 2026-06-14 · Session 6 — config loader + plan-aware cost (`config`, `normalize`, `pricing`, `cli`)

`.aispend.toml` (nearest-ancestor → project/cost_tag/env) and
`~/.aispend/config.toml` (plan/monthly_fee) now drive `cost_tag` grouping and the
**subscription-amortized `effective_allocated`** view. Demonstrated end-to-end
(captured in the phase-0A doc). All green; 87.3% total; `cmd` net-free.

- **Zero-dep config parser (decision).** Rather than vendor a TOML library, the
  loader parses the small flat subset AgentSpend defines (`key = value`, quotes,
  `#` comments, `[section]` skipped). Consistent with FileStore/stdlib-flag; keeps
  the binary dependency-free. **Limitation:** it is not a full TOML parser (no
  nested tables, arrays, multiline) — documented; swap to a vendored lib behind the
  loader if real TOML files are needed. Tested incl. error paths (91.5%).
- **Attribution is injected, not hard-wired.** `normalize.ClaudeCode.Attribute`
  keeps the normalizer pure and golden tests deterministic; the CLI supplies a
  per-directory-cached `config.LoadRepo` resolver. Existing goldens unaffected.
- **effective_allocated is period-level, by design.** Computed at aggregation
  (`pricing.ProratedFee` + `Allocate`, remainder to the largest group so parts sum
  exactly), not per event — flagged `subscription_amortized`, confidence 0.70,
  "allocation, not a metered price." No volatile plan prices are hardcoded: the
  user states `monthly_fee_usd`; absent it, the view explains itself rather than
  guessing.
- **Security:** the config loader only reads local `.toml` files, never
  credentials; no network; no raw paths stored (cost_tag/project are user-authored
  labels). 

**No HIGH/critical issues in session-6 code.**

---

## 2026-06-15 · Session 7 — seeded plan + current model prices; `plans` command

Researched current prices (cited in chat) and seeded them so the tool is useful
on a real machine without manual entry:

- **Model table refreshed** to `anthropic-2026-06`: current models
  `claude-opus-4-8` ($5/$25), `claude-sonnet-4-6` ($3/$15), `claude-haiku-4-5`
  ($1/$5) — so real June-2026 Claude Code sessions price. Legacy `claude-opus-4`
  etc. retained (historical rates) so older sessions and existing fixtures still
  price. Golden regenerated (version bump).
- **Seeded plans** (`internal/config/plans.json`, embedded): Claude Pro/Max
  5x/20x/Team and ChatGPT Go/Plus/Pro. `plan = "claude-max-20x"` now resolves
  $200/mo by default; `monthly_fee_usd` still overrides. New `aispend plans`
  command lists them and marks the configured one.
- **Honesty:** seeded prices carry a "verify against live pricing before release"
  note; nothing is presented as authoritative beyond the evidence ledger's
  confidence. Plan tier is *user-confirmed* (we don't reliably get it from local
  metadata — see below).
- All green; config 92.6%, pricing 94.9%, cli 87.8%; `cmd` net-free.

**Open follow-up (plan auto-detection):** local files reveal auth *method*
(API key vs subscription) but not the subscription *tier/price*, which Anthropic
doesn't expose to the CLI. Reliable path is user confirmation (now one line +
`aispend plans`); a best-effort "API-key ⇒ api pricing, OAuth ⇒ prompt for plan"
heuristic is a reasonable 0B add.

**No HIGH/critical issues in session-7 code.**

---

## 2026-06-15 · Session 8 — bug from real data: large JSONL lines

First run on a real `~/.claude` (Nishant's Mac) hit
`bufio.Scanner: token too long` on `scan`. Real Claude Code lines embed large
tool outputs / file contents on a single line, exceeding the scanner's buffer
(even at 1 MiB).

- **Fix:** `claudecode.readLines` now uses `bufio.Reader.ReadBytes('\n')` (no fixed
  line cap) instead of `bufio.Scanner`. Regression test added (2 MiB line) and an
  end-to-end check (3 MB line scans + prices cleanly). claudecode 85.4%.
- **Why it matters:** this is the canonical "running on real data finds what
  fixtures don't" defect — our sanitized fixtures were all small. Action: add a
  large-line fixture to the golden corpus so the regression net covers it too
  (follow-up).

**No HIGH/critical issues in session-8 code.**

---

## 2026-06-15 · Session 9 — time windows + skip visibility (`scan`, `cli`)

Driven by Nishant's first real run (17K events, only 7 days visible; 268 skips
opaque).

- **Flexible windows:** `--days N`, `--since`/`--until YYYY-MM-DD`, `--all`, and a
  `month` command. `effective_allocated` prorates over the resolved window (over
  the data's actual span for `--all`). Bad dates exit 2.
- **Skip visibility:** `scan.Summary` now retains a capped sample (≤50) of skipped
  records; `scan --verbose` prints `hash#line · reason · sample`. Non-verbose scan
  hints "run with --verbose". Verified: a malformed line is reported with its JSON
  error and content.
- **Privacy:** skip samples are first ~80 printable chars, shown **only** to the
  user locally (never stored/exported); source is the hashed path. Hashed-path +
  no-egress invariants intact; `cmd` net-free; 87.6% total.

**No HIGH/critical issues in session-9 code.** Follow-up still open: per-file
offset tracking to make scan-on-read cheap (the auto-scan idea).

---

## 2026-06-15 · Session 10 — Codex provider + multi-provider scan (first 0B)

- **Multi-provider scan:** `cmdScan` now loops over providers (`claude_code` +
  `codex`), each with its own normalizer, into one store. `week --by provider`
  shows them side by side — the first unified view. Per-provider scan summary +
  combined totals.
- **Shared reader:** extracted `provider.ReadJSONL` (the bufio large-line fix now
  lives in one place); claudecode + codex both use it.
- **Pricing table** renamed `pricing-2026-06` (multi-provider); added
  `gpt-5.3-codex`/`gpt-5-codex` ($1.75/$14, cached $0.175). Golden regenerated.
- **Codex normalizer is STATEFUL** (model/cwd from `TurnContext` correlated with
  the separate `TokenCount` line; resets per file).
- **⚠ Codex field mapping is v1/hypothesis.** Built to the openai/codex rollout
  docs; the exact token-field nesting varies by version. **Must verify against a
  real ~/.codex sample (or align to the ccusage/CodeBurn Codex parser)** before
  trusting Codex numbers — golden fixture lands once we have real data. This is the
  one place the trust thesis is currently on a hypothesis.
- All green; 86.4% total; `cmd` net-free.

**Process note (from Nishant):** for future providers (pi, opencode, …) and to
verify Codex, **reference the ccusage / CodeBurn parsers** rather than
reverse-engineering — recorded as the standing approach for 0B provider breadth.

**No HIGH/critical issues in session-10 code** (modulo the flagged Codex
field-mapping verification).

### Session 10b — Codex parser corrected against real ~/.codex data

Nishant pasted real rollout lines. Three corrections (the hypothesis was wrong in
exactly the ways flagged):

1. **No `item` wrapper** — real lines are flattened `{"timestamp","type","payload"}`;
   the docs' `{"timestamp","item"}` was wrong. (My v1 imported 0 events; now fixed.)
2. **`payload.info` is nullable** — handled.
3. **Subscription mode records only `total_tokens`** (no input/output split). Mapped
   total → Input as a lower bound, flagged `token_breakdown`; pricing downgrades such
   events to `cost_method=inferred`, confidence 0.40 — an honest estimate, not a
   metered price. (This is the api-key-vs-subscription insight: per-token cost is the
   wrong frame on a flat plan; `effective_allocated` is the right view for Codex.)

Verified: real-shape lines now import and price ($0.13 @ conf 0.40, flagged).
Also fixed `explain` hardcoding "Claude Code" → provider label. All green, 86.4%.

### Session 10c — `scan --full` + session_meta cwd + model fallback

Real `session_meta` confirmed: `payload.cwd` present (repo attribution works), but
**no model field** (model lives in `turn_context`). Two fixes:

1. **`scan --full`** — the real reason Codex showed 0 after the parser fix: the
   earlier broken scan had set Codex's last-scan watermark, so re-scans skipped the
   older files. `--full` ignores the watermark (idempotent Upsert makes it safe).
   This is the "re-scan after upgrade" escape hatch.
2. **Model fallback** — `cwd` taken from `session_meta.payload.cwd`; when no
   `turn_context` named the model, fall back to `gpt-5.3-codex` flagged
   `model_assumed`. So Codex always shows a (flagged) number, attributed to the real
   repo, rather than `$0`.

Verified end-to-end: `scan --full` imports Codex, attributes to the real repo
(`rajgad-ai-code-pipeline`), prices `inferred`/0.40 with `missing: token_breakdown,
model_assumed`. `explain` now labels the provider correctly. All green, 86.5%.

**Follow-up:** capture a real `turn_context` to drop the model fallback when present;
add a Codex golden fixture from real shapes.

---

## 2026-06-15 · Session 11 — per-provider plans + weighted confidence

Both driven by Nishant's "why are the totals different?" question.

- **Per-provider plans** (`config.PlanSet`): default `plan` + `<provider>_plan`
  overrides (e.g. `codex_plan = "chatgpt-pro"`). `effective_allocated` now amortizes
  *each provider's* fee over *its own* usage and sums them — so two subscriptions
  (Claude Max + ChatGPT Pro) total $400, not a single $200 smeared across both.
  Uncovered providers (usage, no plan) are noted, not silently mis-allocated.
- **Weighted/mixed confidence header**: api-equivalent now shows spend-weighted
  confidence (0.94 when 98% is 0.95-Claude + 2% is 0.40-Codex) and `mixed` when cost
  methods differ — instead of the misleading worst-case 0.40.
- Verified end-to-end ($400 across 2 plans; `mixed, confidence 0.94`). All green;
  `cmd` net-free.

**No HIGH/critical issues in session-11 code.**

---

## 2026-06-15 · Session 12 — ccusage/CodeBurn verification; Claude dedup + reported-cost

Driven by a deep read of the **ccusage** (Rust) and **CodeBurn** (TS) source
alongside the research note. Two fidelity gaps in shipped 0A code were found,
confirmed against both trackers, and fixed under t-wada TDD; the design docs were
updated across the board (0A/0B/1A, 02-data-model, 04-paths, 05-pricing, new
06-provider-coverage-backlog + the research note committed as a reference).

**Changes (all green, `-race` clean, `cmd/aispend` still net-free):**

1. **Claude Code keep-max dedup `(message.id, requestId)`.** The normalizer was
   per-line, so Claude Code's streaming placeholders (~75% with `input_tokens`
   0/1) summed into a ~100× base-input overcount — the headline research finding.
   Fix mirrors ccusage's `should_replace_deduped_entry`: `EventID` now derives
   from the dedupe key; a per-adapter `Deduper` collapses same-key events keeping
   the max token total, in `scan` before pricing; `scan` reports `… duplicates
   collapsed`. Golden fixture `streaming_placeholders.jsonl` pins it.
2. **Reported-cost path (`costUSD` → `reported` view).** New `CostViews.Reported`;
   normalize captures `costUSD` (when > 0); the engine stamps `cost_method=reported`
   (conf 0.98) while still computing api-equivalent for comparison — ccusage's
   `Auto` (reported-else-computed) as a labeled view, not a blend. Golden
   `reported_cost.jsonl` pins it.

### Code review (technical-code-reviewing)

Reviewed the changed Go against the Go-patterns + critical-defect-classes refs.
Errors handled, no ignored returns, dedup is a pure function over a per-scan local
map (no shared state / races), pre-allocated, idiomatic naming. JSON-null-map and
pgx/batch defect classes are N/A (no maps written post-unmarshal; no SQL/batches).

| # | Sev | Location | Finding | Disposition |
|---|---|---|---|---|
| 12-1 | MEDIUM | `scan` + `store.Upsert` | Within a scan, `Dedupe` is keep-max; across scans `Upsert` is **last-wins**. Safe today (0A re-reads whole files by mtime, so a response's placeholder + full lines are deduped together), but when **per-file offset incremental reading** lands (Session 9 follow-up), a later-read placeholder could overwrite a good event. | **Accepted / carry-forward.** When incremental offsets land, make `Upsert` keep-max on `EventID` (or dedupe at the store boundary). Tracked with the offset work. |
| 12-2 | LOW | `normalize.go` `costUSD` | `int64(math.Round(costUSD*1e6))` could overflow for pathological inputs. | Accepted — first-party bounded data; same class as accepted finding #2. Guard if untrusted ingestion (OTel/API) arrives. |
| 12-3 | LOW | `normalize.go` Reported | `Currency` hardcoded `"USD"` (correct: `costUSD` is USD by definition). | Accepted. Revisit if a non-USD pricing table ever coexists, so an event can't carry mixed-currency views. |
| 12-4 | LOW | `normalize_test.go` | No explicit equal-token tie test for `Dedupe` (behavior: keep first-seen). | Optional follow-up; behavior is deterministic and covered indirectly. |

**No HIGH/blocker code issues.**

### Security review

The automated `/security-review` again could not self-run (it needs the repo as
the working directory; this session's cwd is the scratchpad — same as Session 1).
Run it from the repo root in CI. Manual pass against the standing trust checklist,
with concrete assertions re-run after the change:

| Area | Result |
|---|---|
| **Egress** | ✅ `go list -deps ./cmd/aispend` has no `net/*`; the only import added is `math` (stdlib, non-net). |
| **Path / PII leakage** | ✅ New goldens carry only SHA-256 `source_path_hash`/`cwd_hash`; grep for `/Users/`, `/home/`, raw `cwd` in goldens is clean. `Evidence.DedupeKey` now holds `message.id|requestId` — opaque vendor IDs, not paths/secrets/PII; consistent with the hashed-path posture. |
| **Secrets** | ✅ None read/stored/logged. `costUSD` is a number; fixtures are synthetic. |
| **Injection / exec / deserialization** | ✅ No SQL/exec/template/`unsafe` in changed files; `json.Unmarshal` runs only over first-party local lines (existing threat model). The dedup map is built in-code, not from JSON (no nil-map-write path). |

**No HIGH/critical security issues.** Carry-forward: hash `DedupeKey` IDs only if
their pseudonymity ever matters for sync/export (non-sensitive today).

### Coverage (default build, hand-written packages above the 85–90% floor)

`normalize` 94.7% · `scan` 90.7% · `pricing` 96.2% · `cli` 88.0% · `event` 100% ·
`claudecode`/`codex` 85.2% · `store` 94.3% · `config` 91.8% · `platform` 100% ·
`refresh` 90.0%. `go vet` clean. Removed two stray `*.go.<nnn>` temp artifacts
(one pre-existing in `config/`) from the tree.

---

**Locked + verified (2026-06-14).** Posture decided: default ships embedded **and**
refresh-on (opt-out); embedded table is the always-present offline floor; a
`//go:build offline` artifact is net-free. The build-tag seam `internal/pricing/refresh`
is implemented and verified:

| Check | Result |
|---|---|
| `go build ./...` (default) | compiles; refresh tests 90% |
| `go build -tags offline ./...` | compiles; refresh tests 100% |
| `net/*` in `refresh` (default) | present — `net/http` (the isolated seam) |
| `net/*` in `refresh` (`-tags offline`) | **none — provably net-free** |
| `net/*` reachable from `cmd/aispend` (0A) | **none — 0A binary stays offline** |

The D1–D5 items above remain open for the security review when the refresher is
wired live (daily schedule, table validation, identifier-free requests) in 0B.

---

## 2026-06-15 · Session 12 — plan-start-aware (billing-cycle) amortization

Driven by Nishant: amortization ignored *when a plan started*, so a subscription
that began mid-window was still billed the full window's share of the month.

- **Billing-cycle anchor.** `plan_start` / `<provider>_plan_start` (YYYY-MM-DD)
  set the subscription's renewal anchor. New `pricing.AmortizeSubscription(plan,
  since, until)` walks each cycle from the anchor day to the next month's anchor
  (clamped: a 31st anchor → Feb 28/29) and prices each cycle's days at
  `fee ÷ that cycle's real length`, so a 28-day February day costs more than a
  31-day March day. Days before `plan_start` are never charged.
- **Back-compatible.** `pricing.ProratedFee` (flat `fee × days / 30`) stays the
  primitive and the default when no `plan_start` is set — existing configs and the
  `effective_allocated` numbers are unchanged. The CLI now threads the resolved
  `[since, until]` window (not just a day count) into `renderAllocated` and picks
  the cycle-aware path only when a start date is known.
- **Honest reporting.** A configured plan whose start is after the window is
  flagged ("plan starts <date>, after this window"), distinct from the existing
  "no plan set" note — never a silent `$0`.

### TDD (t-wada)

Red→green per layer: config parsing (`plan_start`, per-provider, bad-date error),
`AmortizeSubscription` (full cycle, mid-cycle start, pre-start window, variable
Feb/Mar daily rate, two-cycle sum, non-amortizable guards), and CLI rendering
(mid-window prorate, not-yet-active flag) — all written failing first.

### Code review (technical-code-reviewing)

**Overall:** Pure-computation change; no new I/O, concurrency, DB/batch, or JSON.
Checked against the critical defect classes — only "loops must terminate" applies:
`AmortizeSubscription`'s loop advances the cycle start by one month each pass
(strictly increasing) and breaks at `!cs.Before(until)` — provably terminates.
Nil `MonthlyFee` guarded before every deref; division-by-zero guarded
(`cycleLen > 0`, and consecutive anchors are ≥28 days apart); `int64` proration
can't overflow (`≈2e8 × ≤31`). `plan_start` parse error wrapped with `%w` like
the existing `monthly_fee_usd`. **No HIGH/MEDIUM.** LOW (accepted): day counting
uses `Duration/24h`, the same convention already in `cmdReport`/`spanDays`.

### Security review

`/security-review` couldn't run (its harness needs a git working dir with tracked
changes, unavailable in this session) — reviewed manually. The only new external
input is `plan_start` from the user's own `~/.aispend/config.toml`, parsed with
`time.Parse` and error-checked; no injection surface (no SQL/shell/path/HTML), no
new I/O, no secrets, no PII beyond echoing the user's own start date to their
stdout. No-egress invariant re-verified: `net/*` still unreachable from
`cmd/aispend`. **No issues introduced.**

All green; cli 88.7%, config 91.8%, pricing 96.0% (new funcs 95–100%); `-race`
clean; `cmd` net-free.

---

## 2026-06-15 · Session 13 — calendar windows + honest empty-state; Cowork verified

Driven by Nishant's real run: `today --by provider` showed "(no events in range —
run `aispend scan`)" while the store held **7,270 events / $306** — two problems.

**Changes (t-wada TDD, all green, `cmd/aispend` net-free):**

1. **`today`/`week`/`month` are now CALENDAR windows** (local): today = midnight→now,
   week = Monday→now (ISO), month = 1st→now. Extracted a pure
   `calendarWindow(now, period)` + `startOfDay/Week/Month` for deterministic tests.
   **`--days N` retained as the rolling escape hatch** (rolling isn't removed, just
   no longer the default); `--since/--until/--all` unchanged.
2. **Honest empty-state.** `cmdReport` now learns the store total when a window is
   empty and `emptyRange(label, storeTotal)` distinguishes "data exists, just not in
   this window" (`N stored; widen with --all or --days N`) from a genuinely empty
   store (`run aispend scan`). No more telling a user with 7,270 events to scan.

**Cowork open item — RESOLVED with live evidence.** Inspected this very Cowork
session's transcript (`~/.claude/projects/<encoded-session-dir>/*.jsonl`, 863 lines):
- Same `~/.claude/projects/` store, **same Claude-Code schema** (`requestId`,
  `isSidechain`, `message.id`, `message.usage` input/output/cache + `service_tier`/
  `speed`), model `claude-opus-4-8`. The `claude_code` normalizer + new dedup work
  unchanged — so Cowork spend is already captured (it's the opus-4-8 line in `--all`).
- **No `costUSD`** → the computed api-equivalent path runs (reported stays nil), as designed.
- **New finding → new open item: attribution.** Every line's `cwd` (and the project
  folder) is Cowork's *own* session dir (`…/local-agent-mode-sessions/<id>/…/outputs`),
  not the user's repo — so Cowork events bucket under a meaningless project and
  `.aispend.toml` cost tags don't attach. Tracked as the Cowork follow-up (task) and
  in [phase-0B] / research §8.
- Nishant's empty `today` was simply the old rolling-24h window + not having re-scanned
  since the morning's Cowork work — not a capture gap.

### Code review (technical-code-reviewing)

CLI/render changes only. `calendarWindow` is pure and unit-tested at the Monday
boundary; `--days` escape hatch preserved; `renderReport`/`renderAllocated` gained a
`storeTotal` param (all call sites updated). No error handling regressions, no new
imports, no net. **No HIGH/MEDIUM.** LOW (accepted): week start is hardcoded to
Monday (ISO) — make it configurable only if a user asks.

### Security review

Manual (the automated `/security-review` still needs a git working dir; run in CI).
No new external input (windows derive from the injected clock; `storeTotal` is an int
count), no new I/O beyond an extra local `Query` on the empty path, no secrets/PII,
no-egress invariant re-verified (`net/*` unreachable from `cmd/aispend`). **No issues.**

All green; cli coverage 87.3% (≥ floor); `go vet` clean; `-race` clean. Pre-existing
gofmt nit in `internal/platform/platform.go` (struct-tag alignment, untouched here)
noted for a separate cleanup.

## 2026-06-15 · Session 14 — pricing coverage gap: `claude-opus-4-7` + unpriced transparency

Found from real data after the dedup landed. On Nishant's machine a clean re-scan
stored 10,936 events but `today --all` priced only 3,376 — a 7,560-event hole. A
`jq` histogram of events with `cost_views.api_equivalent == null` named the culprits:
**7,535 `claude-opus-4-7`** (his primary model for the May 7–Jun 9 span) and 25
`<synthetic>`. Two distinct defects:

1. **Coverage:** the embedded table had `claude-opus-4-8` and the *legacy* generic
   `claude-opus-4` ($15/$75) but not the `-4-7`/`-4-6` snapshots. Lookup is exact
   (`e.t.Models[ev.Model]`), so `-4-7` matched neither → `APIEquivalent` stayed nil.
   A naïve family fallback would have been *worse* — it would price 4.7 at the legacy
   4.0 rate, 3× too high.
2. **Honesty:** `renderReport` skips any event whose view is nil (`pickView` !ok) with
   no trace, so the unpriced 7,535 silently shrank the total. The number looked
   precise and was a quiet undercount — the opposite of the project's "trust" goal.

### TDD (t-wada)

- **Red → green:** `TestPrice_Opus4xSnapshotsPriceAtCurrentRate` asserts 4.6/4.7/4.8
  all price at $5/M input and explicitly *not* the legacy rate. Added
  `claude-opus-4-7` and `claude-opus-4-6` to `pricing-2026-06.json`.
- **Red → green:** `TestRenderReport_SurfacesUnpricedEvents` asserts a mixed report
  shows an `unpriced` footnote naming the models + count. Added a `topUnpriced`
  histogram helper and an `unpriced … (N not in this view — model (n), …)` line.

### Pricing provenance

`claude-opus-4-7` list rate **$5/M input, $25/M output, $0.50/M cache-read** verified
via web search (Anthropic + OpenRouter + Artificial Analysis, June 2026) — unchanged
from 4.6/4.8. Cache-write mirrors 4.8 ($6.25/M, 5-min tier ≈ 1.25× input). `<synthetic>`
turns are genuinely model-less → correctly left unpriced, now *shown* rather than hidden.

### Code review (technical-code-reviewing)

Two-line data add + a render footnote and a pure helper. `topUnpriced` is total over its
map, deterministic (count desc, then name asc), capped with "+N more"; no new imports
(`sort`/`strings`/`fmt` already in `cli`). The empty-window path is unchanged (footnote
only prints when `n > 0` and `skipped > 0`). **No HIGH/MEDIUM.** LOW (accepted): in the
`reported` view the footnote also counts events lacking a self-reported cost — honest, if
chatty; revisit only if a user finds it noisy.

### Security review

No new external input (table is build-time embedded; histogram keys are model strings
already in the store), no new I/O, no secrets/PII, no-egress invariant intact
(`net/*` still unreachable from `cmd/aispend`). **No issues.**

All green; `-race` clean; `go vet` clean. Coverage: pricing 96.2%, cli 87.4%,
normalize 94.7% — all ≥ floor. **Action for users:** rebuild and re-scan to re-price
existing `-4-7` history (the table is embedded at build time).

## 2026-06-15 · Session 15 — capture gap: Cowork desktop sessions (separate config dir)

Same cross-check, second defect. After the `-4-7` fix the total was right for what
aispend *saw* — but a CodeBurn comparison showed **$266.90 of Opus 4.8 today** that
aispend never imported (re-scans kept returning the same 10,936, maxing at Jun 9).
Root cause: aispend's `claude_code` roots were only `$CLAUDE_CONFIG_DIR/projects`
and `~/.claude/projects`. Cowork (the Claude desktop app) runs Claude Code under its
**own per-session config dir**, confirmed on disk at:

```
~/Library/Application Support/Claude/local-agent-mode-sessions/<ws>/<conv>/local_<id>/.claude/projects/<mangled-cwd>/<uuid>.jsonl
```

`CLAUDE_CONFIG_DIR` is unset in the terminal, so a terminal scan never saw any of it.
The transcripts are standard Claude Code JSONL (type=assistant, `message.model`,
`usage`, `message.id`+`requestId`) — fully parseable; aispend was simply pointed at
the wrong tree. Terminal Claude Code genuinely stopped Jun 9; everything since has
been Cowork.

### TDD (t-wada)

- **Red → green** `TestProviderRoots_ClaudeCode_…/macOS includes the Cowork…`: add
  `coworkRoots()` (darwin app-support base; windows `%APPDATA%` best-effort; nil
  elsewhere) and append to the `claude_code` candidates. `ExistingRoots` still
  filters to present dirs, so a wrong/absent path is silently skipped.
- **Red → green** `TestSources_CoworkTreeFoundButOutputsSkipped`: the walker now
  `SkipDir`s `outputs`/`uploads`/`node_modules` so the nested transcript is captured
  but the big sibling artifact trees (where I write generated files) are not walked
  and a stray `.jsonl` artifact is never mis-ingested.

Cross-root dedup is free: `EventID = hash(message.id|requestId)` is unique per
response, and `Upsert` is idempotent, so terminal + Cowork roots can't double-count.

### Code review (technical-code-reviewing)

Path logic + one walker guard. `coworkRoots` is pure and per-OS (injectable GOOS/Home/
Env, 100% covered). The walker change is a dir-name switch returning `fs.SkipDir`;
no new imports, no new goroutines or shared state. **No HIGH/MEDIUM.** LOW (accepted):
Windows base is an unverified guess — harmless (filtered if absent), flagged for a
real check. Attribution (project = mangled outputs path) is the remaining half of
task #13 — captured spend will bucket under a meaningless project until then.

### Security review

No new external input (roots derive from injected Home/Env; walked paths are hashed,
never stored raw — `HashPath` unchanged). Walking a new tree is read-only `os` I/O;
`SkipDir` *reduces* surface and avoids descending user output/upload trees. No
secrets/PII, no-egress invariant intact (`net/*` still unreachable from `cmd/aispend`).
**No issues.**

Full suite green; `go vet` clean; gofmt clean. Coverage: platform 100%, claudecode
87.1% — both ≥ floor. (`-race` not re-run this round — sandbox was flaky — but no
concurrency was added; path/walker code is single-threaded.) **Action for users:**
rebuild and re-scan; Cowork history (incl. today's Opus 4.8) will now import. It will
bucket under a mangled project name until the attribution half of task #13 lands.

**Follow-on (same re-scan):** capturing Cowork took all-time from $2,148 → **$11,063**
(desktop was the majority; opus-4-8 alone $34 → $3,849). The unpriced footnote then did
its job and named a new gap — **`claude-fable-5` (2,214 events)**, the frontier model
Cowork uses under the hood. Added it to the table at the verified $10/M in, $50/M out
(cache-read $1/M; cache-write $12.5/M ≈ 1.25× input) — `TestPrice_Fable5`, red→green.
Remaining unpriced is just `<synthetic>` (174, genuinely model-less). Note: skips rose
to 6,327 with the Cowork tree (new line types: `last-prompt`/`mode`/`attachment`/
`queue-operation`) — benign (no usage), but worth a `--verbose` confirm pass later.

## 2026-06-16 · Session 16 — Cowork project attribution (infer from edited files)

✅ **Core verified** — `go test ./...` green on the user's machine (2026-06-16); a
`scan --full` produced correct names (`ai-agent-spend`, `azure-cost-savings`,
`rajgad-*`, …) matching CodeBurn. The build sandbox was down here (`useradd` I/O
error), so the user ran it. See **Session 16b** below for the follow-on broadening.

The capture fix (Session 15) made Cowork spend visible but bucketed under a mangled
`-Users-…-outputs` project, because the session cwd is the desktop app's outputs dir.
Investigating this session's own transcript: cwd is the outputs dir, `gitBranch` is
just `HEAD`, session-init lines only reference outputs/uploads, and the **real** repo
(`…/agentspend/ai-agent-spend`) appears only inside tool-call file paths (cleanly
160:1 vs. one stray). So — as CodeBurn must — the only signal is the files the session
edited. User chose "infer from edited files".

### Design

A session-level `Attributor` post-pass (parallel to `Deduper`), applied in
`scan.Run` after dedup. `ClaudeCode.AttributeProjects(events, recs)`: for sessions
whose cwd contains `local-agent-mode-sessions`, tally each tool-use file path by repo
root (injected `RepoRoot` hook → walks up to `.git`/`.aispend.toml`), pick the
dominant root per session, stamp its base name as project/repo, and let
`.aispend.toml` at that root override (cost tag/project) exactly like terminal
attribution. Applied to events by `SessionID`. Terminal sessions (real cwd) and
file-less sessions are untouched.

Safety call: tool-path parsing lives in an **attribution-only** struct inside
`AttributeProjects`, *not* the shared `ccContent` — so the strict main `Normalize`
unmarshal is unaffected (an early version put `input` on `ccContent`, which risked
turning an odd tool input into a dropped billable line; reverted).

### TDD (t-wada) — tests written, run pending

- `TestAttributeProjects_CoworkInfersFromEditedFiles`: two edits in `/work/myrepo`
  + one in `/work/other` → project `myrepo` (dominant wins).
- `TestAttributeProjects_TerminalSessionUntouched`: real-cwd session keeps its
  normalize-time project.
- Wiring: `scan.Run` calls the `Attributor` if implemented; `cli` injects a real
  `repoRoot` walker (`.git`/`.aispend.toml`, `""` when none → skip, never guess).

### Code review / security (self, pre-run)

Pure additions + one pipeline hook. No change to the main parse path; `repoRoot`
does read-only `os.Stat` walks up from tool file paths (paths already in the
transcripts, hashed before storage — `RepoRoot` returns a dir name only). No egress.
Open risk to confirm after run: coverage on the new branch, and a real-data check
that inferred names match CodeBurn (`ai-agent-spend`, `modern-ui-layout`,
`Azure Cost Savings Recommendations`). Attribution is heuristic by necessity —
flagged as such; a file-less session stays on its cwd basename.

### Session 16b — broaden to no-cwd / subagent turns ⚠️ verification pending

First real scan after 16: attribution worked (real repos showed) but `(no repo)`
was **39% / $4,750**. A `jq` histogram of empty-repo events showed **all** of them
are `claude_code` with **no cwd** — 9,131 opus-4-7, 6,151 opus-4-8, 1,102 fable-5,
… ≈ 16,946 turns. These are subagent/sidechain transcripts whose lines carry no
`cwd`, so normalize leaves repo empty *and* the 16-version attributor skipped them
(it only fired when cwd held the Cowork marker).

Fix: drop the cwd gate so tool-path inference runs for **every** session, and gate
the *apply* instead — only fill events whose repo is a placeholder (`""` or
`"outputs"`), never overwrite a real cwd-derived repo. Removed the now-unused
`coworkMarker`. New tests: `…_NoCwdTurnInferredFromFiles` (empty-repo turn gets the
edited repo) and `…_RealRepoNeverOverwritten` (a genuine cwd repo is left alone even
when the session edits elsewhere). Existing Cowork/terminal tests still hold.

To confirm: `go test ./... && ./aispend scan --full && ./aispend today --all --by repo`
— expect `(no repo)` to collapse as the no-cwd turns fold into real repos (only
sessions with no inferable file edits should remain). Self-reviewed; not run here
(sandbox lost Go after the outage, toolchain download firewalled).

### Session 16c — fix over-grouping: key per transcript file, not sessionId

16b's broadening **over-corrected** on real data: after rebuild, `(no repo)` ($4,774)
didn't spread across repos — it was dumped *entirely* into one (`cloudyali-platform-demo-app`
$1,320 → $6,104; every other repo unchanged). A `jq` on the no-cwd turns showed why:
**all 34,737 had `sessionId` = MISSING**. The pass keyed the tally/apply on `sessionId`,
so every session-less turn collapsed into the `""` bucket and inherited that bucket's
single dominant repo. Confidently-wrong — worse than an honest `(no repo)`.

Fix: key on the **transcript file** (`Source.PathHash` for tally, `Evidence.SourcePathHash`
for apply) — one `.jsonl` = one session's log = one project. Session-less turns now
inherit *their own file's* dominant repo (spread correctly), and a file with no edits
stays honestly `(no repo)`. Added `TestAttributeProjects_KeyedPerFileNotSession` (two
session-less files editing different repos must not collapse); the `…_NoCwdTurn…` and
Cowork tests now assert via `SourcePathHash`. Guard: empty pathHash is skipped, so the
`""`-collapse can't recur.

✅ Unit-verified here: all five `TestAttributeProjects_*` pass, plus `scan`/`cli`/
`normalize` green, gofmt clean.

✅ **Confirmed on real data** (user rebuild + `scan --full`): the $4,774 spread
correctly — `cloudyali` fell back $6,104 → $2,507; `azure-cost-savings` $969 → $1,838,
`finops-tools` $702 → $1,256, `rajgad-modern-ui-layout` → $842, `rajgad-ai-code-pipeline`
→ $738, `ai-agent-spend` → $445; **`(no repo)` collapsed 39% → 5% ($604)**. Names match
CodeBurn's projects. Residual: `(no repo)` $604 + `outputs` $633 (~10%) are transcript
files that edited no git repo (deliverable generation / planning / pure-reasoning turns)
— genuinely unattributable, correctly left honest rather than guessed. Optional future
polish: link edit-less child turns to their parent transcript; relabel `outputs` →
a clearer "cowork (no repo)". **Cowork capture + attribution: done.**

### Session 17 — link sessions to code (branch · SHA · files · churn)

Goal: tie spend to shipped work. Added git provenance to `AgentEvent` (all additive,
no `SchemaVersion` bump) and surfaced it. t-wada throughout — RED confirmed before each
GREEN; goldens regenerated (only additive `git_branch: "main"` on `basic_session`).

- **`GitBranch`** — parsed from the Claude Code line in `normalize` (it logs a branch
  per turn).
- **`GitSHA`** — the log has none, so `internal/vcs.HeadAt` reconstructs it best-effort
  from `.git/logs/HEAD` (the commit that was HEAD at the turn's timestamp). **Pure-Go,
  no git binary, no network** — offline build + `doctor --network` untouched. Handles
  `.git`-file worktree/submodule indirection (incl. relative gitdir).
- **`SessionChurn`** (`[]FileChurn`) — per-file `+/−` from `git diff --numstat` between
  the session's first and last commit, via `vcs.Numstat` (the **one** git-binary dep,
  behind a hook; local read only). Stamped **once per session** (representative event)
  so a `--by file` rollup can't double-count it. Counts only churn committed *during*
  the session — absent (not over-attributed) otherwise.
- Enrichment is `normalize.EnrichVCS`, a new scan stage after attribution, before
  pricing (pricing is pure, so order changes no number). Repo root resolved from the
  raw records (cwd for terminal sessions, dominant edited-file root for Cowork/no-cwd),
  reusing the AttributeProjects signal — the hashed ledger can't recover paths later.
  SHA is per-turn; two turns in one sitting can land on different commits.
- **Surfaces.** `report --by branch|commit` (1:1, reconcile to the by-model total) and
  `--by file` (fan-out: cost splits equally across a turn's files so rows still sum to
  the total; `(no files)` bucket; commit shown short, kept whole in grouping). Receipt
  gains a `branch · SHA` line and a per-file **cost+churn heatmap** (cost-shaded bar +
  `+adds/−dels`). 09-session-view.md records why this per-file heatmap ships though the
  *streak* grid stayed cut.

✅ Unit-verified here: `event` 100%, `vcs` 94–95% (incl. a real-git `Numstat`
integration test, skipped where git absent), `normalize` 88%, `scan` 91%; `cli` 78.8%→
81.1% (new fns 88–100%; `shortSHA`/`sessionVCSLine` 100%, `costBar` 87.5%). `go vet` +
`gofmt -l` clean; full suite green.

✅ **End-to-end with real git**: built the binary, made a repo with two timestamped
commits, synthesized a session straddling them → `explain session:` showed
`branch feature/smoke · 49d057d5e3`, heatmap `████████ $4.50 app.go +3/-1` (real
`git diff`), and `--by commit` split $4.35/$0.15 across the two real SHAs, reconciling
to $4.50.

Honest limits: churn misses *uncommitted* work (by design — no fabrication); SHA
recovery is bounded by reflog retention, so backfill of old sessions may be empty;
churn surfaces on the receipt only (a `--by file` churn column is deferred). `cli`
sits at 81.1% (pre-existing shortfall vs the 85% floor; the *changed* code is ≥88%) —
lifting it means testing unrelated legacy command handlers, deferred.

**Code review (technical-code-reviewing skill).** One MEDIUM found and fixed:
`stampSessionChurn` would call `Numstat` with an empty file list for a session that
committed but edited no files, and `git diff` with no `--` filter returns the whole
range's churn — unrelated work attributed to the sitting. Guard added (skip when the
session touched no files) + `TestEnrichVCS_NoChurnWhenSessionTouchedNoFiles`. No other
HIGH/MEDIUM: errors all checked, no goroutines/shared state, div-by-N guarded by the
`len==0` checks, naming/interfaces idiomatic.

**Security review (manual — the `/security-review` command requires a git CWD, which
this session didn't have).** `vcs.Numstat` uses `exec.Command("git", …)` with **no
shell** (no metachar injection) and a `--` separator so a hostile filename can't be
read as a git flag. Defense-in-depth fix: `HeadAt` now validates that the reflog's
new-object-id is hex (`isHexSHA`, 7–64) before returning it, so a crafted
`.git/logs/HEAD` can't slip a `--option`-shaped token into `git diff`
(`TestHeadAt_RejectsNonHexNewSHA`). No network (offline build/`doctor --network`
intact), no new deps, no secrets logged. Privacy note: `GitBranch` is stored raw (like
the existing `Repo` basename) — branch names can carry codenames; absolute paths stay
hashed (`CWDHash`). `GitSHA` is itself a hash; churn is line counts only.

### Session 18 — TUI becomes the default channel + carries the VCS linkage

User directive: "default channel is the TUI, not commands." Two changes, t-wada throughout.

- **TUI receipt VCS linkage.** `internal/tui` `receiptView` gained a `branch · short-SHA`
  line and a per-file **cost+churn heatmap** (cost-shaded `spendBar`/`styleBar` + git
  `+adds/-dels`), mirroring the static `explain session:` receipt. New pure helpers
  (`sessionVCSLine`, `shortSHA`, `receiptHeatmap`, `fileCosts`, `churnMap`) replace the
  flat `topFiles` line; per-file cost is the same equal-split that reconciles with the
  session total. `TestModel_ReceiptVCSHeatmap` asserts branch/short-SHA/file/churn and
  that the full 40-char SHA is shortened. Existing receipt/nav tests stay green.
- **No-arg → TUI.** `dispatch` now routes a bare `aispend` to `cmdDefault`, which opens
  the TUI when it's linked (`tuiBuilt`, a build-tagged const: true in `!offline`, false
  in `tui_offline.go`) **and** stdout is a TTY; otherwise it falls back to the static
  `today` glance — so pipes, CI, and the air-gapped offline build never hit a dead TUI
  or bleed escapes. `help` still prints usage. Updated the existing no-arg assertion +
  `usage()` (default-channel note; the stale `G (group)` line now lists branch|commit|file).

✅ Verified here: `internal/tui` 86.0%, `internal/cli` green; full default suite green;
**offline build compiles + vets + tests** (`go build/vet/test -tags offline`), confirming
the no-arg path falls back without the TUI. `go vet` + `gofmt -l` clean.

**Code review (technical-code-reviewing).** No HIGH/MEDIUM: the new helpers are pure
(div-by-N guarded by `len==0`, no nil writes, no ignored errors); `cmdDefault` is a
two-branch selector with no new external input. **Security review (manual; `/security-review`
needs a git CWD this session lacks).** No new attack surface — the TUI only reads
already-stored events; no exec, no network, no new deps in the changed paths; the
offline build's zero-`net/*` guarantee is preserved (TUI stays compiled out).
