// Package platform centralizes OS-aware path discovery. Most of aispend's data
// is sourced from local files whose locations differ across macOS, Linux, and
// Windows, so every provider asks this layer where an agent's files live on the
// current machine rather than hardcoding a path. The Resolver's inputs (GOOS,
// home, env) are injectable, which makes per-OS logic unit-testable from any host.
//
// See design-documents/DESIGN.md.
package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path" // deliberately the slash-based package, used only to normalize for hashing
	"path/filepath"
	"runtime"
	"strings"
)

// Resolver answers "where do this agent's files live on this machine?" from
// injectable inputs so the same logic is testable for every OS.
type Resolver struct {
	GOOS string              // "darwin" | "linux" | "windows"
	Home string              // user home directory
	Env  func(string) string // environment lookup (os.Getenv in production)
}

// Detect wires the real OS, environment, and home directory.
func Detect() Resolver {
	home, _ := os.UserHomeDir()
	return Resolver{GOOS: runtime.GOOS, Home: home, Env: os.Getenv}
}

func (r Resolver) env(k string) string {
	if r.Env == nil {
		return ""
	}
	return r.Env(k)
}

// AppHome is aispend's own data/config directory. A single visible ~/.aispend
// (overridable via AISPEND_HOME) keeps the README promise legible; the Windows
// equivalent resolves under the user profile via the same join.
func (r Resolver) AppHome() string {
	if v := r.env("AISPEND_HOME"); v != "" {
		return v
	}
	return filepath.Join(r.Home, ".aispend")
}

// AppDBPath is the local SQLite database path.
func (r Resolver) AppDBPath() string {
	return filepath.Join(r.AppHome(), "aispend.db")
}

// ClaudeUsagePath is the local usage snapshot Claude Code caches — the weekly / 5h /
// Opus rate-limit windows — honoring CLAUDE_CONFIG_DIR, else ~/.claude/usage-exact.json.
// It is a point-in-time snapshot Claude Code refreshes itself; aispend only reads it.
func (r Resolver) ClaudeUsagePath() string {
	base := r.env("CLAUDE_CONFIG_DIR")
	if base == "" {
		base = filepath.Join(r.Home, ".claude")
	}
	return filepath.Join(base, "usage-exact.json")
}

// ProviderRoots returns the ordered candidate root directories for an agent's
// local data on this OS. Env overrides rank first. The list is candidates, not
// guarantees — callers use ExistingRoots to filter to what is actually present.
func (r Resolver) ProviderRoots(provider string) []string {
	switch provider {
	case "claude_code":
		var roots []string
		if v := r.env("CLAUDE_CONFIG_DIR"); v != "" {
			roots = append(roots, filepath.Join(v, "projects"))
		}
		roots = append(roots, filepath.Join(r.Home, ".claude", "projects"))
		// Cowork (the Claude desktop app) runs Claude Code under its own per-session
		// config dir nested in the app-support tree, so a terminal scan that reads
		// only ~/.claude/projects misses ALL desktop usage. Add that tree as a
		// candidate root; ExistingRoots drops it when absent and the walker filters
		// to .claude/projects transcripts (skipping outputs/uploads).
		return append(roots, r.coworkRoots()...)

	case "codex":
		var roots []string
		if v := r.env("CODEX_HOME"); v != "" {
			roots = append(roots, filepath.Join(v, "sessions"))
		}
		return append(roots, filepath.Join(r.Home, ".codex", "sessions"))

	case "cursor":
		if v := r.env("CURSOR_CONFIG_DIR"); v != "" {
			return []string{v}
		}
		switch r.GOOS {
		case "darwin":
			return []string{filepath.Join(r.Home, "Library", "Application Support", "Cursor")}
		case "windows":
			base := r.env("APPDATA")
			if base == "" {
				base = filepath.Join(r.Home, "AppData", "Roaming")
			}
			return []string{filepath.Join(base, "Cursor")}
		default: // linux and others
			base := r.env("XDG_CONFIG_HOME")
			if base == "" {
				base = filepath.Join(r.Home, ".config")
			}
			return []string{filepath.Join(base, "Cursor")}
		}
	}
	return nil
}

// coworkRoots returns the base dir(s) where the Claude desktop app (Cowork)
// stores its per-session Claude Code transcripts (each under a nested
// <session>/.claude/projects/<mangled-cwd>/<uuid>.jsonl). Empty when the OS has
// no known location. Windows path is a best-effort candidate; ExistingRoots
// filters to what is actually present, so a wrong guess is simply skipped.
func (r Resolver) coworkRoots() []string {
	switch r.GOOS {
	case "darwin":
		return []string{filepath.Join(r.Home, "Library", "Application Support", "Claude", "local-agent-mode-sessions")}
	case "windows":
		base := r.env("APPDATA")
		if base == "" {
			base = filepath.Join(r.Home, "AppData", "Roaming")
		}
		return []string{filepath.Join(base, "Claude", "local-agent-mode-sessions")}
	default:
		return nil
	}
}

// ExistingRoots filters ProviderRoots to directories that actually exist, so a
// missing source is reported (empty result) rather than crashing a scan.
func (r Resolver) ExistingRoots(provider string) []string {
	var out []string
	for _, p := range r.ProviderRoots(provider) {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// HashPath returns a stable, platform-normalized SHA-256 hex digest of p. Raw
// paths are never stored or exported — only this digest. Normalization unifies
// separators and, on case-insensitive filesystems (Windows, macOS), case-folds,
// so the same file yields the same hash however it was referenced.
func HashPath(p, goos string) string {
	s := strings.ReplaceAll(p, `\`, "/")
	s = path.Clean(s)
	if goos == "windows" || goos == "darwin" {
		s = strings.ToLower(s)
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
