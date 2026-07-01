package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/lines"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/quota"
	"github.com/cloudyali/ai-agent-spend/internal/store"
)

// UsageSnapshots assembles the shared presentation model (lines.Snapshot) the menu-bar
// app renders. Each active quota window becomes a progress line (level + reset +
// projection + severity color). From the ledger it leads each provider with the wedge —
// ROI then cache-saved — demotes today's raw spend below the gauges, marks a provider
// with a window but no spend today as Idle, and attaches a 7-day spend Trend series.
// Reported windows only; money is composed beside the gauge, never folded into it
// (package quota stays money-free). Local reads only — no network.
func (a *App) UsageSnapshots(now time.Time) []lines.Snapshot {
	qt := quota.NewTracker()
	for _, s := range a.claudeQuotaSamples(now) {
		qt.Observe(s)
	}
	for _, s := range a.codexQuotaSamples(now) {
		qt.Observe(s)
	}

	byProv := map[string]*lines.Snapshot{}
	var order []string
	codexPlanType := "" // account tier from Codex's rate_limits, for auto-detected ROI
	for _, s := range qt.Active(now) {
		if s.Provider == "codex" && s.PlanType != "" {
			codexPlanType = s.PlanType
		}
		snap := byProv[s.Provider]
		if snap == nil {
			snap = &lines.Snapshot{ProviderID: s.Provider, DisplayName: quotaProviderLabel(s.Provider)}
			byProv[s.Provider] = snap
			order = append(order, s.Provider)
		}
		proj := s.Project(now)
		used, limit, reset := s.UsedPercent, 100.0, s.ResetsAt
		ln := lines.Line{
			Type:       "progress",
			Label:      windowLabel(s.Window),
			Used:       &used,
			Limit:      &limit,
			Format:     &lines.Format{Kind: lines.Percent},
			ResetsAt:   &reset,
			Color:      lines.Classify(s.UsedPercent, 100, proj.Breaches).Hex(),
			Projection: &proj,
		}
		if start, ok := s.WindowStart(); ok {
			ln.PeriodMs = int64(reset.Sub(start) / time.Millisecond)
		}
		snap.Lines = append(snap.Lines, ln)
		if s.ObservedAt.After(snap.FetchedAt) {
			snap.FetchedAt = s.ObservedAt
		}
	}

	// Amplify the wedge from the ledger: today's api-equivalent spend + tokens, the
	// plan ROI, and what caching saved — the numbers only aispend can show (OpenUsage's
	// quota gauge can't). Computed here (off the render path) so the API and the menu
	// bar share them, and always present once you've coded, quota window or not.
	if st, err := a.openStore(); err == nil {
		// Widen to a 7-day window so the menu bar's Trend sparkline has history; today's
		// figures (spend/ROI/cache) still come from today's bucket only.
		since := startOfDay(now).AddDate(0, 0, -6)
		todayStart := startOfDay(now)
		if evs, err := st.Query(store.Filter{Since: since, Until: now}); err == nil {
			eng := a.pricingEngine()
			plans := a.planSet()
			type agg struct {
				spend, without, tokens int64
				trend                  [7]int64
			}
			byLedger := map[string]*agg{}
			var ledgerOrder []string
			for _, e := range evs {
				if e.Provider == "" {
					continue
				}
				g := byLedger[e.Provider]
				if g == nil {
					g = &agg{}
					byLedger[e.Provider] = g
					ledgerOrder = append(ledgerOrder, e.Provider)
				}
				di := int(startOfDay(e.TSStart).Sub(since) / (24 * time.Hour))
				if di < 0 {
					di = 0
				}
				if di > 6 {
					di = 6
				}
				today := !e.TSStart.Before(todayStart)
				if m := e.CostViews.APIEquivalent; m != nil {
					g.trend[di] += m.Micros
					if today {
						g.spend += m.Micros
					}
				}
				if today {
					if w, ok := eng.WithoutCache(e.Model, e.Tokens); ok {
						g.without += w.Micros
					}
					// CacheWrite1h is a subset of CacheWrite, so it isn't added again.
					g.tokens += e.Tokens.Input + e.Tokens.Output + e.Tokens.CacheRead + e.Tokens.CacheWrite
				}
			}
			sort.Strings(ledgerOrder) // deterministic provider order
			for _, lp := range ledgerOrder {
				g := byLedger[lp]
				if g.spend <= 0 {
					continue
				}
				canon := canonicalProvider(lp)
				snap := byProv[canon]
				if snap == nil {
					snap = &lines.Snapshot{ProviderID: canon, DisplayName: quotaProviderLabel(canon), FetchedAt: now}
					byProv[canon] = snap
					order = append(order, canon)
				}
				plan := effectivePlan(lp, plans, codexPlanType)
				if snap.Plan == "" && plan.Kind == "subscription" && plan.Name != "" {
					snap.Plan = tidyPlanLabel(plan.Name, snap.DisplayName) // context for the ROI
				}
				// The wedge leads: ROI then Cache saved, prepended ahead of the quota
				// gauges; today's raw spend is demoted below them.
				var lead []lines.Line
				if fee, ok := pricing.ProratedFee(toPricingPlan(plan), 1); ok && fee.Micros > 0 {
					lead = append(lead, lines.Line{
						Type:  "text",
						Label: "ROI",
						Value: roiStr(float64(g.spend)/float64(fee.Micros)) + " vs plan (" + usd(fee.Micros, "USD") + "/day)",
					})
				}
				if g.without > g.spend {
					saved := g.without - g.spend
					pct := float64(saved) / float64(g.without) * 100
					lead = append(lead, lines.Line{
						Type:  "text",
						Label: "Cache saved",
						Value: fmt.Sprintf("≈ %s (%.0f%%)", usd(saved, "USD"), pct),
					})
				}
				snap.Lines = append(lead, snap.Lines...)
				snap.Lines = append(snap.Lines, lines.Line{
					Type:  "text",
					Label: "Today",
					Value: "≈ " + usd(g.spend, "USD") + " · " + humanTokens(g.tokens),
				})
				snap.Trend = append([]int64(nil), g.trend[:]...)
			}
		}
	}

	out := make([]lines.Snapshot, 0, len(order))
	for _, p := range order {
		snap := byProv[p]
		if snap.FetchedAt.IsZero() {
			snap.FetchedAt = now
		}
		// Idle: a quota window but nothing spent today → the menu bar collapses it.
		hasProgress, hasToday := false, false
		for _, ln := range snap.Lines {
			switch {
			case ln.Type == "progress":
				hasProgress = true
			case ln.Type == "text" && ln.Label == "Today":
				hasToday = true
			}
		}
		snap.Idle = hasProgress && !hasToday
		out = append(out, *snap)
	}
	return out
}

