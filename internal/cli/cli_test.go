package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
	"github.com/cloudyali/ai-agent-spend/internal/pricing"
	"github.com/cloudyali/ai-agent-spend/internal/pricing/refresh"
	"github.com/cloudyali/ai-agent-spend/internal/store"
)

func apiPlans() config.PlanSet {
	return config.PlanSet{Default: config.Plan{Kind: "api"}, ByProvider: map[string]config.Plan{}}
}

const sessionJSONL = `{"type":"user","sessionId":"s","message":{"role":"user","content":[]}}
{"type":"assistant","uuid":"a1","sessionId":"s","timestamp":"2026-06-14T10:00:05Z","cwd":"/x/payments","message":{"id":"m1","model":"claude-opus-4-20250514","content":[{"type":"tool_use","name":"Edit"}],"usage":{"input_tokens":12400,"output_tokens":3100,"cache_read_input_tokens":8900}}}
{"type":"assistant","uuid":"a2","sessionId":"s","timestamp":"2026-06-14T10:01:00Z","cwd":"/x/payments","message":{"id":"m2","model":"claude-sonnet-4","content":[],"usage":{"input_tokens":2000,"output_tokens":500}}}
{"type":"summary","summary":"done"}
`

func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	dir := filepath.Join(home, ".claude", "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(sessionJSONL), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	// Hermetic by default: the launch auto-refresh must never reach the real network
	// from a test. Tests that exercise the refresh path do so via injected fetchers
	// (see refresh_on_launch_test.go), not through run(). A test may set the env
	// explicitly to override this default.
	if os.Getenv("AISPEND_NO_REFRESH") == "" {
		t.Setenv("AISPEND_NO_REFRESH", "1")
	}
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	return out.String(), errb.String(), code
}

func TestRun_EndToEnd(t *testing.T) {
	setupHome(t)

	// scan
	out, errs, code := run(t, "scan")
	if code != 0 {
		t.Fatalf("scan exit %d, stderr=%s", code, errs)
	}
	if !strings.Contains(out, "Imported 2 events") {
		t.Errorf("scan output missing import count:\n%s", out)
	}
	if !strings.Contains(out, "no network calls made") {
		t.Errorf("scan should affirm offline:\n%s", out)
	}

	// report (default period = this week). Header is clock-independent; rows depend
	// on the clock so we only assert it runs and labels the window.
	out, _, code = run(t, "report")
	if code != 0 || !strings.Contains(out, "AI-coding spend") || !strings.Contains(out, "this week") {
		t.Errorf("report failed: code=%d out=%s", code, out)
	}

	// explain has been removed — per-turn/session evidence now lives in the TUI, so
	// the CLI treats `explain` as any other unknown command (exit 2).
	if _, _, c := run(t, "explain", "evt_whatever"); c != 2 {
		t.Errorf("explain should be an unknown command now, exit=%d want 2", c)
	}

	// doctor --network
	if out, _, code = run(t, "doctor", "--network"); code != 0 || !strings.Contains(out, "PASS") {
		t.Errorf("doctor failed: %s", out)
	}

	// version + unknown command
	if out, _, _ = run(t, "version"); !strings.Contains(out, "aispend "+Version) {
		t.Errorf("version: %s", out)
	}
	if out, _, _ = run(t, "version"); !strings.Contains(out, "Home page: "+Website) {
		t.Errorf("version banner should show home page %q: %s", Website, out)
	}
	if out, _, _ = run(t, "version"); !strings.Contains(out, "Report bugs to: "+Issues) {
		t.Errorf("version banner should report bugs to %q: %s", Issues, out)
	}
	if out, _, _ = run(t, "help"); !strings.Contains(out, Website) {
		t.Errorf("help banner should show the website %q: %s", Website, out)
	}
	if out, _, _ = run(t, "help"); !strings.Contains(out, "Report bugs to: "+Issues) {
		t.Errorf("help footer should report bugs to %q: %s", Issues, out)
	}
	if out, _, _ = run(t, "help"); !strings.Contains(out, "Home page: "+Website) {
		t.Errorf("help footer should show home page %q: %s", Website, out)
	}
	if _, _, code = run(t, "bogus"); code != 2 {
		t.Errorf("unknown command exit = %d, want 2", code)
	}
}

// A fresh LiteLLM cache overlays the embedded table: `pricing` reports the source.
// (The overlay's effect on actual pricing is covered by TestRepriceStored_AppliesNewRates.)
func TestRun_PricingLiteLLMOverlay(t *testing.T) {
	home := setupHome(t)
	appHome := filepath.Join(home, ".aispend")

	if out, _, c := run(t, "pricing"); c != 0 || !strings.Contains(out, "embedded") {
		t.Errorf("pricing without a cache should report embedded: code=%d out=%s", c, out)
	}

	// Overlay doubles opus input to $10/Mtok (embedded is $5).
	litellm := `{"claude-opus-4":{"input_cost_per_token":0.00001,"output_cost_per_token":0.00001,"cache_read_input_token_cost":0.000001}}`
	if err := refresh.WriteCache(refresh.CachePath(appHome), []byte(litellm)); err != nil {
		t.Fatal(err)
	}
	if out, _, c := run(t, "pricing"); c != 0 || !strings.Contains(out, "LiteLLM cache") {
		t.Errorf("pricing with a fresh cache should report LiteLLM: code=%d out=%s", c, out)
	}

}

