//go:build !offline

// cmdTui wires the store + pricing engine into the interactive explorer
// (internal/tui), including the in-explorer plan picker (which persists to config
// and recomputes amortization live). It lives behind `!offline` — exactly like the
// network refresh seam — because Bubble Tea transitively pulls net/url + net/netip;
// those are pure value/parsing packages (no dial, no http sink), and the default
// build already carries them via the disclosed `pricing refresh`, but the
// air-gapped `offline` build promises *zero* net/*, so the TUI is compiled out
// there (tui_offline.go). The default `doctor --network` story is unchanged.
package cli

import (
	"flag"
	"fmt"
	"sort"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/budget"
	"github.com/agentspend/ai-agent-spend/internal/config"
	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/provider"
	"github.com/agentspend/ai-agent-spend/internal/provider/claudecode"
	"github.com/agentspend/ai-agent-spend/internal/provider/codex"
	"github.com/agentspend/ai-agent-spend/internal/quota"
	"github.com/agentspend/ai-agent-spend/internal/store"
	"github.com/agentspend/ai-agent-spend/internal/trailer"
	"github.com/agentspend/ai-agent-spend/internal/tui"
)

// tuiBuilt reports that the interactive TUI is linked into this build, so the
// no-arg default (cmdDefault) may open it. The offline build sets it false.
const tuiBuilt = true

// toTUITrailers / fromTUITrailers bridge config.Trailers and the tui's editor struct,
// keeping the tui package free of a config dependency.
func toTUITrailers(t config.Trailers) tui.TrailerSettings {
	return tui.TrailerSettings{Enabled: t.Enabled, Cost: t.Cost, CostModels: t.CostModels, Tokens: t.Tokens, Interactions: t.Interactions, Precision: t.Precision, CostName: t.CostName}
}

func fromTUITrailers(t tui.TrailerSettings) config.Trailers {
	return config.Trailers{Enabled: t.Enabled, Cost: t.Cost, CostModels: t.CostModels, Tokens: t.Tokens, Interactions: t.Interactions, Precision: t.Precision, CostName: t.CostName}
}

func (a *App) cmdTui(args []string) int {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	periodSpec := fs.String("period", "week", "initial calendar window (same grammar as `report`)")
	watch := fs.Bool("watch", false, "live mode: periodically re-scan logs and refresh the view in place")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !isTTY(a.Out) {
		fmt.Fprintln(a.Err, "aispend tui needs an interactive terminal; try `aispend top` or `aispend report`")
		return 1
	}

	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	all, err := st.Query(store.Filter{})
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	now := a.Now()
	// The scrubbable windows, shortest → longest (←/→ in the TUI walks them).
	specs := []string{"today", "week", "this month", "last month", "quarter", "last quarter", "this year", "last year", "all"}

	// buildPeriods pre-filters each window and pre-computes its prorated plan fee.
	// It reads the plan set fresh each call so the in-TUI picker (which rewrites
	// config) recomputes amortization live.
	buildPeriods := func() []tui.Period {
		plans := a.planSet()
		// Re-read the store and the clock each call so the picker AND the watch tick
		// see freshly-scanned turns and current windows; fall back to the startup
		// snapshot if a re-open fails.
		evsAll, winNow := all, a.Now()
		if st2, err := a.openStore(); err == nil {
			if fresh, e := st2.Query(store.Filter{}); e == nil {
				evsAll = fresh
			}
		}
		ps := make([]tui.Period, 0, len(specs))
		for _, spec := range specs {
			win, err := parsePeriod(spec, winNow)
			if err != nil {
				continue
			}
			evs := eventsInWindow(evsAll, win)
			amort, hasPlan := a.amortizedByProvider(evs, win, plans)
			ps = append(ps, tui.Period{Label: win.Label, Events: evs, Amortized: amort, HasPlan: hasPlan, Since: win.Since, Until: win.Until})
		}
		return ps
	}

	periods := buildPeriods()
	startIdx := 0
	if reqWin, err := parsePeriod(*periodSpec, now); err == nil {
		for i, p := range periods {
			if p.Label == reqWin.Label {
				startIdx = i
			}
		}
	}

	// setPlan persists the picker's choice (provider + id + billing-cycle start) and
	// returns recomputed periods so the amortized lens updates without leaving the TUI.
	setPlan := func(provider, id string, start time.Time) []tui.Period {
		if err := config.SetProviderPlan(a.Resolver.AppHome(), provider, id, start); err != nil {
			fmt.Fprintf(a.Err, "aispend: %v\n", err)
		}
		return buildPeriods()
	}

	// Load the repo's [trailers] config once: the receipt badge uses the cost name, the
	// in-explorer editor seeds + writes the whole block. (The git read is cached on
	// drill-in, so it isn't re-parsed per render.)
	trailerCfg := config.DefaultTrailers()
	if tc, err := config.LoadTrailers("."); err == nil {
		trailerCfg = tc
	}
	trailerName := trailerCfg.CostName
	if trailerName == "" {
		trailerName = "AI-Cost"
	}

	m := tui.New(periods, startIdx, a.pricingEngine()).WithNow(now).
		WithQuota(func() []quota.Sample {
			return append(a.claudeQuotaSamples(a.Now()), a.codexQuotaSamples(a.Now())...)
		}).
		WithBudget(func() (budget.Pace, bool) {
			st2, err := a.openStore() // re-open so a watch tick re-paces against fresh spend
			if err != nil {
				return budget.Pace{}, false
			}
			p, _, ok := a.budgetPace(st2, a.Now())
			return p, ok
		}).
		WithPlanPicker(a.planProviders(all), a.planChoices(), now, setPlan).
		WithBudgetSetter(func(micros int64) (budget.Pace, bool) {
			if err := config.SetBudget(a.Resolver.AppHome(), micros); err != nil {
				fmt.Fprintf(a.Err, "aispend: %v\n", err)
			}
			st2, err := a.openStore()
			if err != nil {
				return budget.Pace{}, false
			}
			p, _, ok := a.budgetPace(st2, a.Now())
			return p, ok
		}).
		WithCommitTrailer(func(sha string) (int64, bool) {
			return trailer.ReadCommitCost(".", sha, trailerName)
		}).
		WithTrailerSettings(toTUITrailers(trailerCfg), func(ts tui.TrailerSettings) error {
			return config.SetTrailers(".", fromTUITrailers(ts))
		}).
		WithPending(func() (tui.Pending, bool) {
			u, branch, ok := trailer.Preview(".", a.pendingUsage)
			if !ok {
				return tui.Pending{}, false
			}
			return tui.Pending{Branch: branch, Micros: u.Cost.Micros, Turns: u.Requests}, true
		})
	if *watch {
		// Live mode: every few seconds re-scan + rebuild and advance the clock, so an
		// ongoing session grows and liveness decays without leaving the explorer.
		m = m.WithWatch(3*time.Second, a.Now, buildPeriods)
	}
	if resolve := a.promptResolver(); resolve != nil {
		m = m.WithPromptResolver(resolve)
	}
	if name := a.sessionNameResolver(); name != nil {
		m = m.WithNameResolver(name)
	}
	if err := tui.RunModel(m, a.Out); err != nil {
		fmt.Fprintf(a.Err, "aispend: tui: %v\n", err)
		return 1
	}
	return 0
}

