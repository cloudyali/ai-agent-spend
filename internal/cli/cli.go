// Package cli is the aispend command surface: a zero-dependency, stdlib-flag
// dispatch wiring provider → normalize → price → store and rendering the results.
// The interactive TUI is where a number opens to its evidence (session → file →
// turn) — a number a user can't open is a number they won't trust.
//
// Built on the standard library `flag` package (not cobra) to keep the binary a
// pure-Go, dependency-free static artifact. See
// design-documents/phase-0A-trusted-explainable-ledger.md §Commands.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/githook"
	"github.com/cloudyali/ai-agent-spend/internal/normalize"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/pricing/refresh"
	"github.com/cloudyali/ai-agent-spend/internal/provider"
	"github.com/cloudyali/ai-agent-spend/internal/provider/claudecode"
	"github.com/cloudyali/ai-agent-spend/internal/provider/codex"
	"github.com/cloudyali/ai-agent-spend/internal/scan"
	"github.com/cloudyali/ai-agent-spend/internal/store"
	"github.com/cloudyali/ai-agent-spend/internal/termtext"
	"github.com/cloudyali/ai-agent-spend/internal/trailer"
	"github.com/cloudyali/ai-agent-spend/internal/vcs"
)

// Version is overwritten at release time via -ldflags (-X). It must stay a `var`,
// not a `const`: the Go linker's -X flag can only patch package-level string
// variables, so a const would silently ignore the release stamp and always
// report the dev value. See .goreleaser.yaml (ldflags) and .github/workflows/release.yml.
var Version = "0.1.0-dev"

// Website is the project's home page; Issues is the bug tracker. Both appear in
// the version and help banners. Purely informational text — printing them makes
// no network call.
const (
	Website = "https://aispend.cloudyali.io/"
	Issues  = "https://github.com/agentspend/ai-agent-spend/issues"
)

// App holds resolved dependencies; constructed from the environment, overridable
// in tests (which set HOME / AISPEND_HOME to temp dirs).
type App struct {
	Resolver platform.Resolver
	Now      func() time.Time
	Out, Err io.Writer
	// fetchPrices fetches the LiteLLM price-table bytes for the launch auto-refresh,
	// honoring the context's deadline so a latency-sensitive caller can bound the wait.
	// nil in production → refresh.FetchContext (the single disclosed inbound GET);
	// injected in tests so they never touch the network.
	fetchPrices func(context.Context, string) ([]byte, error)
}

// Run is the entry point: dispatch args, write to out/err, return an exit code.
func Run(args []string, out, err io.Writer) int {
	app := &App{Resolver: platform.Detect(), Now: nowUTC, Out: out, Err: err}
	return app.dispatch(args)
}

// nowUTC is the app clock. aispend is UTC end-to-end — event timestamps and
// billing data are UTC — so period boundaries (today/week/month/…), scan cutoffs,
// and the plan-start default are all computed in UTC for clean reconciliation,
// regardless of the machine's local zone.
func nowUTC() time.Time { return time.Now().UTC() }

func (a *App) dispatch(args []string) int {
	if len(args) == 0 {
		return a.cmdDefault()
	}
	switch cmd, rest := args[0], args[1:]; cmd {
	case "scan":
		return a.cmdScan(rest)
	case "report":
		return a.cmdReport(rest)
	case "today":
		return a.cmdToday(rest)
	case "budget":
		return a.cmdBudget(rest)
	case "top":
		return a.cmdTop(rest)
	case "tui":
		return a.cmdTui(rest)
	case "doctor":
		return a.cmdDoctor(rest)
	case "plans":
		return a.cmdPlans(rest)
	case "pricing":
		return a.cmdPricing(rest)
	case "git":
		return a.cmdGit(rest)
	case "trailer":
		return a.cmdTrailer(rest)
	case "consume":
		return a.cmdConsume(rest)
	case "version", "--version", "-v":
		fmt.Fprintf(a.Out, "aispend %s\n\nReport bugs to: %s\nHome page: %s\n", Version, Issues, Website)
		return 0
	case "help", "-h", "--help":
		a.usage()
		return 0
	default:
		fmt.Fprintf(a.Err, "aispend: unknown command %q\n", cmd)
		a.usage()
		return 2
	}
}

// cmdDefault is the no-argument entrypoint. The TUI is the default channel, so a
// bare `aispend` opens the interactive explorer — but only when it can: the TUI
// needs a TTY and is compiled out of the offline build (tuiBuilt is false there).
// Otherwise it falls back to the static `today` glance, which carries the same
// numbers and never bleeds escapes into a pipe. `aispend help` still prints usage.
func (a *App) cmdDefault() int {
	if tuiBuilt && isTTY(a.Out) {
		return a.cmdTui(nil)
	}
	return a.cmdToday(nil)
}

func (a *App) eventsPath() string { return filepath.Join(a.Resolver.AppHome(), "events.json") }

// pricingEngine returns the engine used to price events. It is offline-first: if a
// LiteLLM price cache is present and fresh (≤24h), its rates overlay the embedded
// table; otherwise the embedded table is used unchanged. Pricing never blocks on
// the network — only `aispend pricing refresh` fetches.
func (a *App) pricingEngine() *pricing.Engine {
	cache := refresh.CachePath(a.Resolver.AppHome())
	if data, ok := refresh.ReadFreshCache(cache, 24*time.Hour); ok {
		if rates, err := pricing.ParseLiteLLM(data); err == nil {
			return pricing.NewEngineWithRates("litellm-cache", rates)
		}
	}
	return pricing.NewEngine()
}

func (a *App) openStore() (*store.FileStore, error) { return store.OpenFileStore(a.eventsPath()) }

func (a *App) identityHash() string {
	return platform.HashPath("identity:"+a.Resolver.Home, a.Resolver.GOOS)
}

// repriceStored re-prices every stored event with eng and writes them back, so a
// changed rate source (after `pricing refresh`) applies to existing data without
// re-reading sessions. Tokens and model are already stored, so this only recomputes
// the cost views. Returns the number of events repriced.
func (a *App) repriceStored(eng *pricing.Engine) (int, error) {
	st, err := a.openStore()
	if err != nil {
		return 0, err
	}
	events, err := st.Query(store.Filter{})
	if err != nil || len(events) == 0 {
		return 0, err
	}
	plans := a.planSet()
	for i := range events {
		if err := eng.Price(&events[i], toPricingPlan(plans.For(events[i].Provider))); err != nil {
			return 0, err
		}
	}
	if err := st.Upsert(events); err != nil {
		return 0, err
	}
	return len(events), nil
}

