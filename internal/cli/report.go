// Machine-readable projection of a metered spend report (`report --json`). The
// table and the JSON are two renderings of ONE aggregation (aggregateReport), so
// a scripted consumer and a human reading the terminal can never disagree about
// the numbers. The amortized view is intentionally not covered here —
// it is an allocation, not a metered total, and gets its own shape later.
package cli

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// reportResult is the JSON form of a metered report. Costs carry both the exact
// integer micros (millionths of the currency unit — the source of truth) and a
// convenience decimal; consumers needing exactness should use *_micros.
type reportResult struct {
	Period      string     `json:"period"`
	Since       *time.Time `json:"since"` // null for all-time (no lower bound)
	Until       time.Time  `json:"until"`
	GroupBy     string     `json:"group_by"`
	View        string     `json:"view"`
	Currency    string     `json:"currency"`
	Method      string     `json:"method,omitempty"`
	Confidence  float64    `json:"confidence"`
	Groups      []groupRow `json:"groups"`
	TotalMicros int64      `json:"total_micros"`
	TotalUSD    float64    `json:"total_usd"`
	Count       int        `json:"count"`
	Unpriced    *unpriced  `json:"unpriced,omitempty"`
	// TotalComponents is the api-equivalent cost split by token class across the
	// whole report; present only for token-priced views (api_equivalent/estimated).
	TotalComponents *componentsJSON `json:"total_components,omitempty"`
}

type groupRow struct {
	Key        string  `json:"key"`
	CostMicros int64   `json:"cost_micros"`
	CostUSD    float64 `json:"cost_usd"`
	Count      int     `json:"count"`
	Percent    float64 `json:"percent"`
	// Components is this group's api-equivalent cost split into input/output/
	// cache-read/cache-write — the four reconcile with cost_micros for the
	// api_equivalent view. Omitted for non-token-priced views and zero-token groups.
	Components *componentsJSON `json:"cost_components,omitempty"`
}

// costLine is one priced token class: exact micros + convenience decimal.
type costLine struct {
	Micros int64   `json:"micros"`
	USD    float64 `json:"usd"`
}

type componentsJSON struct {
	Input      costLine `json:"input"`
	Output     costLine `json:"output"`
	CacheRead  costLine `json:"cache_read"`
	CacheWrite costLine `json:"cache_write"` // 5-minute cache-creation tier
	// CacheWrite1h is the 1-hour cache-creation tier, priced at 2× input.
	CacheWrite1h costLine `json:"cache_write_1h"`
}

// unpriced surfaces events the view couldn't price (e.g. a model missing from the
// pricing table) so a coverage gap is visible in JSON, not a silent undercount.
type unpriced struct {
	Count  int            `json:"count"`
	Models map[string]int `json:"models"`
}

// reportAgg is the shared intermediate both renderers consume.
type reportAgg struct {
	rows         []*aggRow // grouped, sorted by cost desc
	total        int64
	n            int
	skipped      int
	skModels     map[string]int
	currency     string
	methods      map[string]bool
	confWeighted float64 // sum(confidence × micros)
	confBasis    int64
}

// aggregateReport folds events into grouped, cost-sorted rows for a metered view,
// tracking the spend-weighted confidence and any events the view can't price.
func aggregateReport(events []event.AgentEvent, by, view string) reportAgg {
	groups := map[string]*aggRow{}
	var order []string
	agg := reportAgg{skModels: map[string]int{}, methods: map[string]bool{}, currency: "USD"}
	for _, e := range events {
		m, ok := pickView(e, view)
		if !ok {
			agg.skipped++
			name := e.Model
			if name == "" {
				name = "(no model)"
			}
			agg.skModels[name]++
			continue
		}
		agg.total += m.Micros
		agg.n++
		agg.currency = m.Currency
		if e.Evidence.CostMethod != "" {
			agg.methods[e.Evidence.CostMethod] = true
		}
		w := m.Micros
		if w <= 0 {
			w = 1 // weight zero-cost events minimally so they don't dominate
		}
		agg.confWeighted += e.Evidence.ConfidenceScore * float64(w)
		agg.confBasis += w
		for _, c := range groupContributions(e, by, m.Micros) {
			if groups[c.key] == nil {
				groups[c.key] = &aggRow{key: c.key}
				order = append(order, c.key)
			}
			groups[c.key].micros += c.amount
			groups[c.key].count++
		}
	}
	rows := make([]*aggRow, 0, len(order))
	for _, k := range order {
		rows = append(rows, groups[k])
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].micros > rows[j].micros })
	agg.rows = rows
	return agg
}

// contribution is one event's cost charged to one group.
type contribution struct {
	key    string
	amount int64
}

// groupContributions maps an event to its (group, cost) contributions. Every
// dimension except "file" is 1:1 — one group, the full amount — so a grouped total
// reconciles trivially with the by-model total. "file" fans out: a turn that touched
// N files contributes to each, its cost split N ways (integer division, with the
// rounding remainder placed on the first — sorted — file) so the file rows still sum
// to the grand total. A turn that touched no files lands in "(no files)" with its
// full cost, so the split never drops or duplicates spend.
func groupContributions(e event.AgentEvent, by string, micros int64) []contribution {
	if by != "file" {
		return []contribution{{groupKey(e, by), micros}}
	}
	if len(e.Files) == 0 {
		return []contribution{{"(no files)", micros}}
	}
	n := int64(len(e.Files))
	base := micros / n
	out := make([]contribution, len(e.Files))
	for i, f := range e.Files {
		out[i] = contribution{key: f, amount: base}
	}
	out[0].amount += micros - base*n // exact remainder, deterministically on the first file
	return out
}

