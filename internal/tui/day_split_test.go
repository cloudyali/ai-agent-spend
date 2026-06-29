package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// A session that runs across several UTC calendar days must split into one row per
// day, each carrying only that day's turns and cost. This is what lets a day-group
// subtotal be a true per-calendar-day spend (and reconcile across period windows).
func TestGroupSessions_SplitsByUTCDay(t *testing.T) {
	eng := pricing.NewEngine()
	mk := func(day int, in int64) event.AgentEvent {
		ts := time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC)
		return priced(t, eng, fmt.Sprintf("evt%d", day), "s", "payments", "claude-opus-4-8", ts, event.Tokens{Input: in})
	}
	evs := []event.AgentEvent{mk(20, 1_000_000), mk(20, 1_000_000), mk(21, 2_000_000), mk(24, 4_000_000)}

	rows := groupSessions(evs, "api_equivalent")

	byDay := map[string]sessionStat{}
	var sum int64
	for _, r := range rows {
		if r.id != "s" {
			t.Fatalf("every slice keeps the session id, got %q", r.id)
		}
		byDay[dayKey(r.first, time.UTC)] = r
		sum += r.micros
	}
	if len(rows) != 3 {
		t.Fatalf("a session active on 3 UTC days must yield 3 rows, got %d", len(rows))
	}
	if got := byDay["20260620"].turns; got != 2 {
		t.Errorf("Jun20 slice should hold its 2 turns, got %d", got)
	}
	if got, want := byDay["20260620"].micros, apiMicros(evs[0])+apiMicros(evs[1]); got != want {
		t.Errorf("Jun20 slice cost = %d, want only that day's two turns (%d)", got, want)
	}
	if got := byDay["20260624"].turns; got != 1 {
		t.Errorf("Jun24 slice should hold 1 turn, got %d", got)
	}
	// No money is lost or duplicated by the split.
	var whole int64
	for _, e := range evs {
		whole += apiMicros(e)
	}
	if sum != whole {
		t.Errorf("slices must sum to the session total: got %d, want %d", sum, whole)
	}
}

// Day grouping is a CALCULATION (which calendar day a cost lands in) and must pin to
// the UTC calendar so it lines up with the UTC period window. The per-row clock is
// DISPLAY and stays local. Here a turn at 20:00 UTC Jun 19 is 01:30 IST Jun 20: with
// now at 01:00 UTC Jun 20 it is "Yesterday" on the UTC calendar, even though it is the
// same local day as now in IST.
func TestModel_DayGroupingPinsToUTCNotLocal(t *testing.T) {
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.FixedZone("IST", 5*3600+30*60)

	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 20, 1, 0, 0, 0, time.UTC) // 06:30 IST Jun 20
	ts := time.Date(2026, 6, 19, 20, 0, 0, 0, time.UTC) // 01:30 IST Jun 20; UTC Jun 19
	ev := priced(t, eng, "evt1", "s", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})

	m := New([]Period{{Label: "this week", Events: []event.AgentEvent{ev}, Since: now.AddDate(0, 0, -6), Until: now}}, 0, eng).WithNow(now)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	v := m.View()

	if !strings.Contains(v, "Yesterday") {
		t.Errorf("a turn on UTC Jun 19 with now on UTC Jun 20 must group under Yesterday (UTC calendar):\n%s", v)
	}
	if strings.Contains(v, "Today") {
		t.Errorf("no session on the current UTC day → no Today header; grouping must not use the local day:\n%s", v)
	}
	if !strings.Contains(v, "1:30am") {
		t.Errorf("the row clock must still render in LOCAL time (1:30am IST):\n%s", v)
	}
}

// The regression: a calendar day that sits fully inside two different period windows must
// report the same spend in both. Before the per-day split, a session that straddled the
// week-window's lower edge had its whole (clipped) cost dumped on its last day, so
// "Yesterday" read smaller under "this week" than under "this month".
func TestDaySubtotalsStableAcrossWindows(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	mk := func(day int, in int64) event.AgentEvent {
		ts := time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC)
		return priced(t, eng, fmt.Sprintf("e%d", day), "s", "payments", "claude-opus-4-8", ts, event.Tokens{Input: in})
	}
	// One session running Jun 20 → Jun 24; the UTC week window starts Jun 22, so it clips
	// the Jun 20–21 turns. Jun 22/23/24 are fully inside both the week and the month window.
	all := []event.AgentEvent{mk(20, 5_000_000), mk(21, 5_000_000), mk(22, 1_000_000), mk(23, 1_000_000), mk(24, 7_000_000)}
	weekStart := time.Date(2026, 6, 22, 0, 0, 0, 0, time.UTC)
	var week []event.AgentEvent
	for _, e := range all {
		if !e.TSStart.Before(weekStart) { // what the UTC week window admits
			week = append(week, e)
		}
	}

	month := New([]Period{{Label: "this month", Events: all, Since: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), Until: now}}, 0, eng).WithNow(now)
	wk := New([]Period{{Label: "this week", Events: week, Since: weekStart, Until: now}}, 0, eng).WithNow(now)

	ms, ws := daySubtotals(month.rows), daySubtotals(wk.rows)
	for _, day := range []string{"20260622", "20260623", "20260624"} {
		if ms[day] == 0 || ws[day] == 0 {
			t.Fatalf("day %s should be non-zero in both windows (month=%d week=%d)", day, ms[day], ws[day])
		}
		if ms[day] != ws[day] {
			t.Errorf("day %s must match across windows: month=%d week=%d", day, ms[day], ws[day])
		}
	}
	if got, want := ws["20260624"], apiMicros(all[4]); got != want {
		t.Errorf("Jun24 subtotal = %d, want only Jun24's own turn (%d), not the whole session", got, want)
	}
	if _, ok := ws["20260620"]; ok {
		t.Errorf("the week window must omit Jun 20 entirely")
	}
}

// Turns are bucketed by TSStart's UTC day, so a turn that starts just before midnight
// stays on its start day even though it ends after midnight.
func TestGroupSessions_SplitsOnTSStartAcrossMidnight(t *testing.T) {
	eng := pricing.NewEngine()
	e1 := priced(t, eng, "a", "s", "p", "claude-opus-4-8", time.Date(2026, 6, 24, 23, 50, 0, 0, time.UTC), event.Tokens{Input: 1_000_000})
	e2 := priced(t, eng, "b", "s", "p", "claude-opus-4-8", time.Date(2026, 6, 25, 0, 30, 0, 0, time.UTC), event.Tokens{Input: 1_000_000})

	rows := groupSessions([]event.AgentEvent{e1, e2}, "api_equivalent")
	if len(rows) != 2 {
		t.Fatalf("two turns on opposite sides of UTC midnight → 2 day slices, got %d", len(rows))
	}
	byDay := map[string]sessionStat{}
	for _, r := range rows {
		byDay[r.day] = r
	}
	if byDay["20260624"].turns != 1 || byDay["20260625"].turns != 1 {
		t.Errorf("each UTC day should hold exactly its own turn: %+v", byDay)
	}
}
