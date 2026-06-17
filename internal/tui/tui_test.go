package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

func ev(id, sess, model string, micros int64) event.AgentEvent {
	m := event.USD(micros)
	return event.AgentEvent{EventID: id, SessionID: sess, Provider: "claude_code", Model: model, CostViews: event.CostViews{APIEquivalent: &m}}
}

// The hero interaction: arrow to a session, ↵ to its receipt, esc back, q quits.
func TestModel_NavigateDrillBack(t *testing.T) {
	periods := []Period{{Label: "today", Events: []event.AgentEvent{
		ev("e1", "3f9c", "claude-opus-4-8", 4_000_000),
		ev("e2", "3f9c", "claude-sonnet-4", 1_000_000),
		ev("e3", "a17d", "claude-opus-4-8", 6_000_000),
	}}}
	rcpt := func(evs []event.AgentEvent) string { return "RECEIPT:" + evs[0].SessionID }
	m := New(periods, 0, rcpt)

	v := m.View()
	if !strings.Contains(v, "a17d") || strings.Index(v, "a17d") > strings.Index(v, "3f9c") {
		t.Fatalf("sessions should be priciest-first (a17d $6 before 3f9c $5):\n%s", v)
	}
	if m.cursor != 0 {
		t.Fatalf("cursor starts at 0, got %d", m.cursor)
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	if m.cursor != 1 {
		t.Fatalf("down → cursor 1, got %d", m.cursor)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatal("enter should open the receipt")
	}
	if !strings.Contains(m.View(), "RECEIPT:3f9c") {
		t.Fatalf("receipt should be for the selected session (3f9c):\n%s", m.View())
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

// ←/→ scrub the period and reload the rows; you can't scrub past the ends.
func TestModel_PeriodScrub(t *testing.T) {
	periods := []Period{
		{Label: "today", Events: []event.AgentEvent{ev("e1", "s1", "claude-opus-4-8", 1_000_000)}},
		{Label: "this week", Events: []event.AgentEvent{ev("e1", "s1", "claude-opus-4-8", 1_000_000), ev("e2", "s2", "claude-sonnet-4", 2_000_000)}},
	}
	m := New(periods, 0, func(e []event.AgentEvent) string { return "" })
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
	if m.label() != "today" {
		t.Fatalf("left → today, got %s", m.label())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = nm.(Model)
	if m.label() != "today" {
		t.Fatal("left at the first period should stay put")
	}
}

// ctrl+c quits from anywhere; in receipt mode q/esc return to the list (not quit);
// k/j alias up/down; up at the top stays; window size is recorded.
func TestModel_KeysAndModes(t *testing.T) {
	periods := []Period{{Label: "today", Events: []event.AgentEvent{
		ev("e1", "3f9c", "claude-opus-4-8", 4_000_000),
		ev("e2", "a17d", "claude-sonnet-4", 2_000_000),
	}}}
	m := New(periods, 0, func(evs []event.AgentEvent) string { return "R:" + evs[0].SessionID })

	// window size recorded
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = nm.(Model)
	if m.w != 120 || m.h != 40 {
		t.Fatalf("window size not recorded: %dx%d", m.w, m.h)
	}
	// up at top stays at 0
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	if m.cursor != 0 {
		t.Fatalf("up at top should stay 0, got %d", m.cursor)
	}
	// j (alias down) → 1, k (alias up) → 0
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
	// ctrl+c from list quits
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c should issue a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("ctrl+c should quit")
	}
	// in receipt mode, q returns to the list rather than quitting
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	nm, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	m = nm.(Model)
	if m.mode != modeList {
		t.Fatal("q in receipt mode should go back to the list")
	}
	if cmd != nil {
		t.Fatal("q in receipt mode should not quit")
	}
}

func TestHelpers(t *testing.T) {
	if money(431_850) != "$0.43" || money(50_310_000) != "$50.31" {
		t.Errorf("money: %s %s", money(431_850), money(50_310_000))
	}
	if shortSession("3f9c1a2b-aaaa-bbbb") != "3f9c1a2b…" || shortSession("short") != "short" {
		t.Errorf("shortSession: %q %q", shortSession("3f9c1a2b-aaaa-bbbb"), shortSession("short"))
	}
	if modelList(nil) != "(no model)" {
		t.Errorf("empty modelList = %q", modelList(nil))
	}
	got := modelList(map[string]bool{"claude-opus-4-8": true, "claude-sonnet-4": true})
	if got != "opus-4-8, sonnet-4" {
		t.Errorf("modelList = %q, want trimmed + sorted", got)
	}
}

// Empty period: honest message, and ↵ must not crash or drill into nothing.
func TestModel_Empty(t *testing.T) {
	m := New([]Period{{Label: "today", Events: nil}}, 0, func(e []event.AgentEvent) string { return "R" })
	if !strings.Contains(m.View(), "no sessions") {
		t.Fatalf("empty view should say no sessions:\n%s", m.View())
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeList {
		t.Fatal("enter on an empty list must stay in the list (no crash)")
	}
}
