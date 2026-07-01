package menubar

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/lines"
)

func pf(v float64) *float64     { return &v }
func tp(t time.Time) *time.Time { return &t }

func TestRender_EmptyState(t *testing.T) {
	st := Render(nil, time.Now())
	if !strings.Contains(st.Title, "aispend") {
		t.Errorf("title = %q", st.Title)
	}
	if len(st.Items) == 0 || !strings.Contains(st.Items[0].Text, "spend today") {
		t.Errorf("empty state should say there's no spend yet: %+v", st.Items)
	}
}

func TestRender_TitleIsWorstGaugeAndItemsCarryDetail(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	snaps := []lines.Snapshot{
		{ProviderID: "claude", DisplayName: "Claude", Plan: "Team 5x", Lines: []lines.Line{
			{Type: "progress", Label: "Weekly", Used: pf(90), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(24 * time.Hour)), Projection: &lines.Projection{ProjectedUsed: 120, Breaches: true, ETASeconds: 57600}},
			{Type: "text", Label: "Weekly value", Value: "≈ $118.00 at API rates"},
		}},
		{ProviderID: "codex", DisplayName: "Codex", Plan: "Plus", Lines: []lines.Line{
			{Type: "progress", Label: "Weekly", Used: pf(40), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(72 * time.Hour)), Projection: &lines.Projection{ProjectedUsed: 55}},
		}},
	}
	st := Render(snaps, now)
	if !strings.Contains(st.Title, "Claude") || !strings.Contains(st.Title, "90%") {
		t.Errorf("title should surface the worst gauge, got %q", st.Title)
	}
	var joined string
	for _, it := range st.Items {
		joined += it.Text + "\n"
	}
	for _, want := range []string{
		"Claude · Team 5x", "Weekly 90%", "resets in", "on pace to run out",
		"at API rates", "Codex · Plus", "Weekly 40%", "on pace: ~55%",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("items missing %q:\n%s", want, joined)
		}
	}
}

func TestRender_TitleFallsBackToSpendWhenNoGauge(t *testing.T) {
	// No quota window (no progress line) → the menu-bar title should still be useful:
	// show today's spend rather than a bare "aispend".
	snaps := []lines.Snapshot{
		{ProviderID: "claude", DisplayName: "Claude", Lines: []lines.Line{
			{Type: "text", Label: "Today", Value: "≈ $24.47 at API rates"},
		}},
	}
	st := Render(snaps, time.Now())
	if !strings.Contains(st.Title, "$24.47") || !strings.Contains(st.Title, "Claude") {
		t.Errorf("with no gauge the title should surface today's spend, got %q", st.Title)
	}
}

func TestHumanDur(t *testing.T) {
	cases := map[time.Duration]string{
		0:                "now",
		45 * time.Second: "<1m",
		90 * time.Minute: "1h 30m",
		26 * time.Hour:   "1d 2h",
		72 * time.Hour:   "3d",
	}
	for d, want := range cases {
		if got := humanDur(d); got != want {
			t.Errorf("humanDur(%v) = %q, want %q", d, got, want)
		}
	}
}
