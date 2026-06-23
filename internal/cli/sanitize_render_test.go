package cli

import (
	"testing"
)

// escPayload is the audit's exploit shape: an OSC window-title set + a screen
// clear, smuggled into a session-derived field via a JSON  in the log.
const escPayload = "main\x1b]0;PWNED\x07\x1b[2J"

// ctrlBytePresent reports whether s still carries a terminal-control byte (any C0
// control, DEL, or C1 control) — the raw material of escape-sequence injection.
func ctrlBytePresent(s string) bool {
	for _, r := range s {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			return true
		}
	}
	return false
}

// Every report --by group key flows through displayKey before it is printed, so
// a poisoned branch/repo/model/file/cost_tag/session/commit must come out inert.
func TestDisplayKey_SanitizesGroupKeys(t *testing.T) {
	for _, by := range []string{"branch", "repo", "model", "file", "cost_tag", "session", "commit"} {
		if got := displayKey(by, escPayload); ctrlBytePresent(got) {
			t.Errorf("displayKey(%q, payload) leaked a control byte: %q", by, got)
		}
	}
}

// shortModel is the CLI model choke point (top, modelList, top --sessions); an
// unknown model id is passed through, so it must be sanitized.
func TestShortModel_SanitizesModel(t *testing.T) {
	if got := shortModel("gpt\x1b]0;x\x07-5-codex"); ctrlBytePresent(got) {
		t.Errorf("shortModel leaked a control byte: %q", got)
	}
}

// The report "unpriced" line renders raw model ids (report.go uses e.Model, not
// shortModel) via topUnpriced, so the histogram keys must be sanitized.
func TestTopUnpriced_SanitizesModelNames(t *testing.T) {
	got := topUnpriced(map[string]int{escPayload: 3, "claude-opus-4-8": 1}, 5)
	if ctrlBytePresent(got) {
		t.Errorf("topUnpriced leaked a control byte: %q", got)
	}
}
