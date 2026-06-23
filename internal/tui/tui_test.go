package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cloudyali/ai-agent-spend/internal/budget"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/quota"
)

func priced(t *testing.T, eng *pricing.Engine, id, sess, repo, model string, ts time.Time, tk event.Tokens) event.AgentEvent {
	t.Helper()
	e := event.AgentEvent{EventID: id, SessionID: sess, Repo: repo, Provider: "claude_code", Model: model, Tokens: tk, TSStart: ts, TSEnd: ts.Add(time.Minute)}
	if err := eng.Price(&e, pricing.Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	return e
}

// The hero flow: rows are anchored on repo/when (not opaque hashes), priciest
// first; ↵ drills to a receipt with composition + arbitrage; esc back; q quits.
func TestModel_NavigateDrillBack(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	periods := []Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "evt_aaaa1111bbbb", "3f9c", "payments", "claude-opus-4-8", base, event.Tokens{Input: 100_000, Output: 50_000, CacheRead: 5_000_000}),
		priced(t, eng, "evt_bbbb2222cccc", "3f9c", "payments", "claude-sonnet-4", base.Add(time.Hour), event.Tokens{Input: 20_000}),
		priced(t, eng, "evt_cccc3333dddd", "a17d", "web", "claude-opus-4-8", base.Add(2*time.Hour), event.Tokens{Input: 50_000, CacheRead: 20_000_000}),
	}}}
	m := New(periods, 0, eng)

	v := m.View()
	if !strings.Contains(v, "today") || !strings.Contains(v, "web") || !strings.Contains(v, "payments") {
		t.Fatalf("list should show period + repos:\n%s", v)
	}
	if strings.Index(v, "web") > strings.Index(v, "payments") {
		t.Fatalf("sessions should be priciest-first (web/a17d $10 before payments/3f9c $4):\n%s", v)
	}
	// rows must NOT carry the opaque session hash anymore
	if strings.Contains(v, "a17d") || strings.Contains(v, "3f9c") {
		t.Errorf("list rows should not show raw session hashes:\n%s", v)
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model) // cursor → 1 (payments/3f9c)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatal("enter should open the receipt")
	}
	r := m.View()
	for _, want := range []string{"payments", "total", "composition", "cache-read", "without cache", "top turns", "WHEN"} {
		if !strings.Contains(r, want) {
			t.Errorf("receipt missing %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "aaaa1111") {
		t.Errorf("receipt should not surface the raw event id:\n%s", r)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeList {
		t.Fatal("esc should return to the list")
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatal("q should issue a command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q should quit")
	}
}

func TestModel_PeriodScrub(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	periods := []Period{
		{Label: "today", Events: []event.AgentEvent{priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})}},
		{Label: "this week", Events: []event.AgentEvent{
			priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
			priced(t, eng, "e2", "s2", "r", "claude-sonnet-4", ts, event.Tokens{Input: 2_000_000}),
		}},
	}
	m := New(periods, 0, eng)
	if m.label() != "today" || len(m.rows) != 1 {
		t.Fatalf("start today/1 row, got %s/%d", m.label(), len(m.rows))
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRight})
	m = nm.(Model)
	if m.label() != "this week" || len(m.rows) != 2 {
		t.Fatalf("right → this week/2 rows, got %s/%d", m.label(), len(m.rows))
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft}) // can't go past the first
	m = nm.(Model)
	if m.label() != "today" {
		t.Fatalf("left should clamp at today, got %s", m.label())
	}
}

func TestModel_KeysAndModes(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: 4_000_000}),
		priced(t, eng, "e2", "s2", "r", "claude-sonnet-4", ts, event.Tokens{Input: 2_000_000}),
	}}}, 0, eng)

	nm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = nm.(Model)
	if m.w != 120 || m.h != 40 {
		t.Fatalf("window size not recorded: %dx%d", m.w, m.h)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	if m.cursor != 0 {
		t.Fatalf("up at top stays 0, got %d", m.cursor)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	m = nm.(Model)
	if m.cursor != 1 {
		t.Fatalf("j should move down, got %d", m.cursor)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	m = nm.(Model)
	if m.cursor != 0 {
		t.Fatalf("k should move up, got %d", m.cursor)
	}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil || func() bool { _, ok := cmd().(tea.QuitMsg); return !ok }() {
		t.Fatal("ctrl+c should quit")
	}
	// q in receipt mode quits (it is never "back"); esc is what goes back.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt
	m = nm.(Model)
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil || func() bool { _, ok := cmd().(tea.QuitMsg); return !ok }() {
		t.Fatal("q in receipt mode should quit, not go back")
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if nm.(Model).mode != modeList {
		t.Fatal("esc in receipt mode should go back to the list")
	}
}

// A long list clamps to the viewport with a "more" indicator, and the window
// follows the cursor to the bottom.
func TestModel_ScrollWindow(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 17, 1, 0, 0, 0, time.UTC)
	var evs []event.AgentEvent
	for i := 0; i < 20; i++ {
		evs = append(evs, priced(t, eng, fmt.Sprintf("e%02d", i), fmt.Sprintf("s%02d", i), "r",
			"claude-opus-4-8", base.Add(time.Duration(i)*time.Minute), event.Tokens{Input: int64((20 - i) * 100_000)}))
	}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = nm.(Model)
	if s, e := m.windowRange(len(m.rows)); e-s >= len(m.rows) {
		t.Fatalf("expected a clamped window, got %d of %d", e-s, len(m.rows))
	}
	if !strings.Contains(m.View(), "more") {
		t.Errorf("a clamped list should show a 'more' indicator:\n%s", m.View())
	}
	for i := 0; i < 19; i++ {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	if _, e := m.windowRange(len(m.rows)); e != len(m.rows) {
		t.Errorf("cursor at the bottom should reveal the last row, end=%d n=%d", e, len(m.rows))
	}
}

// `v` cycles the cost view; the header names it and totals/ranking follow it.
func TestModel_ViewSwitch(t *testing.T) {
	api := event.USD(5_000_000)
	rep := event.USD(4_000_000)
	e := event.AgentEvent{EventID: "e1", SessionID: "s1", Provider: "claude_code", Model: "claude-opus-4-8",
		TSStart:   time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC),
		CostViews: event.CostViews{APIEquivalent: &api, Reported: &rep}}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, pricing.NewEngine())

	if m.view() != "api_equivalent" {
		t.Fatalf("default view = %q", m.view())
	}
	if v := m.View(); !strings.Contains(v, "api-equivalent") || !strings.Contains(v, "$5.00") {
		t.Errorf("api-equivalent view should show $5.00 and name itself:\n%s", v)
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = nm.(Model)
	if m.view() != "reported" {
		t.Fatalf("after v, view = %q, want reported", m.view())
	}
	if v := m.View(); !strings.Contains(v, "reported") || !strings.Contains(v, "$4.00") {
		t.Errorf("reported view should show $4.00:\n%s", v)
	}
}

func TestListColumnHeader(t *testing.T) {
	eng := pricing.NewEngine()
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "repo", "claude-opus-4-8", time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC), event.Tokens{Input: 1_000_000}),
	}}}, 0, eng)
	v := m.View()
	for _, h := range []string{"COST", "SHARE", "WHEN", "PROJECT", "TURNS", "MODEL"} {
		if !strings.Contains(v, h) {
			t.Errorf("list missing column header %q:\n%s", h, v)
		}
	}
}

