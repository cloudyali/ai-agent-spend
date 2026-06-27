package cli

import (
	"os"
	"path/filepath"
	"time"
)

// syncLockTTL bounds how long a sync lock is honored before it's treated as stale and
// stolen — far longer than any real sync (a price top-up is bounded to a couple seconds,
// an incremental scan is local and fast) so a live sync is never pre-empted, yet short
// enough that a crashed holder doesn't wedge syncing forever. Staleness is judged purely
// by the lock file's mtime, so there are no PID/liveness syscalls (cross-platform,
// offline-safe).
const syncLockTTL = 10 * time.Minute

// acquireSyncLock takes the advisory single-flight lock so two syncs never run at once.
// It returns a release func and ok=true when the caller holds the lock; ok=false when a
// fresh lock is already held — another sync is running, so the caller should do nothing.
//
// It is fail-open: if the lock can't be managed (an unwritable home, a transient I/O
// error), it returns a no-op release and ok=true and lets the sync proceed unguarded —
// blocking a sync on a bookkeeping glitch is worse than a rare concurrent scan, which the
// store's idempotent EventID upserts already reconcile. A lock older than syncLockTTL is
// stolen (a crashed/abandoned holder) so syncing self-heals.
func (a *App) acquireSyncLock() (release func(), ok bool) {
	home := a.Resolver.AppHome()
	_ = os.MkdirAll(home, 0o755) // best-effort; the create below reports any real failure
	path := filepath.Join(home, "sync.lock")

	// Steal a stale lock (older than the TTL → a crashed/abandoned holder).
	if fi, err := os.Stat(path); err == nil && a.Now().Sub(fi.ModTime()) > syncLockTTL {
		_ = os.Remove(path)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, false // a fresh lock is held → another sync is running
		}
		return func() {}, true // fail-open: can't manage the lock → proceed unguarded
	}
	_ = f.Close()
	return func() { _ = os.Remove(path) }, true
}

// guardedScan runs an incremental scan under the single-flight lock: if another sync is
// already running it does nothing and returns (0, false); otherwise it scans and returns
// (imported, true). Shared by `aispend sync` and the daemon so the two never double-scan.
// ponytail: thin wrapper over incrementalScan; the lock is the only added behavior.
func (a *App) guardedScan() (n int, ran bool) {
	release, ok := a.acquireSyncLock()
	if !ok {
		return 0, false
	}
	defer release()
	return a.incrementalScan(), true
}
