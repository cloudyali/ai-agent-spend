package cli

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
)

func twoPriced() []event.AgentEvent {
	opus := event.USD(431850)
	sonnet := event.USD(17250)
	return []event.AgentEvent{
		{Model: "claude-opus-4", Provider: "claude_code", Repo: "payments",
			CostViews: event.CostViews{APIEquivalent: &opus}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
		{Model: "claude-sonnet-4", Provider: "claude_code", Repo: "payments",
			CostViews: event.CostViews{APIEquivalent: &sonnet}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
	}
}

// buildReportResult is the shared projection behind `report --json`. Asserted
// directly (not via the clock-dependent CLI path) so the numbers are deterministic.
func TestBuildReportResult_TwoGroups(t *testing.T) {
	win, _ := parsePeriod("all", fixedNow())
	r := buildReportResult(twoPriced(), "model", "api_equivalent", win, pricing.NewEngine())

	if r.Period != "all time" || r.GroupBy != "model" || r.View != "api_equivalent" || r.Currency != "USD" {
		t.Errorf("meta wrong: %+v", r)
	}
	if r.Since != nil {
		t.Errorf("all-time since should be nil, got %v", r.Since)
	}
	if r.Count != 2 || len(r.Groups) != 2 {
		t.Fatalf("count=%d groups=%d, want 2/2", r.Count, len(r.Groups))
	}
	if r.Method != "token_priced" || r.Confidence != 0.95 {
		t.Errorf("method=%q confidence=%v", r.Method, r.Confidence)
	}
	// sorted by cost desc; opus (431850) before sonnet (17250)
	if r.Groups[0].Key != "claude-opus-4" || r.Groups[0].CostMicros <= r.Groups[1].CostMicros {
		t.Errorf("groups not sorted desc: %+v", r.Groups)
	}
	var sum int64
	var pct float64
	for _, g := range r.Groups {
		sum += g.CostMicros
		pct += g.Percent
		if g.CostUSD != float64(g.CostMicros)/1e6 {
			t.Errorf("cost_usd/micros mismatch: %+v", g)
		}
	}
	if sum != r.TotalMicros || r.TotalMicros != 449100 {
		t.Errorf("group sum=%d total=%d, want 449100", sum, r.TotalMicros)
	}
	if r.TotalUSD != float64(r.TotalMicros)/1e6 {
		t.Errorf("total_usd=%v", r.TotalUSD)
	}
	if math.Abs(pct-100) > 0.1 {
		t.Errorf("percents sum to %v, want ~100", pct)
	}
}

// An unknown model (no api_equivalent) must surface in JSON too, never silently
// vanish — the claude-opus-4-7 regression, JSON edition.
func TestBuildReportResult_Unpriced(t *testing.T) {
	opus := event.USD(431850)
	events := []event.AgentEvent{
		{Model: "claude-opus-4-8", Provider: "claude_code", CostViews: event.CostViews{APIEquivalent: &opus}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
		{Model: "claude-opus-4-7", Provider: "claude_code"},
		{Model: "claude-opus-4-7", Provider: "claude_code"},
		{Model: "<synthetic>", Provider: "claude_code"},
	}
	r := buildReportResult(events, "model", "api_equivalent", mustWindow(t, "all"), pricing.NewEngine())
	if r.Count != 1 {
		t.Errorf("priced count=%d, want 1", r.Count)
	}
	if r.Unpriced == nil || r.Unpriced.Count != 3 {
		t.Fatalf("unpriced=%+v, want count 3", r.Unpriced)
	}
	if r.Unpriced.Models["claude-opus-4-7"] != 2 || r.Unpriced.Models["<synthetic>"] != 1 {
		t.Errorf("unpriced models=%v", r.Unpriced.Models)
	}
}

func TestBuildReportResult_RangeSetsSince(t *testing.T) {
	win := mustWindow(t, "2026-01-01..2026-03-31")
	r := buildReportResult(twoPriced(), "provider", "api_equivalent", win, pricing.NewEngine())
	if r.Since == nil || !r.Since.Equal(win.Since) {
		t.Errorf("range since = %v, want %v", r.Since, win.Since)
	}
	if !r.Until.Equal(win.Until) {
		t.Errorf("until = %v, want %v", r.Until, win.Until)
	}
	if r.GroupBy != "provider" || len(r.Groups) != 1 || r.Groups[0].Key != "claude_code" {
		t.Errorf("group by provider wrong: %+v", r.Groups)
	}
}

// Per-group + total component breakdown, reconciling exactly with cost_micros
// for the api_equivalent view (priced via the same engine that decomposes).
func TestBuildReportResult_Components(t *testing.T) {
	eng := pricing.NewEngine()
	mk := func(in, out, cr, cw int64) event.AgentEvent {
		ev := event.AgentEvent{Model: "claude-opus-4-8", Provider: "claude_code",
			Tokens: event.Tokens{Input: in, Output: out, CacheRead: cr, CacheWrite: cw}}
		_ = eng.Price(&ev, pricing.Plan{Kind: "api"})
		return ev
	}
	events := []event.AgentEvent{
		mk(1_000_000, 1_000_000, 10_000_000, 1_000_000),
		mk(0, 0, 10_000_000, 0),
	}
	r := buildReportResult(events, "model", "api_equivalent", mustWindow(t, "all"), eng)
	if len(r.Groups) != 1 {
		t.Fatalf("groups=%d, want 1", len(r.Groups))
	}
	g := r.Groups[0]
	if g.Components == nil {
		t.Fatal("expected per-group cost components")
	}
	// cache-read: (10M+10M) tokens × $0.50/Mtok = $10.00 = 10_000_000 micros
	if g.Components.CacheRead.Micros != 10_000_000 {
		t.Errorf("cache-read micros=%d, want 10000000", g.Components.CacheRead.Micros)
	}
	// the four lines must reconcile with the group's api-equivalent total
	sum := g.Components.Input.Micros + g.Components.Output.Micros + g.Components.CacheRead.Micros + g.Components.CacheWrite.Micros
	if sum != g.CostMicros {
		t.Errorf("components sum %d != cost_micros %d", sum, g.CostMicros)
	}
	if r.TotalComponents == nil || r.TotalComponents.CacheRead.Micros != 10_000_000 {
		t.Errorf("total components = %+v", r.TotalComponents)
	}
	if r.TotalComponents.CacheRead.USD != 10.0 {
		t.Errorf("cache-read usd=%v, want 10.0", r.TotalComponents.CacheRead.USD)
	}
}

// The 1-hour cache tier surfaces as its own component, priced at 2× input.
func TestBuildReportResult_Components1h(t *testing.T) {
	eng := pricing.NewEngine()
	ev := event.AgentEvent{Model: "claude-opus-4-8", Provider: "claude_code",
		Tokens: event.Tokens{CacheWrite: 2_000_000, CacheWrite1h: 1_000_000}}
	_ = eng.Price(&ev, pricing.Plan{Kind: "api"})
	r := buildReportResult([]event.AgentEvent{ev}, "model", "api_equivalent", mustWindow(t, "all"), eng)
	if len(r.Groups) != 1 || r.Groups[0].Components == nil {
		t.Fatalf("expected one group with components: %+v", r.Groups)
	}
	g := r.Groups[0].Components
	if g.CacheWrite1h.Micros != 10_000_000 { // 1M × $10 (2× input)
		t.Errorf("1-hour component = %d, want 10000000", g.CacheWrite1h.Micros)
	}
	if g.CacheWrite.Micros != 6_250_000 { // 1M five-minute × $6.25
		t.Errorf("5-min component = %d, want 6250000", g.CacheWrite.Micros)
	}
	if b, _ := json.Marshal(g); !strings.Contains(string(b), "cache_write_1h") {
		t.Errorf("json missing cache_write_1h: %s", b)
	}
}

// Components are an api-equivalent decomposition, so they must be omitted for
// non-token-priced views (e.g. reported) where they wouldn't reconcile.
func TestBuildReportResult_ComponentsGatedByView(t *testing.T) {
	eng := pricing.NewEngine()
	reported := event.USD(500000)
	ev := event.AgentEvent{Model: "claude-opus-4-8", Provider: "claude_code",
		Tokens:    event.Tokens{Input: 1_000_000},
		CostViews: event.CostViews{Reported: &reported}}
	r := buildReportResult([]event.AgentEvent{ev}, "model", "reported", mustWindow(t, "all"), eng)
	if len(r.Groups) != 1 {
		t.Fatalf("groups=%d, want 1", len(r.Groups))
	}
	if r.Groups[0].Components != nil || r.TotalComponents != nil {
		t.Error("components must be omitted for the reported (non-token-priced) view")
	}
}

// gitEvent builds a priced event carrying branch/commit/file provenance.
func gitEvent(apiMicros int64, branch, sha string, files ...string) event.AgentEvent {
	m := event.USD(apiMicros)
	return event.AgentEvent{
		Model: "claude-opus-4", Provider: "claude_code",
		GitBranch: branch, GitSHA: sha, Files: files,
		CostViews: event.CostViews{APIEquivalent: &m},
		Evidence:  event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95},
	}
}

func rowByKey(agg reportAgg, key string) (*aggRow, bool) {
	for _, r := range agg.rows {
		if r.key == key {
			return r, true
		}
	}
	return nil, false
}

// --by branch / --by commit are ordinary 1:1 groupings (one row per event), so
// their totals must reconcile exactly with the same window's --by model total —
// the no-double-count / no-drop invariant. Empty provenance buckets to a sentinel.
func TestAggregateReport_ByBranchAndCommit(t *testing.T) {
	events := []event.AgentEvent{
		gitEvent(100_000, "main", "aaaaaaaaaaaa1111"),
		gitEvent(200_000, "feature/x", "bbbbbbbbbbbb2222"),
		gitEvent(50_000, "", ""), // no branch / no commit → sentinels
	}
	byModel := aggregateReport(events, "model", "api_equivalent")

	for _, tc := range []struct {
		by, sentinel string
		want         map[string]int64
	}{
		{"branch", "(no branch)", map[string]int64{"main": 100_000, "feature/x": 200_000, "(no branch)": 50_000}},
		{"commit", "(no commit)", map[string]int64{"aaaaaaaaaaaa1111": 100_000, "bbbbbbbbbbbb2222": 200_000, "(no commit)": 50_000}},
	} {
		t.Run(tc.by, func(t *testing.T) {
			agg := aggregateReport(events, tc.by, "api_equivalent")
			if agg.total != byModel.total {
				t.Errorf("--by %s total %d != by-model total %d (must reconcile)", tc.by, agg.total, byModel.total)
			}
			for key, want := range tc.want {
				r, ok := rowByKey(agg, key)
				if !ok || r.micros != want {
					t.Errorf("--by %s row %q = %v (want %d)", tc.by, key, r, want)
				}
			}
		})
	}
}

// --by file fans out: a turn touching N files contributes to N rows, its cost split
// across them so the file rows still sum to the grand total (reconciliation holds);
// a fileless turn lands in "(no files)". Per-row count is the number of touching turns.
func TestAggregateReport_ByFileFanOut(t *testing.T) {
	events := []event.AgentEvent{
		gitEvent(300_000, "", "", "a.go", "b.go", "c.go"), // 100k each
		gitEvent(100_000, "", "", "a.go"),                 // a.go again
		gitEvent(70_000, "", ""),                          // no files
	}
	byModel := aggregateReport(events, "model", "api_equivalent")
	agg := aggregateReport(events, "file", "api_equivalent")

	if agg.total != byModel.total || agg.total != 470_000 {
		t.Fatalf("file total %d != model total %d (want 470000)", agg.total, byModel.total)
	}
	var sum int64
	for _, r := range agg.rows {
		sum += r.micros
	}
	if sum != agg.total {
		t.Errorf("file rows sum %d != total %d (split must reconcile)", sum, agg.total)
	}
	if r, _ := rowByKey(agg, "a.go"); r == nil || r.micros != 200_000 || r.count != 2 {
		t.Errorf("a.go row = %+v, want micros=200000 count=2", r)
	}
	if r, _ := rowByKey(agg, "(no files)"); r == nil || r.micros != 70_000 {
		t.Errorf("(no files) row = %+v, want micros=70000", r)
	}
	if agg.n != 3 {
		t.Errorf("priced event count = %d, want 3 (count is events, not touches)", agg.n)
	}
}

// The integer split must reconcile exactly even when cost doesn't divide evenly;
// the remainder lands on the first (sorted) file rather than vanishing.
func TestAggregateReport_ByFileSplitRemainder(t *testing.T) {
	agg := aggregateReport([]event.AgentEvent{gitEvent(301_000, "", "", "a.go", "b.go", "c.go")}, "file", "api_equivalent")
	var sum int64
	for _, r := range agg.rows {
		sum += r.micros
	}
	if sum != 301_000 {
		t.Errorf("remainder lost: rows sum %d, want 301000", sum)
	}
	if r, _ := rowByKey(agg, "a.go"); r == nil || r.micros != 100_334 {
		t.Errorf("first file = %+v, want 100334 (100333 + remainder 1... )", r)
	}
}

func mustWindow(t *testing.T, spec string) window {
	t.Helper()
	w, err := parsePeriod(spec, fixedNow())
	if err != nil {
		t.Fatalf("parsePeriod(%q): %v", spec, err)
	}
	return w
}

// End-to-end through the CLI: valid, indented JSON with the right metadata, and a
// self-consistent total. Row counts are clock-dependent, so we don't assert them.
func TestReportJSON_CLI(t *testing.T) {
	setupHome(t)
	run(t, "scan")

	out, _, code := run(t, "report", "--period", "all", "--json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "\n  ") {
		t.Errorf("expected indented JSON, got:\n%s", out)
	}
	var r reportResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if r.Period != "all time" || r.GroupBy != "model" || r.View != "api_equivalent" {
		t.Errorf("meta wrong: %+v", r)
	}
	var sum int64
	for _, g := range r.Groups {
		sum += g.CostMicros
	}
	if sum != r.TotalMicros {
		t.Errorf("group micros %d != total %d", sum, r.TotalMicros)
	}
}

// End-to-end through the CLI: --by file fans out yet the file rows still reconcile
// to the grand total, exercising cmdReport → buildReportResult on the real ledger.
func TestReportJSON_ByFileCLI(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	out, _, code := run(t, "report", "--period", "all", "--by", "file", "--json")
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	var r reportResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if r.GroupBy != "file" {
		t.Errorf("group_by = %q, want file", r.GroupBy)
	}
	var sum int64
	for _, g := range r.Groups {
		sum += g.CostMicros
	}
	if sum != r.TotalMicros {
		t.Errorf("file rows %d != total %d (fan-out split must reconcile)", sum, r.TotalMicros)
	}
}

// The branch/commit facets run through the CLI table path without error.
func TestReport_ByBranchCommitCLI(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	for _, by := range []string{"branch", "commit"} {
		out, _, code := run(t, "report", "--period", "all", "--by", by)
		if code != 0 || !strings.Contains(out, "by "+by) {
			t.Errorf("report --by %s: code=%d out=%s", by, code, out)
		}
	}
}

func TestReportJSON_Empty(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	// A window that predates the fixture (2026) — guaranteed empty regardless of clock.
	out, _, code := run(t, "report", "--period", "2000-01-01..2000-01-02", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var r reportResult
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if r.Count != 0 || len(r.Groups) != 0 {
		t.Errorf("expected empty, got count=%d groups=%d", r.Count, len(r.Groups))
	}
	if r.Period != "2000-01-01 → 2000-01-02" {
		t.Errorf("period=%q", r.Period)
	}
}

func TestReportJSON_AllocatedUnsupported(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	_, errs, code := run(t, "report", "--period", "all", "--view", "amortized", "--json")
	if code != 2 {
		t.Errorf("exit=%d, want 2", code)
	}
	if !strings.Contains(errs, "amortized") {
		t.Errorf("stderr should name the unsupported view: %s", errs)
	}
}
