// `aispend top` — the bridge from "my spend is high" to "where did it go". It ranks
// the priciest turns (default) or sessions in a calendar window and prints each with
// its id; to open a turn or session to its full evidence, launch the interactive
// explorer (`aispend`) and drill in. See design-documents/08-cli-tui-concept.md.
package cli

import (
	"flag"
	"fmt"
	"sort"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/store"
)

func (a *App) cmdTop(args []string) int {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	periodSpec := fs.String("period", "week", "calendar window (same grammar as `report`)")
	limit := fs.Int("limit", 10, "how many rows to list")
	bySessions := fs.Bool("sessions", false, "rank whole sessions instead of individual turns")
	noScan := fs.Bool("no-scan", false, "skip the automatic scan-on-launch; read the ledger as-is")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	win, err := parsePeriod(*periodSpec, a.Now())
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 2
	}
	if *limit < 1 {
		*limit = 10
	}
	a.scanOnLaunch(*noScan)
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	events, err := st.Query(store.Filter{Since: win.Since, Until: win.Until})
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	storeTotal := len(events)
	if len(events) == 0 {
		if all, e := st.Query(store.Filter{}); e == nil {
			storeTotal = len(all)
		}
	}
	a.renderTop(events, *limit, *bySessions, win.Label, storeTotal)
	return 0
}

func (a *App) renderTop(events []event.AgentEvent, limit int, bySessions bool, label string, storeTotal int) {
	color := useColor(a.Out)
	kind := "turns"
	if bySessions {
		kind = "sessions"
	}
	fmt.Fprintf(a.Out, "%s · %s · priciest %s\n\n", paint(color, cBold, "aispend top"), label, kind)

	if len(events) == 0 {
		if storeTotal > 0 {
			fmt.Fprintf(a.Out, "  no spend in %s (%d events stored — widen with --period all)\n", label, storeTotal)
		} else {
			fmt.Fprintln(a.Out, "  no spend recorded yet — run `aispend scan`")
		}
		return
	}

	if bySessions {
		a.renderTopSessions(events, limit)
		return
	}

	// Priciest turns. A "top spend" list must not assert a $0 row, so nil/zero-cost
	// turns are dropped rather than ranked.
	ranked := make([]event.AgentEvent, 0, len(events))
	for _, e := range events {
		if apiMicros(e) > 0 {
			ranked = append(ranked, e)
		}
	}
	if len(ranked) == 0 {
		fmt.Fprintf(a.Out, "  none of the %d turn(s) in %s carry an api-equivalent cost\n", len(events), label)
		return
	}
	sort.SliceStable(ranked, func(i, j int) bool { return apiMicros(ranked[i]) > apiMicros(ranked[j]) })
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	for i, e := range ranked {
		m := e.CostViews.APIEquivalent
		sess := "—"
		if e.SessionID != "" {
			sess = "s " + shortSession(e.SessionID)
		}
		fmt.Fprintf(a.Out, "  %2d  %9s  %s  %-9s %-12s %s in / %s out / %s cache-read\n",
			i+1, usd(m.Micros, m.Currency), e.EventID, shortModel(e.Model), sess,
			comma(e.Tokens.Input), comma(e.Tokens.Output), comma(e.Tokens.CacheRead))
	}
	fmt.Fprintln(a.Out, "\n  → open `aispend` (the explorer) and drill in for the full evidence · `--sessions` to rank sessions")
}

// renderTopSessions ranks whole sessions by summed api-equivalent. Sessionless
// turns aren't addressable as sessions, so they're excluded here.
func (a *App) renderTopSessions(events []event.AgentEvent, limit int) {
	type agg struct {
		id     string
		micros int64
		turns  int
		models map[string]bool
	}
	byID := map[string]*agg{}
	var order []string
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		g := byID[e.SessionID]
		if g == nil {
			g = &agg{id: e.SessionID, models: map[string]bool{}}
			byID[e.SessionID] = g
			order = append(order, e.SessionID)
		}
		g.turns++
		g.micros += apiMicros(e)
		if e.Model != "" {
			g.models[e.Model] = true
		}
	}
	if len(order) == 0 {
		fmt.Fprintln(a.Out, "  no addressable sessions in this window (turns carry no session id)")
		return
	}
	aggs := make([]*agg, 0, len(order))
	for _, id := range order {
		aggs = append(aggs, byID[id])
	}
	sort.SliceStable(aggs, func(i, j int) bool { return aggs[i].micros > aggs[j].micros })
	if len(aggs) > limit {
		aggs = aggs[:limit]
	}
	for i, g := range aggs {
		unit := "turns"
		if g.turns == 1 {
			unit = "turn"
		}
		fmt.Fprintf(a.Out, "  %2d  %9s  %-10s %d %s · %s\n",
			i+1, usd(g.micros, "USD"), shortSession(g.id), g.turns, unit, modelList(g.models))
	}
	fmt.Fprintln(a.Out, "\n  → open `aispend` (the explorer) and drill into a session for its receipt")
}
