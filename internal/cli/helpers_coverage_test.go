package cli

import (
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// providerLabel maps known provider ids to display names and passes unknowns through.
func TestProviderLabel(t *testing.T) {
	cases := map[string]string{
		"claude_code": "Claude Code",
		"codex":       "Codex",
		"cursor":      "Cursor",
		"mystery":     "mystery", // unknown → verbatim
	}
	for in, want := range cases {
		if got := providerLabel(in); got != want {
			t.Errorf("providerLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// methodLabel: "—" for none, the single method when uniform, "mixed" otherwise.
func TestMethodLabel(t *testing.T) {
	if got := methodLabel(nil); got != "—" {
		t.Errorf("no methods → %q, want —", got)
	}
	if got := methodLabel(map[string]bool{"token_priced": true}); got != "token_priced" {
		t.Errorf("single method → %q, want token_priced", got)
	}
	if got := methodLabel(map[string]bool{"token_priced": true, "inferred": true}); got != "mixed" {
		t.Errorf("two methods → %q, want mixed", got)
	}
}

// pickView routes each view name to its CostViews field; an unknown view falls back
// to api_equivalent; a nil field reports not-ok.
func TestPickView_AllViews(t *testing.T) {
	api := event.USD(1)
	est := event.USD(2)
	rep := event.USD(3)
	cv := event.CostViews{APIEquivalent: &api, Estimated: &est, Reported: &rep}
	e := event.AgentEvent{CostViews: cv}

	want := map[string]int64{
		"api_equivalent": 1, "estimated": 2, "reported": 3,
		"totally_unknown": 1, // default → api_equivalent
	}
	for view, micros := range want {
		got, ok := pickView(e, view)
		if !ok || got.Micros != micros {
			t.Errorf("pickView(%q) = %d,%v want %d,true", view, got.Micros, ok, micros)
		}
	}
	// A view whose field is nil reports not-ok.
	if _, ok := pickView(event.AgentEvent{CostViews: event.CostViews{APIEquivalent: &api}}, "estimated"); ok {
		t.Error("estimated with a nil field should report not-ok")
	}
}

// groupKey returns the value for each dimension, with an honest sentinel bucket when
// the field is empty.
func TestGroupKey_AllDimsAndSentinels(t *testing.T) {
	full := event.AgentEvent{Repo: "payments", Provider: "claude_code", CostTag: "team",
		SessionID: "s1", GitBranch: "feature/x", GitSHA: "abc123", Model: "claude-opus-4-8"}
	for by, want := range map[string]string{
		"repo": "payments", "provider": "claude_code", "cost_tag": "team",
		"session": "s1", "branch": "feature/x", "commit": "abc123", "model": "claude-opus-4-8",
	} {
		if got := groupKey(full, by); got != want {
			t.Errorf("groupKey(full, %q) = %q, want %q", by, got, want)
		}
	}
	empty := event.AgentEvent{}
	for by, want := range map[string]string{
		"repo": "(no repo)", "cost_tag": "(untagged)", "session": "(no session)",
		"branch": "(no branch)", "commit": "(no commit)",
	} {
		if got := groupKey(empty, by); got != want {
			t.Errorf("groupKey(empty, %q) = %q, want %q", by, got, want)
		}
	}
}

// bar clamps to [0,10] tenths so an out-of-range percentage never overruns the gauge.
func TestBar_Clamps(t *testing.T) {
	if got := bar(150); got != "▓▓▓▓▓▓▓▓▓▓" { // >100% clamps to a full bar
		t.Errorf("bar(150) = %q, want a full bar", got)
	}
	if got := bar(-20); got != "··········" { // <0% clamps to empty (pct<-5 ⇒ negative n)
		t.Errorf("bar(-20) = %q, want an empty bar", got)
	}
	if got := bar(50); got != "▓▓▓▓▓·····" {
		t.Errorf("bar(50) = %q, want half", got)
	}
}

// comma groups thousands and keeps a leading sign for negatives.
func TestComma_Negative(t *testing.T) {
	for in, want := range map[int64]string{
		-12400: "-12,400", -100: "-100", -1000: "-1,000", 0: "0",
	} {
		if got := comma(in); got != want {
			t.Errorf("comma(%d) = %q, want %q", in, got, want)
		}
	}
}

// shortHash truncates a long hash to 12 chars + ellipsis and leaves short ones alone.
func TestShortHash(t *testing.T) {
	if got := shortHash("abcdef"); got != "abcdef" {
		t.Errorf("short hash should pass through, got %q", got)
	}
	if got := shortHash("0123456789abcdef0000"); got != "0123456789ab…" {
		t.Errorf("long hash = %q, want first 12 + …", got)
	}
}

// shortSession shortens a long id to an 8-char prefix + ellipsis and leaves sentinel
// buckets untouched.
func TestShortSession(t *testing.T) {
	if got := shortSession("(no session)"); got != "(no session)" {
		t.Errorf("sentinel must pass through, got %q", got)
	}
	if got := shortSession("3f9c1a2b"); got != "3f9c1a2b" {
		t.Errorf("8-char id should pass through, got %q", got)
	}
	if got := shortSession("3f9c1a2b-aaaa-bbbb"); got != "3f9c1a2b…" {
		t.Errorf("long id = %q, want 8-char prefix + …", got)
	}
}

// spanDays / spanStart derive the --all amortization window from event timestamps,
// ignoring zero (malformed) timestamps so they can't anchor the span to year 1.
func TestSpanDaysAndStart(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 6, d, 12, 0, 0, 0, time.UTC) }

	// empty → 1 day, zero start
	if got := spanDays(nil); got != 1 {
		t.Errorf("spanDays(nil) = %d, want 1", got)
	}
	if got := spanStart(nil); !got.IsZero() {
		t.Errorf("spanStart(nil) = %v, want zero", got)
	}

	// single event → 1 day, its own start
	one := []event.AgentEvent{{TSStart: day(10)}}
	if got := spanDays(one); got != 1 {
		t.Errorf("spanDays(single) = %d, want 1", got)
	}
	if got := spanStart(one); !got.Equal(day(10)) {
		t.Errorf("spanStart(single) = %v, want %v", got, day(10))
	}

	// multi-day span, with a malformed zero-TS event that must be ignored
	multi := []event.AgentEvent{{TSStart: day(10)}, {TSStart: day(14)}, {}}
	if got := spanDays(multi); got != 5 { // 10..14 inclusive = 5 days
		t.Errorf("spanDays(multi) = %d, want 5", got)
	}
	if got := spanStart(multi); !got.Equal(day(10)) {
		t.Errorf("spanStart(multi) = %v, want %v (zero-TS event ignored)", got, day(10))
	}
}

// startOfMonth / startOfDay snap to midnight on the first of the month / the day, in
// the time's own location.
func TestStartOfMonthAndDay(t *testing.T) {
	ts := time.Date(2026, 6, 17, 14, 30, 5, 0, time.UTC)
	if got := startOfMonth(ts); !got.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("startOfMonth = %v, want 2026-06-01 00:00", got)
	}
	if got := startOfDay(ts); !got.Equal(time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("startOfDay = %v, want 2026-06-17 00:00", got)
	}
}