// --- scan ---

// scanPair binds a provider to its normalizer. scanPairs builds the set the scanner
// loop runs — shared by `scan` and the trailer hook's live refresh, so both ingest
// the same providers identically.
type scanPair struct {
	p provider.Provider
	n normalize.Normalizer
}

func (a *App) scanPairs(idh string, attr func(string) (string, string)) []scanPair {
	return []scanPair{
		{claudecode.New(a.Resolver), normalize.ClaudeCode{GOOS: a.Resolver.GOOS, IdentityHash: idh, Attribute: attr, RepoRoot: a.repoRoot, HeadAt: vcs.HeadAt, Churn: vcs.Numstat, CurrentBranch: vcs.CurrentBranch}},
		{codex.New(a.Resolver), &normalize.Codex{GOOS: a.Resolver.GOOS, IdentityHash: idh, Attribute: attr}},
	}
}

func (a *App) cmdScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	_ = fs.Bool("no-network", false, "hard-disable any network path (0A is already offline)")
	verbose := fs.Bool("verbose", false, "show a sample of skipped (unrecognized) records")
	full := fs.Bool("full", false, "re-read all sessions, ignoring the last-scan watermark (use after upgrading)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	attr := a.attribution()
	plans := a.planSet()
	eng := a.pricingEngine()
	idh := a.identityHash()

	providers := a.scanPairs(idh, attr)

	var totalImported, totalSkipped, detected int
	var allSkips []scan.Skip
	for _, pr := range providers {
		present, err := pr.p.Detect()
		if err != nil {
			fmt.Fprintf(a.Err, "aispend: %v\n", err)
			return 1
		}
		if !present {
			continue
		}
		detected++
		sources, _ := pr.p.Sources()
		sc := &scan.Scanner{Provider: pr.p, Normalizer: pr.n, Pricing: eng, Plan: toPricingPlan(plans.For(pr.p.Name())), Store: st, Sink: st, Now: a.Now, Full: *full}
		sum, err := sc.Run()
		if err != nil {
			fmt.Fprintf(a.Err, "aispend: %v\n", err)
			return 1
		}

		fmt.Fprintf(a.Out, "%s · %d source(s) · imported %d", pr.p.Name(), len(sources), sum.Imported)
		if !sum.Since.IsZero() {
			fmt.Fprintf(a.Out, " · %s → %s", sum.Since.Format("2006-01-02"), sum.Until.Format("2006-01-02"))
		}
		if sum.Deduped > 0 {
			fmt.Fprintf(a.Out, " · %d duplicates collapsed", sum.Deduped)
		}
		if sum.Skipped > 0 {
			fmt.Fprintf(a.Out, " · %d skipped", sum.Skipped)
		}
		fmt.Fprintln(a.Out)

		totalImported += sum.Imported
		totalSkipped += sum.Skipped
		allSkips = append(allSkips, sum.Skips...)
	}

	if detected == 0 {
		fmt.Fprintln(a.Out, "No supported agents detected (looked for Claude Code, Codex). Nothing to scan.")
		return 0
	}
	fmt.Fprintf(a.Out, "Imported %d events total · stored in %s · no network calls made\n", totalImported, a.eventsPath())

	if *verbose && len(allSkips) > 0 {
		fmt.Fprintf(a.Out, "\nSkipped records (showing %d of %d unrecognized):\n", len(allSkips), totalSkipped)
		for _, sk := range allSkips {
			fmt.Fprintf(a.Out, "  %s#L%d  %s\n      %s\n", shortHash(sk.PathHash), sk.Line, sk.Reason, sk.Sample)
		}
	} else if totalSkipped > 0 {
		fmt.Fprintf(a.Out, "(%d records skipped — run `aispend scan --verbose` to see why)\n", totalSkipped)
	}
	return 0
}

// --- report ---

// cmdReport is the single spend surface. The window is chosen entirely by
// --period, which is always a calendar span (see period.go) — there is no rolling
// window. Grouping and the cost lens stay orthogonal flags (--by, --view).
func (a *App) cmdReport(args []string) int {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	by := fs.String("by", "model", "group by: model|repo|provider|cost_tag|session|branch|commit|file")
	view := fs.String("view", "api_equivalent", "cost view: api_equivalent|reported|estimated|billed|amortized|marginal")
	periodSpec := fs.String("period", "week", `calendar window: today|yesterday|week|month|"last week"|"last month"|quarter|"this year"|"N days"|"since YYYY-MM-DD"|YYYY-MM-DD..YYYY-MM-DD|all`)
	jsonOut := fs.Bool("json", false, "emit the report as JSON instead of a table (metered views only)")
	noScan := fs.Bool("no-scan", false, "skip the automatic scan-on-launch; read the ledger as-is")
	noRefresh := fs.Bool("no-refresh", false, "skip the automatic price refresh-on-launch; use cached/embedded rates as-is")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	win, err := parsePeriod(*periodSpec, a.Now())
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 2
	}
	switch dash(*view) {
	case "effective-allocated":
		// Hard rename (no alias): the old name must not silently fall through to
		// api_equivalent — point the user at the new view instead.
		fmt.Fprintln(a.Err, "aispend: --view effective_allocated was renamed to --view amortized")
		return 2
	case "amortized":
		if *jsonOut {
			fmt.Fprintln(a.Err, "aispend: --json isn't supported with --view amortized yet (use a metered view: api_equivalent, reported, estimated, billed, marginal)")
			return 2
		}
	}

	a.refreshOnLaunch(*noRefresh)
	a.scanOnLaunch(*noScan)

	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	events, err := st.Query(store.Filter{Since: win.Since, Until: win.Until})
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}

	if *jsonOut {
		return a.emitReportJSON(events, *by, *view, win, a.pricingEngine())
	}

	// For an honest empty-state message, learn whether the store has any data at
	// all — only when this window came back empty (keeps the common path cheap).
	storeTotal := len(events)
	if len(events) == 0 {
		if all, err := st.Query(store.Filter{}); err == nil {
			storeTotal = len(all)
		}
	}

	a.renderReport(events, *by, *view, win.Since, win.Until, win.Label, a.planSet(), storeTotal)
	return 0
}

