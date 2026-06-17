// Package pricing fills an event's CostViews and the pricing half of its
// Evidence from embedded, versioned rate tables. Pricing is deliberately
// separate from normalization so a re-price never forces a re-read.
//
// Phase 0A computes the always-available lenses — api_equivalent (high
// confidence) and estimated — and records full pricing provenance. Plan-aware
// lenses that need period aggregation (effective_allocated, marginal) are
// computed at the aggregation step, not per event; the engine notes them as
// known-missing here. See design-documents/phase-0A-trusted-explainable-ledger.md.
package pricing

import (
	_ "embed"
	"encoding/json"
	"sort"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

//go:embed tables/pricing-2026-06.json
var pricingTable []byte

// rate holds per-1M-token prices in micro-USD.
type rate struct {
	InputPerMTok      int64 `json:"input_per_mtok"`
	OutputPerMTok     int64 `json:"output_per_mtok"`
	CacheReadPerMTok  int64 `json:"cache_read_per_mtok"`
	CacheWritePerMTok int64 `json:"cache_write_per_mtok"`
}

type table struct {
	Version  string          `json:"version"`
	Currency string          `json:"currency"`
	Models   map[string]rate `json:"models"`
}

// Plan is the billing context for an event's provider.
type Plan struct {
	Provider   string
	Kind       string // "api" | "subscription"
	MonthlyFee *event.Money
	// StartDate is the subscription's start; its day-of-month is the billing anchor
	// used by AmortizeSubscription. The zero value means "unknown" — callers then
	// fall back to the flat ProratedFee.
	StartDate time.Time
}

// Engine prices events against a pinned, embedded table.
type Engine struct {
	t   table
	now func() time.Time
}

// NewEngine loads the embedded Anthropic table. It panics only on a build-time
// corruption of the embedded asset (caught by tests), never on user input.
func NewEngine() *Engine {
	var t table
	if err := json.Unmarshal(pricingTable, &t); err != nil {
		panic("pricing: embedded table is corrupt: " + err.Error())
	}
	return &Engine{t: t, now: time.Now}
}

// TableVersion reports the pinned pricing table version stamped onto events.
func (e *Engine) TableVersion() string { return e.t.Version }

// Price fills the computable cost views and pricing provenance for ev.
func (e *Engine) Price(ev *event.AgentEvent, plan Plan) error {
	// Provenance is stamped even when the event cannot be priced.
	ev.Evidence.PricingTableVersion = e.t.Version
	ev.Evidence.PricedAt = e.now()
	ev.Evidence.Currency = e.t.Currency

	// A cost the tool wrote to disk (Reported) is authoritative: prefer it over the
	// computed number (ccusage's "Auto" = reported-else-computed), while still
	// computing api-equivalent below for comparison when the model is known.
	reported := ev.CostViews.Reported != nil && ev.CostViews.Reported.Micros > 0

	r, known := e.t.Models[ev.Model]
	if !known {
		ev.Evidence.KnownMissingFields = appendMissing(ev.Evidence.KnownMissingFields, "model_rate")
		if !reported {
			ev.Evidence.CostMethod = "inferred"
			ev.Evidence.ConfidenceScore = 0
			ev.Evidence.ConfidenceReason = "model not in pricing table " + e.t.Version
			return nil
		}
		// Unknown model but a real reported cost exists — fall through to the
		// reported override; api-equivalent stays nil (not computable).
	} else {
		fiveMin, oneHour := splitCacheWrite(ev.Tokens)
		total := micros(ev.Tokens.Input, r.InputPerMTok) +
			micros(ev.Tokens.Output, r.OutputPerMTok) +
			micros(ev.Tokens.CacheRead, r.CacheReadPerMTok) +
			micros(fiveMin, r.CacheWritePerMTok) +
			micros(oneHour, oneHourCacheInputMultiple*r.InputPerMTok)

		api := event.Money{Micros: total, Currency: e.t.Currency}
		est := api // with no authoritative plan price, the estimate mirrors api-equivalent
		ev.CostViews.APIEquivalent = &api
		ev.CostViews.Estimated = &est

		ev.Evidence.CostMethod = "token_priced"
		ev.Evidence.ConfidenceScore = 0.95
		ev.Evidence.ConfidenceReason = "tokens × public API rate (cache-read at reduced rate)"

		// When only a total token count is available (e.g. Codex on a subscription
		// records no input/output split), the number is priced at the input rate as a
		// lower bound — a flagged estimate, not a metered price.
		for _, f := range ev.Evidence.KnownMissingFields {
			if f == "token_breakdown" {
				ev.Evidence.CostMethod = "inferred"
				ev.Evidence.ConfidenceScore = 0.40
				ev.Evidence.ConfidenceReason = "estimated: only total tokens recorded (no input/output split); priced at input rate"
			}
		}
	}

	// The reported number wins the cost_method when present — a tool-asserted cost
	// beats both the token computation and any total-only estimate.
	if reported {
		ev.Evidence.CostMethod = "reported"
		ev.Evidence.ConfidenceScore = 0.98
		ev.Evidence.ConfidenceReason = "cost reported by the tool on disk (costUSD)"
	}

	// Subscription amortization is a period-level operation; note it as missing
	// so `explain` is honest about which lenses aren't yet computed.
	if plan.Kind == "subscription" {
		ev.Evidence.KnownMissingFields = appendMissing(ev.Evidence.KnownMissingFields, "effective_allocated")
	}
	return nil
}

// micros converts a token count and a per-1M-token rate into a micro-USD amount.
func micros(tokens, perMTok int64) int64 { return tokens * perMTok / 1_000_000 }

// oneHourCacheInputMultiple is Anthropic's 1-hour cache-write price as a multiple
// of base input tokens (the 5-minute default is 1.25× input). 1-hour cache reads
// are still 0.10× input. See docs.claude.com/.../prompt-caching.
const oneHourCacheInputMultiple = 2

// splitCacheWrite divides total cache-creation (CacheWrite) into its 5-minute and
// 1-hour portions, clamping a malformed 1h > total so the 5-minute part can never
// go negative. The two tiers are priced differently (1.25× vs 2× input).
func splitCacheWrite(tk event.Tokens) (fiveMin, oneHour int64) {
	oneHour = tk.CacheWrite1h
	if oneHour > tk.CacheWrite {
		oneHour = tk.CacheWrite
	}
	if oneHour < 0 {
		oneHour = 0
	}
	return tk.CacheWrite - oneHour, oneHour
}

// CostComponents is the per-line decomposition of an event's api-equivalent cost:
// each token class priced at its own rate. The lines sum to APIEquivalent — the
// breakdown that answers "where did this number come from?", which for a
// cache-heavy workload is dominated by the cache lines, not input/output.
// CacheWrite is the 5-minute tier; CacheWrite1h the 1-hour tier (2× input).
type CostComponents struct {
	Input        event.Money
	Output       event.Money
	CacheRead    event.Money
	CacheWrite   event.Money
	CacheWrite1h event.Money
}

// Total sums the lines — equal to APIEquivalent for a known model.
func (c CostComponents) Total() event.Money {
	return event.Money{
		Micros:   c.Input.Micros + c.Output.Micros + c.CacheRead.Micros + c.CacheWrite.Micros + c.CacheWrite1h.Micros,
		Currency: c.Input.Currency,
	}
}

// Components decomposes a model's token counts into the api-equivalent cost lines,
// using the same pinned rates and rounding as Price (so they reconcile exactly
// with the stored APIEquivalent). ok is false when the model isn't in the table —
// there are no rates to apply, and the caller must not show a false zero.
func (e *Engine) Components(model string, tk event.Tokens) (CostComponents, bool) {
	r, ok := e.t.Models[model]
	if !ok {
		return CostComponents{}, false
	}
	money := func(tokens, perMTok int64) event.Money {
		return event.Money{Micros: micros(tokens, perMTok), Currency: e.t.Currency}
	}
	fiveMin, oneHour := splitCacheWrite(tk)
	return CostComponents{
		Input:        money(tk.Input, r.InputPerMTok),
		Output:       money(tk.Output, r.OutputPerMTok),
		CacheRead:    money(tk.CacheRead, r.CacheReadPerMTok),
		CacheWrite:   money(fiveMin, r.CacheWritePerMTok),
		CacheWrite1h: money(oneHour, oneHourCacheInputMultiple*r.InputPerMTok),
	}, true
}

// WithoutCache prices an event as if prompt caching were off: every cache-read and
// cache-write token is charged as a fresh input token (1× input), alongside the
// normal input and output. It is the hypothetical "no-cache" bill the arbitrage
// line (`without cache ≈ $X · saved Y%`) compares the real, cache-aware
// api-equivalent against — the visible half of the subscription-arbitrage story.
//
// ok is false when the model isn't priced; the caller must not show a false zero.
// The result can be *below* the cache-aware total (a 1-hour cache write is 2×
// input, dearer than not caching), i.e. savings can be negative — an honest
// outcome the receipt renders rather than hides.
func (e *Engine) WithoutCache(model string, tk event.Tokens) (event.Money, bool) {
	r, ok := e.t.Models[model]
	if !ok {
		return event.Money{}, false
	}
	asInput := tk.Input + tk.CacheRead + tk.CacheWrite
	total := micros(asInput, r.InputPerMTok) + micros(tk.Output, r.OutputPerMTok)
	return event.Money{Micros: total, Currency: e.t.Currency}, true
}

func appendMissing(xs []string, s string) []string {
	for _, x := range xs {
		if x == s {
			return xs
		}
	}
	return append(xs, s)
}

// ProratedFee returns the share of a monthly subscription fee attributable to a
// window of `days` (a month is treated as 30 days). Returns (zero, false) unless
// the plan is an amortizable subscription with a known monthly fee. This is the
// basis for the effective_allocated cost view, which is a period-level concept —
// hence computed here at aggregation time, not per event.
func ProratedFee(plan Plan, days int) (event.Money, bool) {
	if plan.Kind != "subscription" || plan.MonthlyFee == nil || days <= 0 {
		return event.Money{}, false
	}
	return event.Money{
		Micros:   plan.MonthlyFee.Micros * int64(days) / 30,
		Currency: plan.MonthlyFee.Currency,
	}, true
}

// AmortizeSubscription returns the share of a monthly subscription fee that falls
// within [since, until], honoring the plan's billing anchor — the day-of-month of
// StartDate. Each billing cycle runs from one anchor day to the next month's
// anchor day (clamped: a 31st anchor lands on Feb 28/29), and a cycle's days are
// priced at MonthlyFee ÷ that cycle's actual length, so a day in a 28-day February
// costs slightly more than a day in a 31-day March — matching how the subscription
// truly bills. Days before StartDate are never charged.
//
// Unlike ProratedFee (a flat fee × days / 30), this needs the real calendar window,
// so it takes [since, until] rather than a day count. It returns (zero, false)
// unless the plan is an amortizable subscription with a fee and a known StartDate
// and at least one whole active day lies in the window. This is the date-aware
// basis for the effective_allocated view.
func AmortizeSubscription(plan Plan, since, until time.Time) (event.Money, bool) {
	if plan.Kind != "subscription" || plan.MonthlyFee == nil || plan.StartDate.IsZero() {
		return event.Money{}, false
	}
	from := since
	if plan.StartDate.After(from) {
		from = plan.StartDate // never bill before the plan existed
	}
	if !until.After(from) {
		return event.Money{}, false
	}

	var total int64
	for n := 0; ; n++ {
		cs := addMonthsClamped(plan.StartDate, n)
		ce := addMonthsClamped(plan.StartDate, n+1)
		if !cs.Before(until) {
			break // this cycle (and all later ones) start at/after the window end
		}
		lo, hi := maxTime(cs, from), minTime(ce, until)
		if !hi.After(lo) {
			continue // no overlap of this cycle with the active window
		}
		if cycleLen := dayCount(cs, ce); cycleLen > 0 {
			total += plan.MonthlyFee.Micros * dayCount(lo, hi) / cycleLen
		}
	}
	if total == 0 {
		return event.Money{}, false
	}
	return event.Money{Micros: total, Currency: plan.MonthlyFee.Currency}, true
}

// addMonthsClamped returns the billing anchor n cycles after t: t's day-of-month in
// the target month, clamped to that month's length (so a 31st anchor never spills
// into the next month the way time.AddDate would). n is always >= 0 here.
func addMonthsClamped(t time.Time, n int) time.Time {
	m := int(t.Month()) - 1 + n // months since January of t's year; >= 0
	y := t.Year() + m/12
	mo := time.Month(m%12) + 1
	day := t.Day()
	if last := daysInMonth(y, mo); day > last {
		day = last
	}
	return time.Date(y, mo, day, 0, 0, 0, 0, t.Location())
}

// daysInMonth returns the number of days in month mo of year y (leap-aware).
func daysInMonth(y int, mo time.Month) int {
	return time.Date(y, mo+1, 0, 0, 0, 0, 0, time.UTC).Day() // day 0 of the next month
}

// dayCount is the number of whole 24h days from a to b (negative if b precedes a).
func dayCount(a, b time.Time) int64 { return int64(b.Sub(a) / (24 * time.Hour)) }

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Allocate distributes total across groups in proportion to each group's basis
// (e.g. api-equivalent micros). Integer rounding remainder is given to the
// largest-basis group so the parts sum exactly to total. Deterministic.
func Allocate(total event.Money, basis map[string]int64) map[string]event.Money {
	out := make(map[string]event.Money, len(basis))
	var sum int64
	for _, v := range basis {
		sum += v
	}
	if sum <= 0 {
		for k := range basis {
			out[k] = event.Money{Currency: total.Currency}
		}
		return out
	}

	keys := make([]string, 0, len(basis))
	for k := range basis {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var allocated, maxBasis int64 = 0, -1
	var maxKey string
	for _, k := range keys {
		share := total.Micros * basis[k] / sum
		out[k] = event.Money{Micros: share, Currency: total.Currency}
		allocated += share
		if basis[k] > maxBasis {
			maxBasis, maxKey = basis[k], k
		}
	}
	if rem := total.Micros - allocated; rem != 0 && maxKey != "" {
		m := out[maxKey]
		m.Micros += rem
		out[maxKey] = m
	}
	return out
}
