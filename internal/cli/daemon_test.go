package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
	"github.com/cloudyali/ai-agent-spend/internal/platform"
)

// lockedBuffer is a concurrency-safe writer for tests that read a daemon's stderr
// while the loop goroutine is still writing to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitFor polls cond until true or the timeout fires (deterministic without sleeps in
// the assertion path).
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// Daemon-loop tests reuse the package's fixedNow clock; the heartbeat timestamp is
// never asserted (it renders in local tz), only the "scanned N new turn(s)" payload.

// The loop scans once immediately (catch-up, no waiting a full interval), then once
// per tick. Driving the tick channel synchronously makes call accounting exact:
// after K synchronous sends + cancel + join, scan ran exactly K+1 times.
func TestDaemonLoop_ScansImmediatelyThenPerTick(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	ns := []int{0, 2, 1} // catch-up imports nothing, tick1 imports 2, tick2 imports 1
	calls := 0
	scan := func() int { n := ns[calls]; calls++; return n }

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() { daemonLoop(ctx, ticks, scan, &buf, fixedNow, false); close(done) }()

	ticks <- time.Time{} // tick 1 (blocks until the loop is back in select after catch-up)
	ticks <- time.Time{} // tick 2
	cancel()
	<-done

	if calls != 3 {
		t.Fatalf("scan calls = %d, want 3 (1 catch-up + 2 ticks)", calls)
	}
	out := buf.String()
	if !strings.Contains(out, "scanned 2 new turns") {
		t.Errorf("expected the plural notice for the 2-turn tick:\n%s", out)
	}
	if !strings.Contains(out, "scanned 1 new turn\n") || strings.Contains(out, "1 new turns") {
		t.Errorf("expected the singular notice for the 1-turn tick:\n%s", out)
	}
}

// A cycle that imports nothing is silent by default (no log spam on an idle machine),
// but `--verbose` emits a heartbeat so a watching operator sees the loop is alive.
func TestDaemonLoop_QuietWhenNothingNew(t *testing.T) {
	run := func(verbose bool) string {
		ctx, cancel := context.WithCancel(context.Background())
		ticks := make(chan time.Time)
		scan := func() int { return 0 }
		var buf bytes.Buffer
		done := make(chan struct{})
		go func() { daemonLoop(ctx, ticks, scan, &buf, fixedNow, verbose); close(done) }()
		ticks <- time.Time{}
		cancel()
		<-done
		return buf.String()
	}

	if got := run(false); strings.Contains(got, "scanned") || strings.Contains(got, "no new") {
		t.Errorf("non-verbose idle loop must stay silent, got: %q", got)
	}
	if got := run(true); !strings.Contains(got, "no new turns") {
		t.Errorf("verbose idle loop should heartbeat, got: %q", got)
	}
}

// Cancelling the context returns promptly without consuming a tick — only the
// immediate catch-up scan ran.
func TestDaemonLoop_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time) // never fed
	calls := 0
	scan := func() int { calls++; return 0 }
	done := make(chan struct{})
	go func() { daemonLoop(ctx, ticks, scan, io.Discard, fixedNow, false); close(done) }()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("daemonLoop did not return within 2s of cancel")
	}
	if calls != 1 {
		t.Errorf("scan calls = %d, want 1 (catch-up only)", calls)
	}
}

