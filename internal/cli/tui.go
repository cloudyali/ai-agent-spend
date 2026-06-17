//go:build !offline

// cmdTui wires the store + pricing engine into the interactive explorer
// (internal/tui). It lives behind `!offline` — exactly like the network refresh
// seam — because Bubble Tea transitively pulls net/url + net/netip; those are pure
// value/parsing packages (no dial, no http sink), and the default build already
// carries them via the disclosed `pricing refresh`, but the air-gapped `offline`
// build promises *zero* net/*, so the TUI is compiled out there (tui_offline.go).
// The default `doctor --network` story is unchanged: nothing here can phone home.
package cli

import (
	"flag"
	"fmt"

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
	plans := a.planSet()

	// Pre-filter the scrubbable windows here (so the tui needs no period parsing or
	// store/sqlite), and pre-compute each window's prorated plan fee so the tui can
	// allocate the amortized lens across sessions.
	reqWin, _ := parsePeriod(*periodSpec, now)
	periods := make([]tui.Period, 0, 4)
	startIdx := 0
	for _, spec := range []string{"today", "week", "month", "all"} {
		win, err := parsePeriod(spec, now)
		if err != nil {
			continue
		}
		if win.Label == reqWin.Label {
			startIdx = len(periods)
		}
		evs := eventsInWindow(all, win)
		amort, hasPlan := a.amortizedTotal(evs, win, plans)
		periods = append(periods, tui.Period{Label: win.Label, Events: evs, Amortized: amort, HasPlan: hasPlan})
	}

	if err := tui.Run(periods, startIdx, a.pricingEngine(), a.Out); err != nil {
		fmt.Fprintf(a.Err, "aispend: tui: %v\n", err)
		return 1
	}
	return 0
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

// maybePickPlan runs the interactive plan picker on a TTY and writes the choice to
// config — the "visual way" to set the subscription plan (so amortized/ROI work)
// without hand-editing TOML. Returns true when it handled the command; false (not
// a TTY) lets the caller fall back to the static plan list.
func (a *App) maybePickPlan() bool {
	if !isTTY(a.Out) {
		return false
	}
	current := ""
	if cfg, err := config.LoadAppConfig(a.Resolver.AppHome()); err == nil {
		current = cfg.Name
	}
	choices := make([]tui.PlanChoice, 0)
	for _, p := range config.Plans() {
		choices = append(choices, tui.PlanChoice{ID: p.ID, Label: p.Label, MonthlyUSD: p.MonthlyFeeUSD, Current: p.ID == current})
	}
	choices = append(choices, tui.PlanChoice{ID: "api", Current: current == "" || current == "api"})

	chosen, ok, err := tui.RunPlanPicker(choices, a.Out)
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return true
	}
	if !ok {
		fmt.Fprintln(a.Out, "plan unchanged.")
		return true
	}
	if err := config.SetDefaultPlan(a.Resolver.AppHome(), chosen); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return true
	}
	if chosen == "" || chosen == "api" {
		fmt.Fprintln(a.Out, "Plan set to API (no subscription) — the amortized view is off.")
	} else {
		fmt.Fprintf(a.Out, "Plan set to %s. Open `aispend tui` and press v for the amortized view (or `aispend report --view effective_allocated`).\n", chosen)
	}
	return true
}

// amortizedTotal sums each present provider's prorated subscription fee for the
// window — the same per-provider proration `report --view effective_allocated`
// uses. Returns (0, false) when no provider has an amortizable plan.
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
		if prorated, ok := proratePlan(plan, winSince, win.Until, legacyDays); ok {
			total += prorated.Micros
			hasPlan = true
		}
	}
	return total, hasPlan
}
