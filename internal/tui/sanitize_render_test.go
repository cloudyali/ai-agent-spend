package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

// escPayload is the audit's exploit shape: an OSC window-title set + a screen
// clear, smuggled into a session-derived field via a JSON  in the log.
const escPayload = "main\x1b]0;PWNED\x07\x1b[2J"

// ctrlBytePresent flags any C0 control, DEL, or C1 control — used for pure render
// helpers that do no styling, so their output must be wholly control-free.
func ctrlBytePresent(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

// injectionPresent flags the bytes that drive an escape-sequence attack but that
// legitimate lipgloss styling never emits: BEL, an OSC introducer (ESC ]), a
// screen-clear (ESC [ 2 J), and the 8-bit C1 CSI. Safe to assert on styled output
// regardless of whether color is active.
func injectionPresent(s string) bool {
	return strings.ContainsRune(s, 0x07) || // BEL
		strings.Contains(s, "\x1b]") || // OSC
		strings.Contains(s, "\x1b[2J") || // erase display
		strings.ContainsRune(s, 0x9b) // C1 CSI
}

func TestHumanModel_Sanitizes(t *testing.T) {
	if got := humanModel("gpt\x1b]0;x\x07-5"); ctrlBytePresent(got) {
		t.Errorf("humanModel leaked a control byte: %q", got)
	}
}

func TestOrDash_Sanitizes(t *testing.T) {
	if got := orDash("repo\x1b]0;x\x07"); ctrlBytePresent(got) {
		t.Errorf("orDash leaked a control byte: %q", got)
	}
	if orDash("") != "—" {
		t.Errorf("orDash(\"\") should still be the em dash")
	}
}

func TestSessionVCSLine_SanitizesBranch(t *testing.T) {
	evs := []event.AgentEvent{{GitBranch: escPayload, GitSHA: "abcdef1234567890"}}
	if got := sessionVCSLine(evs); ctrlBytePresent(got) {
		t.Errorf("sessionVCSLine leaked a control byte: %q", got)
	}
}

func TestFileRowLine_SanitizesPath(t *testing.T) {
	fr := fileRow{path: escPayload, cost: 5}
	for _, selected := range []bool{false, true} {
		if got := fileRowLine(fr, 10, selected); injectionPresent(got) {
			t.Errorf("fileRowLine(selected=%v) leaked an injection sequence: %q", selected, got)
		}
	}
}

func TestFileView_SanitizesPath(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	m.mode = modeFile
	m.sel = sessionStat{repo: "repo"}
	m.selFile = fileRow{path: escPayload, cost: 5}
	if got := m.fileView(); injectionPresent(got) {
		t.Errorf("fileView leaked an injection sequence: %q", got)
	}
}

func TestBuildPromptViewport_SanitizesPrompt(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	m.promptOK = true
	m.promptText = "first line\n" + escPayload + "\nlast line"
	m.w, m.h = 80, 40
	m.buildPromptViewport()
	got := m.promptVP.View()
	if injectionPresent(got) {
		t.Errorf("prompt viewport leaked an injection sequence: %q", got)
	}
	// Multiline structure must survive sanitization (newlines kept).
	if !strings.Contains(got, "first line") || !strings.Contains(got, "last line") {
		t.Errorf("prompt viewport dropped legitimate lines: %q", got)
	}
}

func TestCommitsView_SanitizesTitle(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	m.commits = []Commit{{SHA: "abcdef1234567890", Title: escPayload, Turns: 1, Micros: 5}}
	if got := m.commitsView(); injectionPresent(got) {
		t.Errorf("commitsView leaked an injection sequence: %q", got)
	}
}

func TestCommitDetailView_SanitizesTitleBodyBranch(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	m.selCommit = Commit{
		SHA:    "abcdef1234567890",
		Branch: escPayload,
		Title:  escPayload,
		Body:   "first line\n" + escPayload + "\nlast line",
		Turns:  1,
		Micros: 5,
	}
	got := m.commitDetailView()
	if injectionPresent(got) {
		t.Errorf("commitDetailView leaked an injection sequence: %q", got)
	}
	// The multi-line body keeps its structure (newlines preserved by SanitizeMultiline).
	if !strings.Contains(got, "first line") || !strings.Contains(got, "last line") {
		t.Errorf("commitDetailView dropped legitimate body lines: %q", got)
	}
}

func TestPendingLine_SanitizesBranch(t *testing.T) {
	m := New([]Period{{Label: "today"}}, 0, pricing.NewEngine())
	m.pending = Pending{Branch: escPayload, Micros: 5, Turns: 1}
	if got := m.pendingLine(); injectionPresent(got) {
		t.Errorf("pendingLine leaked an injection sequence: %q", got)
	}
}

// Integration: drilling into a session whose title, repo, branch, and file are all
// poisoned must render a receipt free of injection sequences.
func TestReceiptView_NoInjection(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	ev := priced(t, eng, "evt1", "s", escPayload /*repo*/, "claude-opus-4-8", base, event.Tokens{Input: 100_000, Output: 5_000})
	ev.GitBranch = escPayload
	ev.GitSHA = "abcdef1234567890"
	ev.Files = []string{escPayload}
	periods := []Period{{Label: "today", Events: []event.AgentEvent{ev}}}

	m := New(periods, 0, eng).WithNameResolver(func(event.AgentEvent) (string, bool) {
		return escPayload, true // a poisoned session title (first prompt)
	})
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into the receipt
	m = nm.(Model)
	if got := m.View(); injectionPresent(got) {
		t.Errorf("receipt view leaked an injection sequence:\n%q", got)
	}
}
