package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

func TestModel_CommitsWindowing(t *testing.T) {
	commits := make([]Commit, 30)
	for i := range commits {
		commits[i] = Commit{SHA: fmt.Sprintf("%02d00000000aaaa", i), Micros: int64(i+1) * 1000, Turns: i}
	}
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithCommits(func() []Commit { return commits })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12}) // short terminal
	m = nm.(Model)
	nm, _ = m.Update(runeKey('c'))
	m = nm.(Model)

	v := m.View()
	if !strings.Contains(v, "commits") {
		t.Errorf("header must stay visible:\n%s", v)
	}
	if !strings.Contains(v, "more") {
		t.Errorf("a long list in a short terminal must show a 'more' indicator:\n%s", v)
	}
	if strings.Contains(v, shortSHA(commits[29].SHA)) {
		t.Errorf("the last commit should be windowed out at the top:\n%s", v)
	}
	// Scroll to the bottom: the last commit comes into view (cursor stays on screen).
	for i := 0; i < 29; i++ {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	if v := m.View(); !strings.Contains(v, shortSHA(commits[29].SHA)) {
		t.Errorf("the last commit must be visible after scrolling down:\n%s", v)
	}
}

func TestModel_CommitsView(t *testing.T) {
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC // pin so the rendered timestamp is deterministic
	eng := pricing.NewEngine()
	commits := []Commit{
		{SHA: "abcdef1234567890", Branch: "main", Micros: 1234000, Turns: 34, When: time.Date(2026, 6, 21, 19, 13, 0, 0, time.UTC), Title: "feat: add the thing", Body: "details here", TrailerMicros: 1234000, HasTrailer: true},
		{SHA: "0011223344556677", Branch: "main", Micros: 500000, Turns: 5}, // git-independent: no title/trailer
	}
	m := New([]Period{{Label: "today"}}, 0, eng).WithCommits(func() []Commit { return commits })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = nm.(Model)

	if !strings.Contains(m.View(), "c commits") {
		t.Errorf("legend should advertise the commits key:\n%s", m.View())
	}
	nm, _ = m.Update(runeKey('c'))
	m = nm.(Model)
	if m.mode != modeCommits {
		t.Fatalf("c should open the commit view, mode=%v", m.mode)
	}
	list := m.View()
	for _, want := range []string{"commits", "abcdef1234", "$1.23", "34 turns", "Jun 21 19:13", "feat: add the thing", "✓ trailer", "0011223344", "$0.50"} {
		if !strings.Contains(list, want) {
			t.Errorf("commits list missing %q:\n%s", want, list)
		}
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeCommitDetail {
		t.Fatalf("enter should open the commit detail, mode=%v", m.mode)
	}
	detail := m.View()
	for _, want := range []string{"abcdef1234", "feat: add the thing", "details here", "Jun 21 19:13", "ledger", "$1.23", "trailer", "match"} {
		if !strings.Contains(detail, want) {
			t.Errorf("commit detail missing %q:\n%s", want, detail)
		}
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeCommits {
		t.Errorf("esc from detail → commits list, mode=%v", m.mode)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeList {
		t.Errorf("esc from commits → list, mode=%v", m.mode)
	}
}

func TestModel_CommitsGitIndependent(t *testing.T) {
	// A commit with no git enrichment (no Title/Body/Trailer) must still render from
	// the ledger alone — SHA + cost + turns — and say so honestly.
	commits := []Commit{{SHA: "0011223344556677", Branch: "dev", Micros: 500000, Turns: 5}}
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine()).WithCommits(func() []Commit { return commits })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = nm.(Model)
	nm, _ = m.Update(runeKey('c'))
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	d := m.View()
	for _, want := range []string{"$0.50", "5 turns", "not stamped", "message unavailable"} {
		if !strings.Contains(d, want) {
			t.Errorf("git-independent detail missing %q:\n%s", want, d)
		}
	}
}

func TestModel_CommitsKeyInertWithoutFn(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = nm.(Model)
	if strings.Contains(m.View(), "c commits") {
		t.Errorf("no commits fn → no key in legend:\n%s", m.View())
	}
	nm, _ = m.Update(runeKey('c'))
	m = nm.(Model)
	if m.mode != modeList {
		t.Errorf("c without a fn must be a no-op, mode=%v", m.mode)
	}
}