// attribution returns a per-directory-cached resolver of (project, cost_tag) from
// the nearest .aispend.toml.
func (a *App) attribution() func(string) (string, string) {
	cache := map[string][2]string{}
	return func(cwd string) (string, string) {
		if v, ok := cache[cwd]; ok {
			return v[0], v[1]
		}
		var p, c string
		if r, found, err := config.LoadRepo(cwd); err == nil && found {
			p, c = r.Project, r.CostTag
		}
		cache[cwd] = [2]string{p, c}
		return p, c
	}
}

// repoRoot walks up from a file to the nearest dir holding a .git or .aispend.toml
// — the project the file belongs to. Returns "" when none is found (or the file's
// ancestors are gone), so Cowork attribution skips rather than guessing. Used to
// attribute Cowork sessions, whose cwd is the desktop app's outputs dir.
func (a *App) repoRoot(filePath string) string {
	dir := filepath.Dir(filePath)
	for {
		for _, marker := range []string{".git", ".aispend.toml"} {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir { // reached filesystem root
			return ""
		}
		dir = parent
	}
}

// planSet loads per-provider subscription plans from ~/.aispend/config.toml.
func (a *App) planSet() config.PlanSet {
	set, err := config.LoadPlanSet(a.Resolver.AppHome())
	if err != nil {
		return config.PlanSet{Default: config.Plan{Kind: "api"}, ByProvider: map[string]config.Plan{}}
	}
	return set
}

func toPricingPlan(p config.Plan) pricing.Plan {
	if p.Kind != "subscription" || p.MonthlyFeeUSD <= 0 {
		return pricing.Plan{Kind: "api"}
	}
	fee := event.USD(int64(p.MonthlyFeeUSD * 1_000_000))
	return pricing.Plan{Kind: "subscription", MonthlyFee: &fee, StartDate: p.StartDate}
}

type aggRow struct {
	key      string
	micros   int64
	count    int
	currency string
}

// emptyRange prints an honest empty-state line: "store has data, just none in
// this window" (widen it) vs "store is genuinely empty" (run scan) — so we never
// tell a user with 7,000 stored events to go scan.
func (a *App) emptyRange(label string, storeTotal int) {
	if storeTotal > 0 {
		fmt.Fprintf(a.Out, "  (no events for %s — %d stored; widen with --period all or --period \"90 days\")\n", label, storeTotal)
	} else {
		fmt.Fprintln(a.Out, "  (no events yet — run `aispend scan`)")
	}
}

func (a *App) renderReport(events []event.AgentEvent, by, view string, since, until time.Time, label string, plans config.PlanSet, storeTotal int) {
	if dash(view) == "amortized" {
		a.renderAllocated(events, by, since, until, label, plans, storeTotal)
		return
	}
	agg := aggregateReport(events, by, view)

	fmt.Fprintf(a.Out, "AI-coding spend · %s · by %s · view: %s", label, by, dash(view))
	if agg.n > 0 {
		fmt.Fprintf(a.Out, " (%s, confidence %.2f)", methodLabel(agg.methods), agg.confidence())
	}
	fmt.Fprintln(a.Out)
	if agg.n == 0 {
		// Two very different empty states. If the window itself holds events but
		// none carry a cost in THIS view (e.g. --view reported with no tool-written
		// cost captured), widening the window is useless and saying "no events" is a
		// lie — point at a populated view instead. Only a genuinely empty window
		// gets the scan/widen guidance.
		if len(events) > 0 {
			fmt.Fprintf(a.Out, "  (none of the %d event(s) in %s have a %s cost", len(events), label, dash(view))
			if dash(view) != "api-equivalent" {
				fmt.Fprint(a.Out, " — try --view api_equivalent")
			} else {
				fmt.Fprint(a.Out, " — these turns carry no priced cost in this view")
			}
			fmt.Fprintln(a.Out, ")")
			return
		}
		a.emptyRange(label, storeTotal)
		return
	}

	for _, r := range agg.rows {
		pct := float64(r.micros) / float64(agg.total) * 100
		fmt.Fprintf(a.Out, "  %-18s %10s  %s %3.0f%%\n", displayKey(by, r.key), usd(r.micros, agg.currency), bar(pct), pct)
	}
	fmt.Fprintf(a.Out, "  %-18s %10s  (%d events)\n", "total", usd(agg.total, agg.currency), agg.n)
	if agg.skipped > 0 {
		// Never let a model the pricing table doesn't know silently shrink the
		// total. Show the count and the offending models so the gap is fixable.
		fmt.Fprintf(a.Out, "  %-18s %10s  (%d not in this view — %s)\n", "unpriced", "—", agg.skipped, topUnpriced(agg.skModels, 3))
	}
}

// topUnpriced renders a model histogram most-frequent-first ("claude-opus-4-7
// (7535), <synthetic> (25)"), capped at limit with a "+N more" tail, so a
// coverage gap names itself instead of vanishing from the report.
func topUnpriced(m map[string]int, limit int) string {
	type kv struct {
		k string
		n int
	}
	xs := make([]kv, 0, len(m))
	for k, n := range m {
		xs = append(xs, kv{k, n})
	}
	sort.Slice(xs, func(i, j int) bool {
		if xs[i].n != xs[j].n {
			return xs[i].n > xs[j].n
		}
		return xs[i].k < xs[j].k
	})
	var b strings.Builder
	for i, x := range xs {
		if i == limit {
			fmt.Fprintf(&b, ", +%d more", len(xs)-limit)
			break
		}
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (%d)", termtext.SanitizeLabel(x.k), x.n)
	}
	return b.String()
}

// renderAllocated renders the amortized view: a subscription's prorated
// fee for the window, distributed across groups by api-equivalent share. It is a
// period-level view (subscription_amortized, lower confidence) — an allocation,
// not a metered price.
func (a *App) renderAllocated(events []event.AgentEvent, by string, since, until time.Time, label string, plans config.PlanSet, storeTotal int) {
	// Resolve the proration window. For --all (zero since) amortize over the data's
	// own span, as before; otherwise use the requested [since, until].
	winSince := since
	legacyDays := 0
	if since.IsZero() {
		legacyDays = spanDays(events)
		winSince = spanStart(events)
	} else if d := int(until.Sub(since).Hours() / 24); d > 0 {
		legacyDays = d
	}
	// api-equivalent basis per (provider, group), so each provider's plan fee is
	// amortized across only its own usage — Claude usage vs the Claude plan, Codex
	// usage vs the ChatGPT plan, etc.
	basis := map[string]map[string]int64{}
	providerTotal := map[string]int64{}
	var order []string
	seen := map[string]bool{}
	currency := "USD"
	for _, e := range events {
		m, ok := pickView(e, "api_equivalent")
		if !ok {
			continue
		}
		g := groupKey(e, by)
		if !seen[g] {
			seen[g] = true
			order = append(order, g)
		}
		if basis[e.Provider] == nil {
			basis[e.Provider] = map[string]int64{}
		}
		basis[e.Provider][g] += m.Micros
		providerTotal[e.Provider] += m.Micros
		currency = m.Currency
	}

	fmt.Fprintf(a.Out, "AI-coding spend · %s · by %s · view: amortized (subscription_amortized, confidence 0.70)\n", label, by)
	if len(order) == 0 {
		a.emptyRange(label, storeTotal)
		return
	}

	alloc := map[string]int64{}
	var total int64
	covered := 0
	var uncovered, notYetActive []string
	provs := make([]string, 0, len(basis))
	for p := range basis {
		provs = append(provs, p)
	}
	sort.Strings(provs)
	for _, prov := range provs {
		plan := toPricingPlan(plans.For(prov))
		prorated, ok := proratePlan(plan, winSince, until, legacyDays)
		if !ok || providerTotal[prov] <= 0 {
			if providerTotal[prov] > 0 {
				// A configured plan that simply hasn't started yet is a different
				// story from no plan at all — say so, so the number is explainable.
				if planStartsAfter(plan, until) {
					notYetActive = append(notYetActive, prov)
				} else {
					uncovered = append(uncovered, prov)
				}
			}
			continue
		}
		currency = prorated.Currency
		total += prorated.Micros
		covered++
		for g, m := range pricing.Allocate(prorated, basis[prov]) {
			alloc[g] += m.Micros
		}
	}

	if total == 0 {
		if len(notYetActive) > 0 {
			// A plan exists but hasn't started in this window — explain rather than
			// implying none is configured.
			for _, prov := range notYetActive {
				start := plans.For(prov).StartDate.Format("2006-01-02")
				fmt.Fprintf(a.Out, "  (no spend allocated — %s plan starts %s, after this window)\n", providerLabel(prov), start)
			}
			return
		}
		fmt.Fprintln(a.Out, "  (no subscription plan configured — set `plan` / `<provider>_plan` in ~/.aispend/config.toml; see `aispend plans`)")
		return
	}
	sort.Slice(order, func(i, j int) bool { return alloc[order[i]] > alloc[order[j]] })
	for _, g := range order {
		if alloc[g] == 0 {
			continue
		}
		pct := float64(alloc[g]) / float64(total) * 100
		fmt.Fprintf(a.Out, "  %-26s %10s  %s %3.0f%%\n", displayKey(by, g), usd(alloc[g], currency), bar(pct), pct)
	}
	fmt.Fprintf(a.Out, "  %-26s %10s  (allocation across %d plan(s), not a metered price)\n", "total", usd(total, currency), covered)
	for _, prov := range uncovered {
		fmt.Fprintf(a.Out, "  note: %s usage not allocated — no plan set (add `%s_plan`)\n", providerLabel(prov), prov)
	}
	for _, prov := range notYetActive {
		start := plans.For(prov).StartDate.Format("2006-01-02")
		fmt.Fprintf(a.Out, "  note: %s usage not allocated — plan starts %s, after this window\n", providerLabel(prov), start)
	}
}

// proratePlan computes a plan's prorated fee for the window: billing-cycle aware
// (honoring the plan's start-date anchor) when a start date is known, else the
// flat day-count proration of ProratedFee.
func proratePlan(plan pricing.Plan, since, until time.Time, legacyDays int) (event.Money, bool) {
	if !plan.StartDate.IsZero() {
		return pricing.AmortizeSubscription(plan, since, until)
	}
	return pricing.ProratedFee(plan, legacyDays)
}

// planStartsAfter reports whether a subscription plan has a known start date that
// falls on or after the window end — i.e. it isn't active during the window yet.
func planStartsAfter(plan pricing.Plan, until time.Time) bool {
	return plan.Kind == "subscription" && plan.MonthlyFee != nil &&
		!plan.StartDate.IsZero() && !plan.StartDate.Before(until)
}

// --- plans ---

func (a *App) cmdPlans(_ []string) int {
	// On a terminal, offer the interactive picker (writes the choice to config);
	// otherwise (pipe / offline build) fall back to the static list below.
	if a.maybePickPlan() {
		return 0
	}
	current := ""
	if cfg, err := config.LoadAppConfig(a.Resolver.AppHome()); err == nil {
		current = cfg.Name
	}
	fmt.Fprintln(a.Out, "Known subscription plans (run `aispend plans` in a terminal to pick interactively, or set `plan = \"<id>\"` in ~/.aispend/config.toml):")
	for _, p := range config.Plans() {
		mark := " "
		if p.ID == current {
			mark = "*"
		}
		fmt.Fprintf(a.Out, "  %s %-20s $%6.2f/mo   %s\n", mark, p.ID, p.MonthlyFeeUSD, p.Label)
	}
	fmt.Fprintln(a.Out, "\nOverride any price with `monthly_fee_usd = N`. `*` marks your configured plan.")
	return 0
}

// --- pricing (rate source / refresh) ---

// cmdPricing shows the active rate source, or with `refresh` pulls the LiteLLM
// price table into the local cache. The fetch is the single disclosed inbound
// request; pricing itself stays offline-first (scan/report read the cache, never
// the network).
func (a *App) cmdPricing(args []string) int {
	cache := refresh.CachePath(a.Resolver.AppHome())
	if len(args) > 0 && args[0] == "refresh" {
		return a.cmdPricingRefresh(cache)
	}
	if data, ok := refresh.ReadFreshCache(cache, 24*time.Hour); ok {
		if rates, err := pricing.ParseLiteLLM(data); err == nil {
			fmt.Fprintf(a.Out, "rate source: LiteLLM cache · %d models · fresh (≤24h)\n", len(rates))
			fmt.Fprintf(a.Out, "  %s\n", cache)
			return 0
		}
	}
	fmt.Fprintf(a.Out, "rate source: embedded table %s\n", pricing.NewEngine().TableVersion())
	fmt.Fprintf(a.Out, "  run `aispend pricing refresh` to overlay live LiteLLM rates (one inbound fetch, no data sent)\n")
	return 0
}

func (a *App) cmdPricingRefresh(cache string) int {
	if !refresh.NetworkEnabled {
		fmt.Fprintln(a.Err, "aispend: offline build — pricing refresh is disabled (using embedded table)")
		return 1
	}
	data, err := refresh.Fetch(refresh.LiteLLMURL)
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: pricing refresh: %v\n", err)
		return 1
	}
	models, repriced, err := a.applyPricingTable(cache, data)
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: pricing refresh: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "Refreshed %d model prices from LiteLLM (%s) → cached at %s\n", models, refresh.LiteLLMURL, cache)
	if repriced > 0 {
		fmt.Fprintf(a.Out, "Re-priced %d stored events — report reflects these rates now.\n", repriced)
	} else {
		fmt.Fprintln(a.Out, "No stored events yet — run `aispend scan` and it will price against these rates.")
	}
	return 0
}

