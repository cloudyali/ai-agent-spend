package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/lines"
)

func TestUsageSnapshots_BuildsProviderSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	reset := now.Add(24 * time.Hour).Unix() // 90% weekly with 24h left → breaches
	writeUsage(t, home, fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":90,"resets_at":%d}}}`, reset))

	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(12_500_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-2 * time.Hour), TSEnd: now.Add(-2 * time.Hour), CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}

	snaps := a.UsageSnapshots(now)
	if len(snaps) != 1 {
		t.Fatalf("want 1 provider snapshot, got %d: %+v", len(snaps), snaps)
	}
	s := snaps[0]
	if s.ProviderID != "claude" || s.DisplayName != "Claude" {
		t.Errorf("snapshot identity = %q/%q, want claude/Claude", s.ProviderID, s.DisplayName)
	}
	if s.FetchedAt.IsZero() {
		t.Error("snapshot should carry a fetchedAt")
	}

	var prog *lines.Line
	for i := range s.Lines {
		if s.Lines[i].Type == "progress" {
			prog = &s.Lines[i]
			break
		}
	}
	if prog == nil || prog.Used == nil || *prog.Used != 90 {
		t.Fatalf("expected a 90%% progress line, got %+v", s.Lines)
	}
	if prog.Format == nil || prog.Format.Kind != lines.Percent {
		t.Errorf("progress format = %+v, want percent", prog.Format)
	}
	if prog.Projection == nil || !prog.Projection.Breaches {
		t.Errorf("progress projection should breach, got %+v", prog.Projection)
	}
	if prog.Color != lines.SevWarn.Hex() { // 90% < 95% but on pace → amber
		t.Errorf("progress color = %q, want warn %q", prog.Color, lines.SevWarn.Hex())
	}

	today := false
	for _, ln := range s.Lines {
		if ln.Type == "text" && ln.Label == "Today" {
			today = true
		}
	}
	if !today {
		t.Errorf("snapshot should include today's spend line: %+v", s.Lines)
	}
}

func TestUsageSnapshots_SpendWithoutQuota(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir() // no usage-exact.json → no quota sample at all
	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(3_400_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-1 * time.Hour), TSEnd: now.Add(-1 * time.Hour), CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}

	snaps := a.UsageSnapshots(now)
	if len(snaps) != 1 || snaps[0].ProviderID != "claude" {
		t.Fatalf("spend with no quota should still yield a snapshot: %+v", snaps)
	}
	hasToday, hasProgress := false, false
	for _, ln := range snaps[0].Lines {
		if ln.Type == "text" && strings.Contains(ln.Value, "3.40") {
			hasToday = true
		}
		if ln.Type == "progress" {
			hasProgress = true
		}
	}
	if !hasToday || hasProgress {
		t.Errorf("want a Today $ line and no gauge for a spend-only provider: %+v", snaps[0].Lines)
	}
}

func TestUsageSnapshots_AmplifiesWedge(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	cfg := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("plan = \"max\"\nmonthly_fee_usd = 200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	// A cache-heavy turn: the cached cost ($1) is far below the un-cached cost, so the
	// engine's WithoutCache exceeds it → a real "cache saved" figure. A subscription
	// plan is configured → a real ROI.
	m := event.USD(1_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-1 * time.Hour), TSEnd: now.Add(-1 * time.Hour),
		Tokens:    event.Tokens{Input: 1000, CacheRead: 2_000_000},
		CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}

	snaps := a.UsageSnapshots(now)
	if len(snaps) != 1 {
		t.Fatalf("want 1 snapshot, got %+v", snaps)
	}
	s := snaps[0]
	if s.Plan != "max" {
		t.Errorf("plan header = %q, want max", s.Plan)
	}
	var hasROI, hasCache, hasTokens bool
	for _, ln := range s.Lines {
		if ln.Label == "ROI" && strings.Contains(ln.Value, "×") {
			hasROI = true
		}
		if ln.Label == "Cache saved" {
			hasCache = true
		}
		if ln.Label == "Today" && strings.Contains(ln.Value, "tokens") {
			hasTokens = true
		}
	}
	if !hasROI {
		t.Errorf("expected an ROI line (the wedge OpenUsage can't show): %+v", s.Lines)
	}
	if !hasCache {
		t.Errorf("expected a Cache saved line: %+v", s.Lines)
	}
	if !hasTokens {
		t.Errorf("expected today's tokens alongside $: %+v", s.Lines)
	}
}