// confidence is the spend-weighted mean confidence (0 when nothing priced).
func (agg reportAgg) confidence() float64 {
	if agg.confBasis == 0 {
		return 0
	}
	return agg.confWeighted / float64(agg.confBasis)
}

// buildReportResult projects the aggregation into the JSON shape, attaching the
// per-group and total api-equivalent cost breakdown (recomputed from the pinned
// table) when the view is token-priced.
func buildReportResult(events []event.AgentEvent, by, view string, win window, eng *pricing.Engine) reportResult {
	agg := aggregateReport(events, by, view)
	res := reportResult{
		Period:      win.Label,
		Until:       win.Until,
		GroupBy:     by,
		View:        underscore(view),
		Currency:    agg.currency,
		Confidence:  round2(agg.confidence()),
		Groups:      make([]groupRow, 0, len(agg.rows)), // non-nil ⇒ marshals to [] when empty
		TotalMicros: agg.total,
		TotalUSD:    microsToUSD(agg.total),
		Count:       agg.n,
	}
	if !win.Since.IsZero() {
		s := win.Since
		res.Since = &s
	}
	if agg.n > 0 {
		res.Method = methodLabel(agg.methods)
	}
	for _, r := range agg.rows {
		pct := 0.0
		if agg.total != 0 {
			pct = float64(r.micros) / float64(agg.total) * 100
		}
		res.Groups = append(res.Groups, groupRow{
			Key:        r.key,
			CostMicros: r.micros,
			CostUSD:    microsToUSD(r.micros),
			Count:      r.count,
			Percent:    round2(pct),
		})
	}
	if agg.skipped > 0 {
		res.Unpriced = &unpriced{Count: agg.skipped, Models: agg.skModels}
	}
	if eng != nil && tokenPricedView(view) {
		attachComponents(&res, events, by, eng)
	}
	return res
}

// attachComponents decomposes each priced event into its four api-equivalent cost
// lines and sums them per group (keyed identically to the report's groups) and in
// total. Groups with no decomposable tokens are left without a breakdown rather
// than showing a misleading all-zero split.
func attachComponents(res *reportResult, events []event.AgentEvent, by string, eng *pricing.Engine) {
	perGroup := map[string]pricing.CostComponents{}
	var total pricing.CostComponents
	for _, e := range events {
		c, ok := eng.Components(e.Model, e.Tokens)
		if !ok {
			continue
		}
		total = addComponents(total, c)
		// The per-token-class breakdown reconciles with a row only for 1:1 groupings.
		// The file view splits a turn's cost across its files, so a per-file token
		// split would be a fabricated decomposition — omit it (the total still stands).
		if by != "file" {
			g := groupKey(e, by)
			perGroup[g] = addComponents(perGroup[g], c)
		}
	}
	for i := range res.Groups {
		if c, ok := perGroup[res.Groups[i].Key]; ok && c.Total().Micros > 0 {
			res.Groups[i].Components = toComponentsJSON(c)
		}
	}
	if total.Total().Micros > 0 {
		res.TotalComponents = toComponentsJSON(total)
	}
}

// tokenPricedView reports whether a view's total is the token×rate sum — the only
// views whose number the component breakdown reconciles with.
func tokenPricedView(view string) bool {
	switch underscore(view) {
	case "api_equivalent", "estimated":
		return true
	}
	return false
}

func addComponents(a, b pricing.CostComponents) pricing.CostComponents {
	add := func(x, y event.Money) event.Money {
		cur := x.Currency
		if cur == "" {
			cur = y.Currency
		}
		return event.Money{Micros: x.Micros + y.Micros, Currency: cur}
	}
	return pricing.CostComponents{
		Input:        add(a.Input, b.Input),
		Output:       add(a.Output, b.Output),
		CacheRead:    add(a.CacheRead, b.CacheRead),
		CacheWrite:   add(a.CacheWrite, b.CacheWrite),
		CacheWrite1h: add(a.CacheWrite1h, b.CacheWrite1h),
	}
}

func toComponentsJSON(c pricing.CostComponents) *componentsJSON {
	line := func(m event.Money) costLine { return costLine{Micros: m.Micros, USD: microsToUSD(m.Micros)} }
	return &componentsJSON{
		Input:        line(c.Input),
		Output:       line(c.Output),
		CacheRead:    line(c.CacheRead),
		CacheWrite:   line(c.CacheWrite),
		CacheWrite1h: line(c.CacheWrite1h),
	}
}

// emitReportJSON writes the report as indented JSON. Metered views only; the
// caller rejects amortized before reaching here.
func (a *App) emitReportJSON(events []event.AgentEvent, by, view string, win window, eng *pricing.Engine) int {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(buildReportResult(events, by, view, win, eng)); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	return 0
}

func microsToUSD(micros int64) float64 { return float64(micros) / 1e6 }

func round2(x float64) float64 { return math.Round(x*100) / 100 }

func underscore(s string) string { return strings.ReplaceAll(s, "-", "_") }
