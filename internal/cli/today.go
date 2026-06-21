// `aispend today` — the arbitrage-first daily glance, the founder's primary view
// (07/08-*-concept.md). It leads with value, not a bare total: the api-equivalent
// spend, the subscription ROI (plan $/day vs metered), what prompt caching saved,
// a turns/sessions/top-model strip, and an hourly spike bar that surfaces the
// 2am session that looped (09-session-view.md). Hand-rolled, zero-dependency,
// degrades to plain ASCII off a TTY.
package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/budget"
	"github.com/agentspend/ai-agent-spend/internal/config"
	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/pricing"
	"github.com/agentspend/ai-agent-spend/internal/provider"
	"github.com/agentspend/ai-agent-spend/internal/provider/codex"
	"github.com/agentspend/ai-agent-spend/internal/quota"
	"github.com/agentspend/ai-agent-spend/internal/store"
	"github.com/agentspend/ai-agent-spend/internal/trailer"
)

func (a *App) cmdToday(args []string) int {
	fs := flag.NewFlagSet("today", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	repo := fs.String("repo", ".", "repo to preview uncommitted trailer spend for (defaults to cwd)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	now := a.Now()
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	events, err := st.Query(store.Filter{Since: startOfDay(now), Until: now})
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	// Only when today is empty do we learn the store total — so we can tell "scan
	// first" apart from "you just haven't coded today" without paying for the count
	// on the common path.
	storeTotal := len(events)
	if len(events) == 0 {
		if all, e := st.Query(store.Filter{}); e == nil {
			storeTotal = len(all)
		}
	}
	a.renderToday(events, now, a.planSet(), storeTotal, a.pricingEngine())
	a.renderBudget(st, now)
	a.renderPending(*repo)
	return 0
}

// renderPending prints the read-only "pending commit" preview — the api-equivalent
// spend the next commit on this branch would be stamped with (the trailer watermark
// made visible). Nothing prints outside a repo, on a detached HEAD, or when nothing
// is uncommitted, so it never adds noise. It mirrors the hook's exact computation,
// so the preview equals what `git commit` would actually stamp.
func (a *App) renderPending(repoDir string) {
	u, branch, ok := trailer.Preview(repoDir, a.pendingUsage)
	if !ok {
		return
	}
	fmt.Fprintf(a.Out, "  %s %s: %s · %d turns (uncommitted)\n",
		paint(useColor(a.Out), cBold, "pending commit"), branch, usd(u.Cost.Micros, u.Cost.Currency), u.Requests)
}

// renderBudget prints the month-to-date budget pace line when a budget is configured
// (off by default → nothing printed). It sums the api-equivalent spend for the current
// calendar month against the ceiling and shows PACE, not just level — a budget is your
// dollar ceiling, informational only (never the provider's hard limit, which is the
// quota gauge). Providers with no computable api-equivalent are excluded and disclosed.
// budgetPace computes the month-to-date api-equivalent pace against the configured
// budget (shared by `today` and the TUI header). ok is false when no budget is set;
// uncovered lists providers with no api-equivalent (excluded from the sum) for honest
// disclosure.
func (a *App) budgetPace(st *store.FileStore, now time.Time) (p budget.Pace, uncovered []string, ok bool) {
	micros, set, err := config.LoadBudget(a.Resolver.AppHome())
	if err != nil || !set {
		return budget.Pace{}, nil, false
	}
	start, end := budget.MonthBounds(now)
	evs, err := st.Query(store.Filter{Since: start, Until: now})
	if err != nil {
		return budget.Pace{}, nil, false
	}
	var spent int64
	unc := map[string]bool{}
	for _, e := range evs {
		if m := e.CostViews.APIEquivalent; m != nil {
			spent += m.Micros
		} else if e.Provider != "" {
			unc[e.Provider] = true
		}
	}
	keys := make([]string, 0, len(unc))
	for k := range unc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return budget.ComputePace(micros, spent, start, now, end), keys, true
}

func (a *App) renderBudget(st *store.FileStore, now time.Time) {
	p, uncovered, ok := a.budgetPace(st, now)
	if !ok {
		return
	}
	pct := p.UsedFraction() * 100
	line := fmt.Sprintf("  budget %s/mo  %s  %s used (%.0f%%) · %.0f%% of month",
		usd(p.Limit, "USD"), bar(pct), usd(p.Spent, "USD"), pct, p.ElapsedFraction*100)
	if s := p.Status(); s != "" {
		line += " · " + s
	}
	fmt.Fprintln(a.Out, line)
	if len(uncovered) > 0 {
		fmt.Fprintf(a.Out, "  note: %s excluded from the budget (no api-equivalent)\n", joinProviderLabels(uncovered))
	}
}

func (a *App) renderToday(events []event.AgentEvent, now time.Time, plans config.PlanSet, storeTotal int, eng *pricing.Engine) {
	color := useColor(a.Out)
	fmt.Fprintf(a.Out, "%s · %s\n\n", paint(color, cBold, "aispend today"), now.Format("Mon Jan 2"))

	if len(events) == 0 {
		if storeTotal > 0 {
			fmt.Fprintf(a.Out, "  no AI-coding spend recorded today (%d events stored — try `aispend report --period yesterday`)\n", storeTotal)
		} else {
			fmt.Fprintln(a.Out, "  no AI-coding spend recorded today yet — run `aispend scan`")
		}
		return
	}

	var apiTotal, withoutTotal int64
	priced := 0
	sessions := map[string]bool{}
	providers := map[string]bool{}
	for _, e := range events {
		if m := e.CostViews.APIEquivalent; m != nil {
			apiTotal += m.Micros
		}
		if w, ok := eng.WithoutCache(e.Model, e.Tokens); ok {
			withoutTotal += w.Micros
		}
		if _, ok := eng.Components(e.Model, e.Tokens); ok {
			priced++
		}
		if e.SessionID != "" {
			sessions[e.SessionID] = true
		}
		if e.Provider != "" {
			providers[e.Provider] = true
		}
	}

	// Headline: api-equivalent + the arbitrage clause (plan $/day · ROI).
	head := "not computable"
	if priced > 0 {
		head = usd(apiTotal, "USD")
	}
	line := fmt.Sprintf("  %s api-equivalent", paint(color, cBold, head))
	dailyFee, uncovered := a.dailyPlanFee(providers, plans)
	if dailyFee > 0 && apiTotal > 0 {
		line += fmt.Sprintf("  ·  plan %s/day · %s ROI", usd(dailyFee, "USD"), roiStr(float64(apiTotal)/float64(dailyFee)))
	}
	fmt.Fprintln(a.Out, line)

	// Cache savings — the visible half of the wedge.
	if withoutTotal > 0 && withoutTotal > apiTotal {
		saved := withoutTotal - apiTotal
		pct := float64(saved) / float64(withoutTotal) * 100
		fmt.Fprintf(a.Out, "  cache saved ~%s (%.0f%%)  %s\n", usd(saved, "USD"), pct, bar(pct))
	}

	// Turns · sessions · top model by spend.
	models := aggregateReport(events, "model", "api_equivalent")
	stat := fmt.Sprintf("  %d turns · %d sessions", len(events), len(sessions))
	if len(models.rows) > 0 && models.total > 0 {
		top := models.rows[0]
		stat += fmt.Sprintf(" · %s %.0f%%", shortModel(top.key), float64(top.micros)/float64(models.total)*100)
	}
	fmt.Fprintln(a.Out, stat)

	// Hourly spike bar — catch the hour that ran away. Bucketed in LOCAL time (the
	// ledger stays UTC) so it matches the TUI and the "visual times are local" rule.
	if apiTotal > 0 {
		hours, peakIdx := hourlyBuckets(events, startOfDay(now), now, time.Local)
		if hours[peakIdx] > 0 {
			peakHour := truncHour(startOfDay(now), time.Local).Add(time.Duration(peakIdx) * time.Hour).Hour()
			fmt.Fprintf(a.Out, "  by hour  %s  peak %02d:00 · %s\n", sparkline(hours), peakHour, usd(hours[peakIdx], "USD"))
		}
	}

	// Plan headroom — the weekly / 5h wall, read from the provider's local usage
	// snapshot. A point-in-time reading shown with its as-of, NOT part of the ledger;
	// absent or stale-past-reset → nothing rather than a guess.
	qt := quota.NewTracker()
	for _, s := range a.claudeQuotaSamples(now) {
		qt.Observe(s)
	}
	for _, s := range a.codexQuotaSamples(now) {
		qt.Observe(s)
	}
	claudeShown := false
	for _, s := range qt.Active(now) {
		fmt.Fprintf(a.Out, "  %s %s · %s\n", paint(color, cBold, quotaProviderLabel(s.Provider)), s.Line(now), s.Freshness(now))
		if s.Provider == "claude" {
			claudeShown = true
		}
	}
	// You code in Claude but we have no weekly window on disk → say so, don't stay
	// silent (a "not computable" the tool can explain, never a guess).
	if !claudeShown && providers["claude_code"] {
		fmt.Fprintf(a.Out, "  %s unknown — no local usage snapshot\n", paint(color, cBold, "Claude weekly"))
	}

	// Honesty footer: how to get an ROI, or which providers it omits.
	if dailyFee == 0 {
		fmt.Fprintln(a.Out, "  (set a subscription plan for ROI — see `aispend plans`)")
	} else if len(uncovered) > 0 {
		fmt.Fprintf(a.Out, "  note: %s not in the ROI (no plan set)\n", joinProviderLabels(uncovered))
	}
}

// claudeQuotaSamples reads Claude Code's local usage snapshot (best-effort): absent or
// unparseable → no samples, so the plan-headroom gauge degrades to nothing rather than
// guess. observedAt falls back to now when the file's mtime can't be read. Reading a
// local file only — no network, so the offline promise holds.
func (a *App) claudeQuotaSamples(now time.Time) []quota.Sample {
	path := a.Resolver.ClaudeUsagePath()
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	observed := now
	if fi, e := os.Stat(path); e == nil {
		observed = fi.ModTime()
	}
	return quota.ParseClaudeRateLimits(b, observed)
}

// maxQuotaScanFiles caps how many recent Codex rollouts we scan for a rate_limits
// sample. The freshest window lives in the most recently written session(s), and
// exec-mode runs log rate_limits:null, so we walk newest-first until one is populated.
const maxQuotaScanFiles = 8

// codexQuotaSamples reads Codex's plan-limit windows from the rate_limits block its
// rollout logs already carry (newest session first), reusing quota.ParseCodex. It is
// best-effort: no Codex data, or only exec-mode nulls, yields no samples (the gauge
// degrades to nothing). Local read-only — no network, no new persistence.
func (a *App) codexQuotaSamples(now time.Time) []quota.Sample {
	srcs, err := codex.New(a.Resolver).Sources()
	if err != nil || len(srcs) == 0 {
		return nil
	}
	type aged struct {
		s  provider.Source
		mt time.Time
	}
	all := make([]aged, len(srcs))
	for i, s := range srcs {
		var mt time.Time
		if fi, e := os.Stat(s.RawPath); e == nil {
			mt = fi.ModTime()
		}
		all[i] = aged{s, mt}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mt.After(all[j].mt) }) // newest first

	tr := quota.NewTracker()
	for i, a2 := range all {
		if i >= maxQuotaScanFiles {
			break
		}
		recs, e := provider.ReadJSONL("codex", a2.s)
		if e != nil {
			continue
		}
		for _, r := range recs {
			tr.ObserveCodex(r.Raw)
		}
		if len(tr.Active(now)) > 0 {
			break // a populated window found; older rollouts can't be fresher
		}
	}
	return tr.Active(now)
}

// quotaProviderLabel capitalizes a quota provider key for display ("claude" → "Claude").
func quotaProviderLabel(p string) string {
	if p == "" {
		return p
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// dailyPlanFee sums each present provider's prorated daily subscription fee
// (monthly ÷ 30). Providers with no subscription plan are returned as uncovered so
// the ROI can say what it leaves out rather than silently overstating itself.
func (a *App) dailyPlanFee(providers map[string]bool, plans config.PlanSet) (int64, []string) {
	keys := make([]string, 0, len(providers))
	for p := range providers {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	var fee int64
	var uncovered []string
	for _, p := range keys {
		if f, ok := pricing.ProratedFee(toPricingPlan(plans.For(p)), 1); ok {
			fee += f.Micros
		} else {
			uncovered = append(uncovered, p)
		}
	}
	return fee, uncovered
}

// roiStr formats a plan-ROI multiple: whole numbers once it's a runaway win
// (≥10×), one decimal while it's still close to break-even.
func roiStr(roi float64) string {
	if roi >= 10 {
		return fmt.Sprintf("%.0f×", roi)
	}
	return fmt.Sprintf("%.1f×", roi)
}

func joinProviderLabels(ps []string) string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = providerLabel(p)
	}
	return strings.Join(out, ", ")
}

// hourlyBuckets buckets api-equivalent spend by LOCAL clock-hour across [winStart, now],
// returning per-hour micros and the peak index. Display-zone bucketing (loc injected
// for testability; the cli passes time.Local) keeps the hourly bar consistent with the
// TUI while the ledger instants stay UTC. Half-hour-offset zones (e.g. IST) bucket
// correctly because boundaries are taken on the local clock, not by absolute truncation.
func hourlyBuckets(events []event.AgentEvent, winStart, now time.Time, loc *time.Location) (hours []int64, peakIdx int) {
	start := truncHour(winStart, loc)
	n := int(truncHour(now, loc).Sub(start)/time.Hour) + 1
	if n < 1 {
		n = 1
	}
	hours = make([]int64, n)
	for _, e := range events {
		m := e.CostViews.APIEquivalent
		if m == nil || e.TSStart.IsZero() {
			continue
		}
		if idx := int(truncHour(e.TSStart, loc).Sub(start) / time.Hour); idx >= 0 && idx < n {
			hours[idx] += m.Micros
		}
	}
	for i, v := range hours {
		if v > hours[peakIdx] {
			peakIdx = i
		}
	}
	return hours, peakIdx
}

// truncHour returns t floored to the start of its hour in loc (on the local clock, so
// fractional-offset zones like IST land on their own :00 boundary).
func truncHour(t time.Time, loc *time.Location) time.Time {
	lt := t.In(loc)
	return time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour(), 0, 0, 0, loc)
}
