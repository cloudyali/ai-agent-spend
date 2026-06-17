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

// Period is one selectable window: a label plus the events that fall in it. The
// CLI pre-filters so this package needs no period parsing.
type Period struct {
	Label  string
	Events []event.AgentEvent
}

type mode int

const (
	modeList mode = iota
	modeReceipt
)

// metered cost views the explorer can cycle with `v` (effective_allocated is a
// period-level allocation, not a per-session number, so it is intentionally out).
var views = []string{"api_equivalent", "reported", "estimated", "billed", "marginal"}

// Model is the Bubble Tea model. Update/View are pure over messages, so the whole
// interaction is testable by feeding tea.KeyMsg values — no terminal required.
type Model struct {
	periods []Period
	pIdx    int
	vIdx    int
	eng     *pricing.Engine
	rows    []sessionStat
	cursor  int
	mode    mode
	sel     []event.AgentEvent // events of the drilled session
	w, h    int
}

// sessionStat is one list row: a session rolled up under the active view.
type sessionStat struct {
	id       string
	micros   int64
	turns    int
	first    time.Time
	last     time.Time
	repo     string
	provider string
	byModel  map[string]int64
	evs      []event.AgentEvent
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
	m := Model{periods: periods, pIdx: startIdx, eng: eng, mode: modeList}
	m.rows = groupSessions(m.events(), m.view())
	return m
}

func (m Model) events() []event.AgentEvent {
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

func (m Model) view() string {
	if m.vIdx < 0 || m.vIdx >= len(views) {
		return views[0]
	}
	return views[m.vIdx]
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
				m.rows = groupSessions(m.events(), m.view())
				m.cursor = 0
			}
		case "right", "l":
			if m.pIdx < len(m.periods)-1 {
				m.pIdx++
				m.rows = groupSessions(m.events(), m.view())
				m.cursor = 0
			}
		case "v":
			m.vIdx = (m.vIdx + 1) % len(views)
			m.rows = groupSessions(m.events(), m.view())
			m.cursor = 0
		case "enter":
			if len(m.rows) > 0 {
				m.sel = m.rows[m.cursor].evs
				m.mode = modeReceipt
			}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.mode == modeReceipt {
		return m.receiptView()
	}
	return m.listView()
}

// --- list view -------------------------------------------------------------

