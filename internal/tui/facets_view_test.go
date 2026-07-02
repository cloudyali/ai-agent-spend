package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// f opens the facets explorer, tab cycles the dimension, and esc returns to the list. The
// injected facetFn returns a distinct row set per dimension so the switch is observable.
func TestModel_Facets_EnterCycleRender(t *testing.T) {
	facets := func(dim string, _ []event.AgentEvent) []FacetRow {
		switch dim {
		case "tool":
			return []FacetRow{
				{Key: "Edit", Micros: 200_000, Count: 2, Pct: 66.7},
				{Key: "Bash", Micros: 100_000, Count: 1, Pct: 33.3},
			}
		case "mcp_server":
			return []FacetRow{{Key: "github", Micros: 300_000, Count: 3, Pct: 100}}
		default:
			return nil
		}
	}
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithFacets(facets)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = nm.(Model)

	if !strings.Contains(m.View(), "f facets") {
		t.Errorf("list legend should advertise the facets key:\n%s", m.View())
	}

	nm, _ = m.Update(runeKey('f'))
	m = nm.(Model)
	if m.mode != modeFacets {
		t.Fatalf("f should open the facets explorer, mode=%v", m.mode)
	}
	if v := m.View(); !strings.Contains(v, "by tool") || !strings.Contains(v, "Edit") {
		t.Fatalf("facets view should lead with the tool breakdown:\n%s", v)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // cycle tool → mcp_server
	m = nm.(Model)
	if m.facetDim != "mcp_server" {
		t.Errorf("tab should advance the dimension, got %q", m.facetDim)
	}
	if v := m.View(); !strings.Contains(v, "MCP server") || !strings.Contains(v, "github") {
		t.Errorf("facets view should now show the MCP breakdown:\n%s", v)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeList {
		t.Errorf("esc should return to the list, mode=%v", m.mode)
	}
}

func TestNextFacetDim(t *testing.T) {
	if got := nextFacetDim("tool", 1); got != "mcp_server" {
		t.Errorf("next after tool = %q, want mcp_server", got)
	}
	last := facetDims[len(facetDims)-1]
	if got := nextFacetDim("tool", -1); got != last {
		t.Errorf("prev of first should wrap to last (%q), got %q", last, got)
	}
	if got := nextFacetDim(last, 1); got != facetDims[0] {
		t.Errorf("next of last should wrap to first (%q), got %q", facetDims[0], got)
	}
}
