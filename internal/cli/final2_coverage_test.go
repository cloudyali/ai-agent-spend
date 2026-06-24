package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// budgetPace excludes turns with no api-equivalent from the spend sum and discloses
// their provider; renderBudget prints that "excluded" note.
func TestBudgetPace_UncoveredProvider(t *testing.T) {
	home := setupHome(t)
	run(t, "scan")
	if err := config.SetBudget(filepath.Join(home, ".aispend"), 50_000_000); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	a := &App{Resolver: platform.Detect(), Now: func() time.Time { return now }, Out: nil, Err: nil}
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	// A nil-cost turn with a provider, dated within the month → counted as uncovered.
	if err := st.Upsert([]event.AgentEvent{{EventID: "nc", Provider: "codex", Model: "m",
		TSStart: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)}}); err != nil {
		t.Fatal(err)
	}

	_, uncovered, ok := a.budgetPace(st, now)
	if !ok || len(uncovered) == 0 {
		t.Fatalf("a nil-cost provider turn should be disclosed as uncovered, got ok=%v unc=%v", ok, uncovered)
	}
	var buf strings.Builder
	a.Out = &buf
	a.renderBudget(st, now)
	if !strings.Contains(buf.String(), "excluded from the budget") {
		t.Errorf("renderBudget should note the excluded provider, got: %q", buf.String())
	}
}

// hourlyBuckets sums api-equivalent spend per local clock-hour, skips nil-cost and
// zero-timestamp turns, and clamps to a single bucket when now precedes the window start.
func TestHourlyBuckets_Direct(t *testing.T) {
	m := event.USD(100)
	start := time.Date(2026, 6, 17, 8, 0, 0, 0, time.UTC)
	now := time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)
	events := []event.AgentEvent{
		{CostViews: event.CostViews{APIEquivalent: &m}, TSStart: time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)},
		{TSStart: time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)}, // nil cost → skipped
		{CostViews: event.CostViews{APIEquivalent: &m}},          // zero TS → skipped
	}
	hours, peak := hourlyBuckets(events, start, now, time.UTC)
	if len(hours) == 0 || hours[peak] <= 0 {
		t.Errorf("expected a non-empty peak bucket, got %v peak=%d", hours, peak)
	}
	if h2, _ := hourlyBuckets(nil, now, start, time.UTC); len(h2) != 1 {
		t.Errorf("now before the window start should clamp to 1 bucket, got %d", len(h2))
	}
}

// cmdScan guards a bad flag (exit 2) and, on a non-verbose run, notes that records were
// skipped (pointing at --verbose).
func TestCmdScan_Branches(t *testing.T) {
	home := setupHome(t)
	if _, _, c := run(t, "scan", "--bogus"); c != 2 {
		t.Errorf("scan with a bad flag should exit 2, got %d", c)
	}
	bad := filepath.Join(home, ".claude", "projects", "payments", "bad.jsonl")
	if err := os.WriteFile(bad, []byte("{not valid json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, c := run(t, "scan")
	if c != 0 || !strings.Contains(out, "skipped") {
		t.Errorf("a non-verbose scan with a bad line should note skipped records: code=%d out=%s", c, out)
	}
}

// budget set / clear write and remove the ceiling in config (exit 0 either way).
func TestBudget_SetAndClear(t *testing.T) {
	setupHome(t)
	if out, _, c := run(t, "budget", "set", "100"); c != 0 {
		t.Errorf("budget set 100 exit = %d, out=%s", c, out)
	}
	if _, _, c := run(t, "budget", "clear"); c != 0 {
		t.Errorf("budget clear exit = %d, want 0", c)
	}
}
