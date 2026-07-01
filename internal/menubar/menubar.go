// Package menubar is the platform-agnostic core of the aispend menu-bar app: it turns
// usage snapshots (built locally by the engine) into a menu model — a menu-bar title
// plus dropdown rows. It is pure Go — no cgo, no macOS — so the logic is unit-testable
// anywhere; the thin, macOS-only menuet glue that links the engine and paints the menu
// lives in cmd/aispend-bar behind a darwin build tag.
package menubar

import (
	"fmt"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/lines"
)

// State is the rendered menu model: the always-visible menu-bar Title plus the dropdown
// Items. It is what the menuet glue paints; keeping it a plain value keeps rendering
// testable without a Mac.
type State struct {
	Title string
	Items []Item
}

// Item is one dropdown row. Header marks a provider heading (the glue renders it bold
// and non-clickable).
type Item struct {
	Text   string
	Header bool
}

// Render turns provider snapshots into the menu model. The title surfaces the single
// worst gauge (highest severity, then highest usage) so the menu bar warns at a glance;
// the dropdown lists every provider's lines, including the dollarized-wall value.
func Render(snaps []lines.Snapshot, now time.Time) State {
	if len(snaps) == 0 {
		return State{
			Title: "AiSpend —",
			Items: []Item{{Text: "No AI-coding spend today yet."}},
		}
	}

	var items []Item
	worstSev := lines.SevOK
	worstUsed := -1.0
	worstTitle := ""
	spendTitle := ""
	for _, s := range snaps {
		head := s.DisplayName
		if s.Plan != "" {
			head += " · " + s.Plan
		}
		items = append(items, Item{Text: head, Header: true})
		for i := range s.Lines {
			ln := s.Lines[i]
			items = append(items, Item{Text: formatLine(ln, now)})
			switch {
			case ln.Type == "progress" && ln.Used != nil && ln.Limit != nil:
				breaches := ln.Projection != nil && ln.Projection.Breaches
				sev := lines.Classify(*ln.Used, *ln.Limit, breaches)
				if sev > worstSev || (sev == worstSev && *ln.Used > worstUsed) {
					worstSev, worstUsed = sev, *ln.Used
					worstTitle = fmt.Sprintf("%s %.0f%%", s.DisplayName, *ln.Used)
				}
			case ln.Type == "text" && ln.Label == "Today" && spendTitle == "":
				if d := dollarToken(ln.Value); d != "" {
					spendTitle = s.DisplayName + " " + d
				}
			}
		}
	}
	// Prefer the worst gauge (headroom is the sharper signal); fall back to today's
	// spend when there's no quota window; else a bare label.
	title := "AiSpend"
	switch {
	case worstTitle != "":
		title = worstTitle
	case spendTitle != "":
		title = spendTitle
	}
	return State{Title: title, Items: items}
}

// dollarToken returns the first whitespace-delimited token that looks like a dollar
// amount ("$24.47") in s, or "" if none — used to lift the spend figure into the
// menu-bar title.
func dollarToken(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.HasPrefix(f, "$") {
			return f
		}
	}
	return ""
}

// formatLine renders one line for the dropdown. Progress lines show level, reset
// countdown, and the pace forecast (derived from the structured projection the API
// sends); text/badge lines show "label: value".
func formatLine(ln lines.Line, now time.Time) string {
	if ln.Type != "progress" {
		if ln.Value != "" {
			return ln.Label + ": " + ln.Value
		}
		return ln.Label
	}
	var b strings.Builder
	b.WriteString(ln.Label)
	if ln.Used != nil {
		fmt.Fprintf(&b, " %.0f%%", *ln.Used)
	}
	if ln.ResetsAt != nil {
		b.WriteString(" · resets in " + humanDur(ln.ResetsAt.Sub(now)))
	}
	if p := ln.Projection; p != nil {
		switch {
		case p.Breaches:
			b.WriteString(" · on pace to run out in " + humanDur(time.Duration(p.ETASeconds)*time.Second))
		case p.ProjectedUsed > 0:
			fmt.Fprintf(&b, " · on pace: ~%.0f%% by reset", p.ProjectedUsed)
		}
	}
	return b.String()
}

// humanDur renders a coarse countdown ("1d 2h", "1h 30m", "5m", "<1m", "now"), dropping
// the finer unit once a coarser one is present so a glanceable row never reads "1d 2h 3m".
func humanDur(d time.Duration) string {
	if d <= 0 {
		return "now"
	}
	days := int(d / (24 * time.Hour))
	hours := int((d % (24 * time.Hour)) / time.Hour)
	mins := int((d % time.Hour) / time.Minute)
	switch {
	case days > 0 && hours > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case days > 0:
		return fmt.Sprintf("%dd", days)
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dh", hours)
	case mins > 0:
		return fmt.Sprintf("%dm", mins)
	default:
		return "<1m"
	}
}
