//go:build offline

// In the air-gapped `offline` build the interactive TUI is compiled out: Bubble
// Tea transitively pulls net/url + net/netip, and the offline build's whole point
// is a provably zero-net/* binary. `aispend tui` then reports its absence and
// points at the static surfaces, which carry the same numbers.
package cli

import "fmt"

func (a *App) cmdTui(_ []string) int {
	fmt.Fprintln(a.Err, "aispend: `tui` is unavailable in the offline build (it would link a terminal-UI dependency that pulls net/*). Use `aispend top`, `aispend today`, or `aispend report`.")
	return 1
}

// maybePickPlan is a no-op in the offline build (no TUI) — `plans` falls back to
// the static list; set the plan by editing ~/.aispend/config.toml.
func (a *App) maybePickPlan() bool { return false }
