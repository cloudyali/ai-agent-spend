package tui

import (
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

// June 2026: the 19th is a Friday (per env), so 18th=Thursday, 15th=Monday.
func TestDayKeyAndLabel(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, loc)
	today := time.Date(2026, 6, 19, 9, 0, 0, 0, loc)
	yest := time.Date(2026, 6, 18, 23, 0, 0, 0, loc)
	older := time.Date(2026, 6, 15, 10, 0, 0, 0, loc)

	if k := dayKey(today, loc); k != "20260619" {
		t.Errorf("dayKey(today) = %q, want 20260619", k)
	}
	if l := dayLabel(today, now, loc); l != "Today" {
		t.Errorf("dayLabel(today) = %q, want Today", l)
	}
	if l := dayLabel(yest, now, loc); l != "Yesterday" {
		t.Errorf("dayLabel(yesterday) = %q, want Yesterday", l)
	}
	if l := dayLabel(older, now, loc); l != "Mon Jun 15" {
		t.Errorf("dayLabel(older) = %q, want Mon Jun 15", l)
	}
	// With no reference now, even today's date renders absolute (deterministic).
	if l := dayLabel(today, time.Time{}, loc); l != "Fri Jun 19" {
		t.Errorf("dayLabel(today, zero-now) = %q, want Fri Jun 19", l)
	}
	// A nil location defaults to UTC (defensive — callers pass time.UTC).
	if k := dayKey(today, nil); k != "20260619" {
		t.Errorf("dayKey(nil loc) = %q, want 20260619", k)
	}
	if l := dayLabel(today, now, nil); l != "Today" {
		t.Errorf("dayLabel(nil loc) = %q, want Today", l)
	}
}

func TestIsLive(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	win := 10 * time.Minute
	if !isLive(now.Add(-5*time.Minute), now, win) {
		t.Error("activity 5m ago within a 10m window should be live")
	}
	if isLive(now.Add(-30*time.Minute), now, win) {
		t.Error("activity 30m ago should not be live")
	}
	if isLive(now, time.Time{}, win) {
		t.Error("a zero now can't determine liveness → not live")
	}
}

func TestSessionSpanAndActive(t *testing.T) {
	t0 := time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC)
	s := sessionStat{
		first: t0,
		last:  t0.Add(2 * time.Hour),
		evs: []event.AgentEvent{
			{ActiveMS: 1000},
			{ActiveMS: 500},
		},
	}
	if got := sessionSpan(s); got != 2*time.Hour {
		t.Errorf("sessionSpan = %v, want 2h", got)
	}
	if got := sessionActiveMS(s); got != 1500 {
		t.Errorf("sessionActiveMS = %d, want 1500", got)
	}
	// A reversed pair (last before first) clamps to zero rather than going negative.
	if got := sessionSpan(sessionStat{first: t0.Add(time.Hour), last: t0}); got != 0 {
		t.Errorf("sessionSpan(reversed) = %v, want 0", got)
	}
}

func TestOrderForDayList_DayGroupsLiveFirstThenCost(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, loc)
	mk := func(id string, last time.Time, micros int64) sessionStat {
		return sessionStat{id: id, first: last.Add(-time.Hour), last: last, micros: micros}
	}
	a := mk("a", time.Date(2026, 6, 18, 11, 0, 0, 0, loc), 500) // yesterday
	b := mk("b", time.Date(2026, 6, 19, 10, 0, 0, 0, loc), 100) // today, cheap
	c := mk("c", time.Date(2026, 6, 19, 11, 57, 0, 0, loc), 50) // today, live (3m ago), cheapest
	d := mk("d", time.Date(2026, 6, 19, 8, 30, 0, 0, loc), 900) // today, priciest

	got := orderForDayList([]sessionStat{a, b, c, d}, now, loc, 10*time.Minute)
	want := []string{"c", "d", "b", "a"} // today (live c, then $900 d, then $100 b), then yesterday a
	for i, id := range want {
		if got[i].id != id {
			t.Fatalf("order = %v, want %v", ids(got), want)
		}
	}
}

func TestOrderForDayList_SingleDayPreservesCostDesc(t *testing.T) {
	loc := time.UTC
	d := time.Date(2026, 6, 19, 0, 0, 0, 0, loc)
	mk := func(id string, h int, micros int64) sessionStat {
		return sessionStat{id: id, first: d.Add(time.Duration(h) * time.Hour), last: d.Add(time.Duration(h) * time.Hour), micros: micros}
	}
	// no reference now → no liveness; single day must stay priciest-first (legacy order)
	got := orderForDayList([]sessionStat{mk("x", 9, 100), mk("y", 10, 300), mk("z", 11, 200)}, time.Time{}, loc, 10*time.Minute)
	want := []string{"y", "z", "x"}
	for i, id := range want {
		if got[i].id != id {
			t.Fatalf("single-day order = %v, want %v (priciest-first)", ids(got), want)
		}
	}
}

func ids(rows []sessionStat) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.id
	}
	return out
}

func TestAnyLive(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	rows := []sessionStat{{last: now.Add(-2 * time.Hour)}, {last: now.Add(-4 * time.Minute)}}
	if !anyLive(rows, now, liveWindow) {
		t.Error("a 4-minutes-ago session should make the set live")
	}
	if anyLive(rows[:1], now, liveWindow) {
		t.Error("only a 2-hours-ago session → not live")
	}
	if anyLive(rows, time.Time{}, liveWindow) {
		t.Error("a zero now can never be live")
	}
}

func TestLiveLegendText(t *testing.T) {
	if got := liveLegendText(10 * time.Minute); got != "live — active in the last 10m" {
		t.Errorf("liveLegendText = %q, want \"live — active in the last 10m\"", got)
	}
}

func TestDistinctSessionDays(t *testing.T) {
	d := time.Date(2026, 6, 19, 9, 0, 0, 0, time.UTC)
	rows := []sessionStat{{last: d}, {last: d.Add(3 * time.Hour)}, {last: d.AddDate(0, 0, -1)}}
	if got := distinctSessionDays(rows, time.UTC); got != 2 {
		t.Errorf("distinctSessionDays = %d, want 2 (two calendar days)", got)
	}
}

// Visual times localize: a UTC instant renders its wall clock and calendar day in the
// display zone, so day grouping (Today/Yesterday) tracks the LOCAL day, not UTC.
func TestClockTimeAndDayLabelLocalize(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60)
	utc := time.Date(2026, 6, 19, 20, 0, 0, 0, time.UTC) // == 2026-06-20 01:30 IST
	if got := clockTime(utc, ist); got != "1:30am" {
		t.Errorf("clockTime(ist) = %q, want 1:30am", got)
	}
	if got := dayKey(utc, ist); got != "20260620" {
		t.Errorf("dayKey(ist) = %q, want 20260620 (local day rolled over)", got)
	}
	now := time.Date(2026, 6, 20, 2, 0, 0, 0, ist)
	if got := dayLabel(utc, now, ist); got != "Today" {
		t.Errorf("dayLabel(ist) = %q, want Today (same local day)", got)
	}
}
