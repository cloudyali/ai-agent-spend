package cli

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// budgetPace computes month-to-date api-equivalent pace against a configured ceiling;
// renderBudget prints it. Driven directly so the compute + render path is exercised
// deterministically (a fixed clock in the same month as the fixture events).
func TestBudgetPace_Direct(t *testing.T) {
	home := setupHome(t)
	run(t, "scan")
	if err := config.SetBudget(filepath.Join(home, ".aispend"), 50_000_000); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC) // same month as the seeded events
	a := &App{Resolver: platform.Detect(), Now: func() time.Time { return now }, Out: io.Discard, Err: io.Discard}
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}

	p, _, ok := a.budgetPace(st, now)
	if !ok {
		t.Fatal("budgetPace should be ok with a configured budget")
	}
	if p.Limit != 50_000_000 {
		t.Errorf("budget limit = %d, want 50000000", p.Limit)
	}

	var buf strings.Builder
	a.Out = &buf
	a.renderBudget(st, now)
	if !strings.Contains(buf.String(), "budget") {
		t.Errorf("renderBudget should print a budget line, got: %q", buf.String())
	}
}

// budgetPace is silent when no budget is configured (renderBudget prints nothing).
func TestBudgetPace_Unset(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := a.budgetPace(st, time.Now()); ok {
		t.Error("budgetPace should report not-ok when no budget is configured")
	}
}

// renderTopSessions: sessionless turns are skipped, an all-sessionless window says so,
// and the list truncates to the limit (priciest-first).
func TestRenderTopSessions_Branches(t *testing.T) {
	var buf strings.Builder
	a := &App{Out: &buf, Now: time.Now}
	m := event.USD(100)

	// Every turn lacks a session id → the honest "no addressable sessions" message.
	a.renderTop([]event.AgentEvent{{Provider: "claude_code", CostViews: event.CostViews{APIEquivalent: &m}}}, 10, true, "all", 1)
	if !strings.Contains(buf.String(), "no addressable sessions") {
		t.Errorf("sessionless turns → no addressable sessions, got: %s", buf.String())
	}

	// Three sessions + a sessionless turn, limit 2 → top 2 only, sessionless skipped.
	buf.Reset()
	a1, a2, a3 := event.USD(300), event.USD(200), event.USD(100)
	evs := []event.AgentEvent{
		{SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8", CostViews: event.CostViews{APIEquivalent: &a1}},
		{SessionID: "s2", Provider: "claude_code", Model: "claude-sonnet-4", CostViews: event.CostViews{APIEquivalent: &a2}},
		{SessionID: "s3", Provider: "claude_code", Model: "claude-haiku-4", CostViews: event.CostViews{APIEquivalent: &a3}},
		{SessionID: "", Provider: "claude_code", CostViews: event.CostViews{APIEquivalent: &a3}}, // skipped
	}
	a.renderTop(evs, 2, true, "all", len(evs))
	if out := buf.String(); !strings.Contains(out, "s1") || strings.Contains(out, "s3") {
		t.Errorf("limit 2 should list the top 2 sessions (s1,s2), not s3:\n%s", out)
	}
}

// When the requested view is api_equivalent but the (non-empty) window's turns carry no
// priced cost, renderReport says exactly that rather than telling the user to widen.
func TestRenderReport_ApiEquivalentEmpty(t *testing.T) {
	var buf strings.Builder
	a := &App{Out: &buf, Now: time.Now}
	events := []event.AgentEvent{
		{Model: "mystery-1", Provider: "claude_code"}, // no APIEquivalent
		{Model: "mystery-2", Provider: "claude_code"},
	}
	until := time.Now()
	a.renderReport(events, "model", "api_equivalent", until.AddDate(0, 0, -7), until, "last 7d", apiPlans(), len(events))
	got := buf.String()
	if !strings.Contains(got, "no priced cost in this view") {
		t.Errorf("api_equivalent + unpriced turns should say so, got:\n%s", got)
	}
	if strings.Contains(got, "widen with --period all") {
		t.Errorf("must not tell the user to widen a non-empty window:\n%s", got)
	}
}

// proratePlan is cycle-aware when the plan has a start-date anchor, and falls back to a
// flat day-count proration otherwise; planStartsAfter flags a not-yet-active plan.
func TestProratePlanAndStartsAfter(t *testing.T) {
	since := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	withStart := toPricingPlan(config.Plan{Kind: "subscription", MonthlyFeeUSD: 200, StartDate: since})
	if m, ok := proratePlan(withStart, since, until, 30); !ok || m.Micros <= 0 {
		t.Errorf("prorate with a start date should amortize, got %v ok=%v", m, ok)
	}
	flat := toPricingPlan(config.Plan{Kind: "subscription", MonthlyFeeUSD: 200})
	if m, ok := proratePlan(flat, since, until, 30); !ok || m.Micros <= 0 {
		t.Errorf("prorate without a start date should flat-prorate, got %v ok=%v", m, ok)
	}

	future := toPricingPlan(config.Plan{Kind: "subscription", MonthlyFeeUSD: 200, StartDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)})
	if !planStartsAfter(future, until) {
		t.Error("a plan starting after the window end should report startsAfter=true")
	}
	if planStartsAfter(flat, until) {
		t.Error("a plan with no start date must not be flagged as starting after the window")
	}
}
