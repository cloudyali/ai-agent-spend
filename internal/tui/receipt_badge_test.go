package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

func TestModel_ReceiptTrailerBadge(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	ev := priced(t, eng, "evt1", "s", "payments", "claude-opus-4-8", base, event.Tokens{Input: 100_000, Output: 5_000})
	ev.GitBranch = "main"
	ev.GitSHA = "abcdef1234567890"
	ledger := ev.CostViews.APIEquivalent.Micros
	periods := []Period{{Label: "today", Events: []event.AgentEvent{ev}}}

	// Exact match: trailer == ledger → "match".
	m := New(periods, 0, eng).WithCommitTrailer(func(sha string) (int64, bool) {
		if sha == "abcdef1234567890" {
			return ledger, true
		}
		return 0, false
	})
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if r := m.View(); !strings.Contains(r, "trailer") || !strings.Contains(r, "✓") || !strings.Contains(r, "match") {
		t.Errorf("receipt should show a matching trailer badge:\n%s", r)
	}

	// Mismatch: a delta is surfaced (the reconciliation's whole point).
	m2 := New(periods, 0, eng).WithCommitTrailer(func(string) (int64, bool) { return ledger + 10_000, true })
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = nm.(Model)
	if r := m2.View(); !strings.Contains(r, "Δ") {
		t.Errorf("a trailer≠ledger mismatch should show a delta:\n%s", r)
	}

	// No SHA match (commit not in this repo) → no badge, receipt still renders.
	m3 := New(periods, 0, eng).WithCommitTrailer(func(string) (int64, bool) { return 0, false })
	nm, _ = m3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m3 = nm.(Model)
	if r := m3.View(); strings.Contains(r, "in git") {
		t.Errorf("no trailer for this commit → no badge:\n%s", r)
	}

	// No fn wired at all → no badge, no panic.
	m4 := New(periods, 0, eng)
	nm, _ = m4.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m4 = nm.(Model)
	if r := m4.View(); strings.Contains(r, "in git") {
		t.Errorf("no commit-trailer fn → no badge:\n%s", r)
	}
}
