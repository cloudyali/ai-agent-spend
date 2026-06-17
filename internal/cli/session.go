// The session view: `explain` one level up. A session is the natural unit of an
// AI-coding sitting — you sat down, worked, stopped — so its receipt answers the
// question the per-event receipt can't: what did *this sitting* cost? Window,
// total, per-token-class composition, the arbitrage line, and the top costly
// turns as drillable ids. Reached via the `explain session:<id|max|last>`
// selector grammar, never by typing a hash. See design-documents/09-session-view.md.
package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

// resolveSessionID picks one session from the stored events for a selector:
// "max" (priciest by summed api-equivalent), "last" (most recent by latest turn),
// or a sessionId prefix. Sessionless turns aren't addressable. Returns the full
// session id or an error that names the problem (empty/none/no-match/ambiguous).
func resolveSessionID(events []event.AgentEvent, sel string) (string, error) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return "", fmt.Errorf("explain session:<id|max|last> — empty selector")
	}
	type agg struct {
		micros int64
		last   time.Time
	}
	sessions := map[string]*agg{}
	var order []string // insertion order (events arrive TSStart-ascending) → deterministic ties
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		a := sessions[e.SessionID]
		if a == nil {
			a = &agg{}
			sessions[e.SessionID] = a
			order = append(order, e.SessionID)
		}
		if m := e.CostViews.APIEquivalent; m != nil {
			a.micros += m.Micros
		}
		if e.TSStart.After(a.last) {
			a.last = e.TSStart
		}
	}
	if len(sessions) == 0 {
		return "", fmt.Errorf("no sessions found (run `aispend scan` first)")
	}

	switch sel {
	case "max":
		best, bestM := "", int64(-1)
		for _, sid := range order {
			if sessions[sid].micros > bestM {
				best, bestM = sid, sessions[sid].micros
			}
		}
		return best, nil
	case "last":
		best := ""
		var bestT time.Time
		for _, sid := range order {
			if best == "" || sessions[sid].last.After(bestT) {
				best, bestT = sid, sessions[sid].last
			}
		}
		return best, nil
	default:
		var matches []string
		for _, sid := range order {
			if strings.HasPrefix(sid, sel) {
				matches = append(matches, sid)
			}
		}
		switch len(matches) {
		case 0:
			return "", fmt.Errorf("no session matches %q (try `aispend report --by session` to list them)", sel)
		case 1:
			return matches[0], nil
		default:
			return "", fmt.Errorf("%q matches %d sessions — use a longer prefix", sel, len(matches))
		}
	}
}

// sessionEvents returns the events of one session, oldest turn first.
func sessionEvents(events []event.AgentEvent, sid string) []event.AgentEvent {
	var out []event.AgentEvent
	for _, e := range events {
		if e.SessionID == sid {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TSStart.Before(out[j].TSStart) })
	return out
}

