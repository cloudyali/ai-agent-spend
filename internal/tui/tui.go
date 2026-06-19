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

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
	"github.com/agentspend/ai-agent-spend/internal/quota"
)

// Period is one selectable window: a label, the events that fall in it, and the
// period's prorated subscription fee (Amortized) when a plan is configured — the
// CLI computes that allocation basis so this package needs no plan/period parsing.
type Period struct {
	Label     string
	Events    []event.AgentEvent
	Amortized map[string]int64 // prorated plan fee per provider (micros); empty if no plan
	HasPlan   bool             // any provider has a subscription plan (enables the amortized lens)
	Since     time.Time        // window bounds; the spend bar buckets across these so its
	Until     time.Time        // unit tracks the selected period. Both zero for unbounded "all".
}

type mode int

const (
	modeList mode = iota
	modeReceipt
	modeFile    // a single file's receipt: the turns (evidence) that touched it
	modeExplain // a single turn's evidence + cost breakdown (the in-TUI `explain`)
	modePlan    // the in-explorer plan picker (set the subscription without leaving the TUI)
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

	// Drill state, frozen when a view is opened so Update's bounds and View's window
	// never drift. The receipt is one continuous cursor (recCursor) over the files
	// heatmap followed by the top turns: ↑/↓ flows from the files straight into the
	// turns (no mode switch), and ↵ opens the highlighted row — a file (→ file view)
	// or a turn (→ explain). recCursor in [0,len(selFiles)) selects a file; the
	// remainder indexes selTurns. The file view lists the file's own turns
	// (selFileTurns), each of which also opens explain. selTurn is the turn whose
	// evidence the explain view renders; explainBack is where ↵-back goes.
	selFiles       []fileRow
	selFile        fileRow
	selTurns       []event.AgentEvent // the receipt's top turns
	recCursor      int                // unified cursor: [0,len(selFiles)) = a file; the rest = a top turn
	selFileTurns   []fileTurn         // the file view's turns (the open file's)
	fileTurnCursor int
	selTurn        event.AgentEvent // the turn the explain view renders
	explainBack    mode             // mode to return to from explain (receipt or file)

	// promptResolver (optional; enabled via WithPromptResolver) re-reads the human
	// prompt behind a turn from the original session log on demand; nil hides the
	// prompt section in the explain view.
	promptResolver func(event.AgentEvent) (string, bool)
	// nameResolver (optional; WithNameResolver) recovers a human session title from the
	// original log; resolved once on drill-in into the receipt, nil → no title line.
	nameResolver func(event.AgentEvent) (string, bool)
	selName      string
	selNameOK    bool
	// The resolved prompt for the open turn, rendered in a scrollable viewport so a
	// long prompt scrolls instead of burying the evidence above it. Resolved once on
	// drill-in (openExplain), not re-read each render.
	promptText  string
	promptOK    bool
	promptLines int // wrapped line count, for the scrollbar
	promptVP    viewport.Model

	// in-explorer plan picker (optional; enabled via WithPlanPicker). setPlan
	// persists the choice (for the chosen provider) and returns recomputed periods
	// so the amortized lens updates live without leaving the TUI.
	providers []ProviderChoice
	plans     []PlanChoice
	today     time.Time
	setPlan   func(provider, planID string, start time.Time) []Period
	picker    planPicker

	// now is the reference clock for the day-grouped session list: it powers the
	// Today/Yesterday headers and the live badge. Zero when unset (e.g. in tests),
	// which renders absolute day labels and no live session — keeping output
	// deterministic. Set via WithNow from the cli.
	now time.Time

	// watch-mode (optional; enabled via WithWatch): every watchInt the model calls
	// reload for fresh periods and nowFn to advance the clock, refreshing in place so
	// an ongoing session grows and liveness decays without leaving the list.
	watchInt time.Duration
	nowFn    func() time.Time
	reload   func() []Period

	// quota (optional; enabled via WithQuota): the provider's plan-limit windows read
	// from its local usage snapshot — a reported point-in-time gauge, separate from the
	// ledger. quotaFn re-reads on each refresh so a watch tick keeps it current.
	quotaFn func() []quota.Sample
	quota   []quota.Sample
}

// WithPlanPicker enables the in-explorer plan picker (the `p` key): providers is
// the set whose plans can be set (one per provider), plans is the catalog, today
// seeds the start-date default, and setPlan persists the choice (provider + id +
// start) and returns the recomputed periods.
func (m Model) WithPlanPicker(providers []ProviderChoice, plans []PlanChoice, today time.Time, setPlan func(provider, planID string, start time.Time) []Period) Model {
	m.providers = providers
	m.plans = plans
	m.today = today
	m.setPlan = setPlan
	return m
}

// WithQuota enables the plan-limit gauge in the list header: fn returns the freshest
// quota samples (the cli reads them from the provider's local usage snapshot). They
// re-read on every refresh, so a watch tick keeps them current; absent or
// expired-past-reset samples simply don't render. It's a reported gauge, separate
// from the ledger.
func (m Model) WithQuota(fn func() []quota.Sample) Model {
	m.quotaFn = fn
	m.refresh()
	return m
}

// WithNow sets the reference clock for the day-grouped session list — the relative
// day headers (Today/Yesterday) and the live badge. Without it the list groups by
// absolute date and shows no live session. The cli passes the scan's now; tests pin it.
func (m Model) WithNow(now time.Time) Model {
	m.now = now
	m.refresh() // rebuild rows so day-grouping + liveness use the new clock
	return m
}

// WithWatch turns the explorer into a live view: every interval it calls reload to
// pull fresh periods (a re-scan + rebuild, in the cli) and nowFn to advance the
// clock, so an ongoing session grows and liveness decays in place. nowFn may be nil
// (clock not advanced); a zero interval disables ticking. Wiring lives in the cli so
// this package stays filesystem-free and unit-testable.
func (m Model) WithWatch(interval time.Duration, nowFn func() time.Time, reload func() []Period) Model {
	m.watchInt = interval
	m.nowFn = nowFn
	m.reload = reload
	return m
}

