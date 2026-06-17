package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

func priced(t *testing.T, eng *pricing.Engine, id, sess, repo, model string, ts time.Time, tk event.Tokens) event.AgentEvent {
	t.Helper()
	e := event.AgentEvent{EventID: id, SessionID: sess, Repo: repo, Provider: "claude_code", Model: model, Tokens: tk, TSStart: ts, TSEnd: ts.Add(time.Minute)}
	if err := eng.Price(&e, pricing.Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	return e
}

// The hero flow: rows are anchored on repo/when (not opaque hashes), priciest
// first; ↵ drills to a receipt with composition + arbitrage; esc back; q quits.
func TestModel_NavigateDrillBack(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	periods := []Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "evt_aaaa1111bbbb", "3f9c", "payments", "claude-opus-4-8", base, event.Tokens{Input: 100_000, Output: 50_000, CacheRead: 5_000_000}),
		priced(t, eng, "evt_bbbb2222cccc", "3f9c", "payments", "claude-sonnet-4", base.Add(time.Hour), event.Tokens{Input: 20_000}),
		priced(t, eng, "evt_cccc3333dddd", "a17d", "web", "claude-opus-4-8", base.Add(2*time.Hour), event.Tokens{Input: 50_000, CacheRead: 20_000_000}),
	}}}
	m := New(periods, 0, eng)

	v := m.View()
	if !strings.Contains(v, "today") || !strings.Contains(v, "web") || !strings.Contains(v, "payments") {
		t.Fatalf("list should show period + repos:\n%s", v)
	}
	if strings.Index(v, "web") > strings.Index(v, "payments") {
		t.Fatalf("sessions should be priciest-first (web/a17d $10 before payments/3f9c $4):\n%s", v)
	}
	// rows must NOT carry the opaque session hash anymore
	if strings.Contains(v, "a17d") || strings.Contains(v, "3f9c") {
		t.Errorf("list rows should not show raw session hashes:\n%s", v)
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model) // cursor → 1 (payments/3f9c)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatal("enter should open the receipt")
	}
	r := m.View()
	for _, want := range []string{"payments", "total", "composition", "cache-read", "without cache", "top turns", "aaaa1111"} {
		if !strings.Contains(r, want) {
			t.Errorf("receipt missing %q:\n%s", want, r)
		}
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeList {
		t.Fatal("esc should return to the list")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should issue a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q should quit")
	}
}

func TestModel_PeriodScrub(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	periods := []Period{
		{Label: "today", Events: []event.AgentEvent{priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})}},
		{Label: "this week", Events: []event.AgentEvent{
			priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
			priced(t, eng, "e2", "s2", "r", "claude-sonnet-4", ts, event.Tokens{Input: 2_000_000}),
		}},
	}
	m := New(periods, 0, eng)
	if m.label() != "today" || len(m.rows) != 1 {
		t.Fatalf("start today/1 row, got %s/%d", m.label(), len(m.rows))
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = nm.(Model)
	if m.label() != "this week" || len(m.rows) != 2 {
		t.Fatalf("right → this week/2 rows, got %s/%d", m.label(), len(m.rows))
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // can't go past the first
	m = nm.(Model)
	if m.label() != "today" {
		t.Fatalf("left should clamp at today, got %s", m.label())
	}
}

func TestModel_KeysAndModes(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: 4_000_000}),
		priced(t, eng, "e2", "s2", "r", "claude-sonnet-4", ts, event.Tokens{Input: 2_000_000}),
	}}}, 0, eng)

	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = nm.(Model)
	if m.w != 120 || m.h != 40 {
		t.Fatalf("window size not recorded: %dx%d", m.w, m.h)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	if m.cursor != 0 {
		t.Fatalf("up at top stays 0, got %d", m.cursor)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = nm.(Model)
	if m.cursor != 1 {
		t.Fatalf("j should move down, got %d", m.cursor)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = nm.(Model)
	if m.cursor != 0 {
		t.Fatalf("k should move up, got %d", m.cursor)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || func() bool { _, ok := cmd().(tea.QuitMsg); return !ok }() {
		t.Fatal("ctrl+c should quit")
	}
	// q in receipt mode returns to the list (does not quit)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = nm.(Model)
	if m.mode != modeList || cmd != nil {
		t.Fatal("q in receipt mode should go back, not quit")
	}
}

// A long list clamps to the viewport with a "more" indicator, and the window
// follows the cursor to the bottom.
func TestModel_ScrollWindow(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 17, 1, 0, 0, 0, time.UTC)
	var evs []event.AgentEvent
	for i := 0; i < 20; i++ {
		evs = append(evs, priced(t, eng, fmt.Sprintf("e%02d", i), fmt.Sprintf("s%02d", i), "r",
			"claude-opus-4-8", base.Add(time.Duration(i)*time.Minute), event.Tokens{Input: int64((20 - i) * 100_000)}))
	}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = nm.(Model)
	if s, e := m.windowRange(len(m.rows)); e-s >= len(m.rows) {
		t.Fatalf("expected a clamped window, got %d of %d", e-s, len(m.rows))
	}
	if !strings.Contains(m.View(), "more") {
		t.Errorf("a clamped list should show a 'more' indicator:\n%s", m.View())
	}
	for i := 0; i < 19; i++ {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	if _, e := m.windowRange(len(m.rows)); e != len(m.rows) {
		t.Errorf("cursor at the bottom should reveal the last row, end=%d n=%d", e, len(m.rows))
	}
}

// `v` cycles the cost view; the header names it and totals/ranking follow it.
func TestModel_ViewSwitch(t *testing.T) {
	api := event.USD(5_000_000)
	rep := event.USD(4_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart:   time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC),
		CostViews: event.CostViews{APIEquivalent: &api, Reported: &rep}}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, pricing.NewEngine())

	if m.view() != "api_equivalent" {
		t.Fatalf("default view = %q", m.view())
	}
	if v := m.View(); !strings.Contains(v, "api-equivalent") || !strings.Contains(v, "$5.00") {
		t.Errorf("api-equivalent view should show $5.00 and name itself:\n%s", v)
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = nm.(Model)
	if m.view() != "reported" {
		t.Fatalf("after v, view = %q, want reported", m.view())
	}
	if v := m.View(); !strings.Contains(v, "reported") || !strings.Contains(v, "$4.00") {
		t.Errorf("reported view should show $4.00:\n%s", v)
	}
}

func TestListColumnHeader(t *testing.T) {
	eng := pricing.NewEngine()
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "repo", "claude-opus-4-8", time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC), event.Tokens{Input: 1_000_000}),
	}}}, 0, eng)
	v := m.View()
	for _, h := range []string{"COST", "SHARE", "WHEN", "PROJECT", "TURNS", "MODEL"} {
		if !strings.Contains(v, h) {
			t.Errorf("list missing column header %q:\n%s", h, v)
		}
	}
}

