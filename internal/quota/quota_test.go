package quota

import (
	"strings"
	"testing"
	"time"
)

// Real-shape Codex token_count fixtures (rate_limits verified against ~/.codex
// rollout data + openai/codex exec JSONL, 2026-06): primary = 5-hour window,
// secondary = weekly window; exec mode emits rate_limits:null.
const (
	cxRich        = `{"timestamp":"2026-06-15T10:00:05Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":100}},"rate_limits":{"primary":{"used_percent":0,"window_minutes":299,"resets_in_seconds":17940},"secondary":{"used_percent":6,"window_minutes":10079,"resets_in_seconds":275281}}}}`
	cxNull        = `{"timestamp":"2026-06-15T10:00:06Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":100}},"rate_limits":null}}`
	cxThin        = `{"timestamp":"2026-06-15T10:00:07Z","type":"event_msg","payload":{"type":"token_count","info":null,"rate_limits":{"limit_id":"codex"}}}`
	cxPrimaryOnly = `{"timestamp":"2026-06-15T10:00:08Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":12.5,"window_minutes":300,"resets_in_seconds":600}}}}`
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad fixture time %q: %v", s, err)
	}
	return tm
}

func TestParseCodex_RichBothWindows(t *testing.T) {
	ss := ParseCodex([]byte(cxRich))
	if len(ss) != 2 {
		t.Fatalf("want 2 samples, got %d (%+v)", len(ss), ss)
	}
	var wk *Sample
	for i := range ss {
		if ss[i].Window == WindowWeekly {
			wk = &ss[i]
		}
	}
	if wk == nil {
		t.Fatal("no weekly sample parsed")
	}
	if wk.Provider != "codex" {
		t.Errorf("provider = %q, want codex", wk.Provider)
	}
	if wk.UsedPercent != 6 {
		t.Errorf("used_percent = %v, want 6", wk.UsedPercent)
	}
	if wk.WindowMinutes != 10079 {
		t.Errorf("window_minutes = %d, want 10079", wk.WindowMinutes)
	}
	obs := mustTime(t, "2026-06-15T10:00:05Z")
	if !wk.ObservedAt.Equal(obs) {
		t.Errorf("observed_at = %v, want %v", wk.ObservedAt, obs)
	}
	if want := obs.Add(275281 * time.Second); !wk.ResetsAt.Equal(want) {
		t.Errorf("resets_at = %v, want %v", wk.ResetsAt, want)
	}
	if wk.Source != "codex:rate_limits.secondary" {
		t.Errorf("source = %q", wk.Source)
	}
}

func TestParseCodex_NoSample(t *testing.T) {
	cases := map[string]string{
		"exec-null":     cxNull,
		"thin":          cxThin,
		"agent_message": `{"timestamp":"2026-06-15T10:00:00Z","type":"event_msg","payload":{"type":"agent_message"}}`,
		"malformed":     `{bad json`,
		"empty":         ``,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if ss := ParseCodex([]byte(raw)); ss != nil {
				t.Errorf("want nil, got %+v", ss)
			}
		})
	}
}

func TestParseCodex_PrimaryOnly(t *testing.T) {
	ss := ParseCodex([]byte(cxPrimaryOnly))
	if len(ss) != 1 || ss[0].Window != Window5h {
		t.Fatalf("want one 5h sample, got %+v", ss)
	}
	if ss[0].UsedPercent != 12.5 || ss[0].Source != "codex:rate_limits.primary" {
		t.Errorf("sample = %+v", ss[0])
	}
}

// Real Codex shape (verified against ~/.codex rollout data, 2026-02): resets_at is an
// absolute epoch (not resets_in_seconds), and the WEEKLY window (window_minutes 10080)
// sits in the PRIMARY slot with secondary null. The parser must classify by
// window_minutes and read resets_at — not assume primary==5h or resets_in_seconds.
const cxReal = `{"timestamp":"2026-02-13T14:20:31Z","type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"total_tokens":100}},"rate_limits":{"limit_id":"codex","limit_name":null,"primary":{"used_percent":0.0,"window_minutes":10080,"resets_at":1771577735},"secondary":null,"credits":{"has_credits":false,"unlimited":false,"balance":null},"plan_type":null}}}`

