package trailer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentspend/ai-agent-spend/internal/event"
)

// --- pure formatting ---

func TestFormatCost(t *testing.T) {
	cases := []struct {
		micros int64
		prec   int
		want   string
	}{
		{420000, 2, "0.42"},
		{1_000_000, 2, "1.00"},
		{1_400_000, 0, "1"},
		{12_345_678, 2, "12.35"},
	}
	for _, c := range cases {
		if got := formatCost(c.micros, c.prec); got != c.want {
			t.Errorf("formatCost(%d,%d) = %q, want %q", c.micros, c.prec, got, c.want)
		}
	}
}

func TestFormatTrailers_AllLines(t *testing.T) {
	u := Usage{
		Cost:     event.USD(420000),
		Tokens:   128944,
		Requests: 37,
		PerModel: map[string]int64{"claude-opus-4-8": 410000, "claude-haiku-4-5": 10000},
	}
	cfg := Config{Cost: true, CostModels: true, Tokens: true, Interactions: true, Precision: 2, CostName: "AI-Cost"}
	got := FormatTrailers(u, cfg)
	want := []string{
		"AI-Cost: 0.42",
		"AI-Cost-Models: claude-haiku-4-5=0.01,claude-opus-4-8=0.41", // sorted by model
		"AI-Tokens: 128944",
		"AI-Interactions: 37",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("FormatTrailers =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFormatTrailers_ZeroCostEmitsNothing(t *testing.T) {
	// A turn with no attributable cost must not produce "AI-Cost: 0.00".
	if got := FormatTrailers(Usage{Cost: event.USD(0), Requests: 0}, DefaultConfig()); len(got) != 0 {
		t.Errorf("zero usage must emit no trailers, got %v", got)
	}
}

func TestFormatTrailers_SanitizesNewlines(t *testing.T) {
	// A crafted model name must not be able to inject a second line / fake trailer
	// into the commit message.
	u := Usage{Cost: event.USD(420000), PerModel: map[string]int64{"opus\nFake-Trailer: pwned": 420000}}
	cfg := Config{Cost: true, CostModels: true, Precision: 2, CostName: "AI-Cost"}
	lines := FormatTrailers(u, cfg)
	for _, ln := range lines {
		if strings.ContainsAny(ln, "\r\n") {
			t.Errorf("trailer line carries a newline (injection): %q", ln)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "Fake-Trailer") {
		t.Errorf("expected the crafted text to survive inline (just neutralized): %v", lines)
	}
}

func TestFormatTrailers_Rename(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CostName = "Claude-Cost-Equiv"
	got := FormatTrailers(Usage{Cost: event.USD(420000)}, cfg)
	if len(got) != 1 || got[0] != "Claude-Cost-Equiv: 0.42" {
		t.Errorf("rename = %v, want [Claude-Cost-Equiv: 0.42]", got)
	}
}

// --- apply: append / idempotency / squash-fold / comments ---

func TestApply_AppendsBlock(t *testing.T) {
	out := Apply("Add the thing\n", Usage{Cost: event.USD(420000)}, DefaultConfig(), ModeNormal)
	if !strings.Contains(out, "Add the thing") || !strings.Contains(out, "\nAI-Cost: 0.42\n") {
		t.Errorf("apply = %q", out)
	}
	// Blank line must separate body from the trailer (git trailer convention).
	if !strings.Contains(out, "thing\n\nAI-Cost: 0.42") {
		t.Errorf("trailer must be its own block:\n%q", out)
	}
}

func TestApply_Idempotent(t *testing.T) {
	cfg := DefaultConfig()
	u := Usage{Cost: event.USD(420000)}
	once := Apply("Add the thing\n", u, cfg, ModeNormal)
	twice := Apply(once, u, cfg, ModeNormal)
	if n := strings.Count(twice, "AI-Cost:"); n != 1 {
		t.Errorf("prepare-commit-msg firing twice must not duplicate: got %d AI-Cost lines\n%s", n, twice)
	}
}

func TestApply_SquashFolds(t *testing.T) {
	// Two commits' trailers carried in by a squash must fold into one summed line.
	msg := "Squashed\n\nAI-Cost: 0.40\n\nmore\n\nAI-Cost: 0.02\n"
	out := Apply(msg, Usage{}, DefaultConfig(), ModeSquash)
	if n := strings.Count(out, "AI-Cost:"); n != 1 {
		t.Fatalf("squash must collapse to one AI-Cost line, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "AI-Cost: 0.42") {
		t.Errorf("squash must sum 0.40+0.02=0.42:\n%s", out)
	}
}

func TestApply_NoUsageLeavesMessage(t *testing.T) {
	msg := "Trivial typo fix\n"
	if out := Apply(msg, Usage{}, DefaultConfig(), ModeNormal); out != msg {
		t.Errorf("no usage must leave the message untouched, got %q", out)
	}
}

func TestApply_TrailerBeforeComments(t *testing.T) {
	msg := "Title\n\n# Please enter the commit message for your changes.\n# Lines starting with '#' are ignored.\n"
	out := Apply(msg, Usage{Cost: event.USD(420000)}, DefaultConfig(), ModeNormal)
	ai := strings.Index(out, "AI-Cost:")
	hash := strings.Index(out, "# Please")
	if ai < 0 || hash < 0 || ai > hash {
		t.Errorf("trailer must sit before the comment block:\n%s", out)
	}
}

// --- source routing ---

func TestModeForSource(t *testing.T) {
	cases := []struct {
		src  string
		mode Mode
		skip bool
	}{
		{"", ModeNormal, false},
		{"message", ModeNormal, false},
		{"template", ModeNormal, false},
		{"merge", ModeNormal, true},  // merge commit — reuse, don't attribute
		{"commit", ModeNormal, true}, // -c/-C/--amend reuse — message already stamped
		{"squash", ModeSquash, false},
	}
	for _, c := range cases {
		m, skip := modeForSource(c.src)
		if m != c.mode || skip != c.skip {
			t.Errorf("modeForSource(%q) = (%v,%v), want (%v,%v)", c.src, m, skip, c.mode, c.skip)
		}
	}
}

// --- watermark store ---

func TestWatermark_StagePromoteGet(t *testing.T) {
	gd := t.TempDir()
	w := watermark{gitDir: gd}

	if _, ok := w.get("main"); ok {
		t.Error("fresh watermark must be absent")
	}
	if err := w.stage("main", time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.get("main"); ok {
		t.Error("stage writes the PENDING mark; committed must still be absent until promote")
	}
	if err := w.promote(); err != nil {
		t.Fatal(err)
	}
	if ts, ok := w.get("main"); !ok || !ts.Equal(time.Unix(100, 0)) {
		t.Errorf("after promote get(main) = %v,%v, want 100,true", ts, ok)
	}

	// A second branch promotes independently and must not clobber main.
	_ = w.stage("dev", time.Unix(200, 0))
	_ = w.promote()
	if ts, _ := w.get("dev"); !ts.Equal(time.Unix(200, 0)) {
		t.Errorf("dev watermark = %v, want 200", ts)
	}
	if ts, _ := w.get("main"); !ts.Equal(time.Unix(100, 0)) {
		t.Errorf("main watermark clobbered = %v, want 100", ts)
	}
}

func TestRebaseInProgress(t *testing.T) {
	gd := t.TempDir()
	if rebaseInProgress(gd) {
		t.Error("no rebase dir → false")
	}
	if err := os.MkdirAll(filepath.Join(gd, "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !rebaseInProgress(gd) {
		t.Error("rebase-merge dir → true")
	}
}

// --- integration: real git repo ---

func TestTrailer_AttachesThenConsumeAdvancesWatermark(t *testing.T) {
	dir := newGitRepo(t)
	msg := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	writeFile(t, msg, "Add the churn heatmap\n")

	calls := 0
	pending := func(repoDir, branch string, since time.Time) (Usage, error) {
		calls++
		if branch != "main" {
			t.Errorf("branch = %q, want main", branch)
		}
		// First call: watermark is zero, so everything is pending.
		if calls == 1 && !since.IsZero() {
			t.Errorf("first call since = %v, want zero", since)
		}
		// Second call (after consume): nothing newer than the advanced watermark.
		if calls == 2 {
			if !since.Equal(time.Unix(1000, 0)) {
				t.Errorf("second call since = %v, want 1000 (advanced)", since)
			}
			return Usage{}, nil
		}
		return Usage{Cost: event.USD(420000), Requests: 3, MaxTS: time.Unix(1000, 0)}, nil
	}
	clock := func() time.Time { return time.Unix(9999, 0) }

	if err := Trailer(dir, "", msg, DefaultConfig(), pending, clock); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(msg); !strings.Contains(string(body), "AI-Cost: 0.42") {
		t.Errorf("first commit message missing trailer:\n%s", body)
	}
	gd, _ := gitDirOf(dir)
	if _, ok := (watermark{gitDir: gd}).get("main"); ok {
		t.Error("watermark must not advance until consume (a cancelled commit carries usage forward)")
	}

	if err := Consume(dir); err != nil {
		t.Fatal(err)
	}
	if ts, ok := (watermark{gitDir: gd}).get("main"); !ok || !ts.Equal(time.Unix(1000, 0)) {
		t.Errorf("after consume watermark = %v,%v, want 1000", ts, ok)
	}

	// Second trailer run sees the advanced watermark and attaches nothing.
	writeFile(t, msg, "Follow-up\n")
	if err := Trailer(dir, "", msg, DefaultConfig(), pending, clock); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(msg); strings.Contains(string(body), "AI-Cost") {
		t.Errorf("no new usage → no trailer, got:\n%s", body)
	}
}

func TestTrailer_MergeSourceSkips(t *testing.T) {
	dir := newGitRepo(t)
	msg := filepath.Join(t.TempDir(), "MSG")
	writeFile(t, msg, "Merge branch 'x'\n")
	pending := func(string, string, time.Time) (Usage, error) {
		t.Error("merge source must not even compute usage")
		return Usage{}, nil
	}
	if err := Trailer(dir, "merge", msg, DefaultConfig(), pending, time.Now); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(msg); strings.Contains(string(body), "AI-Cost") {
		t.Errorf("merge commit must get no trailer:\n%s", body)
	}
}

func TestConsume_NoOpDuringRebase(t *testing.T) {
	dir := newGitRepo(t)
	gd, _ := gitDirOf(dir)
	_ = (watermark{gitDir: gd}).stage("main", time.Unix(5, 0))
	if err := os.MkdirAll(filepath.Join(gd, "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Consume(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := (watermark{gitDir: gd}).get("main"); ok {
		t.Error("consume must be a no-op during rebase (usage survives the replay)")
	}
}

func TestTrailer_DetachedHeadSkips(t *testing.T) {
	dir := newGitRepo(t)
	// Make a commit, then detach HEAD onto it.
	gitIn(t, dir, "commit", "--allow-empty", "-q", "-m", "one")
	sha := gitOut(t, dir, "rev-parse", "HEAD")
	gitIn(t, dir, "checkout", "-q", sha)

	msg := filepath.Join(t.TempDir(), "MSG")
	writeFile(t, msg, "On a detached head\n")
	pending := func(string, string, time.Time) (Usage, error) {
		return Usage{Cost: event.USD(500000), Requests: 1, MaxTS: time.Unix(1, 0)}, nil
	}
	if err := Trailer(dir, "", msg, DefaultConfig(), pending, time.Now); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(msg); strings.Contains(string(body), "AI-Cost") {
		t.Errorf("detached HEAD has no branch to attribute to — no trailer:\n%s", body)
	}
}

func TestPreview(t *testing.T) {
	dir := newGitRepo(t)
	stub := func(_, branch string, _ time.Time) (Usage, error) {
		if branch != "main" {
			t.Errorf("branch = %q, want main", branch)
		}
		return Usage{Cost: event.USD(420000), Requests: 3, MaxTS: time.Unix(1, 0)}, nil
	}
	u, branch, ok := Preview(dir, stub)
	if !ok || branch != "main" || u.Cost.Micros != 420000 || u.Requests != 3 {
		t.Fatalf("Preview = %+v, %q, %v; want usage 0.42/3 on main", u, branch, ok)
	}

	empty := func(string, string, time.Time) (Usage, error) { return Usage{}, nil }
	if _, _, ok := Preview(dir, empty); ok {
		t.Error("no attributable usage → ok must be false (no pending line)")
	}
	if _, _, ok := Preview(t.TempDir(), stub); ok {
		t.Error("non-repo → ok must be false")
	}
}

func TestReadCommitCost(t *testing.T) {
	dir := newGitRepo(t)
	gitIn(t, dir, "commit", "--allow-empty", "-q", "-m", "Add feature\n\nAI-Cost: 0.42")
	sha := gitOut(t, dir, "rev-parse", "HEAD")
	if micros, ok := ReadCommitCost(dir, sha, "AI-Cost"); !ok || micros != 420000 {
		t.Fatalf("ReadCommitCost = %d,%v; want 420000,true", micros, ok)
	}
	// a renamed trailer key is honored
	if micros, ok := ReadCommitCost(dir, sha, "Claude-Cost-Equiv"); ok || micros != 0 {
		t.Errorf("wrong key must not match: %d,%v", micros, ok)
	}

	gitIn(t, dir, "commit", "--allow-empty", "-q", "-m", "no trailer here")
	sha2 := gitOut(t, dir, "rev-parse", "HEAD")
	if _, ok := ReadCommitCost(dir, sha2, "AI-Cost"); ok {
		t.Error("a commit with no trailer → ok=false")
	}
	if _, ok := ReadCommitCost(dir, "deadbeefdeadbeef", "AI-Cost"); ok {
		t.Error("an unknown sha (not in this repo) → ok=false")
	}
}

// --- helpers ---

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	return dir
}

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
