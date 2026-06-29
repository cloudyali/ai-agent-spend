// `aispend budget` — manage and read the optional monthly spend ceiling. A budget
// is informational, never enforced: aispend observes, it never blocks (that wall is
// the provider quota window, not this). The bare command shows month-to-date PACE
// (spent + the run-rate projection vs the ceiling, reusing the same budgetPace that
// `today` and the TUI render); `set`/`clear` write the ceiling to ~/.aispend/config.toml,
// giving the existing config.SetBudget a CLI surface. --json emits the pace for
// scripting; --strict turns an over-pace projection into a non-zero exit so a budget
// can gate a CI step. Off by default — measured against api-equivalent only, with any
// provider lacking an api-equivalent cost excluded and disclosed.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/budget"
	"github.com/cloudyali/ai-agent-spend/internal/config"
)

// cmdBudget dispatches the budget surface: `set <amount>` and `clear` manage the
// ceiling (pure config writes — no scan), while a bare `aispend budget` (with optional
// --json/--strict) reads the month-to-date pace. set/clear are matched positionally
// before any flag parsing so a negative amount (`set -5`) reaches parseUSDAmount as a
// value, not an unknown flag.
func (a *App) cmdBudget(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "set":
			return a.cmdBudgetSet(args[1:])
		case "clear", "unset":
			return a.cmdBudgetClear(args[1:])
		}
	}

	fs := flag.NewFlagSet("budget", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	jsonOut := fs.Bool("json", false, "emit the budget pace as JSON instead of a table")
	strict := fs.Bool("strict", false, "exit non-zero when the run-rate projects over budget (for CI gating)")
	noScan := fs.Bool("no-scan", false, "skip the automatic scan-on-launch; read the ledger as-is")
	noRefresh := fs.Bool("no-refresh", false, "skip the automatic price refresh-on-launch; use cached/embedded rates as-is")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	a.refreshOnLaunch(*noRefresh)
	a.scanOnLaunch(*noScan)
	now := a.Now()
	st, err := a.openStore()
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}

	p, uncovered, ok := a.budgetPace(st, now)
	if !ok {
		if *jsonOut {
			return a.emitBudgetJSON(budgetResult{Configured: false})
		}
		fmt.Fprintln(a.Out, "No budget configured. Set a monthly api-equivalent ceiling with `aispend budget set <amount>` (e.g. `aispend budget set $500`).")
		return 0 // no budget is a valid state, not an error — and nothing for --strict to trip on
	}

	if *jsonOut {
		if rc := a.emitBudgetJSON(buildBudgetResult(p, uncovered, now)); rc != 0 {
			return rc
		}
	} else {
		a.renderBudgetStatus(p, uncovered)
	}
	if *strict && p.OverPace() {
		return 1
	}
	return 0
}

// cmdBudgetSet writes a positive monthly ceiling. A non-positive or unparseable
// amount is a usage error (exit 2) rather than a silently written non-positive value
// (which LoadBudget would treat as "unset"); to remove a budget, `clear` says so.
func (a *App) cmdBudgetSet(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.Err, "aispend: budget set <amount>   (e.g. `aispend budget set $500`)")
		return 2
	}
	micros, err := parseUSDAmount(args[0])
	if err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 2
	}
	if micros <= 0 {
		fmt.Fprintln(a.Err, "aispend: budget must be a positive dollar amount (use `aispend budget clear` to remove it)")
		return 2
	}
	if err := config.SetBudget(a.Resolver.AppHome(), micros); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.Out, "Budget set to %s/mo — a monthly api-equivalent ceiling (informational; aispend never blocks).\n", usd(micros, "USD"))
	return 0
}

// cmdBudgetClear removes the ceiling by writing a non-positive value, which LoadBudget
// reads back as "unset" — pace tracking then goes quiet in `budget`, `today`, and the TUI.
func (a *App) cmdBudgetClear(_ []string) int {
	if err := config.SetBudget(a.Resolver.AppHome(), 0); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Out, "Budget cleared — pace tracking off (set one again with `aispend budget set <amount>`).")
	return 0
}

