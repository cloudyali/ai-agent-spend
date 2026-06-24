package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
)

// cmdDaemon runs the background scan loop: every interval it scans each detected
// provider incrementally from its last checkpoint, so the ledger stays current
// without a remembered `aispend scan`. It is offline-safe (local reads only; prices
// are not refreshed here) and exits cleanly on Ctrl-C / SIGTERM. `--once` runs a
// single cycle and exits — the entrypoint to wrap with cron or a launchd/systemd
// timer when you'd rather the OS own the schedule. The full re-read with a checkpoint
// reset stays `aispend scan --full`.
//
// The daemon is just a persistent caller of the same App.incrementalScan as
// scan-on-launch; it adds no new write path. Because the FileStore is whole-file,
// last-writer-wins across processes, a manual `scan` (or a read command's launch
// scan) running at the same instant can momentarily lose an update — but EventID
// upserts are idempotent, so the next cycle re-imports and reconverges. A
// multi-writer-safe store (design-documents Item 8) is the durable fix; until then,
// running the daemon does not require stopping other invocations.
func (a *App) cmdDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(a.Err)
	interval := fs.Duration("interval", 0, "scan cadence, e.g. 15m or 1h (default: scan_interval in config.toml, else 15m)")
	once := fs.Bool("once", false, "run a single scan cycle and exit (for cron/launchd-style external schedulers)")
	verbose := fs.Bool("verbose", false, "log every cycle, including cycles that imported nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *once {
		if n := a.incrementalScan(); n > 0 {
			fmt.Fprintf(a.Err, "scanned %d new %s\n", n, turnNoun(n))
		} else {
			fmt.Fprintln(a.Err, "no new turns since the last checkpoint")
		}
		return 0
	}

	d, err := a.resolveScanInterval(*interval)
	if err != nil {
		// Non-fatal: a bad interval source falls back to the safe default rather than
		// refusing to start a background scanner the user explicitly asked for.
		fmt.Fprintf(a.Err, "aispend: %v (using %s)\n", err, d)
	}

	// signal.NotifyContext cancels the loop on the first Ctrl-C / SIGTERM; a second
	// signal then hits the default handler, so the daemon is never unkillable.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a.runDaemon(ctx, d, *verbose)
	return 0
}

// resolveScanInterval picks the daemon cadence: an explicit positive --interval flag
// wins; a negative flag is rejected (a non-positive ticker would panic); otherwise
// (flag unset / zero) the config scan_interval, else the 15m default. On any error it
// returns the safe default alongside the error so the caller can warn and still run.
func (a *App) resolveScanInterval(flagVal time.Duration) (time.Duration, error) {
	switch {
	case flagVal > 0:
		return flagVal, nil
	case flagVal < 0:
		return config.DefaultScanInterval, fmt.Errorf("--interval must be positive, got %s", flagVal)
	default:
		return config.LoadScanInterval(a.Resolver.AppHome())
	}
}

// daemonLoop runs the background scan on a timer until ctx is cancelled. It scans
// once immediately — a freshly started daemon catches up without waiting a full
// interval — then once per tick from `tick` (a time.Ticker.C in production, a
// hand-driven channel in tests). scan returns the number of imported turns; a
// non-empty result is announced on w, and verbose additionally heartbeats the idle
// cycles so an operator can see the loop is alive. Cancelling ctx returns after the
// in-flight cycle completes (cycles are synchronous, so no scan is left half-done).
func daemonLoop(ctx context.Context, tick <-chan time.Time, scan func() int, w io.Writer, now func() time.Time, verbose bool) {
	cycle := func() {
		n := scan()
		switch {
		case n > 0:
			fmt.Fprintf(w, "[%s] scanned %d new %s\n", now().Local().Format("15:04:05"), n, turnNoun(n))
		case verbose:
			fmt.Fprintf(w, "[%s] no new turns\n", now().Local().Format("15:04:05"))
		}
	}
	cycle() // immediate catch-up so the first scan doesn't wait one interval
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			cycle()
		}
	}
}

// runDaemon drives daemonLoop with a real ticker at the chosen interval, scanning
// every detected provider incrementally from its last checkpoint. It is offline-safe
// — incrementalScan reads only local session logs and prices are not refreshed here —
// and returns when ctx is cancelled (Ctrl-C / SIGTERM). The startup and shutdown
// banners go to stderr so stdout stays clean. interval must be positive (cmdDaemon
// guarantees it); time.NewTicker would otherwise panic.
func (a *App) runDaemon(ctx context.Context, interval time.Duration, verbose bool) {
	fmt.Fprintf(a.Err, "aispend daemon: scanning every %s · incremental from the last checkpoint · press Ctrl-C to stop\n", interval)
	t := time.NewTicker(interval)
	defer t.Stop()
	daemonLoop(ctx, t.C, a.incrementalScan, a.Err, a.Now, verbose)
	fmt.Fprintln(a.Err, "aispend daemon: stopped")
}

// turnNoun pluralizes the per-scan turn count for the human-facing scan notices.
func turnNoun(n int) string {
	if n == 1 {
		return "turn"
	}
	return "turns"
}
