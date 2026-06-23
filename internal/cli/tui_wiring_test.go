//go:build !offline

package cli

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// Off a TTY (the test writer is a buffer), `tui` refuses and points at the static
// commands rather than opening an unusable interactive screen; a bad flag exits 2.
func TestCmdTui_NonTTY(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	if _, errs, c := run(t, "tui"); c != 1 || !strings.Contains(errs, "interactive terminal") {
		t.Errorf("tui off a TTY should error (exit 1), got c=%d err=%s", c, errs)
	}
	if _, _, c := run(t, "tui", "--nope"); c != 2 {
		t.Errorf("tui with a bad flag should exit 2, got %d", c)
	}
}

func evIDs(evs []event.AgentEvent) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventID
	}
	return out
}

// eventsInWindow filters by [Since, Until]; a zero Since means no lower bound, a
// zero Until no upper bound (the same semantics the report store filter uses).
func TestEventsInWindow(t *testing.T) {
	ev := func(id string, day int) event.AgentEvent {
		return event.AgentEvent{EventID: id, TSStart: time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC)}
	}
	all := []event.AgentEvent{ev("a", 1), ev("b", 10), ev("c", 20)}

	mid := eventsInWindow(all, window{
		Since: time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC),
	})
	if len(mid) != 1 || mid[0].EventID != "b" {
		t.Fatalf("bounded window should keep only b, got %v", evIDs(mid))
	}
	if noLower := eventsInWindow(all, window{Until: time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)}); len(noLower) != 2 {
		t.Fatalf("no-lower-bound window should keep a,b, got %v", evIDs(noLower))
	}
	if unbounded := eventsInWindow(all, window{}); len(unbounded) != 3 {
		t.Fatalf("unbounded window should keep all 3, got %v", evIDs(unbounded))
	}
}

// amortizedByProvider returns nothing to amortize for api-only plans (no
// subscription fee), and dedupes providers from the events it sees.
func TestAmortizedByProvider_APIOnly(t *testing.T) {
	setupHome(t)
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	evs := []event.AgentEvent{{Provider: "claude_code"}, {Provider: "codex"}}
	win := window{
		Since: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	}
	out, hasPlan := a.amortizedByProvider(evs, win, apiPlans())
	if hasPlan || len(out) != 0 {
		t.Fatalf("api-only plans should amortize nothing, got %v hasPlan=%v", out, hasPlan)
	}
}

// With a subscription plan for a provider, a bounded window prorates that provider's
// fee (and only that provider's), keyed by provider.
func TestAmortizedByProvider_Subscription(t *testing.T) {
	setupHome(t)
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	plans := config.PlanSet{
		Default: config.Plan{Kind: "api"},
		ByProvider: map[string]config.Plan{
			"claude_code": {Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200, StartDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	evs := []event.AgentEvent{{Provider: "claude_code"}, {Provider: "codex"}}
	win := window{
		Since: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		Until: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
	}
	out, hasPlan := a.amortizedByProvider(evs, win, plans)
	if !hasPlan {
		t.Fatal("a subscription plan in the window should amortize")
	}
	if out["claude_code"] <= 0 {
		t.Errorf("claude_code's prorated fee should be > 0, got %d", out["claude_code"])
	}
	if _, ok := out["codex"]; ok {
		t.Errorf("codex has no plan — it must not appear in the amortized map: %v", out)
	}
}

// planChoices always offers the seeded catalog plus an explicit "api" option;
// planProviders dedupes the providers present in the data.
func TestPlanChoicesAndProviders(t *testing.T) {
	setupHome(t)
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	if len(a.planChoices()) < 2 { // at least one seeded plan + the api option
		t.Errorf("planChoices should include seeded plans + an api option, got %d", len(a.planChoices()))
	}
	provs := a.planProviders([]event.AgentEvent{
		{Provider: "claude_code"}, {Provider: "claude_code"}, {Provider: "codex"}, {Provider: ""},
	})
	if len(provs) != 2 {
		t.Errorf("planProviders should dedupe to 2 providers (claude_code, codex), got %d", len(provs))
	}
}
