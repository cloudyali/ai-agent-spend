package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/event"
)

func TestParseNumstat(t *testing.T) {
	in := "12\t3\tsrc/app.go\n0\t5\tREADME.md\n-\t-\tlogo.png\n\ngarbage\n"
	got := parseNumstat(in)
	want := []event.FileChurn{
		{Path: "src/app.go", Added: 12, Removed: 3},
		{Path: "README.md", Added: 0, Removed: 5},
	}
	if len(got) != len(want) {
		t.Fatalf("parseNumstat = %+v, want %+v (binary + garbage lines skipped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNumstat_EmptyRangeIsNil(t *testing.T) {
	if got := Numstat("/repo", "abc", "abc", nil); got != nil {
		t.Errorf("identical from/to must yield nil churn, got %+v", got)
	}
	if got := Numstat("", "a", "b", nil); got != nil {
		t.Errorf("empty repoRoot must yield nil, got %+v", got)
	}
}

// Integration: drive a real git repo (sandbox has git). Skipped where git is absent,
// so the offline/non-git gate still passes everywhere.
func TestNumstat_Integration(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)
		out, err := exec.Command("git", full...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q")
	write("f.go", "a\nb\n")
	git("add", "f.go")
	git("commit", "-q", "-m", "one")
	sha1 := git("rev-parse", "HEAD")
	write("f.go", "a\nb\nc\nd\n") // +2 lines
	git("commit", "-q", "-am", "two")
	sha2 := git("rev-parse", "HEAD")

	churn := Numstat(dir, sha1, sha2, []string{"f.go"})
	if len(churn) != 1 || churn[0].Path != "f.go" || churn[0].Added != 2 || churn[0].Removed != 0 {
		t.Errorf("Numstat = %+v, want one f.go +2/-0", churn)
	}
}
