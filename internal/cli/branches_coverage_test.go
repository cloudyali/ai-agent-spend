package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing/refresh"
)

// cmdPricingRefresh fetches through the injected priceFetcher seam (same as the launch
// refresh), so the one network command is testable hermetically: a good fetcher caches
// + reprices and exits 0; a fetch error is reported and exits 1.
func TestCmdPricingRefresh_InjectedFetcher(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("pricing refresh runs only in the networked (default) build")
	}
	home := setupHome(t)
	run(t, "scan") // some stored events to reprice
	cache := refresh.CachePath(filepath.Join(home, ".aispend"))
	litellm := []byte(`{"claude-opus-4":{"input_cost_per_token":0.00001,"output_cost_per_token":0.00001,"cache_read_input_token_cost":0.000001}}`)

	var out, errb bytes.Buffer
	app := &App{
		Resolver:    platform.Detect(),
		Now:         time.Now,
		Out:         &out,
		Err:         &errb,
		fetchPrices: func(context.Context, string) ([]byte, error) { return litellm, nil },
	}
	if code := app.cmdPricingRefresh(cache); code != 0 {
		t.Fatalf("refresh with a good fetcher should exit 0, got %d (err=%s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "Refreshed") {
		t.Errorf("refresh should report success:\n%s", out.String())
	}

	out.Reset()
	errb.Reset()
	app.fetchPrices = func(context.Context, string) ([]byte, error) { return nil, errors.New("network down") }
	if code := app.cmdPricingRefresh(cache); code != 1 || !strings.Contains(errb.String(), "pricing refresh") {
		t.Errorf("a fetch error should exit 1 with a message, got code=%d err=%q", code, errb.String())
	}
}

// A corrupt/unwritable ledger (the events path is a directory) makes openStore fail.
// The read commands must surface it (exit 1), never panic — the error branch each
// command guards.
func TestReadCommands_BrokenStore(t *testing.T) {
	home := setupHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".aispend", "events.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"report", "--period", "all", "--no-scan", "--no-refresh"},
		{"today", "--no-scan", "--no-refresh"},
		{"top", "--no-scan", "--no-refresh"},
		{"budget", "--no-scan", "--no-refresh"},
		{"scan"},
	} {
		if _, errs, code := run(t, args...); code != 1 || !strings.Contains(errs, "aispend:") {
			t.Errorf("%v on a broken ledger: code=%d err=%q, want exit 1", args, code, errs)
		}
	}
}

// cmdTop guards a bad --period (exit 2) and resets a non-positive --limit to the default.
func TestCmdTop_BadPeriodAndLimit(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	if _, _, c := run(t, "top", "--period", "nonsense"); c != 2 {
		t.Errorf("top with a bad period should exit 2, got %d", c)
	}
	if _, _, c := run(t, "top", "--limit", "0", "--period", "all"); c != 0 {
		t.Errorf("top --limit 0 should reset to the default and exit 0, got %d", c)
	}
}

// With a plan configured in config.toml, the static `plans` list marks it with `*`.
func TestCmdPlans_MarksConfigured(t *testing.T) {
	home := setupHome(t)
	appHome := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appHome, "config.toml"), []byte("plan = \"claude-max-20x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, code := run(t, "plans")
	if code != 0 || !strings.Contains(out, "claude-max-20x") || !strings.Contains(out, "*") {
		t.Errorf("plans should mark the configured plan with *: code=%d out=%s", code, out)
	}
}

// A configured budget drives the month-to-date pace path in `budget` and in `today`'s
// budget line (budgetPace), plus the --json pace object.
func TestBudget_Configured(t *testing.T) {
	home := setupHome(t)
	run(t, "scan")
	if err := config.SetBudget(filepath.Join(home, ".aispend"), 100_000_000); err != nil {
		t.Fatal(err)
	}
	if _, _, c := run(t, "budget"); c != 0 {
		t.Errorf("budget (configured) exit = %d, want 0", c)
	}
	if out, _, c := run(t, "budget", "--json"); c != 0 || !json.Valid([]byte(out)) {
		t.Errorf("budget --json should emit valid JSON: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "today"); c != 0 || !strings.Contains(out, "today") {
		t.Errorf("today with a budget should still render: code=%d out=%s", c, out)
	}
}

// report --json over several facets and an empty window all emit one valid JSON doc.
func TestReportJSON_FacetsAndEmpty(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	for _, by := range []string{"session", "commit", "model"} {
		out, _, c := run(t, "report", "--period", "all", "--json", "--by", by)
		if c != 0 || !json.Valid([]byte(out)) {
			t.Errorf("report --json --by %s: code=%d valid=%v", by, c, json.Valid([]byte(out)))
		}
	}
	if out, _, c := run(t, "report", "--period", "today", "--json"); c != 0 || !json.Valid([]byte(out)) {
		t.Errorf("empty-window report --json should still be valid JSON: code=%d out=%s", c, out)
	}
}
