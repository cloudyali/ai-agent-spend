package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// Exercises the rich rendering over realistic, mixed data — including an unpriced
// <synthetic> turn, an empty-repo session, and 1-hour cache writes — asserting the
// new UX elements: spend bars, humanized models, the header's cache-saved line,
// the receipt composition stripe + legend, and turn pluralization.
func TestView_RichData(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 15, 7, 39, 0, 0, time.UTC)
	mk := func(id, sess, repo, model string, hrs int, tk event.Tokens) event.AgentEvent {
		return priced(t, eng, id, sess, repo, model, base.Add(time.Duration(hrs)*time.Hour), tk)
	}
	evs := []event.AgentEvent{
		// session 906bf775: an unpriced <synthetic> turn (must not crash / not assert $) + a priced opus turn
		mk("evt_906bf7750001", "906bf775", "", "<synthetic>", 0, event.Tokens{Output: 200000, CacheRead: 90000000, CacheWrite: 8000000, CacheWrite1h: 6000000}),
		mk("evt_906bf7750002", "906bf775", "", "claude-opus-4-8", 1, event.Tokens{Input: 4000, Output: 60000, CacheRead: 30000000}),
		mk("evt_2e986fb700ab", "2e986fb7", "payments", "claude-opus-4-8", 2, event.Tokens{Input: 8000, Output: 120000, CacheRead: 60000000}),
		mk("evt_5a25e4ef00cd", "5a25e4ef", "", "claude-haiku-4-5", 4, event.Tokens{Input: 2000, Output: 9000, CacheRead: 5000000}),
	}
	m := New([]Period{{Label: "this month", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = nm.(Model)

	list := m.View()
	t.Logf("\n--- LIST ---\n%s", list) // visible with -v; not an assertion
	if !strings.Contains(list, "cache saved") {
		t.Errorf("list header should lead with the cache-saved arbitrage line:\n%s", list)
	}
	if !strings.Contains(list, "█") {
		t.Errorf("list rows should carry a proportion bar:\n%s", list)
	}
	if !strings.Contains(list, "payments") {
		t.Errorf("list should anchor a session on its repo:\n%s", list)
	}
	if strings.Contains(list, "906bf775") || strings.Contains(list, "<synthetic>") {
		t.Errorf("rows must not leak raw hashes or the synthetic placeholder:\n%s", list)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill the priciest session
	m = nm.(Model)
	rec := m.View()
	t.Logf("\n--- RECEIPT ---\n%s", rec)
	for _, want := range []string{"composition", "cache-read", "without cache", "top turns"} {
		if !strings.Contains(rec, want) {
			t.Errorf("receipt missing %q:\n%s", want, rec)
		}
	}
}

func TestTurnsWord(t *testing.T) {
	if turnsWord(1) != "turn" || turnsWord(0) != "turns" || turnsWord(2) != "turns" {
		t.Errorf("turnsWord pluralization wrong")
	}
}
