package lines

// Severity is a gauge's health, the single signal that drives "know before you run
// out": color across every surface (ANSI in the TUI/`today`, the hex color field in
// the HTTP API) and the emphasis on the run-out warning.
type Severity int

const (
	// SevOK is healthy — within limits and not on pace to breach.
	SevOK Severity = iota
	// SevWarn is a heads-up — high level or an adverse pace.
	SevWarn
	// SevCrit means act now — at/near the wall.
	SevCrit
)

// Threshold cutoffs as a fraction of the limit used. These are the level half of the
// agreed "level + pace" policy; pace adds to it in Classify.
//
// ponytail: fixed cutoffs (not yet user-configurable) — a config knob can overlay
// these when someone actually needs different thresholds.
const (
	WarnFrac = 0.80
	CritFrac = 0.95
)

// Classify combines level and pace into a severity (the "level + pace" policy):
//   - SevCrit when usage is at/over 95% of the limit;
//   - SevWarn when usage is at/over 80%, OR the current pace will breach the limit
//     before the window resets (the early "you'll run out" signal);
//   - SevOK otherwise.
//
// A non-positive limit is unrankable, so it reports SevOK rather than guessing.
func Classify(used, limit float64, breaches bool) Severity {
	if limit <= 0 {
		return SevOK
	}
	switch frac := used / limit; {
	case frac >= CritFrac:
		return SevCrit
	case frac >= WarnFrac || breaches:
		return SevWarn
	default:
		return SevOK
	}
}

// Hex is the severity's color for structured surfaces (the HTTP API's line.color and
// the web UI). OK is intentionally empty so a healthy gauge renders neutral.
func (s Severity) Hex() string {
	switch s {
	case SevWarn:
		return "#f59e0b" // amber
	case SevCrit:
		return "#ef4444" // red
	default:
		return ""
	}
}
