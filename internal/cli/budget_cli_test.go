package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/budget"
	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/store"
)

// errWriter fails every write — used to drive the JSON encode-error branch.
type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }

// parseUSDAmount accepts a human dollar amount ($, commas, decimals) and returns
// micros; format garbage errors, the sign is preserved so the caller can reject it.
func TestParseUSDAmount(t *testing.T) {
	ok := []struct {
		in   string
		want int64
	}{
		{"$500", 500_000_000},
		{"500", 500_000_000},
		{"500.50", 500_500_000},
		{"1,000", 1_000_000_000},
		{"$1,234.56", 1_234_560_000},
		{"  $2000 ", 2_000_000_000},
		{"-5", -5_000_000}, // parsed, not rejected here — the command rejects non-positive
	}
	for _, tc := range ok {
		got, err := parseUSDAmount(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parseUSDAmount(%q) = %d, %v; want %d, nil", tc.in, got, err, tc.want)
		}
	}
	// Non-numeric, malformed, the non-finite values ParseFloat would otherwise accept
	// (inf/nan), and out-of-range magnitudes that would overflow the micros conversion
	// must all error rather than become a bogus ceiling.
	for _, bad := range []string{"", "$", "abc", "1.2.3", "  ", "inf", "Inf", "+Inf", "nan", "NaN", "1e13", "1e20", "-1e20"} {
		if got, err := parseUSDAmount(bad); err == nil {
			t.Errorf("parseUSDAmount(%q) = %d, nil; want an error", bad, got)
		}
	}
}

// renderBudgetStatus is the standalone pace render: ceiling, spent + used%, the
// run-rate projection with its verdict, and a disclosure of excluded providers.
func TestRenderBudgetStatus(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Out: &buf}
	p := budget.Pace{Spent: 250_000_000, Limit: 500_000_000, Projected: 480_000_000, ElapsedFraction: 0.5}
	a.renderBudgetStatus(p, nil)
	got := buf.String()
	for _, want := range []string{"budget", "ceiling", "$500.00", "$250.00", "$480.00", "50% used", "on track"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderBudgetStatus missing %q:\n%s", want, got)
		}
	}

	// An uncovered provider is named, not silently dropped from the sum.
	buf.Reset()
	a.renderBudgetStatus(p, []string{"codex"})
	if got := buf.String(); !strings.Contains(got, "Codex") || !strings.Contains(got, "excluded") {
		t.Errorf("uncovered provider should be disclosed:\n%s", got)
	}
}

// buildBudgetResult is the JSON projection: snake_case keys, micros + USD pairs,
// the month label, and the derived verdict — asserted directly so it's clock-free.
func TestBuildBudgetResult(t *testing.T) {
	now := time.Date(2026, 6, 17, 14, 30, 0, 0, time.UTC)
	p := budget.Pace{Spent: 250_000_000, Limit: 500_000_000, Projected: 480_000_000, ElapsedFraction: 0.5}
	r := buildBudgetResult(p, []string{"codex"}, now)

	if !r.Configured || r.Month != "2026-06" {
		t.Errorf("meta wrong: configured=%v month=%q", r.Configured, r.Month)
	}
	if r.LimitMicros != 500_000_000 || r.LimitUSD != 500.0 {
		t.Errorf("limit = %d / %v", r.LimitMicros, r.LimitUSD)
	}
	if r.SpentMicros != 250_000_000 || r.ProjectedMicros != 480_000_000 {
		t.Errorf("spent=%d projected=%d", r.SpentMicros, r.ProjectedMicros)
	}
	if math.Abs(r.UsedFraction-0.5) > 1e-9 || math.Abs(r.PaceRatio-0.96) > 1e-9 {
		t.Errorf("used=%v pace=%v", r.UsedFraction, r.PaceRatio)
	}
	if r.OverPace || r.Status != "on track" {
		t.Errorf("verdict wrong: over=%v status=%q", r.OverPace, r.Status)
	}
	if len(r.Uncovered) != 1 || r.Uncovered[0] != "codex" {
		t.Errorf("uncovered = %v", r.Uncovered)
	}
	if b, _ := json.Marshal(r); !strings.Contains(string(b), "limit_micros") ||
		!strings.Contains(string(b), "over_pace") || !strings.Contains(string(b), "uncovered_providers") {
		t.Errorf("json keys missing: %s", b)
	}
}

