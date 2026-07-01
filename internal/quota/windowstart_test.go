package quota

import (
	"testing"
	"time"
)

func TestSampleWindowStart(t *testing.T) {
	reset := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	wantWeekly := reset.Add(-7 * 24 * time.Hour)

	// Codex carries window_minutes explicitly.
	cx := Sample{Window: WindowWeekly, WindowMinutes: 10080, ResetsAt: reset}
	if start, ok := cx.WindowStart(); !ok || !start.Equal(wantWeekly) {
		t.Errorf("codex weekly start = %v (ok=%v), want %v", start, ok, wantWeekly)
	}

	// Claude reports no minutes → nominal length by window kind.
	cl := Sample{Window: WindowWeekly, ResetsAt: reset}
	if start, ok := cl.WindowStart(); !ok || !start.Equal(wantWeekly) {
		t.Errorf("claude weekly start = %v (ok=%v), want %v", start, ok, wantWeekly)
	}

	// An unrecognized window can't be placed → no start (caller won't query a bogus range).
	if _, ok := (Sample{Window: Window("mystery"), ResetsAt: reset}).WindowStart(); ok {
		t.Error("unknown window should report no start")
	}
}
