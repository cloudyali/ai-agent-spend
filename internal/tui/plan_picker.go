package tui

import (
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// PlanChoice is one selectable plan in the picker. The CLI builds these from the
// seeded plans table + the currently-configured plan, so this package needs no
// config access.
type PlanChoice struct {
	ID         string
	Label      string
	MonthlyUSD float64
	Current    bool
}

type pickPhase int

const (
	phasePlan pickPhase = iota // choosing the plan
	phaseDate                  // choosing the start date (billing-cycle anchor)
)

type planPicker struct {
	choices []PlanChoice
	cursor  int
	phase   pickPhase
	start   time.Time // selected start date; anchors the monthly cycle for amortization
	chosen  string
	done    bool
}

func newPlanPicker(choices []PlanChoice, today time.Time) planPicker {
	m := planPicker{choices: choices, start: dayStart(today)}
	for i, c := range choices { // start the cursor on the current plan
		if c.Current {
			m.cursor = i
		}
	}
	return m
}

// dayStart truncates to UTC midnight. AgentSpend is UTC end-to-end (billing data,
// event timestamps, and plan start dates are all UTC), so the plan-start anchor is
// the UTC calendar date — timezone-independent and consistent with the ledger.
func dayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func isNoSub(id string) bool { return id == "" || id == "api" }

// step advances the picker on a key, returning the new state and whether the user
// finished (done=true means confirmed; done=false on a finished step means
// cancelled). It never issues tea.Quit, so it can be embedded in another model.
func (m planPicker) step(msg tea.Msg) (planPicker, bool) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, false
	}
	switch m.phase {
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
			m.phase = phaseDate // proceed to the start-date step
			return m, false
		case "esc", "q", "ctrl+c":
			return m, true // cancel
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
			m.phase = phasePlan // back to plan choice
			return m, false
		case "ctrl+c":
			return m, true // cancel
		}
	}
	return m, false
}

// Update satisfies tea.Model for the standalone picker (`aispend plans`): it quits
// the program once the user finishes.
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
	if m.phase == phaseDate {
		return m.dateView()
	}
	var b strings.Builder
	b.WriteString(stBold.Render("Set your subscription plan") + stFaint.Render("  — powers the amortized lens & ROI") + "\n\n")
	for i, c := range m.choices {
		price := ""
		if c.MonthlyUSD > 0 {
			price = fmt.Sprintf("$%.0f/mo", c.MonthlyUSD)
		}
		body := fmt.Sprintf("%-24s %8s", trunc(planChoiceName(c), 24), price)
		if i == m.cursor {
			b.WriteString(stSel.Render("▶ "+body) + currentMark(c) + "\n")
		} else {
			b.WriteString("  " + body + currentMark(c) + "\n")
		}
	}
	b.WriteString("\n" + stFaint.Render("↑/↓ move · ↵ select · esc cancel"))
	return b.String()
}

func (m planPicker) dateView() string {
	var b strings.Builder
	b.WriteString(stBold.Render("When did this plan start?") + stFaint.Render("  — anchors the monthly billing cycle") + "\n\n")
	name := m.chosen
	if m.cursor < len(m.choices) {
		name = planChoiceName(m.choices[m.cursor])
	}
	b.WriteString("  plan         " + name + "\n")
	b.WriteString("  start date   " + stBold.Render(m.start.Format("Mon 2006-01-02")) + "\n")
	b.WriteString("\n" + stFaint.Render("↑/↓ ±1 day · ←/→ ±1 month · ↵ confirm · esc back"))
	return b.String()
}

func currentMark(c PlanChoice) string {
	if c.Current {
		return stFaint.Render("  (current)")
	}
	return ""
}

// RunPlanPicker shows the picker and returns the chosen plan id, its start date,
// and whether the user confirmed (ok=false = cancelled, leave config untouched).
func RunPlanPicker(choices []PlanChoice, today time.Time, out io.Writer) (string, time.Time, bool, error) {
	final, err := tea.NewProgram(newPlanPicker(choices, today), tea.WithAltScreen(), tea.WithOutput(out)).Run()
	if err != nil {
		return "", time.Time{}, false, err
	}
	m, _ := final.(planPicker)
	return m.chosen, m.start, m.done, nil
}
