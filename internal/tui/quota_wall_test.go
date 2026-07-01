package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/quota"
)

// The TUI is the default channel, so its wall gauge must carry the full story: the
// dollar value of the window, plus the run-out forecast.
func TestQuotaLines_DollarizesAndForecasts(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "week"}}, 0, pricing.NewEngine()).WithNow(now)
	m.quota = []quota.Sample{{
		Provider: "claude", Window: quota.WindowWeekly, UsedPercent: 90,
		ResetsAt: now.Add(24 * time.Hour), ObservedAt: now,
	}}
	m.wallSpendFn = func(quota.Sample) (int64, bool) { return 12_500_000, true }
	m.refresh() // wall-spend values are cached at refresh, off the render path

	got := strings.Join(m.quotaLines(), "\n")
	if !strings.Contains(got, "at API rates") {
		t.Errorf("TUI wall gauge should be dollarized:\n%s", got)
	}
	if !strings.Contains(got, "on pace to run out") {
		t.Errorf("TUI wall gauge should forecast run-out:\n%s", got)
	}
}