func TestParseCodex_RealShape_ResetsAtAndWindowMinutes(t *testing.T) {
	ss := ParseCodex([]byte(cxReal))
	if len(ss) != 1 {
		t.Fatalf("want one sample (primary=weekly, secondary null), got %+v", ss)
	}
	s := ss[0]
	if s.Window != WindowWeekly {
		t.Errorf("window_minutes 10080 must classify weekly even in the primary slot, got %s", s.Window)
	}
	if !s.ResetsAt.Equal(time.Unix(1771577735, 0).UTC()) {
		t.Errorf("absolute resets_at epoch should parse, got %v", s.ResetsAt)
	}
	if s.UsedPercent != 0 || s.WindowMinutes != 10080 {
		t.Errorf("sample = %+v", s)
	}
}

// Claude's usage snapshot (documented rate_limits shape): five_hour + seven_day use
// used_percentage + epoch resets_at; seven_day_opus uses utilization + a string
// resets_at. Mixed on purpose — the parser must handle both.
const claudeUsage = `{"rate_limits":{"five_hour":{"used_percentage":12.5,"resets_at":1750334400},"seven_day":{"used_percentage":78,"resets_at":1750507200},"seven_day_opus":{"utilization":40,"resets_at":"2026-06-25T09:00:00Z"}}}`

func TestParseClaude_AllWindows(t *testing.T) {
	obs := mustTime(t, "2026-06-19T12:00:00Z")
	ss := ParseClaudeRateLimits([]byte(claudeUsage), obs)
	if len(ss) != 3 {
		t.Fatalf("want 3 windows, got %d (%+v)", len(ss), ss)
	}
	byW := map[Window]Sample{}
	for _, s := range ss {
		byW[s.Window] = s
	}
	wk := byW[WindowWeekly]
	if wk.Provider != "claude" || wk.UsedPercent != 78 {
		t.Errorf("weekly = %+v, want provider=claude used=78", wk)
	}
	if !wk.ResetsAt.Equal(time.Unix(1750507200, 0).UTC()) {
		t.Errorf("weekly resets_at = %v (epoch should parse)", wk.ResetsAt)
	}
	if !wk.ObservedAt.Equal(obs) {
		t.Errorf("observed_at = %v, want %v", wk.ObservedAt, obs)
	}
	if !byW[Window5h].ResetsAt.Equal(time.Unix(1750334400, 0).UTC()) {
		t.Errorf("five_hour resets_at = %v", byW[Window5h].ResetsAt)
	}
	opus := byW[WindowWeeklyOpus]
	if opus.UsedPercent != 40 {
		t.Errorf("opus utilization should map to used percent, got %v", opus.UsedPercent)
	}
	if !opus.ResetsAt.Equal(mustTime(t, "2026-06-25T09:00:00Z")) {
		t.Errorf("opus resets_at (string) should parse, got %v", opus.ResetsAt)
	}
	if opus.Source != "claude:rate_limits.seven_day_opus" {
		t.Errorf("opus source = %q", opus.Source)
	}
}

func TestParseClaude_SkipsBogusAndMissing(t *testing.T) {
	obs := mustTime(t, "2026-06-19T12:00:00Z")
	// five_hour carries the known CC bug (epoch in used_percentage → >100 → skip);
	// seven_day_opus is absent; only seven_day is a valid sample.
	raw := `{"rate_limits":{"five_hour":{"used_percentage":1750000000,"resets_at":1750334400},"seven_day":{"used_percentage":30,"resets_at":1750507200}}}`
	ss := ParseClaudeRateLimits([]byte(raw), obs)
	if len(ss) != 1 || ss[0].Window != WindowWeekly || ss[0].UsedPercent != 30 {
		t.Fatalf("want only a valid weekly sample, got %+v", ss)
	}
}

func TestParseClaude_NoSample(t *testing.T) {
	obs := mustTime(t, "2026-06-19T12:00:00Z")
	for name, raw := range map[string]string{
		"no rate_limits": `{"foo":1}`,
		"null":           `{"rate_limits":null}`,
		"malformed":      `{bad`,
		"empty":          ``,
	} {
		t.Run(name, func(t *testing.T) {
			if ss := ParseClaudeRateLimits([]byte(raw), obs); ss != nil {
				t.Errorf("want nil, got %+v", ss)
			}
		})
	}
}

