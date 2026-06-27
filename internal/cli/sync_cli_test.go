package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `aispend sync` brings the ledger current on demand: on a never-scanned home it imports
// the on-disk sessions and reports the count; a second run finds nothing new.
func TestSync_ImportsThenIdempotent(t *testing.T) {
	home := setupHome(t)

	out, errs, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync exit %d, stderr=%s", code, errs)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("sync should import the 2 fixture turns, ledger has %d", n)
	}
	if !strings.Contains(out, "synced 2 new turns") {
		t.Errorf("sync should report the import count:\n%s", out)
	}

	out2, _, code2 := run(t, "sync")
	if code2 != 0 {
		t.Fatalf("second sync exit %d", code2)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("second sync must not double-import; ledger has %d", n)
	}
	if !strings.Contains(out2, "up to date") {
		t.Errorf("a second sync should report nothing new:\n%s", out2)
	}
}

// "If a sync is already running, do nothing": with a fresh lock held (e.g. the daemon
// mid-cycle), `aispend sync` imports nothing and says a sync is already running, exit 0.
func TestSync_NoopWhenAlreadyRunning(t *testing.T) {
	home := setupHome(t)
	appHome := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appHome, "sync.lock"), []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errs, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync exit %d, stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "already running") {
		t.Errorf("sync should report that a sync is already running:\n%s", errs)
	}
	if n := storedCount(t, home); n != 0 {
		t.Errorf("a skipped sync must import nothing; ledger has %d", n)
	}
}

// The daemon shares the sync lock: `daemon --once` with a sync already running skips its
// cycle (imports nothing) and says so — so the daemon and `aispend sync` never double-scan.
func TestDaemonOnce_SkipsWhenSyncRunning(t *testing.T) {
	home := setupHome(t)
	appHome := filepath.Join(home, ".aispend")
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appHome, "sync.lock"), []byte("held"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errs, code := run(t, "daemon", "--once")
	if code != 0 {
		t.Fatalf("daemon --once exit %d, stderr=%s", code, errs)
	}
	if !strings.Contains(errs, "already running") {
		t.Errorf("a held lock should make --once skip and say so:\n%s", errs)
	}
	if n := storedCount(t, home); n != 0 {
		t.Errorf("a skipped --once cycle must import nothing; ledger has %d", n)
	}
}

// The sync summary is on stdout (so it stays pipe-clean and matches `scan`); a no-op
// "already running" notice is a diagnostic on stderr.
func TestSync_SummaryOnStdout(t *testing.T) {
	setupHome(t)
	out, errs, code := run(t, "sync")
	if code != 0 {
		t.Fatalf("sync exit %d", code)
	}
	if !strings.Contains(out, "synced") {
		t.Errorf("the summary should be on stdout:\n%s", out)
	}
	if strings.Contains(errs, "synced") {
		t.Errorf("the summary should not leak onto stderr:\n%s", errs)
	}
}