func TestTimeLayout(t *testing.T) {
	// the layout itself (am/pm); fmtTime additionally converts to local zone.
	if got := time.Date(2026, 6, 17, 15, 4, 0, 0, time.UTC).Format(timeLayout); got != "Jun 17 3:04pm" {
		t.Errorf("pm layout = %q, want 'Jun 17 3:04pm'", got)
	}
	if got := time.Date(2026, 6, 17, 7, 42, 0, 0, time.UTC).Format(timeLayout); got != "Jun 17 7:42am" {
		t.Errorf("am layout = %q, want 'Jun 17 7:42am'", got)
	}
	// Visual times render in the GIVEN (local) zone, not forced to UTC: 02:00 in +5:30
	// stays 2:00am rendered in that zone, and converts only when rendered elsewhere
	// (the backend keeps the UTC instant — fmtTimeIn just chooses the display zone).
	ist := time.FixedZone("IST", 5*3600+30*60)
	in := time.Date(2026, 6, 17, 2, 0, 0, 0, ist)
	if got := fmtTimeIn(in, ist); got != "Jun 17 2:00am" {
		t.Errorf("fmtTimeIn(local) should render the wall clock of that zone, got %q", got)
	}
	if got := fmtTimeIn(in, time.UTC); got != "Jun 16 8:30pm" {
		t.Errorf("fmtTimeIn(UTC) should convert to UTC, got %q", got)
	}
}

// When only one cost lens has data, `v` is a no-op and its hint is hidden — no
// switching into an all-$0 screen.
func TestModel_SingleViewNoSwitch(t *testing.T) {
	eng := pricing.NewEngine()
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "repo", "claude-opus-4-8", time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC), event.Tokens{Input: 1_000_000}),
	}}}, 0, eng)
	if len(m.avail) != 1 || m.view() != "api_equivalent" {
		t.Fatalf("only api-equivalent should be available, got %v", m.avail)
	}
	if strings.Contains(m.View(), "v view") {
		t.Errorf("the v hint should hide when there's only one populated view:\n%s", m.View())
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
	m = nm.(Model)
	if m.view() != "api_equivalent" {
		t.Errorf("v should be a no-op with a single view, got %q", m.view())
	}
}

// A session with no cost in the active lens shows "—", never a phantom $0.00.
func TestModel_UnpricedSessionShowsDash(t *testing.T) {
	eng := pricing.NewEngine()
	good := priced(t, eng, "e1", "s1", "payments", "claude-opus-4-8", time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC), event.Tokens{Input: 1_000_000})
	bad := event.AgentEvent{EventID: "e2", SessionID: "s2", Repo: "infra", Provider: "claude_code", Model: "mystery-model", TSStart: time.Date(2026, 6, 17, 10, 0, 0, 0, time.UTC)}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{good, bad}}}, 0, eng)
	v := m.View()
	if !strings.Contains(v, "—") {
		t.Errorf("the unpriced session should show — for cost:\n%s", v)
	}
	if strings.Contains(v, "$0.00") {
		t.Errorf("must never assert a phantom $0.00:\n%s", v)
	}
}

func TestAvailableViews_Amortized(t *testing.T) {
	api := event.USD(5_000_000)
	evs := []event.AgentEvent{{CostViews: event.CostViews{APIEquivalent: &api}}}
	if got := availableViews(evs, false); len(got) != 1 || got[0] != "api_equivalent" {
		t.Errorf("no plan should offer only api-equivalent: %v", got)
	}
	if got := availableViews(evs, true); len(got) != 2 || got[1] != "amortized" {
		t.Errorf("a plan should append the amortized lens: %v", got)
	}
}

// The amortized lens: the period's prorated plan fee allocated across sessions by
// api-equivalent share, with a plan-vs-api ROI headline.
func TestModel_AmortizedLens(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	evs := []event.AgentEvent{
		priced(t, eng, "e1", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 3_000_000}), // api $15
		priced(t, eng, "e2", "s2", "web", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),      // api $5
	}
	per := Period{Label: "this month", Events: evs, Amortized: map[string]int64{"claude_code": 200_000_000}, HasPlan: true} // $200 plan
	m := New([]Period{per}, 0, eng)

	found := false
	for _, v := range m.avail {
		if v == amortizedView {
			found = true
		}
	}
	if !found {
		t.Fatalf("amortized should be available with a plan: %v", m.avail)
	}
	for m.view() != amortizedView { // cycle to it
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
		m = nm.(Model)
	}
	v := m.View()
	if !strings.Contains(v, "amortized") || !strings.Contains(v, "ROI") || !strings.Contains(v, "$200.00") {
		t.Errorf("amortized header should show plan total + ROI:\n%s", v)
	}
	if len(m.rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(m.rows))
	}
	if m.rows[0].micros+m.rows[1].micros != 200_000_000 {
		t.Errorf("allocated rows must sum to the plan total, got %d", m.rows[0].micros+m.rows[1].micros)
	}
	if m.rows[0].micros != 150_000_000 { // 75% api share → $150 of $200
		t.Errorf("top session (75%% share) should get $150.00, got %d", m.rows[0].micros)
	}
}

// The in-explorer plan picker: `p` opens it, choosing a plan + start date calls
// setPlan and lands live on the amortized lens.
func TestModel_InTUIPlanPicker(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	base := []Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
	}}}
	withPlan := []Period{{Label: "today", Events: base[0].Events, Amortized: map[string]int64{"claude_code": 100_000_000}, HasPlan: true}}
	var gotProvider, gotID string
	var gotStart time.Time
	setPlan := func(provider, id string, start time.Time) []Period {
		gotProvider, gotID, gotStart = provider, id, start
		return withPlan
	}
	provs := []ProviderChoice{{Name: "claude_code", Label: "Claude Code"}}
	m := New(base, 0, eng).WithPlanPicker(provs, planChoices(), ts, setPlan)
	for _, v := range m.avail {
		if v == amortizedView {
			t.Fatal("no plan yet → amortized must not be available")
		}
	}
	if !strings.Contains(m.View(), "p set plan") {
		t.Errorf("list hint should advertise the picker:\n%s", m.View())
	}

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = nm.(Model)
	if m.mode != modePlan || !strings.Contains(m.View(), "Set the plan for Claude Code") {
		t.Fatalf("p should open the picker:\n%s", m.View())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // pick the current plan → date step
	m = nm.(Model)
	if m.mode != modePlan {
		t.Fatal("a real plan should advance to the date step (still in picker)")
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm the date
	m = nm.(Model)
	wantStart := time.Date(2026, 6, 17, 0, 0, 0, 0, time.UTC) // start date defaults to today (midnight, UTC)
	if gotProvider != "claude_code" || gotID != "claude-max-20x" || !gotStart.Equal(wantStart) {
		t.Errorf("setPlan called with (%q, %q, %v), want (claude_code, claude-max-20x, %v)", gotProvider, gotID, gotStart, wantStart)
	}
	if m.mode != modeList {
		t.Error("after confirm should return to the list")
	}
	if m.view() != amortizedView {
		t.Errorf("should land on the amortized lens, got %q", m.view())
	}
}

// With multiple providers, `p` opens the provider step first, then the chosen
// provider's plan step.
func TestModel_InTUIPlanPicker_MultiProvider(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	base := []Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
	}}}
	provs := []ProviderChoice{{Name: "claude_code", Label: "Claude Code"}, {Name: "codex", Label: "Codex"}}
	m := New(base, 0, eng).WithPlanPicker(provs, planChoices(), ts, func(string, string, time.Time) []Period { return base })

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	m = nm.(Model)
	if m.mode != modePlan || !strings.Contains(m.View(), "which provider") {
		t.Fatalf("p with multiple providers should show the provider step:\n%s", m.View())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // → codex
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // choose codex
	m = nm.(Model)
	if !strings.Contains(m.View(), "Set the plan for Codex") {
		t.Errorf("should advance to codex's plan step:\n%s", m.View())
	}
}

