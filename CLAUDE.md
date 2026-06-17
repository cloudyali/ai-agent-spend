# aispend — working notes for Claude

Local, explainable AI-coding spend. Scans Claude Code / Codex session logs, prices
each turn against a pinned table, stores an evidence ledger. Zero-dependency,
provably offline (`aispend doctor --network`). Go + stdlib `flag` (no cobra).

## Design docs

Full design record in `design-documents/` — start at `00-index.md`. For surface /
UX work, read the concept captures on demand: `design-documents/07-ui-concept.md`
(web) and `design-documents/08-cli-tui-concept.md` (CLI/TUI).

## Build & test

Pure-Go, vendored, no network needed.

```
go build ./cmd/aispend
go test ./...                 # keep green
go test ./internal/... -cover # 85–90% min per package
gofmt -l internal/ && go vet ./...
```

(Sandbox only: `go` isn't on PATH — extract the bundled `go1.25.*.tar.gz` to
`/tmp/go`, then `export PATH=/tmp/go/bin:$PATH GOFLAGS=-mod=vendor GOCACHE=/tmp/gocache`.
`go test -race` can't finish under the 45s cap; non-race + vet are the gate.)

## Conventions (non-negotiable)

- **t-wada-style TDD**: write the failing test first (confirm RED), minimal code to
  GREEN, then refactor. Every change lands with tests.
- **Coverage 85–90% minimum** per package.
- **Reviews**: run the code-review skill and a security review on changes before done.

## CLI surface

One spend command, calendar-only windows (no rolling window):

```
aispend report --period <today|yesterday|week|month|"last week"|"last month"|
                         quarter|"this year"|"N days"|"since YYYY-MM-DD"|
                         YYYY-MM-DD..YYYY-MM-DD|all> [--by G] [--view V] [--json]
                         # --by: model|repo|provider|cost_tag|session
aispend today                  # arbitrage-first daily glance: ROI, cache savings, hourly spike bar
aispend explain <event-id>     # the hero: every number → its evidence + cost breakdown
aispend explain session:<id|max|last>   # the session receipt (explain, one level up)
aispend scan | doctor | plans
aispend pricing [refresh]       # show the active rate source; `refresh` pulls live LiteLLM rates
```

Rich static surfaces are **hand-rolled, zero-dependency ANSI** (no Bubble Tea /
lipgloss / x/term — keeps the offline-build + `doctor --network` promise). They
degrade to plain ASCII off a TTY, under `NO_COLOR`, or with `TERM=dumb`, and never
bleed an escape code into a pipe. `today` + the session receipt share the web
color language (cache-read blue, cache-write amber, output teal, input purple) and
the `pricing.WithoutCache` primitive for the `without cache ≈ $X · saved Y%` line.
The interactive TUI (`tui`/`watch`) stays deferred — see `08-cli-tui-concept.md`.

Pricing is **offline-first**: `scan`/`report`/`explain` price against a fresh
(≤24h) LiteLLM cache at `~/.aispend/pricing/litellm.json` when present, else the
embedded table. Only `aispend pricing refresh` touches the network (one inbound GET
of a public file — `doctor --network` discloses it; the `offline` build compiles out
all `net/*`). LiteLLM rates overlay the embedded table, which remains the floor for
any model LiteLLM doesn't list. `ParseLiteLLM` canonicalizes upstream ids
(`canonicalizeModelID`: lowercase, strip `vendor/` prefix + `-YYYYMMDD` snapshot,
then the extensible `modelAliases` map for dotted versions) so overlay keys land on
the same ids the engine prices by; zero-priced LiteLLM stubs are excluded.

`report --json` (token-priced views) and `explain` both surface a per-token-class
cost breakdown: input / output / cache-read / cache-write / cache-write-1h.

## Cache pricing (the subtle part)

Costs are dominated by cache on high-cache-hit workloads, so the cache rates matter
most. See `design-documents/05-llm-pricing.md`.

- **Anthropic**: two cache-write TTLs — 5-minute (default) = **1.25× input**, 1-hour
  (extended) = **2× input**; cache-read = **0.10× input**. TTL refreshes on each read.
  The normalizer reads `cache_creation.ephemeral_5m/1h_input_tokens`;
  `event.Tokens.CacheWrite1h` holds the 1-hour subset; the engine prices it at
  `2× input` (`oneHourCacheInputMultiple` in `internal/pricing/pricing.go`). The
  1-hour rate is derived in code, not a table column.
- **OpenAI / Codex**: **no cache-write charge** (`cache_write_per_mtok: 0`), cache-read
  ≈ **0.5× input** (the cached-input discount — NOT the 10% Anthropic heuristic),
  automatic TTL (no user tier) — the 1-hour multiplier never applies.
- **Gemini** (not yet ingested): billed by cache *storage time* (per-token-hour),
  a different cost dimension — needs its own model when added.

Reference tool for reconciliation: **CodeBurn** (TypeScript) splits the same TTL tiers
(`calculateCost`, `ONE_HOUR_CACHE_WRITE_MULTIPLIER_FROM_FIVE_MINUTE_RATE = 1.6`, i.e.
1.25 × 1.6 = 2.0× input) and pulls live rates from LiteLLM.