// applyPricingTable validates LiteLLM bytes, caches them, and re-prices stored
// events in place so a refresh takes effect immediately. Separated from the
// network fetch so the parse/cache/reprice path is testable offline. Returns the
// model count and the number of events repriced.
func (a *App) applyPricingTable(cache string, data []byte) (models, repriced int, err error) {
	rates, err := pricing.ParseLiteLLM(data)
	if err != nil {
		return 0, 0, err
	}
	if err := refresh.WriteCache(cache, data); err != nil {
		return 0, 0, err
	}
	repriced, err = a.repriceStored(a.pricingEngine())
	if err != nil {
		return 0, 0, err
	}
	return len(rates), repriced, nil
}

// --- git (cost-trailer hooks) ---

// cmdGit installs, removes, or reports the per-commit cost-trailer hooks. The heavy
// lifting (core.hooksPath, refuse-to-clobber, manager detection) lives in
// internal/githook; this stays a thin dispatch. The hooks themselves are fail-open
// so a trailer problem never blocks a commit. See
// design-documents/11-commit-cost-trailers.md.
func (a *App) cmdGit(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "aispend: git <install|uninstall|status> [dir]")
		return 2
	}
	sub, rest := args[0], args[1:]
	dir := "."
	if len(rest) > 0 {
		dir = rest[0]
	}
	var (
		rep githook.Report
		err error
	)
	switch sub {
	case "install":
		rep, err = githook.Install(dir)
	case "uninstall":
		rep, err = githook.Uninstall(dir)
	case "status":
		rep, err = githook.Status(dir)
	default:
		fmt.Fprintf(a.Err, "aispend: unknown git subcommand %q (want install|uninstall|status)\n", sub)
		return 2
	}
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	for _, line := range rep.Render() {
		fmt.Fprintln(a.Out, line)
	}
	return rep.ExitCode()
}

