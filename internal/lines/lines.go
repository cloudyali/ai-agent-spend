// Package lines is aispend's shared presentation model. A provider's at-a-glance state
// is a Snapshot: a list of typed Lines (progress / text / badge) that every surface
// renders the same way — the static `today` glance, the TUI gauge, and the menu-bar app
// (cmd/aispend-bar).
//
// It is pure data plus pace math: no ANSI, no net/*, and — like package quota — no money
// is stored on a line (a progress line carries only what a surface shows). Field tags
// follow OpenUsage's line vocabulary (type, label, used, limit, format.kind, resetsAt,
// periodDurationMs, color, subtitle) with aispend-only additions (projection).
package lines

import "time"

// Kind is a progress line's unit, matching OpenUsage's format.kind.
type Kind string

const (
	// Percent is a 0..100 gauge (limit must be 100).
	Percent Kind = "percent"
	// Dollars is a monetary gauge (a display unit only — the value is not part of
	// the priced ledger).
	Dollars Kind = "dollars"
	// Count is an integer gauge; Format.Suffix names the unit (e.g. "credits").
	Count Kind = "count"
)

// Format describes how a progress line's used/limit are rendered.
type Format struct {
	Kind   Kind   `json:"kind"`
	Suffix string `json:"suffix,omitempty"`
}

// Projection is aispend's forecast for a progress line: where the current pace lands
// by the window's reset, and whether that crosses the limit first. It is the
// "pace, not level" signal — extrapolated from how much of the window has elapsed.
type Projection struct {
	// ProjectedUsed is the value reached by reset at the current run rate, in the
	// line's own unit (so a percent line projects a percent that may exceed 100).
	ProjectedUsed float64 `json:"projectedUsed"`
	// Breaches is true when the limit is reached before the window resets.
	Breaches bool `json:"breaches"`
	// ETASeconds is seconds until the limit is hit at the current rate; 0 when not
	// breaching, or when already at/over the limit ("now").
	ETASeconds int64 `json:"etaSeconds,omitempty"`
}

// Line is one row in a Snapshot. Type selects which fields are meaningful:
// "progress" uses Used/Limit/Format/ResetsAt/PeriodMs/Projection; "text" and
// "badge" use Value. Used/Limit are pointers so non-progress lines omit them (and a
// genuine 0%% gauge still serializes used:0).
type Line struct {
	Type     string     `json:"type"`
	Label    string     `json:"label"`
	Value    string     `json:"value,omitempty"`
	Used     *float64   `json:"used,omitempty"`
	Limit    *float64   `json:"limit,omitempty"`
	Format   *Format    `json:"format,omitempty"`
	ResetsAt *time.Time `json:"resetsAt,omitempty"`
	PeriodMs int64      `json:"periodDurationMs,omitempty"`
	Color    string     `json:"color,omitempty"`
	Subtitle string     `json:"subtitle,omitempty"`
	// Projection is aispend's forecast extension (absent on non-progress lines and
	// when usage can't yet be extrapolated).
	Projection *Projection `json:"projection,omitempty"`
}

// Snapshot is one provider's latest readings — the payload the gauges and the menu bar
// render from.
type Snapshot struct {
	ProviderID  string    `json:"providerId"`
	DisplayName string    `json:"displayName"`
	Plan        string    `json:"plan,omitempty"`
	Lines       []Line    `json:"lines"`
	FetchedAt   time.Time `json:"fetchedAt"`
	// Idle marks a provider with quota windows but no ledger spend today: the menu bar
	// collapses it to a single expandable row instead of a wall of zeros.
	Idle bool `json:"idle,omitempty"`
	// Trend is per-day api-equivalent spend in micros, oldest→newest (up to 7 days),
	// for the menu bar's Trend sparkline. Absent when there's no ledger spend.
	Trend []int64 `json:"trend,omitempty"`
}

// Project extrapolates current usage to the window's reset at the run rate observed
// so far. used/limit are in one unit (e.g. percent with limit 100); elapsed is how
// much of the window has passed and window is its full length.
//
// It is deliberately conservative: with no elapsed time (or no window) it cannot
// infer a rate, so it reports the current value and never invents a breach; usage
// already at/over the limit breaches "now" (ETA 0).
func Project(used, limit float64, elapsed, window time.Duration) Projection {
	if used >= limit { // already at the wall
		return Projection{ProjectedUsed: used, Breaches: true, ETASeconds: 0}
	}
	elapsedSec, windowSec := elapsed.Seconds(), window.Seconds()
	if elapsedSec <= 0 || windowSec <= 0 {
		return Projection{ProjectedUsed: used} // cannot extrapolate yet
	}
	projected := used * windowSec / elapsedSec
	rate := used / elapsedSec // units per second
	if rate <= 0 {
		return Projection{ProjectedUsed: projected} // no consumption → never runs out
	}
	timeToLimitSec := (limit - used) / rate
	remainingSec := windowSec - elapsedSec
	// Only trust a run-out projection once enough of the window has been observed. Early on a
	// tiny sample extrapolates wildly (1% in hour 1 of a week → an implied ~168%), which would
	// raise severity and let an idle provider hijack the menu-bar title. The projected value is
	// still reported for display — it just isn't flagged as a breach yet.
	const minObservedFrac = 0.10
	if timeToLimitSec <= remainingSec && elapsedSec >= minObservedFrac*windowSec {
		return Projection{ProjectedUsed: projected, Breaches: true, ETASeconds: int64(timeToLimitSec + 0.5)}
	}
	return Projection{ProjectedUsed: projected}
}