// WithNameResolver injects an optional, lazy session-title re-reader: given a
// representative turn it returns the session's human name (Claude Code summary, else
// first prompt), resolved once on drill-in. nil hides the receipt's title line.
// Wiring lives in the cli, keeping this package filesystem-free and unit-testable.
func (m Model) WithNameResolver(fn func(event.AgentEvent) (string, bool)) Model {
	m.nameResolver = fn
	return m
}

// WithPromptResolver injects an optional, lazy prompt re-reader: given a turn it
// returns the human prompt behind it (re-read from the original session log) and
// whether one was found. Wiring lives in the cli, which knows the source paths, so
// this package stays filesystem-free and unit-testable.
func (m Model) WithPromptResolver(fn func(event.AgentEvent) (string, bool)) Model {
	m.promptResolver = fn
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
	subagents map[string]bool // distinct subagent worker ids rolled up under this session
	evs       []event.AgentEvent
}

// subCount is how many distinct Claude Code subagents rolled up under this session.
func (s sessionStat) subCount() int { return len(s.subagents) }

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
	if m.quotaFn != nil {
		m.quota = m.quotaFn() // re-read the plan-limit snapshot so a watch tick keeps it fresh
	}
}

func (m Model) period() Period {
	if m.pIdx < 0 || m.pIdx >= len(m.periods) {
		return Period{}
	}
	return m.periods[m.pIdx]
}

func (m Model) events() []event.AgentEvent { return m.period().Events }

func (m Model) label() string { return m.period().Label }

// periodDates renders the active window's date span in the LOCAL display zone — a
// single date ("Jun 19") or a range ("Jun 15–Jun 21"). The bounds are UTC instants;
// this only chooses the display zone. Callers guard the unbounded "all" window, so
// both bounds are non-zero here.
func periodDates(since, until time.Time) string {
	s, u := since.In(time.Local), until.In(time.Local)
	if s.Format("20060102") == u.Format("20060102") {
		return s.Format("Jan 2")
	}
	return s.Format("Jan 2") + "–" + u.Format("Jan 2")
}

// periodSpanLabel is the active window's date span tagged with the local zone
// abbreviation (e.g. "Jun 15–Jun 21 IST"), or "" when the window is unbounded
// ("all"), where the label alone conveys the span.
func (m Model) periodSpanLabel() string {
	p := m.period()
	if p.Since.IsZero() && p.Until.IsZero() {
		return ""
	}
	return periodDates(p.Since, p.Until) + " " + p.Since.In(time.Local).Format("MST")
}

func (m Model) view() string { return m.curView }

// buildRows groups the current period into session rows for the active lens. For
// the amortized lens it allocates the period's prorated plan fee across sessions
// by api-equivalent share (pricing.Allocate → exact integer split).
func (m Model) buildRows() []sessionStat {
	rows := groupSessions(m.events(), m.curView)
	if m.curView == amortizedView {
		per := m.period()
		// Allocate each provider's prorated fee across ONLY its own sessions, by
		// api-equivalent share — so a codex session never absorbs claude's plan fee.
		basisByProv := map[string]map[string]int64{}
		for _, r := range rows {
			if basisByProv[r.provider] == nil {
				basisByProv[r.provider] = map[string]int64{}
			}
			basisByProv[r.provider][r.id] = r.apiMicros
		}
		alloc := map[string]int64{}
		for prov, basis := range basisByProv {
			if fee := per.Amortized[prov]; fee > 0 {
				for sid, m := range pricing.Allocate(event.Money{Micros: fee, Currency: "USD"}, basis) {
					alloc[sid] = m.Micros
				}
			}
		}
		for i := range rows {
			rows[i].micros = alloc[rows[i].id]
			rows[i].hasView = per.Amortized[rows[i].provider] > 0 // covered by a plan
		}
	}
	// Day-grouped ordering: most-recent day first, the live session leading its day,
	// then priciest-first. A single-day period reduces to the legacy cost ordering, so
	// existing single-day behavior (and its tests) is unchanged.
	return orderForDayList(rows, m.now, time.Local, liveWindow)
}

// tickMsg is the watch-mode heartbeat; each one reloads fresh data and re-arms.
type tickMsg time.Time