// --- trailer / consume (invoked by the installed git hooks; hidden from help) ---

// cmdTrailer is invoked by prepare-commit-msg as
// `aispend trailer <msgfile> --source <s> [--repo <dir>]`. It ALWAYS exits 0 — a
// trailer problem must never block a commit (fail-open). Args are hand-parsed so
// the positional message file can precede the flags, as the hook passes them.
func (a *App) cmdTrailer(args []string) int {
	msgFile, source, repoDir := "", "", "."
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--source" && i+1 < len(args):
			source = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--source="):
			source = strings.TrimPrefix(args[i], "--source=")
		case args[i] == "--repo" && i+1 < len(args):
			repoDir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--repo="):
			repoDir = strings.TrimPrefix(args[i], "--repo=")
		case strings.HasPrefix(args[i], "-"):
			// ignore unknown flags — fail-open
		default:
			if msgFile == "" {
				msgFile = args[i]
			}
		}
	}
	if msgFile == "" {
		return 0
	}
	tcfg, err := config.LoadTrailers(repoDir)
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: trailer: %v\n", err)
		return 0 // fail-open
	}
	if !tcfg.Enabled {
		return 0 // committed repo-wide off switch
	}
	if err := trailer.Trailer(repoDir, source, msgFile, toTrailerConfig(tcfg), a.pendingUsageLive, a.Now); err != nil {
		fmt.Fprintf(a.Err, "aispend: trailer: %v\n", err)
	}
	return 0
}

// toTrailerConfig maps the parsed .aispend.toml [trailers] config to the engine's
// Config. (Enabled is handled by the caller — it's a gate, not a render option.)
func toTrailerConfig(t config.Trailers) trailer.Config {
	return trailer.Config{
		Cost:         t.Cost,
		CostModels:   t.CostModels,
		Tokens:       t.Tokens,
		Interactions: t.Interactions,
		Precision:    t.Precision,
		CostName:     t.CostName,
	}
}

// cmdConsume is invoked by post-commit as `aispend consume [--repo <dir>]`. Hidden;
// always exits 0.
func (a *App) cmdConsume(args []string) int {
	repoDir := "."
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--repo" && i+1 < len(args):
			repoDir = args[i+1]
			i++
		case strings.HasPrefix(args[i], "--repo="):
			repoDir = strings.TrimPrefix(args[i], "--repo=")
		}
	}
	if err := trailer.Consume(repoDir); err != nil {
		fmt.Fprintf(a.Err, "aispend: consume: %v\n", err)
	}
	return 0
}

