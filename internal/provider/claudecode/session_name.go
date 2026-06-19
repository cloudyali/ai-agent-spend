// Session naming: recover a human label for a session by reading its original
// .jsonl on demand — Claude Code's auto-generated summary title when present, else
// the first human prompt. Like the prompt re-read, AgentSpend stores no title and
// hashes paths, so the name is recovered live from the user's own log; nothing new is
// persisted. Local, read-only (no network) — the offline promise is untouched.
package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"strings"
)

// SessionName returns a human label for the session whose log is at path: Claude
// Code's auto-generated `summary` title when present (it wins, even though it sits at
// the end of the file), else the first human prompt. ok is false — never an error —
// when the file can't be read or carries neither, so the caller degrades gracefully.
func SessionName(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	// bufio.Reader (not Scanner) tolerates the very long lines real sessions embed.
	r := bufio.NewReader(f)
	firstPrompt, haveFirst := "", false
	for {
		line, rerr := r.ReadBytes('\n')
		if t := bytes.TrimSpace(line); len(t) > 0 {
			if s, ok := summaryText(t); ok {
				return s, true // the auto-title wins outright
			}
			if !haveFirst {
				if txt, ok := userPromptText(t); ok {
					firstPrompt, haveFirst = txt, true
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	if haveFirst {
		return firstPrompt, true
	}
	return "", false
}

// summaryText extracts the title from a Claude Code summary line
// (`{"type":"summary","summary":"..."}`); ok is false for any other line.
func summaryText(raw []byte) (string, bool) {
	var rec struct {
		Type    string `json:"type"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", false
	}
	if rec.Type == "summary" {
		if s := strings.TrimSpace(rec.Summary); s != "" {
			return s, true
		}
	}
	return "", false
}
