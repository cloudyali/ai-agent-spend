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

// The gauge should not just show the level — it should forecast. A weekly window at
// 90% with only 24h left is on pace to hit the wall, and `today` must say so.
func TestRenderToday_ShowsPaceWarning(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	reset := now.Add(24 * time.Hour).Unix()
	writeUsage(t, home, fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":%d}}}`, reset))

	var out strings.Builder
	a := appWithHome(home, &out, now)
	m := event.USD(5_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now, TSEnd: now, Tokens: event.Tokens{Input: 100_000}, CostViews: event.CostViews{APIEquivalent: &m}}
	a.renderToday([]event.AgentEvent{e}, now, config.PlanSet{}, 1, pricing.NewEngine())

	if v := out.String(); !strings.Contains(v, "on pace to run out") {
		t.Errorf("today should warn when the weekly wall is on pace to be hit:\n%s", v)
	}
}
