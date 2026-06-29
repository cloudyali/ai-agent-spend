package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// The amortized view across mixed providers: a started plan allocates; a provider with
// no plan is disclosed as uncovered; a provider whose plan starts after the window is
// flagged not-yet-active; an unpriced turn is skipped from the basis.
func TestRenderAllocated_MixedProviders(t *testing.T) {
	var buf strings.Builder
	a := &App{Out: &buf, Now: time.Now}
	cc := event.USD(100)
	cx := event.USD(50)
	cu := event.USD(25)
	events := []event.AgentEvent{
		{Provider: "claude_code", Model: "claude-opus-4-8", CostViews: event.CostViews{APIEquivalent: &cc}}, // plan started → allocated
		{Provider: "codex", Model: "gpt-5.3-codex", CostViews: event.CostViews{APIEquivalent: &cx}},         // no plan → uncovered note
		{Provider: "cursor", Model: "cursor-x", CostViews: event.CostViews{APIEquivalent: &cu}},             // future plan → not-yet-active
		{Provider: "claude_code", Model: "mystery"},                                                         // nil cost → skipped basis
	}
	jun1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	jul1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	plans := config.PlanSet{
		Default: config.Plan{Kind: "api"},
		ByProvider: map[string]config.Plan{
			"claude_code": {Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200, StartDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
			"cursor":      {Kind: "subscription", Name: "cursor-pro", MonthlyFeeUSD: 20, StartDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		},
	}
	a.renderReport(events, "provider", "amortized", jun1, jul1, "last 30d", plans, len(events))
	got := buf.String()
	if !strings.Contains(got, "$200.00") {
		t.Errorf("the started claude plan should allocate $200:\n%s", got)
	}
	if !strings.Contains(got, "Codex usage not allocated") {
		t.Errorf("codex (no plan) should be disclosed as uncovered:\n%s", got)
	}
	if !strings.Contains(got, "Cursor usage not allocated") || !strings.Contains(got, "2026-08-01") {
		t.Errorf("cursor (future plan) should be flagged not-yet-active:\n%s", got)
	}
}

// Amortized view where no turn carries a priced basis → fall through to the honest
// empty-range message (store has data, this view doesn't), never a fabricated total.
func TestRenderAllocated_NoPricedBasis(t *testing.T) {
	var buf strings.Builder
	a := &App{Out: &buf, Now: time.Now}
	events := []event.AgentEvent{{Provider: "claude_code", Model: "mystery"}} // nil cost
	jun1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	jul1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	subPlans := config.PlanSet{
		Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200, StartDate: jun1},
		ByProvider: map[string]config.Plan{},
	}
	a.renderReport(events, "model", "amortized", jun1, jul1, "last 30d", subPlans, 42)
	if got := buf.String(); !strings.Contains(got, "42") {
		t.Errorf("no priced basis should fall through to the empty-range message citing the stored count:\n%s", got)
	}
}