// promptResolver builds the explain view's lazy prompt re-reader. It snapshots the
// user's Claude Code session paths (hash → real path) once for this TUI session,
// then for a turn re-opens the matching log and recovers the human prompt behind it.
// Nothing is persisted, and only enumerated source paths are ever opened — the event
// supplies a content hash, never a path, so a foreign or forged hash simply misses
// rather than coercing an out-of-tree read. Returns nil when no Claude Code sources
// are present (the explain view then shows no prompt section).
func (a *App) promptResolver() func(event.AgentEvent) (string, bool) {
	cc := sourceMap(claudecode.New(a.Resolver))
	cx := sourceMap(codex.New(a.Resolver))
	if len(cc) == 0 && len(cx) == 0 {
		return nil
	}
	return func(e event.AgentEvent) (string, bool) {
		switch e.Provider {
		case "claude_code":
			if path, ok := cc[e.Evidence.SourcePathHash]; ok {
				return claudecode.PromptBefore(path, e.Evidence.SourceLine)
			}
		case "codex":
			if path, ok := cx[e.Evidence.SourcePathHash]; ok {
				return codex.PromptBefore(path, e.Evidence.SourceLine)
			}
		}
		return "", false
	}
}

// sessionNameResolver builds a lazy session-title resolver mirroring promptResolver:
// snapshot Claude Code sources (hash → real path) once, then re-read the human title
// (summary, else first prompt) from the matching log on drill-in. nil when no Claude
// Code sources are present. Local read-only — no network, no new persistence.
func (a *App) sessionNameResolver() func(event.AgentEvent) (string, bool) {
	cc := sourceMap(claudecode.New(a.Resolver))
	if len(cc) == 0 {
		return nil
	}
	return func(e event.AgentEvent) (string, bool) {
		if e.Provider != "claude_code" {
			return "", false
		}
		if path, ok := cc[e.Evidence.SourcePathHash]; ok {
			return claudecode.SessionName(path)
		}
		return "", false
	}
}

// sourceMap snapshots a provider's enumerated session files as hash → real path, so
// the explain view can re-open the matching log by its (already-hashed) source path.
// Returns nil on enumeration error or when the provider has no sources, so the event
// only ever supplies a hash key — never a path — and a foreign hash simply misses.
func sourceMap(p provider.Provider) map[string]string {
	srcs, err := p.Sources()
	if err != nil || len(srcs) == 0 {
		return nil
	}
	m := make(map[string]string, len(srcs))
	for _, s := range srcs {
		m[s.PathHash] = s.RawPath
	}
	return m
}

