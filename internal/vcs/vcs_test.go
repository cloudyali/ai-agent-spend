package vcs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var (
	shaZero = strings.Repeat("0", 40)
	shaA    = strings.Repeat("a", 40)
	shaB    = strings.Repeat("b", 40)
	shaC    = strings.Repeat("c", 40)
)

// reflogLine renders one .git/logs/HEAD entry:
// "<old> <new> Name <email> <unixts> <tz>\t<message>".
func reflogLine(old, new string, ts time.Time, msg string) string {
	return fmt.Sprintf("%s %s Dev <dev@example.com> %d +0000\t%s", old, new, ts.Unix(), msg)
}

// writeRepo creates a repoRoot with a real .git dir holding the given reflog lines.
func writeRepo(t *testing.T, lines []string) string {
	t.Helper()
	root := t.TempDir()
	logsDir := filepath.Join(root, ".git", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(logsDir, "HEAD"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestHeadAt(t *testing.T) {
	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	root := writeRepo(t, []string{
		reflogLine(shaZero, shaA, t1, "commit (initial): init"),
		reflogLine(shaA, shaB, t2, "commit: second"),
		reflogLine(shaB, shaC, t3, "commit: third"),
	})

	cases := []struct {
		name string
		at   time.Time
		want string
		ok   bool
	}{
		{"between second and third → second's sha", t2.Add(20 * time.Minute), shaB, true},
		{"exactly at an entry → that entry (>= semantics)", t2, shaB, true},
		{"after last entry → last sha", t3.Add(time.Hour), shaC, true},
		{"at first entry → first sha", t1, shaA, true},
		{"before the reflog starts → not resolvable", t1.Add(-time.Minute), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := HeadAt(root, c.at)
			if got != c.want || ok != c.ok {
				t.Errorf("HeadAt(%s) = (%q, %v), want (%q, %v)", c.at, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestHeadAt_NoReflogIsBestEffort(t *testing.T) {
	// A repo root with no .git at all, and one with .git but no logs/HEAD.
	cases := map[string]string{
		"no .git": t.TempDir(),
		"git but no reflog": func() string {
			r := t.TempDir()
			if err := os.MkdirAll(filepath.Join(r, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			return r
		}(),
	}
	for name, root := range cases {
		t.Run(name, func(t *testing.T) {
			if got, ok := HeadAt(root, time.Now()); ok || got != "" {
				t.Errorf("expected (\"\", false) for %s, got (%q, %v)", name, got, ok)
			}
		})
	}
}

func TestHeadAt_EmptyRepoRoot(t *testing.T) {
	if got, ok := HeadAt("", time.Now()); ok || got != "" {
		t.Errorf("empty repoRoot must be a no-op, got (%q, %v)", got, ok)
	}
}

func TestHeadAt_SkipsMalformedLines(t *testing.T) {
	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 14, 11, 0, 0, 0, time.UTC)
	root := writeRepo(t, []string{
		"garbage line with too few fields",
		reflogLine(shaZero, shaA, t1, "commit (initial): init"),
		"", // blank
		reflogLine(shaA, shaB, t2, "commit: second"),
	})
	if got, ok := HeadAt(root, t2.Add(time.Minute)); !ok || got != shaB {
		t.Errorf("HeadAt with malformed lines = (%q, %v), want (%q, true)", got, ok, shaB)
	}
}

func TestHeadAt_WorktreeGitFileIndirection(t *testing.T) {
	// A linked worktree stores `.git` as a FILE: "gitdir: <path>". The reflog the
	// worktree writes lives under that path's logs/HEAD.
	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	realGitDir := t.TempDir()
	logsDir := filepath.Join(realGitDir, "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "HEAD"),
		[]byte(reflogLine(shaZero, shaA, t1, "commit (initial): init")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"),
		[]byte("gitdir: "+realGitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := HeadAt(root, t1.Add(time.Minute)); !ok || got != shaA {
		t.Errorf("HeadAt via .git-file indirection = (%q, %v), want (%q, true)", got, ok, shaA)
	}
}

func TestHeadAt_WorktreeRelativeGitdir(t *testing.T) {
	// Submodules write a RELATIVE gitdir, resolved against repoRoot.
	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	root := t.TempDir()
	logsDir := filepath.Join(root, "realgit", "logs")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logsDir, "HEAD"),
		[]byte(reflogLine(shaZero, shaA, t1, "init")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: realgit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := HeadAt(root, t1.Add(time.Minute)); !ok || got != shaA {
		t.Errorf("HeadAt via relative gitdir = (%q, %v), want (%q, true)", got, ok, shaA)
	}
}

// Defense-in-depth: a crafted reflog whose "new sha" is a git option (not a hash)
// must be rejected, so it can never be passed through to `git diff` as a flag.
func TestHeadAt_RejectsNonHexNewSHA(t *testing.T) {
	t1 := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	root := writeRepo(t, []string{
		reflogLine(shaZero, "--output=/tmp/evil", t1, "crafted"),
	})
	if got, ok := HeadAt(root, t1.Add(time.Minute)); ok || got != "" {
		t.Errorf("non-hex new-sha must be rejected, got (%q, %v)", got, ok)
	}
}

func TestHeadAt_GitFileWithoutGitdirPointer(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir pointer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, ok := HeadAt(root, time.Now()); ok || got != "" {
		t.Errorf("malformed .git pointer must be best-effort empty, got (%q, %v)", got, ok)
	}
}