func TestUsageSnapshots_TidyPlanLabel(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	cfg := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	// A catalog plan id should render as its tidy label, minus the redundant provider
	// word (the header already says "Claude"): "claude-max-20x" → "Max 20x".
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("plan = \"claude-max-20x\"\nmonthly_fee_usd = 200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(5_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-1 * time.Hour), TSEnd: now.Add(-1 * time.Hour), CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}
	snaps := a.UsageSnapshots(now)
	if len(snaps) != 1 || snaps[0].Plan != "Max 20x" {
		t.Errorf("plan header should tidy to \"Max 20x\", got %q (%+v)", snaps[0].Plan, snaps)
	}
}

func TestUsageSnapshots_DetectsCodexPlan(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	// Codex rollout carries an active weekly window + plan_type "pro"; NO codex_plan config.
	writeCodexRollout(t, home, "2026/06/19",
		`{"timestamp":"2026-06-19T11:59:00Z","type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":{"secondary":{"used_percent":30,"window_minutes":10080,"resets_in_seconds":300000},"plan_type":"pro"}}}`)
	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(4_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "codex", Model: "gpt-5.3-codex",
		TSStart: now.Add(-1 * time.Hour), TSEnd: now.Add(-1 * time.Hour), CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}

	var codex *lines.Snapshot
	for _, s := range a.UsageSnapshots(now) {
		if s.ProviderID == "codex" {
			c := s
			codex = &c
		}
	}
	if codex == nil {
		t.Fatal("expected a codex snapshot")
	}
	// Auto-detected from plan_type → a ChatGPT Pro plan header and a real ROI line.
	if !strings.Contains(codex.Plan, "Pro") {
		t.Errorf("codex plan header = %q, want a detected ChatGPT Pro", codex.Plan)
	}
	hasROI := false
	for _, ln := range codex.Lines {
		if ln.Label == "ROI" {
			hasROI = true
		}
	}
	if !hasROI {
		t.Errorf("a detected plan should yield an ROI line: %+v", codex.Lines)
	}
}

// The After ordering: ROI + Cache saved LEAD (before the quota gauges) and Today is
// DEMOTED below them.
func TestUsageSnapshots_WedgeLeadsTodayDemoted(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	cfg := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "config.toml"), []byte("plan = \"max\"\nmonthly_fee_usd = 200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reset := now.Add(24 * time.Hour).Unix()
	writeUsage(t, home, fmt.Sprintf(`{"rate_limits":{"seven_day":{"used_percentage":50,"resets_at":%d}}}`, reset))
	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(1_000_000) // cache-heavy → real Cache saved; plan set → real ROI
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart: now.Add(-2 * time.Hour), TSEnd: now.Add(-2 * time.Hour),
		Tokens:    event.Tokens{Input: 1000, CacheRead: 2_000_000},
		CostViews: event.CostViews{APIEquivalent: &m}}
	if err := st.Upsert([]event.AgentEvent{e}); err != nil {
		t.Fatal(err)
	}

	s := a.UsageSnapshots(now)[0]
	roiIdx, cacheIdx, firstProg, lastProg, todayIdx := -1, -1, -1, -1, -1
	for i, ln := range s.Lines {
		switch {
		case ln.Type == "progress":
			if firstProg == -1 {
				firstProg = i
			}
			lastProg = i
		case ln.Label == "ROI":
			roiIdx = i
		case ln.Label == "Cache saved":
			cacheIdx = i
		case ln.Label == "Today":
			todayIdx = i
		}
	}
	if firstProg == -1 {
		t.Fatalf("expected a quota progress line: %+v", s.Lines)
	}
	if roiIdx == -1 || roiIdx >= firstProg {
		t.Errorf("ROI should lead before the quota gauges (roi=%d firstProg=%d): %+v", roiIdx, firstProg, s.Lines)
	}
	if cacheIdx == -1 || cacheIdx >= firstProg {
		t.Errorf("Cache saved should lead before the quota gauges (cache=%d firstProg=%d)", cacheIdx, firstProg)
	}
	if todayIdx == -1 || todayIdx <= lastProg {
		t.Errorf("Today should be demoted below the quota gauges (today=%d lastProg=%d)", todayIdx, lastProg)
	}
}

// A provider with a quota window but no spend today is marked Idle (the menu collapses it).
func TestUsageSnapshots_MarksIdleProvider(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	writeCodexRollout(t, home, "2026/06/19",
		`{"timestamp":"2026-06-19T11:59:00Z","type":"event_msg","payload":{"type":"token_count","info":{},"rate_limits":{"secondary":{"used_percent":5,"window_minutes":10080,"resets_in_seconds":300000}}}}`)
	a := appWithHome(home, &strings.Builder{}, now)
	if _, err := a.openStore(); err != nil { // store exists, but no events → no spend today
		t.Fatal(err)
	}
	var codex *lines.Snapshot
	for _, s := range a.UsageSnapshots(now) {
		if s.ProviderID == "codex" {
			c := s
			codex = &c
		}
	}
	if codex == nil {
		t.Fatal("expected a codex snapshot from the quota window")
	}
	if !codex.Idle {
		t.Errorf("quota but no spend today should mark the provider Idle: %+v", codex)
	}
	hasProg, hasToday := false, false
	for _, ln := range codex.Lines {
		if ln.Type == "progress" {
			hasProg = true
		}
		if ln.Label == "Today" {
			hasToday = true
		}
	}
	if !hasProg || hasToday {
		t.Errorf("idle codex should have a gauge and no Today line: %+v", codex.Lines)
	}
}

// The snapshot carries a 7-day per-day spend series for the menu-bar Trend sparkline.
func TestUsageSnapshots_AttachesTrend(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	home := t.TempDir()
	a := appWithHome(home, &strings.Builder{}, now)
	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	mk := func(id string, ago time.Duration, micros int64) event.AgentEvent {
		m := event.USD(micros)
		return event.AgentEvent{EventID: id, SessionID: id, Provider: "claude_code", Model: "claude-opus-4-8",
			TSStart: now.Add(-ago), TSEnd: now.Add(-ago), CostViews: event.CostViews{APIEquivalent: &m}}
	}
	if err := st.Upsert([]event.AgentEvent{
		mk("e1", 2*time.Hour, 5_000_000),  // today
		mk("e2", 26*time.Hour, 3_000_000), // yesterday
		mk("e3", 50*time.Hour, 2_000_000), // two days ago
	}); err != nil {
		t.Fatal(err)
	}

	s := a.UsageSnapshots(now)[0]
	if len(s.Trend) != 7 {
		t.Fatalf("want a 7-day trend series, got %d: %v", len(s.Trend), s.Trend)
	}
	if s.Trend[6] <= 0 || s.Trend[5] <= 0 || s.Trend[4] <= 0 {
		t.Errorf("trend should carry today + the prior two days: %v", s.Trend)
	}
	var sum int64
	for _, v := range s.Trend {
		sum += v
	}
	if sum != 10_000_000 {
		t.Errorf("trend sum = %d, want 10_000_000", sum)
	}
}