func TestModel_PlanPickerDisabled(t *testing.T) {
	eng := pricing.NewEngine()
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "e1", "s1", "r", "claude-opus-4-8", time.Now(), event.Tokens{Input: 1_000_000}),
	}}}, 0, eng) // no WithPlanPicker
	if strings.Contains(m.View(), "p set plan") {
		t.Error("hint must not advertise p when the picker is disabled")
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if nm.(Model).mode != modeList {
		t.Error("p should be a no-op when the picker is disabled")
	}
}

// The session receipt lists the files touched, most-touched first.
func TestModel_ReceiptFiles(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string, files []string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = files
		return e
	}
	evs := []event.AgentEvent{
		mk("e1", []string{"internal/cli/tui.go", "README.md"}),
		mk("e2", []string{"internal/cli/tui.go"}), // touched twice → ranks first
	}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	r := m.View()
	if !strings.Contains(r, "files") || !strings.Contains(r, "internal/cli/tui.go") || !strings.Contains(r, "README.md") {
		t.Errorf("receipt should list the files touched:\n%s", r)
	}
	if strings.Index(r, "internal/cli/tui.go") > strings.Index(r, "README.md") {
		t.Errorf("most-touched file should come first:\n%s", r)
	}
}

// The drilled receipt links the sitting to code: a branch · short-SHA line and a
// per-file cost+churn heatmap (mirrors the static `explain session:` receipt).
func TestModel_ReceiptVCSHeatmap(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string, files []string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = files
		e.GitBranch = "feature/retry"
		e.GitSHA = "9f3c1a2b7d4e5f60718293a4b5c6d7e8f9012345"
		return e
	}
	evs := []event.AgentEvent{
		mk("e1", []string{"app.go", "README.md"}),
		mk("e2", []string{"app.go"}),
	}
	evs[0].SessionChurn = []event.FileChurn{{Path: "app.go", Added: 120, Removed: 30}}

	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	r := m.View()
	for _, want := range []string{"feature/retry", "9f3c1a2b7d", "app.go", "+120/-30"} {
		if !strings.Contains(r, want) {
			t.Errorf("TUI receipt missing %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "9f3c1a2b7d4e5f60718293a4b5c6d7e8f9012345") {
		t.Errorf("TUI receipt must shorten the sha:\n%s", r)
	}
}

// The receipt's file heatmap is a scrollable, height-aware window: a long list
// clamps with a "more" indicator, and ↓ follows the cursor to reveal the last file.
func TestModel_ReceiptFileScroll(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	// One session, 30 turns; each touches a distinct file with decreasing cost so
	// file00 is priciest (top of the list) and file29 cheapest (last).
	var evs []event.AgentEvent
	for i := 0; i < 30; i++ {
		e := priced(t, eng, fmt.Sprintf("e%02d", i), "s1", "payments", "claude-opus-4-8", ts,
			event.Tokens{Input: int64((30 - i) * 100_000)})
		e.Files = []string{fmt.Sprintf("file%02d.go", i)}
		evs = append(evs, e)
	}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // drill into the receipt
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatal("enter should open the receipt")
	}
	v := m.View()
	if !strings.Contains(v, "file00.go") {
		t.Fatalf("the priciest file should be visible at the top:\n%s", v)
	}
	if strings.Contains(v, "file29.go") {
		t.Fatalf("a clamped file list must not show the last file yet:\n%s", v)
	}
	if !strings.Contains(v, "more") {
		t.Errorf("a clamped file list should show a 'more' indicator:\n%s", v)
	}
	for i := 0; i < 29; i++ { // scroll to the bottom
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	if !strings.Contains(m.View(), "file29.go") {
		t.Errorf("scrolling to the bottom should reveal the last file:\n%s", m.View())
	}
}

// ↵ on a selected file drills into a file-detail view: the file's name and the
// turns (evidence) that touched it; esc returns to the receipt.
func TestModel_ReceiptFileDrill(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string, files []string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = files
		return e
	}
	evs := []event.AgentEvent{
		mk("evt_app1111aaaa", []string{"app.go", "README.md"}),
		mk("evt_app2222bbbb", []string{"app.go"}), // app.go touched twice → ranks first
	}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // receipt → file detail (top file: app.go)
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("enter on a file should open the file view, mode=%v", m.mode)
	}
	v := m.View()
	for _, want := range []string{"app.go", "2 turns", fmtTime(ts), "esc back"} {
		if !strings.Contains(v, want) {
			t.Errorf("file view missing %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "app1111a") || strings.Contains(v, "app2222b") {
		t.Errorf("file view should not surface raw event ids:\n%s", v)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatalf("esc from the file view should return to the receipt, mode=%v", m.mode)
	}
}

// The file view clamps its turn list to the terminal height (a "+N more" tail) and
// shows the file's churn on the total line.
func TestModel_FileDrillClampAndChurn(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	var evs []event.AgentEvent
	for i := 0; i < 20; i++ { // hot.go touched by 20 turns → many evidence rows
		e := priced(t, eng, fmt.Sprintf("e%02d", i), "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = []string{"hot.go"}
		evs = append(evs, e)
	}
	evs[0].SessionChurn = []event.FileChurn{{Path: "hot.go", Added: 100, Removed: 20}}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 16})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // receipt → file (hot.go is the only file)
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("should drill into hot.go, mode=%v", m.mode)
	}
	v := m.View()
	for _, want := range []string{"hot.go", "+100/-20", "more"} {
		if !strings.Contains(v, want) {
			t.Errorf("file view missing %q:\n%s", want, v)
		}
	}
}

// The honesty invariant: a turn's cost splits equally across the files it touched
// (remainder on the first-listed file), so per file the turns sum to that file's
// heatmap cost, and across files they sum to the session's api-equivalent total.
func TestFileTurnsReconcile(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string, files []string, in int64) event.AgentEvent {
		e := priced(t, eng, id, "s1", "r", "claude-opus-4-8", ts, event.Tokens{Input: in})
		e.Files = files
		return e
	}
	evs := []event.AgentEvent{
		mk("e1", []string{"a.go", "b.go", "c.go"}, 700_001), // api not divisible by 3 → remainder on a.go
		mk("e2", []string{"b.go"}, 1_000_000),
	}
	costs := fileCosts(evs)
	var grand int64
	for f, want := range costs {
		var sum int64
		for _, ft := range fileTurns(evs, f) {
			sum += ft.share
		}
		if sum != want {
			t.Errorf("file %s: fileTurns shares sum=%d, want fileCosts=%d", f, sum, want)
		}
		grand += want
	}
	var api int64
	for _, e := range evs {
		api += apiMicros(e)
	}
	if grand != api {
		t.Errorf("per-file costs sum=%d, want api total=%d", grand, api)
	}
}

// A receipt whose session touched no files focuses the top-turns section, so ↵
// opens the top turn's explain (no files to drill, but turns are evidence).
func TestModel_ReceiptNoFilesFocusesTurns(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "evt_solo1111", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
	}}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatal("enter should open the receipt")
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // no files → focus is turns → open explain
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("enter on a fileless receipt should open the top turn's explain, mode=%v", m.mode)
	}
}

