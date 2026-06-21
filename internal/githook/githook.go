// Package githook installs, removes, and reports aispend's per-commit cost-trailer
// git hooks (prepare-commit-msg + post-commit). It is the write-path setup for the
// commit-trailer feature — see design-documents/11-commit-cost-trailers.md.
//
// Two safety rules dominate the design, both aimed at the same failure mode — a
// hook that silently never runs:
//
//   - Honor core.hooksPath FIRST. Husky v9+ / Lefthook commonly repoint it away from
//     .git/hooks; when it's set, git ignores .git/hooks entirely, so dropping a file
//     there would never fire. We detect that, refuse to write a dead hook, and print
//     paste-ready wiring instead (KindManaged).
//   - Refuse to clobber. We never overwrite a prepare-commit-msg / post-commit we
//     didn't author (doing so would silently break a team's lint/format pipeline).
//     A foreign hook yields KindRefused with wiring guidance — and the refusal is
//     atomic (we never write one hook then refuse the other).
//
// Git is consulted via the binary for repo/hooks-dir/config discovery (resolve),
// the same local-only, no-network seam as internal/vcs/numstat.go — so the offline
// build and `doctor --network` promise are unaffected. The shims themselves are
// fail-open: a missing or failing aispend never blocks a commit.
package githook

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// hookNames are the two hooks we manage, in install/scan order.
var hookNames = []string{"prepare-commit-msg", "post-commit"}

// markerSentinel is the substring that identifies a hook as ours. It must survive
// in any shim we write so isOurHook can recognize (and uninstall) it later.
const markerSentinel = "aispend-managed-hook"

const markerLine = "# " + markerSentinel + " — managed by `aispend git`; fail-open, remove with `aispend git uninstall`"

// ErrNotRepo is returned when the target directory isn't inside a git repository.
var ErrNotRepo = errors.New("not a git repository")

// Manager is a detected git hook manager (Husky / Lefthook / pre-commit), or the
// zero value when none is found. Evidence is the file/dir that revealed it.
type Manager struct {
	Name     string
	Evidence string
}

// Kind is the outcome of an Install / Uninstall / Status call.
type Kind string

const (
	KindInstalled    Kind = "installed"
	KindManaged      Kind = "managed"
	KindRefused      Kind = "refused"
	KindUninstalled  Kind = "uninstalled"
	KindNotInstalled Kind = "not_installed"
	KindForeign      Kind = "foreign"
)

// Report is the structured result the CLI renders. Keeping the text in Render (not
// the CLI) makes the user-facing wiring guidance unit-testable.
type Report struct {
	Kind     Kind
	Manager  Manager
	HooksDir string
	Removed  int
	Wired    bool
}

// ExitCode maps a report to a process exit code: only a genuine refusal is a
// failure. The managed path (we deliberately didn't write) is a success — it's the
// correct outcome for a hook-manager repo, with guidance printed.
func (r Report) ExitCode() int {
	if r.Kind == KindRefused {
		return 1
	}
	return 0
}

// Render returns the user-facing lines for a report.
func (r Report) Render() []string {
	switch r.Kind {
	case KindInstalled:
		return []string{
			"✓ aispend trailer hooks installed (" + r.HooksDir + ")",
			"  trailers attach on your next commit; tune them in .aispend.toml [trailers]",
			"  `aispend git status` to check · `aispend git uninstall` to remove",
		}
	case KindUninstalled:
		return []string{fmt.Sprintf("✓ removed %d aispend hook(s) from %s", r.Removed, r.HooksDir)}
	case KindNotInstalled:
		return []string{"aispend trailer hooks are not installed — run `aispend git install`"}
	case KindManaged:
		if r.Wired {
			return []string{"✓ managed by " + managerName(r.Manager) + " — trailer wiring: detected"}
		}
		out := []string{
			"⚠ hooks here run through " + managerName(r.Manager) + " (core.hooksPath) — aispend won't drop a file git would ignore",
			"  wire these two lines into your manager instead:",
		}
		out = append(out, wiringLines()...)
		out = append(out, "  then `aispend git status` flips: trailer wiring: NOT detected → detected")
		return out
	case KindForeign:
		out := []string{
			"⚠ a non-aispend hook owns prepare-commit-msg (" + managerName(r.Manager) + "?) — aispend is not installed",
			"  wire aispend into your manager instead:",
		}
		return append(out, wiringLines()...)
	case KindRefused:
		out := []string{
			"✗ refused: " + filepath.Join(r.HooksDir, "prepare-commit-msg") + " already exists and isn't ours — not overwriting it",
			"  (" + managerName(r.Manager) + " may own it) — wire aispend in instead:",
		}
		return append(out, wiringLines()...)
	default:
		return []string{string(r.Kind)}
	}
}

