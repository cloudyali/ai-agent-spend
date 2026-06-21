package githook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- pure unit tests (no git binary needed) ---

func TestHookScript_AbsPathFailOpenAndMarker(t *testing.T) {
	bin := "/opt/aispend/aispend"
	pre := hookScript("prepare-commit-msg", bin)
	// Embeds the install-time absolute path (so it fires without aispend on PATH),
	// keeps a PATH fallback, and stays fail-open.
	for _, want := range []string{"#!/bin/sh", markerLine, bin, "trailer", `"$1"`, "command -v aispend", "exit 0"} {
		if !strings.Contains(pre, want) {
			t.Errorf("prepare-commit-msg script missing %q:\n%s", want, pre)
		}
	}
	if !strings.Contains(pre, "|| true") {
		t.Errorf("prepare-commit-msg must be fail-open (|| true):\n%s", pre)
	}
	post := hookScript("post-commit", bin)
	if !strings.Contains(post, "consume") || !strings.Contains(post, bin) || !strings.Contains(post, markerLine) {
		t.Errorf("post-commit script wrong:\n%s", post)
	}
}

func TestIsOurHook(t *testing.T) {
	if !isOurHook(hookScript("post-commit", "/x/aispend")) {
		t.Error("our own script must be recognized as ours")
	}
	foreign := "#!/bin/sh\nnpx husky run\n"
	if isOurHook(foreign) {
		t.Error("a foreign hook must NOT be recognized as ours")
	}
}

func TestDetectManagerIn(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  string
	}{
		{"husky", func(d string) { mkdir(t, filepath.Join(d, ".husky")) }, "Husky"},
		{"lefthook", func(d string) { writeFile(t, filepath.Join(d, "lefthook.yml"), "x") }, "Lefthook"},
		{"precommit", func(d string) { writeFile(t, filepath.Join(d, ".pre-commit-config.yaml"), "x") }, "pre-commit"},
		{"none", func(d string) {}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(dir)
			if got := detectManagerIn(dir).Name; got != tc.want {
				t.Errorf("detectManagerIn = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReport_ExitCode(t *testing.T) {
	// Only a genuine refusal (foreign hook we won't clobber) is non-zero; everything
	// else — including the managed path where we deliberately don't write — is success.
	cases := map[Kind]int{
		KindInstalled:    0,
		KindManaged:      0,
		KindUninstalled:  0,
		KindNotInstalled: 0,
		KindForeign:      0,
		KindRefused:      1,
	}
	for k, want := range cases {
		if got := (Report{Kind: k}).ExitCode(); got != want {
			t.Errorf("ExitCode(%s) = %d, want %d", k, got, want)
		}
	}
}

func TestReport_Render_RefusedAndManagedIncludeWiring(t *testing.T) {
	r := Report{Kind: KindRefused, Manager: Manager{Name: "Husky", Evidence: ".husky/"}}
	out := strings.Join(r.Render(), "\n")
	if !strings.Contains(out, "Husky") || !strings.Contains(out, "aispend trailer") {
		t.Errorf("refused render must name the manager and give paste-ready wiring:\n%s", out)
	}
	m := Report{Kind: KindManaged, Manager: Manager{Name: "Husky", Evidence: ".husky/"}}
	if !strings.Contains(strings.Join(m.Render(), "\n"), "aispend consume") {
		t.Errorf("managed render must include both shim invocations:\n%s", strings.Join(m.Render(), "\n"))
	}
}

func TestWiringDetectedIn(t *testing.T) {
	dir := t.TempDir()
	wired := filepath.Join(dir, "prepare-commit-msg")
	writeFile(t, wired, "#!/bin/sh\naispend trailer \"$1\" --source \"${2:-}\"\n")
	bare := filepath.Join(dir, "post-commit")
	writeFile(t, bare, "#!/bin/sh\nnpx lint-staged\n")
	if !wiringDetectedIn([]string{wired, filepath.Join(dir, "nope")}) {
		t.Error("must detect our invocation in a wired hook")
	}
	if wiringDetectedIn([]string{bare}) {
		t.Error("must NOT report wiring for a hook without our invocation")
	}
}

// --- real-git e2e (sandbox has git); skipped where git is absent ---

func TestInstall_PlainHooks(t *testing.T) {
	dir := newGitRepo(t)

	rep, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if rep.Kind != KindInstalled {
		t.Fatalf("Install kind = %s, want installed", rep.Kind)
	}
	for _, name := range hookNames {
		p := filepath.Join(dir, ".git", "hooks", name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("hook %s not written: %v", name, err)
		}
		if fi.Mode()&0o111 == 0 {
			t.Errorf("hook %s is not executable (mode %v)", name, fi.Mode())
		}
		body, _ := os.ReadFile(p)
		if !isOurHook(string(body)) {
			t.Errorf("hook %s lacks our marker", name)
		}
	}

	if st, _ := Status(dir); st.Kind != KindInstalled {
		t.Errorf("Status after install = %s, want installed", st.Kind)
	}

	un, err := Uninstall(dir)
	if err != nil || un.Kind != KindUninstalled || un.Removed != 2 {
		t.Fatalf("Uninstall = %+v err=%v, want uninstalled/removed 2", un, err)
	}
	for _, name := range hookNames {
		if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", name)); !os.IsNotExist(err) {
			t.Errorf("hook %s should be gone after uninstall", name)
		}
	}
	if st, _ := Status(dir); st.Kind != KindNotInstalled {
		t.Errorf("Status after uninstall = %s, want not_installed", st.Kind)
	}
}

func TestInstall_RefusesForeignHookAtomically(t *testing.T) {
	dir := newGitRepo(t)
	hooks := filepath.Join(dir, ".git", "hooks")
	mkdir(t, hooks)
	foreign := "#!/bin/sh\n# husky\nnpx lint-staged\n"
	writeFile(t, filepath.Join(hooks, "prepare-commit-msg"), foreign)

	rep, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if rep.Kind != KindRefused {
		t.Fatalf("Install over a foreign hook = %s, want refused", rep.Kind)
	}
	// The foreign hook is untouched...
	if body, _ := os.ReadFile(filepath.Join(hooks, "prepare-commit-msg")); string(body) != foreign {
		t.Error("foreign prepare-commit-msg must be left exactly as-is")
	}
	// ...and we did NOT write our post-commit (refuse is atomic — no partial install).
	if _, err := os.Stat(filepath.Join(hooks, "post-commit")); !os.IsNotExist(err) {
		t.Error("refuse must be atomic: post-commit must not be written when refusing")
	}
	if st, _ := Status(dir); st.Kind != KindForeign {
		t.Errorf("Status with a foreign hook = %s, want foreign", st.Kind)
	}
}

func TestInstall_CoreHooksPath_Managed(t *testing.T) {
	dir := newGitRepo(t)
	mkdir(t, filepath.Join(dir, ".husky"))
	gitCfg(t, dir, "core.hooksPath", ".husky/_")

	rep, err := Install(dir)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if rep.Kind != KindManaged {
		t.Fatalf("Install with core.hooksPath set = %s, want managed", rep.Kind)
	}
	if rep.Manager.Name != "Husky" {
		t.Errorf("manager = %q, want Husky", rep.Manager.Name)
	}
	// Crucial: no dead hook dropped into .git/hooks (git ignores it when hooksPath is set).
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "prepare-commit-msg")); !os.IsNotExist(err) {
		t.Error("managed install must NOT write a dead .git/hooks file")
	}
	if !strings.Contains(strings.Join(rep.Render(), "\n"), "aispend trailer") {
		t.Error("managed install must print paste-ready wiring")
	}

	// Before wiring: status says managed, not wired.
	if st, _ := Status(dir); st.Kind != KindManaged || st.Wired {
		t.Errorf("Status before wiring = %+v, want managed & not wired", st)
	}
	// Simulate the user wiring our line into the Husky hook.
	writeFile(t, filepath.Join(dir, ".husky", "prepare-commit-msg"),
		"#!/bin/sh\naispend trailer \"$1\" --source \"${2:-}\" || true\n")
	if st, _ := Status(dir); st.Kind != KindManaged || !st.Wired {
		t.Errorf("Status after wiring = %+v, want managed & wired", st)
	}
}

