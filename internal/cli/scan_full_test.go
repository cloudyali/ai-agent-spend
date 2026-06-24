package cli

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudyali/ai-agent-spend/internal/store"
)

// getLastScan reads a provider's stored checkpoint (watermark) under a test HOME.
func getLastScan(t *testing.T, home, provider string) time.Time {
	t.Helper()
	st, err := store.OpenFileStore(filepath.Join(home, ".aispend", "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	ts, err := st.LastScan(provider)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// Requirement: `scan --full` does a complete re-read (ignoring the watermark) AND
// advances the checkpoint to the latest, so a subsequent incremental scan reads
// nothing new. This locks the existing behavior (scan.Run sets the checkpoint
// unconditionally) against regression.
func TestScanFull_ReReadsAndResetsCheckpoint(t *testing.T) {
	home := setupHome(t)

	// First incremental scan: imports the fixture and sets the checkpoint.
	out, _, code := run(t, "scan")
	if code != 0 {
		t.Fatalf("scan exit %d", code)
	}
	if !strings.Contains(out, "Imported 2 events total") {
		t.Fatalf("first scan should import 2:\n%s", out)
	}
	st1 := getLastScan(t, home, "claude_code")
	if st1.IsZero() {
		t.Fatal("checkpoint should be set after the first scan")
	}

	// Full scan: re-reads everything (idempotent upserts keep the ledger at 2) and
	// pushes the checkpoint forward to the latest.
	out, _, code = run(t, "scan", "--full")
	if code != 0 {
		t.Fatalf("scan --full exit %d", code)
	}
	if !strings.Contains(out, "imported 2") {
		t.Errorf("--full should re-read all 2 turns, ignoring the watermark:\n%s", out)
	}
	if n := storedCount(t, home); n != 2 {
		t.Errorf("idempotent re-read should keep the ledger at 2, got %d", n)
	}
	st2 := getLastScan(t, home, "claude_code")
	if st2.Before(st1) {
		t.Errorf("--full must advance the checkpoint to latest: before=%v after=%v", st1, st2)
	}

	// A plain incremental scan now finds nothing newer than the reset checkpoint.
	out, _, code = run(t, "scan")
	if code != 0 {
		t.Fatalf("third scan exit %d", code)
	}
	if !strings.Contains(out, "Imported 0 events total") {
		t.Errorf("incremental scan after --full should import nothing new:\n%s", out)
	}
}
