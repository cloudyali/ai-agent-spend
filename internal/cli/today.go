// `aispend today` — the arbitrage-first daily glance, the founder's primary view
// (07/08-*-concept.md). It leads with value, not a bare total: the api-equivalent
// spend, the subscription ROI (plan $/day vs metered), what prompt caching saved,
// a turns/sessions/top-model strip, and an hourly spike bar that surfaces the
// 2am session that looped (09-session-view.md). Hand-rolled, zero-dependency,
// degrades to plain ASCII off a TTY.
package cli

import (
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/config"
	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
	"github.com/agentspend/ai-agent-spend/internal/store"
)

func (a *App) cmdToday(args []string) int {
	fs := flag.NewFlagSet("today", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	now := a.Now()
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	events, err := st.Query(store.Filter{Since: startOfDay(now), Until: now})
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	// Only when today is empty do we learn the store total — so we can tell "scan
	// first" apart from "you just haven't coded today" without paying for the count
	// on the common path.
	storeTotal := len(events)
	if len(events) == 0 {
		if all, e := st.Query(store.Filter{}); e == nil {
			storeTotal = len(all)
		}
	}
	a.renderToday(events, now, a.planSet(), storeTotal, a.pricingEngine())
	return 0
}

func (a *App) renderToday(events []event.AgentEvent, now time.Time, plans config.PlanSet, storeTotal int, eng *pricing.Engine) {
	color := useColor(a.Out)
	fmt.Fprintf(a.Out, "%s · %s\n\n", paint(color, cBold, "aispend today"), now.Format("Mon Jan 2"))

	if len(events) == 0 {
		if storeTotal > 0 {
			fmt.Fprintf(a.Out, "  no AI-coding spend recorded today (%d events stored — try `aispend report --period yesterday`)\n", storeTotal)
		} else {
			fmt.Fprintln(a.Out, "  no AI-coding spend recorded today yet — run `aispend scan`")
		}
		return
	}

	var apiTotal, withoutTotal int64
	priced := 0
	sessions := map[string]bool{}
	providers := map[string]bool{}
	hourMax := now.Hour()
	hours := make([]int64, hourMax+1) // 00:00 .. the current hour (future hours don't exist yet)
	for _, e := range events {
		if m := e.CostViews.APIEquivalent; m != nil {
			apiTotal += m.Micros
			if h := e.TSStart.In(now.Location()).Hour(); h >= 0 && h <= hourMax {
				hours[h] += m.Micros
			}
		}
		if w, ok := eng.WithoutCache(e.Model, e.Tokens); ok {
			withoutTotal += w.Micros
		}
		if _, ok := eng.Components(e.Model, e.Tokens); ok {
			priced++
		}
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
		if e.Provider != "" {
			providers[e.Provider] = true
		}
	}

	// Headline: api-equivalent + the arbitrage clause (plan $/day · ROI).
	head := "not computable"
	if priced > 0 {
		head = usd(apiTotal, "USD")
	}
	line := fmt.Sprintf("  %s api-equivalent", paint(color, cBold, head))
	dailyFee, uncovered := a.dailyPlanFee(providers, plans)
	if dailyFee > 0 && apiTotal > 0 {
		line += fmt.Sprintf("  ·  plan %s/day · %s ROI", usd(dailyFee, "USD"), roiStr(float64(apiTotal)/float64(dailyFee)))
	}
	fmt.Fprintln(a.Out, line)

	// Cache savings — the visible half of the wedge.
	if withoutTotal > 0 && withoutTotal > apiTotal {
		saved := withoutTotal - apiTotal
		pct := float64(saved) / float64(withoutTotal) * 100
		fmt.Fprintf(a.Out, "  cache saved ~%s (%.0f%%)  %s\n", usd(saved, "USD"), pct, bar(pct))
	}

	// Turns · sessions · top model by spend.
	models := aggregateReport(events, "model", "api_equivalent")
	stat := fmt.Sprintf("  %d turns · %d sessions", len(events), len(sessions))
	if len(models.rows) > 0 && models.total > 0 {
		top := models.rows[0]
		stat += fmt.Sprintf(" · %s %.0f%%", shortModel(top.key), float64(top.micros)/float64(models.total)*100)
	}
	fmt.Fprintln(a.Out, stat)

	// Hourly spike bar — catch the hour that ran away.
	if apiTotal > 0 {
		peakH, peakV := 0, int64(-1)
		for h, v := range hours {
			if v > peakV {
				peakH, peakV = h, v
			}
		}
		if peakV > 0 {
			fmt.Fprintf(a.Out, "  by hour  %s  peak %02d:00 · %s\n", sparkline(hours), peakH, usd(peakV, "USD"))
		}
	}

	// Honesty footer: how to get an ROI, or which providers it omits.
	if dailyFee == 0 {
		fmt.Fprintln(a.Out, "  (set a subscription plan for ROI — see `aispend plans`)")
	} else if len(uncovered) > 0 {
		fmt.Fprintf(a.Out, "  note: %s not in the ROI (no plan set)\n", joinProviderLabels(uncovered))
	}
}

// dailyPlanFee sums each present provider's prorated daily subscription fee
// (monthly ÷ 30). Providers with no subscription plan are returned as uncovered so
// the ROI can say what it leaves out rather than silently overstating itself.
func (a *App) dailyPlanFee(providers map[string]bool, plans config.PlanSet) (int64, []string) {
	keys := make([]string, 0, len(providers))
	for p := range providers {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var fee int64
	var uncovered []string
	for _, p := range keys {
		if f, ok := pricing.ProratedFee(toPricingPlan(plans.For(p)), 1); ok {
			fee += f.Micros
		} else {
			uncovered = append(uncovered, p)
		}
	}
	return fee, uncovered
}

// roiStr formats a plan-ROI multiple: whole numbers once it's a runaway win
// (≥10×), one decimal while it's still close to break-even.
func roiStr(roi float64) string {
	if roi >= 10 {
		return fmt.Sprintf("%.0f×", roi)
	}
	return fmt.Sprintf("%.1f×", roi)
}

func joinProviderLabels(ps []string) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = providerLabel(p)
	}
	return strings.Join(out, ", ")
}
