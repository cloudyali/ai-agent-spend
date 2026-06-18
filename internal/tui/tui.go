// Package tui is the interactive explorer (`aispend tui`): a navigable session
// list that drills to the receipt on ↵. It is isolated from the cli package so it
// imports only Bubble Tea + lipgloss + the data model + the (sqlite-free) pricing
// engine — never the store — which keeps Update/View pure and unit-testable
// without a TTY and keeps the binary's net-free import graph intact.
package tui

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
)

// Period is one selectable window: a label, the events that fall in it, and the
// period's prorated subscription fee (Amortized) when a plan is configured — the
// CLI computes that allocation basis so this package needs no plan/period parsing.
type Period struct {
	Label     string
	Events    []event.AgentEvent
	Amortized int64 // prorated plan fee for this window (micros); 0 if no plan
	HasPlan   bool  // a subscription plan is configured (enables the amortized lens)
}

type mode int

const (
	modeList mode = iota
	modeReceipt
	modePlan // the in-explorer plan picker (set the subscription without leaving the TUI)
)

// amortizedView is the period-level allocation lens (the subscription-arbitrage
// half): the prorated plan fee distributed across sessions by api-equivalent
// share. It is offered only when a plan is configured (see availableViews).
const amortizedView = "amortized"

// cycleViews are the distinct per-event cost lenses `v` can rotate through.
// estimated mirrors api-equivalent in 0A so it is omitted; only views actually
// present in the data are offered, so `v` never lands on an all-$0 screen.
var cycleViews = []string{"api_equivalent", "reported", "billed", "marginal"}

// Model is the Bubble Tea model. Update/View are pure over messages, so the whole
// interaction is testable by feeding tea.KeyMsg values — no terminal required.
type Model struct {
	periods []Period
	pIdx    int
	curView string
	avail   []string
	eng     *pricing.Engine
	rows    []sessionStat
	cursor  int
	mode    mode
	sel     sessionStat // the drilled session
	w, h    int

	// in-explorer plan picker (optional; enabled via WithPlanPicker). setPlan
	// persists the choice and returns recomputed periods so the amortized lens
	// updates live without leaving the TUI.
	plans   []PlanChoice
	today   time.Time
	setPlan func(planID string, start time.Time) []Period
	picker  planPicker
}

// WithPlanPicker enables the in-explorer plan picker (the `p` key): plans is the
// selectable list, today seeds the start-date default, and setPlan persists the
// choice (id + start) and returns the recomputed periods.
func (m Model) WithPlanPicker(plans []PlanChoice, today time.Time, setPlan func(string, time.Time) []Period) Model {
	m.plans = plans
	m.today = today
	m.setPlan = setPlan
	return m
}

type sessionStat struct {
	id        string
	micros    int64 // cost in the active lens (allocated share when amortized)
	apiMicros int64 // api-equivalent (always; the amortized allocation basis)
	hasView   bool  // at least one turn carries the active lens (else cost is "—")
	turns     int
	first     time.Time
	last      time.Time
	repo      string
	provider  string
	byModel   map[string]int64
	evs       []event.AgentEvent
}

// --- color language: a muted, low-saturation palette via AdaptiveColor, so it
// stays legible and easy on the eyes on BOTH light and dark terminal backgrounds.
var (
	stFaint = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "244", Dark: "245"})
	stBold  = lipgloss.NewStyle().Bold(true)
	stBar   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "30", Dark: "73"})
	stSel   = lipgloss.NewStyle().Bold(true).
		Foreground(lipgloss.AdaptiveColor{Light: "232", Dark: "231"}).
		Background(lipgloss.AdaptiveColor{Light: "251", Dark: "238"})
	stRead   = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "25", Dark: "110"})
	stWrite  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "130", Dark: "179"})
	stOutput = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "29", Dark: "108"})
	stInput  = lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "97", Dark: "139"})
)

func New(periods []Period, startIdx int, eng *pricing.Engine) Model {
	if startIdx < 0 || startIdx >= len(periods) {
		startIdx = 0
	}
	m := Model{periods: periods, pIdx: startIdx, eng: eng, mode: modeList, curView: "api_equivalent"}
	m.refresh()
	return m
}

// refresh recomputes the available lenses + rows for the current period, keeping
// the active lens valid. Called on construction and whenever the period changes.
func (m *Model) refresh() {
	m.avail = availableViews(m.events(), m.period().HasPlan)
	m.curView = ensureView(m.curView, m.avail)
	m.rows = m.buildRows()
}

