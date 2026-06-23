package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/budget"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

func runeKey(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestModel_BudgetEditor_SetsAndShowsGauge(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	var gotMicros int64
	calls := 0
	m := New([]Period{{Label: "today"}}, 0, eng).WithNow(now).
		WithBudgetSetter(func(micros int64) (budget.Pace, bool) {
			gotMicros, calls = micros, calls+1
			start, end := budget.MonthBounds(now)
			return budget.ComputePace(micros, micros/2, start, now, end), true
		})
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)

	if !strings.Contains(m.View(), "b budget") {
		t.Errorf("list legend should advertise the budget key:\n%s", m.View())
	}
	nm, _ = m.Update(runeKey('b'))
	m = nm.(Model)
	if m.mode != modeBudget || !strings.Contains(m.View(), "monthly budget") {
		t.Fatalf("b should open the budget editor (mode=%v):\n%s", m.mode, m.View())
	}
	for _, r := range "500" {
		nm, _ = m.Update(runeKey(r))
		m = nm.(Model)
	}
	if !strings.Contains(m.View(), "500") {
		t.Errorf("editor should echo the typed amount:\n%s", m.View())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if calls != 1 || gotMicros != 500_000_000 {
		t.Fatalf("setter got micros=%d calls=%d, want 500000000/1", gotMicros, calls)
	}
	if m.mode != modeList {
		t.Errorf("after save should return to the list, mode=%v", m.mode)
	}
	if v := m.View(); !strings.Contains(v, "budget") || !strings.Contains(v, "$500") {
		t.Errorf("list should now show the budget gauge:\n%s", v)
	}
}

func TestModel_BudgetEditor_EscAndInvalid(t *testing.T) {
	calls := 0
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).
		WithBudgetSetter(func(int64) (budget.Pace, bool) { calls++; return budget.Pace{}, true })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)

	// esc cancels without persisting
	nm, _ = m.Update(runeKey('b'))
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeList || calls != 0 {
		t.Errorf("esc should cancel: mode=%v calls=%d", m.mode, calls)
	}
	// an empty amount must not persist (stays in the editor)
	nm, _ = m.Update(runeKey('b'))
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeBudget || calls != 0 {
		t.Errorf("empty amount must not persist: mode=%v calls=%d", m.mode, calls)
	}
}

// A configured budget must be visible — and editable — when the editor opens.
// Pressing b prefills the field with the current ceiling (a fractional value here,
// to prove the micros→dollars conversion) instead of a blank input.
func TestModel_BudgetEditor_PrefillsExistingBudget(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	start, end := budget.MonthBounds(now)
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithNow(now).
		WithBudget(func() (budget.Pace, bool) {
			return budget.ComputePace(250_500_000, 100_000_000, start, now, end), true // $250.50 ceiling
		}).
		WithBudgetSetter(func(int64) (budget.Pace, bool) { return budget.Pace{}, true })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)

	nm, _ = m.Update(runeKey('b'))
	m = nm.(Model)
	if m.mode != modeBudget {
		t.Fatalf("b should open the budget editor, mode=%v", m.mode)
	}
	if m.budgetBuf != "250.5" {
		t.Errorf("editor should prefill the existing ceiling: budgetBuf=%q, want %q", m.budgetBuf, "250.5")
	}
	if v := m.View(); !strings.Contains(v, "$250.5") {
		t.Errorf("editor should display the existing budget:\n%s", v)
	}
	// The prefill stays editable: backspace trims the seeded value.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = nm.(Model)
	if m.budgetBuf != "250." {
		t.Errorf("prefilled value should be editable: budgetBuf=%q, want %q", m.budgetBuf, "250.")
	}
}

func TestModel_BudgetKeyIgnoredWithoutSetter(t *testing.T) {
	// Without WithBudgetSetter, 'b' is inert (no editor, no legend entry).
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	if strings.Contains(m.View(), "b budget") {
		t.Errorf("no setter → no budget key in the legend:\n%s", m.View())
	}
	nm, _ = m.Update(runeKey('b'))
	m = nm.(Model)
	if m.mode != modeList {
		t.Errorf("b without a setter must be a no-op, mode=%v", m.mode)
	}
}
