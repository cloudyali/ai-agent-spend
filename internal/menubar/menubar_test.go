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
	if !strings.Contains(st.Title, "AiSpend") {
		t.Errorf("title = %q", st.Title)
	}
	if len(st.Items) == 0 || !strings.Contains(st.Items[0].Text, "spend today") {
		t.Errorf("empty state should say there's no spend yet: %+v", st.Items)
	}
}

// The After design: the provider header is a Header row, ROI is the Hero, Cache saved
// and Today are Dim (secondary), quota lines carry a unicode bar, and a Trend submenu
// hangs off the provider from its Trend series.
func TestRender_LeadsWithWedgeHierarchy(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	claude := lines.Snapshot{
		ProviderID: "claude", DisplayName: "Claude", Plan: "Max 20x",
		Trend: []int64{10_000_000, 30_000_000, 20_000_000, 59_000_000},
		Lines: []lines.Line{
			{Type: "text", Label: "ROI", Value: "6.1× vs plan ($6.67/day)"},
			{Type: "text", Label: "Cache saved", Value: "≈ $340 (28%)"},
			{Type: "progress", Label: "Session", Used: pf(23), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(25 * time.Minute)), Projection: &lines.Projection{ProjectedUsed: 40}},
			{Type: "progress", Label: "Weekly", Used: pf(23), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(5 * 24 * time.Hour)), Projection: &lines.Projection{ProjectedUsed: 30}},
			{Type: "text", Label: "Today", Value: "≈ $59 · 46.0M tokens"},
		},
	}
	st := Render([]lines.Snapshot{claude}, now)

	if !strings.Contains(st.Title, "Claude") || !strings.Contains(st.Title, "23%") {
		t.Errorf("title should surface the worst gauge, got %q", st.Title)
	}

	var header, hero, dimCache, dimToday, barRow, trend *Item
	for i := range st.Items {
		it := &st.Items[i]
		switch {
		case it.Header && strings.Contains(it.Text, "Claude · Max 20x"):
			header = it
		case it.Hero && strings.Contains(it.Text, "×"):
			hero = it
		case it.Dim && strings.Contains(it.Text, "Cache saved"):
			dimCache = it
		case it.Dim && strings.Contains(it.Text, "Today"):
			dimToday = it
		case strings.Contains(it.Text, "▓") && strings.Contains(it.Text, "23%"):
			barRow = it
		case it.Text == "Trend":
			trend = it
		}
	}
	if header == nil {
		t.Error("want a bold provider Header row")
	}
	if hero == nil {
		t.Error("want the ROI line rendered as the Hero")
	}
	if dimCache == nil || dimToday == nil {
		t.Error("want Cache saved and Today rendered Dim (secondary)")
	}
	if barRow == nil {
		t.Error("want a quota row carrying a unicode bar next to the percent")
	}
	if trend == nil || len(trend.Children) == 0 {
		t.Fatalf("want a Trend submenu with children: %+v", trend)
	}
	if !strings.ContainsAny(trend.Children[0].Text, "▁▂▃▄▅▆▇█") {
		t.Errorf("Trend submenu should show a sparkline, got %q", trend.Children[0].Text)
	}
}

// An idle provider (quota windows, no spend today) collapses to one row whose detail
// lives in a submenu — not a wall of zero rows in the main menu. Providers are
// separated by a Separator row.
func TestRender_CollapsesIdleProvider(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	claude := lines.Snapshot{
		ProviderID: "claude", DisplayName: "Claude", Plan: "Max 20x",
		Lines: []lines.Line{
			{Type: "text", Label: "ROI", Value: "6.1× vs plan ($6.67/day)"},
			{Type: "progress", Label: "Session", Used: pf(23), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(25 * time.Minute))},
			{Type: "text", Label: "Today", Value: "≈ $59 · 46.0M tokens"},
		},
	}
	codex := lines.Snapshot{
		ProviderID: "codex", DisplayName: "Codex", Plan: "Plus", Idle: true,
		Lines: []lines.Line{
			{Type: "progress", Label: "Session", Used: pf(1), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(4 * time.Hour))},
			{Type: "progress", Label: "Weekly", Used: pf(0), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(6 * 24 * time.Hour))},
		},
	}
	st := Render([]lines.Snapshot{claude, codex}, now)

	var codexRow *Item
	var sawSeparator, topLevelWeekly bool
	for i := range st.Items {
		it := &st.Items[i]
		if it.Separator {
			sawSeparator = true
		}
		if strings.Contains(it.Text, "Codex") {
			codexRow = it
		}
		if strings.Contains(it.Text, "Weekly") {
			topLevelWeekly = true // codex's Weekly must NOT surface at top level
		}
	}
	if !sawSeparator {
		t.Error("want a Separator between provider blocks")
	}
	if codexRow == nil || !codexRow.Header || !strings.Contains(codexRow.Text, "idle") {
		t.Fatalf("idle provider should collapse to one 'idle' Header row: %+v", codexRow)
	}
	if len(codexRow.Children) != 2 {
		t.Errorf("collapsed provider should keep its 2 quota lines in a submenu, got %d", len(codexRow.Children))
	}
	if topLevelWeekly {
		t.Error("an idle provider's quota rows should live in the submenu, not the top level")
	}
}

func TestRender_ProgressPaceAndBar(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	snaps := []lines.Snapshot{
		{ProviderID: "claude", DisplayName: "Claude", Plan: "Team 5x", Lines: []lines.Line{
			{Type: "progress", Label: "Weekly", Used: pf(90), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(24 * time.Hour)), Projection: &lines.Projection{ProjectedUsed: 120, Breaches: true, ETASeconds: 57600}},
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
	for _, want := range []string{"Weekly", "90%", "▓", "on pace to run out", "on pace: ~55%"} {
		if !strings.Contains(joined, want) {
			t.Errorf("items missing %q:\n%s", want, joined)
		}
	}
}

func TestRender_TitleFallsBackToSpendWhenNoGauge(t *testing.T) {
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

func TestBar(t *testing.T) {
	if got := bar(0); got != "░░░░░░░░" {
		t.Errorf("bar(0) = %q", got)
	}
	if got := bar(100); got != "▓▓▓▓▓▓▓▓" {
		t.Errorf("bar(100) = %q", got)
	}
	if n := len([]rune(bar(37))); n != 8 {
		t.Errorf("bar width = %d, want 8", n)
	}
}

func TestSpark(t *testing.T) {
	if got := spark([]int64{0, 0}); got != "▁▁" {
		t.Errorf("spark(zeros) = %q, want ▁▁", got)
	}
	got := []rune(spark([]int64{1, 2, 4}))
	if len(got) != 3 || got[2] != '█' {
		t.Errorf("spark ascending should peak at █, got %q", string(got))
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
