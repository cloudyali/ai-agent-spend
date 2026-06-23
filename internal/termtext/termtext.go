// Package termtext neutralizes terminal control sequences in untrusted,
// session-derived strings before they are written to a TTY.
//
// aispend renders fields lifted verbatim from session logs — branch names, repo
// names, file paths, model ids, session titles, and the raw human prompt. A log is
// attacker-influenceable (a crafted .jsonl, a synced/shared ~/.claude or ~/.codex
// directory, or simply a branch created with control bytes:
//
//	git checkout -b $'main\e]0;PWNED\a\e[2J'
//
// because encoding/json decodes  into a literal ESC byte). Rendered raw, such
// a value can clear the screen, hide text, forge output, rewrite the window title,
// or — on emulators that honor response/clipboard sequences — inject a paste. This
// is terminal escape-sequence injection (CWE-150).
//
// The defense is to sanitize at the render boundary while leaving the stored value
// intact, so grouping/identity (which keys reports and matches turns to files) is
// unaffected. This supersedes the ad-hoc, scan-only scrubber in internal/scan
// (sampleOf), which never covered the report/TUI surfaces.
package termtext

import "strings"

// replacement is U+FFFD REPLACEMENT CHARACTER — one inert glyph per stripped byte,
// so tampering stays visible rather than silently vanishing.
const replacement = '�'

// isControl reports whether r is a C0 control, DEL, or C1 control — the bytes a
// terminal may interpret as the start of (or part of) an escape sequence.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// SanitizeLabel returns s with every C0 control (including ESC, BEL, CR, LF, TAB),
// DEL, and C1 control replaced by U+FFFD. The result is a single inert line, so it
// is safe to drop into a fixed-width column. Use for single-line labels: branch,
// repo, model, file path, session title, and report group keys.
func SanitizeLabel(s string) string {
	if !strings.ContainsFunc(s, isControl) {
		return s // common, clean path: no allocation
	}
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return replacement
		}
		return r
	}, s)
}

// SanitizeMultiline is like SanitizeLabel but preserves newline and tab, so
// genuinely multi-line untrusted text — the human prompt shown in the TUI's
// evidence viewport — keeps its line structure while ESC, CR, and every other
// control byte is still neutralized.
func SanitizeMultiline(s string) string {
	keep := func(r rune) bool { return r == '\n' || r == '\t' }
	if !strings.ContainsFunc(s, func(r rune) bool { return isControl(r) && !keep(r) }) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if keep(r) {
			return r
		}
		if isControl(r) {
			return replacement
		}
		return r
	}, s)
}
