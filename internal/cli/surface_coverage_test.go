package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// report --json emits one valid JSON document for a metered view (the emitReportJSON
// path), both ungrouped and grouped.
func TestReport_JSON(t *testing.T) {
	setupHome(t)
	run(t, "scan")

	out, _, code := run(t, "report", "--period", "all", "--json")
	if code != 0 {
		t.Fatalf("report --json exit %d", code)
	}
	if !json.Valid([]byte(out)) {
		t.Errorf("report --json should emit valid JSON:\n%s", out)
	}
	if out, _, code := run(t, "report", "--period", "all", "--json", "--by", "provider"); code != 0 || !json.Valid([]byte(out)) {
		t.Errorf("report --json --by provider: code=%d valid=%v\n%s", code, json.Valid([]byte(out)), out)
	}
}

// scan --full re-reads every session ignoring the watermark (the Full:true path) and
// still reports an import total.
func TestScan_Full(t *testing.T) {
	setupHome(t)
	if _, _, code := run(t, "scan"); code != 0 {
		t.Fatalf("initial scan failed")
	}
	if out, _, code := run(t, "scan", "--full"); code != 0 || !strings.Contains(out, "Imported") {
		t.Errorf("scan --full: code=%d out=%s", code, out)
	}
}

// top honors an explicit --limit and the --sessions view over a wide window.
func TestTop_Variants(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	if _, _, code := run(t, "top", "--limit", "1", "--period", "all"); code != 0 {
		t.Errorf("top --limit 1 failed: %d", code)
	}
	if out, _, code := run(t, "top", "--sessions", "--period", "all"); code != 0 || !strings.Contains(out, "sessions") {
		t.Errorf("top --sessions: code=%d out=%s", code, out)
	}
}

// today --no-scan renders from the ledger as-is (no scan-on-launch).
func TestToday_NoScan(t *testing.T) {
	setupHome(t)
	run(t, "scan")
	if out, _, code := run(t, "today", "--no-scan"); code != 0 || !strings.Contains(out, "today") {
		t.Errorf("today --no-scan: code=%d out=%s", code, out)
	}
}

// The fail-open trailer/consume hooks: every flag form parses and the hooks always exit
// 0, so a trailer problem can never block a commit.
func TestTrailerConsume_FailOpen(t *testing.T) {
	setupHome(t)   // hermetic store under a temp HOME
	run(t, "scan") // some ledger for pendingUsageLive to read
	repo := t.TempDir()
	// Enable trailers so cmdTrailer runs the full path, not just the disabled gate.
	if err := os.WriteFile(filepath.Join(repo, ".aispend.toml"), []byte("[trailers]\nenabled = true\ncost = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := filepath.Join(repo, "MSG")
	if err := os.WriteFile(msg, []byte("feat: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// space-separated flags
	if _, _, code := run(t, "trailer", msg, "--source", "claude_code", "--repo", repo); code != 0 {
		t.Errorf("trailer (space flags) must fail-open exit 0, got %d", code)
	}
	// = forms + an unknown flag that must be ignored (fail-open)
	if _, _, code := run(t, "trailer", msg, "--source=claude_code", "--repo="+repo, "--bogus"); code != 0 {
		t.Errorf("trailer (= flags) must fail-open exit 0, got %d", code)
	}
	// no message file → nothing to do, still exit 0
	if _, _, code := run(t, "trailer"); code != 0 {
		t.Errorf("trailer with no msg file must exit 0, got %d", code)
	}
	// consume, both flag forms
	if _, _, code := run(t, "consume", "--repo", repo); code != 0 {
		t.Errorf("consume --repo must exit 0, got %d", code)
	}
	if _, _, code := run(t, "consume", "--repo="+repo); code != 0 {
		t.Errorf("consume --repo= must exit 0, got %d", code)
	}
}
