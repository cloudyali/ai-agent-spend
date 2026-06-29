package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

func TestModel_PendingCommitLine(t *testing.T) {
	eng := pricing.NewEngine()
	m := New([]Period{{Label: "today"}}, 0, eng).
		WithPending(func() (Pending, bool) {
			return Pending{Branch: "main", Micros: 56_880_000, Turns: 517}, true
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	v := m.View()
	for _, want := range []string{"pending commit", "main", "$56.88", "517 turns"} {
		if !strings.Contains(v, want) {
			t.Errorf("list header should show the pending-commit preview (%q):\n%s", want, v)
		}
	}
}

func TestModel_PendingLineAbsentWithoutFn(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	if strings.Contains(m.View(), "pending commit") {
		t.Errorf("no pending fn → no pending line:\n%s", m.View())
	}
}
