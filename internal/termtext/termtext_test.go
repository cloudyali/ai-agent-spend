package termtext

import (
	"strings"
	"testing"
)

// hasUnsafe reports whether s still carries a terminal-control byte: any C0
// control, DEL, or C1 control. allowWS lets the multiline check keep \n and \t.
func hasUnsafe(s string, allowWS bool) bool {
	for _, r := range s {
		if allowWS && (r == '\n' || r == '\t') {
			continue
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

func TestSanitizeLabel_StripsEscapeAndControls(t *testing.T) {
	// The audit's exploit payload: a branch name carrying an OSC title-set + screen
	// clear. ESC (0x1b), BEL (0x07), and CSI bytes must all be neutralized.
	payload := "main\x1b]0;PWNED\x07\x1b[2J"
	got := SanitizeLabel(payload)
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("ESC survived: %q", got)
	}
	if strings.ContainsRune(got, 0x07) {
		t.Fatalf("BEL survived: %q", got)
	}
	if hasUnsafe(got, false) {
		t.Fatalf("control byte survived: %q", got)
	}
	if !strings.HasPrefix(got, "main") {
		t.Fatalf("printable prefix lost: %q", got)
	}
}

func TestSanitizeLabel_ReplacesWithU_FFFD(t *testing.T) {
	got := SanitizeLabel("a\x1bb")
	want := "a�b"
	if got != want {
		t.Fatalf("SanitizeLabel(%q) = %q, want %q", "a\x1bb", got, want)
	}
}

func TestSanitizeLabel_StripsTabNewlineCR(t *testing.T) {
	// A single-line label must collapse to one line: tab, newline, CR are all
	// replaced (a label is never legitimately multi-line).
	for _, in := range []string{"a\tb", "a\nb", "a\rb"} {
		if hasUnsafe(SanitizeLabel(in), false) {
			t.Fatalf("whitespace control survived in label: %q -> %q", in, SanitizeLabel(in))
		}
	}
}

func TestSanitizeLabel_StripsDELandC1(t *testing.T) {
	// 0x7f DEL, 0x9b is the C1 CSI introducer some terminals honor directly.
	got := SanitizeLabel("x\x7fy\x9bz")
	if hasUnsafe(got, false) {
		t.Fatalf("DEL/C1 survived: %q", got)
	}
}

func TestSanitizeLabel_PreservesPrintableUnicode(t *testing.T) {
	for _, in := range []string{"", "main", "feat/login", "café", "日本語-branch", "a b  c"} {
		if got := SanitizeLabel(in); got != in {
			t.Fatalf("clean input mutated: SanitizeLabel(%q) = %q", in, got)
		}
	}
}

func TestSanitizeLabel_Idempotent(t *testing.T) {
	in := "main\x1b]0;x\x07 café\t日本"
	once := SanitizeLabel(in)
	if twice := SanitizeLabel(once); twice != once {
		t.Fatalf("not idempotent: %q -> %q", once, twice)
	}
}

func TestSanitizeMultiline_KeepsNewlinesAndTabs(t *testing.T) {
	in := "line one\n\tindented two\nline three"
	got := SanitizeMultiline(in)
	if got != in {
		t.Fatalf("multiline mutated clean text: %q -> %q", in, got)
	}
	if strings.Count(got, "\n") != 2 {
		t.Fatalf("newlines not preserved: %q", got)
	}
}

func TestSanitizeMultiline_StripsEscapeButNotStructure(t *testing.T) {
	in := "prompt line\n\x1b[31mred\x1b[0m\ttail\rCR"
	got := SanitizeMultiline(in)
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("ESC survived multiline: %q", got)
	}
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("CR survived multiline: %q", got)
	}
	if hasUnsafe(got, true) {
		t.Fatalf("control (other than \\n,\\t) survived: %q", got)
	}
	if !strings.Contains(got, "\n") || !strings.Contains(got, "\t") {
		t.Fatalf("newline/tab structure lost: %q", got)
	}
}

func TestSanitizeMultiline_PreservesUnicode(t *testing.T) {
	in := "héllo\n世界"
	if got := SanitizeMultiline(in); got != in {
		t.Fatalf("unicode mutated: %q -> %q", in, got)
	}
}