func (m Model) listView() string {
	width := m.w
	if width <= 0 {
		width = 100
	}
	view := m.view()
	var b strings.Builder

	var total, apiTotal, without int64
	viewPriced := 0
	for _, e := range m.events() {
		if mic, ok := viewMicros(e, view); ok {
			total += mic
			viewPriced++
		}
		if a := e.CostViews.APIEquivalent; a != nil {
			apiTotal += a.Micros
		}
		if w, ok := m.eng.WithoutCache(e.Model, e.Tokens); ok {
			without += w.Micros
		}
	}

	// Header.
	b.WriteString(stBold.Render("aispend") + stFaint.Render(fmt.Sprintf(" · %s   ·   %d sessions", m.label(), len(m.rows))) + "\n")
	if viewPriced == 0 {
		b.WriteString(stFaint.Render("no "+viewLabel(view)+" cost for these sessions — press v to switch view") + "\n")
	} else {
		head := stBold.Render(money(total)) + stFaint.Render(" "+viewLabel(view))
		if view == "api_equivalent" && without > apiTotal {
			head += stFaint.Render(fmt.Sprintf("   ·   cache saved %s (%.0f%%)", money(without-apiTotal), pct(without-apiTotal, without)))
		}
		b.WriteString(head + "\n")
	}
	b.WriteString(stFaint.Render("←/→ period · v view · ↑/↓ move · ↵ open receipt · q quit") + "\n\n")

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

	// Column header — so the columns are self-explanatory (no cryptic "t").
	b.WriteString("  " + stFaint.Render(fmt.Sprintf("%9s  %-*s  %-15s  %-18s  %6s  %s",
		"COST", barW, "SHARE", "WHEN", "PROJECT", "TURNS", "MODEL")) + "\n")

	maxMicros := m.rows[0].micros
	start, end := m.windowRange(len(m.rows))
	for i := start; i < end; i++ {
		r := m.rows[i]
		cost := money(r.micros)
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

// windowRange returns the slice of rows to show so the cursor stays visible,
// accounting for header/footer chrome. With no known height (tests) it shows all.
func (m Model) windowRange(n int) (int, int) {
	visible := m.h - 7 // header(3) + blank + column header + "more" + margin
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
	var b strings.Builder
	evs := m.sel
	view := m.view()
	if len(evs) == 0 {
		return "  (no turns)\n" + stFaint.Render("  esc back · q quit")
	}

	first, last := evs[0].TSStart, evs[0].TSStart
	var total int64
	var without int64
	var comp pricing.CostComponents
	priced, viewPriced := 0, 0
	repo, provider := "", evs[0].Provider
	for _, e := range evs {
		if e.TSStart.Before(first) {
			first = e.TSStart
		}
		te := e.TSEnd
		if te.IsZero() {
			te = e.TSStart
		}
		if te.After(last) {
			last = te
		}
		if repo == "" {
			repo = sessionRepo(e)
		}
		if mic, ok := viewMicros(e, view); ok {
			total += mic
			viewPriced++
		}
		if c, ok := m.eng.Components(e.Model, e.Tokens); ok {
			comp = addComp(comp, c)
			priced++
		}
		if w, ok := m.eng.WithoutCache(e.Model, e.Tokens); ok {
			without += w.Micros
		}
	}

	title := fmt.Sprintf("%s · %s · %s → %s",
		stBold.Render(orDash(repo)), providerLabel(provider),
		fmtTime(first), fmtTime(last))
	b.WriteString(title + "\n")
	b.WriteString(stFaint.Render(fmt.Sprintf("%d %s over %s elapsed", len(evs), turnsWord(len(evs)), elapsed(last.Sub(first)))) + "\n\n")

	if viewPriced == 0 {
		b.WriteString("  total       " + stBold.Render("not computable") + stFaint.Render(" (no "+viewLabel(view)+" cost)") + "\n")
	} else {
		b.WriteString("  total       " + stBold.Render(money(total)) + stFaint.Render(" "+viewLabel(view)) + "\n")
	}
	if priced > 0 {
		b.WriteString("  composition " + compositionStripe(comp, 28) + "\n")
		b.WriteString("              " + compositionLegend(comp) + "\n")
		if without > 0 {
			apiTotal := comp.Total().Micros
			b.WriteString("  arbitrage   " + stFaint.Render(fmt.Sprintf("without cache ≈ %s · saved %.0f%%", money(without), pct(without-apiTotal, without))) + "\n")
		}
	}

	b.WriteString("\n  top turns\n")
	for _, e := range topTurns(evs, 5, view) {
		amt := "—"
		if mic, ok := viewMicros(e, view); ok {
			amt = money(mic)
		}
		b.WriteString(fmt.Sprintf("    %8s  %s  %-9s %s\n",
			amt, stFaint.Render(shortID(e.EventID)), humanModel(e.Model), tokenSummary(e.Tokens)))
	}
	b.WriteString("\n" + stFaint.Render("  esc back · q quit"))
	return b.String()
}

// Run starts the interactive program on the alt screen.
func Run(periods []Period, startIdx int, eng *pricing.Engine, out io.Writer) error {
	p := tea.NewProgram(New(periods, startIdx, eng), tea.WithAltScreen(), tea.WithOutput(out))
	_, err := p.Run()
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
		mic, _ := viewMicros(e, view)
		g.micros += mic
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

// viewMicros reads one cost lens from an event; ok is false when that lens is not
// computable for the event (a nil view is never a $0).
func viewMicros(e event.AgentEvent, view string) (int64, bool) {
	cv := e.CostViews
	var m *event.Money
	switch view {
	case "reported":
		m = cv.Reported
	case "estimated":
		m = cv.Estimated
	case "billed":
		m = cv.Billed
	case "marginal":
		m = cv.Marginal
	default:
		m = cv.APIEquivalent
	}
	if m == nil {
		return 0, false
	}
	return m.Micros, true
}

func viewLabel(view string) string { return strings.ReplaceAll(view, "_", "-") }

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

// styleBar colors a "██░░" proportion bar: the filled run in the calm accent, the
// empty track in grey — so the eye tracks length, not glare.
func styleBar(bar string) string {
	fill := strings.Count(bar, "█")
	total := len([]rune(bar))
	return stBar.Render(strings.Repeat("█", fill)) + stFaint.Render(strings.Repeat("░", total-fill))
}

// compositionStripe is a single width-N bar split into colored segments by token
// class proportion (the "composition stripe" — cache dominance visible at a glance).
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

func topTurns(evs []event.AgentEvent, n int, view string) []event.AgentEvent {
	cp := make([]event.AgentEvent, len(evs))
	copy(cp, evs)
	sort.SliceStable(cp, func(i, j int) bool {
		mi, _ := viewMicros(cp[i], view)
		mj, _ := viewMicros(cp[j], view)
		return mi > mj
	})
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
// 7:42am"); fmtTime converts to the viewer's LOCAL zone first, since the stored
// timestamps come from logs in UTC and "when did I work on this" means local.
const timeLayout = "Jan 02 3:04pm"

func fmtTime(t time.Time) string { return t.Local().Format(timeLayout) }

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