func TestInstall_Idempotent(t *testing.T) {
	dir := newGitRepo(t)
	if _, err := Install(dir); err != nil {
		t.Fatal(err)
	}
	rep, err := Install(dir)
	if err != nil || rep.Kind != KindInstalled {
		t.Fatalf("second Install = %+v err=%v, want installed (idempotent)", rep, err)
	}
}

func TestResolve_NotARepo(t *testing.T) {
	if _, err := Install(t.TempDir()); err == nil {
		t.Error("Install on a non-git dir must return an error")
	}
}

func TestReport_RenderAllKinds(t *testing.T) {
	cases := []struct {
		name string
		rep  Report
		want string
	}{
		{"installed", Report{Kind: KindInstalled, HooksDir: "/r/.git/hooks"}, "installed"},
		{"uninstalled", Report{Kind: KindUninstalled, Removed: 2, HooksDir: "/r/.git/hooks"}, "removed 2"},
		{"not_installed", Report{Kind: KindNotInstalled}, "not installed"},
		{"foreign", Report{Kind: KindForeign}, "aispend trailer"},
		{"managed_wired", Report{Kind: KindManaged, Manager: Manager{Name: "Lefthook"}, Wired: true}, "detected"},
		{"managed_unwired", Report{Kind: KindManaged}, "NOT detected"},
		{"unknown_passthrough", Report{Kind: Kind("weird")}, "weird"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := strings.Join(tc.rep.Render(), "\n")
			if !strings.Contains(out, tc.want) {
				t.Errorf("Render(%s) = %q, want substring %q", tc.name, out, tc.want)
			}
		})
	}
	// A managed report with no detected manager must fall back to the generic label.
	if out := strings.Join((Report{Kind: KindManaged}).Render(), "\n"); !strings.Contains(out, "a hook manager") {
		t.Errorf("unknown manager must render the generic label: %q", out)
	}
}

func TestUninstall_NothingToRemove(t *testing.T) {
	dir := newGitRepo(t)
	rep, err := Uninstall(dir)
	if err != nil || rep.Kind != KindNotInstalled || rep.Removed != 0 {
		t.Fatalf("Uninstall on a clean repo = %+v err=%v, want not_installed/0", rep, err)
	}
}

// --- test helpers ---

func newGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t", "-c", "init.defaultBranch=main"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	return dir
}

func gitCfg(t *testing.T, dir, key, val string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", dir, "config", key, val).CombinedOutput(); err != nil {
		t.Fatalf("git config %s: %v\n%s", key, err, out)
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
