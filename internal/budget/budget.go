// Package budget models an optional, informational spend ceiling measured against the
// api-equivalent cost view. aispend observes — it never enforces — so a budget is a
// pace gauge, not a gate: it answers "am I on track for the month?", not "stop." It is
// off by default and calendar-aligned (a monthly $ ceiling tracked against the current
// calendar month), and is distinct from a provider quota window (the hard wall).
package budget

import (
	"fmt"
	"time"
)

// Pace is a budget reading: period-to-date spend vs the ceiling, how far through the
// period we are, and the run-rate projection to period end. Money is integer micros.
type Pace struct {
	Spent           int64   // api-equivalent spend in the period so far (micros)
	Limit           int64   // the budget ceiling (micros)
	Projected       int64   // run-rate projection to period end (micros)
	ElapsedFraction float64 // 0..1 of the period elapsed at `now`
}

// ComputePace projects period-to-date spend to the period end at the current run rate:
// the elapsed fraction is now-start over the whole period (clamped to [0,1]), and the
// projection divides spend by it — so 43% spent at 20% elapsed projects ≈2.15× the
// budget. A zero-length period or zero elapsed degrades to projecting exactly spend.
func ComputePace(limitMicros, spentMicros int64, start, now, end time.Time) Pace {
	p := Pace{Spent: spentMicros, Limit: limitMicros}
	if total := end.Sub(start); total > 0 {
		ef := float64(now.Sub(start)) / float64(total)
		if ef < 0 {
			ef = 0
		}
		if ef > 1 {
			ef = 1
		}
		p.ElapsedFraction = ef
	}
	if p.ElapsedFraction > 0 {
		p.Projected = int64(float64(spentMicros)/p.ElapsedFraction + 0.5)
	} else {
		p.Projected = spentMicros
	}
	return p
}

// UsedFraction is spend / budget (0..∞); 0 when no budget is set.
func (p Pace) UsedFraction() float64 {
	if p.Limit <= 0 {
		return 0
	}
	return float64(p.Spent) / float64(p.Limit)
}

// PaceRatio is the run-rate projection / budget — how far over (>1) or under (<1) the
// current pace lands by period end. 0 when no budget.
func (p Pace) PaceRatio() float64 {
	if p.Limit <= 0 {
		return 0
	}
	return float64(p.Projected) / float64(p.Limit)
}

// OverPace reports whether the run-rate projects past the budget by period end.
func (p Pace) OverPace() bool { return p.Limit > 0 && p.Projected > p.Limit }

// Status is the glanceable verdict comparing the run rate to the budget: an
// over-pace multiple ("2.2× over pace"), "under", or "on track" near 1×. Empty when
// no budget is configured.
func (p Pace) Status() string {
	if p.Limit <= 0 {
		return ""
	}
	switch r := p.PaceRatio(); {
	case r > 1.05:
		return fmt.Sprintf("%.1f× over pace", r)
	case r < 0.8:
		return "under"
	default:
		return "on track"
	}
}

// MonthBounds returns the start (first of the month, 00:00) and end (first of next
// month) of now's calendar month, in now's location — so the budget tracks the user's
// local calendar month while the stored instants stay UTC.
func MonthBounds(now time.Time) (start, end time.Time) {
	start = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	end = start.AddDate(0, 1, 0)
	return start, end
}
