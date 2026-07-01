package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/lines"
)

func pf(v float64) *float64     { return &v }
func tp(t time.Time) *time.Time { return &t }

func TestRender_WedgeAndGauges(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	snaps := []lines.Snapshot{{
		ProviderID: "claude", DisplayName: "Claude", Plan: "Max 20x",
		Trend: []int64{10_000_000, 30_000_000, 20_000_000, 59_000_000},
		Lines: []lines.Line{
			{Type: "text", Label: "ROI", Value: "37× vs plan ($6.67/day)"},
			{Type: "text", Label: "Cache saved", Value: "≈ $889 (78%)"},
			{Type: "progress", Label: "Weekly", Used: pf(26), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent},
				ResetsAt: tp(now.Add(131 * time.Hour)), Color: "#BA7517",
				Projection: &lines.Projection{Breaches: true, ETASeconds: 367200}},
			{Type: "text", Label: "Today", Value: "≈ $243 · 223M tokens"},
		},
	}}
	out := Render(snaps, now)
	for _, want := range []string{
		"Claude", "Max 20x", "37× vs plan", "Cache saved", "≈ $889",
		"Weekly", "26%", "width:26%", "#BA7517", "on pace to run out",
		"≈ $243", "aispend://refresh", "aispend://quit",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("popover HTML missing %q", want)
		}
	}
	if !strings.ContainsAny(out, "▁▂▃▄▅▆▇█") {
		t.Error("want a Trend sparkline in the popover")
	}
}

func TestRender_IdleCollapsed(t *testing.T) {
	snaps := []lines.Snapshot{{
		ProviderID: "codex", DisplayName: "Codex", Plan: "Plus", Idle: true,
		Lines: []lines.Line{{Type: "progress", Label: "Weekly", Used: pf(0), Limit: pf(100), Format: &lines.Format{Kind: lines.Percent}}},
	}}
	out := Render(snaps, time.Now())
	if !strings.Contains(out, "idle today") {
		t.Error("an idle provider should render an 'idle today' row")
	}
}

func TestRender_ProjectedPaceAndColorFallback(t *testing.T) {
	// A non-breaching projection, an invalid color, and no reset instant exercise the
	// projected-pace branch, the color fallback, and the empty-reset path.
	snaps := []lines.Snapshot{{ProviderID: "c", DisplayName: "Claude", Lines: []lines.Line{
		{Type: "progress", Label: "Session", Used: pf(23), Limit: pf(100), Color: "not-a-hex",
			Projection: &lines.Projection{ProjectedUsed: 44}},
	}}}
	out := Render(snaps, time.Now())
	if !strings.Contains(out, "on pace ~44% by reset") {
		t.Error("projected-pace text missing")
	}
	if !strings.Contains(out, "#8a8a8e") {
		t.Error("an invalid color should fall back to the neutral swatch")
	}
}

func TestHumanDur(t *testing.T) {
	cases := map[time.Duration]string{
		0:                "now",
		45 * time.Second: "<1m",
		30 * time.Minute: "30m",
		90 * time.Minute: "1h 30m",
		3 * time.Hour:    "3h",
		26 * time.Hour:   "1d 2h",
		72 * time.Hour:   "3d",
	}
	for d, want := range cases {
		if got := humanDur(d); got != want {
			t.Errorf("humanDur(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestRender_Empty(t *testing.T) {
	if out := Render(nil, time.Now()); !strings.Contains(out, "No AI-coding spend") {
		t.Errorf("empty state should say so, got %q", out)
	}
}

func TestRender_EscapesUntrustedText(t *testing.T) {
	snaps := []lines.Snapshot{{
		ProviderID: "x", DisplayName: "<script>alert(1)</script>",
		Lines: []lines.Line{{Type: "text", Label: "Today", Value: "$1"}},
	}}
	out := Render(snaps, time.Now())
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Error("provider-derived text must be HTML-escaped (no raw <script>)")
	}
}

// The body carries a little vertical padding so the first and last rows clear the
// NSPopover's rounded corners (the web view fills the popover edge-to-edge).
func TestRender_BodyPaddingClearsRoundedCorners(t *testing.T) {
	if out := Render(nil, time.Now()); !strings.Contains(out, "padding:6px 0") {
		t.Error("body should carry vertical padding so content clears the popover's rounded corners")
	}
}

// Each provider card leads with its brand mark, keyed by provider id; an unknown provider
// falls back to a neutral mark. (aria-label doubles as the accessible name and the test hook.)
func TestRender_ProviderLogos(t *testing.T) {
	for id, label := range map[string]string{
		"claude": "Anthropic", "codex": "OpenAI", "gemini": "Gemini", "grok": "AI",
	} {
		out := Render([]lines.Snapshot{{ProviderID: id, DisplayName: id,
			Lines: []lines.Line{{Type: "text", Label: "Today", Value: "$1"}}}}, time.Now())
		if !strings.Contains(out, `aria-label="`+label+`"`) {
			t.Errorf("provider %q should render the %q brand mark", id, label)
		}
	}
}

// The idle one-liner also carries the provider's mark.
func TestRender_IdleShowsLogo(t *testing.T) {
	out := Render([]lines.Snapshot{{ProviderID: "codex", DisplayName: "Codex", Idle: true,
		Lines: []lines.Line{{Type: "progress", Label: "Weekly", Used: pf(0), Limit: pf(100)}}}}, time.Now())
	if !strings.Contains(out, "idle today") || !strings.Contains(out, `aria-label="OpenAI"`) {
		t.Errorf("idle row should carry the provider mark: %s", out)
	}
}
