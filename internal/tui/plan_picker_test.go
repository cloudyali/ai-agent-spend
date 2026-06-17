package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func planChoices() []PlanChoice {
	return []PlanChoice{
		{ID: "claude-max-20x", Label: "Claude Max 20×", MonthlyUSD: 200, Current: true},
		{ID: "claude-pro", Label: "Claude Pro", MonthlyUSD: 20},
		{ID: "api"}, // no subscription
	}
}

// A real plan goes plan → date; the start date defaults to today and is adjustable.
func TestPlanPicker_PlanThenDate(t *testing.T) {
	today := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	m := newPlanPicker(planChoices(), today)
	if m.cursor != 0 {
		t.Fatalf("cursor should start on the current plan, got %d", m.cursor)
	}
	v := m.View()
	for _, want := range []string{"Claude Max 20×", "$200/mo", "current", "API / no subscription", "↵ select"} {
		if !strings.Contains(v, want) {
			t.Errorf("plan view missing %q:\n%s", want, v)
		}
	}

	// enter on the current (real) plan → date phase, defaulting to today
	pk, finished := m.step(tea.KeyMsg{Type: tea.KeyEnter})
	m = pk
	if finished || m.phase != phaseDate {
		t.Fatalf("a real plan should advance to the date step (finished=%v phase=%v)", finished, m.phase)
	}
	if !m.start.Equal(today) {
		t.Errorf("start should default to today, got %v", m.start)
	}
	if dv := m.View(); !strings.Contains(dv, "start date") || !strings.Contains(dv, "2026-06-17") {
		t.Errorf("date view should show the default start:\n%s", dv)
	}
	// nudge: -1 day → Jun 16
	pk, _ = m.step(tea.KeyMsg{Type: tea.KeyDown})
	m = pk
	if !m.start.Equal(time.Date(2026, 6, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("down should subtract a day, got %v", m.start)
	}
	// confirm
	pk, finished = m.step(tea.KeyMsg{Type: tea.KeyEnter})
	m = pk
	if !finished || !m.done || m.chosen != "claude-max-20x" {
		t.Errorf("enter on date should confirm: finished=%v done=%v chosen=%q", finished, m.done, m.chosen)
	}
}

// The no-subscription option skips the date step and confirms immediately.
func TestPlanPicker_NoSubSkipsDate(t *testing.T) {
	today := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	m := newPlanPicker(planChoices(), today)
	m.cursor = 2 // "api"
	pk, finished := m.step(tea.KeyMsg{Type: tea.KeyEnter})
	m = pk
	if !finished || !m.done || m.chosen != "api" {
		t.Errorf("api should confirm without a date step: finished=%v done=%v chosen=%q", finished, m.done, m.chosen)
	}
}

func TestPlanPicker_EscCancels(t *testing.T) {
	m := newPlanPicker(planChoices(), time.Now())
	pk, finished := m.step(tea.KeyMsg{Type: tea.KeyEsc})
	if !finished || pk.done {
		t.Errorf("esc in the plan step should cancel (finished, not done): finished=%v done=%v", finished, pk.done)
	}
	// esc in the date step goes back to the plan step, not cancel
	m2 := newPlanPicker(planChoices(), time.Now())
	m2.phase = phaseDate
	pk2, finished2 := m2.step(tea.KeyMsg{Type: tea.KeyEsc})
	if finished2 || pk2.phase != phasePlan {
		t.Errorf("esc in the date step should return to the plan step: finished=%v phase=%v", finished2, pk2.phase)
	}
}