// ↵ on a file-view turn opens that turn's explain: the evidence + cost breakdown.
// esc returns to the file view.
func TestModel_FileTurnExplain(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
		e.Files = []string{"app.go"}
		e.Evidence = event.Evidence{ParserName: "claude_code", ParserVersion: "0A", SourcePathHash: "abcdef0123456789", SourceLine: 42, CostMethod: "computed", ConfidenceScore: 1, PricingTableVersion: "embedded", PricedAt: ts}
		return e
	}
	evs := []event.AgentEvent{mk("evt_aaaa1111"), mk("evt_bbbb2222")}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // receipt → file view (app.go)
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("want file view, got %v", m.mode)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // file turn → explain
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("enter on a file turn should open explain, got %v", m.mode)
	}
	v := m.View()
	for _, want := range []string{"opus-4-8", "composition", "cache-read", "esc back"} {
		if !strings.Contains(v, want) {
			t.Errorf("explain view missing %q:\n%s", want, v)
		}
	}
	// The provenance ledger rows were cut — the evidence view is cost + prompt now.
	for _, gone := range []string{"source", "parser", "method", "priced", "missing", "views"} {
		if strings.Contains(v, gone) {
			t.Errorf("explain view should no longer show the %q row:\n%s", gone, v)
		}
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("esc from explain should return to the file view, got %v", m.mode)
	}
}

// In the receipt, ↓ moves the unified cursor from the files down into the top-turns
// section (no tab); ↵ then opens the highlighted turn's evidence. esc returns.
func TestModel_ReceiptTurnExplain(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
		e.Files = []string{"app.go"} // one file → cursor starts on it; ↓ flows into the turns
		e.Evidence = event.Evidence{ParserName: "claude_code", ParserVersion: "0A", CostMethod: "computed", ConfidenceScore: 1}
		return e
	}
	evs := []event.AgentEvent{mk("evt_aaaa1111"), mk("evt_bbbb2222")}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt (cursor: top file)
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // ↓ past the single file → first top turn
	m = nm.(Model)
	if !strings.Contains(m.View(), "▶") {
		t.Errorf("the highlighted row should carry the ▶ cursor:\n%s", m.View())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open the highlighted turn's explain
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("↓ into the turns + enter should open explain, got %v", m.mode)
	}
	if !strings.Contains(m.View(), "composition") {
		t.Errorf("explain view should show the composition breakdown:\n%s", m.View())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatalf("esc from explain should return to the receipt, got %v", m.mode)
	}
}

// The receipt is one continuous ↑/↓ cursor: it starts on the priciest file, flows
// down into the top-turns section (no tab), and ↵ opens whatever's highlighted — a
// file (→ file view) or a turn (→ evidence). The cursor clamps at both ends so ↵
// never lands on an empty row.
func TestModel_ReceiptUnifiedCursor(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	withFile := func(id string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = []string{"app.go"}
		return e
	}
	// One unique file (app.go) + two turns → cursor rows: [app.go, turn, turn].
	evs := []event.AgentEvent{withFile("evt_aaaa1111"), withFile("evt_bbbb2222")}
	open := func() Model {
		m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
		return nm.(Model)
	}

	// Cursor starts on the file: ↵ opens the file view.
	m := open()
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("enter at the top of the receipt should open the top file, got %v", m.mode)
	}

	// ↑ above the first row clamps, keeping the file selected: ↵ still opens the file.
	m = open()
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("↑ above the first row should clamp on the file, got %v", m.mode)
	}

	// ↓ once flows past the single file into the first turn: ↵ opens its evidence.
	m = open()
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("↓ into the turns + enter should open a turn's evidence, got %v", m.mode)
	}

	// ↓ well past the last turn clamps (never opens an empty row): ↵ still opens a turn.
	m = open()
	for i := 0; i < 9; i++ {
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("the cursor should clamp at the last turn, opening evidence on enter, got %v", m.mode)
	}

	// Fileless session: the cursor starts on the first turn; ↵ opens evidence directly.
	m2 := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "evt_cccc3333", "s2", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
	}}}, 0, eng)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt (no files → cursor on the turn)
	m2 = nm.(Model)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → explain
	m2 = nm.(Model)
	if m2.mode != modeExplain {
		t.Fatalf("a fileless receipt should open a turn's evidence on enter, got %v", m2.mode)
	}
}

// The by-day bar reconciles with the session totals: a priced turn whose log line
// carried no parseable timestamp is still counted — folded onto its OWN session's
// dated day — so the bar's grand total never silently runs under the headline.
func TestBucketSpend_UndatedTurnsFoldIntoSessionDay(t *testing.T) {
	eng := pricing.NewEngine()
	d10 := time.Date(2026, 6, 10, 9, 0, 0, 0, time.UTC)
	d11 := time.Date(2026, 6, 11, 9, 0, 0, 0, time.UTC)
	s1dated := priced(t, eng, "e1", "s1", "payments", "claude-fable-5", d10, event.Tokens{Input: 1_000_000})           // $10, s1, Jun 10
	s2dated := priced(t, eng, "e2", "s2", "web", "claude-fable-5", d11, event.Tokens{Input: 500_000})                  // $5,  s2, Jun 11
	s1undated := priced(t, eng, "e3", "s1", "payments", "claude-fable-5", time.Time{}, event.Tokens{Input: 2_000_000}) // $20, s1, NO timestamp
	evs := []event.AgentEvent{s1dated, s2dated, s1undated}

	var want int64
	for _, e := range evs {
		want += apiMicros(e)
	}
	vals, unit, start := bucketSpend(evs, time.Time{}, time.Time{}, time.UTC)
	var got int64
	for _, v := range vals {
		got += v
	}
	if got != want {
		t.Fatalf("bar total %s must reconcile with the session total %s (undated turn was dropped)", money(got), money(want))
	}
	// The undated s1 turn folds onto s1's own day (Jun 10), not s2's day or a stray bucket.
	if i := bucketIndex(start, unit, d10, time.UTC); vals[i] != apiMicros(s1dated)+apiMicros(s1undated) {
		t.Errorf("undated s1 spend should fold onto Jun 10 (s1's day): bucket=%s want=%s",
			money(vals[i]), money(apiMicros(s1dated)+apiMicros(s1undated)))
	}
}

// tab is an accelerator over the unified cursor: from the files it jumps straight to
// the first top turn, and from the turns back to the priciest file. ↑/↓ still flows
// across both; with only one section present tab is a no-op (never an empty row).
func TestModel_ReceiptTabJumpsBetweenSections(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id, file string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = []string{file}
		return e
	}
	// Two distinct files + two turns → cursor rows: [a.go, b.go, turn, turn].
	evs := []event.AgentEvent{mk("evt_aaaa1111", "a.go"), mk("evt_bbbb2222", "b.go")}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt, cursor on the top file
	m = nm.(Model)

	// tab from the files → first top turn: ↵ opens a turn's evidence (not a file view).
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("tab from the files should land on a turn; enter should open evidence, got %v", m.mode)
	}

	// Back to the receipt (cursor still on the turn), then tab → priciest file: ↵ opens it.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeFile {
		t.Fatalf("tab from the turns should land on the top file; enter should open it, got %v", m.mode)
	}

	// Fileless session: tab has no second section to reach, so it stays on the turn.
	m2 := New([]Period{{Label: "today", Events: []event.AgentEvent{
		priced(t, eng, "evt_cccc3333", "s2", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000}),
	}}}, 0, eng)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt, cursor on the turn
	m2 = nm.(Model)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyTab}) // no files → no-op
	m2 = nm.(Model)
	nm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m2 = nm.(Model)
	if m2.mode != modeExplain {
		t.Fatalf("tab with only a turns section should stay on the turn, got %v", m2.mode)
	}
}

