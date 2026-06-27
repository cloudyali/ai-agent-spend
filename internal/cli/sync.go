package cli

import (
	"context"
	"flag"
	"fmt"
)

// cmdSync is the on-demand "bring me current now" command: a bounded price top-up (when
// the cache is stale) followed by an incremental ledger scan — the same pair the
// explorer's background sync runs, exposed as an explicit verb that mirrors the TUI's `s`
// key and the "synced …" freshness stamp.
//
// It is single-flight: it takes the shared sync lock first, and if a sync is already
// running (the daemon mid-cycle, or another `aispend sync`) it does nothing and says so,
// exit 0 — never a second concurrent writer. Offline-safe: the scan reads only local
// session logs, and the price refresh honors the same opt-outs as the read commands
// (--no-refresh, AISPEND_NO_REFRESH, refresh_on_launch=false) and is compiled out of the
// offline build. The summary is on stdout (pipe-clean, like `scan`); the no-op notice is
// a diagnostic on stderr.
func (a *App) cmdSync(args []string) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	noRefresh := fs.Bool("no-refresh", false, "skip the price refresh; bring the ledger current only")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Take the lock before doing anything, so "a sync is already running" truly does
	// nothing — no refresh, no scan.
	release, ok := a.acquireSyncLock()
	if !ok {
		fmt.Fprintln(a.Err, "a sync is already running — nothing to do")
		return 0
	}
	defer release()

	// Price top-up first (bounded so a slow network never hangs the command), then the
	// incremental ledger sync. Pricing is pure, so order changes no number.
	ctx, cancel := context.WithTimeout(context.Background(), launchRefreshBudget)
	defer cancel()
	a.refreshIfStale(ctx, *noRefresh)

	n := a.incrementalScan()
	if n > 0 {
		fmt.Fprintf(a.Out, "synced %d new %s\n", n, turnNoun(n))
	} else {
		fmt.Fprintln(a.Out, "already up to date — no new turns")
	}
	return 0
}
