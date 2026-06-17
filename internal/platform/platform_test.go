package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeEnv builds an Env lookup backed by a map, so OS-specific environment
// variables (APPDATA, CLAUDE_CONFIG_DIR, ...) can be injected on any host.
func fakeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func slash(p string) string { return filepath.ToSlash(p) }

func TestAppHome_OverrideThenDefault(t *testing.T) {
	t.Run("AISPEND_HOME override wins", func(t *testing.T) {
		r := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(map[string]string{"AISPEND_HOME": "/custom/spend"})}
		if got := r.AppHome(); got != "/custom/spend" {
			t.Errorf("AppHome() = %q, want /custom/spend", got)
		}
	})
	t.Run("default is <home>/.aispend", func(t *testing.T) {
		r := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(nil)}
		want := filepath.Join("/home/dev", ".aispend")
		if got := r.AppHome(); got != want {
			t.Errorf("AppHome() = %q, want %q", got, want)
		}
	})
	t.Run("AppDBPath sits under AppHome", func(t *testing.T) {
		r := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(nil)}
		want := filepath.Join("/home/dev", ".aispend", "aispend.db")
		if got := r.AppDBPath(); got != want {
			t.Errorf("AppDBPath() = %q, want %q", got, want)
		}
	})
}

func TestProviderRoots_ClaudeCode(t *testing.T) {
	t.Run("default dotdir, all OSes", func(t *testing.T) {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			r := Resolver{GOOS: goos, Home: "/home/dev", Env: fakeEnv(nil)}
			roots := r.ProviderRoots("claude_code")
			if len(roots) == 0 || slash(roots[0]) != "/home/dev/.claude/projects" {
				t.Errorf("[%s] roots = %v, want ~/.claude/projects to rank first", goos, roots)
			}
		}
	})
	t.Run("CLAUDE_CONFIG_DIR override ranks first", func(t *testing.T) {
		r := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(map[string]string{"CLAUDE_CONFIG_DIR": "/opt/claude"})}
		roots := r.ProviderRoots("claude_code")
		if len(roots) < 2 || slash(roots[0]) != "/opt/claude/projects" {
			t.Errorf("override root = %v, want /opt/claude/projects first", roots)
		}
	})
	t.Run("macOS includes the Cowork desktop session tree", func(t *testing.T) {
		// Cowork runs Claude Code under its own per-session config dir nested in
		// the app-support tree; without this root a terminal scan misses ALL
		// desktop usage (the real-world Jun-15 $266.90 gap).
		r := Resolver{GOOS: "darwin", Home: "/home/dev", Env: fakeEnv(nil)}
		want := "/home/dev/Library/Application Support/Claude/local-agent-mode-sessions"
		found := false
		for _, p := range r.ProviderRoots("claude_code") {
			if slash(p) == want {
				found = true
			}
		}
		if !found {
			t.Errorf("claude_code roots %v missing Cowork base %q", r.ProviderRoots("claude_code"), want)
		}
	})
}

func TestProviderRoots_Cursor_PerOS(t *testing.T) {
	cases := map[string]struct {
		goos string
		env  map[string]string
		want string
	}{
		"darwin uses Application Support": {"darwin", nil, "/home/dev/Library/Application Support/Cursor"},
		"linux uses .config":              {"linux", nil, "/home/dev/.config/Cursor"},
		"windows uses APPDATA":            {"windows", map[string]string{"APPDATA": "/users/dev/AppData/Roaming"}, "/users/dev/AppData/Roaming/Cursor"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := Resolver{GOOS: c.goos, Home: "/home/dev", Env: fakeEnv(c.env)}
			roots := r.ProviderRoots("cursor")
			if len(roots) == 0 || slash(roots[0]) != c.want {
				t.Errorf("[%s] cursor root = %v, want %q", c.goos, roots, c.want)
			}
		})
	}
}

func TestExistingRoots_FiltersToRealDirs(t *testing.T) {
	tmp := t.TempDir()
	// create <tmp>/.claude/projects
	real := filepath.Join(tmp, ".claude", "projects")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	r := Resolver{GOOS: "linux", Home: tmp, Env: fakeEnv(nil)}
	got := r.ExistingRoots("claude_code")
	if len(got) != 1 || got[0] != real {
		t.Errorf("ExistingRoots = %v, want [%s]", got, real)
	}

	// a home with nothing present yields no roots (reported, not crashed)
	empty := Resolver{GOOS: "linux", Home: t.TempDir(), Env: fakeEnv(nil)}
	if got := empty.ExistingRoots("claude_code"); len(got) != 0 {
		t.Errorf("ExistingRoots on empty home = %v, want []", got)
	}
}