// RefreshSnapshots brings the ledger current with a bounded, offline incremental scan,
// then returns the usage snapshots for the self-contained menu-bar client
// (cmd/aispend-bar) — which links the engine directly rather than going over HTTP.
func (a *App) RefreshSnapshots(now time.Time) []lines.Snapshot {
	a.scanOnLaunch(false) // best-effort, watermark-gated, offline (local reads only)
	return a.UsageSnapshots(now)
}

// effectivePlan resolves which plan a provider's ROI is priced against: an explicit
// per-provider config plan wins; for Codex without one, the plan auto-detected from its
// reported plan_type is used; otherwise the config default — except Codex never inherits
// a non-Codex default (a Claude plan shouldn't price Codex), so it falls back to no plan.
func effectivePlan(ledgerProvider string, plans config.PlanSet, codexPlanType string) config.Plan {
	if _, explicit := plans.ByProvider[ledgerProvider]; explicit {
		return plans.For(ledgerProvider)
	}
	if ledgerProvider == "codex" {
		if dp, ok := detectedCodexPlan(codexPlanType); ok {
			return dp
		}
		return config.Plan{Kind: "api"}
	}
	return plans.For(ledgerProvider)
}

// detectedCodexPlan builds a config.Plan from a Codex plan_type, taking the fee from the
// seeded catalog. Returns false for an unrecognized tier (config `codex_plan` overrides).
func detectedCodexPlan(planType string) (config.Plan, bool) {
	id, ok := codexPlanID(planType)
	if !ok {
		return config.Plan{}, false
	}
	for _, sp := range config.Plans() {
		if sp.ID == id {
			return config.Plan{Name: id, Kind: "subscription", MonthlyFeeUSD: sp.MonthlyFeeUSD, Currency: sp.Currency}, true
		}
	}
	return config.Plan{}, false
}

// codexPlanID maps Codex's reported plan_type to a seeded ChatGPT plan id. The exact
// strings are reverse-engineered and may vary by account, so this normalizes (lowercase,
// trims a "chatgpt-"/"chatgpt_" prefix) and returns false for anything unrecognized —
// config `codex_plan` is always the override.
func codexPlanID(planType string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(planType))
	s = strings.TrimPrefix(s, "chatgpt-")
	s = strings.TrimPrefix(s, "chatgpt_")
	switch s {
	case "pro":
		return "chatgpt-pro", true
	case "plus":
		return "chatgpt-plus", true
	case "go":
		return "chatgpt-go", true
	}
	return "", false
}

// canonicalProvider folds a ledger provider id onto the short id the quota gauges and
// the API use (matching OpenUsage): claude_code → claude; others unchanged. It lets a
// provider's quota gauge and its ledger spend land in one snapshot.
func canonicalProvider(p string) string {
	if p == "claude_code" {
		return "claude"
	}
	return p
}

// tidyPlanLabel turns a plan id into its catalog label, minus a leading provider word
// that would just repeat the snapshot's header ("claude-max-20x" → "Max 20x" under a
// "Claude" header). Unknown ids fall back to the raw value.
func tidyPlanLabel(id, providerLabel string) string {
	label := id
	for _, sp := range config.Plans() {
		if sp.ID == id {
			label = sp.Label
			break
		}
	}
	return strings.TrimPrefix(label, providerLabel+" ")
}

// humanTokens renders a token count compactly ("2.0M tokens", "45.0K tokens") for the
// at-a-glance surfaces.
func humanTokens(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB tokens", float64(n)/1e9)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1e6)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK tokens", float64(n)/1e3)
	default:
		return fmt.Sprintf("%d tokens", n)
	}
}

// windowLabel is the human label for a quota window, matching OpenUsage's vocabulary
// ("Session" for the 5-hour window, "Weekly" for the 7-day).
func windowLabel(w quota.Window) string {
	switch w {
	case quota.Window5h:
		return "Session"
	case quota.WindowWeekly:
		return "Weekly"
	case quota.WindowWeeklyOpus:
		return "Weekly (Opus)"
	case quota.WindowWeeklySonnet:
		return "Weekly (Sonnet)"
	default:
		return string(w)
	}
}
