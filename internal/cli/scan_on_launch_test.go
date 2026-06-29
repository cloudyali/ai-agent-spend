package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudyali/ai-agent-spend/internal/store"
)

// storedCount opens the ledger under a test HOME and returns how many events it holds
// — the side-effect we assert scan-on-launch on (did the read command import or not?).
func storedCount(t *testing.T, home string) int {
	t.Helper()
	st, err := store.OpenFileStore(filepath.Join(home, ".aispend", "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := st.Query(store.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return len(evs)
}

func writeAppConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A read command brings the ledger current first: `report` on a never-scanned home
// imports the sessions on disk, renders them, and announces the scan on STDERR.
func TestScanOnLaunch_ReportScansFirst(t *testing.T) {
	home := setupHome(t) // fixture session present; deliberately NOT scanned
	out, errs, code := run(t, "report", "--period", "all")
	if code != 0 {
		t.Fatalf("report exit %d, stderr=%s", code, errs)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("scan-on-launch should have imported the 2 fixture turns, ledger has %d", n)
	}
	if !strings.Contains(errs, "scanned") || !strings.Contains(errs, "2") {
		t.Errorf("expected a scan notice on stderr, got: %q", errs)
	}
	if !strings.Contains(out, "(2 events)") {
		t.Errorf("report should render the just-scanned events:\n%s", out)
	}
}

// `--no-scan` reads the ledger exactly as-is: no import, no notice. On a never-scanned
// home that means the empty-state guidance, proving the scan was skipped.
func TestScanOnLaunch_NoScanFlagSkips(t *testing.T) {
	home := setupHome(t)
	out, errs, code := run(t, "report", "--no-scan", "--period", "all")
	if code != 0 {
		t.Fatalf("report exit %d", code)
	}
	if n := storedCount(t, home); n != 0 {
		t.Errorf("--no-scan must not import; ledger has %d", n)
	}
	if strings.Contains(errs, "scanned") {
		t.Errorf("--no-scan must not print a scan notice, got: %q", errs)
	}
	if !strings.Contains(out, "aispend scan") {
		t.Errorf("empty ledger should still point at `aispend scan`:\n%s", out)
	}
}

// `scan_on_launch = false` in config.toml disables the launch scan for every read
// command (here `today`), without needing the per-invocation flag.
func TestScanOnLaunch_ConfigFalseDisables(t *testing.T) {
	home := setupHome(t)
	writeAppConfig(t, home, "scan_on_launch = false\n")
	_, errs, code := run(t, "today")
	if code != 0 {
		t.Fatalf("today exit %d", code)
	}
	if n := storedCount(t, home); n != 0 {
		t.Errorf("scan_on_launch=false must not import; ledger has %d", n)
	}
	if strings.Contains(errs, "scanned") {
		t.Errorf("config off → no scan notice, got: %q", errs)
	}
}

// The AISPEND_NO_SCAN env var is the scripting escape hatch — same effect as the flag,
// applied globally (useful in CI / tight loops).
func TestScanOnLaunch_EnvDisables(t *testing.T) {
	home := setupHome(t)
	t.Setenv("AISPEND_NO_SCAN", "1")
	_, errs, code := run(t, "report", "--period", "all")
	if code != 0 {
		t.Fatalf("report exit %d", code)
	}
	if n := storedCount(t, home); n != 0 {
		t.Errorf("AISPEND_NO_SCAN=1 must not import; ledger has %d", n)
	}
	if strings.Contains(errs, "scanned") {
		t.Errorf("env off → no scan notice, got: %q", errs)
	}
}

// When nothing new is on disk (already scanned), the launch scan stays silent — a
// fresh ledger prints no notice, so the common case adds no noise.
func TestScanOnLaunch_SilentWhenNothingNew(t *testing.T) {
	home := setupHome(t)
	if _, _, code := run(t, "scan"); code != 0 {
		t.Fatalf("initial scan exit %d", code)
	}
	_, errs, code := run(t, "today")
	if code != 0 {
		t.Fatalf("today exit %d", code)
	}
	if strings.Contains(errs, "scanned") {
		t.Errorf("nothing new → silent, but got notice: %q", errs)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("ledger should still hold the 2 events, has %d", n)
	}
}

// The notice is grammatical: exactly one imported turn reads "1 new turn", not "turns".
func TestScanOnLaunch_SingularNotice(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv("AISPEND_NO_SCAN", "")
	dir := filepath.Join(home, ".claude", "projects", "solo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oneTurn := `{"type":"user","sessionId":"s","message":{"role":"user","content":[]}}
{"type":"assistant","uuid":"a1","sessionId":"s","timestamp":"2026-06-14T10:00:05Z","cwd":"/x/solo","message":{"id":"m1","model":"claude-opus-4-20250514","content":[{"type":"tool_use","name":"Edit"}],"usage":{"input_tokens":1200,"output_tokens":300}}}
`
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(oneTurn), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errs, code := run(t, "report", "--period", "all")
	if code != 0 {
		t.Fatalf("report exit %d", code)
	}
	if !strings.Contains(errs, "1 new turn") || strings.Contains(errs, "turns") {
		t.Errorf("expected the singular 'scanned 1 new turn', got: %q", errs)
	}
}

// The scan notice must go to STDERR, never STDOUT — so `report --json` stays a clean,
// pipe-safe payload even when a launch scan ran.
func TestScanOnLaunch_NoticeOffStdoutForJSON(t *testing.T) {
	home := setupHome(t)
	out, errs, code := run(t, "report", "--period", "all", "--json")
	if code != 0 {
		t.Fatalf("report --json exit %d", code)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("json report should also scan-on-launch; ledger has %d", n)
	}
	if strings.Contains(out, "scanned") {
		t.Errorf("stdout JSON must not carry the scan notice:\n%s", out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "{") {
		t.Errorf("stdout should be clean JSON:\n%s", out)
	}
	if !strings.Contains(errs, "scanned") {
		t.Errorf("the notice belongs on stderr, got: %q", errs)
	}
}
