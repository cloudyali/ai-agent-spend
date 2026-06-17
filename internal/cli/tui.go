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
	// An interactive UI in a pipe or non-terminal would hang or garble; bail with a
	// pointer to the static surfaces instead.
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

	// Pre-filter the scrubbable windows here so the tui package needs no period
	// parsing (and stays free of the store/sqlite import graph).
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
		periods = append(periods, tui.Period{Label: win.Label, Events: eventsInWindow(all, win)})
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
