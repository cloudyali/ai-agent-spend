package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing/refresh"
)

// brokenLedger makes the events path a directory so OpenFileStore fails — a corrupt /
// unwritable ledger.
func brokenLedger(t *testing.T) {
	t.Helper()
	home := setupHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".aispend", "events.json"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// repriceStored surfaces a store-open failure rather than silently reporting success.
func TestRepriceStored_BrokenStore(t *testing.T) {
	brokenLedger(t)
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	if _, err := a.repriceStored(a.pricingEngine()); err == nil {
		t.Error("repriceStored on a broken ledger should return an error")
	}
}

// applyPricingTable caches the parsed table, then propagates the reprice error when the
// ledger can't be opened.
func TestApplyPricingTable_RepriceError(t *testing.T) {
	brokenLedger(t)
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	cache := refresh.CachePath(a.Resolver.AppHome())
	if _, _, err := a.applyPricingTable(cache, []byte(`{"claude-opus-4":{"input_cost_per_token":0.00001}}`)); err == nil {
		t.Error("applyPricingTable should propagate the reprice error on a broken ledger")
	}
}

// pendingUsage opens its own store; a broken ledger is an error, not a silent zero.
func TestPendingUsage_BrokenStore(t *testing.T) {
	brokenLedger(t)
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	if _, err := a.pendingUsage("", "main", time.Now()); err == nil {
		t.Error("pendingUsage on a broken ledger should return an error")
	}
}

// pendingUsage sums priced turns on the target branch (placeholder "" branches fold in),
// excludes turns on a different real branch, and skips nil-cost turns.
func TestPendingUsage_BranchAndNilCost(t *testing.T) {
	home := setupHome(t)
	run(t, "scan") // fixture turns have an empty branch → they fold into the target
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	other := event.USD(999)
	extra := []event.AgentEvent{
		{EventID: "on-other", Provider: "claude_code", Model: "m", GitBranch: "other",
			TSStart: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC), TSEnd: time.Date(2026, 6, 15, 9, 1, 0, 0, time.UTC),
			CostViews: event.CostViews{APIEquivalent: &other}}, // different real branch → excluded
		{EventID: "nilcost", Provider: "claude_code", Model: "m", GitBranch: "main",
			TSStart: time.Date(2026, 6, 15, 9, 0, 0, 0, time.UTC)}, // on-branch but no priced cost → skipped
	}
	if err := st.Upsert(extra); err != nil {
		t.Fatal(err)
	}
	_ = home

	u, err := a.pendingUsage("", "main", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if u.Cost.Micros <= 0 || u.Requests == 0 {
		t.Errorf("matching priced turns should sum, got %+v", u)
	}
	if u.PerModel["m"] != 0 {
		t.Errorf("the 'other'-branch turn must be excluded from the per-model sum, got %d", u.PerModel["m"])
	}
}

// doctor renders its trust/paths report for the bare and combined-flag forms.
func TestDoctor_Variants(t *testing.T) {
	setupHome(t)
	if _, _, c := run(t, "doctor"); c != 0 {
		t.Errorf("bare doctor exit = %d, want 0", c)
	}
	if out, _, c := run(t, "doctor", "--network", "--paths"); c != 0 || out == "" {
		t.Errorf("doctor --network --paths exit = %d (out empty=%v), want 0", c, out == "")
	}
}
