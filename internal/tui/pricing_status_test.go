package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// With a fresh LiteLLM cache, the header carries a compact provenance line: the source
// and how long ago it was synced.
func TestModel_PricingStatus_LiteLLMShowsAge(t *testing.T) {
	now := time.Date(2026, 6, 23, 12, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithNow(now).
		WithPricingStatus(func() PricingStatus {
			return PricingStatus{Source: "LiteLLM", SyncedAt: now.Add(-2 * time.Hour)}
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "rates: LiteLLM") || !strings.Contains(v, "2h ago") {
		t.Errorf("header should show compact pricing status with sync age:\n%s", v)
	}
}

// The embedded fallback (no cache) renders its source with no date.
func TestModel_PricingStatus_Embedded(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).
		WithPricingStatus(func() PricingStatus { return PricingStatus{Source: "embedded"} })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "rates: embedded") {
		t.Errorf("embedded source should render in the header:\n%s", v)
	}
}

// Without WithPricingStatus the header stays exactly as before — no "rates:" segment.
func TestModel_PricingStatus_OffByDefault(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)
	if strings.Contains(m.View(), "rates:") {
		t.Errorf("no pricing status wired → no rates segment:\n%s", m.View())
	}
}

func TestRelAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{-5 * time.Second, "just now"}, // clock skew → not in the future
		{5 * time.Minute, "5m ago"},
		{3 * time.Hour, "3h ago"},
		{50 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := relAge(c.d); got != c.want {
			t.Errorf("relAge(%s) = %q, want %q", c.d, got, c.want)
		}
	}
}