// The file heatmap keeps at least five files visible (or all, when fewer) even on a
// short terminal, so the priciest files don't collapse to a single row.
func TestModel_ReceiptShowsAtLeastFiveFiles(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	var evs []event.AgentEvent
	for i := 0; i < 8; i++ { // 8 distinct files, decreasing cost → file00 priciest
		e := priced(t, eng, fmt.Sprintf("e%02d", i), "s1", "payments", "claude-opus-4-8", ts.Add(time.Duration(i)*time.Minute),
			event.Tokens{Input: int64((8 - i) * 100_000)})
		e.Files = []string{fmt.Sprintf("file%02d.go", i)}
		evs = append(evs, e)
	}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 16}) // a short terminal
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt
	m = nm.(Model)
	v := m.View()
	for i := 0; i < 5; i++ {
		if !strings.Contains(v, fmt.Sprintf("file%02d.go", i)) {
			t.Errorf("receipt should show at least the 5 priciest files on a short terminal; missing file%02d.go:\n%s", i, v)
		}
	}
}

// Each provider's plan fee is allocated only among ITS sessions — a codex session
// never absorbs claude's fee.
func TestModel_AmortizedPerProvider(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id, prov, model string, tk event.Tokens) event.AgentEvent {
		e := event.AgentEvent{EventID: id, SessionID: id, Provider: prov, Model: model, Tokens: tk, TSStart: ts, TSEnd: ts}
		if err := eng.Price(&e, pricing.Plan{Kind: "api"}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	evs := []event.AgentEvent{
		mk("cc", "claude_code", "claude-opus-4-8", event.Tokens{Input: 2_000_000}),
		mk("cx", "codex", "gpt-5.3-codex", event.Tokens{Input: 1_000_000}),
	}
	per := Period{Label: "this month", Events: evs, HasPlan: true,
		Amortized: map[string]int64{"claude_code": 200_000_000, "codex": 100_000_000}}
	m := New([]Period{per}, 0, eng)
	for m.view() != amortizedView {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'v'}})
		m = nm.(Model)
	}
	var cc, cx int64
	for _, r := range m.rows {
		switch r.id {
		case "cc":
			cc = r.micros
		case "cx":
			cx = r.micros
		}
	}
	if cc != 200_000_000 {
		t.Errorf("claude_code session should get claude's $200 (not a share of the combined pool), got %d", cc)
	}
	if cx != 100_000_000 {
		t.Errorf("codex session should get codex's $100, got %d", cx)
	}
}

// The spend-over-time bar buckets api-equivalent spend by an adaptive calendar
// unit and labels the peak.
func TestDurationBar(t *testing.T) {
	if chooseUnit(10*time.Hour) != "hour" || chooseUnit(10*24*time.Hour) != "day" ||
		chooseUnit(100*24*time.Hour) != "week" || chooseUnit(400*24*time.Hour) != "month" {
		t.Fatalf("unit selection wrong")
	}

	mk := func(day int, micros int64) event.AgentEvent {
		m := event.USD(micros)
		ts := time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC)
		return event.AgentEvent{EventID: fmt.Sprintf("e%d", day), SessionID: "s", Provider: "claude_code",
			Model: "claude-opus-4-8", TSStart: ts, TSEnd: ts, CostViews: event.CostViews{APIEquivalent: &m}}
	}
	evs := []event.AgentEvent{mk(1, 1_000_000), mk(3, 5_000_000), mk(5, 2_000_000)} // span 4 days → daily

	vals, unit, start := bucketSpend(evs, time.Time{}, time.Time{}, time.UTC)
	if unit != "day" || len(vals) != 5 || !start.Equal(time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("expected 5 daily buckets from Jun 1, got unit=%q n=%d start=%v", unit, len(vals), start)
	}
	if vals[0] != 1_000_000 || vals[1] != 0 || vals[2] != 5_000_000 || vals[3] != 0 || vals[4] != 2_000_000 {
		t.Errorf("daily buckets wrong: %v", vals)
	}
	db := durationBar(evs, time.Time{}, time.Time{}, time.UTC)
	for _, want := range []string{"by day", "peak", "Jun 3", "$5.00"} {
		if !strings.Contains(db, want) {
			t.Errorf("durationBar missing %q:\n%s", want, db)
		}
	}
}

// The spend bar's unit follows the SELECTED PERIOD, not where the data happens to
// sit: a long window stays coarse (week/month) even when the events span only days.
// The unbounded "all" window falls back to the events' own span.
func TestBucketSpend_UnitTracksPeriod(t *testing.T) {
	mk := func(day int, micros int64) event.AgentEvent {
		m := event.USD(micros)
		ts := time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC)
		return event.AgentEvent{EventID: fmt.Sprintf("e%d", day), SessionID: "s", Provider: "claude_code",
			Model: "claude-opus-4-8", TSStart: ts, TSEnd: ts, CostViews: event.CostViews{APIEquivalent: &m}}
	}
	evs := []event.AgentEvent{mk(1, 1_000_000), mk(3, 5_000_000)} // only a 3-day event span

	// A full-quarter window must bucket by week across the quarter (~13 weeks), not day.
	qSince := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	qUntil := time.Date(2026, 6, 30, 23, 59, 59, 0, time.UTC)
	vals, unit, _ := bucketSpend(evs, qSince, qUntil, time.UTC)
	if unit != "week" {
		t.Errorf("a ~90-day period must bucket by week regardless of event span, got %q", unit)
	}
	if len(vals) < 12 {
		t.Errorf("buckets should span the quarter (~13 weeks), got %d", len(vals))
	}

	// A year-long window → month.
	if _, unit, _ := bucketSpend(evs, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), time.UTC); unit != "month" {
		t.Errorf("a ~365-day period must bucket by month, got %q", unit)
	}

	// Unbounded "all" falls back to the events' own span → day.
	if _, unit, _ := bucketSpend(evs, time.Time{}, time.Time{}, time.UTC); unit != "day" {
		t.Errorf("the unbounded window should fall back to the event span (day), got %q", unit)
	}

	// The list view wires the period bounds through, so a long window reads "by week".
	if db := durationBar(evs, qSince, qUntil, time.UTC); !strings.Contains(db, "by week") {
		t.Errorf("durationBar over a quarter should read 'by week':\n%s", db)
	}
}

// The spend bar buckets and labels in the display zone: a turn at 20:00 UTC lands in
// the next local day's 1am hour bucket under IST (+5:30), not the UTC hour.
func TestBucketSpend_LocalizesToDisplayZone(t *testing.T) {
	ist := time.FixedZone("IST", 5*3600+30*60)
	m := event.USD(3_000_000)
	ts := time.Date(2026, 6, 19, 20, 0, 0, 0, time.UTC) // == 2026-06-20 01:30 IST
	ev := event.AgentEvent{EventID: "e1", SessionID: "s", Provider: "claude_code",
		Model: "claude-opus-4-8", TSStart: ts, TSEnd: ts, CostViews: event.CostViews{APIEquivalent: &m}}
	since := time.Date(2026, 6, 19, 18, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 19, 22, 0, 0, 0, time.UTC)
	db := durationBar([]event.AgentEvent{ev}, since, until, ist)
	for _, want := range []string{"by hour", "Jun 20", "1am"} { // local day rolled over, local hour
		if !strings.Contains(db, want) {
			t.Errorf("durationBar should bucket/label in IST (want %q):\n%s", want, db)
		}
	}
}