// runDaemon wires the real ticker to incrementalScan: a freshly started daemon on an
// unscanned home imports the on-disk sessions immediately (the catch-up cycle),
// advancing the checkpoint, and prints a startup + stopped banner on stderr.
func TestRunDaemon_CatchUpScanImportsAndCheckpoints(t *testing.T) {
	home := setupHome(t) // fixture session present, deliberately unscanned
	t.Setenv("AISPEND_NO_REFRESH", "1")
	var errb bytes.Buffer
	a := &App{Resolver: platform.Detect(), Now: nowUTC, Out: io.Discard, Err: &errb}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { a.runDaemon(ctx, 5*time.Millisecond, false); close(done) }()

	// The catch-up scan runs at once; wait for it to land the 2 fixture turns.
	deadline := time.After(3 * time.Second)
	for storedCount(t, home) < 2 {
		select {
		case <-deadline:
			cancel()
			t.Fatalf("catch-up scan did not import fixture turns; ledger=%d", storedCount(t, home))
		case <-time.After(5 * time.Millisecond):
		}
	}
	cancel()
	<-done

	if n := storedCount(t, home); n != 2 {
		t.Errorf("daemon should have imported 2 fixture turns, ledger has %d", n)
	}
	out := errb.String()
	if !strings.Contains(out, "scanning every 5ms") {
		t.Errorf("missing startup banner with interval:\n%s", out)
	}
	if !strings.Contains(out, "scanned 2 new turns") {
		t.Errorf("catch-up scan should announce the import:\n%s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("missing shutdown line:\n%s", out)
	}
}

// Interval resolution precedence: an explicit --interval flag wins; otherwise the
// config scan_interval; otherwise the 5m default. A negative flag is rejected (the
// daemon would panic on a non-positive ticker) and falls back to the default + error.
func TestResolveScanInterval(t *testing.T) {
	newApp := func(t *testing.T, config string) *App {
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("AISPEND_HOME", "")
		t.Setenv("CLAUDE_CONFIG_DIR", "")
		if config != "" {
			writeAppConfig(t, home, config)
		}
		return &App{Resolver: platform.Detect(), Now: nowUTC, Out: io.Discard, Err: io.Discard}
	}

	t.Run("flag wins over config", func(t *testing.T) {
		a := newApp(t, "scan_interval = 20m\n")
		got, err := a.resolveScanInterval(30 * time.Minute)
		if err != nil || got != 30*time.Minute {
			t.Fatalf("got %v, err %v; want 30m, nil", got, err)
		}
	})
	t.Run("config used when flag unset", func(t *testing.T) {
		a := newApp(t, "scan_interval = 20m\n")
		got, err := a.resolveScanInterval(0)
		if err != nil || got != 20*time.Minute {
			t.Fatalf("got %v, err %v; want 20m, nil", got, err)
		}
	})
	t.Run("default when neither set", func(t *testing.T) {
		a := newApp(t, "")
		got, err := a.resolveScanInterval(0)
		if err != nil || got != config.DefaultScanInterval {
			t.Fatalf("got %v, err %v; want %v, nil", got, err, config.DefaultScanInterval)
		}
	})
	t.Run("invalid config falls back to default with error", func(t *testing.T) {
		a := newApp(t, "scan_interval = soon\n")
		got, err := a.resolveScanInterval(0)
		if err == nil || got != config.DefaultScanInterval {
			t.Fatalf("got %v, err %v; want %v, non-nil err", got, err, config.DefaultScanInterval)
		}
	})
	t.Run("negative flag rejected", func(t *testing.T) {
		a := newApp(t, "")
		got, err := a.resolveScanInterval(-5 * time.Minute)
		if err == nil || got != config.DefaultScanInterval {
			t.Fatalf("got %v, err %v; want %v, non-nil err", got, err, config.DefaultScanInterval)
		}
	})
}

// `aispend daemon --once` runs a single incremental cycle and exits 0 — the
// cron/launchd-friendly entrypoint. On an unscanned home it imports the fixture and
// advances the checkpoint; a second run finds nothing new and says so. No loop, so
// the command returns immediately.
func TestDaemonOnce_ScansThenIdempotent(t *testing.T) {
	home := setupHome(t)
	_, errs, code := run(t, "daemon", "--once")
	if code != 0 {
		t.Fatalf("daemon --once exit %d, stderr=%s", code, errs)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("--once should import the 2 fixture turns, ledger has %d", n)
	}
	if !strings.Contains(errs, "scanned 2 new turns") {
		t.Errorf("expected an import notice, got: %q", errs)
	}

	_, errs2, code2 := run(t, "daemon", "--once")
	if code2 != 0 {
		t.Fatalf("second daemon --once exit %d", code2)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("second --once must not double-import; ledger has %d", n)
	}
	if !strings.Contains(errs2, "no new turns") {
		t.Errorf("second run should report nothing new, got: %q", errs2)
	}
}

// A malformed --interval is a flag parse error → exit 2, before any loop is entered.
func TestDaemon_InvalidIntervalExits2(t *testing.T) {
	setupHome(t)
	_, _, code := run(t, "daemon", "--interval", "nope", "--once")
	if code != 2 {
		t.Errorf("bad --interval exit = %d, want 2", code)
	}
}

// The full daemon path: cmdDaemon resolves the interval, warns + falls back to the
// default on a bad config value, wires a signal context, runs the loop (catch-up scan
// imports the fixture), then exits cleanly when SIGTERM is delivered. The signal is
// sent only AFTER the startup banner appears — that banner is printed by runDaemon,
// which cmdDaemon calls strictly after signal.NotifyContext has registered the handler,
// so the SIGTERM is caught (never fatal to the test process). A bad scan_interval is
// used so the warn-and-default branch is exercised; the catch-up scan runs immediately
// regardless of the (defaulted, long) interval, so the test never waits for a tick.
func TestDaemon_RunsLoopThenStopsOnSIGTERM(t *testing.T) {
	home := setupHome(t)
	t.Setenv("AISPEND_NO_REFRESH", "1")
	writeAppConfig(t, home, "scan_interval = nonsense\n") // → warn + fall back to 5m default
	errb := &lockedBuffer{}
	a := &App{Resolver: platform.Detect(), Now: nowUTC, Out: io.Discard, Err: errb}

	done := make(chan int, 1)
	go func() { done <- a.cmdDaemon(nil) }() // no --interval → config → default

	// Banner ⟹ the signal handler is registered; catch-up scan ⟹ a cycle actually ran.
	waitFor(t, func() bool { return strings.Contains(errb.String(), "scanning every 5m") }, 3*time.Second)
	waitFor(t, func() bool { return storedCount(t, home) == 2 }, 3*time.Second)

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("kill self with SIGTERM: %v", err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("daemon exit = %d, want 0", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not stop within 3s of SIGTERM")
	}
	out := errb.String()
	if !strings.Contains(out, "scan_interval") || !strings.Contains(out, "using 5m") {
		t.Errorf("expected a warn + fallback notice for the bad interval:\n%s", out)
	}
	if !strings.Contains(out, "stopped") {
		t.Errorf("expected the shutdown line after SIGTERM:\n%s", out)
	}
}
