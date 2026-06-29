package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func planChoices() []PlanChoice {
	return []PlanChoice{
		{ID: "claude-max-20x", Label: "Claude Max 20×", MonthlyUSD: 200},
		{ID: "claude-pro", Label: "Claude Pro", MonthlyUSD: 20},
		{ID: "api"}, // no subscription
	}
}

func providerChoices() []ProviderChoice {
	return []ProviderChoice{
		{Name: "claude_code", Label: "Claude Code", Current: "claude-max-20x"},
		{Name: "codex", Label: "Codex", Current: ""},
	}
}

// One provider: skip the provider step, go straight to plan → date.
func TestPlanPicker_SingleProvider(t *testing.T) {
	today := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	provs := []ProviderChoice{{Name: "claude_code", Label: "Claude Code", Current: "claude-max-20x"}}
	m := newPlanPicker(provs, planChoices(), today)
	if m.phase != phasePlan || m.provider != "claude_code" {
		t.Fatalf("single provider should start at the plan step for it, got phase=%v provider=%q", m.phase, m.provider)
	}
	if v := m.View(); !strings.Contains(v, "Set the plan for Claude Code") || !strings.Contains(v, "current") {
		t.Errorf("plan view should name the provider + mark current:\n%s", v)
	}
	pk, fin := m.step(tea.KeyMsg{Type: tea.KeyEnter}) // real plan → date step
	m = pk
	if fin || m.phase != phaseDate {
		t.Fatalf("a real plan should advance to the date step, got fin=%v phase=%v", fin, m.phase)
	}
	pk, fin = m.step(tea.KeyMsg{Type: tea.KeyEnter}) // confirm date
	m = pk
	if !fin || !m.done || m.chosen != "claude-max-20x" || m.provider != "claude_code" {
		t.Errorf("confirm: fin=%v done=%v chosen=%q provider=%q", fin, m.done, m.chosen, m.provider)
	}
}

// Multiple providers: provider step first, then per-provider plan + date.
func TestPlanPicker_MultiProvider(t *testing.T) {
	today := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC)
	m := newPlanPicker(providerChoices(), planChoices(), today)
	if m.phase != phaseProvider {
		t.Fatalf("multiple providers should start at the provider step, got %v", m.phase)
	}
	if !strings.Contains(m.View(), "which provider") {
		t.Errorf("provider view:\n%s", m.View())
	}
	pk, _ := m.step(tea.KeyMsg{Type: tea.KeyDown}) // → codex
	m = pk
	pk, _ = m.step(tea.KeyMsg{Type: tea.KeyEnter}) // choose codex → plan step
	m = pk
	if m.phase != phasePlan || m.provider != "codex" {
		t.Fatalf("after choosing codex: phase=%v provider=%q", m.phase, m.provider)
	}
	if !strings.Contains(m.View(), "Set the plan for Codex") {
		t.Errorf("plan header should name codex:\n%s", m.View())
	}
	pk, _ = m.step(tea.KeyMsg{Type: tea.KeyDown}) // cursor → claude-pro
	m = pk
	pk, fin := m.step(tea.KeyMsg{Type: tea.KeyEnter}) // real → date
	m = pk
	if fin || m.phase != phaseDate {
		t.Fatal("real plan → date")
	}
	pk, fin = m.step(tea.KeyMsg{Type: tea.KeyEnter})
	m = pk
	if !fin || !m.done || m.provider != "codex" || m.chosen != "claude-pro" {
		t.Errorf("confirm codex/claude-pro: provider=%q chosen=%q done=%v", m.provider, m.chosen, m.done)
	}
}

func TestPlanPicker_NoSubSkipsDate(t *testing.T) {
	m := newPlanPicker([]ProviderChoice{{Name: "claude_code"}}, planChoices(), time.Now())
	m.cursor = 2 // "api"
	pk, fin := m.step(tea.KeyMsg{Type: tea.KeyEnter})
	m = pk
	if !fin || !m.done || m.chosen != "api" {
		t.Errorf("api should confirm without a date step: fin=%v done=%v chosen=%q", fin, m.done, m.chosen)
	}
}

func TestPlanPicker_EscNavigation(t *testing.T) {
	// multi: esc in the plan step goes back to the provider step; esc there cancels.
	m := newPlanPicker(providerChoices(), planChoices(), time.Now())
	pk, _ := m.step(tea.KeyMsg{Type: tea.KeyEnter}) // choose provider 0 → plan
	m = pk
	pk, fin := m.step(tea.KeyMsg{Type: tea.KeyEsc})
	m = pk
	if fin || m.phase != phaseProvider {
		t.Fatalf("esc in plan (multi) → provider step, got fin=%v phase=%v", fin, m.phase)
	}
	if _, fin = m.step(tea.KeyMsg{Type: tea.KeyEsc}); !fin {
		t.Error("esc in the provider step should finish (cancel)")
	}
	// single: esc in the plan step cancels.
	s := newPlanPicker([]ProviderChoice{{Name: "claude_code"}}, planChoices(), time.Now())
	if _, fin = s.step(tea.KeyMsg{Type: tea.KeyEsc}); !fin {
		t.Error("esc in plan (single provider) should finish (cancel)")
	}
}
