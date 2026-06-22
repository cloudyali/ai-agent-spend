package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
	"github.com/agentspend/ai-agent-spend/internal/store"
)

// End-to-end through the CLI: a seeded ledger event is stamped onto a commit
// message by `trailer`, and after `consume` advances the watermark a second run
// attaches nothing.
func TestCmdTrailer_StampsThenConsumeAdvances(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	st, err := store.OpenFileStore(filepath.Join(home, ".aispend", "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(420000)
	if err := st.Upsert([]event.AgentEvent{{
		EventID: "e1", SessionID: "s", Provider: "claude_code", GitBranch: "main",
		Model:     "claude-opus-4-8",
		Tokens:    event.Tokens{Input: 1000},
		CostViews: event.CostViews{APIEquivalent: &m},
		TSStart:   time.Unix(1000, 0).UTC(), TSEnd: time.Unix(1005, 0).UTC(),
	}}); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	msg := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	mustWrite(t, msg, "Add feature\n")

	if _, errs, code := run(t, "trailer", msg, "--source", "", "--repo", repo); code != 0 {
		t.Fatalf("trailer exit %d err=%s", code, errs)
	}
	if body, _ := os.ReadFile(msg); !strings.Contains(string(body), "AI-Cost: 0.42") {
		t.Errorf("message not stamped:\n%s", body)
	}

	if _, _, code := run(t, "consume", "--repo", repo); code != 0 {
		t.Errorf("consume exit %d", code)
	}
	mustWrite(t, msg, "Second\n")
	if _, _, code := run(t, "trailer", msg, "--source", "", "--repo", repo); code != 0 {
		t.Fatal("trailer (2) nonzero")
	}
	if body, _ := os.ReadFile(msg); strings.Contains(string(body), "AI-Cost") {
		t.Errorf("watermark advanced — second commit must get no trailer:\n%s", body)
	}
}

// Cowork logs gitBranch:"HEAD" — an unresolved symbolic ref, not a real branch — so
// its priced turns never matched the committed branch ("main") and could never reach
// a commit, even though `today` counted them. A "HEAD" (or empty) placeholder must
// fold into whatever branch you commit on; a turn naming a *different real* branch
// must still be excluded.
func TestCmdTrailer_StampsPlaceholderBranchTurns(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	st, err := store.OpenFileStore(filepath.Join(home, ".aispend", "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	headCost := event.USD(420000)  // a Cowork turn tagged "HEAD" → must stamp onto main
	otherCost := event.USD(990000) // a turn on a different real branch → must NOT stamp onto main
	if err := st.Upsert([]event.AgentEvent{
		{
			EventID: "head", SessionID: "s", Provider: "claude_code", GitBranch: "HEAD",
			Model: "claude-opus-4-8", Tokens: event.Tokens{Input: 1000},
			CostViews: event.CostViews{APIEquivalent: &headCost},
			TSStart:   time.Unix(1000, 0).UTC(), TSEnd: time.Unix(1005, 0).UTC(),
		},
		{
			EventID: "other", SessionID: "s2", Provider: "claude_code", GitBranch: "feature/x",
			Model: "claude-opus-4-8", Tokens: event.Tokens{Input: 1000},
			CostViews: event.CostViews{APIEquivalent: &otherCost},
			TSStart:   time.Unix(1001, 0).UTC(), TSEnd: time.Unix(1006, 0).UTC(),
		},
	}); err != nil {
		t.Fatal(err)
	}

	repo := initMainRepo(t)
	msg := filepath.Join(t.TempDir(), "MSG")
	mustWrite(t, msg, "Add\n")
	if _, errs, code := run(t, "trailer", msg, "--source", "", "--repo", repo); code != 0 {
		t.Fatalf("trailer exit %d err=%s", code, errs)
	}
	body, _ := os.ReadFile(msg)
	if !strings.Contains(string(body), "AI-Cost: 0.42") {
		t.Errorf(`a "HEAD"-tagged (Cowork) turn must stamp onto the committed branch:\n%s`, body)
	}
	if strings.Contains(string(body), "1.41") { // 0.42 + 0.99 → the other branch leaked in
		t.Errorf("a turn on a different real branch must not be folded into main:\n%s", body)
	}
}

func TestBranchMatches(t *testing.T) {
	cases := []struct {
		ev, target string
		want       bool
	}{
		{"main", "main", true},       // same real branch
		{"HEAD", "main", true},       // Cowork / detached placeholder folds in
		{"", "main", true},           // missing branch folds in
		{"feature/x", "main", false}, // a different real branch is excluded
		{"main", "develop", false},   // wrong real branch
		{"HEAD", "feature/x", true},  // placeholder folds into any target
	}
	for _, c := range cases {
		if got := branchMatches(c.ev, c.target); got != c.want {
			t.Errorf("branchMatches(%q, %q) = %v, want %v", c.ev, c.target, got, c.want)
		}
	}
}

// A merge-source commit must never be stamped (and must still exit 0).
func TestCmdTrailer_MergeSourceIsNoOp(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AISPEND_HOME", "")
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	msg := filepath.Join(t.TempDir(), "MSG")
	mustWrite(t, msg, "Merge branch 'x'\n")
	if _, _, code := run(t, "trailer", msg, "--source", "merge", "--repo", repo); code != 0 {
		t.Fatalf("merge trailer exit %d", code)
	}
	if body, _ := os.ReadFile(msg); strings.Contains(string(body), "AI-Cost") {
		t.Errorf("merge commit must not be stamped:\n%s", body)
	}
}

func seedMainEvent(t *testing.T, home string) {
	t.Helper()
	st, err := store.OpenFileStore(filepath.Join(home, ".aispend", "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := event.USD(420000)
	if err := st.Upsert([]event.AgentEvent{{
		EventID: "e1", SessionID: "s", Provider: "claude_code", GitBranch: "main",
		Model:     "claude-opus-4-8",
		Tokens:    event.Tokens{Input: 1000},
		CostViews: event.CostViews{APIEquivalent: &m},
		TSStart:   time.Unix(1000, 0).UTC(), TSEnd: time.Unix(1005, 0).UTC(),
	}}); err != nil {
		t.Fatal(err)
	}
}

func initMainRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q", "-b", "main").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return repo
}

// `[trailers] enabled = false` is the committed repo-wide off switch: even with the
// hooks installed and usage pending, no trailer attaches.
func TestCmdTrailer_ConfigDisabled(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	seedMainEvent(t, home)
	repo := initMainRepo(t)
	mustWrite(t, filepath.Join(repo, ".aispend.toml"), "[trailers]\nenabled = false\n")

	msg := filepath.Join(t.TempDir(), "MSG")
	mustWrite(t, msg, "Add\n")
	if _, _, code := run(t, "trailer", msg, "--source", "", "--repo", repo); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if body, _ := os.ReadFile(msg); strings.Contains(string(body), "Cost") {
		t.Errorf("enabled=false must suppress the trailer:\n%s", body)
	}
}

// `[trailers.rename] cost` + `cost_models` are honored from the committed config.
func TestCmdTrailer_ConfigRenameAndModels(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	seedMainEvent(t, home)
	repo := initMainRepo(t)
	mustWrite(t, filepath.Join(repo, ".aispend.toml"),
		"[trailers]\ncost_models = true\n\n[trailers.rename]\ncost = \"Claude-Cost-Equiv\"\n")

	msg := filepath.Join(t.TempDir(), "MSG")
	mustWrite(t, msg, "Add\n")
	if _, errs, code := run(t, "trailer", msg, "--source", "", "--repo", repo); code != 0 {
		t.Fatalf("exit %d err=%s", code, errs)
	}
	body, _ := os.ReadFile(msg)
	if !strings.Contains(string(body), "Claude-Cost-Equiv: 0.42") {
		t.Errorf("rename not applied:\n%s", body)
	}
	if !strings.Contains(string(body), "AI-Cost-Models: claude-opus-4-8=0.42") {
		t.Errorf("cost_models not applied:\n%s", body)
	}
}

// `aispend today` shows a read-only "pending commit" preview of what the next
// commit on this branch would be stamped with.
func TestCmdToday_ShowsPendingCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	seedMainEvent(t, home)
	repo := initMainRepo(t)

	out, _, code := run(t, "today", "--repo", repo)
	if code != 0 {
		t.Fatalf("today exit %d", code)
	}
	if !strings.Contains(out, "pending commit") || !strings.Contains(out, "main") || !strings.Contains(out, "0.42") {
		t.Errorf("today missing the pending-commit preview:\n%s", out)
	}
}

func TestCmdToday_NoPendingLineWhenClean(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	// No seeded usage → nothing pending.
	repo := initMainRepo(t)
	out, _, code := run(t, "today", "--repo", repo)
	if code != 0 {
		t.Fatalf("today exit %d", code)
	}
	if strings.Contains(out, "pending commit") {
		t.Errorf("no uncommitted usage → no pending line:\n%s", out)
	}
}

// The hook scans the live ~/.claude logs at commit time, so a turn logged since the
// last `aispend scan` is still stamped — no prior scan required.
func TestCmdTrailer_LiveScansAtCommitTime(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeLiveClaudeTurn(t, home)

	repo := initMainRepo(t)
	msg := filepath.Join(t.TempDir(), "MSG")
	mustWrite(t, msg, "Add\n")

	// Deliberately NO `aispend scan` first — the hook must scan the live logs itself.
	if _, errs, code := run(t, "trailer", msg, "--source", "", "--repo", repo); code != 0 {
		t.Fatalf("trailer exit %d err=%s", code, errs)
	}
	if body, _ := os.ReadFile(msg); !strings.Contains(string(body), "AI-Cost") {
		t.Errorf("trailer must live-scan and stamp a never-scanned turn:\n%s", body)
	}
}

// today's pending preview reads the ledger only — it must NOT trigger a live scan
// (that keeps `today` fast). A live, never-scanned turn yields no pending line.
func TestCmdToday_PreviewStaysStoreOnly(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeLiveClaudeTurn(t, home)

	repo := initMainRepo(t)
	out, _, code := run(t, "today", "--repo", repo)
	if code != 0 {
		t.Fatalf("today exit %d", code)
	}
	if strings.Contains(out, "pending commit") {
		t.Errorf("today must not live-scan — no pending line until `aispend scan`:\n%s", out)
	}
}

// writeLiveClaudeTurn drops one priced assistant turn on branch main into the Claude
// projects dir, simulating activity logged but not yet scanned into the ledger.
func writeLiveClaudeTurn(t *testing.T, home string) {
	t.Helper()
	proj := filepath.Join(home, ".claude", "projects", "repo")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","uuid":"a1","sessionId":"s","gitBranch":"main","timestamp":"2026-06-14T10:00:05Z","cwd":"/x/repo","message":{"id":"m1","model":"claude-opus-4-20250514","content":[],"usage":{"input_tokens":120000,"output_tokens":4000}}}` + "\n"
	mustWrite(t, filepath.Join(proj, "sess.jsonl"), line)
}

func mustWrite(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