func TestTimeLayout(t *testing.T) {
	// the layout itself (am/pm); fmtTime additionally converts to local zone.
	if got := time.Date(2026, 6, 17, 15, 4, 0, 0, time.UTC).Format(timeLayout); got != "Jun 17 3:04pm" {
		t.Errorf("pm layout = %q, want 'Jun 17 3:04pm'", got)
	}
	if got := time.Date(2026, 6, 17, 7, 42, 0, 0, time.UTC).Format(timeLayout); got != "Jun 17 7:42am" {
		t.Errorf("am layout = %q, want 'Jun 17 7:42am'", got)
	}
}

func TestModel_Empty(t *testing.T) {
	m := New([]Period{{Label: "today", Events: nil}}, 0, pricing.NewEngine())
	if !strings.Contains(m.View(), "no sessions") {
		t.Fatalf("empty view should say no sessions:\n%s", m.View())
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeList {
		t.Fatal("enter on an empty list must not crash/drill")
	}
}

func TestHelpers(t *testing.T) {
	if money(431_850) != "$0.43" || money(3_630_860_000) != "$3,630.86" {
		t.Errorf("money: %s %s", money(431_850), money(3_630_860_000))
	}
	if humanModel("<synthetic>") != "other" || humanModel("claude-opus-4-8") != "opus-4-8" || humanModel("") != "—" {
		t.Errorf("humanModel wrong: %q %q %q", humanModel("<synthetic>"), humanModel("claude-opus-4-8"), humanModel(""))
	}
	if shortID("evt_ebc9d002053373db") != "ebc9d002" {
		t.Errorf("shortID = %q", shortID("evt_ebc9d002053373db"))
	}
	if comma(1256) != "1,256" || comma(254) != "254" {
		t.Errorf("comma: %s %s", comma(1256), comma(254))
	}
	if elapsed(22*time.Hour+12*time.Minute) != "22h12m" || elapsed(26*time.Hour) != "1d2h" {
		t.Errorf("elapsed: %s %s", elapsed(22*time.Hour+12*time.Minute), elapsed(26*time.Hour))
	}
	if trunc("abcdef", 4) != "abc…" || orDash("") != "—" || providerLabel("claude_code") != "Claude Code" {
		t.Errorf("trunc/orDash/providerLabel wrong")
	}
	if spendBar(5, 10, 4) != "██░░" {
		t.Errorf("spendBar = %q, want ██░░", spendBar(5, 10, 4))
	}
}
