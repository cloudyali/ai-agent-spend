package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

func TestModel_TrailersEditor_TogglesAndSaves(t *testing.T) {
	eng := pricing.NewEngine()
	cur := TrailerSettings{Enabled: true, Cost: true, Precision: 2, CostName: "AI-Cost"}
	var saved TrailerSettings
	calls := 0
	m := New([]Period{{Label: "today"}}, 0, eng).
		WithTrailerSettings(cur, func(ts TrailerSettings) error { saved, calls = ts, calls+1; return nil })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)

	if !strings.Contains(m.View(), "t trailers") {
		t.Errorf("legend should advertise the trailers key:\n%s", m.View())
	}
	nm, _ = m.Update(runeKey('t'))
	m = nm.(Model)
	if m.mode != modeTrailers || !strings.Contains(m.View(), "Trailer settings") {
		t.Fatalf("t should open the trailers editor (mode=%v):\n%s", m.mode, m.View())
	}

	// move cursor to per-model (index 2) and toggle it on
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	nm, _ = m.Update(runeKey(' '))
	m = nm.(Model)

	// precision: move to last row and bump it up once (2 → 3)
	for i := 0; i < 3; i++ {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = nm.(Model)

	// save
	nm, _ = m.Update(runeKey('s'))
	m = nm.(Model)
	if calls != 1 {
		t.Fatalf("save should persist once, calls=%d", calls)
	}
	if !saved.CostModels {
		t.Errorf("CostModels should be toggled on, saved=%+v", saved)
	}
	if saved.Precision != 3 {
		t.Errorf("precision should be 3, saved=%+v", saved)
	}
	if m.mode != modeList {
		t.Errorf("after save → list, mode=%v", m.mode)
	}
}

func TestModel_TrailersEditor_EscCancels(t *testing.T) {
	calls := 0
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).
		WithTrailerSettings(TrailerSettings{Enabled: true, Cost: true, Precision: 2}, func(TrailerSettings) error { calls++; return nil })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	nm, _ = m.Update(runeKey('t'))
	m = nm.(Model)
	nm, _ = m.Update(runeKey(' ')) // toggle enabled off (uncommitted)
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeList || calls != 0 {
		t.Errorf("esc must cancel without saving: mode=%v calls=%d", m.mode, calls)
	}
}

func TestModel_TrailersKeyInertWithoutSetter(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	if strings.Contains(m.View(), "t trailers") {
		t.Errorf("no setter → no trailers key in legend:\n%s", m.View())
	}
	nm, _ = m.Update(runeKey('t'))
	m = nm.(Model)
	if m.mode != modeList {
		t.Errorf("t without a setter must be a no-op, mode=%v", m.mode)
	}
}
