package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// quotaProviderLabel title-cases a provider id and passes the empty string through.
func TestQuotaProviderLabel(t *testing.T) {
	for in, want := range map[string]string{"": "", "claude": "Claude", "codex": "Codex"} {
		if got := quotaProviderLabel(in); got != want {
			t.Errorf("quotaProviderLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// modelList renders the distinct models sorted + short-named, or a sentinel when empty.
func TestModelList(t *testing.T) {
	if got := modelList(nil); got != "(no model)" {
		t.Errorf("empty model set = %q, want (no model)", got)
	}
	if got := modelList(map[string]bool{"claude-opus-4-8": true, "gpt-5.3-codex": true}); got != "gpt-5.3-codex, opus-4-8" {
		t.Errorf("modelList = %q, want sorted short names", got)
	}
}

// topUnpriced renders a most-frequent-first model histogram capped at limit, with a
// "+N more" tail so a coverage gap names itself instead of vanishing.
func TestTopUnpriced(t *testing.T) {
	got := topUnpriced(map[string]int{"claude-opus-4-7": 7535, "synthetic": 25, "x": 3, "y": 1}, 2)
	if !strings.Contains(got, "claude-opus-4-7") || !strings.Contains(got, "7535") {
		t.Errorf("most-frequent model should lead: %q", got)
	}
	if !strings.Contains(got, "+2 more") {
		t.Errorf("the tail beyond the cap should read '+2 more': %q", got)
	}
}

// attribution resolves (project, cost_tag) from the nearest .aispend.toml and caches
// per directory — a second lookup of the same cwd hits the cache.
func TestAttribution(t *testing.T) {
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	resolve := a.attribution()

	// A directory with no config → empty project/tag.
	bare := t.TempDir()
	if p, c := resolve(bare); p != "" || c != "" {
		t.Errorf("no config → (%q,%q), want empty", p, c)
	}

	// A directory whose .aispend.toml names a project + cost_tag.
	tagged := t.TempDir()
	if err := os.WriteFile(filepath.Join(tagged, ".aispend.toml"), []byte("project = \"payments\"\ncost_tag = \"team\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, c := resolve(tagged); p != "payments" || c != "team" {
		t.Errorf("tagged dir → (%q,%q), want (payments,team)", p, c)
	}
	// Second lookup hits the per-directory cache (same result, no re-read).
	if p, c := resolve(tagged); p != "payments" || c != "team" {
		t.Errorf("cached lookup → (%q,%q), want (payments,team)", p, c)
	}
}

// repoRoot walks up to the nearest dir holding a .git or .aispend.toml marker, and
// returns "" when a file has no such ancestor.
func TestRepoRoot(t *testing.T) {
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := a.repoRoot(filepath.Join(deep, "f.go")); got != root {
		t.Errorf("repoRoot under a .git tree = %q, want %q", got, root)
	}
}

// repriceStored on an empty ledger is a no-op (0 repriced, no error) rather than a
// failure — the "nothing to do" branch.
func TestRepriceStored_EmptyStore(t *testing.T) {
	setupHome(t) // a temp HOME, but no scan → the store is empty
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	if n, err := a.repriceStored(a.pricingEngine()); n != 0 || err != nil {
		t.Errorf("repriceStored on an empty store = (%d,%v), want (0,nil)", n, err)
	}
}

// Flag-variant surfaces: the --no-scan / --no-refresh opt-outs and bad-flag exits on
// the read commands, exercised through the real dispatch.
func TestReadCommands_FlagVariants(t *testing.T) {
	setupHome(t)
	run(t, "scan")

	if _, _, c := run(t, "top", "--no-scan", "--period", "all"); c != 0 {
		t.Errorf("top --no-scan exit = %d, want 0", c)
	}
	if _, _, c := run(t, "top", "--no-refresh"); c != 0 {
		t.Errorf("top --no-refresh exit = %d, want 0", c)
	}
	if _, _, c := run(t, "top", "--bogus"); c != 2 {
		t.Errorf("top --bogus exit = %d, want 2", c)
	}
	if _, _, c := run(t, "today", "--no-refresh"); c != 0 {
		t.Errorf("today --no-refresh exit = %d, want 0", c)
	}
	if _, _, c := run(t, "today", "--bogus"); c != 2 {
		t.Errorf("today --bogus exit = %d, want 2", c)
	}
	if _, _, c := run(t, "report", "--no-scan", "--period", "all"); c != 0 {
		t.Errorf("report --no-scan exit = %d, want 0", c)
	}
}