// tickCmd schedules the next watch tick, or nil when watch is off.
func (m Model) tickCmd() tea.Cmd {
	if m.watchInt <= 0 {
		return nil
	}
	return tea.Tick(m.watchInt, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// applyReload advances the clock and pulls fresh periods (keeping the selected window
// by label), then rebuilds rows. Total by design: an empty reload keeps the current
// data, and the cursor is clamped so a shrunk list can never be indexed past its end.
func (m *Model) applyReload() {
	if m.nowFn != nil {
		m.now = m.nowFn()
	}
	if m.reload != nil {
		if ps := m.reload(); len(ps) > 0 {
			label := m.period().Label
			m.periods = ps
			m.pIdx = 0
			for i, p := range ps {
				if p.Label == label {
					m.pIdx = i
					break
				}
			}
		}
	}
	m.refresh()
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m Model) Init() tea.Cmd { return m.tickCmd() }

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.buildPromptViewport() // re-fit the prompt box to the new size
	case tickMsg:
		m.applyReload() // watch heartbeat: refresh in place, then re-arm
		return m, m.tickCmd()
	case tea.KeyMsg:
		// q and ctrl+c quit from anywhere — including drill-downs — so q is never
		// "back" (esc/←/h/backspace are). The plan picker owns its keys, so q is not
		// intercepted while it is open.
		if s := msg.String(); s == "ctrl+c" || (s == "q" && m.mode != modePlan) {
			return m, tea.Quit
		}
		switch m.mode {
		case modeReceipt:
			switch msg.String() {
			case "esc", "left", "h", "backspace":
				m.mode = modeList
			case "tab":
				// Accelerator over the unified cursor: jump between the top of the files
				// and the first top turn (toggle), so a long heatmap isn't in the way of
				// the turns. No-op when one section is empty — never lands on a gap.
				if nf := len(m.selFiles); nf > 0 && len(m.selTurns) > 0 {
					if m.recCursor < nf {
						m.recCursor = nf // → first top turn
					} else {
						m.recCursor = 0 // → priciest file
					}
				}
			case "up", "k":
				if m.recCursor > 0 {
					m.recCursor--
				}
			case "down", "j":
				if m.recCursor < m.receiptRows()-1 {
					m.recCursor++
				}
			case "enter":
				// The cursor walks the files first, then the turns; ↵ opens whichever
				// row it is on — a file (→ file view) or a turn (→ evidence).
				if nf := len(m.selFiles); m.recCursor < nf {
					m.selFile = m.selFiles[m.recCursor]
					m.selFileTurns = fileTurns(m.sel.evs, m.selFile.path)
					m.fileTurnCursor = 0
					m.mode = modeFile
				} else if ti := m.recCursor - nf; ti >= 0 && ti < len(m.selTurns) {
					m.openExplain(m.selTurns[ti], modeReceipt)
				}
			}
			return m, nil
		case modeFile:
			switch msg.String() {
			case "esc", "left", "h", "backspace":
				m.mode = modeReceipt // back to the receipt, cursors preserved
			case "up", "k":
				if m.fileTurnCursor > 0 {
					m.fileTurnCursor--
				}
			case "down", "j":
				if m.fileTurnCursor < len(m.selFileTurns)-1 {
					m.fileTurnCursor++
				}
			case "enter":
				if len(m.selFileTurns) > 0 {
					m.openExplain(m.selFileTurns[m.fileTurnCursor].ev, modeFile)
				}
			}
			return m, nil
		case modeExplain:
			switch msg.String() {
			case "esc", "left", "h", "backspace":
				m.mode = m.explainBack // back to the receipt or the file view
				return m, nil
			}
			if m.promptOK { // everything else scrolls the prompt box
				var cmd tea.Cmd
				m.promptVP, cmd = m.promptVP.Update(msg)
				return m, cmd
			}
			return m, nil
		case modePlan:
			pk, finished := m.picker.step(msg)
			m.picker = pk
			if finished {
				m.mode = modeList
				if m.picker.done && m.setPlan != nil { // confirmed → persist + recompute live
					m.periods = m.setPlan(m.picker.provider, m.picker.chosen, m.picker.start)
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
					m.picker = newPlanPicker(m.providers, m.plans, m.today)
					m.mode = modePlan
				}
			case "enter":
				if len(m.rows) > 0 {
					m.sel = m.rows[m.cursor]
					m.selName, m.selNameOK = "", false
					if m.nameResolver != nil && len(m.sel.evs) > 0 {
						m.selName, m.selNameOK = m.nameResolver(m.sel.evs[0]) // resolve the title once, on drill-in
					}
					m.selFiles = receiptFiles(m.sel.evs) // freeze the receipt's lists
					m.selTurns = topTurns(m.sel.evs, 5)
					m.recCursor = 0 // start on the priciest file (or the top turn when fileless)
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
	case modeFile:
		return m.fileView()
	case modeExplain:
		return m.explainView()
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

	hdr := " · " + m.label()
	if d := m.periodSpanLabel(); d != "" {
		hdr += " · " + d
	}
	hdr += fmt.Sprintf("   ·   %d sessions", len(m.rows))
	b.WriteString(stBold.Render("aispend") + stFaint.Render(hdr) + "\n")
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
	b.WriteString(stFaint.Render(strings.Join(parts, " · ")) + "\n")
	if anyLive(m.rows, m.now, liveWindow) { // a special legend, only when something is live
		b.WriteString(stOutput.Render("  ●") + stFaint.Render(" "+liveLegendText(liveWindow)) + "\n")
	}
	b.WriteString("\n")

	// Plan-limit gauges (the weekly/5h wall) — a reported snapshot, shown above the
	// sessions and before the empty check so the wall is visible even on a quiet day,
	// with an explicit "unknown" line when Claude activity has no local snapshot.
	if ql := m.quotaLines(); len(ql) > 0 {
		for _, line := range ql {
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(m.rows) == 0 {
		b.WriteString(stFaint.Render("  no sessions in "+m.label()) + "\n")
		return b.String()
	}

	if db := durationBar(m.events(), m.period().Since, m.period().Until, time.Local); db != "" { // spend-over-time across the period
		b.WriteString(db + "\n\n")
	}

	barW := width - 72
	if barW < 8 {
		barW = 8
	}
	if barW > 28 {
		barW = 28
	}
	b.WriteString("  " + stFaint.Render(fmt.Sprintf("%9s  %-*s  %-15s  %-18s  %6s  %s",
		"COST", barW, "SHARE", "WHEN · SPAN", "PROJECT", "TURNS", "MODEL")) + "\n")

	var maxMicros int64
	daySubtotal := map[string]int64{}
	for _, r := range m.rows {
		if r.micros > maxMicros {
			maxMicros = r.micros
		}
		daySubtotal[dayKey(r.last, time.Local)] += r.micros
	}
	start, end := m.windowRange(len(m.rows))
	prevDay := ""
	for i := start; i < end; i++ {
		r := m.rows[i]
		if dk := dayKey(r.last, time.Local); dk != prevDay {
			prevDay = dk
			b.WriteString("  " + stBold.Render(dayLabel(r.last, m.now, time.Local)) + stFaint.Render(" · "+money(daySubtotal[dk])) + "\n")
		}
		cost := money(r.micros)
		if !r.hasView {
			cost = "—"
		}
		bar := spendBar(r.micros, maxMicros, barW)
		live := isLive(r.last, m.now, liveWindow)
		when := clockTime(r.first, time.Local) + " " + elapsed(sessionSpan(r))
		if live {
			when = "live " + elapsed(sessionSpan(r))
		}
		model := trunc(humanModel(r.dominant()), 12)
		if n := r.subCount(); n > 0 {
			model += fmt.Sprintf(" ⋮%d sub", n) // subagents rolled up under this session
		}
		meta := fmt.Sprintf("%-15s  %-18s  %6s  %s",
			when, trunc(orDash(r.repo), 18), comma(int64(r.turns)), model)
		switch {
		case i == m.cursor:
			b.WriteString(stSel.Render(fmt.Sprintf("▶ %9s  %s  %s", cost, bar, meta)) + "\n")
		case live:
			b.WriteString(stOutput.Render("● ") + stBold.Render(fmt.Sprintf("%9s", cost)) + "  " + styleBar(bar) + "  " + stFaint.Render(meta) + "\n")
		default:
			b.WriteString("  " + stBold.Render(fmt.Sprintf("%9s", cost)) + "  " + styleBar(bar) + "  " + stFaint.Render(meta) + "\n")
		}
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
		var total int64
		for _, v := range per.Amortized {
			total += v
		}
		var api int64
		for _, e := range m.events() {
			api += apiMicros(e)
		}
		head := stBold.Render(money(total)) + stFaint.Render(" amortized (plan)")
		if api > 0 && total > 0 {
			head += stFaint.Render(fmt.Sprintf("   ·   api-equivalent %s   ·   %s ROI", money(api), roiStr(float64(api)/float64(total))))
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
	if durationBar(m.events(), m.period().Since, m.period().Until, time.Local) != "" {
		visible -= 2 // the spend bar + its blank line also sit above the rows
	}
	if anyLive(m.rows, m.now, liveWindow) {
		visible-- // the live legend line sits above the rows too
	}
	if n := len(m.quotaLines()); n > 0 {
		visible -= n + 1 // plan-limit gauge lines + their separating blank
	}
	// Day-group headers cost a line each: one day needs a single header, but several
	// days can put a header before nearly every row, so halve the row budget then —
	// never overflowing the viewport (TestModel_ListFitsHeightWithDurationBar guards it).
	if distinctSessionDays(m.rows, time.Local) > 1 {
		visible /= 2
	} else {
		visible--
	}
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
	b.WriteString(m.breadcrumb() + "\n\n")
	if m.selNameOK { // the session's human title leads the receipt when we can recover it
		b.WriteString(stBold.Render(trunc(m.selName, 64)) + "\n")
	}
	b.WriteString(fmt.Sprintf("%s · %s · %s → %s UTC\n",
		stBold.Render(orDash(s.repo)), providerLabel(s.provider), fmtTime(s.first), fmtTime(s.last)))
	subNote := ""
	if n := s.subCount(); n > 0 {
		subNote = fmt.Sprintf(" · ⋮%d sub", n)
	}
	b.WriteString(stFaint.Render(fmt.Sprintf("%d %s over %s elapsed%s", len(s.evs), turnsWord(len(s.evs)), elapsed(s.last.Sub(s.first)), subNote)) + "\n")
	if vcs := sessionVCSLine(s.evs); vcs != "" {
		b.WriteString("  branch      " + stFaint.Render(vcs) + "\n")
	}
	b.WriteString("\n")

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

	// The tail (top turns + key hints) is built first so the file heatmap that sits
	// between the header above and the tail below can be sized to the room that's left.
	var tail strings.Builder
	tail.WriteString("\n  top turns" + stFaint.Render("  (api-equivalent)") + "\n")
	tail.WriteString(turnTableHeader())
	selTurn := m.recCursor - len(m.selFiles) // <0 while the cursor is still in the files
	for i, e := range m.selTurns {
		amt := "—"
		if a := apiMicros(e); a > 0 {
			amt = money(a)
		}
		tail.WriteString(turnRow(amt, e, i == selTurn) + "\n")
	}
	tail.WriteString("\n" + stFaint.Render("  "+m.receiptHint()))

	if n := len(m.selFiles); n > 0 {
		var max int64
		for _, fr := range m.selFiles {
			if fr.cost > max {
				max = fr.cost
			}
		}
		// Size the window to the terminal height (show all when size is unknown),
		// leaving room for the header above and the tail below.
		budget := n
		if m.h > 0 {
			avail := m.h - strings.Count(b.String(), "\n") - strings.Count(tail.String(), "\n") - 2 // -2: the blank line + "files" header
			if avail < 1 {
				avail = 1
			}
			if n > avail {
				budget = avail - 1 // reserve a line for the more-indicator
				if budget < 1 {
					budget = 1
				}
			}
		}
		// Keep at least the five priciest files (or all, when fewer) visible — the
		// heatmap is the receipt's signal, so it never collapses to a row or two on a
		// short terminal, even if that pushes the view past the window height.
		floor := 5
		if floor > n {
			floor = n
		}
		if budget < floor {
			budget = floor
		}
		// The window follows the cursor while it is in the files; once the cursor
		// crosses into the turns it clamps to the last file, keeping the bottom of the
		// heatmap (the rows just above the turns) in view.
		fileCur := m.recCursor
		if fileCur >= n {
			fileCur = n - 1
		}
		start, end := fileWindow(n, fileCur, budget)
		b.WriteString("\n  files       " + stFaint.Render("cost · churn") + "\n")
		for i := start; i < end; i++ {
			b.WriteString(fileRowLine(m.selFiles[i], max, i == m.recCursor) + "\n")
		}
		if start > 0 || end < n {
			var ind []string
			if start > 0 {
				ind = append(ind, fmt.Sprintf("↑%d", start))
			}
			if end < n {
				ind = append(ind, fmt.Sprintf("↓%d", n-end))
			}
			b.WriteString(stFaint.Render("    "+strings.Join(ind, " ")+" more") + "\n")
		}
	}

	b.WriteString(tail.String())
	return b.String()
}

// fileView is one file's receipt: the path, its api-equivalent total (with git
// churn where present), and the turns that touched it as evidence — the same
// per-file cost split shown in the heatmap, so the turns reconcile to the total.
func (m Model) fileView() string {
	fr := m.selFile
	turns := m.selFileTurns
	var b strings.Builder
	b.WriteString(m.breadcrumb() + "\n\n")
	b.WriteString(stBold.Render(fr.path) + "\n")
	b.WriteString(stFaint.Render(fmt.Sprintf("in %s · %d %s", orDash(m.sel.repo), len(turns), turnsWord(len(turns)))) + "\n\n")
	line := "  total       " + stBold.Render(money(fr.cost)) + stFaint.Render(" api-equivalent")
	if fr.hasChurn {
		line += stFaint.Render(fmt.Sprintf(" · +%d/-%d", fr.churn.Added, fr.churn.Removed))
	}
	b.WriteString(line + "\n")
	b.WriteString("\n  turns" + stFaint.Render("  (this file's share, priciest first)") + "\n")
	b.WriteString(turnTableHeader())

	// A cursor-navigable, height-windowed list (the long tail of cheap turns is the
	// least informative); ↵ on a row opens that turn's explain.
	n := len(turns)
	budget := n
	if m.h > 0 {
		avail := m.h - 11 // chrome incl. breadcrumb, column header + footer
		if avail < 1 {
			avail = 1
		}
		if n > avail {
			budget = avail - 1 // reserve a line for the more-indicator
			if budget < 1 {
				budget = 1
			}
		}
	}
	start, end := fileWindow(n, m.fileTurnCursor, budget)
	for i := start; i < end; i++ {
		ft := turns[i]
		amt := "—"
		if ft.share > 0 {
			amt = money(ft.share)
		}
		b.WriteString(turnRow(amt, ft.ev, i == m.fileTurnCursor) + "\n")
	}
	if start > 0 || end < n {
		var ind []string
		if start > 0 {
			ind = append(ind, fmt.Sprintf("↑%d", start))
		}
		if end < n {
			ind = append(ind, fmt.Sprintf("↓%d", n-end))
		}
		b.WriteString(stFaint.Render("    "+strings.Join(ind, " ")+" more") + "\n")
	}
	b.WriteString("\n" + stFaint.Render("  ↑/↓ turns · ↵ explain · esc back · q quit"))
	return b.String()
}

// explainView is one turn's cost breakdown — the in-TUI `explain`. It answers "why
// is this number what it is" with the per-token-class composition (the same color
// language as the receipt) and, when resolvable, the human prompt behind the turn.
func (m Model) explainView() string {
	e := m.selTurn
	var b strings.Builder
	b.WriteString(m.breadcrumb() + "\n\n")
	cost := "—"
	if a := apiMicros(e); a > 0 {
		cost = money(a)
	}
	b.WriteString(stBold.Render(humanModel(e.Model)) + "  " + stBold.Render(cost) + stFaint.Render(" api-equivalent") + "\n")
	b.WriteString(stFaint.Render(fmt.Sprintf("  %s · %s", providerLabel(e.Provider), tokenSummary(e.Tokens))) + "\n\n")

	if c, ok := m.eng.Components(e.Model, e.Tokens); ok {
		b.WriteString("  composition " + compositionStripe(c, 28) + "\n")
		b.WriteString("              " + compositionLegend(c) + "\n")
	}
	if m.promptOK {
		b.WriteString("\n  " + stBold.Render("prompt") + "\n")
		bar := promptScrollbar(m.promptVP)
		for i, ln := range strings.Split(m.promptVP.View(), "\n") {
			cell := stFaint.Render("│")
			if i < len(bar) {
				cell = bar[i]
			}
			b.WriteString("  " + cell + " " + ln + "\n")
		}
		if m.promptLines > m.promptVP.Height { // scrolls — advertise it
			b.WriteString(stFaint.Render(fmt.Sprintf("  ↑/↓ scroll · %d lines", m.promptLines)) + "\n")
		}
	} else if m.promptResolver != nil {
		b.WriteString("\n  prompt      " + stFaint.Render("(unavailable — session log not found)") + "\n")
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
		if e.SubagentID != "" {
			if g.subagents == nil {
				g.subagents = map[string]bool{}
			}
			g.subagents[e.SubagentID] = true
		}
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

// turnTableHeader is the faint column header shared by the receipt's top-turns and
// the file view: COST · WHEN (the turn's wall-clock, UTC) · MODEL · TOKENS, aligned
// to the turn-row format below it.
func turnTableHeader() string {
	return stFaint.Render(fmt.Sprintf("    %8s  %-14s  %-9s %s", "COST", "WHEN", "MODEL", "TOKENS")) + "\n"
}

// turnRow renders one turn line — cost, turn time (UTC), model, token summary —
// shared by the receipt's top-turns and the file view. The selected row (when its
// section has focus) is highlighted like the session list's cursor row.
func turnRow(amt string, e event.AgentEvent, selected bool) string {
	when := fmtTime(e.TSStart)
	if selected {
		return "  " + stSel.Render(fmt.Sprintf("▶ %8s  %-14s  %-9s %s",
			amt, when, humanModel(e.Model), tokenSummary(e.Tokens)))
	}
	return fmt.Sprintf("    %8s  %s  %-9s %s",
		amt, stFaint.Render(fmt.Sprintf("%-14s", when)), humanModel(e.Model), tokenSummary(e.Tokens))
}

// openExplain drills into one turn's evidence view, remembering where ↵-back returns.
func (m *Model) openExplain(e event.AgentEvent, back mode) {
	m.selTurn = e
	m.explainBack = back
	m.mode = modeExplain
	m.promptText, m.promptOK = "", false
	if m.promptResolver != nil {
		if prompt, ok := m.promptResolver(e); ok {
			m.promptText, m.promptOK = prompt, true
		}
	}
	m.buildPromptViewport()
}

// buildPromptViewport (re)wraps the open turn's prompt to the current width and sizes
// a viewport to the room left below the evidence, so the prompt scrolls in place. It
// is a no-op (empty box) when there is no prompt.
func (m *Model) buildPromptViewport() {
	if !m.promptOK {
		m.promptVP = viewport.Model{}
		m.promptLines = 0
		return
	}
	w := m.w - 4
	if w < 20 {
		w = 120 // unknown/tiny width (e.g. off a TTY in tests): a sane default
	}
	wrapped := wrapText(m.promptText, w)
	m.promptLines = len(wrapped)
	h := m.promptBoxHeight()
	if h > len(wrapped) {
		h = len(wrapped)
	}
	if h < 1 {
		h = 1
	}
	vp := viewport.New(w, h)
	vp.SetContent(strings.Join(wrapped, "\n"))
	m.promptVP = vp
}

// promptBoxHeight is how many lines the scrollable prompt box gets: the terminal
// height minus the evidence chrome and footer. A non-positive height (size not yet
// known, e.g. in tests) returns a large value so the whole prompt shows unscrolled.
func (m Model) promptBoxHeight() int {
	const chrome = 14 // breadcrumb, cost, provider, composition, header, hint, footer
	if m.h <= 0 {
		return 1 << 20
	}
	if h := m.h - chrome; h > 2 {
		return h
	}
	return 3
}

// promptScrollbar returns one gutter cell per visible prompt line: a faint track when
// the prompt fits, or a track with a proportional thumb when it scrolls — a hand-drawn
// scrollbar over the viewport's scroll state that doubles as the prompt's quote gutter.
func promptScrollbar(vp viewport.Model) []string {
	h := vp.Height
	if h < 1 {
		h = 1
	}
	cells := make([]string, h)
	total := vp.TotalLineCount()
	if total <= h {
		for i := range cells {
			cells[i] = stFaint.Render("│")
		}
		return cells
	}
	thumb := h * h / total
	if thumb < 1 {
		thumb = 1
	}
	start := 0
	if denom := total - h; denom > 0 {
		start = vp.YOffset * (h - thumb) / denom
	}
	if lim := h - thumb; start > lim {
		start = lim
	}
	for i := range cells {
		if i >= start && i < start+thumb {
			cells[i] = stBold.Render("┃")
		} else {
			cells[i] = stFaint.Render("│")
		}
	}
	return cells
}

// receiptRows is the number of rows the unified receipt cursor walks: every file in
// the heatmap followed by every top turn.
func (m Model) receiptRows() int {
	return len(m.selFiles) + len(m.selTurns)
}

// receiptHint is the receipt's key legend. One continuous ↑/↓ cursor runs the files
// and the top turns; tab is the accelerator that jumps between the two, shown only
// when both sections are present to switch between. ↵ opens whatever is selected.
func (m Model) receiptHint() string {
	var parts []string
	if len(m.selFiles) > 0 && len(m.selTurns) > 0 {
		parts = append(parts, "tab files/turns")
	}
	parts = append(parts, "↑/↓ move", "↵ open", "esc back", "q quit")
	return strings.Join(parts, " · ")
}

// breadcrumb shows the drill path from the period down to the current level, so the
// context (which period, session, file) stays visible after drilling in — the period
// shown on the list is never "lost" deeper in. The leaf segment is emphasized.
func (m Model) breadcrumb() string {
	period := m.label()
	if d := m.periodSpanLabel(); d != "" {
		period += " · " + d
	}
	segs := []string{period, orDash(m.sel.repo)}
	switch m.mode {
	case modeFile:
		segs = append(segs, m.selFile.path)
	case modeExplain:
		when := fmtTime(m.selTurn.TSStart)
		if m.explainBack == modeFile {
			segs = append(segs, m.selFile.path, when)
		} else {
			segs = append(segs, "turn "+when)
		}
	}
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteString(stFaint.Render(" › "))
		}
		// The period root and the current leaf are emphasized; the middle of the path
		// stays faint.
		if i == 0 || i == len(segs)-1 {
			b.WriteString(stBold.Render(s))
		} else {
			b.WriteString(stFaint.Render(s))
		}
	}
	return b.String()
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

var blockRamp = []rune{' ', '▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders one block glyph per value, scaled to the series max, so the
// peak bucket towers over the rest. Zero/all-zero cells are blank; any nonzero
// value shows at least the lowest block.
func sparkline(vals []int64) string {
	var max int64
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	var b strings.Builder
	for _, v := range vals {
		if max <= 0 || v <= 0 {
			b.WriteRune(blockRamp[0])
			continue
		}
		idx := int(v * 8 / max)
		if idx < 1 {
			idx = 1
		}
		if idx > 8 {
			idx = 8
		}
		b.WriteRune(blockRamp[idx])
	}
	return b.String()
}

// durationBar renders a spend-over-time bar for the current period: api-equivalent
// spend bucketed across the events' span by an adaptive calendar unit, with the
// peak bucket labelled. Empty when there's no priced spend.
func durationBar(events []event.AgentEvent, since, until time.Time, loc *time.Location) string {
	vals, unit, start := bucketSpend(events, since, until, loc)
	if len(vals) <= 1 { // a single bucket isn't a chart worth showing
		return ""
	}
	peakIdx, peakVal := 0, int64(-1)
	for i, v := range vals {
		if v > peakVal {
			peakIdx, peakVal = i, v
		}
	}
	if peakVal <= 0 {
		return ""
	}
	end := bucketAt(start, unit, len(vals)-1)
	return fmt.Sprintf("  by %-5s %s  %s–%s · peak %s %s",
		unit, stBar.Render(sparkline(vals)),
		bucketLabel(start, unit), bucketLabel(end, unit),
		bucketLabel(bucketAt(start, unit, peakIdx), unit), money(peakVal))
}

// bucketSpend buckets api-equivalent spend into adaptive calendar buckets (hour for
// <~1.5d, day for <50d, week for <350d, else month), returning the per-bucket
// totals, the unit, and the aligned start. The axis spans the SELECTED PERIOD
// [since,until] when bounded — so the unit tracks the window the user picked, not
// where the data happens to sit (a quarter reads "by week" even if you only worked a
// few days). The unbounded "all" window (zero bounds) falls back to the events' span.
func bucketSpend(events []event.AgentEvent, since, until time.Time, loc *time.Location) ([]int64, string, time.Time) {
	if loc == nil {
		loc = time.Local
	}
	// sessRep is each session's earliest dated turn, so a priced turn whose log line
	// carried no parseable timestamp can still be placed on the timeline — folded onto
	// its own session's known day — instead of being dropped. Without this the bar
	// total runs under the headline whenever any priced turn is undated (a real, if
	// narrow, gap: Claude Code stamps every turn, but a malformed/absent timestamp on
	// any provider's billable line would otherwise vanish from the spend-over-time view).
	sessRep := map[string]time.Time{}
	var lo, hi time.Time
	for _, e := range events {
		if e.TSStart.IsZero() || apiMicros(e) <= 0 {
			continue
		}
		t := e.TSStart // absolute instant; bucketing converts to the display zone
		if lo.IsZero() || t.Before(lo) {
			lo = t
		}
		if t.After(hi) {
			hi = t
		}
		if r, ok := sessRep[e.SessionID]; !ok || t.Before(r) {
			sessRep[e.SessionID] = t
		}
	}
	if lo.IsZero() {
		return nil, "", time.Time{} // no dated turn at all — a timeline can't be drawn
	}
	// The axis is the period window when bounded, else the events' own span.
	axisLo, axisHi := lo, hi
	if !since.IsZero() && !until.IsZero() {
		axisLo, axisHi = since, until
	}
	unit := chooseUnit(axisHi.Sub(axisLo))
	start := truncateTo(axisLo, unit, loc)
	n := bucketIndex(start, unit, axisHi, loc) + 1
	if n < 1 {
		n = 1
	}
	vals := make([]int64, n)
	for _, e := range events {
		a := apiMicros(e)
		if a <= 0 {
			continue
		}
		t := e.TSStart          // absolute instant; bucketing converts to the display zone
		if e.TSStart.IsZero() { // undated priced turn → fold onto its session's day (else the period's earliest)
			r, ok := sessRep[e.SessionID]
			if !ok {
				r = lo
			}
			t = r
		}
		if i := bucketIndex(start, unit, t, loc); i >= 0 && i < n {
			vals[i] += a
		}
	}
	return vals, unit, start
}

func chooseUnit(span time.Duration) string {
	switch {
	case span < 36*time.Hour:
		return "hour"
	case span < 50*24*time.Hour:
		return "day"
	case span < 350*24*time.Hour:
		return "week"
	default:
		return "month"
	}
}

// truncateTo aligns t to the start of its bucket IN THE DISPLAY ZONE loc (nil ⇒
// local), so bucket boundaries fall on local midnights/hours/Mondays — the instant is
// still absolute, only the calendar grid is the user's. Backend windows stay UTC.
func truncateTo(t time.Time, unit string, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	t = t.In(loc)
	switch unit {
	case "hour":
		return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
	case "week":
		d := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)) // back to Monday
	case "month":
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
	default: // day
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
}

// bucketIndex returns which bucket t falls in, counting from start. hour/day/week use
// the absolute elapsed span (start is a loc-aligned instant); month counts calendar
// months in loc so the grid matches the local year/month.
func bucketIndex(start time.Time, unit string, t time.Time, loc *time.Location) int {
	switch unit {
	case "hour":
		return int(t.Sub(start) / time.Hour)
	case "week":
		return int(t.Sub(start) / (7 * 24 * time.Hour))
	case "month":
		if loc == nil {
			loc = time.Local
		}
		lt := t.In(loc)
		return (lt.Year()-start.Year())*12 + int(lt.Month()) - int(start.Month())
	default: // day
		return int(t.Sub(start) / (24 * time.Hour))
	}
}

func bucketAt(start time.Time, unit string, i int) time.Time {
	switch unit {
	case "hour":
		return start.Add(time.Duration(i) * time.Hour)
	case "week":
		return start.AddDate(0, 0, 7*i)
	case "month":
		return start.AddDate(0, i, 0)
	default: // day
		return start.AddDate(0, 0, i)
	}
}

func bucketLabel(t time.Time, unit string) string {
	switch unit {
	case "hour":
		return t.Format("Jan 2 3pm")
	case "month":
		return t.Format("Jan 2006")
	default: // day / week
		return t.Format("Jan 2")
	}
}

// sessionVCSLine renders the drilled session's git linkage: "branch · short-SHA".
// Branch is the first non-empty branch among the turns; SHA is the latest turn's
// commit (where the sitting ended), shortened. Empty when neither was resolved.
func sessionVCSLine(evs []event.AgentEvent) string {
	branch := ""
	for _, e := range evs {
		if e.GitBranch != "" {
			branch = e.GitBranch
			break
		}
	}
	sha := ""
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].GitSHA != "" {
			sha = shortSHA(evs[i].GitSHA)
			break
		}
	}
	switch {
	case branch != "" && sha != "":
		return branch + " · " + sha
	case branch != "":
		return branch
	default:
		return sha
	}
}

// shortSHA trims a commit to its first 10 hex chars for display.
func shortSHA(s string) string {
	if r := []rune(s); len(r) > 10 {
		return string(r[:10])
	}
	return s
}

// fileRow is one row of the receipt's cost+churn heatmap: a real file, its
// api-equivalent cost, and its git line-churn where a commit landed in the session.
type fileRow struct {
	path     string
	cost     int64
	churn    event.FileChurn
	hasChurn bool
}

// receiptFiles ranks a session's files priciest-first (ties broken by path), pairing
// each with its line-churn where present. Per-file cost is each turn's api-equivalent
// split equally across the files it touched, so the rows reconcile with the session
// total (mirrors the static receipt and `report --by file`).
func receiptFiles(evs []event.AgentEvent) []fileRow {
	costs := fileCosts(evs)
	churn := churnMap(evs)
	if len(costs) == 0 && len(churn) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			paths = append(paths, f)
		}
	}
	for f := range costs {
		add(f)
	}
	for f := range churn {
		add(f)
	}
	sort.Slice(paths, func(i, j int) bool {
		if costs[paths[i]] != costs[paths[j]] {
			return costs[paths[i]] > costs[paths[j]]
		}
		return paths[i] < paths[j]
	})
	rows := make([]fileRow, 0, len(paths))
	for _, p := range paths {
		c, ok := churn[p]
		rows = append(rows, fileRow{path: p, cost: costs[p], churn: c, hasChurn: ok && (c.Added > 0 || c.Removed > 0)})
	}
	return rows
}

// fileWindow clamps a file list of length n to budget rows, sliding to keep cursor
// visible — the receipt's vertical analogue of windowRange. budget ≤ 0 (size
// unknown) or ≥ n shows everything.
func fileWindow(n, cursor, budget int) (start, end int) {
	if budget <= 0 || budget >= n {
		return 0, n
	}
	if cursor >= budget {
		start = cursor - budget + 1
	}
	end = start + budget
	if end > n {
		end = n
	}
	return start, end
}

// fileRowLine renders one heatmap row — a cost-shaded bar, the file's cost, its path,
// and churn where present. The selected row is highlighted (plain text under the
// select style, ▶ marker), mirroring the session list's cursor row.
func fileRowLine(fr fileRow, max int64, selected bool) string {
	bar := spendBar(fr.cost, max, 10)
	churn := ""
	if fr.hasChurn {
		churn = fmt.Sprintf("  +%d/-%d", fr.churn.Added, fr.churn.Removed)
	}
	if selected {
		return "  " + stSel.Render(fmt.Sprintf("▶ %s  %9s  %s%s", bar, money(fr.cost), fr.path, churn))
	}
	row := fmt.Sprintf("    %s  %9s  %s", styleBar(bar), money(fr.cost), fr.path)
	if churn != "" {
		row += stFaint.Render(churn)
	}
	return row
}

// fileTurn is one turn that touched a file, with the api-equivalent cost charged to
// that file (its share of the turn's equal split).
type fileTurn struct {
	ev    event.AgentEvent
	share int64
}

// fileTurns returns the turns that touched path, each carrying the api-equivalent
// cost charged to this file — the per-turn equal split (remainder on the turn's
// first-listed file) — priciest first, so the rows reconcile to the file's heatmap
// total.
func fileTurns(evs []event.AgentEvent, path string) []fileTurn {
	var out []fileTurn
	for _, e := range evs {
		idx := -1
		for i, f := range e.Files {
			if f == path {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		m := apiMicros(e)
		nf := int64(len(e.Files))
		share := m / nf
		if idx == 0 {
			share += m - (m/nf)*nf // the remainder lands on the first-listed file
		}
		out = append(out, fileTurn{ev: e, share: share})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].share > out[j].share })
	return out
}

// fileCosts charges each turn's api-equivalent cost to the files it touched, split
// equally (remainder on the first sorted file), mirroring the static receipt and the
// `report --by file` rollup so the per-file costs reconcile with the session total.
func fileCosts(evs []event.AgentEvent) map[string]int64 {
	costs := map[string]int64{}
	for _, e := range evs {
		if len(e.Files) == 0 {
			continue
		}
		m := apiMicros(e)
		nf := int64(len(e.Files))
		base := m / nf
		for i, f := range e.Files {
			costs[f] += base
			if i == 0 {
				costs[f] += m - base*nf
			}
		}
	}
	return costs
}

// churnMap collects the per-file churn captured for the session (it rides on the
// representative event; empty when no commit landed during the sitting).
func churnMap(evs []event.AgentEvent) map[string]event.FileChurn {
	out := map[string]event.FileChurn{}
	for _, e := range evs {
		for _, c := range e.SessionChurn {
			out[c.Path] = c
		}
	}
	return out
}

func tokenSummary(t event.Tokens) string {
	return fmt.Sprintf("%s in · %s out · %s cache-read · %s cache-write",
		comma(t.Input), comma(t.Output), comma(t.CacheRead), comma(t.CacheWrite))
}

// wrapText soft-wraps s to width columns on word boundaries, wrapping each existing
// line independently so paragraph breaks survive. A width ≤ 0 (terminal size not yet
// known) returns the lines unwrapped; a single word longer than width is left whole.
func wrapText(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		if width <= 0 {
			out = append(out, para)
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) > width {
				out = append(out, line)
				line = w
			} else {
				line += " " + w
			}
		}
		out = append(out, line)
	}
	return out
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

// timeLayout renders a 12-hour wall-clock with an am/pm marker (e.g. "Jun 17 7:42am").
// VISUAL times render in the user's LOCAL zone while the backend stays UTC end-to-end
// (event timestamps, windows, dedupe and pricing are all UTC for clean
// reconciliation); the display layer just chooses the zone to show. Span labels carry
// the local zone abbreviation so the window is unambiguous.
const timeLayout = "Jan 02 3:04pm"

// fmtTimeIn renders an instant's wall clock in the display zone loc (nil ⇒ local).
func fmtTimeIn(t time.Time, loc *time.Location) string {
	if loc == nil {
		loc = time.Local
	}
	return t.In(loc).Format(timeLayout)
}

// fmtTime renders in the user's local zone — the visual default everywhere.
func fmtTime(t time.Time) string { return fmtTimeIn(t, time.Local) }

// quotaProviderTitle capitalizes a quota provider key for the gauge ("claude" → "Claude").
func quotaProviderTitle(p string) string {
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// quotaLines builds the plan-limit gauge block for the list header: one line per active
// sample, plus an explicit "unknown" line when the user has Claude activity but no
// Claude window snapshot — so the gauge explains its blank rather than vanishing.
// Returned lines are pre-styled; the count is also the height budget (windowRange).
func (m Model) quotaLines() []string {
	qt := quota.NewTracker()
	for _, s := range m.quota {
		qt.Observe(s)
	}
	var out []string
	claudeShown := false
	for _, s := range qt.Active(m.now) {
		out = append(out, "  "+stBold.Render(quotaProviderTitle(s.Provider))+" "+stFaint.Render(s.Line(m.now)))
		if s.Provider == "claude" {
			claudeShown = true
		}
	}
	if !claudeShown && m.hasProvider("claude_code") {
		out = append(out, "  "+stBold.Render("Claude weekly")+" "+stFaint.Render("unknown — no local usage snapshot"))
	}
	return out
}

// hasProvider reports whether any session in the current rows came from provider p.
func (m Model) hasProvider(p string) bool {
	for _, r := range m.rows {
		if r.provider == p {
			return true
		}
	}
	return false
}

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