// parseUSDAmount reads a human dollar amount — an optional leading `$`, thousands
// commas, and a decimal — into integer micros, rounding to the nearest micro. The sign
// is preserved (a negative parses cleanly) so the caller can reject non-positive with a
// budget-specific message rather than this returning an opaque format error. Only
// genuinely non-numeric or non-finite input errors here.
func parseUSDAmount(s string) (int64, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimPrefix(t, "$")
	t = strings.ReplaceAll(t, ",", "")
	t = strings.TrimSpace(t)
	if t == "" {
		return 0, fmt.Errorf("budget %q: empty amount", s)
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, fmt.Errorf("budget %q: not a dollar amount", s)
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, fmt.Errorf("budget %q: not a finite amount", s)
	}
	// Bound before the ×1e6 micros conversion so an absurd magnitude can't overflow
	// int64 into a garbage (possibly negative) ceiling. $1T/mo is far beyond any real
	// budget yet leaves f*1e6 well inside int64.
	if f > maxBudgetDollars || f < -maxBudgetDollars {
		return 0, fmt.Errorf("budget %q: amount out of range", s)
	}
	return int64(math.Round(f * 1_000_000)), nil
}

// maxBudgetDollars caps a parsed amount so the micros conversion stays within int64
// (max ≈ 9.2e18 micros ⇒ ≈ 9.2e12 dollars); $1T/mo is comfortably under and absurd
// enough that anything larger is a typo, not a ceiling.
const maxBudgetDollars = 1e12

// renderBudgetStatus prints the standalone month-to-date pace: the ceiling, spend with
// a used-fraction bar, the run-rate projection with its glanceable verdict, and a
// disclosure line for any provider excluded from the sum (no api-equivalent).
func (a *App) renderBudgetStatus(p budget.Pace, uncovered []string) {
	color := useColor(a.Out)
	usedPct := p.UsedFraction() * 100
	fmt.Fprintf(a.Out, "%s · this month\n\n", paint(color, cBold, "aispend budget"))
	fmt.Fprintf(a.Out, "  ceiling    %s/mo\n", usd(p.Limit, "USD"))
	fmt.Fprintf(a.Out, "  spent      %s  %s  %.0f%% used · %.0f%% of month elapsed\n",
		usd(p.Spent, "USD"), bar(usedPct), usedPct, p.ElapsedFraction*100)
	proj := fmt.Sprintf("  projected  %s/mo at this rate", usd(p.Projected, "USD"))
	if s := p.Status(); s != "" {
		proj += "  ·  " + s
	}
	fmt.Fprintln(a.Out, proj)
	if len(uncovered) > 0 {
		fmt.Fprintf(a.Out, "  note: %s excluded from the budget (no api-equivalent)\n", joinProviderLabels(uncovered))
	}
}

// budgetResult is the `budget --json` projection: each money figure as micros + USD,
// the calendar month it covers, and the derived verdict. The numeric fields are always
// emitted (a stable schema for scripts/CI — a legitimately-zero spend stays present, not
// dropped); only the string and slice fields are omitted when empty, so the unconfigured
// shape stays terse: {"configured": false, ...zeros..., "over_pace": false}.
type budgetResult struct {
	Configured      bool     `json:"configured"`
	Month           string   `json:"month,omitempty"` // YYYY-MM the pace covers
	LimitMicros     int64    `json:"limit_micros"`
	LimitUSD        float64  `json:"limit_usd"`
	SpentMicros     int64    `json:"spent_micros"`
	SpentUSD        float64  `json:"spent_usd"`
	ProjectedMicros int64    `json:"projected_micros"`
	ProjectedUSD    float64  `json:"projected_usd"`
	UsedFraction    float64  `json:"used_fraction"`
	ElapsedFraction float64  `json:"elapsed_fraction"`
	PaceRatio       float64  `json:"pace_ratio"`
	OverPace        bool     `json:"over_pace"`
	Status          string   `json:"status,omitempty"`
	Uncovered       []string `json:"uncovered_providers,omitempty"`
}

// buildBudgetResult projects a Pace into the JSON shape, deriving the month label from
// now (the same clock budgetPace bucketed against).
func buildBudgetResult(p budget.Pace, uncovered []string, now time.Time) budgetResult {
	return budgetResult{
		Configured:      true,
		Month:           now.Format("2006-01"),
		LimitMicros:     p.Limit,
		LimitUSD:        microsToUSD(p.Limit),
		SpentMicros:     p.Spent,
		SpentUSD:        microsToUSD(p.Spent),
		ProjectedMicros: p.Projected,
		ProjectedUSD:    microsToUSD(p.Projected),
		UsedFraction:    p.UsedFraction(),
		ElapsedFraction: p.ElapsedFraction,
		PaceRatio:       p.PaceRatio(),
		OverPace:        p.OverPace(),
		Status:          p.Status(),
		Uncovered:       uncovered,
	}
}

// emitBudgetJSON writes a budgetResult as indented JSON (matching report --json's style).
func (a *App) emitBudgetJSON(r budgetResult) int {
	enc := json.NewEncoder(a.Out)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintf(a.Err, "aispend: %v\n", err)
		return 1
	}
	return 0
}
