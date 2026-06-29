//go:build !offline

package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/config"
)

// lastSyncTime returns the most recent per-provider scan watermark, so the TUI header
// can stamp ledger freshness. The newest provider wins; no scans yet → zero.
func TestLastSyncTime(t *testing.T) {
	home := t.TempDir()
	a := appWithHome(home, &strings.Builder{}, time.Now())

	if got := a.lastSyncTime(); !got.IsZero() {
		t.Errorf("no scans yet → zero sync time, got %s", got)
	}

	st, err := a.openStore()
	if err != nil {
		t.Fatal(err)
	}
	older := time.Date(2026, 6, 27, 10, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 27, 11, 30, 0, 0, time.UTC)
	if err := st.SetLastScan("claude_code", older); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLastScan("codex", newer); err != nil {
		t.Fatal(err)
	}
	if got := a.lastSyncTime(); !got.Equal(newer) {
		t.Errorf("lastSyncTime should be the newest watermark %s, got %s", newer, got)
	}
}

// The in-process sync is ON by default: without --watch the explorer still re-scans on
// the daemon cadence (5m); --watch opts into the 3s live tick.
func TestTuiSyncInterval(t *testing.T) {
	a := appWithHome(t.TempDir(), &strings.Builder{}, time.Now())
	if d := a.tuiSyncInterval(false); d != config.DefaultScanInterval {
		t.Errorf("default (no --watch) should sync on the 5m daemon cadence, got %s", d)
	}
	if d := a.tuiSyncInterval(true); d != 3*time.Second {
		t.Errorf("--watch should use the 3s live cadence, got %s", d)
	}
}