func TestTracker_KeepsFreshestAndExpires(t *testing.T) {
	tr := NewTracker()
	older := Sample{Provider: "codex", Window: WindowWeekly, UsedPercent: 5,
		ObservedAt: mustTime(t, "2026-06-15T10:00:00Z"), ResetsAt: mustTime(t, "2026-06-18T10:00:00Z")}
	newer := Sample{Provider: "codex", Window: WindowWeekly, UsedPercent: 9,
		ObservedAt: mustTime(t, "2026-06-15T12:00:00Z"), ResetsAt: mustTime(t, "2026-06-18T12:00:00Z")}
	tr.Observe(newer)
	tr.Observe(older) // arrives out of order; must NOT overwrite the fresher reading
	got := tr.Active(mustTime(t, "2026-06-15T13:00:00Z"))
	if len(got) != 1 || got[0].UsedPercent != 9 {
		t.Fatalf("want freshest 9%%, got %+v", got)
	}
	if exp := tr.Active(mustTime(t, "2026-06-19T00:00:00Z")); len(exp) != 0 {
		t.Errorf("sample past its reset should be dropped, got %+v", exp)
	}
}

func TestTracker_ActiveSortedByProviderWindow(t *testing.T) {
	tr := NewTracker()
	now := mustTime(t, "2026-06-15T10:00:00Z")
	reset := now.Add(time.Hour)
	tr.Observe(Sample{Provider: "openai", Window: WindowWeekly, ObservedAt: now, ResetsAt: reset})
	tr.Observe(Sample{Provider: "anthropic", Window: Window5h, ObservedAt: now, ResetsAt: reset})
	tr.Observe(Sample{Provider: "anthropic", Window: WindowWeekly, ObservedAt: now, ResetsAt: reset})
	got := tr.Active(now)
	if len(got) != 3 {
		t.Fatalf("want 3 active, got %d", len(got))
	}
	if got[0].Provider != "anthropic" || got[0].Window != Window5h {
		t.Errorf("order[0] = %+v, want anthropic/5h", got[0])
	}
	if got[2].Provider != "openai" {
		t.Errorf("order[2] = %+v, want openai", got[2])
	}
}

func TestTracker_ObserveCodex(t *testing.T) {
	tr := NewTracker()
	tr.ObserveCodex([]byte(cxRich))
	tr.ObserveCodex([]byte(cxNull)) // no-op
	if got := tr.Active(mustTime(t, "2026-06-15T10:00:05Z")); len(got) != 2 {
		t.Fatalf("want 2 samples from the rich line, got %d", len(got))
	}
}

func TestTracker_ZeroValueUsable(t *testing.T) {
	var tr Tracker // zero value, never went through NewTracker
	tr.ObserveCodex([]byte(cxRich))
	if got := tr.Active(mustTime(t, "2026-06-15T10:00:05Z")); len(got) != 2 {
		t.Fatalf("zero-value Tracker must be safe and functional, got %d samples", len(got))
	}
}

func TestBar(t *testing.T) {
	cases := []struct {
		pct   float64
		width int
		want  string
	}{
		{0, 10, "----------"},
		{100, 10, "##########"},
		{50, 10, "#####-----"},
		{150, 10, "##########"}, // clamp high
		{-5, 4, "----"},         // clamp low
		{42, 0, ""},             // zero width
	}
	for _, c := range cases {
		if got := Bar(c.pct, c.width); got != c.want {
			t.Errorf("Bar(%v,%d) = %q, want %q", c.pct, c.width, got, c.want)
		}
	}
}

func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "now"},
		{-time.Hour, "now"},
		{52 * time.Minute, "52m"},
		{90 * time.Second, "1m"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
		{3 * time.Hour, "3h"},
		{3*24*time.Hour + 4*time.Hour + 28*time.Minute, "3d 4h"},
		{5 * 24 * time.Hour, "5d"},
	}
	for _, c := range cases {
		if got := HumanDuration(c.d); got != c.want {
			t.Errorf("HumanDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

func TestSampleLine(t *testing.T) {
	obs := mustTime(t, "2026-06-15T10:00:05Z")
	s := Sample{Provider: "codex", Window: WindowWeekly, UsedPercent: 6,
		ObservedAt: obs, ResetsAt: obs.Add(275281 * time.Second)}
	line := s.Line(obs)
	for _, want := range []string{"weekly", "6%", "resets in 3d 4h"} {
		if !strings.Contains(line, want) {
			t.Errorf("Line = %q, missing %q", line, want)
		}
	}
}

func TestSampleFreshness(t *testing.T) {
	obs := mustTime(t, "2026-06-15T10:00:00Z")
	s := Sample{ObservedAt: obs}
	if f := s.Freshness(obs.Add(2 * time.Minute)); !strings.Contains(f, "2m") {
		t.Errorf("Freshness = %q, want it to mention 2m", f)
	}
}
