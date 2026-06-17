package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPlanPicker(t *testing.T) {
	choices := []PlanChoice{
		{ID: "claude-max-20x", Label: "Claude Max 20×", MonthlyUSD: 200, Current: true},
		{ID: "claude-pro", Label: "Claude Pro", MonthlyUSD: 20},
		{ID: "api"}, // no subscription
	}
	m := newPlanPicker(choices)
	if m.cursor != 0 {
		t.Fatalf("cursor should start on the current plan, got %d", m.cursor)
	}
	v := m.View()
	for _, want := range []string{"Claude Max 20×", "$200/mo", "current", "API / no subscription", "↵ select"} {
		if !strings.Contains(v, want) {
			t.Errorf("picker view missing %q:\n%s", want, v)
		}
	}

	// down to the api option, enter → confirmed choice "api"
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(planPicker)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(planPicker)
	nm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(planPicker)
	if !m.done || m.chosen != "api" {
		t.Errorf("enter should choose api, got chosen=%q done=%v", m.chosen, m.done)
	}
	if cmd == nil {
		t.Error("enter should quit")
	}

	// esc cancels without choosing
	c := newPlanPicker(choices)
	nm, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	c = nm.(planPicker)
	if c.done {
		t.Error("esc should cancel (done=false)")
	}
}