func (m Model) period() Period {
	if m.pIdx < 0 || m.pIdx >= len(m.periods) {
		return Period{}
	}
	return m.periods[m.pIdx]
}

func (m Model) events() []event.AgentEvent { return m.period().Events }

func (m Model) label() string { return m.period().Label }

func (m Model) view() string { return m.curView }

// buildRows groups the current period into session rows for the active lens. For
// the amortized lens it allocates the period's prorated plan fee across sessions
// by api-equivalent share (pricing.Allocate → exact integer split).
func (m Model) buildRows() []sessionStat {
	rows := groupSessions(m.events(), m.curView)
	if m.curView == amortizedView {
		per := m.period()
		basis := make(map[string]int64, len(rows))
		for _, r := range rows {
			basis[r.id] = r.apiMicros
		}
		alloc := pricing.Allocate(event.Money{Micros: per.Amortized, Currency: "USD"}, basis)
		for i := range rows {
			rows[i].micros = alloc[rows[i].id].Micros
			rows[i].hasView = per.HasPlan && rows[i].apiMicros > 0
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].micros > rows[j].micros })
	}
	return rows
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
		switch m.mode {
		case modeReceipt:
			switch msg.String() {
			case "esc", "q", "left", "h", "backspace":
				m.mode = modeList
			}
			return m, nil
		case modePlan:
			pk, finished := m.picker.step(msg)
			m.picker = pk
			if finished {
				m.mode = modeList
				if m.picker.done && m.setPlan != nil { // confirmed → persist + recompute live
					m.periods = m.setPlan(m.picker.chosen, m.picker.start)
					if m.pIdx >= len(m.periods) {
						m.pIdx = 0
					}
					m.curView = amortizedView // jump to the result (clamped to api-equivalent if no plan)
					m.refresh()
					m.cursor = 0
				}
			}
			return m, nil
		default: // modeList
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
					m.refresh()
					m.cursor = 0
				}
			case "right", "l":
				if m.pIdx < len(m.periods)-1 {
					m.pIdx++
					m.refresh()
					m.cursor = 0
				}
			case "v":
				if len(m.avail) > 1 {
					m.curView = nextView(m.avail, m.curView)
					m.rows = m.buildRows()
					m.cursor = 0
				}
			case "p":
				if m.setPlan != nil {
					m.picker = newPlanPicker(m.plans, m.today)
					m.mode = modePlan
				}
			case "enter":
				if len(m.rows) > 0 {
					m.sel = m.rows[m.cursor]
					m.mode = modeReceipt
				}
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	switch m.mode {
	case modeReceipt:
		return m.receiptView()
	case modePlan:
		return m.picker.View()
	default:
		return m.listView()
	}
}

// --- list view -------------------------------------------------------------

func (m Model) listView() string {
	width := m.w
	if width <= 0 {
		width = 100
	}
	view := m.view()
	var b strings.Builder

	b.WriteString(stBold.Render("aispend") + stFaint.Render(fmt.Sprintf(" · %s   ·   %d sessions", m.label(), len(m.rows))) + "\n")
	b.WriteString(m.headerLine(view) + "\n")

	parts := []string{"←/→ period"}
	if len(m.avail) > 1 {
		parts = append(parts, "v view ("+viewLabel(view)+")")
	}
	parts = append(parts, "↑/↓ move", "↵ receipt")
	if m.setPlan != nil {
		parts = append(parts, "p set plan")
	}
	parts = append(parts, "q quit")
	b.WriteString(stFaint.Render(strings.Join(parts, " · ")) + "\n\n")

	if len(m.rows) == 0 {
		b.WriteString(stFaint.Render("  no sessions in "+m.label()) + "\n")
		return b.String()
	}

	barW := width - 72
	if barW < 8 {
		barW = 8
	}
	if barW > 28 {
		barW = 28
	}
	b.WriteString("  " + stFaint.Render(fmt.Sprintf("%9s  %-*s  %-15s  %-18s  %6s  %s",
		"COST", barW, "SHARE", "WHEN (UTC)", "PROJECT", "TURNS", "MODEL")) + "\n")

	maxMicros := m.rows[0].micros
	start, end := m.windowRange(len(m.rows))
	for i := start; i < end; i++ {
		r := m.rows[i]
		cost := money(r.micros)
		if !r.hasView {
			cost = "—"
		}
		bar := spendBar(r.micros, maxMicros, barW)
		meta := fmt.Sprintf("%-15s  %-18s  %6s  %s",
			fmtTime(r.first), trunc(orDash(r.repo), 18), comma(int64(r.turns)), trunc(humanModel(r.dominant()), 12))
		if i == m.cursor {
			b.WriteString(stSel.Render(fmt.Sprintf("▶ %9s  %s  %s", cost, bar, meta)) + "\n")
			continue
		}
		b.WriteString("  " + stBold.Render(fmt.Sprintf("%9s", cost)) + "  " + styleBar(bar) + "  " + stFaint.Render(meta) + "\n")
	}
	if end < len(m.rows) {
		b.WriteString(stFaint.Render(fmt.Sprintf("  … +%d more ↓", len(m.rows)-end)) + "\n")
	}
	return b.String()
}

