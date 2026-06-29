package cli

import (
	"os"
	"testing"
)

func TestRoiStr(t *testing.T) {
	if got := roiStr(518.3); got != "518×" {
		t.Errorf("big ROI = %q, want 518×", got)
	}
	if got := roiStr(4.5); got != "4.5×" {
		t.Errorf("near-break-even ROI = %q, want 4.5×", got)
	}
	if got := roiStr(10); got != "10×" {
		t.Errorf("boundary ROI = %q, want 10×", got)
	}
}

func TestShortModel(t *testing.T) {
	if got := shortModel("claude-opus-4-8"); got != "opus-4-8" {
		t.Errorf("claude prefix should be trimmed, got %q", got)
	}
	if got := shortModel("gpt-5.3-codex"); got != "gpt-5.3-codex" {
		t.Errorf("non-claude model should pass through, got %q", got)
	}
	if got := shortModel(""); got != "(no model)" {
		t.Errorf("empty model = %q, want (no model)", got)
	}
}

// A regular file is not a character device, so isTTY is false — the path that
// keeps `aispend today > out.txt` plain.
func TestIsTTY_RegularFileIsNotATTY(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("a regular file must not be treated as a TTY")
	}
}
