package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// PlanChoice is one selectable plan in the catalog.
type PlanChoice struct {
	ID         string
	Label      string
	MonthlyUSD float64
}

// ProviderChoice is one provider whose plan can be set (one plan per provider).
// Current is its currently-configured plan id ("" = none); Name "" is the default.
type ProviderChoice struct {
	Name    string
	Label   string
	Current string
}

type pickPhase int

const (
	phaseProvider pickPhase = iota // choose which provider (only when >1)
	phasePlan                      // choose the plan
	phaseDate                      // choose the start date (billing-cycle anchor)
)

type planPicker struct {
	providers []ProviderChoice
	pcursor   int
	provider  string // selected provider name
	provLabel string // selected provider label (for the header)
	curPlan   string // selected provider's current plan id (marked in the plan list)

	choices []PlanChoice
	cursor  int
	phase   pickPhase
	start   time.Time
	chosen  string
	done    bool
}

func newPlanPicker(providers []ProviderChoice, choices []PlanChoice, today time.Time) planPicker {
	m := planPicker{providers: providers, choices: choices, start: dayStart(today)}
	switch len(providers) {
	case 0: // no providers in the data → set the default plan
		m.phase = phasePlan
	case 1:
		m.phase = phasePlan
		m.selectProvider(0)
	default:
		m.phase = phaseProvider
	}
	return m
}

func (m *planPicker) selectProvider(i int) {
	if i < 0 || i >= len(m.providers) {
		return
	}
	p := m.providers[i]
	m.provider, m.provLabel, m.curPlan = p.Name, p.Label, p.Current
	m.cursor = planCursorFor(m.choices, p.Current)
}

func planCursorFor(choices []PlanChoice, id string) int {
	for i, c := range choices {
		if c.ID == id {
			return i
		}
	}
	return 0
}

func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func isNoSub(id string) bool { return id == "" || id == "api" }

// step advances the picker on a key; returns (state, finished). finished+done =
// confirmed; finished without done = cancelled. Never issues tea.Quit, so it can
// be embedded in another model.
func (m planPicker) step(msg tea.Msg) (planPicker, bool) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, false
	}
	switch m.phase {
	case phaseProvider:
		switch k.String() {
		case "up", "k":
			if m.pcursor > 0 {
				m.pcursor--
			}
		case "down", "j":
			if m.pcursor < len(m.providers)-1 {
				m.pcursor++
			}
		case "enter":
			m.selectProvider(m.pcursor)
			m.phase = phasePlan
		case "esc", "q", "ctrl+c":
			return m, true // done choosing (cancel any further change)
		}
	case phasePlan:
		switch k.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.choices)-1 {
				m.cursor++
			}
		case "enter":
			if len(m.choices) == 0 {
				return m, true
			}
			m.chosen = m.choices[m.cursor].ID
			if isNoSub(m.chosen) { // no subscription → no start date needed
				m.done = true
				return m, true
			}
			m.phase = phaseDate
		case "esc", "q":
			if len(m.providers) > 1 {
				m.phase = phaseProvider // back to provider choice
				return m, false
			}
			return m, true // single provider → cancel
		case "ctrl+c":
			return m, true
		}
	case phaseDate:
		switch k.String() {
		case "up", "k":
			m.start = m.start.AddDate(0, 0, 1)
		case "down", "j":
			m.start = m.start.AddDate(0, 0, -1)
		case "right", "l":
			m.start = m.start.AddDate(0, 1, 0)
		case "left", "h":
			m.start = m.start.AddDate(0, -1, 0)
		case "enter":
			m.done = true
			return m, true
		case "esc", "q":
			m.phase = phasePlan
		case "ctrl+c":
			return m, true
		}
	}
	return m, false
}

// Update satisfies tea.Model for the standalone picker (`aispend plans`).
func (m planPicker) Init() tea.Cmd { return nil }
func (m planPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	nm, finished := m.step(msg)
	if finished {
		return nm, tea.Quit
	}
	return nm, nil
}