// headerLine is the second header row: the period total in the active lens, and
// — for amortized — the arbitrage comparison (plan vs api-equivalent + ROI).
func (m Model) headerLine(view string) string {
	if view == amortizedView {
		per := m.period()
		var api int64
		for _, e := range m.events() {
			api += apiMicros(e)
		}
		head := stBold.Render(money(per.Amortized)) + stFaint.Render(" amortized (plan)")
		if api > 0 && per.Amortized > 0 {
			head += stFaint.Render(fmt.Sprintf("   ·   api-equivalent %s   ·   %s ROI", money(api), roiStr(float64(api)/float64(per.Amortized))))
		}
		return head
	}

	var total, apiTotal, without int64
	viewPriced := 0
	for _, e := range m.events() {
		if mic, ok := viewMicros(e, view); ok {
			total += mic
			viewPriced++
		}
		apiTotal += apiMicros(e)
		if w, ok := m.eng.WithoutCache(e.Model, e.Tokens); ok {
			without += w.Micros
		}
	}
	if viewPriced == 0 {
		return stFaint.Render("no " + viewLabel(view) + " cost recorded for these sessions")
	}
	head := stBold.Render(money(total)) + stFaint.Render(" "+viewLabel(view))
	if view == "api_equivalent" && without > apiTotal {
		head += stFaint.Render(fmt.Sprintf("   ·   cache saved %s (%.0f%%)", money(without-apiTotal), pct(without-apiTotal, without)))
	}
	return head
}

func (m Model) windowRange(n int) (int, int) {
	visible := m.h - 7
	if m.h <= 0 || visible >= n {
		return 0, n
	}
	if visible < 1 {
		visible = 1
	}
	start := 0
	if m.cursor >= visible {
		start = m.cursor - visible + 1
	}
	end := start + visible
	if end > n {
		end = n
	}
	return start, end
}

// --- receipt view ----------------------------------------------------------

func (m Model) receiptView() string {
	s := m.sel
	view := m.view()
	if len(s.evs) == 0 {
		return "  (no turns)\n" + stFaint.Render("  esc back · q quit")
	}

	var without int64
	var comp pricing.CostComponents
	priced := 0
	for _, e := range s.evs {
		if c, ok := m.eng.Components(e.Model, e.Tokens); ok {
			comp = addComp(comp, c)
			priced++
		}
		if w, ok := m.eng.WithoutCache(e.Model, e.Tokens); ok {
			without += w.Micros
		}
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s · %s · %s → %s UTC\n",
		stBold.Render(orDash(s.repo)), providerLabel(s.provider), fmtTime(s.first), fmtTime(s.last)))
	b.WriteString(stFaint.Render(fmt.Sprintf("%d %s over %s elapsed", len(s.evs), turnsWord(len(s.evs)), elapsed(s.last.Sub(s.first)))) + "\n\n")

	if s.hasView {
		b.WriteString("  total       " + stBold.Render(money(s.micros)) + stFaint.Render(" "+viewLabel(view)) + "\n")
	} else {
		b.WriteString("  total       " + stBold.Render("not computable") + stFaint.Render(" (no "+viewLabel(view)+" cost)") + "\n")
	}
	if priced > 0 {
		b.WriteString("  composition " + compositionStripe(comp, 28) + "\n")
		b.WriteString("              " + compositionLegend(comp) + "\n")
		if without > 0 {
			api := comp.Total().Micros
			b.WriteString("  arbitrage   " + stFaint.Render(fmt.Sprintf("without cache ≈ %s · saved %.0f%%", money(without), pct(without-api, without))) + "\n")
		}
	}

	b.WriteString("\n  top turns" + stFaint.Render("  (api-equivalent)") + "\n")
	for _, e := range topTurns(s.evs, 5) {
		amt := "—"
		if a := apiMicros(e); a > 0 {
			amt = money(a)
		}
		b.WriteString(fmt.Sprintf("    %8s  %s  %-9s %s\n",
			amt, stFaint.Render(shortID(e.EventID)), humanModel(e.Model), tokenSummary(e.Tokens)))
	}
	b.WriteString("\n" + stFaint.Render("  esc back · q quit"))
	return b.String()
}