// pendingUsage is the real PendingFunc: it sums the api-equivalent cost of stored
// events on `branch` newer than the watermark. (Scanning the live session logs at
// commit time is a later refinement; for now a recent `aispend scan` is the source.)
func (a *App) pendingUsage(_, branch string, since time.Time) (trailer.Usage, error) {
	st, err := a.openStore()
	if err != nil {
		return trailer.Usage{}, err
	}
	events, err := st.Query(store.Filter{Since: since})
	if err != nil {
		return trailer.Usage{}, err
	}
	u := trailer.Usage{PerModel: map[string]int64{}, Cost: event.USD(0)}
	for _, e := range events {
		if !branchMatches(e.GitBranch, branch) || !e.TSStart.After(since) {
			continue
		}
		m := e.CostViews.APIEquivalent
		if m == nil {
			continue
		}
		u.Cost.Micros += m.Micros
		u.Cost.Currency = m.Currency
		u.PerModel[e.Model] += m.Micros
		u.Tokens.Input += e.Tokens.Input
		u.Tokens.Output += e.Tokens.Output
		u.Tokens.CacheRead += e.Tokens.CacheRead
		u.Tokens.CacheWrite += e.Tokens.CacheWrite
		u.Tokens.CacheWrite1h += e.Tokens.CacheWrite1h
		u.Requests++
		if e.TSEnd.After(u.MaxTS) {
			u.MaxTS = e.TSEnd
		}
	}
	return u, nil
}

// branchMatches reports whether a turn tagged evBranch belongs to the branch being
// committed. A turn counts when it names the target branch, or carries a
// non-committal placeholder git never resolved to a real branch: "" (the log omitted
// the field) or "HEAD" (a detached checkout or a Cowork session that recorded the
// symbolic ref verbatim instead of "main"). Placeholders fold into whatever branch
// you commit on, so their cost still reaches a commit — matching what `today`
// already counts. A turn naming a *different* real branch is excluded, so per-branch
// attribution still holds.
func branchMatches(evBranch, target string) bool {
	switch evBranch {
	case target, "", "HEAD":
		return true
	default:
		return false
	}
}

// incrementalScan runs a silent, incremental (watermark-bounded) scan across every
// detected provider and returns the total number of events imported. Best-effort: a
// provider that fails to detect/read/price is skipped, never fatal, so callers fall
// back to whatever the ledger already holds. Incremental (Full:false) — only session
// data newer than the last scan is read — and offline-safe (local files only, no net).
// Shared by the trailer hook's live refresh and the scan-on-launch path.
func (a *App) incrementalScan() int {
	st, err := a.openStore()
	if err != nil {
		return 0
	}
	attr := a.attribution()
	plans := a.planSet()
	eng := a.pricingEngine()
	idh := a.identityHash()
	total := 0
	for _, pr := range a.scanPairs(idh, attr) {
		present, derr := pr.p.Detect()
		if derr != nil || !present {
			continue
		}
		sc := &scan.Scanner{Provider: pr.p, Normalizer: pr.n, Pricing: eng, Plan: toPricingPlan(plans.For(pr.p.Name())), Store: st, Sink: st, Now: a.Now, Full: false}
		sum, runErr := sc.Run()
		if runErr != nil {
			continue // best-effort; a provider error falls back to the existing ledger
		}
		total += sum.Imported
	}
	return total
}

// scanOnLaunch brings the ledger current before a read command (today/report/top/tui)
// renders, so `aispend` "just works" without a remembered `aispend scan`. It is:
//   - opt-out: the per-command --no-scan flag (passed as skip), AISPEND_NO_SCAN in the
//     environment, or scan_on_launch=false in config.toml each disable it;
//   - quiet: a one-line notice on STDERR only when something new was imported, so
//     stdout (report --json, pipes) stays clean and an already-fresh ledger says nothing;
//   - best-effort + offline-safe: incrementalScan swallows provider errors and reads
//     only local files (no network), so a flaky provider never blocks the render.
func (a *App) scanOnLaunch(skip bool) {
	if skip || !a.scanOnLaunchEnabled() {
		return
	}
	n := a.incrementalScan()
	if n <= 0 {
		return
	}
	noun := "turns"
	if n == 1 {
		noun = "turn"
	}
	fmt.Fprintf(a.Err, "scanned %d new %s\n", n, noun)
}

// scanOnLaunchEnabled reports whether the launch scan should run: off when
// AISPEND_NO_SCAN is explicitly truthy (1/true/yes/on), or when scan_on_launch=false in
// config.toml; on by default. The env vocabulary is explicit (not presence-based) so a
// stray AISPEND_NO_SCAN=false/0 reads as "don't disable" rather than silently turning
// scanning off. A malformed config value keeps the safe default (on, freshness) rather
// than silently disabling scanning.
func (a *App) scanOnLaunchEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AISPEND_NO_SCAN"))) {
	case "1", "true", "yes", "on":
		return false // explicitly disabled via the environment
	}
	on, _ := config.LoadScanOnLaunch(a.Resolver.AppHome())
	return on
}

// launchRefreshBudget bounds the one-shot launch top-up so a slow network never hangs a
// short-lived command (report/today/top). The TUI's background worker passes a plain
// context and uses the full client timeout instead, since it never blocks the UI.
const launchRefreshBudget = 2500 * time.Millisecond

// priceFetcher returns the function used to pull the LiteLLM table. Tests inject
// a.fetchPrices to stay hermetic; production falls back to refresh.FetchContext (the one
// disclosed inbound GET — a no-op in the offline build, which never reaches here
// because refreshIfStale gates on refresh.NetworkEnabled first).
func (a *App) priceFetcher() func(context.Context, string) ([]byte, error) {
	if a.fetchPrices != nil {
		return a.fetchPrices
	}
	return refresh.FetchContext
}

// refreshIfStale tops up the LiteLLM price cache when it is missing or older than 24h:
// one inbound fetch (under ctx), then cache + reprice in place (applyPricingTable), so
// reports use rates no more than a day old. It is the shared seam for the launch refresh
// and the TUI's background top-up. Best-effort and silent — it returns (0,false),
// leaving the existing cache or the embedded floor in place, when:
//   - disabled (skip, AISPEND_NO_REFRESH, or refresh_on_launch=false),
//   - the network is compiled out (offline build: refresh.NetworkEnabled=false),
//   - the cache is already fresh (≤24h), or
//   - the fetch/parse fails or ctx expires (offline, timeout, bad payload).
//
// The offline build can never fetch — preserving the provably-offline promise
// (doctor --network discloses it).
func (a *App) refreshIfStale(ctx context.Context, skip bool) (models int, did bool) {
	if skip || !refresh.NetworkEnabled || !a.refreshOnLaunchEnabled() {
		return 0, false
	}
	cache := refresh.CachePath(a.Resolver.AppHome())
	if _, ok := refresh.ReadFreshCache(cache, 24*time.Hour); ok {
		return 0, false // already fresh — no fetch
	}
	data, err := a.priceFetcher()(ctx, refresh.LiteLLMURL)
	if err != nil {
		return 0, false // best-effort: keep the stale cache / embedded floor
	}
	n, _, err := a.applyPricingTable(cache, data)
	if err != nil {
		return 0, false
	}
	return n, true
}