// Install installs the hook pair into repoDir's effective hooks directory, honoring
// the two safety rules described in the package doc.
func Install(repoDir string) (Report, error) {
	lay, err := resolve(repoDir)
	if err != nil {
		return Report{}, err
	}
	if lay.CoreHooksSet {
		mgr := DetectManager(repoDir)
		return Report{Kind: KindManaged, Manager: mgr, HooksDir: lay.HooksDir, Wired: wiringDetected(lay, mgr, repoDir)}, nil
	}
	// Refuse-to-clobber pre-scan: decide before writing anything, so a refusal is
	// atomic (never one hook written then the other refused).
	for _, name := range hookNames {
		if body, ok := readFile(filepath.Join(lay.HooksDir, name)); ok && !isOurHook(body) {
			return Report{Kind: KindRefused, Manager: DetectManager(repoDir), HooksDir: lay.HooksDir}, nil
		}
	}
	if err := os.MkdirAll(lay.HooksDir, 0o755); err != nil {
		return Report{}, err
	}
	bin := aispendBinary()
	for _, name := range hookNames {
		p := filepath.Join(lay.HooksDir, name)
		if err := os.WriteFile(p, []byte(hookScript(name, bin)), 0o755); err != nil {
			return Report{}, err
		}
		// Re-assert the exec bit: WriteFile's mode is masked by umask, and a hook
		// that isn't executable silently never runs.
		if err := os.Chmod(p, 0o755); err != nil {
			return Report{}, err
		}
	}
	return Report{Kind: KindInstalled, HooksDir: lay.HooksDir}, nil
}

// Uninstall removes only the hooks we authored, leaving foreign hooks (and any
// committed config) untouched.
func Uninstall(repoDir string) (Report, error) {
	lay, err := resolve(repoDir)
	if err != nil {
		return Report{}, err
	}
	removed := 0
	for _, name := range hookNames {
		p := filepath.Join(lay.HooksDir, name)
		if body, ok := readFile(p); ok && isOurHook(body) {
			if err := os.Remove(p); err != nil {
				return Report{}, err
			}
			removed++
		}
	}
	if removed == 0 {
		return Report{Kind: KindNotInstalled, HooksDir: lay.HooksDir}, nil
	}
	return Report{Kind: KindUninstalled, HooksDir: lay.HooksDir, Removed: removed}, nil
}

// Status reports whether the trailer hooks are wired for this repo — including the
// managed case, where it surfaces whether the user's manager carries our wiring
// (so a managed repo never silently looks "not installed").
func Status(repoDir string) (Report, error) {
	lay, err := resolve(repoDir)
	if err != nil {
		return Report{}, err
	}
	mgr := DetectManager(repoDir)
	if lay.CoreHooksSet {
		return Report{Kind: KindManaged, Manager: mgr, HooksDir: lay.HooksDir, Wired: wiringDetected(lay, mgr, repoDir)}, nil
	}
	present, foreign := 0, false
	for _, name := range hookNames {
		if body, ok := readFile(filepath.Join(lay.HooksDir, name)); ok {
			if isOurHook(body) {
				present++
			} else {
				foreign = true
			}
		}
	}
	switch {
	case present == len(hookNames):
		return Report{Kind: KindInstalled, HooksDir: lay.HooksDir}, nil
	case foreign:
		return Report{Kind: KindForeign, Manager: mgr, HooksDir: lay.HooksDir}, nil
	default:
		return Report{Kind: KindNotInstalled, HooksDir: lay.HooksDir}, nil
	}
}

// DetectManager best-effort identifies a hook manager from the working-tree root.
func DetectManager(repoDir string) Manager {
	top := repoDir
	if t, err := runGit(repoDir, "rev-parse", "--show-toplevel"); err == nil && t != "" {
		top = t
	}
	return detectManagerIn(top)
}

// --- internals ---

type layout struct {
	RepoDir      string
	GitDir       string
	HooksDir     string
	CoreHooksSet bool
}

