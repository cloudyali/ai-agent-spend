package cli

import (
	"sort"
	"strings"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/termtext"
)

// Shared turn/session display helpers used across the CLI surfaces (top, today,
// report). The session receipt and per-event explain now live in the interactive
// TUI (internal/tui), so only these small formatters remain in the CLI.

// apiMicros reads a turn's api-equivalent cost lens (0 when not computed — never an
// asserted $0).
func apiMicros(e event.AgentEvent) int64 {
	if m := e.CostViews.APIEquivalent; m != nil {
		return m.Micros
	}
	return 0
}

// shortModel trims the noisy vendor prefix for display (claude-opus-4-8 →
// opus-4-8), leaving other ids (e.g. gpt-5.3-codex) intact. The id is lifted
// verbatim from the session log, so it is sanitized at this render boundary
// (terminal escape-sequence injection — CWE-150).
func shortModel(m string) string {
	if m == "" {
		return "(no model)"
	}
	return termtext.SanitizeLabel(strings.TrimPrefix(m, "claude-"))
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