// refreshOnLaunch is the one-shot read-command hook (today/report/top): it keeps prices
// fresh before anything is priced, mirroring scanOnLaunch. The fetch is bounded by
// launchRefreshBudget so a slow network never hangs the command — on timeout it proceeds
// with the cached/embedded rates and the cache simply updates on a later run. Quiet: a
// one-line notice on STDERR only when a refresh actually happened (so stdout / --json
// stays pipe-clean). Opt out with --no-refresh, AISPEND_NO_REFRESH, or
// refresh_on_launch=false. (The TUI refreshes asynchronously instead — see cmdTui.)
func (a *App) refreshOnLaunch(skip bool) {
	ctx, cancel := context.WithTimeout(context.Background(), launchRefreshBudget)
	defer cancel()
	if n, did := a.refreshIfStale(ctx, skip); did {
		fmt.Fprintf(a.Err, "refreshed %d model prices (cache was stale)\n", n)
	}
}

// refreshOnLaunchEnabled reports whether the launch refresh should run: off when
// AISPEND_NO_REFRESH is explicitly truthy (1/true/yes/on), or when
// refresh_on_launch=false in config.toml; on by default. The env vocabulary is explicit
// (not presence-based) so a stray AISPEND_NO_REFRESH=false/0 reads as "don't disable".
// A malformed config value keeps the safe default (on, freshness).
func (a *App) refreshOnLaunchEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("AISPEND_NO_REFRESH"))) {
	case "1", "true", "yes", "on":
		return false // explicitly disabled via the environment
	}
	on, _ := config.LoadRefreshOnLaunch(a.Resolver.AppHome())
	return on
}

// pendingUsageLive is the trailer hook's PendingFunc: it live-scans first, then reads
// the freshened ledger — so a commit stamps turns that were never explicitly scanned.
// today's preview deliberately uses the non-scanning pendingUsage to stay fast.
func (a *App) pendingUsageLive(repoDir, branch string, since time.Time) (trailer.Usage, error) {
	_ = a.incrementalScan()
	return a.pendingUsage(repoDir, branch, since)
}

// --- doctor ---

func (a *App) cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	network := fs.Bool("network", false, "prove no network path is active")
	paths := fs.Bool("paths", false, "show OS-resolved data locations")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*network && !*paths {
		*network = true
	}

	if *paths {
		fmt.Fprintf(a.Out, "os: %s\n", a.Resolver.GOOS)
		fmt.Fprintf(a.Out, "app home: %s\n", a.Resolver.AppHome())
		fmt.Fprintf(a.Out, "events:   %s\n", a.eventsPath())
		existing := map[string]bool{}
		for _, r := range a.Resolver.ExistingRoots("claude_code") {
			existing[r] = true
		}
		fmt.Fprintln(a.Out, "claude_code roots:")
		for _, r := range a.Resolver.ProviderRoots("claude_code") {
			mark := "– missing"
			if existing[r] {
				mark = "✓ exists"
			}
			fmt.Fprintf(a.Out, "  %-44s %s\n", r, mark)
		}
	}
	if *network {
		fmt.Fprintln(a.Out, "default build: no network-capable sink in import graph  ✓")
		// Honest disclosure: the only outbound is an INBOUND price fetch (a public
		// file; no spend, identity, or telemetry leaves the machine). It fires on the
		// explicit `pricing refresh` AND automatically on launch when the cache is
		// >24h old — the latter is on by default but opt-out.
		if refresh.NetworkEnabled {
			fmt.Fprintf(a.Out, "inbound only: GET %s (a public price file; no data sent)\n", refresh.LiteLLMURL)
			fmt.Fprintln(a.Out, "  · on `aispend pricing refresh`, and automatically when the cache is >24h old")
			fmt.Fprintln(a.Out, "  · auto top-up runs in the background in the TUI, and bounded (≤2.5s) on other commands")
			fmt.Fprintln(a.Out, "  · disable the auto top-up: --no-refresh, AISPEND_NO_REFRESH=1, or refresh_on_launch=false")
		} else {
			fmt.Fprintln(a.Out, "offline build: price refresh disabled (no net/* compiled in)")
		}
		fmt.Fprintln(a.Out, "RESULT: PASS — this binary cannot phone home")
	}
	return 0
}

// --- rendering helpers ---

func pickView(e event.AgentEvent, view string) (event.Money, bool) {
	cv := e.CostViews
	var m *event.Money
	switch dash(view) {
	case "api-equivalent":
		m = cv.APIEquivalent
	case "estimated":
		m = cv.Estimated
	case "reported":
		m = cv.Reported
	case "billed":
		m = cv.Billed
	case "amortized":
		m = cv.Amortized
	case "marginal":
		m = cv.Marginal
	default:
		m = cv.APIEquivalent
	}
	if m == nil {
		return event.Money{}, false
	}
	return *m, true
}

func groupKey(e event.AgentEvent, by string) string {
	switch by {
	case "repo":
		if e.Repo != "" {
			return e.Repo
		}
		return "(no repo)"
	case "provider":
		return e.Provider
	case "cost_tag":
		if e.CostTag != "" {
			return e.CostTag
		}
		return "(untagged)"
	case "session":
		if e.SessionID != "" {
			return e.SessionID
		}
		return "(no session)"
	case "branch":
		if e.GitBranch != "" {
			return e.GitBranch
		}
		return "(no branch)"
	case "commit":
		if e.GitSHA != "" {
			return e.GitSHA
		}
		return "(no commit)"
	default:
		return e.Model
	}
}

