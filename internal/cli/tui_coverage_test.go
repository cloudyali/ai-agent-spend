//go:build !offline

package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// toTUITrailers/fromTUITrailers bridge config.Trailers <-> the tui editor struct; the
// round-trip must preserve every field, since the in-explorer editor writes the whole
// block back to config.
func TestTrailerSettingsRoundTrip(t *testing.T) {
	in := config.Trailers{Enabled: true, Cost: true, CostModels: true, Tokens: true, Interactions: true, Precision: 5, CostName: "Spend"}
	if got := fromTUITrailers(toTUITrailers(in)); !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip changed the config:\n got %+v\nwant %+v", got, in)
	}
}

// buildCommits groups the ledger by commit SHA — summing api-equivalent micros and
// turns, newest commit first — and drops turns with no SHA. Git enrichment is
// best-effort: an unknown SHA simply stays ledger-only (no Title), which is what these
// synthetic SHAs exercise.
func TestBuildCommits_LedgerOnly(t *testing.T) {
	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	mk := func(sha, branch string, micros int64, day int) event.AgentEvent {
		m := event.USD(micros)
		return event.AgentEvent{
			GitSHA: sha, GitBranch: branch,
			CostViews: event.CostViews{APIEquivalent: &m},
			TSStart:   time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC),
		}
	}
	events := []event.AgentEvent{
		mk("aaaaaaaaaaaa0000", "feature/x", 300, 10),
		mk("aaaaaaaaaaaa0000", "feature/x", 100, 11), // same commit, later turn
		mk("bbbbbbbbbbbb1111", "main", 500, 14),      // newest commit
		mk("", "main", 999, 20),                      // no SHA → must be dropped
	}
	commits := a.buildCommits(events, "AI-Cost")

	if len(commits) != 2 {
		t.Fatalf("want 2 commits (the SHA-less turn dropped), got %d", len(commits))
	}
	if commits[0].SHA != "bbbbbbbbbbbb1111" {
		t.Errorf("commits should be newest-first, got %q first", commits[0].SHA)
	}
	for _, c := range commits {
		if c.SHA == "aaaaaaaaaaaa0000" {
			if c.Micros != 400 || c.Turns != 2 || c.Branch != "feature/x" {
				t.Errorf("two-turn commit = micros %d / turns %d / branch %q, want 400 / 2 / feature/x", c.Micros, c.Turns, c.Branch)
			}
		}
		if c.Title != "" { // synthetic SHAs aren't in any repo → no git enrichment
			t.Errorf("unknown SHA must stay ledger-only, got Title=%q", c.Title)
		}
	}
}

// sessionNameResolver mirrors promptResolver: it snapshots the Claude Code sources once
// and resolves a turn's human session title by content hash only — never a path from the
// event — so a wrong provider or a foreign hash simply misses.
func TestSessionNameResolver(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "projects", "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "s.jsonl")
	content := `{"type":"summary","summary":"wire the resolver"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	res := platform.Resolver{GOOS: "linux", Home: home, Env: func(string) string { return "" }}
	a := &App{Resolver: res}
	name := a.sessionNameResolver()
	if name == nil {
		t.Fatal("sessionNameResolver should be non-nil when Claude sources exist")
	}

	hash := platform.HashPath(path, res.GOOS)
	if got, ok := name(event.AgentEvent{Provider: "claude_code", Evidence: event.Evidence{SourcePathHash: hash}}); !ok || got == "" {
		t.Errorf("a matching claude_code hash should resolve a name, got %q,%v", got, ok)
	}
	if _, ok := name(event.AgentEvent{Provider: "codex", Evidence: event.Evidence{SourcePathHash: hash}}); ok {
		t.Error("a codex event must not resolve a Claude session name")
	}
	if _, ok := name(event.AgentEvent{Provider: "claude_code", Evidence: event.Evidence{SourcePathHash: "deadbeef"}}); ok {
		t.Error("an unknown source hash must not resolve a name")
	}
}

// With no Claude Code logs present, the resolver is nil (the explain view shows no
// session title) — never a crash.
func TestSessionNameResolver_NoSources(t *testing.T) {
	a := &App{Resolver: platform.Resolver{GOOS: "linux", Home: t.TempDir(), Env: func(string) string { return "" }}}
	if a.sessionNameResolver() != nil {
		t.Error("sessionNameResolver should be nil with no Claude sources")
	}
}

// An unbounded (--all) window amortizes over the data's own span — but only when the
// plan has a start-date anchor. Without one, it must skip rather than flat-prorate a
// fee across the entire event span (a stray timestamp could otherwise blow it up).
func TestAmortizedByProvider_Unbounded(t *testing.T) {
	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	evs := []event.AgentEvent{
		{Provider: "claude_code", TSStart: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		{Provider: "claude_code", TSStart: time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC)},
	}

	// Unbounded == zero Since (the --all marker); Until is the real data end the
	// proration runs to.
	unbounded := window{Until: time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)}

	withStart := config.PlanSet{
		Default:    config.Plan{Kind: "subscription", Name: "claude-max-20x", MonthlyFeeUSD: 200, StartDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		ByProvider: map[string]config.Plan{},
	}
	if out, hasPlan := a.amortizedByProvider(evs, unbounded, withStart); !hasPlan || out["claude_code"] <= 0 {
		t.Errorf("unbounded window with a plan-start should amortize, got %v hasPlan=%v", out, hasPlan)
	}

	noStart := config.PlanSet{
		Default:    config.Plan{Kind: "subscription", Name: "x", MonthlyFeeUSD: 200},
		ByProvider: map[string]config.Plan{},
	}
	if out, hasPlan := a.amortizedByProvider(evs, unbounded, noStart); hasPlan || len(out) != 0 {
		t.Errorf("unbounded window without a plan-start must skip, got %v hasPlan=%v", out, hasPlan)
	}
}