// RunModel starts a (possibly picker-enabled) model on the alt screen. The cli
// builds the model via New(...).WithPlanPicker(...) so the in-explorer plan picker
// can persist to config and recompute amortization live.
func RunModel(m Model, out io.Writer) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithOutput(out)).Run()
	return err
}

// --- aggregation + helpers -------------------------------------------------

func groupSessions(events []event.AgentEvent, view string) []sessionStat {
	byID := map[string]*sessionStat{}
	var order []string
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		g := byID[e.SessionID]
		if g == nil {
			g = &sessionStat{id: e.SessionID, provider: e.Provider, first: e.TSStart, last: e.TSStart, byModel: map[string]int64{}}
			byID[e.SessionID] = g
			order = append(order, e.SessionID)
		}
		g.turns++
		g.evs = append(g.evs, e)
		if e.TSStart.Before(g.first) {
			g.first = e.TSStart
		}
		te := e.TSEnd
		if te.IsZero() {
			te = e.TSStart
		}
		if te.After(g.last) {
			g.last = te
		}
		if g.repo == "" {
			g.repo = sessionRepo(e)
		}
		mic, ok := viewMicros(e, view)
		if ok {
			g.hasView = true
		}
		g.micros += mic
		g.apiMicros += apiMicros(e)
		if e.Model != "" {
			g.byModel[e.Model] += mic
		}
	}
	out := make([]sessionStat, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].micros > out[j].micros })
	return out
}

func (s sessionStat) dominant() string {
	best, bestM := "", int64(-1)
	for model, mic := range s.byModel {
		if mic > bestM {
			best, bestM = model, mic
		}
	}
	return best
}

func sessionRepo(e event.AgentEvent) string {
	if e.Repo != "" {
		return e.Repo
	}
	return e.Project
}

func apiMicros(e event.AgentEvent) int64 {
	if m := e.CostViews.APIEquivalent; m != nil {
		return m.Micros
	}
	return 0
}

// viewMicros reads one per-event cost lens; ok is false when that lens is not
// computable for the event (a nil view is never a $0). The amortized lens is not
// a per-event field — it is allocated at the period level in buildRows.
func viewMicros(e event.AgentEvent, view string) (int64, bool) {
	cv := e.CostViews
	var m *event.Money
	switch view {
	case "reported":
		m = cv.Reported
	case "billed":
		m = cv.Billed
	case "marginal":
		m = cv.Marginal
	default: // api_equivalent (and the amortized basis)
		m = cv.APIEquivalent
	}
	if m == nil {
		return 0, false
	}
	return m.Micros, true
}

func viewLabel(view string) string { return strings.ReplaceAll(view, "_", "-") }