// renderSessionReceipt prints the session as a receipt. Totals/composition/
// arbitrage are computed only over priceable turns; a session with no priceable
// turn reads "not computable", never an asserted $0 (the nil-cost discipline).
func (a *App) renderSessionReceipt(events []event.AgentEvent, eng *pricing.Engine) {
	if len(events) == 0 {
		fmt.Fprintln(a.Out, "  (no turns in this session)")
		return
	}
	color := useColor(a.Out)
	sid := events[0].SessionID

	start, end := events[0].TSStart, events[0].TSStart
	var total, without int64
	var comps pricing.CostComponents
	priced := 0
	models := map[string]bool{}
	for _, e := range events {
		if e.TSStart.Before(start) {
			start = e.TSStart
		}
		te := e.TSEnd
		if te.IsZero() {
			te = e.TSStart
		}
		if te.After(end) {
			end = te
		}
		if e.Model != "" {
			models[e.Model] = true
		}
		if m := e.CostViews.APIEquivalent; m != nil {
			total += m.Micros
		}
		if c, ok := eng.Components(e.Model, e.Tokens); ok {
			comps = addComponents(comps, c)
			priced++
		}
		if w, ok := eng.WithoutCache(e.Model, e.Tokens); ok {
			without += w.Micros
		}
	}

	header := paint(color, cBold, "session "+shortSession(sid))
	fmt.Fprintf(a.Out, "%s  ·  %s  ·  %s → %s (%s)\n",
		header, providerLabel(events[0].Provider),
		start.Format("2006-01-02 15:04"), end.Format("15:04"), humanizeDuration(end.Sub(start)))

	totalStr := "not computable"
	if priced > 0 {
		totalStr = usd(total, "USD")
	}
	fmt.Fprintf(a.Out, "  %-12s %s · %d turns · %s\n", "total", totalStr, len(events), modelList(models))

	if priced > 0 {
		fmt.Fprintf(a.Out, "  %-12s %s\n", "composition", compositionBreakdown(comps, color))
		if without > 0 {
			fmt.Fprintf(a.Out, "  %-12s %s\n", "arbitrage", arbitrageLine(without, without-total))
		}
	}

	if top := topTurns(events, 5); len(top) > 0 {
		fmt.Fprintln(a.Out, "  top turns")
		for _, e := range top {
			amt := "n/a"
			if m := e.CostViews.APIEquivalent; m != nil {
				amt = usd(m.Micros, m.Currency)
			}
			fmt.Fprintf(a.Out, "    %9s  %s  %-9s %s in / %s out / %s cache-read\n",
				amt, e.EventID, shortModel(e.Model), comma(e.Tokens.Input), comma(e.Tokens.Output), comma(e.Tokens.CacheRead))
		}
	}
	fmt.Fprintf(a.Out, "  %-12s %d turns · local_only · offline\n", "evidence", len(events))
}

// arbitrageLine renders the per-session subscription-arbitrage line. Savings can
// be negative (1-hour cache writes cost 2× input, dearer than not caching) — said
// honestly rather than hidden.
func arbitrageLine(without, savings int64) string {
	pct := 0.0
	if without > 0 {
		pct = float64(savings) / float64(without) * 100
	}
	if savings >= 0 {
		return fmt.Sprintf("without cache ≈ %s · saved %.0f%%", usd(without, "USD"), pct)
	}
	return fmt.Sprintf("without cache ≈ %s · cost +%.0f%% (cache writes exceeded reads)", usd(without, "USD"), -pct)
}

// topTurns returns up to n turns of a session, priciest first (api-equivalent;
// nil-cost turns sort last). Stable, so equal-cost turns keep chronological order.
func topTurns(events []event.AgentEvent, n int) []event.AgentEvent {
	cp := make([]event.AgentEvent, len(events))
	copy(cp, events)
	sort.SliceStable(cp, func(i, j int) bool { return apiMicros(cp[i]) > apiMicros(cp[j]) })
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp
}

func apiMicros(e event.AgentEvent) int64 {
	if m := e.CostViews.APIEquivalent; m != nil {
		return m.Micros
	}
	return 0
}

// modelList renders the distinct models in a session as short names, sorted.
func modelList(set map[string]bool) string {
	if len(set) == 0 {
		return "(no model)"
	}
	xs := make([]string, 0, len(set))
	for m := range set {
		xs = append(xs, shortModel(m))
	}
	sort.Strings(xs)
	return strings.Join(xs, ", ")
}

// shortModel trims the noisy vendor prefix for display (claude-opus-4-8 →
// opus-4-8), leaving other ids (e.g. gpt-5.3-codex) intact.
func shortModel(m string) string {
	if m == "" {
		return "(no model)"
	}
	return strings.TrimPrefix(m, "claude-")
}

// humanizeDuration renders a wall-clock span compactly: seconds under a minute,
// minutes under an hour, hours+minutes under a day, else days+hours. It is the
// span of the sitting (first turn → last turn), not active time — labelled as such
// so a resumed session with idle gaps never reads as misleadingly busy.
func humanizeDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}