// The receipt's top-turns and the file view label their columns with the turn's
// COST · WHEN · MODEL · TOKENS — the opaque event id is no longer surfaced.
func TestModel_TurnTableHeaders(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string, files []string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
		e.Files = files
		return e
	}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{mk("evt_aaaa1111bbbb", []string{"app.go"})}}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	r := m.View()
	for _, want := range []string{"COST", "WHEN", "MODEL", "TOKENS"} {
		if !strings.Contains(r, want) {
			t.Errorf("receipt top-turns missing column header %q:\n%s", want, r)
		}
	}
	if strings.Contains(r, "EVENT") {
		t.Errorf("receipt top-turns should no longer show the EVENT column:\n%s", r)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // receipt → file view
	m = nm.(Model)
	f := m.View()
	for _, want := range []string{"COST", "WHEN", "MODEL", "TOKENS"} {
		if !strings.Contains(f, want) {
			t.Errorf("file view missing column header %q:\n%s", want, f)
		}
	}
	if strings.Contains(f, "EVENT") {
		t.Errorf("file view should no longer show the EVENT column:\n%s", f)
	}
}

// Drilling down keeps the context visible: a breadcrumb at the top of each view
// shows the path from the period down to the current level (so the period — shown
// on the list — is never "lost" once you drill in).
func TestModel_Breadcrumb(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string) event.AgentEvent {
		e := priced(t, eng, id, "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
		e.Files = []string{"app.go"}
		return e
	}
	m := New([]Period{{Label: "this week", Events: []event.AgentEvent{mk("evt_aaaa1111bbbb")}}}, 0, eng)

	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt
	m = nm.(Model)
	r := m.View()
	if !strings.Contains(r, "this week") || !strings.Contains(r, "payments") || !strings.Contains(r, "›") {
		t.Errorf("receipt breadcrumb should show period › session:\n%s", r)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → file (app.go)
	m = nm.(Model)
	f := m.View()
	if !strings.Contains(f, "this week") || !strings.Contains(f, "app.go") || !strings.Contains(f, "›") {
		t.Errorf("file breadcrumb should show period › session › file:\n%s", f)
	}

	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → explain (the turn)
	m = nm.(Model)
	e := m.View()
	if !strings.Contains(e, "this week") || !strings.Contains(e, fmtTime(ts)) || !strings.Contains(e, "›") {
		t.Errorf("explain breadcrumb should carry the period down to the turn (by time):\n%s", e)
	}
	if strings.Contains(e, "aaaa1111") {
		t.Errorf("explain view should not surface the raw event id:\n%s", e)
	}
}

func TestModel_Empty(t *testing.T) {
	m := New([]Period{{Label: "today", Events: nil}}, 0, pricing.NewEngine())
	if !strings.Contains(m.View(), "no sessions") {
		t.Fatalf("empty view should say no sessions:\n%s", m.View())
	}
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = nm.(Model)
	if m.mode != modeList {
		t.Fatal("enter on an empty list must not crash/drill")
	}
}

// wrapText soft-wraps on word boundaries to a width, leaves text intact when width
// is unknown (≤0), and preserves blank-line paragraph breaks.
func TestWrapText(t *testing.T) {
	got := wrapText("alpha beta gamma delta", 11)
	want := []string{"alpha beta", "gamma delta"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("wrapText = %#v want %#v", got, want)
	}
	if g := wrapText("a b c d", 0); len(g) != 1 || g[0] != "a b c d" {
		t.Errorf("wrapText width<=0 = %#v", g)
	}
	if g := wrapText("one\n\ntwo", 80); len(g) != 3 || g[0] != "one" || g[1] != "" || g[2] != "two" {
		t.Errorf("wrapText paragraphs = %#v", g)
	}
}

// With a prompt resolver injected, the turn evidence view re-reads and renders the
// human prompt behind the turn (full text; unwrapped here since width is unset).
func TestModel_ExplainShowsPrompt(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_aaaa1111bbbb", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
	e.Files = []string{"app.go"}
	prompt := "Refactor the payment retry handler to use exponential backoff and add a unit test for the timeout path."
	resolver := func(ev event.AgentEvent) (string, bool) { return prompt, ev.EventID == "evt_aaaa1111bbbb" }
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng).WithPromptResolver(resolver)
	for i := 0; i < 3; i++ { // list → receipt → file → explain
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = nm.(Model)
	}
	v := m.View()
	if !strings.Contains(v, "prompt") {
		t.Fatalf("explain view should carry a prompt section:\n%s", v)
	}
	for _, frag := range []string{"Refactor the payment", "exponential backoff", "timeout path"} {
		if !strings.Contains(v, frag) {
			t.Errorf("explain view missing prompt fragment %q:\n%s", frag, v)
		}
	}
	// The body sits in a gutter so the "prompt" header is distinct from the text.
	if !strings.Contains(v, "│") {
		t.Errorf("prompt body should sit in a │ gutter, set off from the header:\n%s", v)
	}
}

// When the prompt can't be recovered (log gone / no preceding user text), the view
// says so rather than silently dropping the section.
func TestModel_ExplainPromptUnavailable(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_aaaa1111bbbb", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000})
	e.Files = []string{"app.go"}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng).
		WithPromptResolver(func(event.AgentEvent) (string, bool) { return "", false })
	for i := 0; i < 3; i++ {
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = nm.(Model)
	}
	if v := m.View(); !strings.Contains(v, "unavailable") {
		t.Errorf("explain view should note an unavailable prompt:\n%s", v)
	}
}

// q quits from anywhere, including a drill-down — it is never "back" (esc/←/h/
// backspace are back). Regression guard: q used to collapse one level like esc.
func TestModel_QuitFromDrill(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_aaaa1111", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000})
	e.Files = []string{"app.go"}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatalf("setup: want receipt, got %v", m.mode)
	}
	// q must quit, not go back to the list.
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}); cmd == nil {
		t.Fatal("q from a drill-down should issue a command")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q from a drill-down should quit, not go back")
	}
	// esc still goes back one level.
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if nm.(Model).mode != modeList {
		t.Fatalf("esc should go back to the list, got %v", nm.(Model).mode)
	}
}

