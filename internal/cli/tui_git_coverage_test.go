//go:build !offline

package cli

import (
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/event"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// buildCommits best-effort git enrichment: a SHA that IS in the cwd repo gets its commit
// title read back. The test package dir lives inside this repo, so a real HEAD resolves.
func TestBuildCommits_GitEnrichment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	shaOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Skip("not a git checkout")
	}
	head := strings.TrimSpace(string(shaOut))

	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	m := event.USD(100)
	commits := a.buildCommits([]event.AgentEvent{
		{GitSHA: head, GitBranch: "main", CostViews: event.CostViews{APIEquivalent: &m}, TSStart: time.Now()},
	}, "AI-Cost")
	if len(commits) != 1 {
		t.Fatalf("want 1 commit, got %d", len(commits))
	}
	if commits[0].Title == "" {
		t.Errorf("a real HEAD sha should be git-enriched with a commit title")
	}
}

// planProviders marks a provider's currently-effective subscription plan so the picker
// can show what's active.
func TestPlanProviders_Subscription(t *testing.T) {
	home := setupHome(t)
	if err := config.SetProviderPlan(filepath.Join(home, ".aispend"), "claude_code", "claude-max-20x", time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	a := &App{Resolver: platform.Detect(), Now: time.Now, Out: io.Discard, Err: io.Discard}
	provs := a.planProviders([]event.AgentEvent{{Provider: "claude_code"}})
	if len(provs) != 1 || provs[0].Current == "" {
		t.Errorf("a configured subscription should set the provider's Current plan, got %+v", provs)
	}
}
