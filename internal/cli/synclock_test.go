package cli

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// lockApp builds an App rooted at a fresh temp HOME, so the sync lock lands under an
// isolated ~/.aispend and never collides with another test.
func lockApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AISPEND_HOME", "")
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return &App{Resolver: platform.Detect(), Now: nowUTC, Out: io.Discard, Err: io.Discard}
}

// acquireSyncLock is the cross-process single-flight guard: the first caller holds the
// lock; while it is held a second caller is told the lock is taken (ok=false), so an
// on-demand sync can "do nothing if a sync is already running". Releasing frees it.
func TestAcquireSyncLock_SecondCallerBlockedUntilRelease(t *testing.T) {
	a := lockApp(t)

	release, ok := a.acquireSyncLock()
	if !ok {
		t.Fatal("the first caller should acquire the lock")
	}
	if _, ok2 := a.acquireSyncLock(); ok2 {
		t.Error("a second caller must be blocked while the lock is held")
	}
	release()

	release2, ok3 := a.acquireSyncLock()
	if !ok3 {
		t.Error("after release the lock should be acquirable again")
	}
	release2()
}

// A stale lock — older than the TTL, i.e. one left by a crashed sync — is stolen, so a
// dead holder never wedges syncing forever.
func TestAcquireSyncLock_StealsStaleLock(t *testing.T) {
	a := lockApp(t)

	// Plant a lock, then backdate it well past the TTL to simulate a crashed holder.
	if err := os.MkdirAll(a.Resolver.AppHome(), 0o755); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(a.Resolver.AppHome(), "sync.lock")
	if err := os.WriteFile(lock, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * syncLockTTL)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}

	release, ok := a.acquireSyncLock()
	if !ok {
		t.Fatal("a stale lock should be stolen, not honored")
	}
	release()
}

// guardedScan runs the scan under the lock and reports whether it ran. A second
// guardedScan while the lock is held does nothing and returns ran=false — the daemon and
// `aispend sync` therefore never double-scan.
func TestGuardedScan_SkipsWhenLockHeld(t *testing.T) {
	a := lockApp(t)
	// A fixture session lives under this HOME so a real scan has something to import.
	dir := filepath.Join(a.Resolver.Home, ".claude", "projects", "payments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sess.jsonl"), []byte(sessionJSONL), 0o644); err != nil {
		t.Fatal(err)
	}

	// Hold the lock → guardedScan must skip (ran=false, nothing imported).
	release, ok := a.acquireSyncLock()
	if !ok {
		t.Fatal("setup: should acquire the lock")
	}
	if n, ran := a.guardedScan(); ran || n != 0 {
		t.Errorf("guardedScan must skip while the lock is held; got n=%d ran=%v", n, ran)
	}
	if c := storedCount(t, a.Resolver.Home); c != 0 {
		t.Errorf("a skipped scan must import nothing; ledger has %d", c)
	}
	release()

	// Lock free → guardedScan runs and imports the fixture.
	n, ran := a.guardedScan()
	if !ran {
		t.Fatal("guardedScan should run when the lock is free")
	}
	if n != 2 {
		t.Errorf("guardedScan should import the 2 fixture turns, got %d", n)
	}
}