// The active period — in words AND its actual UTC date span — stays visible on every
// screen (list → receipt → file → turn evidence) so the window is never lost deeper in.
func TestModel_PeriodPersistsAcrossDrill(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	since := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	until := time.Date(2026, 6, 21, 23, 59, 59, 0, time.UTC)
	span := periodDates(since, until)
	if !strings.Contains(span, "Jun 15") || !strings.Contains(span, "Jun 21") {
		t.Fatalf("periodDates = %q, want a Jun 15..Jun 21 range", span)
	}
	e := priced(t, eng, "evt_aaaa1111", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
	e.Files = []string{"app.go"}
	m := New([]Period{{Label: "this week", Events: []event.AgentEvent{e}, Since: since, Until: until}}, 0, eng)
	for _, screen := range []string{"list", "receipt", "file", "explain"} {
		v := m.View()
		if !strings.Contains(v, "this week") || !strings.Contains(v, span) {
			t.Errorf("%s should show the period words + dates (%q):\n%s", screen, span, v)
		}
		nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = nm.(Model)
	}
}

// The session list groups by calendar day (most-recent first), labels Today/Yesterday
// against the reference now, and the live session leads its day with a badge.
func TestModel_DayGroupedListWithLive(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	mk := func(id, repo string, ts time.Time, in int64) event.AgentEvent {
		return priced(t, eng, "evt_"+id, id, repo, "claude-opus-4-8", ts, event.Tokens{Input: in})
	}
	evs := []event.AgentEvent{
		mk("slive", "payments", now.Add(-3*time.Minute), 1_000_000), // today, live (3m ago)
		mk("sbig", "web", now.Add(-5*time.Hour), 9_000_000),         // today, priciest
		mk("syest", "billing", now.AddDate(0, 0, -1), 2_000_000),    // yesterday
	}
	m := New([]Period{{Label: "this week", Events: evs, Since: now.AddDate(0, 0, -6), Until: now}}, 0, eng).WithNow(now)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	v := m.View()

	if !strings.Contains(v, "Today") || !strings.Contains(v, "Yesterday") {
		t.Fatalf("expected Today/Yesterday day headers:\n%s", v)
	}
	if !strings.Contains(v, "live") {
		t.Errorf("expected a live badge for the 3-minutes-ago session:\n%s", v)
	}
	if !strings.Contains(v, "active in the last 10m") {
		t.Errorf("expected the live legend explaining the badge:\n%s", v)
	}
	if strings.Index(v, "Today") > strings.Index(v, "Yesterday") {
		t.Errorf("the most-recent day (Today) should sort above Yesterday:\n%s", v)
	}
	if strings.Index(v, "payments") > strings.Index(v, "web") {
		t.Errorf("the live session should lead its day (payments before the priciest web):\n%s", v)
	}
}

// Watch mode: a tick reloads fresh periods and advances the clock in place, so an
// ongoing session's data updates and liveness decays without leaving the list.
func TestModel_WatchTickRefreshesAndAdvancesClock(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	mk := func(id string, ago time.Duration, in int64) event.AgentEvent {
		return priced(t, eng, "evt_"+id, id, "payments", "claude-opus-4-8", now.Add(-ago), event.Tokens{Input: in})
	}
	win := func(evs []event.AgentEvent) []Period {
		return []Period{{Label: "this week", Events: evs, Since: now.AddDate(0, 0, -6), Until: now}}
	}
	start := win([]event.AgentEvent{mk("s1", 2*time.Minute, 1_000_000)})                                      // one live session
	grown := win([]event.AgentEvent{mk("s1", 2*time.Minute, 1_000_000), mk("s2", 30*time.Minute, 2_000_000)}) // a second appears

	clock := now
	m := New(start, 0, eng).
		WithNow(now).
		WithWatch(time.Second, func() time.Time { return clock }, func() []Period { return grown })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	if len(m.rows) != 1 {
		t.Fatalf("setup: one session before the tick, got %d", len(m.rows))
	}
	if !strings.Contains(m.View(), "active in the last") {
		t.Fatal("setup: the 2-minutes-ago session should be live before the tick")
	}

	clock = now.Add(20 * time.Minute) // time moves on; the session goes stale
	nm, cmd := m.Update(tickMsg(clock))
	m = nm.(Model)

	if len(m.rows) != 2 {
		t.Errorf("a tick should reload fresh data (2 sessions now), got %d", len(m.rows))
	}
	if cmd == nil {
		t.Error("a watch tick should re-arm the next tick")
	}
	if strings.Contains(m.View(), "active in the last") {
		t.Errorf("after the clock advanced 20m nothing is live → legend should be gone:\n%s", m.View())
	}
}

func TestModel_InitArmsTickOnlyWhenWatching(t *testing.T) {
	eng := pricing.NewEngine()
	base := []Period{{Label: "today", Events: nil}}
	if New(base, 0, eng).Init() != nil {
		t.Error("no watch configured → Init should arm no tick")
	}
	watched := New(base, 0, eng).WithWatch(time.Second, nil, func() []Period { return base })
	if watched.Init() == nil {
		t.Error("watch configured → Init should arm a tick")
	}
}

// Without a reference clock (no WithNow), nothing is live, so the live legend is
// suppressed — the badge never appears without something to explain.
func TestModel_NoLiveLegendWithoutClock(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 19, 11, 59, 0, 0, time.UTC)
	e := priced(t, eng, "evt_x", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 1_000_000})
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng) // no WithNow → now zero
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = nm.(Model)
	if strings.Contains(m.View(), "active in the last") {
		t.Errorf("no reference clock should mean no live legend:\n%s", m.View())
	}
}

// The plan-limit gauge renders in the list header from the injected quota samples,
// and shows even with no sessions in the window (the wall is visible on a quiet day).
func TestModel_QuotaGaugeRenders(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	samples := []quota.Sample{{Provider: "claude", Window: quota.WindowWeekly, UsedPercent: 78, ResetsAt: now.Add(48 * time.Hour), ObservedAt: now}}
	m := New([]Period{{Label: "this week", Events: nil, Since: now.AddDate(0, 0, -6), Until: now}}, 0, eng).
		WithNow(now).
		WithQuota(func() []quota.Sample { return samples })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	v := m.View()
	for _, want := range []string{"Claude", "weekly", "78%", "resets in"} {
		if !strings.Contains(v, want) {
			t.Errorf("list header should show the Claude weekly gauge (%q):\n%s", want, v)
		}
	}
}

// With Claude activity but no quota snapshot, the header shows an explicit "unknown"
// line rather than nothing — so the gauge explains its blank.
// The receipt leads with the resolved human session title when a name resolver is wired.
func TestModel_ReceiptShowsSessionName(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_aaaa1111", "s1", "payments", "claude-opus-4-8", now, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng).
		WithNameResolver(func(event.AgentEvent) (string, bool) { return "Fixed the failing test", true })
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	if m.mode != modeReceipt {
		t.Fatalf("enter should open the receipt, mode=%v", m.mode)
	}
	if !strings.Contains(m.View(), "Fixed the failing test") {
		t.Errorf("receipt should lead with the resolved session title:\n%s", m.View())
	}
}

// Subagent turns roll up under their parent session and the row shows a ⋮N-sub marker.
func TestModel_ListShowsSubagentRollup(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id, sub string) event.AgentEvent {
		e := priced(t, eng, "evt_"+id, "P", "payments", "claude-opus-4-8", now, event.Tokens{Input: 100_000})
		e.SubagentID = sub
		return e
	}
	evs := []event.AgentEvent{mk("p", ""), mk("a", "w1"), mk("b", "w2")} // one session P + two subagents
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng).WithNow(now)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	if v := m.View(); !strings.Contains(v, "⋮2 sub") {
		t.Errorf("the session row should show the rolled-up subagent count (⋮2 sub):\n%s", v)
	}
}

