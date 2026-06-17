package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

// Color is gated three ways — TTY, NO_COLOR, TERM=dumb — and a non-*os.File
// writer (a buffer or a pipe) is never a TTY, so output into a pipe or a test
// buffer is always plain. This keeps `aispend … | cat` free of escape codes.
func TestColorGating(t *testing.T) {
	if !colorAllowed(true, "", "xterm-256color") {
		t.Error("a TTY with no NO_COLOR and a real TERM should allow color")
	}
	if colorAllowed(false, "", "xterm") {
		t.Error("a non-TTY (pipe/file) must never colorize")
	}
	if colorAllowed(true, "1", "xterm") {
		t.Error("NO_COLOR set (any value) must disable color")
	}
	if colorAllowed(true, "", "dumb") {
		t.Error("TERM=dumb must disable color")
	}
	if isTTY(&bytes.Buffer{}) {
		t.Error("a bytes.Buffer is not a TTY")
	}
	if got := paint(false, "31", "x"); got != "x" {
		t.Errorf("paint disabled = %q, want identity %q", got, "x")
	}
	if got := paint(true, "31", "x"); !strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\x1b[0m") {
		t.Errorf("paint enabled = %q, want wrapped in SGR 31 + reset", got)
	}
}

// sparkline scales each value to the series max so a spike towers over the rest;
// an all-zero series is blank (never a misleading flat ridge) and any nonzero
// value shows at least the lowest block (a small-but-real hour stays visible).
func TestSparkline(t *testing.T) {
	if got := sparkline(nil); got != "" {
		t.Errorf("nil series = %q, want empty", got)
	}
	if got := sparkline([]int64{0, 0, 0}); got != "   " {
		t.Errorf("all-zero = %q, want three blanks", got)
	}
	r := []rune(sparkline([]int64{0, 1, 100}))
	if len(r) != 3 {
		t.Fatalf("len = %d, want 3", len(r))
	}
	if r[0] != ' ' {
		t.Errorf("zero cell = %q, want blank", string(r[0]))
	}
	if r[1] != '▁' {
		t.Errorf("tiny-but-nonzero cell = %q, want lowest block ▁", string(r[1]))
	}
	if r[2] != '█' {
		t.Errorf("peak cell = %q, want full block █", string(r[2]))
	}
}

// compositionBreakdown is the receipt's "where did this number come from" line:
// classes largest-first, zero classes omitted (never an asserted $0), and pipe-
// safe (no escape codes when color is off).
func TestCompositionBreakdown(t *testing.T) {
	c := pricing.CostComponents{
		Input:      event.USD(5_000_000),
		Output:     event.USD(25_000_000),
		CacheRead:  event.USD(5_000_000),
		CacheWrite: event.USD(6_250_000),
		// CacheWrite1h intentionally zero → must be omitted
	}
	got := compositionBreakdown(c, false)
	if strings.Contains(got, "\x1b[") {
		t.Errorf("color disabled must emit no ANSI: %q", got)
	}
	if strings.Contains(got, "cache-write-1h") {
		t.Errorf("a zero token class must be omitted, got: %q", got)
	}
	// output ($25) dominates and must be listed before input ($5).
	if oi, ii := strings.Index(got, "output"), strings.Index(got, "input"); oi < 0 || ii < 0 || oi > ii {
		t.Errorf("classes should be largest-first (output before input): %q", got)
	}
	if !strings.Contains(got, "61%") { // 25.0/41.25 ≈ 60.6 → 61%
		t.Errorf("output share should read ~61%%: %q", got)
	}
	// color on → class names tinted (ANSI present), content still there.
	if lit := compositionBreakdown(c, true); !strings.Contains(lit, "\x1b[") {
		t.Errorf("color enabled should tint class names: %q", lit)
	}
	// all-zero components → honest "no priced tokens", never a $0 line.
	if got := compositionBreakdown(pricing.CostComponents{}, false); !strings.Contains(got, "no priced tokens") {
		t.Errorf("empty components = %q, want a not-computable note", got)
	}
}
