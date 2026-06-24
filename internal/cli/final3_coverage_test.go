package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing/refresh"
)

// budget set rejects a missing, unparseable, or non-positive amount (usage error,
// exit 2) rather than silently writing it.
func TestCmdBudgetSet_BadAmount(t *testing.T) {
	setupHome(t)
	if _, _, c := run(t, "budget", "set"); c != 2 {
		t.Errorf("budget set with no amount should exit 2, got %d", c)
	}
	if _, _, c := run(t, "budget", "set", "not-money"); c != 2 {
		t.Errorf("budget set with an unparseable amount should exit 2, got %d", c)
	}
	if _, _, c := run(t, "budget", "set", "0"); c != 2 {
		t.Errorf("budget set 0 should exit 2 (use clear to remove), got %d", c)
	}
}

// budget --strict exits non-zero when the run-rate projects over a (tiny) ceiling —
// the CI-gating path. Driven with a fixed clock in the fixture's month so the run-rate
// is deterministic regardless of the wall-clock date the suite runs on.
func TestBudget_StrictOver(t *testing.T) {
	home := setupHome(t)
	run(t, "scan")                                                                   // import the June fixture events
	if err := config.SetBudget(filepath.Join(home, ".aispend"), 1_000); err != nil { // $0.001 — guaranteed over
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	a := &App{Resolver: platform.Detect(), Now: func() time.Time { return now }, Out: io.Discard, Err: io.Discard}
	if code := a.cmdBudget([]string{"--strict", "--no-scan", "--no-refresh"}); code == 0 {
		t.Errorf("budget --strict over a tiny ceiling should exit non-zero, got %d", code)
	}
}

// cmdPricingRefresh on an empty ledger caches the table and reports that there is
// nothing to reprice yet (the repriced==0 branch).
func TestCmdPricingRefresh_EmptyStore(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("pricing refresh runs only in the networked (default) build")
	}
	home := setupHome(t) // note: no scan → the store is empty
	cache := refresh.CachePath(filepath.Join(home, ".aispend"))
	var out, errb bytes.Buffer
	app := &App{
		Resolver: platform.Detect(),
		Now:      time.Now,
		Out:      &out,
		Err:      &errb,
		fetchPrices: func(context.Context, string) ([]byte, error) {
			return []byte(`{"claude-opus-4":{"input_cost_per_token":0.00001}}`), nil
		},
	}
	if code := app.cmdPricingRefresh(cache); code != 0 {
		t.Fatalf("refresh on an empty store should still exit 0, got %d (err=%s)", code, errb.String())
	}
	if !strings.Contains(out.String(), "No stored events") {
		t.Errorf("an empty-store refresh should say there is nothing to reprice yet:\n%s", out.String())
	}
}

// cmdPricingRefresh reports and exits 1 when applying the freshly-fetched table fails
// (here: the reprice step can't open a corrupt ledger) — the table-apply error branch.
func TestCmdPricingRefresh_ApplyError(t *testing.T) {
	if !refresh.NetworkEnabled {
		t.Skip("pricing refresh runs only in the networked (default) build")
	}
	brokenLedger(t) // events.json is a directory → repriceStored (inside applyPricingTable) fails
	var out, errb bytes.Buffer
	app := &App{
		Resolver: platform.Detect(),
		Now:      time.Now,
		Out:      &out,
		Err:      &errb,
		fetchPrices: func(context.Context, string) ([]byte, error) {
			return []byte(`{"claude-opus-4":{"input_cost_per_token":0.00001}}`), nil
		},
	}
	if code := app.cmdPricingRefresh(refresh.CachePath(app.Resolver.AppHome())); code != 1 {
		t.Errorf("a reprice failure during refresh should exit 1, got %d", code)
	}
	if !strings.Contains(errb.String(), "pricing refresh") {
		t.Errorf("the apply error should be reported on stderr, got: %q", errb.String())
	}
}