// set → show → clear round-trip through the real CLI: the ceiling lands in config,
// `budget` reads it back, and `clear` removes it. Clock-independent assertions.
func TestBudget_SetShowClearRoundTrip(t *testing.T) {
	home := setupHome(t)
	t.Setenv("AISPEND_NO_SCAN", "1")
	appHome := filepath.Join(home, ".aispend")

	if out, _, code := run(t, "budget", "set", "$500"); code != 0 || !strings.Contains(out, "$500.00") {
		t.Fatalf("budget set: code=%d out=%s", code, out)
	}
	if micros, ok, err := config.LoadBudget(appHome); err != nil || !ok || micros != 500_000_000 {
		t.Fatalf("LoadBudget after set = %d, %v, %v; want 500000000, true, nil", micros, ok, err)
	}

	if out, _, code := run(t, "budget"); code != 0 || !strings.Contains(out, "$500.00") || !strings.Contains(out, "ceiling") {
		t.Fatalf("budget show: code=%d out=%s", code, out)
	}

	if out, _, code := run(t, "budget", "clear"); code != 0 || !strings.Contains(strings.ToLower(out), "cleared") {
		t.Fatalf("budget clear: code=%d out=%s", code, out)
	}
	if _, ok, _ := config.LoadBudget(appHome); ok {
		t.Errorf("budget should be unset after clear")
	}
}

// With no budget configured, `budget` is not an error — it explains how to set one,
// and `--json` reports configured:false.
func TestBudget_ShowUnconfigured(t *testing.T) {
	setupHome(t)
	t.Setenv("AISPEND_NO_SCAN", "1")

	if out, _, code := run(t, "budget"); code != 0 || !strings.Contains(strings.ToLower(out), "no budget") {
		t.Errorf("unconfigured show: code=%d out=%s", code, out)
	}

	out, _, code := run(t, "budget", "--json")
	if code != 0 {
		t.Fatalf("unconfigured --json exit %d", code)
	}
	var r budgetResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if r.Configured {
		t.Errorf("configured should be false when unset: %s", out)
	}

	// An unknown flag on the show path is a usage error (exit 2), not a panic.
	if _, _, code := run(t, "budget", "--bogus"); code != 2 {
		t.Errorf("unknown flag exit = %d, want 2", code)
	}
}

// Bad inputs to `set` fail with a usage exit (2), never a silent no-op or a written
// non-positive ceiling.
func TestBudget_SetValidation(t *testing.T) {
	setupHome(t)
	t.Setenv("AISPEND_NO_SCAN", "1")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing amount", []string{"budget", "set"}},
		{"zero", []string{"budget", "set", "0"}},
		{"negative", []string{"budget", "set", "-5"}},
		{"garbage", []string{"budget", "set", "abc"}},
	} {
		if _, _, code := run(t, tc.args...); code != 2 {
			t.Errorf("%s: exit = %d, want 2", tc.name, code)
		}
	}
}