// availableViews returns the lenses present in the data, in canonical order, plus
// the amortized lens when a plan is configured — so `v` only offers real views.
func availableViews(events []event.AgentEvent, hasPlan bool) []string {
	has := map[string]bool{}
	for _, e := range events {
		for _, v := range cycleViews {
			if _, ok := viewMicros(e, v); ok {
				has[v] = true
			}
		}
	}
	out := make([]string, 0, len(cycleViews)+1)
	for _, v := range cycleViews {
		if has[v] {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		out = []string{"api_equivalent"}
	}
	if hasPlan {
		out = append(out, amortizedView)
	}
	return out
}

func ensureView(cur string, avail []string) string {
	for _, v := range avail {
		if v == cur {
			return cur
		}
	}
	return avail[0]
}

func nextView(avail []string, cur string) string {
	for i, v := range avail {
		if v == cur {
			return avail[(i+1)%len(avail)]
		}
	}
	return avail[0]
}

func spendBar(micros, max int64, width int) string {
	if max <= 0 || width <= 0 {
		return strings.Repeat("░", maxInt(width, 0))
	}
	fill := int(micros * int64(width) / max)
	if micros > 0 && fill < 1 {
		fill = 1
	}
	if fill > width {
		fill = width
	}
	return strings.Repeat("█", fill) + strings.Repeat("░", width-fill)
}

func styleBar(bar string) string {
	fill := strings.Count(bar, "█")
	total := len([]rune(bar))
	return stBar.Render(strings.Repeat("█", fill)) + stFaint.Render(strings.Repeat("░", total-fill))
}

func compositionStripe(c pricing.CostComponents, width int) string {
	type seg struct {
		micros int64
		style  lipgloss.Style
	}
	segs := []seg{
		{c.CacheWrite.Micros + c.CacheWrite1h.Micros, stWrite},
		{c.CacheRead.Micros, stRead},
		{c.Output.Micros, stOutput},
		{c.Input.Micros, stInput},
	}
	total := c.Total().Micros
	if total <= 0 {
		return strings.Repeat("░", width)
	}
	var out strings.Builder
	used := 0
	for _, s := range segs {
		w := int(s.micros * int64(width) / total)
		if w <= 0 {
			continue
		}
		out.WriteString(s.style.Render(strings.Repeat("█", w)))
		used += w
	}
	if used < width {
		out.WriteString(strings.Repeat(" ", width-used))
	}
	return out.String()
}

func compositionLegend(c pricing.CostComponents) string {
	type cl struct {
		name   string
		micros int64
		style  lipgloss.Style
	}
	cls := []cl{
		{"cache-write", c.CacheWrite.Micros + c.CacheWrite1h.Micros, stWrite},
		{"cache-read", c.CacheRead.Micros, stRead},
		{"output", c.Output.Micros, stOutput},
		{"input", c.Input.Micros, stInput},
	}
	total := c.Total().Micros
	var parts []string
	for _, x := range cls {
		if x.micros <= 0 {
			continue
		}
		parts = append(parts, x.style.Render(x.name)+stFaint.Render(fmt.Sprintf(" %.0f%% %s", pct(x.micros, total), money(x.micros))))
	}
	if len(parts) == 0 {
		return stFaint.Render("(no priced tokens)")
	}
	return strings.Join(parts, stFaint.Render(" · "))
}

func topTurns(evs []event.AgentEvent, n int) []event.AgentEvent {
	cp := make([]event.AgentEvent, len(evs))
	copy(cp, evs)
	sort.SliceStable(cp, func(i, j int) bool { return apiMicros(cp[i]) > apiMicros(cp[j]) })
	if len(cp) > n {
		cp = cp[:n]
	}
	return cp
}

func addComp(a, b pricing.CostComponents) pricing.CostComponents {
	add := func(x, y event.Money) event.Money {
		cur := x.Currency
		if cur == "" {
			cur = y.Currency
		}
		return event.Money{Micros: x.Micros + y.Micros, Currency: cur}
	}
	return pricing.CostComponents{
		Input:        add(a.Input, b.Input),
		Output:       add(a.Output, b.Output),
		CacheRead:    add(a.CacheRead, b.CacheRead),
		CacheWrite:   add(a.CacheWrite, b.CacheWrite),
		CacheWrite1h: add(a.CacheWrite1h, b.CacheWrite1h),
	}
}

func tokenSummary(t event.Tokens) string {
	return fmt.Sprintf("%s in · %s out · %s cache", comma(t.Input), comma(t.Output), comma(t.CacheRead))
}

func money(micros int64) string {
	v := float64(micros) / 1e6
	neg := ""
	if v < 0 {
		neg, v = "-", -v
	}
	whole := int64(v)
	frac := int64((v-float64(whole))*100 + 0.5)
	if frac == 100 {
		whole, frac = whole+1, 0
	}
	return neg + "$" + comma(whole) + "." + fmt.Sprintf("%02d", frac)
}

func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}

func roiStr(roi float64) string {
	if roi >= 10 {
		return fmt.Sprintf("%.0f×", roi)
	}
	return fmt.Sprintf("%.1f×", roi)
}

func humanModel(m string) string {
	if m == "" {
		return "—"
	}
	if m == "<synthetic>" {
		return "other"
	}
	return strings.TrimPrefix(m, "claude-")
}

func providerLabel(p string) string {
	switch p {
	case "claude_code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "cursor":
		return "Cursor"
	case "":
		return "—"
	default:
		return p
	}
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "evt_")
	if r := []rune(id); len(r) > 8 {
		return string(r[:8])
	}
	return id
}

// timeLayout renders a 12-hour wall-clock with an am/pm marker (e.g. "Jun 17
// 7:42am"). fmtTime renders in UTC: AgentSpend is UTC end-to-end (billing data and
// event timestamps are UTC), so every surface shows UTC for clean reconciliation —
// the WHEN column header and the receipt window are labelled "UTC".
const timeLayout = "Jan 02 3:04pm"

func fmtTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func comma(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	pre := len(s) % 3
	if pre == 0 {
		pre = 3
	}
	b.WriteString(s[:pre])
	for i := pre; i < len(s); i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func turnsWord(n int) string {
	if n == 1 {
		return "turn"
	}
	return "turns"
}