// displayKey shortens a group key for the table only — currently just session
// ids, which are long UUIDs. The full id is preserved everywhere it matters
// (grouping, JSON, `explain session:<id>` prefix-matching); other dimensions pass
// through unchanged.
//
// Keys are lifted verbatim from session logs (branch, repo, model, file, cost_tag)
// or are id prefixes, so each is sanitized at this render boundary — a poisoned
// value must not inject terminal escape sequences (CWE-150). Only the displayed
// copy is scrubbed; the stored key that drives grouping is untouched.
func displayKey(by, key string) string {
	switch by {
	case "session":
		return termtext.SanitizeLabel(shortSession(key))
	case "commit":
		return termtext.SanitizeLabel(shortSHA(key))
	}
	return termtext.SanitizeLabel(key)
}

// shortSHA renders a commit as its first 10 hex chars — enough to identify and to
// paste into a future `explain commit:<prefix>` — leaving sentinel buckets like
// "(no commit)" untouched. The full SHA is preserved in grouping and JSON.
func shortSHA(sha string) string {
	if strings.HasPrefix(sha, "(") {
		return sha
	}
	if r := []rune(sha); len(r) > 10 {
		return string(r[:10])
	}
	return sha
}

// shortSession renders a session id as a short, copy-pasteable prefix — enough to
// disambiguate and to feed `explain session:<prefix>` — leaving sentinel buckets
// like "(no session)" untouched.
func shortSession(id string) string {
	if strings.HasPrefix(id, "(") { // sentinel bucket, e.g. "(no session)"
		return id
	}
	if r := []rune(id); len(r) > 8 {
		return string(r[:8]) + "…"
	}
	return id
}

func usd(micros int64, currency string) string {
	v := float64(micros) / 1e6
	if currency == "USD" || currency == "" {
		return fmt.Sprintf("$%.2f", v)
	}
	return fmt.Sprintf("%.2f %s", v, currency)
}

func bar(pct float64) string {
	n := int(pct/10 + 0.5)
	if n > 10 {
		n = 10
	}
	if n < 0 {
		n = 0
	}
	return strings.Repeat("▓", n) + strings.Repeat("·", 10-n)
}

func dash(s string) string { return strings.ReplaceAll(s, "_", "-") }

func providerLabel(p string) string {
	switch p {
	case "claude_code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "cursor":
		return "Cursor"
	default:
		return p
	}
}

// methodLabel summarizes the cost methods across a report: the single method if
// uniform, else "mixed" (e.g. high-confidence token_priced Claude + inferred Codex).
func methodLabel(methods map[string]bool) string {
	switch len(methods) {
	case 0:
		return "—"
	case 1:
		for m := range methods {
			return m
		}
	}
	return "mixed"
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// startOfWeek returns the Monday 00:00 of t's week (ISO 8601 week start), in t's
// own location.
func startOfWeek(t time.Time) time.Time {
	offset := (int(t.Weekday()) + 6) % 7 // days since Monday (Sun=0 → 6, Mon=1 → 0)
	return startOfDay(t).AddDate(0, 0, -offset)
}

func startOfMonth(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
}

// spanDays returns the number of days spanned by the events' timestamps (min 1),
// used to amortize a subscription over `--all` history.
func spanDays(events []event.AgentEvent) int {
	var lo, hi time.Time
	for _, e := range events {
		if e.TSStart.IsZero() { // a malformed event must not stretch the span to year 1
			continue
		}
		if lo.IsZero() || e.TSStart.Before(lo) {
			lo = e.TSStart
		}
		if e.TSStart.After(hi) {
			hi = e.TSStart
		}
	}
	if lo.IsZero() {
		return 1
	}
	if d := int(hi.Sub(lo).Hours()/24) + 1; d > 1 {
		return d
	}
	return 1
}

// spanStart returns the earliest event timestamp — the window start used to
// amortize a subscription over --all history. Zero when there are no events.
func spanStart(events []event.AgentEvent) time.Time {
	var lo time.Time
	for _, e := range events {
		if e.TSStart.IsZero() { // skip malformed events so they can't anchor the window to year 1
			continue
		}
		if lo.IsZero() || e.TSStart.Before(lo) {
			lo = e.TSStart
		}
	}
	return lo
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
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

func (a *App) usage() {
	fmt.Fprintf(a.Out, `aispend %s — local, explainable AI-coding spend

Usage: aispend <command>   (no command opens the interactive TUI; off a TTY it shows `+"`today`"+`)

  scan [--verbose]              import & price new sessions (no network); --verbose shows skips
  report [--period P] [flags]   spend over a calendar window (default: this week)
  today                         arbitrage-first daily glance: ROI, cache savings, hourly spikes
  budget [set <amt>|clear]      monthly spend ceiling: bare shows pace; set/clear manage it (--json, --strict)
  top [--period P] [--sessions] priciest turns (or sessions) in a window
  tui [--period P]              interactive explorer: arrow sessions, ↵ to drill to the receipt → file → turn evidence (not in offline build)
  doctor [--network] [--paths]  prove the trust promise / show data locations
  plans                         list known subscription plans (seeded prices)
  pricing [refresh]             show the active rate source; 'refresh' pulls live LiteLLM rates
  git <install|status|…>        install per-commit cost-trailer hooks (safe; honors hook managers)
  version                       print version

  today/report/top/tui/budget scan new sessions on launch first; --no-scan reads the ledger as-is
  (or set scan_on_launch = false in ~/.aispend/config.toml, or AISPEND_NO_SCAN=1)

  report flags: --period P  --by G  --view V  --json
  P (period): today | yesterday | week | month | "last week" | "last month" |
              quarter | "last quarter" | "this year" | "last year" | "N days" (e.g. "90 days") |
              "since YYYY-MM-DD" | YYYY-MM-DD..YYYY-MM-DD | all   (always calendar time, never rolling)
  G (group):  model | repo | provider | cost_tag | session | branch | commit | file
  V (view):   api_equivalent | reported | estimated | billed | amortized | marginal
  --json:     emit the report as JSON instead of a table (metered views only)

  examples:
    aispend report --period today
    aispend report --period "last month" --by cost_tag
    aispend report --period "90 days" --view amortized
    aispend report --period 2026-01-01..2026-03-31 --by repo --json

Report bugs to: %s
Home page: %s
`, Version, Issues, Website)
}
