package cli

import (
	"os"
	"strings"
	"testing"
	"time"
)

// arbitrageLine must say the honest thing in both directions: a saving when cache
// reads beat the no-cache bill, and an *added* cost when 1-hour cache writes (2×
// input) exceed what not caching would have cost.
func TestArbitrageLine(t *testing.T) {
	if got := arbitrageLine(10_000_000, 8_400_000); got != "without cache ≈ $10.00 · saved 84%" {
		t.Errorf("positive savings = %q", got)
	}
	if got := arbitrageLine(5_000_000, -5_000_000); !strings.Contains(got, "cost +100%") || !strings.Contains(got, "exceeded") {
		t.Errorf("negative savings should read as an added cost, got %q", got)
	}
}

func TestHumanizeDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-time.Second, "0s"},
		{30 * time.Second, "30s"},
		{42 * time.Minute, "42m"},
		{90 * time.Minute, "1h30m"},
		{26 * time.Hour, "1d2h"},
	}
	for _, c := range cases {
		if got := humanizeDuration(c.d); got != c.want {
			t.Errorf("humanizeDuration(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

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
