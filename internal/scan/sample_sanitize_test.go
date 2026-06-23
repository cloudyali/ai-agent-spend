package scan

import (
	"strings"
	"testing"
)

// sampleOf feeds `scan --verbose`, which prints the sample straight to the TTY, so
// a crafted log line must not smuggle a terminal escape through it. The previous
// scrubber only blanked C0 (<0x20), letting DEL and the UTF-8-encoded C1 controls
// (e.g. U+009B, the C1 CSI introducer) survive — this guards that gap (CWE-150).
func TestSampleOf_StripsControlAndC1(t *testing.T) {
	// Build the raw line explicitly so the control bytes are unambiguous.
	raw := []byte("ok ")
	raw = append(raw, 0x1b, '[', '2', 'J', ' ') // ESC CSI screen-clear (C0)
	raw = append(raw, 0x7f, ' ')                // DEL
	raw = append(raw, 0xc2, 0x9b, ' ')          // U+009B, the C1 CSI introducer (UTF-8)
	raw = append(raw, "end"...)

	got := sampleOf(raw)
	for _, r := range got {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			t.Fatalf("sampleOf leaked control rune %#x: %q", r, got)
		}
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "end") {
		t.Fatalf("sampleOf dropped printable content: %q", got)
	}
}