// planChoices builds the plan catalog for the picker, with an explicit "API / no
// subscription" option appended. The currently-selected plan is marked per
// provider (see planProviders), not here.
func (a *App) planChoices() []tui.PlanChoice {
	cs := make([]tui.PlanChoice, 0)
	for _, p := range config.Plans() {
		cs = append(cs, tui.PlanChoice{ID: p.ID, Label: p.Label, MonthlyUSD: p.MonthlyFeeUSD})
	}
	cs = append(cs, tui.PlanChoice{ID: "api"})
	return cs
}

// planProviders lists the providers present in the data with their currently
// effective subscription plan, so the picker can set one plan per provider.
func (a *App) planProviders(all []event.AgentEvent) []tui.ProviderChoice {
	plans := a.planSet()
	seen := map[string]bool{}
	var out []tui.ProviderChoice
	for _, e := range all {
		if e.Provider == "" || seen[e.Provider] {
			continue
		}
		seen[e.Provider] = true
		cur := ""
		if p := plans.For(e.Provider); p.Kind == "subscription" {
			cur = p.Name
		}
		out = append(out, tui.ProviderChoice{Name: e.Provider, Label: providerLabel(e.Provider), Current: cur})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// maybePickPlan runs the standalone picker for `aispend plans` on a TTY, writing
// the choice to config. Returns true when handled; false (non-TTY) lets the caller
// fall back to the static plan list.
func (a *App) maybePickPlan() bool {
	if !isTTY(a.Out) {
		return false
	}
	var provs []tui.ProviderChoice
	if st, err := a.openStore(); err == nil {
		if all, err := st.Query(store.Filter{}); err == nil {
			provs = a.planProviders(all)
		}
	}
	provider, chosen, start, ok, err := tui.RunPlanPicker(provs, a.planChoices(), a.Now(), a.Out)
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return true
	}
	if !ok {
		fmt.Fprintln(a.Out, "plan unchanged.")
		return true
	}
	if err := config.SetProviderPlan(a.Resolver.AppHome(), provider, chosen, start); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return true
	}
	target := "default"
	if provider != "" {
		target = provider
	}
	if chosen == "" || chosen == "api" {
		fmt.Fprintf(a.Out, "%s: no subscription — the amortized view is off for it.\n", target)
	} else {
		fmt.Fprintf(a.Out, "%s plan set to %s (start %s). Open `aispend tui` and press v for the amortized view.\n", target, chosen, start.Format("2006-01-02"))
	}
	return true
}

// eventsInWindow returns the events whose start falls in win (zero Since = no lower
// bound), mirroring the store filter the report path uses.
func eventsInWindow(all []event.AgentEvent, win window) []event.AgentEvent {
	out := make([]event.AgentEvent, 0, len(all))
	for _, e := range all {
		if !win.Since.IsZero() && e.TSStart.Before(win.Since) {
			continue
		}
		if !win.Until.IsZero() && e.TSStart.After(win.Until) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// amortizedByProvider prorates each present provider's subscription fee for the
// window — the same per-provider proration `report --view effective_allocated`
// uses (cycle-aware via the plan's start-date anchor) — keyed by provider so the
// TUI can allocate each provider's fee only among its own sessions. Empty map when
// no provider has an amortizable plan.
func (a *App) amortizedByProvider(events []event.AgentEvent, win window, plans config.PlanSet) (map[string]int64, bool) {
	provs := map[string]bool{}
	for _, e := range events {
		if e.Provider != "" {
			provs[e.Provider] = true
		}
	}
	winSince := win.Since
	legacyDays := 0
	if win.Since.IsZero() { // --all: amortize over the data's own span
		legacyDays = spanDays(events)
		winSince = spanStart(events)
	} else if d := int(win.Until.Sub(win.Since).Hours() / 24); d > 0 {
		legacyDays = d
	}
	out := map[string]int64{}
	hasPlan := false
	for prov := range provs {
		plan := toPricingPlan(plans.For(prov))
		// Amortizing "all time" (no lower bound) without a billing anchor would
		// flat-prorate the fee across the ENTIRE event span — which a single stray
		// or corrupt timestamp can blow up into decades of plan fees. Require a
		// plan start for the unbounded window; with one, AmortizeSubscription clamps
		// to it (robust to bad timestamps). Bounded windows (this month, …) are fine.
		if win.Since.IsZero() && plan.StartDate.IsZero() {
			continue
		}
		if prorated, ok := proratePlan(plan, winSince, win.Until, legacyDays); ok && prorated.Micros > 0 {
			out[prov] = prorated.Micros
			hasPlan = true
		}
	}
	return out, hasPlan
}
