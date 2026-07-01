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

// Item is one dropdown row. The flags select how the glue paints it: Header is the bold
// provider heading, Hero is the emphasized ROI line (the wedge), Dim is a smaller
// secondary line, Separator is a divider, and Children is a submenu (a collapsed idle
// provider's detail, or the Trend sparkline).
type Item struct {
	Text      string
	Header    bool
	Hero      bool
	Dim       bool
	Separator bool
	Children  []Item
}

// Render turns provider snapshots into the menu model. The title surfaces the single
// worst gauge (highest severity, then highest usage) so the menu bar warns at a glance.
// The dropdown leads each provider with the wedge (ROI + cache saved), shows quota
// gauges with a bar, demotes today's spend, and collapses idle providers into a submenu.
func Render(snaps []lines.Snapshot, now time.Time) State {
	if len(snaps) == 0 {
		return State{
			Title: "AiSpend —",
			Items: []Item{{Text: "No AI-coding spend today yet."}},
		}
	}

	worstSev := lines.SevOK
	worstUsed := -1.0
	worstName, worstLabel := "", ""
	spendTitle := ""
	roiFirst, roiFirstName := "", ""
	roiByProv := map[string]string{}
	for _, s := range snaps {
		for i := range s.Lines {
			ln := s.Lines[i]
			switch {
			case ln.Type == "progress" && ln.Used != nil && ln.Limit != nil:
				breaches := ln.Projection != nil && ln.Projection.Breaches
				sev := lines.Classify(*ln.Used, *ln.Limit, breaches)
				if sev > worstSev || (sev == worstSev && *ln.Used > worstUsed) {
					worstSev, worstUsed = sev, *ln.Used
					worstName = s.DisplayName
					worstLabel = fmt.Sprintf("%s %.0f%%", s.DisplayName, *ln.Used)
				}
			case ln.Type == "text" && ln.Label == "ROI":
				if tok := roiToken(ln.Value); tok != "" {
					if _, seen := roiByProv[s.DisplayName]; !seen {
						roiByProv[s.DisplayName] = tok
					}
					if roiFirst == "" {
						roiFirst, roiFirstName = tok, s.DisplayName
					}
				}
			case ln.Type == "text" && ln.Label == "Today" && spendTitle == "":
				if d := dollarToken(ln.Value); d != "" {
					spendTitle = s.DisplayName + " " + d
				}
			}
		}
	}
	// Title priority: warn on the worst gauge first (flexing the ROI beside it when
	// known); with no live gauge, lead with the ROI rather than raw spend (the wedge,
	// not a bill); fall back to today's spend, else a bare label.
	title := "AiSpend"
	switch {
	case worstLabel != "":
		title = worstLabel
		if roi := roiByProv[worstName]; roi != "" {
			title += " · " + roi
		}
	case roiFirst != "":
		title = roiFirstName + " " + roiFirst
	case spendTitle != "":
		title = spendTitle
	}

	var items []Item
	for idx, s := range snaps {
		if idx > 0 {
			items = append(items, Item{Separator: true})
		}
		head := s.DisplayName
		if s.Plan != "" {
			head += " · " + s.Plan
		}
		// Idle: quota windows but nothing spent today — collapse to one row, keep the
		// detail one click away instead of a wall of zeros.
		if s.Idle {
			items = append(items, Item{Text: head + " — idle today", Header: true, Children: detailItems(s, now)})
			continue
		}
		items = append(items, Item{Text: head, Header: true})
		for i := range s.Lines {
			items = append(items, renderLineItem(s.Lines[i], now))
		}
		if len(s.Trend) >= 2 && sumInt64(s.Trend) > 0 {
			items = append(items, Item{Text: "Trend", Children: []Item{
				{Text: spark(s.Trend) + " · last 7 days", Dim: true},
			}})
		}
	}
	return State{Title: title, Items: items}
}

// detailItems renders a provider's lines for a submenu (used for a collapsed idle
// provider), reusing the same per-line formatting as the top level.
func detailItems(s lines.Snapshot, now time.Time) []Item {
	out := make([]Item, 0, len(s.Lines))
	for i := range s.Lines {
		out = append(out, renderLineItem(s.Lines[i], now))
	}
	return out
}

// renderLineItem maps one snapshot line to a dropdown row, applying the After hierarchy:
// ROI is the Hero, Cache saved and Today are Dim, progress lines carry a bar, and any
// other text line is a plain row.
func renderLineItem(ln lines.Line, now time.Time) Item {
	if ln.Type == "progress" {
		return Item{Text: formatProgress(ln, now)}
	}
	switch ln.Label {
	case "ROI":
		return Item{Text: ln.Value, Hero: true}
	case "Cache saved", "Today":
		return Item{Text: formatText(ln), Dim: true}
	default:
		return Item{Text: formatText(ln)}
	}
}

// formatText renders a non-progress line as "label: value" (or just the label).
func formatText(ln lines.Line) string {
	if ln.Value != "" {
		return ln.Label + ": " + ln.Value
	}
	return ln.Label
}

// formatProgress renders a quota gauge: label, a unicode bar, the level, the reset
// countdown, and the pace forecast (from the structured projection).
func formatProgress(ln lines.Line, now time.Time) string {
	var b strings.Builder
	b.WriteString(ln.Label)
	if ln.Used != nil {
		fmt.Fprintf(&b, " %s %.0f%%", bar(*ln.Used), *ln.Used)
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

// bar renders pct (0..100) as an 8-cell filled/empty gauge, clamped at both ends.
func bar(pct float64) string {
	const cells = 8
	n := int(pct/100*cells + 0.5)
	if n < 0 {
		n = 0
	}
	if n > cells {
		n = cells
	}
	return strings.Repeat("▓", n) + strings.Repeat("░", cells-n)
}

// spark renders a series as a unicode sparkline scaled to its own max; an all-zero
// series is all lows.
func spark(vals []int64) string {
	ramp := []rune("▁▂▃▄▅▆▇█")
	var peak int64
	for _, v := range vals {
		if v > peak {
			peak = v
		}
	}
	var b strings.Builder
	for _, v := range vals {
		idx := 0
		if peak > 0 {
			idx = int(float64(v)/float64(peak)*float64(len(ramp)-1) + 0.5)
		}
		b.WriteRune(ramp[idx])
	}
	return b.String()
}

func sumInt64(vals []int64) int64 {
	var s int64
	for _, v := range vals {
		s += v
	}
	return s
}

// roiToken returns the leading token of an ROI line's value ("31×" from
// "31× vs plan (...)") for the menu-bar title.
func roiToken(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
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
