// Rendering helpers shared by the rich static surfaces (`today`, the session
// receipt). Hand-rolled, zero-dependency ANSI — no Bubble Tea, no lipgloss, no
// x/term — so the binary stays the pure-Go, provably-offline artifact `doctor
// --network` asserts. Everything degrades to plain ASCII off a TTY, under
// NO_COLOR, or with TERM=dumb, and never bleeds an escape code into a pipe.
// See design-documents/08-cli-tui-concept.md (craft) and 09-session-view.md.
package cli

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

// --- color gating (stdlib-only; no golang.org/x/term) ---

// isTTY reports whether w is a character device — an interactive terminal. A
// *bytes.Buffer (tests), an os.Pipe, or a redirected regular file is not, so
// piped and captured output is always plain. Uses os.ModeCharDevice; no x/term.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// colorAllowed is the pure policy, isolated for testing: color only on a TTY,
// never when NO_COLOR is set to any value (the no-color.org convention) or the
// terminal advertises itself as "dumb".
func colorAllowed(tty bool, noColor, term string) bool {
	return tty && noColor == "" && term != "dumb"
}

// useColor resolves the policy for a writer against the live environment.
func useColor(w io.Writer) bool {
	return colorAllowed(isTTY(w), os.Getenv("NO_COLOR"), os.Getenv("TERM"))
}

// ANSI SGR codes for the token-class color language shared with the web UI
// (08-cli-tui-concept.md): cache-read blue, cache-write amber, output teal,
// input purple. Applied only when color is enabled.
const (
	cInput      = "35" // purple
	cOutput     = "36" // teal / cyan
	cCacheRead  = "34" // blue
	cCacheWrite = "33" // amber / yellow
	cBold       = "1"
	cDim        = "2"
)

// paint wraps s in an SGR code when enabled, else returns it unchanged — so a
// pipe or a NO_COLOR terminal never sees an escape sequence.
func paint(enabled bool, code, s string) string {
	if !enabled || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// --- intensity sparkline (block ramp) ---

var blockRamp = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders one cell per value as a block glyph scaled to the series max,
// so the hour that burned 10× the rest towers over them — the spike-finder from
// 09-session-view.md. An all-zero series is blank cells (never a misleading flat
// ridge); any nonzero value shows at least the lowest block so a small-but-real
// hour isn't invisible.
func sparkline(vals []int64) string {
	if len(vals) == 0 {
		return ""
	}
	var max int64
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for _, v := range vals {
		if max <= 0 || v <= 0 {
			b.WriteRune(blockRamp[0])
			continue
		}
		idx := int(v * 8 / max)
		if idx < 1 {
			idx = 1
		}
		if idx > 8 {
			idx = 8
		}
		b.WriteRune(blockRamp[idx])
	}
	return b.String()
}

// --- composition by token class ---

// compositionBreakdown renders an api-equivalent cost as an ordered,
// percentage-annotated breakdown by token class — the receipt's "where did this
// number come from" line. Classes are largest-first; zero classes are omitted
// (never an asserted $0); class names are tinted when color is on. Pipe-safe:
// no escape sequences when color is off.
func compositionBreakdown(c pricing.CostComponents, color bool) string {
	classes := []struct {
		name string
		code string
		m    event.Money
	}{
		{"cache-read", cCacheRead, c.CacheRead},
		{"cache-write", cCacheWrite, c.CacheWrite},
		{"cache-write-1h", cCacheWrite, c.CacheWrite1h},
		{"output", cOutput, c.Output},
		{"input", cInput, c.Input},
	}
	total := c.Total().Micros
	// Largest-first; stable so ties fall back to the fixed class order above.
	sort.SliceStable(classes, func(i, j int) bool { return classes[i].m.Micros > classes[j].m.Micros })
	var parts []string
	for _, cl := range classes {
		if cl.m.Micros <= 0 {
			continue
		}
		pct := 0.0
		if total > 0 {
			pct = float64(cl.m.Micros) / float64(total) * 100
		}
		parts = append(parts, fmt.Sprintf("%s %s (%.0f%%)", paint(color, cl.code, cl.name), usd(cl.m.Micros, cl.m.Currency), pct))
	}
	if len(parts) == 0 {
		return "(no priced tokens)"
	}
	return strings.Join(parts, " · ")
}