// budgetTestApp wires an App onto an isolated home with a fixed clock and a
// pre-populated, in-month ledger — so JSON values and the --strict exit are
// deterministic rather than dependent on the wall clock.
func budgetTestApp(t *testing.T, spentMicros int64) (*App, *bytes.Buffer, string) {
	t.Helper()
	home := setupHome(t)
	t.Setenv("AISPEND_NO_SCAN", "1")
	t.Setenv("AISPEND_NO_REFRESH", "1")
	appHome := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	now := fixedNow() // 2026-06-17 — June, so an event dated June 10 is in-month
	m := event.USD(spentMicros)
	ev := event.AgentEvent{
		EventID: "e1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart:   time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC),
		TSEnd:     time.Date(2026, 6, 10, 12, 1, 0, 0, time.UTC),
		CostViews: event.CostViews{APIEquivalent: &m},
	}
	st, err := store.OpenFileStore(filepath.Join(appHome, "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Upsert([]event.AgentEvent{ev}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	a := &App{Resolver: platform.Detect(), Now: func() time.Time { return now }, Out: &buf, Err: io.Discard}
	return a, &buf, appHome
}

// Configured --json reports the live month-to-date pace against the ceiling, with
// values that match the populated ledger.
func TestBudget_JSONConfigured(t *testing.T) {
	a, buf, appHome := budgetTestApp(t, 5_000_000) // $5 spent this month
	if err := config.SetBudget(appHome, 1_000_000_000_000); err != nil {
		t.Fatal(err) // $1,000,000 ceiling → comfortably under
	}
	if code := a.cmdBudget([]string{"--json"}); code != 0 {
		t.Fatalf("budget --json exit %d", code)
	}
	var r budgetResult
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if !r.Configured || r.Month != "2026-06" {
		t.Errorf("meta: configured=%v month=%q", r.Configured, r.Month)
	}
	if r.SpentMicros != 5_000_000 {
		t.Errorf("spent_micros = %d, want 5000000", r.SpentMicros)
	}
	if r.LimitMicros != 1_000_000_000_000 || r.OverPace {
		t.Errorf("limit=%d over=%v (want under)", r.LimitMicros, r.OverPace)
	}
}

// --strict turns the pace verdict into an exit code: non-zero when the run-rate
// projects over the ceiling (CI gate), zero when it doesn't.
func TestBudget_StrictExit(t *testing.T) {
	t.Run("over pace exits 1", func(t *testing.T) {
		a, _, appHome := budgetTestApp(t, 5_000_000) // $5 spent
		if err := config.SetBudget(appHome, 10_000); err != nil {
			t.Fatal(err) // $0.01 ceiling → wildly over pace
		}
		if code := a.cmdBudget([]string{"--strict"}); code != 1 {
			t.Errorf("over-pace --strict exit = %d, want 1", code)
		}
	})
	t.Run("under pace exits 0", func(t *testing.T) {
		a, _, appHome := budgetTestApp(t, 5_000_000)
		if err := config.SetBudget(appHome, 1_000_000_000_000); err != nil {
			t.Fatal(err) // $1,000,000 ceiling → under
		}
		if code := a.cmdBudget([]string{"--strict"}); code != 0 {
			t.Errorf("under-pace --strict exit = %d, want 0", code)
		}
	})
	t.Run("unset budget never trips strict", func(t *testing.T) {
		a, _, _ := budgetTestApp(t, 5_000_000) // no SetBudget call
		if code := a.cmdBudget([]string{"--strict"}); code != 0 {
			t.Errorf("unset --strict exit = %d, want 0 (nothing to be over)", code)
		}
	})
}

// A config write that can't land (AppHome under a regular file → MkdirAll fails) is a
// real error exit (1), not a silent success — for both set and clear.
func TestBudget_ConfigWriteError(t *testing.T) {
	home := setupHome(t)
	t.Setenv("AISPEND_NO_SCAN", "1")
	t.Setenv("AISPEND_NO_REFRESH", "1")
	fileAsParent := filepath.Join(home, "afile")
	if err := os.WriteFile(fileAsParent, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AISPEND_HOME", filepath.Join(fileAsParent, "nested")) // parent is a file → unwritable

	if _, _, code := run(t, "budget", "set", "$500"); code != 1 {
		t.Errorf("set with unwritable AppHome: exit = %d, want 1", code)
	}
	if _, _, code := run(t, "budget", "clear"); code != 1 {
		t.Errorf("clear with unwritable AppHome: exit = %d, want 1", code)
	}
}

// A failing stdout writer surfaces as an error exit from the JSON path, not a swallowed
// encode failure.
func TestBudget_JSONWriteError(t *testing.T) {
	a, _, appHome := budgetTestApp(t, 5_000_000)
	if err := config.SetBudget(appHome, 500_000_000); err != nil {
		t.Fatal(err)
	}
	a.Out = errWriter{}
	if code := a.cmdBudget([]string{"--json"}); code != 1 {
		t.Errorf("JSON encode to a failing writer: exit = %d, want 1", code)
	}
}
