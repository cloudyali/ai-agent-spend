package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// End-to-end through the CLI dispatch: `aispend git <install|status|uninstall> [dir]`.
func TestCmdGit_InstallStatusUninstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	if out, errs, code := run(t, "git", "install", repo); code != 0 || !strings.Contains(out, "installed") {
		t.Fatalf("git install: code=%d out=%q err=%q", code, out, errs)
	}
	if out, _, code := run(t, "git", "status", repo); code != 0 || !strings.Contains(out, "installed") {
		t.Errorf("git status: code=%d out=%q", code, out)
	}
	if _, _, code := run(t, "git", "uninstall", repo); code != 0 {
		t.Errorf("git uninstall: code=%d", code)
	}
}

func TestCmdGit_Errors(t *testing.T) {
	// No subcommand → usage error.
	if _, _, code := run(t, "git"); code != 2 {
		t.Errorf("bare `git` exit = %d, want 2", code)
	}
	// Unknown subcommand → usage error.
	if _, _, code := run(t, "git", "frobnicate"); code != 2 {
		t.Errorf("unknown subcommand exit = %d, want 2", code)
	}
	// A real directory that isn't a git repo → runtime error.
	if _, errs, code := run(t, "git", "install", t.TempDir()); code != 1 || !strings.Contains(errs, "not a git repository") {
		t.Errorf("install on non-repo: code=%d err=%q, want 1 + 'not a git repository'", code, errs)
	}
}