// repriceStored re-prices already-stored events in place with the current engine,
// so a `pricing refresh` takes effect on existing data without a re-scan.
func TestRepriceStored_AppliesNewRates(t *testing.T) {
	home := setupHome(t)
	run(t, "scan") // events priced with the embedded opus rate ($5/Mtok)
	appHome := filepath.Join(home, ".aispend")

	st, err := store.OpenFileStore(filepath.Join(appHome, "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	evs, _ := st.Query(store.Filter{})
	var id string
	var before int64
	for _, e := range evs {
		if strings.Contains(e.Model, "opus") {
			id, before = e.EventID, e.CostViews.APIEquivalent.Micros
		}
	}

	// Overlay doubles opus input to $10/Mtok, then reprice in place.
	litellm := `{"claude-opus-4":{"input_cost_per_token":0.00001,"output_cost_per_token":0.00001,"cache_read_input_token_cost":0.000001}}`
	if err := refresh.WriteCache(refresh.CachePath(appHome), []byte(litellm)); err != nil {
		t.Fatal(err)
	}
	app := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	n, err := app.repriceStored(app.pricingEngine())
	if err != nil || n == 0 {
		t.Fatalf("repriceStored: n=%d err=%v", n, err)
	}

	st2, _ := store.OpenFileStore(filepath.Join(appHome, "events.json"))
	after, _ := st2.Get(id)
	// overlay $10/Mtok all-in: 12,400 in + 3,100 out + 8,900 cache-read ($1/Mtok)
	// = 124,000 + 31,000 + 8,900 = 163,900 micros — replacing the embedded total.
	if got := after.CostViews.APIEquivalent.Micros; got == before || got != 163_900 {
		t.Errorf("reprice should apply overlay rates: before=%d after=%d (want 163900)", before, got)
	}
}

// applyPricingTable validates + caches a LiteLLM table and re-prices stored events;
// the network fetch is the only part left out (cmdPricingRefresh's thin wrapper).
func TestApplyPricingTable(t *testing.T) {
	home := setupHome(t)
	appHome := filepath.Join(home, ".aispend")
	cache := refresh.CachePath(appHome)
	app := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	litellm := []byte(`{"claude-opus-4":{"input_cost_per_token":0.00001,"output_cost_per_token":0.00001}}`)

	// No events yet → repriced 0, but the table is cached.
	models, repriced, err := app.applyPricingTable(cache, litellm)
	if err != nil || models != 1 || repriced != 0 {
		t.Fatalf("no events: models=%d repriced=%d err=%v", models, repriced, err)
	}
	if _, ok := refresh.ReadFreshCache(cache, time.Hour); !ok {
		t.Error("table should be cached")
	}

	// After a scan, applying re-prices the stored events.
	run(t, "scan")
	if _, repriced, err = app.applyPricingTable(cache, litellm); err != nil || repriced == 0 {
		t.Fatalf("with events: repriced=%d err=%v", repriced, err)
	}

	// Malformed bytes error cleanly.
	if _, _, err := app.applyPricingTable(cache, []byte("nope")); err == nil {
		t.Error("malformed table should error")
	}
}

func TestRun_ScanWhenAbsent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	out, _, code := run(t, "scan")
	if code != 0 || !strings.Contains(out, "No supported agents detected") {
		t.Errorf("absent scan: code=%d out=%s", code, out)
	}
}

func TestRun_MoreCommands(t *testing.T) {
	setupHome(t)
	run(t, "scan", "--no-network")

	if out, _, c := run(t, "report", "--period", "today"); c != 0 || !strings.Contains(out, "today") {
		t.Errorf("report today: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "today"); c != 0 || !strings.Contains(out, "today ·") {
		t.Errorf("today command: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "top"); c != 0 || !strings.Contains(out, "aispend top") {
		t.Errorf("top command: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "top", "--sessions", "--period", "all"); c != 0 || !strings.Contains(out, "sessions") {
		t.Errorf("top --sessions: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "report", "--period", "week", "--by", "repo"); c != 0 || !strings.Contains(out, "by repo") {
		t.Errorf("report --by repo: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "report", "--period", "week", "--by", "provider", "--view", "estimated"); c != 0 || !strings.Contains(out, "by provider") {
		t.Errorf("report flags: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "doctor", "--paths"); c != 0 || !strings.Contains(out, "claude_code roots") || !strings.Contains(out, "os:") {
		t.Errorf("doctor --paths: code=%d out=%s", c, out)
	}
	if out, _, c := run(t, "plans"); c != 0 || !strings.Contains(out, "claude-max-20x") || !strings.Contains(out, "200.00") {
		t.Errorf("plans: code=%d out=%s", c, out)
	}
	if out, _, _ := run(t, "help"); !strings.Contains(out, "Usage:") {
		t.Errorf("help: %s", out)
	}
	// The TUI is the default channel: a bare `aispend` opens it on a TTY, and off a
	// TTY (as here, and in the offline build) falls back to the static `today` glance
	// — never the usage screen (that's `help`).
	if out, _, c := run(t); c != 0 || !strings.Contains(out, "aispend today") || strings.Contains(out, "Usage:") {
		t.Errorf("no args off a TTY should fall back to `today`, not usage: code=%d out=%s", c, out)
	}
	if _, _, c := run(t, "report", "--bogusflag"); c != 2 {
		t.Errorf("bad flag exit=%d, want 2", c)
	}
	if _, _, c := run(t, "report", "--period", "garbage"); c != 2 {
		t.Errorf("bad --period exit=%d, want 2", c)
	}
}

// report --by is validated like --period and --view: an unknown dimension is
// rejected (exit 2) with a message that names the bad value and the valid set —
// never silently accepted and mislabeled as a by-model report (the `· by wombat ·`
// header lie). Fail-fast: nothing reaches stdout, so --json pipes stay clean.
func TestReport_RejectsUnknownBy(t *testing.T) {
	setupHome(t)

	out, errs, code := run(t, "report", "--period", "all", "--by", "wombat")
	if code != 2 {
		t.Fatalf("unknown --by should exit 2, got %d (out=%q errs=%q)", code, out, errs)
	}
	if !strings.Contains(errs, "wombat") {
		t.Errorf("error should name the bad value, got: %q", errs)
	}
	if !strings.Contains(errs, "model") {
		t.Errorf("error should list the valid dimensions, got: %q", errs)
	}
	if strings.Contains(out, "by wombat") {
		t.Errorf("rejected --by must not render a report header, got stdout: %q", out)
	}

	// Boundary guard: every advertised dimension still passes validation, so the
	// validator can't drift out of sync with the flag help / groupKey switch.
	for _, by := range []string{"model", "repo", "provider", "cost_tag", "session", "branch", "commit", "file"} {
		if _, _, c := run(t, "report", "--period", "all", "--by", by); c != 0 {
			t.Errorf("valid --by %s should exit 0, got %d", by, c)
		}
	}
}

func TestRun_WindowsAndVerbose(t *testing.T) {
	home := setupHome(t)
	// a malformed line so `scan --verbose` has a skip to show
	bad := filepath.Join(home, ".claude", "projects", "payments", "bad.jsonl")
	if err := os.WriteFile(bad, []byte("{this is not json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if out, _, code := run(t, "scan", "--verbose"); code != 0 || !strings.Contains(out, "Skipped records") || !strings.Contains(out, "not json") {
		t.Errorf("scan --verbose: code=%d out=%s", code, out)
	}
	if out, _, c := run(t, "report", "--period", "month"); c != 0 || !strings.Contains(out, "this month") {
		t.Errorf("month: %s", out)
	}
	if out, _, c := run(t, "report", "--period", "last month"); c != 0 || !strings.Contains(out, "last month") {
		t.Errorf("last month: %s", out)
	}
	if out, _, c := run(t, "report", "--period", "since 2026-01-01"); c != 0 || !strings.Contains(out, "since 2026-01-01") {
		t.Errorf("since: %s", out)
	}
	if out, _, c := run(t, "report", "--period", "all"); c != 0 || !strings.Contains(out, "all time") {
		t.Errorf("all: %s", out)
	}
	if out, _, c := run(t, "report", "--period", "2026-01-01..2026-06-30"); c != 0 || !strings.Contains(out, "2026-01-01 → 2026-06-30") {
		t.Errorf("custom range: code=%d out=%s", c, out)
	}
	if _, _, c := run(t, "report", "--period", "since nonsense"); c != 2 {
		t.Errorf("bad since should exit 2, got %d", c)
	}
	if _, _, c := run(t, "report", "--period", "2026-01-01..nonsense"); c != 2 {
		t.Errorf("bad range end should exit 2, got %d", c)
	}
	// `all` + amortized exercises the spanDays derivation
	if _, _, c := run(t, "report", "--period", "all", "--view", "amortized"); c != 0 {
		t.Errorf("all amortized should exit 0, got %d", c)
	}
}

// The view was renamed effective_allocated → amortized (hard cut, no alias). The
// old name must not silently fall through to api_equivalent: it errors with a hint.
func TestReportView_EffectiveAllocatedRenamed(t *testing.T) {
	for _, old := range []string{"effective_allocated", "effective-allocated"} {
		_, errs, code := run(t, "report", "--period", "all", "--view", old)
		if code != 2 {
			t.Errorf("--view %s should exit 2 (renamed), got %d", old, code)
		}
		if !strings.Contains(errs, "amortized") || !strings.Contains(errs, "renamed") {
			t.Errorf("--view %s error should name the new view and that it was renamed: %s", old, errs)
		}
	}
}

// report --view is validated like --by and --period: an unknown lens is rejected
// (exit 2) with a message naming the bad value and the valid set — never silently
// fallen through pickView's default to api_equivalent under a lying `view: <garbage>`
// header. The renamed effective_allocated keeps its own migration hint (above); this
// guards every *other* unknown value.
func TestReportView_RejectsUnknown(t *testing.T) {
	setupHome(t)

	out, errs, code := run(t, "report", "--period", "all", "--view", "wombat")
	if code != 2 {
		t.Fatalf("unknown --view should exit 2, got %d (out=%q errs=%q)", code, out, errs)
	}
	if !strings.Contains(errs, "wombat") {
		t.Errorf("error should name the bad value, got: %q", errs)
	}
	if !strings.Contains(errs, "api_equivalent") {
		t.Errorf("error should list the valid views, got: %q", errs)
	}
	if strings.Contains(out, "view: wombat") {
		t.Errorf("rejected --view must not render a report header, got stdout: %q", out)
	}

	// Boundary guard: every advertised view still passes, in both the underscore and
	// dash spellings the normalizer accepts, so the validator can't drift from pickView.
	for _, v := range []string{"api_equivalent", "api-equivalent", "reported", "estimated", "amortized"} {
		if _, _, c := run(t, "report", "--period", "all", "--view", v); c != 0 {
			t.Errorf("valid --view %s should exit 0, got %d", v, c)
		}
	}
}

func TestStartOfWeek_MondayStart(t *testing.T) {
	// Sunday 2026-06-14 belongs to the week starting Monday 2026-06-08.
	sun := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	if got := startOfWeek(sun); !got.Equal(time.Date(2026, 6, 8, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Sunday → %v, want Mon 2026-06-08", got)
	}
	// Monday stays on its own midnight.
	mon := time.Date(2026, 6, 15, 23, 0, 0, 0, time.UTC)
	if got := startOfWeek(mon); !got.Equal(time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("Monday → %v, want Mon 2026-06-15 00:00", got)
	}
}

func TestRenderReport(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	opus := event.USD(431850)
	sonnet := event.USD(17250)
	events := []event.AgentEvent{
		{Model: "claude-opus-4", Repo: "payments", Provider: "claude_code",
			CostViews: event.CostViews{APIEquivalent: &opus}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
		{Model: "claude-sonnet-4", Repo: "payments", Provider: "claude_code",
			CostViews: event.CostViews{APIEquivalent: &sonnet}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
	}
	until := time.Now()
	since7 := until.AddDate(0, 0, -7)
	a.renderReport(events, "model", "api_equivalent", since7, until, "last 7d", apiPlans(), len(events))
	got := buf.String()
	for _, want := range []string{"claude-opus-4", "claude-sonnet-4", "$0.43", "$0.02", "total", "confidence 0.95"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}

	// empty window but the store HAS data → point at --all/--days, never "run scan"
	buf.Reset()
	a.renderReport(nil, "model", "api_equivalent", since7, until, "today", apiPlans(), 7270)
	if got := buf.String(); !strings.Contains(got, "7270 stored") || !strings.Contains(got, "--period all") {
		t.Errorf("empty-window message should cite stored count + --period all, got: %s", got)
	}

	// genuinely empty store → tell them to scan
	buf.Reset()
	a.renderReport(nil, "model", "api_equivalent", since7, until, "today", apiPlans(), 0)
	if got := buf.String(); !strings.Contains(got, "run `aispend scan`") {
		t.Errorf("empty-store message should point at scan, got: %s", got)
	}
}

// A full window whose events simply have no cost in the REQUESTED view (e.g.
// `--view reported` when no tool-written cost was captured) must say so — not
// claim the window is empty and tell the user to widen it. This is the exact
// confusion the CodeBurn comparison surfaced: 5,197 events present, `reported`
// view empty, yet aispend said "no events … widen with --period all".
func TestRenderReport_ViewEmptyButWindowHasEvents(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	opus := event.USD(431850)
	events := []event.AgentEvent{
		{Model: "claude-opus-4", Provider: "claude_code", CostViews: event.CostViews{APIEquivalent: &opus}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
		{Model: "claude-sonnet-4", Provider: "claude_code", CostViews: event.CostViews{APIEquivalent: &opus}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
	}
	until := time.Now()
	since := until.AddDate(0, 0, -7)
	// `reported` is unpopulated on these events, but the window is full.
	a.renderReport(events, "model", "reported", since, until, "last 7 days", apiPlans(), len(events))
	got := buf.String()
	if !strings.Contains(got, "none of the 2 event(s) in last 7 days have a reported cost") {
		t.Errorf("should explain the VIEW is empty, not the window:\n%s", got)
	}
	if !strings.Contains(got, "--view api_equivalent") {
		t.Errorf("should point at a populated view:\n%s", got)
	}
	if strings.Contains(got, "widen with --period all") {
		t.Errorf("must NOT tell the user to widen a full window:\n%s", got)
	}
}

// A nil cost view (unknown model — e.g. a snapshot missing from the pricing
// table) must be SURFACED, not silently dropped. This is the claude-opus-4-7
// regression: 7.5k turns vanished from the report because pickView skipped
// them with no trace, making the total a quiet undercount.
func TestRenderReport_SurfacesUnpricedEvents(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	opus := event.USD(431_850)
	events := []event.AgentEvent{
		{Model: "claude-opus-4-8", Provider: "claude_code",
			CostViews: event.CostViews{APIEquivalent: &opus}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}},
		{Model: "claude-opus-4-7", Provider: "claude_code"}, // unknown model → no api_equivalent
		{Model: "claude-opus-4-7", Provider: "claude_code"},
		{Model: "<synthetic>", Provider: "claude_code"},
	}
	until := time.Now()
	since7 := until.AddDate(0, 0, -7)
	a.renderReport(events, "model", "api_equivalent", since7, until, "last 7d", apiPlans(), len(events))
	got := buf.String()
	for _, want := range []string{"unpriced", "3", "claude-opus-4-7"} {
		if !strings.Contains(got, want) {
			t.Errorf("report must surface unpriced events (missing %q):\n%s", want, got)
		}
	}
}

func TestRenderAllocated(t *testing.T) {
	opus := event.USD(431850)
	sonnet := event.USD(17250)
	events := []event.AgentEvent{
		{Model: "claude-opus-4", CostViews: event.CostViews{APIEquivalent: &opus}},
		{Model: "claude-sonnet-4", CostViews: event.CostViews{APIEquivalent: &sonnet}},
	}

	subPlans := config.PlanSet{
		Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200},
		ByProvider: map[string]config.Plan{},
	}

	// A clean 30-day window (all of June) for deterministic proration assertions.
	jun1 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	jul1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	t.Run("subscription plan amortizes", func(t *testing.T) {
		var buf bytes.Buffer
		a := &App{Out: &buf, Now: time.Now}
		a.renderReport(events, "model", "amortized", jun1, jul1, "last 30d", subPlans, len(events)) // 30d ⇒ full $200
		got := buf.String()
		for _, want := range []string{"view: amortized", "subscription_amortized", "$200.00", "not a metered price"} {
			if !strings.Contains(got, want) {
				t.Errorf("allocated report missing %q:\n%s", want, got)
			}
		}
	})

	t.Run("per-provider plans sum to the real total", func(t *testing.T) {
		var buf bytes.Buffer
		a := &App{Out: &buf, Now: time.Now}
		cc := event.USD(100)
		cx := event.USD(50)
		evs := []event.AgentEvent{
			{Provider: "claude_code", Model: "claude-opus-4-8", CostViews: event.CostViews{APIEquivalent: &cc}},
			{Provider: "codex", Model: "gpt-5.3-codex", CostViews: event.CostViews{APIEquivalent: &cx}},
		}
		set := config.PlanSet{
			Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200},
			ByProvider: map[string]config.Plan{"codex": {Kind: "subscription", Name: "chatgpt-pro", MonthlyFeeUSD: 200}},
		}
		a.renderReport(evs, "provider", "amortized", jun1, jul1, "last 30d", set, len(evs))
		got := buf.String()
		if !strings.Contains(got, "$400.00") || !strings.Contains(got, "2 plan(s)") {
			t.Errorf("per-provider total should be $400 across 2 plans:\n%s", got)
		}
	})

	t.Run("plan started mid-window amortizes only active days", func(t *testing.T) {
		var buf bytes.Buffer
		a := &App{Out: &buf, Now: time.Now}
		// Plan starts Jun 12 → only Jun 12..30 (19 of the 30-day Jun12–Jul12 cycle)
		// is billable: $200 × 19/30 = $126.67, not the full $200.
		start := config.PlanSet{
			Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200, StartDate: time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)},
			ByProvider: map[string]config.Plan{},
		}
		a.renderReport(events, "model", "amortized", jun1, jul1, "last 30d", start, len(events))
		got := buf.String()
		if !strings.Contains(got, "$126.67") {
			t.Errorf("mid-window start should prorate to $126.67, not full $200:\n%s", got)
		}
		if strings.Contains(got, "$200.00") {
			t.Errorf("should not bill the full month when the plan started mid-window:\n%s", got)
		}
	})

	t.Run("plan starting after the window is flagged, not allocated", func(t *testing.T) {
		var buf bytes.Buffer
		a := &App{Out: &buf, Now: time.Now}
		future := config.PlanSet{
			Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200, StartDate: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
			ByProvider: map[string]config.Plan{},
		}
		a.renderReport(events, "model", "amortized", jun1, jul1, "last 30d", future, len(events))
		got := buf.String()
		if !strings.Contains(got, "plan starts 2026-08-01") {
			t.Errorf("a not-yet-active plan should be explained:\n%s", got)
		}
	})

	t.Run("no plan configured is explained, not n/a", func(t *testing.T) {
		var buf bytes.Buffer
		a := &App{Out: &buf, Now: time.Now}
		a.renderReport(events, "model", "amortized", jun1, jul1, "last 30d", apiPlans(), len(events))
		if !strings.Contains(buf.String(), "no subscription plan configured") {
			t.Errorf("expected guidance when no plan: %s", buf.String())
		}
	})
}

// report --by session: a new grouping dimension over event.SessionID. The
// acceptance bar (09-session-view.md) is reconciliation — the by-session total
// must equal the by-model total for the same events, no double-count, no drop —
// plus an honest "(no session)" bucket and a short, copy-pasteable id in the table.
func TestReport_BySession(t *testing.T) {
	opus := event.USD(300)
	son := event.USD(100)
	hai := event.USD(50)
	mk := func(sid, model string, m *event.Money) event.AgentEvent {
		return event.AgentEvent{SessionID: sid, Model: model, Provider: "claude_code",
			CostViews: event.CostViews{APIEquivalent: m}, Evidence: event.Evidence{CostMethod: "token_priced", ConfidenceScore: 0.95}}
	}
	events := []event.AgentEvent{
		mk("3f9c1a2b-aaaa-bbbb", "claude-opus-4-8", &opus),
		mk("3f9c1a2b-aaaa-bbbb", "claude-sonnet-4", &son), // same session as above
		mk("", "claude-haiku-4", &hai),                    // no session id
	}

	if got := groupKey(events[0], "session"); got != "3f9c1a2b-aaaa-bbbb" {
		t.Errorf("groupKey session = %q, want the full session id", got)
	}
	if got := groupKey(events[2], "session"); got != "(no session)" {
		t.Errorf("empty session should bucket as %q, got %q", "(no session)", got)
	}

	bySession := aggregateReport(events, "session", "api_equivalent")
	byModel := aggregateReport(events, "model", "api_equivalent")
	if bySession.total != byModel.total || bySession.total != 450 {
		t.Errorf("by-session total (%d) must reconcile with by-model total (%d), want 450", bySession.total, byModel.total)
	}
	if len(bySession.rows) != 2 { // the 2-turn session collapses to one row + the no-session bucket
		t.Fatalf("want 2 session rows, got %d", len(bySession.rows))
	}
	if bySession.rows[0].micros != 400 {
		t.Errorf("top session should sum its turns (300+100), got %d", bySession.rows[0].micros)
	}

	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	until := time.Now()
	a.renderReport(events, "session", "api_equivalent", until.AddDate(0, 0, -7), until, "this week", apiPlans(), len(events))
	got := buf.String()
	if !strings.Contains(got, "by session") {
		t.Errorf("header should read 'by session':\n%s", got)
	}
	if !strings.Contains(got, "3f9c1a2b") {
		t.Errorf("table should show the shortened session id:\n%s", got)
	}
	if strings.Contains(got, "3f9c1a2b-aaaa-bbbb") {
		t.Errorf("table should shorten the id, not print the full UUID:\n%s", got)
	}
}

// report --by branch|commit|file: the VCS facets. Branch/commit are ordinary 1:1
// groupings (reconcile to the by-model total); a commit id is shortened in the table
// yet kept whole for grouping; --by file fans out so per-file rows still sum to the
// grand total and a fileless turn shows under "(no files)".
func TestReport_ByVCSFacets(t *testing.T) {
	full := "9f3c1a2b7d4e5f60718293a4b5c6d7e8f9012345"
	events := []event.AgentEvent{
		gitEvent(300_000, "feature/retry", full, "a.go", "b.go"),
		gitEvent(100_000, "feature/retry", full, "a.go"),
		gitEvent(60_000, "", ""), // no branch/commit/files → sentinels
	}
	render := func(by string) string {
		var buf bytes.Buffer
		a := &App{Out: &buf, Now: time.Now}
		until := time.Now()
		a.renderReport(events, by, "api_equivalent", until.AddDate(0, 0, -7), until, "this week", apiPlans(), len(events))
		return buf.String()
	}

	t.Run("branch", func(t *testing.T) {
		out := render("branch")
		if !strings.Contains(out, "by branch") || !strings.Contains(out, "feature/retry") || !strings.Contains(out, "(no branch)") {
			t.Errorf("branch view missing header/branch/sentinel:\n%s", out)
		}
	})
	t.Run("commit shortens in table, keeps full in grouping", func(t *testing.T) {
		if got := groupKey(events[0], "commit"); got != full {
			t.Errorf("groupKey commit = %q, want the full sha", got)
		}
		out := render("commit")
		if !strings.Contains(out, "by commit") || !strings.Contains(out, "9f3c1a2b7d") {
			t.Errorf("commit view should show the short sha:\n%s", out)
		}
		if strings.Contains(out, full) {
			t.Errorf("table must shorten the sha, not print all 40 chars:\n%s", out)
		}
	})
	t.Run("file fan-out shows files and the no-files bucket", func(t *testing.T) {
		out := render("file")
		if !strings.Contains(out, "by file") || !strings.Contains(out, "a.go") || !strings.Contains(out, "(no files)") {
			t.Errorf("file view missing header/file/sentinel:\n%s", out)
		}
	})
}

func TestShortSHA(t *testing.T) {
	if got := shortSHA("(no commit)"); got != "(no commit)" {
		t.Errorf("sentinel must pass through, got %q", got)
	}
	if got := shortSHA("abcd"); got != "abcd" {
		t.Errorf("short sha must pass through, got %q", got)
	}
	if got := shortSHA("0123456789abcdef"); got != "0123456789" {
		t.Errorf("long sha = %q, want first 10 chars", got)
	}
}

// `today` is the arbitrage-first daily glance (the founder's primary view): the
// api-equivalent total, the plan $/day + ROI, the cache-savings headline, the
// turns/sessions/top-model strip, and an hourly spike bar (09-session-view.md).
func TestRenderToday_ArbitrageFirst(t *testing.T) {
	defer func(l *time.Location) { time.Local = l }(time.Local)
	time.Local = time.UTC // pin: the hourly bar is now local-tz, so make it deterministic
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 17, 14, 30, 0, 0, time.UTC)
	mk := func(sid, model string, hour int, tk event.Tokens) event.AgentEvent {
		ts := time.Date(2026, 6, 17, hour, 0, 0, 0, time.UTC)
		e := event.AgentEvent{SessionID: sid, Provider: "claude_code", Model: model, Tokens: tk, TSStart: ts, TSEnd: ts.Add(time.Minute)}
		if err := eng.Price(&e, pricing.Plan{Kind: "api"}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	events := []event.AgentEvent{
		mk("s1", "claude-opus-4-8", 9, event.Tokens{Input: 100_000, Output: 50_000, CacheRead: 5_000_000}),
		mk("s1", "claude-opus-4-8", 14, event.Tokens{Input: 50_000, CacheRead: 20_000_000}), // clear peak hour
		mk("s2", "claude-sonnet-4", 10, event.Tokens{Input: 20_000, Output: 5_000}),
	}
	subPlans := config.PlanSet{Default: config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200}, ByProvider: map[string]config.Plan{}}
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: func() time.Time { return now }}
	a.renderToday(events, now, subPlans, len(events), eng)
	got := buf.String()
	for _, want := range []string{
		"aispend today · Wed Jun 17",
		"api-equivalent",
		"plan $", "ROI", // arbitrage-first headline
		"cache saved",
		"3 turns", "2 sessions",
		"opus-4-8",              // top model by spend
		"by hour", "peak 14:00", // hourly spike bar finds the hot hour
	} {
		if !strings.Contains(got, want) {
			t.Errorf("today missing %q:\n%s", want, got)
		}
	}
}

// No plan configured → no fabricated ROI, just an honest hint.
func TestRenderToday_NoPlanHidesROI(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)
	ts := time.Date(2026, 6, 17, 9, 0, 0, 0, time.UTC)
	e := event.AgentEvent{SessionID: "s", Provider: "claude_code", Model: "claude-opus-4-8", Tokens: event.Tokens{Input: 1_000_000}, TSStart: ts, TSEnd: ts}
	if err := eng.Price(&e, pricing.Plan{Kind: "api"}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: func() time.Time { return now }}
	a.renderToday([]event.AgentEvent{e}, now, apiPlans(), 1, eng)
	got := buf.String()
	if strings.Contains(got, "× ROI") || strings.Contains(got, "plan $") {
		t.Errorf("no plan must not print a fabricated ROI multiple or plan $/day:\n%s", got)
	}
	if !strings.Contains(got, "plans`") {
		t.Errorf("no plan should hint at `aispend plans`:\n%s", got)
	}
}

// Empty-today is honest, and store-aware: tell a fresh user to scan, but never
// tell a user with 4,200 stored events to scan just because today is quiet.
func TestRenderToday_EmptyStates(t *testing.T) {
	now := time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)
	a := &App{Now: func() time.Time { return now }}

	var buf bytes.Buffer
	a.Out = &buf
	a.renderToday(nil, now, apiPlans(), 0, pricing.NewEngine())
	if got := buf.String(); !strings.Contains(got, "no AI-coding spend") || !strings.Contains(got, "scan") {
		t.Errorf("fresh empty today should point at scan:\n%s", got)
	}

	buf.Reset()
	a.renderToday(nil, now, apiPlans(), 4200, pricing.NewEngine())
	if got := buf.String(); strings.Contains(got, "run `aispend scan`") {
		t.Errorf("with stored data, a quiet today must NOT say run scan:\n%s", got)
	}
}

// When some providers are on a subscription but another has spend with no plan,
// `today` shows the ROI AND names what it leaves out — never silently overstating
// the arbitrage.
func TestRenderToday_UncoveredProviderNoted(t *testing.T) {
	eng := pricing.NewEngine()
	now := time.Date(2026, 6, 17, 14, 0, 0, 0, time.UTC)
	mk := func(provider, model string, hour int, tk event.Tokens) event.AgentEvent {
		ts := time.Date(2026, 6, 17, hour, 0, 0, 0, time.UTC)
		e := event.AgentEvent{SessionID: provider + "-s", Provider: provider, Model: model, Tokens: tk, TSStart: ts, TSEnd: ts}
		if err := eng.Price(&e, pricing.Plan{Kind: "api"}); err != nil {
			t.Fatal(err)
		}
		return e
	}
	events := []event.AgentEvent{
		mk("claude_code", "claude-opus-4-8", 9, event.Tokens{Input: 1_000_000, CacheRead: 4_000_000}),
		mk("codex", "gpt-5.3-codex", 10, event.Tokens{Input: 500_000}),
	}
	// Claude on a subscription, Codex with no plan → Codex falls outside the ROI.
	plans := config.PlanSet{
		Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200},
		ByProvider: map[string]config.Plan{"codex": {Kind: "api"}},
	}
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: func() time.Time { return now }}
	a.renderToday(events, now, plans, len(events), eng)
	got := buf.String()
	if !strings.Contains(got, "ROI") {
		t.Errorf("a configured subscription should still produce an ROI:\n%s", got)
	}
	if !strings.Contains(got, "Codex not in the ROI") {
		t.Errorf("the uncovered provider should be named:\n%s", got)
	}
}

// `aispend top` is the bridge from "my spend is high" to "explain why": the
// priciest turns (default) or sessions in a window, each printed with its id so
// the next step is a copy-paste `explain` (08-cli-tui-concept.md).
func TestRenderTop_Turns(t *testing.T) {
	big := event.USD(5_000_000)
	mid := event.USD(1_000_000)
	events := []event.AgentEvent{
		{EventID: "evt_big", SessionID: "3f9c1a2b-aa", Provider: "claude_code", Model: "claude-opus-4-8",
			Tokens: event.Tokens{Input: 40_000, Output: 9_000, CacheRead: 21_000_000}, CostViews: event.CostViews{APIEquivalent: &big}},
		{EventID: "evt_mid", SessionID: "a17d4e5f-bb", Provider: "claude_code", Model: "claude-sonnet-4", CostViews: event.CostViews{APIEquivalent: &mid}},
		{EventID: "evt_nil", Provider: "claude_code", Model: "mystery"}, // no api-equivalent
	}
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	a.renderTop(events, 10, false, "this week", len(events))
	got := buf.String()
	for _, want := range []string{"aispend top", "this week", "priciest turns", "evt_big", "$5.00", "evt_mid", "$1.00", "drill in"} {
		if !strings.Contains(got, want) {
			t.Errorf("top missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "evt_big") > strings.Index(got, "evt_mid") {
		t.Errorf("turns must be priciest-first:\n%s", got)
	}
	if strings.Contains(got, "evt_nil") {
		t.Errorf("a nil-cost turn must not appear as a ranked row:\n%s", got)
	}
}

func TestRenderTop_Sessions(t *testing.T) {
	a1, a2, b := event.USD(3_000_000), event.USD(1_000_000), event.USD(5_000_000)
	events := []event.AgentEvent{
		{EventID: "e1", SessionID: "3f9c", Provider: "claude_code", Model: "claude-opus-4-8", CostViews: event.CostViews{APIEquivalent: &a1}},
		{EventID: "e2", SessionID: "3f9c", Provider: "claude_code", Model: "claude-sonnet-4", CostViews: event.CostViews{APIEquivalent: &a2}},
		{EventID: "e3", SessionID: "a17d", Provider: "claude_code", Model: "claude-opus-4-8", CostViews: event.CostViews{APIEquivalent: &b}},
	}
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	a.renderTop(events, 10, true, "this week", len(events))
	got := buf.String()
	for _, want := range []string{"priciest sessions", "3f9c", "a17d", "$5.00", "$4.00", "drill into a session"} {
		if !strings.Contains(got, want) {
			t.Errorf("top --sessions missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "a17d") > strings.Index(got, "3f9c") {
		t.Errorf("sessions must be priciest-first (a17d $5 before 3f9c $4):\n%s", got)
	}
}

func TestRenderTop_EmptyStates(t *testing.T) {
	var buf bytes.Buffer
	a := &App{Out: &buf, Now: time.Now}
	a.renderTop(nil, 10, false, "today", 0)
	if got := buf.String(); !strings.Contains(got, "no spend") || !strings.Contains(got, "scan") {
		t.Errorf("fresh empty top should point at scan:\n%s", got)
	}
	buf.Reset()
	a.renderTop(nil, 10, false, "today", 4200)
	if got := buf.String(); strings.Contains(got, "run `aispend scan`") {
		t.Errorf("with stored data, an empty window must not say run scan:\n%s", got)
	}
}

func TestHelpers(t *testing.T) {
	if usd(431850, "USD") != "$0.43" || usd(27080000, "USD") != "$27.08" {
		t.Errorf("usd: %s %s", usd(431850, "USD"), usd(27080000, "USD"))
	}
	if usd(1500000, "EUR") != "1.50 EUR" {
		t.Errorf("usd eur: %s", usd(1500000, "EUR"))
	}
	if comma(12400) != "12,400" || comma(8900) != "8,900" || comma(100) != "100" || comma(0) != "0" {
		t.Errorf("comma broken: %s %s %s", comma(12400), comma(8900), comma(100))
	}
	if got := bar(100); !strings.HasPrefix(got, "▓▓▓▓▓▓▓▓▓▓") {
		t.Errorf("bar(100) = %q", got)
	}
	e := event.AgentEvent{Model: "m", Repo: "r", Provider: "p", CostTag: "team"}
	if groupKey(e, "repo") != "r" || groupKey(e, "provider") != "p" || groupKey(e, "cost_tag") != "team" || groupKey(e, "model") != "m" {
		t.Error("groupKey wrong")
	}
	if _, ok := pickView(event.AgentEvent{}, "reported"); ok {
		t.Error("nil view should report not-ok")
	}
	// amortized maps to the renamed CostViews.Amortized lens (was EffectiveAllocated).
	amt := event.USD(200_000_000)
	if m, ok := pickView(event.AgentEvent{CostViews: event.CostViews{Amortized: &amt}}, "amortized"); !ok || m.Micros != 200_000_000 {
		t.Errorf("amortized view should return cv.Amortized, got %v ok=%v", m, ok)
	}
}
