package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// "Dollarize the wall": beside the weekly gauge, show the api-equivalent value of
// the work done within that window — the computed ledger next to the reported wall.
func TestRenderToday_DollarizesWall(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	reset := now.Add(48 * time.Hour).Unix() // weekly window opened ~5 days ago, still active
	writeUsage(t, home, fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":40,"resets_at":%d}}}`, reset))

	var out strings.Builder
	a := appWithHome(home, &out, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(12_500_000) // $12.50 of api-equivalent inside the window
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-2 * time.Hour), TSEnd: now.Add(-2 * time.Hour), CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}

	a.renderToday([]event.AgentEvent{e}, now, config.PlanSet{}, 1, pricing.NewEngine())
	v := out.String()
	if !strings.Contains(v, "at API rates") || !strings.Contains(v, "12.50") {
		t.Errorf("today should dollarize the weekly wall (≈ $12.50 at API rates):\n%s", v)
	}
}