func TestHashPath(t *testing.T) {
	t.Run("case-insensitive OS: separators and case fold to same hash", func(t *testing.T) {
		a := HashPath(`C:\Users\Dev\.claude\projects\a.jsonl`, "windows")
		b := HashPath(`C:/users/dev/.claude/projects/a.jsonl`, "windows")
		if a != b {
			t.Errorf("windows variants hashed differently:\n a=%s\n b=%s", a, b)
		}
	})
	t.Run("case-sensitive OS: case matters", func(t *testing.T) {
		a := HashPath("/home/dev/A.jsonl", "linux")
		b := HashPath("/home/dev/a.jsonl", "linux")
		if a == b {
			t.Error("linux paths differing only by case should hash differently")
		}
	})
	t.Run("distinct paths never collide", func(t *testing.T) {
		a := HashPath("/home/dev/a.jsonl", "linux")
		b := HashPath("/home/dev/b.jsonl", "linux")
		if a == b {
			t.Error("distinct paths collided")
		}
	})
	t.Run("never returns the raw path", func(t *testing.T) {
		raw := "/home/dev/secret/a.jsonl"
		if h := HashPath(raw, "linux"); h == raw || len(h) != 64 {
			t.Errorf("HashPath leaked or wrong length: %q", h)
		}
	})
}

func TestProviderRoots_Codex(t *testing.T) {
	r := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(nil)}
	if got := r.ProviderRoots("codex"); len(got) != 1 || slash(got[0]) != "/home/dev/.codex/sessions" {
		t.Errorf("codex default = %v, want [/home/dev/.codex/sessions]", got)
	}
	r2 := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(map[string]string{"CODEX_HOME": "/opt/codex"})}
	if got := r2.ProviderRoots("codex"); len(got) != 2 || slash(got[0]) != "/opt/codex/sessions" {
		t.Errorf("codex override = %v, want /opt/codex/sessions first", got)
	}
}

func TestProviderRoots_CursorOverridesAndFallbacks(t *testing.T) {
	t.Run("explicit CURSOR_CONFIG_DIR override", func(t *testing.T) {
		r := Resolver{GOOS: "darwin", Home: "/home/dev", Env: fakeEnv(map[string]string{"CURSOR_CONFIG_DIR": "/x/cursor"})}
		if got := r.ProviderRoots("cursor"); len(got) != 1 || got[0] != "/x/cursor" {
			t.Errorf("override = %v, want [/x/cursor]", got)
		}
	})
	t.Run("linux honors XDG_CONFIG_HOME", func(t *testing.T) {
		r := Resolver{GOOS: "linux", Home: "/home/dev", Env: fakeEnv(map[string]string{"XDG_CONFIG_HOME": "/xdg"})}
		if got := r.ProviderRoots("cursor"); slash(got[0]) != "/xdg/Cursor" {
			t.Errorf("xdg = %v, want /xdg/Cursor", got)
		}
	})
	t.Run("windows falls back to home AppData when APPDATA unset", func(t *testing.T) {
		r := Resolver{GOOS: "windows", Home: "/home/dev", Env: fakeEnv(nil)}
		if got := r.ProviderRoots("cursor"); slash(got[0]) != "/home/dev/AppData/Roaming/Cursor" {
			t.Errorf("win fallback = %v", got)
		}
	})
}

func TestProviderRoots_UnknownProviderIsNil(t *testing.T) {
	r := Resolver{GOOS: "linux", Home: "/h", Env: fakeEnv(nil)}
	if got := r.ProviderRoots("nope"); got != nil {
		t.Errorf("unknown provider = %v, want nil", got)
	}
}

func TestResolver_NilEnvIsSafe(t *testing.T) {
	r := Resolver{GOOS: "linux", Home: "/home/dev"} // Env left nil
	if got := r.AppHome(); got != filepath.Join("/home/dev", ".aispend") {
		t.Errorf("nil-env AppHome = %v", got)
	}
}

func TestDetect_PopulatesGOOS(t *testing.T) {
	if r := Detect(); r.GOOS == "" {
		t.Error("Detect() returned empty GOOS")
	}
}
