//go:build !offline

// cmdTui wires the store + pricing engine into the interactive explorer
// (internal/tui), including the in-explorer plan picker (which persists to config
// and recomputes amortization live). It lives behind `!offline` — exactly like the
// network refresh seam — because Bubble Tea transitively pulls net/url + net/netip;
// those are pure value/parsing packages (no dial, no http sink), and the default
// build already carries them via the disclosed `pricing refresh`, but the
// air-gapped `offline` build promises *zero* net/*, so the TUI is compiled out
// there (tui_offline.go). The default `doctor --network` story is unchanged.
package cli

import (
	"flag"
	"fmt"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/config"
	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/store"
	"github.com/agentspend/ai-agent-spend/internal/tui"
)

func (a *App) cmdTui(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	periodSpec := fs.String("period", "week", "initial calendar window (same grammar as `report`)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !isTTY(a.Out) {
		fmt.Fprintln(a.Err, "aispend tui needs an interactive terminal; try `aispend top` or `aispend report`")
		return 1
	}

	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	all, err := st.Query(store.Filter{})
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	now := a.Now()
	// The scrubbable windows, shortest → longest (←/→ in the TUI walks them).
	specs := []string{"today", "week", "this month", "last month", "quarter", "last quarter", "this year", "last year", "all"}

	// buildPeriods pre-filters each window and pre-computes its prorated plan fee.
	// It reads the plan set fresh each call so the in-TUI picker (which rewrites
	// config) recomputes amortization live.
	buildPeriods := func() []tui.Period {
		plans := a.planSet()
		ps := make([]tui.Period, 0, len(specs))
		for _, spec := range specs {
			win, err := parsePeriod(spec, now)
			if err != nil {
				continue
			}
			evs := eventsInWindow(all, win)
			amort, hasPlan := a.amortizedTotal(evs, win, plans)
			ps = append(ps, tui.Period{Label: win.Label, Events: evs, Amortized: amort, HasPlan: hasPlan})
		}
		return ps
	}

	periods := buildPeriods()
	startIdx := 0
	if reqWin, err := parsePeriod(*periodSpec, now); err == nil {
		for i, p := range periods {
			if p.Label == reqWin.Label {
				startIdx = i
			}
		}
	}

	// setPlan persists the picker's choice (id + billing-cycle start) and returns
	// recomputed periods so the amortized lens updates without leaving the TUI.
	setPlan := func(id string, start time.Time) []tui.Period {
		if err := config.SetDefaultPlan(a.Resolver.AppHome(), id, start); err != nil {
			fmt.Fprintf(a.Err, "aispend: %v\n", err)
		}
		return buildPeriods()
	}

	m := tui.New(periods, startIdx, a.pricingEngine()).WithPlanPicker(a.planChoices(), now, setPlan)
	if err := tui.RunModel(m, a.Out); err != nil {
		fmt.Fprintf(a.Err, "aispend: tui: %v\n", err)
		return 1
	}
	return 0
}

// planChoices builds the picker list from the seeded plans + the configured one,
// with an explicit "API / no subscription" option appended.
func (a *App) planChoices() []tui.PlanChoice {
	current := ""
	if cfg, err := config.LoadAppConfig(a.Resolver.AppHome()); err == nil {
		current = cfg.Name
	}
	cs := make([]tui.PlanChoice, 0)
	for _, p := range config.Plans() {
		cs = append(cs, tui.PlanChoice{ID: p.ID, Label: p.Label, MonthlyUSD: p.MonthlyFeeUSD, Current: p.ID == current})
	}
	cs = append(cs, tui.PlanChoice{ID: "api", Current: current == "" || current == "api"})
	return cs
}

// maybePickPlan runs the standalone picker for `aispend plans` on a TTY, writing
// the choice to config. Returns true when handled; false (non-TTY) lets the caller
// fall back to the static plan list.
func (a *App) maybePickPlan() bool {
	if !isTTY(a.Out) {
		return false
	}
	chosen, start, ok, err := tui.RunPlanPicker(a.planChoices(), a.Now(), a.Out)
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return true
	}
	if !ok {
		fmt.Fprintln(a.Out, "plan unchanged.")
		return true
	}
	if err := config.SetDefaultPlan(a.Resolver.AppHome(), chosen, start); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return true
	}
	if chosen == "" || chosen == "api" {
		fmt.Fprintln(a.Out, "Plan set to API (no subscription) — the amortized view is off.")
	} else {
		fmt.Fprintf(a.Out, "Plan set to %s (start %s). Open `aispend tui` and press v for the amortized view (or `aispend report --view effective_allocated`).\n", chosen, start.Format("2006-01-02"))
	}
	return true
}

// eventsInWindow returns the events whose start falls in win (zero Since = no lower
// bound), mirroring the store filter the report path uses.
func eventsInWindow(all []event.AgentEvent, win window) []event.AgentEvent {
	out := make([]event.AgentEvent, 0, len(all))
	for _, e := range all {
		if !win.Since.IsZero() && e.TSStart.Before(win.Since) {
			continue
		}
		if !win.Until.IsZero() && e.TSStart.After(win.Until) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// amortizedTotal sums each present provider's prorated subscription fee for the
// window — the same per-provider proration `report --view effective_allocated`
// uses (cycle-aware via the plan's start-date anchor). Returns (0, false) when no
// provider has an amortizable plan.
func (a *App) amortizedTotal(events []event.AgentEvent, win window, plans config.PlanSet) (int64, bool) {
	provs := map[string]bool{}
	for _, e := range events {
		if e.Provider != "" {
			provs[e.Provider] = true
		}
	}
	winSince := win.Since
	legacyDays := 0
	if win.Since.IsZero() { // --all: amortize over the data's own span
		legacyDays = spanDays(events)
		winSince = spanStart(events)
	} else if d := int(win.Until.Sub(win.Since).Hours() / 24); d > 0 {
		legacyDays = d
	}
	var total int64
	hasPlan := false
	for prov := range provs {
		plan := toPricingPlan(plans.For(prov))
		// Amortizing "all time" (no lower bound) without a billing anchor would
		// flat-prorate the fee across the ENTIRE event span — which a single stray
		// or corrupt timestamp can blow up into decades of plan fees. Require a
		// plan start for the unbounded window; with one, AmortizeSubscription clamps
		// to it (robust to bad timestamps). Bounded windows (this month, …) are fine.
		if win.Since.IsZero() && plan.StartDate.IsZero() {
			continue
		}
		if prorated, ok := proratePlan(plan, winSince, win.Until, legacyDays); ok {
			total += prorated.Micros
			hasPlan = true
		}
	}
	return total, hasPlan
}
