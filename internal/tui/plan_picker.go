package tui

import (
	"fmt"
	"io"
	"strings"

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

type planPicker struct {
	choices []PlanChoice
	cursor  int
	chosen  string
	done    bool
}

func newPlanPicker(choices []PlanChoice) planPicker {
	m := planPicker{choices: choices}
	for i, c := range choices { // start the cursor on the current plan
		if c.Current {
			m.cursor = i
		}
	}
	return m
}

func (m planPicker) Init() tea.Cmd { return nil }

func (m planPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
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
			if len(m.choices) > 0 {
				m.chosen = m.choices[m.cursor].ID
				m.done = true
			}
			return m, tea.Quit
		case "esc", "q", "ctrl+c":
			return m, tea.Quit // cancel: done stays false
		}
	}
	return m, nil
}

func planChoiceName(c PlanChoice) string {
	if c.ID == "" || c.ID == "api" {
		return "API / no subscription"
	}
	if c.Label != "" {
		return c.Label
	}
	return c.ID
}

func (m planPicker) View() string {
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

func currentMark(c PlanChoice) string {
	if c.Current {
		return stFaint.Render("  (current)")
	}
	return ""
}

// RunPlanPicker shows the picker and returns the chosen plan id and whether the
// user confirmed (ok=false means cancelled — leave the config untouched).
func RunPlanPicker(choices []PlanChoice, out io.Writer) (string, bool, error) {
	final, err := tea.NewProgram(newPlanPicker(choices), tea.WithAltScreen(), tea.WithOutput(out)).Run()
	if err != nil {
		return "", false, err
	}
	m, _ := final.(planPicker)
	return m.chosen, m.done, nil
}