// `c` opens the chain from the receipt; ↓ moves the cursor; ↵ opens a turn's evidence
// and esc returns to the chain.
func TestModel_ChainViewFromReceipt(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	mk := func(id string, min int, in int64) event.AgentEvent {
		return priced(t, eng, "evt_"+id, "s1", "payments", "claude-opus-4-8", now.Add(time.Duration(min)*time.Minute), event.Tokens{Input: in})
	}
	evs := []event.AgentEvent{mk("a", 0, 1_000_000), mk("b", 1, 2_000_000), mk("c", 2, 500_000)}
	m := New([]Period{{Label: "today", Events: evs}}, 0, eng)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // list → receipt
	m = nm.(Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}) // receipt → chain
	m = nm.(Model)
	if m.mode != modeChain {
		t.Fatalf("c should open the chain view, got mode %v", m.mode)
	}
	if !strings.Contains(m.View(), "CHAIN") {
		t.Errorf("chain view should render a CHAIN header:\n%s", m.View())
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown}) // ↓ to the next turn
	m = nm.(Model)
	if m.chainCursor != 1 {
		t.Errorf("down should move the chain cursor to 1, got %d", m.chainCursor)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // open that turn's evidence
	m = nm.(Model)
	if m.mode != modeExplain {
		t.Fatalf("enter should open the turn's evidence, got %v", m.mode)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // back to the chain
	m = nm.(Model)
	if m.mode != modeChain {
		t.Errorf("esc from evidence should return to the chain, got %v", m.mode)
	}
}

// The monthly budget pace gauge renders in the list header from the injected pace.
func TestModel_BudgetGaugeRenders(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC)
	start, end := budget.MonthBounds(now)
	pace := budget.ComputePace(500_000_000, 250_000_000, start, now, end) // 50% at ~half the month
	m := New([]Period{{Label: "today", Events: nil}}, 0, eng).
		WithNow(now).
		WithBudget(func() (budget.Pace, bool) { return pace, true })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	v := m.View()
	for _, want := range []string{"budget", "$500", "50%", "on track"} {
		if !strings.Contains(v, want) {
			t.Errorf("list header should show the budget pace gauge (%q):\n%s", want, v)
		}
	}
}

func TestModel_QuotaUnknownWhenClaudeActivityNoSnapshot(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_x", "s1", "payments", "claude-opus-4-8", now.Add(-time.Hour), event.Tokens{Input: 1_000_000})
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}, Since: now.AddDate(0, 0, -1), Until: now}}, 0, eng).WithNow(now)
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = nm.(Model)
	v := m.View()
	if !strings.Contains(v, "Claude weekly") || !strings.Contains(v, "unknown") {
		t.Errorf("Claude activity with no quota snapshot should show the explicit unknown line:\n%s", v)
	}
}

// A long prompt is bounded in a scrollable box (so it never buries the evidence) with
// a scrollbar thumb, and ↓ scrolls it.
func TestModel_PromptScrolls(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_aaaa1111", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000})
	e.Files = []string{"app.go"}
	var sb strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&sb, "line%02d of the prompt\n", i)
	}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng).
		WithPromptResolver(func(event.AgentEvent) (string, bool) { return sb.String(), true })
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = nm.(Model)
	for i := 0; i < 3; i++ { // list → receipt → file → explain
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = nm.(Model)
	}
	if m.mode != modeExplain {
		t.Fatalf("want explain, got %v", m.mode)
	}
	top := m.View()
	if !strings.Contains(top, "line00") {
		t.Fatalf("explain should show the top of a long prompt:\n%s", top)
	}
	if strings.Contains(top, "line39") {
		t.Fatalf("a long prompt must be bounded — line39 should be scrolled off:\n%s", top)
	}
	if !strings.Contains(top, "┃") {
		t.Errorf("an overflowing prompt should show a scrollbar thumb:\n%s", top)
	}
	for i := 0; i < 3; i++ { // ↓ scrolls the box
		nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = nm.(Model)
	}
	if m.View() == top {
		t.Errorf("the prompt box should scroll on ↓")
	}
}

// The list must fit the terminal height so the period + view header never scrolls off
// the top: the spend bar's two lines have to be counted in the row budget, not just
// the static chrome.
func TestModel_ListFitsHeightWithDurationBar(t *testing.T) {
	eng := pricing.NewEngine()
	base := time.Date(2026, 6, 15, 1, 0, 0, 0, time.UTC)
	var evs []event.AgentEvent
	for i := 0; i < 30; i++ {
		evs = append(evs, priced(t, eng, fmt.Sprintf("e%02d", i), fmt.Sprintf("s%02d", i), "repo",
			"claude-opus-4-8", base.Add(time.Duration(i)*6*time.Hour), event.Tokens{Input: int64((30 - i) * 100_000)}))
	}
	m := New([]Period{{Label: "this week", Events: evs, Since: base, Until: base.Add(8 * 24 * time.Hour)}}, 0, eng)
	const H = 20
	nm, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: H})
	m = nm.(Model)
	v := m.View()
	if !strings.Contains(v, "by ") { // the spend bar is what overflows the budget
		t.Fatalf("expected a spend bar in the list:\n%s", v)
	}
	if got := len(strings.Split(strings.TrimRight(v, "\n"), "\n")); got > H {
		t.Errorf("list renders %d lines into a %d-row terminal — the period/view header scrolls off:\n%s", got, H, v)
	}
}

// Receipt sections are separated by a blank line — the arbitrage/cost block and the
// files heatmap must not run together.
func TestModel_ReceiptSectionSpacing(t *testing.T) {
	eng := pricing.NewEngine()
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := priced(t, eng, "evt_aaaa1111", "s1", "payments", "claude-opus-4-8", ts, event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 50_000_000})
	e.Files = []string{"app.go"}
	m := New([]Period{{Label: "today", Events: []event.AgentEvent{e}}}, 0, eng)
	nm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // → receipt
	m = nm.(Model)
	r := m.View()
	if !strings.Contains(r, "arbitrage") || !strings.Contains(r, "files") {
		t.Fatalf("setup: receipt should show both arbitrage and files:\n%s", r)
	}
	if !strings.Contains(r, "\n\n  files") {
		t.Errorf("a blank line should separate the cost/arbitrage section from the files section:\n%s", r)
	}
}

func TestHelpers(t *testing.T) {
	if money(431_850) != "$0.43" || money(3_630_860_000) != "$3,630.86" {
		t.Errorf("money: %s %s", money(431_850), money(3_630_860_000))
	}
	if humanModel("<synthetic>") != "other" || humanModel("claude-opus-4-8") != "opus-4-8" || humanModel("") != "—" {
		t.Errorf("humanModel wrong: %q %q %q", humanModel("<synthetic>"), humanModel("claude-opus-4-8"), humanModel(""))
	}
	if got := tokenSummary(event.Tokens{Input: 100_000, Output: 2_000, CacheRead: 5_000_000, CacheWrite: 80_000}); got != "100,000 in · 2,000 out · 5,000,000 cache-read · 80,000 cache-write" {
		t.Errorf("tokenSummary split = %q", got)
	}
	if comma(1256) != "1,256" || comma(254) != "254" {
		t.Errorf("comma: %s %s", comma(1256), comma(254))
	}
	if elapsed(22*time.Hour+12*time.Minute) != "22h12m" || elapsed(26*time.Hour) != "1d2h" {
		t.Errorf("elapsed: %s %s", elapsed(22*time.Hour+12*time.Minute), elapsed(26*time.Hour))
	}
	if trunc("abcdef", 4) != "abc…" || orDash("") != "—" || providerLabel("claude_code") != "Claude Code" {
		t.Errorf("trunc/orDash/providerLabel wrong")
	}
	if spendBar(5, 10, 4) != "██░░" {
		t.Errorf("spendBar = %q, want ██░░", spendBar(5, 10, 4))
	}
}