// resolve locates the git dir and the *effective* hooks directory for repoDir,
// honoring core.hooksPath (absolute, or relative to the working-tree root).
func resolve(repoDir string) (layout, error) {
	gitDir, err := runGit(repoDir, "rev-parse", "--absolute-git-dir")
	if err != nil || gitDir == "" {
		return layout{}, ErrNotRepo
	}
	lay := layout{RepoDir: repoDir, GitDir: gitDir, HooksDir: filepath.Join(gitDir, "hooks")}
	if hp, err := runGit(repoDir, "config", "--get", "core.hooksPath"); err == nil && hp != "" {
		lay.CoreHooksSet = true
		if filepath.IsAbs(hp) {
			lay.HooksDir = hp
		} else {
			base := repoDir
			if top, err := runGit(repoDir, "rev-parse", "--show-toplevel"); err == nil && top != "" {
				base = top
			}
			lay.HooksDir = filepath.Join(base, hp)
		}
	}
	return lay, nil
}

func detectManagerIn(dir string) Manager {
	if isDir(filepath.Join(dir, ".husky")) {
		return Manager{Name: "Husky", Evidence: ".husky/"}
	}
	for _, f := range []string{"lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml"} {
		if isFile(filepath.Join(dir, f)) {
			return Manager{Name: "Lefthook", Evidence: f}
		}
	}
	for _, f := range []string{".pre-commit-config.yaml", ".pre-commit-config.yml"} {
		if isFile(filepath.Join(dir, f)) {
			return Manager{Name: "pre-commit", Evidence: f}
		}
	}
	return Manager{}
}

// wiringDetected reports whether our invocation is present in the manager's hook
// scripts/config — the "is it actually wired?" check that closes the silent no-op.
func wiringDetected(lay layout, mgr Manager, repoDir string) bool {
	top := repoDir
	if t, err := runGit(repoDir, "rev-parse", "--show-toplevel"); err == nil && t != "" {
		top = t
	}
	var candidates []string
	for _, name := range hookNames {
		candidates = append(candidates, filepath.Join(lay.HooksDir, name))
		if mgr.Name == "Husky" {
			candidates = append(candidates, filepath.Join(top, ".husky", name))
		}
	}
	if mgr.Evidence != "" {
		candidates = append(candidates, filepath.Join(top, mgr.Evidence))
	}
	return wiringDetectedIn(candidates)
}

func wiringDetectedIn(paths []string) bool {
	for _, p := range paths {
		if body, ok := readFile(p); ok {
			if strings.Contains(body, "aispend trailer") ||
				strings.Contains(body, "aispend consume") ||
				strings.Contains(body, markerSentinel) {
				return true
			}
		}
	}
	return false
}

// hookScript is the shim written for a hook. It is fail-open by construction: it
// only calls aispend when present, swallows any error (|| true), and exits 0 so a
// trailer problem can never abort the user's commit.
func hookScript(name, bin string) string {
	var verb string
	switch name {
	case "prepare-commit-msg":
		verb = `trailer "$1" --source "${2:-}"`
	case "post-commit":
		verb = `consume`
	}
	return strings.Join([]string{
		"#!/bin/sh",
		markerLine,
		"# " + name + ": records the API-equivalent cost of Claude Code / Codex activity as a git trailer.",
		"# Fail-open by design — a missing or failing aispend never blocks your commit.",
		"# Resolve aispend: $AISPEND_BIN, then the install-time path, then PATH, then repo-local ./aispend.",
		`bin="${AISPEND_BIN:-}"`,
		`[ -x "$bin" ] || bin=` + shellQuote(bin),
		`[ -x "$bin" ] || bin="$(command -v aispend 2>/dev/null)"`,
		`[ -x "$bin" ] || bin="$(git rev-parse --show-toplevel 2>/dev/null)/aispend"`,
		`[ -x "$bin" ] && "$bin" ` + verb + ` || true`,
		"exit 0",
		"",
	}, "\n")
}

// shellQuote single-quotes s for safe embedding in a /bin/sh script (a path can
// contain spaces; a stray quote is escaped).
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// aispendBinary is the absolute path of the running binary, embedded into the hook so
// it fires even when `aispend` isn't on PATH. Falls back to the bare name (PATH
// lookup) if the path can't be resolved.
func aispendBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "aispend"
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

func isOurHook(content string) bool { return strings.Contains(content, markerSentinel) }

func wiringLines() []string {
	return []string{
		`    prepare-commit-msg:  aispend trailer "$1" --source "${2:-}" || true`,
		`    post-commit:         aispend consume || true`,
	}
}

func managerName(m Manager) string {
	if m.Name == "" {
		return "a hook manager"
	}
	return m.Name
}

// runGit runs `git -C dir <args...>` and returns trimmed stdout. Local-only, no
// network — the same git-binary seam as internal/vcs/numstat.go.
func runGit(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	return strings.TrimSpace(string(out)), err
}

func readFile(p string) (string, bool) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func isDir(p string) bool  { fi, err := os.Stat(p); return err == nil && fi.IsDir() }
func isFile(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }
