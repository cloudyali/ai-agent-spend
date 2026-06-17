// Package tui is the interactive explorer (`aispend tui`): a navigable session
// list that drills to the receipt on ↵ — the one interaction that earns going
// interactive (08-cli-tui-concept.md). It is deliberately isolated from the cli
// package so it imports only Bubble Tea + lipgloss + the data model — never the
// store (and thus never sqlite) — which keeps the model pure, unit-testable
// without a TTY, and keeps the binary's net-free import graph intact.
//
// The CLI pre-filters events into Period slices and injects the receipt renderer,
// so this package needs neither period parsing nor the receipt internals.
package tui

import (
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

// Period is one selectable window: a label plus the events that fall in it.
type Period struct {
	Label  string
	Events []event.AgentEvent
}

type mode int

const (
	modeList mode = iota
	modeReceipt
)

type sessionStat struct {
	id     string
	micros int64
	turns  int
	models map[string]bool
	evs    []event.AgentEvent
}

// Model is the Bubble Tea model. Update/View are pure over messages, so the whole
// interaction is testable by feeding tea.KeyMsg values — no terminal required.
type Model struct {
	periods []Period
	pIdx    int
	receipt func([]event.AgentEvent) string
	rows    []sessionStat
	cursor  int
	mode    mode
	body    string
	w, h    int
}

var (
	headerStyle   = lipgloss.NewStyle().Bold(true)
	selectedStyle = lipgloss.NewStyle().Reverse(true)
	faintStyle    = lipgloss.NewStyle().Faint(true)
)

// New builds the model over pre-filtered periods, starting on startIdx, with a
// receipt renderer injected by the caller.
func New(periods []Period, startIdx int, receipt func([]event.AgentEvent) string) Model {
	if startIdx < 0 || startIdx >= len(periods) {
		startIdx = 0
	}
	m := Model{periods: periods, pIdx: startIdx, receipt: receipt, mode: modeList}
	m.rows = groupSessions(m.currentEvents())
	return m
}

func (m Model) currentEvents() []event.AgentEvent {
	if m.pIdx < 0 || m.pIdx >= len(m.periods) {
		return nil
	}
	return m.periods[m.pIdx].Events
}

func (m Model) label() string {
	if m.pIdx < 0 || m.pIdx >= len(m.periods) {
		return ""
	}
	return m.periods[m.pIdx].Label
}

func (m Model) Init() tea.Cmd { return nil }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		if m.mode == modeReceipt {
			switch msg.String() {
			case "esc", "q", "left", "h", "backspace":
				m.mode = modeList
			}
			return m, nil
		}
		switch msg.String() {
		case "q":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "left", "h":
			if m.pIdx > 0 {
				m.pIdx--
				m.rows = groupSessions(m.currentEvents())
				m.cursor = 0
			}
		case "right", "l":
			if m.pIdx < len(m.periods)-1 {
				m.pIdx++
				m.rows = groupSessions(m.currentEvents())
				m.cursor = 0
			}
		case "enter":
			if len(m.rows) > 0 && m.receipt != nil {
				m.body = m.receipt(m.rows[m.cursor].evs)
				m.mode = modeReceipt
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.mode == modeReceipt {
		return m.body + "\n" + faintStyle.Render("  esc back · q quit")
	}
	var b strings.Builder
	var total int64
	for _, r := range m.rows {
		total += r.micros
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("aispend · %s · %s · %d sessions", m.label(), money(total), len(m.rows))))
	b.WriteByte('\n')
	b.WriteString(faintStyle.Render("←/→ period · ↑/↓ move · ↵ open receipt · q quit"))
	b.WriteString("\n\n")
	if len(m.rows) == 0 {
		b.WriteString("  no sessions in " + m.label() + "\n")
		return b.String()
	}
	for i, r := range m.rows {
		line := fmt.Sprintf("  %9s  %-10s %d turns · %s", money(r.micros), shortSession(r.id), r.turns, modelList(r.models))
		if i == m.cursor {
			line = selectedStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// Run starts the interactive program on the alt screen. Kept here so the cli
// command code need not import Bubble Tea directly.
func Run(periods []Period, startIdx int, receipt func([]event.AgentEvent) string, out io.Writer) error {
	p := tea.NewProgram(New(periods, startIdx, receipt), tea.WithAltScreen(), tea.WithOutput(out))
	_, err := p.Run()
	return err
}

// groupSessions rolls events up into per-session rows, priciest first. Sessionless
// turns aren't addressable as a session, so they're excluded.
func groupSessions(events []event.AgentEvent) []sessionStat {
	byID := map[string]*sessionStat{}
	var order []string
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		g := byID[e.SessionID]
		if g == nil {
			g = &sessionStat{id: e.SessionID, models: map[string]bool{}}
			byID[e.SessionID] = g
			order = append(order, e.SessionID)
		}
		g.turns++
		g.evs = append(g.evs, e)
		if m := e.CostViews.APIEquivalent; m != nil {
			g.micros += m.Micros
		}
		if e.Model != "" {
			g.models[e.Model] = true
		}
	}
	out := make([]sessionStat, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].micros > out[j].micros })
	return out
}

func money(micros int64) string { return fmt.Sprintf("$%.2f", float64(micros)/1e6) }

func shortSession(id string) string {
	if r := []rune(id); len(r) > 8 {
		return string(r[:8]) + "…"
	}
	return id
}

func modelList(set map[string]bool) string {
	if len(set) == 0 {
		return "(no model)"
	}
	xs := make([]string, 0, len(set))
	for s := range set {
		xs = append(xs, strings.TrimPrefix(s, "claude-"))
	}
	sort.Strings(xs)
	return strings.Join(xs, ", ")
}