func planChoiceName(c PlanChoice) string {
	if isNoSub(c.ID) {
		return "API / no subscription"
	}
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

func (m planPicker) View() string {
	switch m.phase {
	case phaseProvider:
		return m.providerView()
	case phaseDate:
		return m.dateView()
	default:
		return m.planView()
	}
}

func (m planPicker) providerView() string {
	var b strings.Builder
	b.WriteString(stBold.Render("Set a plan for which provider?") + stFaint.Render("  — one plan per provider") + "\n\n")
	for i, p := range m.providers {
		cur := "no plan"
		if p.Current != "" && p.Current != "api" {
			cur = p.Current
		}
		body := fmt.Sprintf("%-16s %s", trunc(orProvider(p.Label, p.Name), 16), stFaint.Render(cur))
		if i == m.pcursor {
			b.WriteString(stSel.Render("▶ "+fmt.Sprintf("%-16s %s", trunc(orProvider(p.Label, p.Name), 16), cur)) + "\n")
		} else {
			b.WriteString("  " + body + "\n")
		}
	}
	b.WriteString("\n" + stFaint.Render("↑/↓ move · ↵ choose provider · esc done"))
	return b.String()
}

func (m planPicker) planView() string {
	var b strings.Builder
	title := "Set your subscription plan"
	if m.provLabel != "" || m.provider != "" {
		title = "Set the plan for " + orProvider(m.provLabel, m.provider)
	}
	b.WriteString(stBold.Render(title) + stFaint.Render("  — powers the amortized lens & ROI") + "\n\n")
	for i, c := range m.choices {
		price := ""
		if c.MonthlyUSD > 0 {
			price = fmt.Sprintf("$%.0f/mo", c.MonthlyUSD)
		}
		body := fmt.Sprintf("%-24s %8s", trunc(planChoiceName(c), 24), price)
		mark := ""
		if c.ID == m.curPlan {
			mark = stFaint.Render("  (current)")
		}
		if i == m.cursor {
			b.WriteString(stSel.Render("▶ "+body) + mark + "\n")
		} else {
			b.WriteString("  " + body + mark + "\n")
		}
	}
	b.WriteString("\n" + stFaint.Render("↑/↓ move · ↵ select · esc back"))
	return b.String()
}

func (m planPicker) dateView() string {
	var b strings.Builder
	b.WriteString(stBold.Render("When did this plan start?") + stFaint.Render("  — anchors the monthly billing cycle (UTC)") + "\n\n")
	name := m.chosen
	if m.cursor < len(m.choices) {
		name = planChoiceName(m.choices[m.cursor])
	}
	if m.provider != "" || m.provLabel != "" {
		b.WriteString("  provider     " + orProvider(m.provLabel, m.provider) + "\n")
	}
	b.WriteString("  plan         " + name + "\n")
	b.WriteString("  start date   " + stBold.Render(m.start.Format("Mon 2006-01-02")) + "\n")
	b.WriteString("\n" + stFaint.Render("↑/↓ ±1 day · ←/→ ±1 month · ↵ confirm · esc back"))
	return b.String()
}

func orProvider(label, name string) string {
	if label != "" {
		return label
	}
	if name != "" {
		return name
	}
	return "default"
}

// RunPlanPicker shows the picker and returns the chosen provider, plan id, start
// date, and whether the user confirmed (ok=false = cancelled).
func RunPlanPicker(providers []ProviderChoice, choices []PlanChoice, today time.Time, out io.Writer) (provider, planID string, start time.Time, ok bool, err error) {
	final, e := tea.NewProgram(newPlanPicker(providers, choices, today), tea.WithAltScreen(), tea.WithOutput(out)).Run()
	if e != nil {
		return "", "", time.Time{}, false, e
	}
	m, _ := final.(planPicker)
	return m.provider, m.chosen, m.start, m.done, nil
}
