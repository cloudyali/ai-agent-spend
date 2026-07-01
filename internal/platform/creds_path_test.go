package platform

import (
	"path/filepath"
	"testing"
)

func TestClaudeCredentialsPath(t *testing.T) {
	r := Resolver{GOOS: "linux", Home: "/home/u", Env: func(string) string { return "" }}
	want := filepath.Join("/home/u", ".claude", ".credentials.json")
	if got := r.ClaudeCredentialsPath(); got != want {
		t.Errorf("ClaudeCredentialsPath = %q, want %q", got, want)
	}
	// CLAUDE_CONFIG_DIR overrides the base.
	r2 := Resolver{GOOS: "linux", Home: "/home/u", Env: func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return "/cfg"
		}
		return ""
	}}
	if got := r2.ClaudeCredentialsPath(); got != filepath.Join("/cfg", ".credentials.json") {
		t.Errorf("CLAUDE_CONFIG_DIR override = %q", got)
	}
}

func TestCodexAuthPaths(t *testing.T) {
	r := Resolver{GOOS: "linux", Home: "/home/u", Env: func(string) string { return "" }}
	got := r.CodexAuthPaths()
	want := []string{
		filepath.Join("/home/u", ".config", "codex", "auth.json"),
		filepath.Join("/home/u", ".codex", "auth.json"),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("CodexAuthPaths = %v, want %v", got, want)
	}
	// CODEX_HOME collapses to a single path.
	r2 := Resolver{GOOS: "linux", Home: "/home/u", Env: func(k string) string {
		if k == "CODEX_HOME" {
			return "/ch"
		}
		return ""
	}}
	if g := r2.CodexAuthPaths(); len(g) != 1 || g[0] != filepath.Join("/ch", "auth.json") {
		t.Errorf("CODEX_HOME override = %v", g)
	}
}
