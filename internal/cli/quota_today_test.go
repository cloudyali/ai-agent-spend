package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

func writeUsage(t *testing.T, home, raw string) {
	t.Helper()
	cdir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(cdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cdir, "usage-exact.json"), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appWithHome(home string, out *strings.Builder, now time.Time) *App {
	return &App{
		Resolver: platform.Resolver{GOOS: "linux", Home: home, Env: func(string) string { return "" }},
		Out:      out,
		Now:      func() time.Time { return now },
	}
}

func TestClaudeQuotaSamples_ReadsSnapshot(t *testing.T) {
	home := t.TempDir()
	writeUsage(t, home, `{"rate_limits":{"seven_day":{"used_percentage":78,"resets_at":1750507200}}}`)
	a := appWithHome(home, &strings.Builder{}, time.Now())
	ss := a.claudeQuotaSamples(time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC))
	if len(ss) != 1 || ss[0].UsedPercent != 78 || ss[0].Provider != "claude" {
		t.Fatalf("want one Claude weekly sample at 78%%, got %+v", ss)
	}
}

func TestClaudeQuotaSamples_AbsentIsEmpty(t *testing.T) {
	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	if ss := a.claudeQuotaSamples(time.Now()); ss != nil {
		t.Errorf("absent snapshot should yield no samples, got %+v", ss)
	}
}

func TestRenderToday_ShowsQuotaGauge(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	reset := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC).Unix() // in the future → active
	writeUsage(t, home, fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":78,"resets_at":%d}}}`, reset))

	var out strings.Builder
	a := appWithHome(home, &out, now)
	m := event.USD(5_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now, TSEnd: now, Tokens: event.Tokens{Input: 100_000}, CostViews: event.CostViews{APIEquivalent: &m}}
	a.renderToday([]event.AgentEvent{e}, now, config.PlanSet{}, 1, pricing.NewEngine())

	v := out.String()
	for _, want := range []string{"Claude", "78%", "resets in"} {
		if !strings.Contains(v, want) {
			t.Errorf("today glance should show the Claude weekly gauge (%q):\n%s", want, v)
		}
	}
}

func writeCodexRollout(t *testing.T, home, rel, line string) {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", rel)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-x.jsonl"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCodexQuotaSamples_ReadsRateLimits(t *testing.T) {
	home := t.TempDir()
	writeCodexRollout(t, home, "2026/06/19",
		`{"timestamp":"2026-06-19T11:59:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":100}},"rate_limits":{"secondary":{"used_percent":42,"window_minutes":10080,"resets_in_seconds":200000}}}}`)
	a := appWithHome(home, &strings.Builder{}, time.Now())
	got := a.codexQuotaSamples(time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC))
	if len(got) != 1 || got[0].Provider != "codex" || got[0].UsedPercent != 42 {
		t.Fatalf("want one Codex weekly sample at 42%%, got %+v", got)
	}
}

func TestCodexQuotaSamples_NoneWhenAbsent(t *testing.T) {
	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	if got := a.codexQuotaSamples(time.Now()); got != nil {
		t.Errorf("no Codex sessions should yield no samples, got %+v", got)
	}
}

func TestRenderToday_ShowsCodexQuotaGauge(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	writeCodexRollout(t, home, "2026/06/19",
		`{"timestamp":"2026-06-19T11:59:00Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":100}},"rate_limits":{"secondary":{"used_percent":42,"window_minutes":10080,"resets_in_seconds":200000}}}}`)
	var out strings.Builder
	a := appWithHome(home, &out, now)
	m := event.USD(5_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "codex", Model: "gpt-5.3-codex",
		TSStart: now, TSEnd: now, Tokens: event.Tokens{Input: 100_000}, CostViews: event.CostViews{APIEquivalent: &m}}
	a.renderToday([]event.AgentEvent{e}, now, config.PlanSet{}, 1, pricing.NewEngine())
	v := out.String()
	for _, want := range []string{"Codex", "42%", "resets in"} {
		if !strings.Contains(v, want) {
			t.Errorf("today should show the Codex weekly gauge (%q):\n%s", want, v)
		}
	}
}

func TestRenderBudget_ShowsPace(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("budget_usd = 500\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC) // mid-month
	var out strings.Builder
	a := appWithHome(home, &out, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(250_000_000) // $250 spent this month → 50% of a $500 budget at ~half the month
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-48 * time.Hour), TSEnd: now.Add(-48 * time.Hour), CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Write([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}
	a.renderBudget(st, now)
	v := out.String()
	for _, want := range []string{"budget", "$500", "50%", "on track"} {
		if !strings.Contains(v, want) {
			t.Errorf("budget pace line missing %q:\n%s", want, v)
		}
	}
}

func TestRenderBudget_OffByDefault(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".aispend"), 0o755); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	a := appWithHome(home, &out, time.Now()) // no config.toml → no budget
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	a.renderBudget(st, time.Now())
	if out.String() != "" {
		t.Errorf("no budget configured → nothing printed, got:\n%s", out.String())
	}
}

func TestRenderToday_UnknownWhenSnapshotAbsent(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	var out strings.Builder
	a := appWithHome(t.TempDir(), &out, now) // no usage-exact.json
	m := event.USD(5_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now, TSEnd: now, Tokens: event.Tokens{Input: 100_000}, CostViews: event.CostViews{APIEquivalent: &m}}
	a.renderToday([]event.AgentEvent{e}, now, config.PlanSet{}, 1, pricing.NewEngine())
	v := out.String()
	if strings.Contains(v, "resets in") {
		t.Errorf("with no snapshot there is no real gauge (no countdown):\n%s", v)
	}
	// ...but Claude activity with no snapshot gets an explicit, explained blank.
	if !strings.Contains(v, "Claude weekly") || !strings.Contains(v, "unknown") {
		t.Errorf("expected the explicit 'Claude weekly — unknown' line:\n%s", v)
	}
}
